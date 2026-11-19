package transcription

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type webAudioCapture struct {
	calls          int
	filename, mime string
	content        []byte
}

func (c *webAudioCapture) Transcribe(_ context.Context, input Input) (string, error) {
	c.calls++
	c.filename, c.mime = input.Filename, input.ContentType
	c.content, _ = io.ReadAll(input.Audio)
	return "周三晚上八点复习半小时", nil
}

func webTestWave(seconds int) []byte {
	data := make([]byte, 44+seconds*16000*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 16000)
	binary.LittleEndian.PutUint32(data[28:], 32000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(len(data)-44))
	return data
}

func TestWebAudioPreservesWorkspaceTranscriberAndCanonicalPCM(t *testing.T) {
	delegate := &webAudioCapture{}
	text, err := NewWebAudioTranscriber(delegate).Transcribe(context.Background(), Input{Audio: bytes.NewReader(webTestWave(1)), ContentType: "audio/wav", Language: "zh"})
	if err != nil || text == "" || delegate.calls != 1 || delegate.filename != "recording.wav" || delegate.mime != "audio/wav" {
		t.Fatalf("transcription = %q, %v, delegate=%+v", text, err, delegate)
	}
	if !bytes.Equal(delegate.content, webTestWave(1)) {
		t.Fatal("canonical PCM changed")
	}
}

func TestWebAudioRejectsInvalidAndOversizedRecordingsBeforeProvider(t *testing.T) {
	for _, test := range []struct {
		name, mime string
		data       []byte
		want       error
	}{
		{"empty", "audio/webm", nil, ErrInvalidWebAudio},
		{"disguised", "audio/mp4", webTestWave(1), ErrInvalidWebAudio},
		{"playlist", "audio/webm", []byte("#EXTM3U\nhttps://example.com/private\n"), ErrInvalidWebAudio},
		{"unsupported", "text/plain", webTestWave(1), ErrInvalidWebAudio},
		{"too long", "audio/wav", webTestWave(181), ErrWebAudioTooLong},
		{"too large", "audio/webm", make([]byte, webAudioMaxBytes+1), ErrWebAudioTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			delegate := &webAudioCapture{}
			_, err := NewWebAudioTranscriber(delegate).Transcribe(context.Background(), Input{Audio: bytes.NewReader(test.data), ContentType: test.mime})
			if !errors.Is(err, test.want) || delegate.calls != 0 {
				t.Fatalf("got %v, calls %d", err, delegate.calls)
			}
		})
	}
}

func TestWebAudioDecodesBrowserContainers(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	directory := t.TempDir()
	input := filepath.Join(directory, "source.wav")
	if err := os.WriteFile(input, webTestWave(1), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, mime, codec string }{
		{"webm", "audio/webm;codecs=opus", "libopus"},
		{"m4a", "audio/mp4", "aac"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := filepath.Join(directory, "source."+test.name)
			if result, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", input, "-c:a", test.codec, output).CombinedOutput(); err != nil {
				t.Fatalf("make browser fixture: %v %s", err, result)
			}
			file, err := os.Open(output)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			delegate := &webAudioCapture{}
			_, err = NewWebAudioTranscriber(delegate).Transcribe(context.Background(), Input{Audio: file, ContentType: test.mime})
			if err != nil || delegate.calls != 1 || len(delegate.content) < 44 || delegate.mime != "audio/wav" {
				t.Fatalf("decode = %v, delegate=%+v", err, delegate)
			}
		})
	}
}

func TestWebAudioRejectsAmbiguousWaveChunks(t *testing.T) {
	for _, chunk := range []string{"data", "fmt "} {
		t.Run(chunk, func(t *testing.T) {
			// A long first payload and short last payload must not pass by only
			// timing the last one while sending the entire recording upstream.
			data := webTestWave(181)
			if chunk == "data" {
				data = append(data, webTestWave(1)[36:]...)
			} else {
				data = append(data, webTestWave(1)[12:36]...)
			}
			binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
			delegate := &webAudioCapture{}
			_, err := NewWebAudioTranscriber(delegate).Transcribe(context.Background(), Input{Audio: bytes.NewReader(data), ContentType: "audio/wav"})
			if !errors.Is(err, ErrInvalidWebAudio) || delegate.calls != 0 {
				t.Fatalf("ambiguous WAV reached provider: %v calls=%d", err, delegate.calls)
			}
		})
	}
}
