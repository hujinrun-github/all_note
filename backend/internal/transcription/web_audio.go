package transcription

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const webAudioMaxBytes int64 = 20 * 1024 * 1024
const webAudioMaxSeconds = 180

// Recorder.stop is dispatched by the browser event loop and compressed audio
// may include a final padded frame. Allow a bounded half-second overrun.
const webAudioDurationSlackSamples = 16000 / 2

var (
	ErrInvalidWebAudio         = errors.New("invalid or unsupported browser audio")
	ErrWebAudioTooLarge        = errors.New("browser audio exceeds the size limit")
	ErrWebAudioTooLong         = errors.New("browser audio exceeds the duration limit")
	ErrAudioDecoderUnavailable = errors.New("browser audio decoder is unavailable")
)

// WebAudioTranscriber bounds and decodes browser recordings before calling the
// workspace's existing transcriber. No provider or credentials are selected here.
type WebAudioTranscriber struct{ delegate Transcriber }

func NewWebAudioTranscriber(delegate Transcriber) *WebAudioTranscriber {
	return &WebAudioTranscriber{delegate: delegate}
}

func WebAudioDecoderAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

func (t *WebAudioTranscriber) CheckAvailable(ctx context.Context, workspaceID string) error {
	if t == nil || t.delegate == nil {
		return ErrUnavailable
	}
	if !WebAudioDecoderAvailable() {
		return ErrAudioDecoderUnavailable
	}
	if checker, ok := t.delegate.(interface {
		CheckAvailable(context.Context, string) error
	}); ok {
		return checker.CheckAvailable(ctx, workspaceID)
	}
	return ErrUnavailable
}

