package controlsettings

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/netip"
	"testing"

	"github.com/hujinrun/flowspace/internal/outbound"
	wyomingprotocol "github.com/hujinrun/flowspace/internal/wyoming"
)

func TestWyomingProbeDescribesASRService(t *testing.T) {
	prober, err := NewHTTPProber(outbound.Policy{AllowedPrivateCIDRs: []netip.Prefix{netip.MustParsePrefix("192.168.1.13/32")}})
	if err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	prober.dialContext = func(context.Context, string, string) (net.Conn, error) { return client, nil }
	serverResult := make(chan error, 1)
	go func() {
		defer server.Close()
		event, err := wyomingprotocol.ReadEvent(bufio.NewReader(server))
		if err != nil {
			serverResult <- err
			return
		}
		if event.Type != "describe" {
			serverResult <- fmt.Errorf("event type = %q", event.Type)
			return
		}
		serverResult <- wyomingprotocol.WriteEvent(server, wyomingprotocol.Event{Type: "info", Data: map[string]any{"asr": []any{map[string]any{"name": "faster-whisper"}}}})
	}()

	result, err := prober.Probe(context.Background(), "llm_transcription", "wyoming", []byte(`{"endpoint":"tcp://192.168.1.13:20300","model":"auto"}`), nil)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if result.Code != "OK" || result.Message != "Wyoming 语音转写服务连接测试通过" {
		t.Fatalf("result = %#v", result)
	}
	if err := <-serverResult; err != nil {
		t.Fatalf("server: %v", err)
	}
}

func TestWyomingProfileNormalizesEndpointAndDefaultModel(t *testing.T) {
	profile := map[string]any{"endpoint": "192.168.1.13:20300", "model": ""}
	if err := validateProfileConfig("llm_transcription", "wyoming", profile); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if profile["endpoint"] != "tcp://192.168.1.13:20300" || profile["model"] != "auto" {
		t.Fatalf("profile = %#v", profile)
	}
	if err := validateProfileConfig("llm_transcription", "wyoming", map[string]any{"endpoint": "http://192.168.1.13:20300"}); err == nil {
		t.Fatal("expected HTTP endpoint to be rejected")
	}
}
