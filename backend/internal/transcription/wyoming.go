package transcription

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	wyomingprotocol "github.com/hujinrun/flowspace/internal/wyoming"
)

const (
	maxWyomingInputBytes = int64(64 * 1024 * 1024)
	wyomingChunkBytes    = 32 * 1024
)

var errUnsupportedPCMWave = errors.New("audio is not a 16-bit PCM WAVE file")

type DialContext func(context.Context, string, string) (net.Conn, error)

type WyomingConfig struct {
	Endpoint string
	Model    string
	Timeout  time.Duration
}

// WyomingTranscriber adapts stored voice-note audio to Wyoming's raw PCM TCP
// event stream.
type WyomingTranscriber struct {
	endpoint wyomingprotocol.Endpoint
	model    string
	timeout  time.Duration
	dial     DialContext
}

func NewWyomingTranscriber(cfg WyomingConfig, dial DialContext) (*WyomingTranscriber, error) {
	if dial == nil {
		return nil, errors.New("Wyoming TCP dialer is required")
	}
	endpoint, err := wyomingprotocol.ParseEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return &WyomingTranscriber{endpoint: endpoint, model: strings.TrimSpace(cfg.Model), timeout: timeout, dial: dial}, nil
}

func (t *WyomingTranscriber) Transcribe(ctx context.Context, input Input) (string, error) {
	pcm, err := prepareWyomingPCM(ctx, input)
	if err != nil {
		return "", err
	}
	defer pcm.Close()

	connection, err := t.dial(ctx, "tcp", t.endpoint.Address)
	if err != nil {
		return "", fmt.Errorf("connect Wyoming transcription service: %w", err)
	}
	defer connection.Close()
	stopCancellation := context.AfterFunc(ctx, func() { _ = connection.SetDeadline(time.Now()) })
	defer stopCancellation()
	deadline := time.Now().Add(t.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return "", fmt.Errorf("set Wyoming connection deadline: %w", err)
	}

	transcribeData := make(map[string]any)
	if t.model != "" {
		transcribeData["name"] = t.model
	}
	if language := wyomingLanguage(input.Language); language != "" {
		transcribeData["language"] = language
	}
	if err := wyomingprotocol.WriteEvent(connection, wyomingprotocol.Event{Type: "transcribe", Data: transcribeData}); err != nil {
		return "", err
	}
	format := map[string]any{"rate": pcm.rate, "width": pcm.width, "channels": pcm.channels}
	if err := wyomingprotocol.WriteEvent(connection, wyomingprotocol.Event{Type: "audio-start", Data: format}); err != nil {
		return "", err
	}
	buffer := make([]byte, wyomingChunkBytes)
	for {
		count, readErr := pcm.Read(buffer)
		if count > 0 {
			chunkFormat := map[string]any{"rate": pcm.rate, "width": pcm.width, "channels": pcm.channels}
			if err := wyomingprotocol.WriteEvent(connection, wyomingprotocol.Event{Type: "audio-chunk", Data: chunkFormat, Payload: buffer[:count]}); err != nil {
				return "", err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("read decoded transcription audio: %w", readErr)
		}
	}
	if err := wyomingprotocol.WriteEvent(connection, wyomingprotocol.Event{Type: "audio-stop"}); err != nil {
		return "", err
	}

	reader := bufio.NewReader(connection)
	var streamed strings.Builder
	for eventCount := 0; eventCount < 128; eventCount++ {
		event, err := wyomingprotocol.ReadEvent(reader)
		if err != nil {
			return "", fmt.Errorf("read Wyoming transcription response: %w", err)
		}
		switch event.Type {
		case "transcript":
			text, _ := event.Data["text"].(string)
			text = strings.TrimSpace(text)
			if text == "" {
				text = strings.TrimSpace(streamed.String())
			}
			if text == "" {
				return "", errors.New("Wyoming transcription response did not contain text")
			}
			return text, nil
		case "transcript-chunk":
			text, _ := event.Data["text"].(string)
			streamed.WriteString(text)
		case "transcript-stop":
			if text := strings.TrimSpace(streamed.String()); text != "" {
				return text, nil
			}
		case "error":
			return "", fmt.Errorf("Wyoming transcription service: %s", wyomingErrorMessage(event.Data))
		}
	}
	return "", errors.New("Wyoming transcription response did not complete")
}

func wyomingLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if separator := strings.IndexAny(language, "-_"); separator > 0 {
		language = language[:separator]
	}
	if language == "auto" {
		return ""
	}
	return language
}

func wyomingErrorMessage(data map[string]any) string {
	for _, key := range []string{"text", "message", "error"} {
		if message, ok := data[key].(string); ok && strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message)
		}
	}
	return "unknown error"
}

type pcmSource struct {
	file      *os.File
	reader    io.Reader
	rate      int
	width     int
	channels  int
	temporary string
}

func (s *pcmSource) Read(buffer []byte) (int, error) {
	return s.reader.Read(buffer)
}

func (s *pcmSource) Close() error {
	var closeErr error
	if s.file != nil {
		closeErr = s.file.Close()
	}
	if s.temporary != "" {
		if removeErr := os.RemoveAll(s.temporary); closeErr == nil {
			closeErr = removeErr
		}
	}
	return closeErr
}

