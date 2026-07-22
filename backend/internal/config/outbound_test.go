package config

import (
	"net/netip"
	"testing"
)

func TestLoadOutboundPolicyParsesExplicitPrivateCIDRs(t *testing.T) {
	t.Setenv("FLOWSPACE_ALLOWED_PRIVATE_CIDRS", "192.168.1.70/32, 10.20.0.7/16,192.168.1.70/32")
	policy, err := LoadOutboundPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.AllowedPrivateCIDRs) != 2 || policy.AllowedPrivateCIDRs[0] != netip.MustParsePrefix("192.168.1.70/32") || policy.AllowedPrivateCIDRs[1] != netip.MustParsePrefix("10.20.0.0/16") {
		t.Fatalf("allowed CIDRs = %v", policy.AllowedPrivateCIDRs)
	}
}

func TestLoadOutboundPolicyRejectsInvalidCIDR(t *testing.T) {
	t.Setenv("FLOWSPACE_ALLOWED_PRIVATE_CIDRS", "192.168.1.70")
	if _, err := LoadOutboundPolicy(); err == nil {
		t.Fatal("invalid CIDR was accepted")
	}
}
