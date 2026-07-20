package outbound

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var ErrAddressDenied = errors.New("outbound address is denied")

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type Policy struct {
	AllowedPrivateCIDRs []netip.Prefix
}

type Dialer struct {
	resolver    Resolver
	policy      Policy
	network     *net.Dialer
	dialNetwork func(context.Context, string, string) (net.Conn, error)
}

func NewDialer(resolver Resolver, policy Policy) (*Dialer, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	for _, prefix := range policy.AllowedPrivateCIDRs {
		if !prefix.IsValid() {
			return nil, errors.New("invalid allowed private CIDR")
		}
	}
	network := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	dialer := &Dialer{resolver: resolver, policy: policy, network: network}
	dialer.dialNetwork = network.DialContext
	return dialer, nil
}

func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid outbound address")
	}
	ip, err := d.ResolveAllowed(ctx, host)
	if err != nil {
		return nil, err
	}
	return d.dialNetwork(ctx, network, net.JoinHostPort(ip.String(), port))
}

func (d *Dialer) ResolveAllowed(ctx context.Context, host string) (netip.Addr, error) {
	host = strings.TrimSpace(strings.TrimSuffix(host, "."))
	if host == "" || strings.EqualFold(host, "localhost") {
		return netip.Addr{}, ErrAddressDenied
	}
	if literal, err := netip.ParseAddr(host); err == nil {
		if !d.allowed(literal.Unmap()) {
			return netip.Addr{}, ErrAddressDenied
		}
		return literal.Unmap(), nil
	}
	addresses, err := d.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return netip.Addr{}, fmt.Errorf("resolve outbound host")
	}
	var selected netip.Addr
	for _, address := range addresses {
		address = address.Unmap()
		if !d.allowed(address) {
			return netip.Addr{}, ErrAddressDenied
		}
		if !selected.IsValid() {
			selected = address
		}
	}
	return selected, nil
}

func (d *Dialer) HTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConnsPerHost:   2,
		DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errors.New("invalid TLS outbound address")
			}
			ip, err := d.ResolveAllowed(ctx, host)
			if err != nil {
				return nil, err
			}
			tlsDialer := tls.Dialer{NetDialer: d.network, Config: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}}
			return tlsDialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many outbound redirects")
			}
			if request.URL.Scheme != "http" && request.URL.Scheme != "https" {
				return ErrAddressDenied
			}
			if _, err := d.ResolveAllowed(request.Context(), request.URL.Hostname()); err != nil {
				return err
			}
			request.Header.Del("Authorization")
			request.Header.Del("Cookie")
			return nil
		},
	}
}

func (d *Dialer) ValidateURL(ctx context.Context, rawURL string, schemes ...string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return ErrAddressDenied
	}
	allowedScheme := false
	for _, scheme := range schemes {
		if parsed.Scheme == scheme {
			allowedScheme = true
		}
	}
	if !allowedScheme {
		return ErrAddressDenied
	}
	_, err = d.ResolveAllowed(ctx, parsed.Hostname())
	return err
}

func (d *Dialer) allowed(address netip.Addr) bool {
	if !address.IsValid() || address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() {
		return false
	}
	if address.IsPrivate() {
		for _, prefix := range d.policy.AllowedPrivateCIDRs {
			if prefix.Contains(address) {
				return true
			}
		}
		return false
	}
	return true
}
