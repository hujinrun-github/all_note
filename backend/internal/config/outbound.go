package config

import (
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/hujinrun/flowspace/internal/outbound"
)

func LoadOutboundPolicy() (outbound.Policy, error) {
	raw := strings.TrimSpace(os.Getenv("FLOWSPACE_ALLOWED_PRIVATE_CIDRS"))
	if raw == "" {
		return outbound.Policy{}, nil
	}
	parts := strings.Split(raw, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	seen := make(map[netip.Prefix]bool, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return outbound.Policy{}, fmt.Errorf("invalid FLOWSPACE_ALLOWED_PRIVATE_CIDRS entry %q", value)
		}
		prefix = prefix.Masked()
		if !seen[prefix] {
			seen[prefix] = true
			prefixes = append(prefixes, prefix)
		}
	}
	return outbound.Policy{AllowedPrivateCIDRs: prefixes}, nil
}
