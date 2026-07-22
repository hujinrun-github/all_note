package controlsettings

import (
	"context"
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/outbound"
)

func TestPostgresProbeRejectsPasswordEmbeddedInProfileConfig(t *testing.T) {
	prober, err := NewHTTPProber(outbound.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = prober.Probe(context.Background(), "data_store", "postgres", []byte(`{"endpoint":"postgres://user:password@db.example.com/flowspace","schema":"public"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("error = %v", err)
	}
}

func TestPostgresProbeAppliesPrivateNetworkPolicyBeforeConnecting(t *testing.T) {
	prober, err := NewHTTPProber(outbound.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = prober.Probe(context.Background(), "data_store", "postgres", []byte(`{"endpoint":"postgres://user@192.168.1.70:5432/flowspace","schema":"public"}`), []byte("secret"))
	if err == nil || !strings.Contains(err.Error(), outbound.ErrAddressDenied.Error()) {
		t.Fatalf("error = %v", err)
	}
}
