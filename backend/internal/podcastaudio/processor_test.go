package podcastaudio

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestFFmpegProcessorReportsUnavailableBinary(t *testing.T) {
	processor := NewFFmpegProcessor()
	processor.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	_, err := processor.Split(context.Background(), filepath.Join(t.TempDir(), "input.mp3"), t.TempDir())
	if !errors.Is(err, ErrProcessorUnavailable) {
		t.Fatalf("Split() error = %v", err)
	}
}

func TestFFmpegProcessorSplitsAudioWhenFFmpegIsAvailable(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	directory := t.TempDir()
	input := filepath.Join(directory, "input.wav")
	command := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=2.2", input)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create fixture audio: %v: %s", err, output)
	}
	processor := NewFFmpegProcessor()
	processor.SegmentDuration = time.Second
	chunks, err := processor.Split(context.Background(), input, filepath.Join(directory, "chunks"))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 || chunks[0].Offset != 0 || chunks[1].Offset != time.Second || chunks[0].SHA256 == "" {
		t.Fatalf("chunks = %#v", chunks)
	}
}
