package podcastaudio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrProcessorUnavailable = errors.New("podcast audio processor is unavailable")
	ErrInvalidAudio         = errors.New("podcast audio could not be decoded")
)

const maxChunkCount = 64

type Chunk struct {
	Path        string
	Filename    string
	ContentType string
	Offset      time.Duration
	SHA256      string
}

type Processor interface {
	Split(context.Context, string, string) ([]Chunk, error)
}

type FFmpegProcessor struct {
	SegmentDuration time.Duration
	lookPath        func(string) (string, error)
}

func NewFFmpegProcessor() *FFmpegProcessor {
	return &FFmpegProcessor{SegmentDuration: 15 * time.Minute, lookPath: exec.LookPath}
}

func (p *FFmpegProcessor) Split(ctx context.Context, inputPath, outputDir string) ([]Chunk, error) {
	if strings.TrimSpace(inputPath) == "" || strings.TrimSpace(outputDir) == "" {
		return nil, ErrInvalidAudio
	}
	duration := p.SegmentDuration
	if duration <= 0 {
		duration = 15 * time.Minute
	}
	lookPath := p.lookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	ffmpeg, err := lookPath("ffmpeg")
	if err != nil {
		return nil, ErrProcessorUnavailable
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return nil, fmt.Errorf("create podcast chunk directory: %w", err)
	}
	pattern := filepath.Join(outputDir, "chunk-%04d.mp3")
	command := exec.CommandContext(ctx, ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", inputPath, "-map", "0:a:0", "-vn", "-sn", "-dn",
		"-ac", "1", "-ar", "16000", "-codec:a", "libmp3lame", "-b:a", "48k",
		"-f", "segment", "-segment_time", strconv.FormatInt(int64(duration/time.Second), 10),
		"-reset_timestamps", "1", pattern,
	)
	var output boundedBuffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("%w: %v: %s", ErrInvalidAudio, err, output.String())
	}
	paths, err := filepath.Glob(filepath.Join(outputDir, "chunk-*.mp3"))
	if err != nil {
		return nil, fmt.Errorf("list podcast audio chunks: %w", err)
	}
	sort.Strings(paths)
	if len(paths) == 0 || len(paths) > maxChunkCount {
		return nil, ErrInvalidAudio
	}
	chunks := make([]Chunk, 0, len(paths))
	for index, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			return nil, ErrInvalidAudio
		}
		digest, err := fileSHA256(path)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, Chunk{
			Path: path, Filename: filepath.Base(path), ContentType: "audio/mpeg",
			Offset: time.Duration(index) * duration, SHA256: digest,
		})
	}
	return chunks, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open podcast audio chunk: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("hash podcast audio chunk: %w", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(contents []byte) (int, error) {
	const limit = 8 << 10
	remaining := limit - b.Len()
	if remaining > 0 {
		if remaining > len(contents) {
			remaining = len(contents)
		}
		_, _ = b.Buffer.Write(contents[:remaining])
	}
	return len(contents), nil
}
