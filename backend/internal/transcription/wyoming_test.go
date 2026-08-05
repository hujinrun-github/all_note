package transcription

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"

	wyomingprotocol "github.com/hujinrun/flowspace/internal/wyoming"
)

func TestWyomingTranscriberStreamsPCMAndReadsTranscript(t *testing.T) {
	client, server := net.Pipe()
	serverResult := make(chan error, 1)
	go func() {
		defer server.Close()
		reader := bufio.NewReader(server)
		for _, wantType := range []string{"transcribe", "audio-start", "audio-chunk", "audio-stop"} {
			event, err := wyomingprotocol.ReadEvent(reader)
			if err != nil {
				serverResult <- err
				return
			}
			if event.Type != wantType {
				serverResult <- fmt.Errorf("event type = %q, want %q", event.Type, wantType)
				return
			}
			switch event.Type {
			case "transcribe":
				if event.Data["name"] != "auto" || event.Data["language"] != "zh" {
					serverResult <- fmt.Errorf("transcribe data = %#v", event.Data)
					return
				}
			case "audio-chunk":
				if !bytes.Equal(event.Payload, []byte{1, 2, 3, 4}) {
					serverResult <- fmt.Errorf("audio payload = %v", event.Payload)
					return
				}
			}
		}
		serverResult <- wyomingprotocol.WriteEvent(server, wyomingprotocol.Event{Type: "transcript", Data: map[string]any{"text": " 测试转写完成 "}})
	}()

	transcriber, err := NewWyomingTranscriber(WyomingConfig{Endpoint: "tcp://speech.example:20300", Model: "auto", Timeout: time.Second}, func(context.Context, string, string) (net.Conn, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("new transcriber: %v", err)
	}
	text, err := transcriber.Transcribe(context.Background(), Input{
		Audio:       bytes.NewReader(pcmWave(16000, 1, []byte{1, 2, 3, 4})),
		Filename:    "recording.wav",
		ContentType: "audio/wav",
		Language:    "zh-CN",
	})
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if text != "测试转写完成" {
		t.Fatalf("text = %q", text)
	}
	if err := <-serverResult; err != nil {
		t.Fatalf("server: %v", err)
	}
}

func TestWyomingLanguageOmitsAutomaticDetectionValue(t *testing.T) {
	tests := map[string]string{
		"auto":  "",
		"AUTO":  "",
		"zh-CN": "zh",
		"en_US": "en",
		"":      "",
	}
	for input, want := range tests {
		if got := wyomingLanguage(input); got != want {
			t.Errorf("wyomingLanguage(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPrepareWyomingPCMAcceptsExtensibleWave(t *testing.T) {
	pcm, err := prepareWyomingPCM(context.Background(), Input{Audio: bytes.NewReader(pcmExtensibleWave(22050, 1, []byte{1, 2, 3, 4})), Filename: "recording.wav", ContentType: "audio/wav"})
	if err != nil {
		t.Fatalf("prepare PCM: %v", err)
	}
	defer pcm.Close()
	if pcm.rate != 22050 || pcm.width != 2 || pcm.channels != 1 {
		t.Fatalf("format = rate:%d width:%d channels:%d", pcm.rate, pcm.width, pcm.channels)
	}
	decoded := make([]byte, 4)
	if _, err := pcm.Read(decoded); err != nil {
		t.Fatalf("read PCM: %v", err)
	}
	if !bytes.Equal(decoded, []byte{1, 2, 3, 4}) {
		t.Fatalf("PCM = %v", decoded)
	}
}

func pcmWave(rate, channels int, pcm []byte) []byte {
	var wave bytes.Buffer
	wave.WriteString("RIFF")
	_ = binary.Write(&wave, binary.LittleEndian, uint32(36+len(pcm)))
	wave.WriteString("WAVEfmt ")
	_ = binary.Write(&wave, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wave, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wave, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&wave, binary.LittleEndian, uint32(rate))
	_ = binary.Write(&wave, binary.LittleEndian, uint32(rate*channels*2))
	_ = binary.Write(&wave, binary.LittleEndian, uint16(channels*2))
	_ = binary.Write(&wave, binary.LittleEndian, uint16(16))
	wave.WriteString("data")
	_ = binary.Write(&wave, binary.LittleEndian, uint32(len(pcm)))
	wave.Write(pcm)
	return wave.Bytes()
}

func pcmExtensibleWave(rate, channels int, pcm []byte) []byte {
	var wave bytes.Buffer
	wave.WriteString("RIFF")
	_ = binary.Write(&wave, binary.LittleEndian, uint32(60+len(pcm)))
	wave.WriteString("WAVEfmt ")
	_ = binary.Write(&wave, binary.LittleEndian, uint32(40))
	_ = binary.Write(&wave, binary.LittleEndian, uint16(0xfffe))
	_ = binary.Write(&wave, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&wave, binary.LittleEndian, uint32(rate))
	_ = binary.Write(&wave, binary.LittleEndian, uint32(rate*channels*2))
	_ = binary.Write(&wave, binary.LittleEndian, uint16(channels*2))
	_ = binary.Write(&wave, binary.LittleEndian, uint16(16))
	_ = binary.Write(&wave, binary.LittleEndian, uint16(22))
	_ = binary.Write(&wave, binary.LittleEndian, uint16(16))
	_ = binary.Write(&wave, binary.LittleEndian, uint32(4))
	wave.Write([]byte{1, 0, 0, 0, 0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71})
	wave.WriteString("data")
	_ = binary.Write(&wave, binary.LittleEndian, uint32(len(pcm)))
	wave.Write(pcm)
	return wave.Bytes()
}
