package wyoming

import (
	"bufio"
	"bytes"
	"reflect"
	"testing"
)

func TestEventRoundTripWithDataAndPayload(t *testing.T) {
	var wire bytes.Buffer
	want := Event{
		Type:    "audio-chunk",
		Data:    map[string]any{"rate": 16000, "width": 2, "channels": 1},
		Payload: []byte{1, 2, 3, 4},
	}
	if err := WriteEvent(&wire, want); err != nil {
		t.Fatalf("write event: %v", err)
	}
	got, err := ReadEvent(bufio.NewReader(&wire))
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	if got.Type != want.Type || !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("event = %#v", got)
	}
	if got.Data["rate"] != float64(16000) || got.Data["width"] != float64(2) || got.Data["channels"] != float64(1) {
		t.Fatalf("event data = %#v", got.Data)
	}
}

func TestReadEventMergesInlineAndTrailingData(t *testing.T) {
	wire := bytes.NewBufferString("{\"type\":\"info\",\"data\":{\"inline\":true},\"data_length\":15}\n{\"asr\":[\"one\"]}")
	event, err := ReadEvent(bufio.NewReader(wire))
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	if !reflect.DeepEqual(event.Data, map[string]any{"inline": true, "asr": []any{"one"}}) {
		t.Fatalf("data = %#v", event.Data)
	}
}

func TestParseEndpointNormalizesHostPort(t *testing.T) {
	endpoint, err := ParseEndpoint("192.168.1.13:20300")
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	if endpoint.URL != "tcp://192.168.1.13:20300" || endpoint.Address != "192.168.1.13:20300" {
		t.Fatalf("endpoint = %#v", endpoint)
	}
	if _, err := ParseEndpoint("http://192.168.1.13:20300"); err == nil {
		t.Fatal("expected HTTP endpoint to be rejected")
	}
}