func prepareWyomingPCM(ctx context.Context, input Input) (*pcmSource, error) {
	if input.Audio == nil {
		return nil, errors.New("transcription audio is required")
	}
	temporary, err := os.MkdirTemp("", "flowspace-wyoming-")
	if err != nil {
		return nil, fmt.Errorf("create audio conversion directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(temporary) }
	extension := supportedAudioExtension(input.Filename, input.ContentType)
	inputPath := filepath.Join(temporary, "input"+extension)
	inputFile, err := os.OpenFile(inputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create audio conversion input: %w", err)
	}
	written, copyErr := io.Copy(inputFile, io.LimitReader(input.Audio, maxWyomingInputBytes+1))
	closeErr := inputFile.Close()
	if copyErr != nil {
		cleanup()
		return nil, fmt.Errorf("stage transcription audio: %w", copyErr)
	}
	if closeErr != nil {
		cleanup()
		return nil, fmt.Errorf("close transcription audio: %w", closeErr)
	}
	if written > maxWyomingInputBytes {
		cleanup()
		return nil, errors.New("transcription audio exceeds the Wyoming adapter size limit")
	}

	pcm, err := openPCM16Wave(inputPath, temporary)
	if err == nil {
		return pcm, nil
	}
	if !errors.Is(err, errUnsupportedPCMWave) {
		cleanup()
		return nil, err
	}
	outputPath := filepath.Join(temporary, "decoded.wav")
	if err := convertAudioToPCM16Wave(ctx, inputPath, outputPath); err != nil {
		cleanup()
		return nil, err
	}
	pcm, err = openPCM16Wave(outputPath, temporary)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("open converted transcription audio: %w", err)
	}
	return pcm, nil
}

func supportedAudioExtension(filename, contentType string) string {
	extension := strings.ToLower(filepath.Ext(filepath.Base(strings.TrimSpace(filename))))
	switch extension {
	case ".m4a", ".mp4", ".aac", ".mp3", ".wav", ".caf", ".aif", ".aiff":
		return extension
	}
	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "audio/m4a", "audio/x-m4a":
		return ".m4a"
	case "audio/mp4":
		return ".mp4"
	case "audio/aac":
		return ".aac"
	case "audio/mpeg":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	}
	return ".audio"
}

func convertAudioToPCM16Wave(ctx context.Context, inputPath, outputPath string) error {
	if ffmpeg, err := exec.LookPath("ffmpeg"); err == nil {
		command := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", inputPath, "-vn", "-c:a", "pcm_s16le", "-f", "wav", outputPath)
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("decode transcription audio with ffmpeg: %w: %s", err, boundedCommandOutput(output))
		}
		return nil
	}
	if runtime.GOOS == "darwin" {
		if afconvert, err := exec.LookPath("afconvert"); err == nil {
			command := exec.CommandContext(ctx, afconvert, inputPath, "-o", outputPath, "-f", "WAVE", "-d", "LEI16")
			if output, err := command.CombinedOutput(); err != nil {
				return fmt.Errorf("decode transcription audio with afconvert: %w: %s", err, boundedCommandOutput(output))
			}
			return nil
		}
	}
	return errors.New("Wyoming transcription requires ffmpeg (or afconvert on macOS) to decode compressed audio")
}

func boundedCommandOutput(output []byte) string {
	const maxBytes = 4096
	if len(output) > maxBytes {
		output = output[:maxBytes]
	}
	return strings.TrimSpace(string(output))
}

func openPCM16Wave(path, temporary string) (*pcmSource, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*pcmSource, error) {
		_ = file.Close()
		return nil, err
	}
	header := make([]byte, 12)
	if _, err := io.ReadFull(file, header); err != nil || string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return fail(errUnsupportedPCMWave)
	}
	var rate, width, channels int
	var dataOffset, dataLength int64
	for {
		chunkHeader := make([]byte, 8)
		if _, err := io.ReadFull(file, chunkHeader); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return fail(fmt.Errorf("read WAVE chunk header: %w", err))
		}
		chunkSize := int64(binary.LittleEndian.Uint32(chunkHeader[4:]))
		chunkStart, err := file.Seek(0, io.SeekCurrent)
		if err != nil {
			return fail(err)
		}
		switch string(chunkHeader[:4]) {
		case "fmt ":
			if chunkSize < 16 || chunkSize > 1024*1024 {
				return fail(errUnsupportedPCMWave)
			}
			format := make([]byte, chunkSize)
			if _, err := io.ReadFull(file, format); err != nil {
				return fail(fmt.Errorf("read WAVE format: %w", err))
			}
			formatTag := binary.LittleEndian.Uint16(format[0:2])
			isPCM := formatTag == 1
			if formatTag == 0xfffe && len(format) >= 40 {
				isPCM = binary.LittleEndian.Uint16(format[18:20]) == 16 && binary.LittleEndian.Uint32(format[24:28]) == 1
			}
			if !isPCM || binary.LittleEndian.Uint16(format[14:16]) != 16 {
				return fail(errUnsupportedPCMWave)
			}
			channels = int(binary.LittleEndian.Uint16(format[2:4]))
			rate = int(binary.LittleEndian.Uint32(format[4:8]))
			width = 2
		case "data":
			dataOffset, dataLength = chunkStart, chunkSize
		}
		next := chunkStart + chunkSize
		if chunkSize%2 != 0 {
			next++
		}
		if _, err := file.Seek(next, io.SeekStart); err != nil {
			return fail(fmt.Errorf("seek WAVE chunk: %w", err))
		}
	}
	if rate < 8000 || rate > 384000 || channels < 1 || channels > 8 || width != 2 || dataOffset <= 0 || dataLength <= 0 {
		return fail(errUnsupportedPCMWave)
	}
	stat, err := file.Stat()
	if err != nil || dataOffset+dataLength > stat.Size() {
		return fail(errUnsupportedPCMWave)
	}
	return &pcmSource{file: file, reader: io.NewSectionReader(file, dataOffset, dataLength), rate: rate, width: width, channels: channels, temporary: temporary}, nil
}

var _ Transcriber = (*WyomingTranscriber)(nil)
