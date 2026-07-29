package main

import (
	"context"
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/handler"
)

func TestValidateMobileV2WiringFailsClosed(t *testing.T) {
	if err := validateMobileV2Wiring(config.NativeConfig{}, nil); err != nil {
		t.Fatalf("disabled mobile-v2 wiring: %v", err)
	}

	err := validateMobileV2Wiring(config.NativeConfig{MobileSyncV2Enabled: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING=true") {
		t.Fatalf("missing task-domain routing error = %v", err)
	}

	err = validateMobileV2Wiring(config.NativeConfig{
		MobileSyncV2Enabled:        true,
		TaskDomainV2RoutingEnabled: true,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "concrete mobile-v2 service") {
		t.Fatalf("missing concrete service error = %v", err)
	}

	err = validateMobileV2Wiring(config.NativeConfig{
		MobileSyncV2Enabled:        true,
		TaskDomainV2RoutingEnabled: true,
	}, mobileV2WiringService{})
	if err != nil {
		t.Fatalf("complete mobile-v2 wiring: %v", err)
	}
}

func TestMobileV2TokenSecretIsDomainSeparatedAndStable(t *testing.T) {
	first := deriveMobileV2TokenSecret("session-secret")
	if first == "" || first == "session-secret" {
		t.Fatalf("derived token secret = %q", first)
	}
	if first != deriveMobileV2TokenSecret("session-secret") {
		t.Fatal("derived token secret must be stable")
	}
	if first == deriveMobileV2TokenSecret("different-session-secret") {
		t.Fatal("different session secrets must derive different mobile-v2 token secrets")
	}
}

type mobileV2WiringService struct{}

var _ handler.MobileV2Service = mobileV2WiringService{}

func (mobileV2WiringService) Capabilities(context.Context, handler.MobileV2Identity) (any, error) {
	return nil, nil
}

func (mobileV2WiringService) Snapshot(context.Context, handler.MobileV2SnapshotRequest) (any, error) {
	return nil, nil
}

func (mobileV2WiringService) Changes(context.Context, handler.MobileV2ChangesRequest) (any, error) {
	return nil, nil
}

func (mobileV2WiringService) ApplyCommand(context.Context, handler.MobileV2CommandRequest) (any, error) {
	return nil, nil
}

func (mobileV2WiringService) Receipt(context.Context, handler.MobileV2ReceiptRequest) (any, error) {
	return nil, nil
}