func (t *WebAudioTranscriber) Transcribe(ctx context.Context, input Input) (string, error) {
	if t == nil || t.delegate == nil {
		return "", ErrUnavailable
	}
	if input.Audio == nil {
		return "", ErrInvalidWebAudio
	}
	mediaType, _, err := mime.ParseMediaType(input.ContentType)
	if err != nil {
		return "", ErrInvalidWebAudio
	}
	demuxer, extension := webAudioFormat(strings.ToLower(mediaType))
	if demuxer == "" {
		return "", ErrInvalidWebAudio
	}
	data, err := io.ReadAll(io.LimitReader(input.Audio, webAudioMaxBytes+1))
	if err != nil {
		return "", ErrInvalidWebAudio
	}
	if int64(len(data)) > webAudioMaxBytes {
		return "", ErrWebAudioTooLarge
	}
	if !webAudioHeaderMatches(mediaType, data) {
		return "", ErrInvalidWebAudio
	}
	if demuxer == "wav" && !singleWavePayload(data) {
		return "", ErrInvalidWebAudio
	}
	directory, err := os.MkdirTemp("", "flowspace-plan-audio-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(directory)
	inputPath := filepath.Join(directory, "recording"+extension)
	if err := os.WriteFile(inputPath, data, 0o600); err != nil {
		return "", err
	}
	outputPath := inputPath
	// Rewrap validated PCM so the provider receives exactly the sample bytes
	// whose duration we checked, with one unambiguous format and data chunk.
	canonical := false
	if demuxer == "wav" {
		pcm, parseErr := openPCM16Wave(inputPath, "")
		if parseErr == nil {
			canonical = pcm.rate == 16000 && pcm.channels == 1
			if canonical {
				payload, readErr := io.ReadAll(io.LimitReader(pcm, 16000*2*(webAudioMaxSeconds+1)+1))
				if readErr != nil {
					_ = pcm.Close()
					return "", ErrInvalidWebAudio
				}
				if len(payload) == 0 || len(payload)%2 != 0 {
					_ = pcm.Close()
					return "", ErrInvalidWebAudio
				}
				if len(payload) > 16000*2*webAudioMaxSeconds+2*webAudioDurationSlackSamples {
					_ = pcm.Close()
					return "", ErrWebAudioTooLong
				}
				outputPath = filepath.Join(directory, "canonical.wav")
				if writeErr := writeCanonicalWebWave(outputPath, payload); writeErr != nil {
					_ = pcm.Close()
					return "", writeErr
				}
			}
			_ = pcm.Close()
		}
	}
	if !canonical {
		ffmpeg, lookupErr := exec.LookPath("ffmpeg")
		if lookupErr != nil {
			return "", ErrAudioDecoderUnavailable
		}
		decodeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		outputPath = filepath.Join(directory, "decoded.wav")
		// Force the container demuxer and restrict protocols: uploaded bytes must
		// never be interpreted as a playlist that fetches remote resources.
		command := exec.CommandContext(decodeCtx, ffmpeg,
			"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
			"-protocol_whitelist", "file,pipe", "-threads", "1", "-f", demuxer, "-i", inputPath,
			"-map", "0:a:0", "-vn", "-sn", "-dn", "-t", "181", "-ac", "1", "-ar", "16000",
			"-threads", "1", "-filter_threads", "1", "-c:a", "pcm_s16le", "-f", "wav", outputPath)
		command.Stdout = io.Discard
		command.Stderr = io.Discard
		if err := command.Run(); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", ErrInvalidWebAudio
		}
	}
	pcm, err := openPCM16Wave(outputPath, "")
	if err != nil {
		return "", ErrInvalidWebAudio
	}
	count, readErr := io.Copy(io.Discard, io.LimitReader(pcm, 16000*2*(webAudioMaxSeconds+1)+1))
	closeErr := pcm.Close()
	if readErr != nil || closeErr != nil || count == 0 {
		return "", ErrInvalidWebAudio
	}
	if count > int64(16000*2*webAudioMaxSeconds+2*webAudioDurationSlackSamples) {
		return "", ErrWebAudioTooLong
	}
	file, err := os.Open(outputPath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	input.Audio, input.ContentType, input.Filename = file, "audio/wav", "recording.wav"
	return t.delegate.Transcribe(ctx, input)
}

// A RIFF with multiple format/data chunks is interpreted inconsistently by
// decoders. Accept only a single self-contained payload, allowing metadata.
func singleWavePayload(data []byte) bool {
	if len(data) < 12 || uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
		return false
	}
	formats, payloads := 0, 0
	for offset := 12; offset < len(data); {
		if len(data)-offset < 8 {
			return false
		}
		size := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		next := uint64(offset) + 8 + size + size%2
		if next > uint64(len(data)) {
			return false
		}
		switch string(data[offset : offset+4]) {
		case "fmt ":
			formats++
		case "data":
			payloads++
		}
		if formats > 1 || payloads > 1 {
			return false
		}
		offset = int(next)
	}
	return formats == 1 && payloads == 1
}

func writeCanonicalWebWave(path string, payload []byte) error {
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(len(payload)+36))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], 16000)
	binary.LittleEndian.PutUint32(header[28:], 32000)
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(len(payload)))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(header)
	if writeErr == nil {
		_, writeErr = file.Write(payload)
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func webAudioFormat(mediaType string) (string, string) {
	switch mediaType {
	case "audio/webm":
		return "matroska", ".webm"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return "mov", ".m4a"
	case "audio/ogg":
		return "ogg", ".ogg"
	case "audio/wav", "audio/x-wav":
		return "wav", ".wav"
	case "audio/mpeg":
		return "mp3", ".mp3"
	case "audio/aac":
		return "aac", ".aac"
	default:
		return "", ""
	}
}

func webAudioHeaderMatches(mediaType string, data []byte) bool {
	if len(data) < 12 {
		return false
	}
	switch strings.ToLower(mediaType) {
	case "audio/webm":
		return bytes.Equal(data[:4], []byte{0x1a, 0x45, 0xdf, 0xa3})
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return string(data[4:8]) == "ftyp"
	case "audio/ogg":
		return string(data[:4]) == "OggS"
	case "audio/wav", "audio/x-wav":
		return string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE"
	case "audio/mpeg":
		return string(data[:3]) == "ID3" || data[0] == 0xff && data[1]&0xe0 == 0xe0
	case "audio/aac":
		return data[0] == 0xff && data[1]&0xf6 == 0xf0
	default:
		return false
	}
}
