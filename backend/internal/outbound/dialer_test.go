package outbound

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"testing"
)

type sequenceResolver struct {
	answers [][]netip.Addr
	calls   int
}

func (r *sequenceResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	index := r.calls
	r.calls++
	if index >= len(r.answers) {
		index = len(r.answers) - 1
	}
	return r.answers[index], nil
}

func TestDialerRevalidatesEveryPhysicalConnectionAndRejectsRebinding(t *testing.T) {
	resolver := &sequenceResolver{answers: [][]netip.Addr{{netip.MustParseAddr("203.0.113.10")}, {netip.MustParseAddr("127.0.0.1")}}}
	dialer, _ := NewDialer(resolver, Policy{})
	var dialed []string
	dialer.dialNetwork = func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = append(dialed, address)
		return nil, errors.New("test stop")
	}
	_, _ = dialer.DialContext(context.Background(), "tcp", "database.example:5432")
	if len(dialed) != 1 || dialed[0] != "203.0.113.10:5432" {
		t.Fatalf("first selected address=%v", dialed)
	}
	if _, err := dialer.DialContext(context.Background(), "tcp", "database.example:5432"); !errors.Is(err, ErrAddressDenied) {
		t.Fatalf("DNS rebinding error=%v", err)
	}
	if len(dialed) != 1 || resolver.calls != 2 {
		t.Fatalf("denied address reached network: dialed=%v calls=%d", dialed, resolver.calls)
	}
}

func TestPrivateAddressesRequireExplicitCIDRAndMixedDNSIsDenied(t *testing.T) {
	private := netip.MustParseAddr("192.168.1.20")
	denied, _ := NewDialer(&sequenceResolver{answers: [][]netip.Addr{{private}}}, Policy{})
	if _, err := denied.ResolveAllowed(context.Background(), "minio.internal"); !errors.Is(err, ErrAddressDenied) {
		t.Fatalf("private address error=%v", err)
	}
	allowed, _ := NewDialer(&sequenceResolver{answers: [][]netip.Addr{{private}}}, Policy{AllowedPrivateCIDRs: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}})
	if address, err := allowed.ResolveAllowed(context.Background(), "minio.internal"); err != nil || address != private {
		t.Fatalf("explicit private address=%v err=%v", address, err)
	}
	mixed, _ := NewDialer(&sequenceResolver{answers: [][]netip.Addr{{netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("127.0.0.1")}}}, Policy{})
	if _, err := mixed.ResolveAllowed(context.Background(), "mixed.example"); !errors.Is(err, ErrAddressDenied) {
		t.Fatalf("mixed DNS answer error=%v", err)
	}
}

func TestHTTPClientDisablesProxyAndValidatesRedirect(t *testing.T) {
	dialer, _ := NewDialer(&sequenceResolver{answers: [][]netip.Addr{{netip.MustParseAddr("203.0.113.10")}, {netip.MustParseAddr("127.0.0.1")}}}, Policy{})
	client := dialer.HTTPClient()
	transport := client.Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Fatal("HTTP transport must ignore environment proxies")
	}
	request, _ := http.NewRequest(http.MethodGet, "http://redirect.example/private", nil)
	request.Header.Set("Authorization", "Bearer secret")
	if err := client.CheckRedirect(request, nil); err != nil {
		t.Fatalf("public redirect rejected: %v", err)
	}
	if request.Header.Get("Authorization") != "" {
		t.Fatal("authorization header survived redirect")
	}
	second, _ := http.NewRequest(http.MethodGet, "http://redirect.example/internal", nil)
	if err := client.CheckRedirect(second, []*http.Request{request}); !errors.Is(err, ErrAddressDenied) {
		t.Fatalf("redirect rebinding error=%v", err)
	}
}
