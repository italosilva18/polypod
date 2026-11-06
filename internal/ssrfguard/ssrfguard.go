// Package ssrfguard provides SSRF prevention by blocking unsafe IP destinations.
package ssrfguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Standard errors.
var (
	ErrLoopback       = errors.New("ssrfguard: loopback address blocked")
	ErrPrivate        = errors.New("ssrfguard: private network address blocked")
	ErrLinkLocal      = errors.New("ssrfguard: link-local address blocked")
	ErrMulticast      = errors.New("ssrfguard: multicast address blocked")
	ErrUnspecified    = errors.New("ssrfguard: unspecified address blocked")
	ErrMetadata       = errors.New("ssrfguard: metadata service address blocked")
	ErrReservedIPv6   = errors.New("ssrfguard: reserved IPv6 address blocked")
	ErrInvalidAddress = errors.New("ssrfguard: invalid address")
)

// Config configures the SSRF guard.
type Config struct {
	// AllowLoopback permits 127.0.0.0/8 and ::1 (default: false).
	AllowLoopback bool
	// AllowPrivate permits RFC1918 and similar (default: false).
	AllowPrivate bool
	// AllowLinkLocal permits 169.254.0.0/16 and fe80::/10 (default: false).
	AllowLinkLocal bool
	// Allowlist is a set of hostnames/IPs to always permit, bypassing all checks.
	Allowlist []string
	// Denylist is a set of hostnames/IPs to always block, regardless of other settings.
	Denylist []string
}

// DefaultConfig returns safe defaults (block everything private).
func DefaultConfig() Config {
	return Config{}
}

// Guard is a configured SSRF guard.
type Guard struct {
	mu       sync.RWMutex
	config   Config
	allowSet map[string]bool
	denySet  map[string]bool
}

// NewGuard creates a new guard with the given config.
func NewGuard(cfg Config) *Guard {
	g := &Guard{
		config:   cfg,
		allowSet: make(map[string]bool),
		denySet:  make(map[string]bool),
	}
	for _, h := range cfg.Allowlist {
		g.allowSet[strings.ToLower(h)] = true
	}
	for _, h := range cfg.Denylist {
		g.denySet[strings.ToLower(h)] = true
	}
	return g
}

// Default returns a guard with default config.
func Default() *Guard {
	return NewGuard(DefaultConfig())
}

// IsSafeHost checks if a hostname is safe (without DNS resolution).
// Returns (safe, reason). If the host is an IP, also checks IP safety.
func (g *Guard) IsSafeHost(host string) (bool, string) {
	host = strings.ToLower(host)

	// Strip port if present
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	// Strip brackets from IPv6
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")

	if host == "" {
		return false, "empty host"
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	// Allowlist first
	if g.allowSet[host] {
		return true, ""
	}

	// Denylist
	if g.denySet[host] {
		return false, "hostname in denylist"
	}

	// If it's an IP, check directly
	if ip := net.ParseIP(host); ip != nil {
		ok, reason := g.isSafeIPLocked(ip)
		if !ok {
			return false, reason
		}
		return true, ""
	}

	// Hostname - can't check until resolved
	// Block obvious suspicious patterns
	if isLocalhostAlias(host) && !g.config.AllowLoopback {
		return false, "loopback hostname alias"
	}

	return true, ""
}

// IsSafeIP checks if an IP address is safe.
func (g *Guard) IsSafeIP(ip net.IP) (bool, string) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.isSafeIPLocked(ip)
}

func (g *Guard) isSafeIPLocked(ip net.IP) (bool, string) {
	if ip == nil {
		return false, "nil IP"
	}

	// Denylist check for IP string form
	if g.denySet[ip.String()] {
		return false, "IP in denylist"
	}

	// Allowlist check
	if g.allowSet[ip.String()] {
		return true, ""
	}

	// Metadata service addresses (AWS/GCP/Azure/OCI/Alibaba)
	if isMetadataIP(ip) {
		return false, ErrMetadata.Error()
	}

	// Unspecified (0.0.0.0, ::)
	if ip.IsUnspecified() {
		return false, ErrUnspecified.Error()
	}

	// Loopback
	if ip.IsLoopback() && !g.config.AllowLoopback {
		return false, ErrLoopback.Error()
	}

	// Link-local
	if (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) && !g.config.AllowLinkLocal {
		return false, ErrLinkLocal.Error()
	}

	// Multicast
	if ip.IsMulticast() {
		return false, ErrMulticast.Error()
	}

	// Private (RFC1918, etc.)
	if ip.IsPrivate() && !g.config.AllowPrivate {
		return false, ErrPrivate.Error()
	}

	// Reserved IPv6 ranges
	if ip4 := ip.To4(); ip4 == nil {
		if isReservedIPv6(ip) {
			return false, ErrReservedIPv6.Error()
		}
	}

	return true, ""
}

// isMetadataIP checks known cloud metadata service IPs.
func isMetadataIP(ip net.IP) bool {
	metadata := []string{
		"169.254.169.254", // AWS, GCP, Azure, Alibaba
		"169.254.170.2",   // AWS ECS task metadata
		"fd00:ec2::254",   // AWS IPv6
		"100.100.100.200", // Alibaba Cloud
	}
	ipStr := ip.String()
	for _, m := range metadata {
		if ipStr == m {
			return true
		}
	}
	return false
}

// isReservedIPv6 checks for reserved IPv6 ranges.
func isReservedIPv6(ip net.IP) bool {
	ip = ip.To16()
	if ip == nil {
		return false
	}
	// fd00::/8 - Unique Local Addresses
	if ip[0] == 0xfd || ip[0] == 0xfc {
		return true
	}
	// ::ffff:0:0/96 - IPv4-mapped addresses
	if ip[0] == 0 && ip[1] == 0 && ip[2] == 0 && ip[3] == 0 &&
		ip[4] == 0 && ip[5] == 0 && ip[6] == 0 && ip[7] == 0 &&
		ip[8] == 0 && ip[9] == 0 && ip[10] == 0xff && ip[11] == 0xff {
		// Check if the IPv4 part would be unsafe
		return false // delegate to IPv4 check
	}
	return false
}

// isLocalhostAlias checks for common localhost hostnames.
func isLocalhostAlias(host string) bool {
	lower := strings.ToLower(host)
	aliases := []string{"localhost", "localhost.localdomain", "ip6-localhost", "ip6-loopback"}
	for _, a := range aliases {
		if lower == a {
			return true
		}
	}
	return strings.HasSuffix(lower, ".localhost")
}

// GuardedDialContext wraps a DialContext function to block unsafe addresses post-DNS.
func (g *Guard) GuardedDialContext(base func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if base == nil {
		dialer := &net.Dialer{Timeout: 30 * time.Second}
		base = dialer.DialContext
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("ssrfguard: %w: %v", ErrInvalidAddress, err)
		}

		// Resolve the host
		resolver := net.Resolver{}
		ips, err := resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("ssrfguard: dns lookup failed: %w", err)
		}

		// Check each resolved IP
		for _, ip := range ips {
			if ok, reason := g.IsSafeIP(ip.IP); !ok {
				return nil, fmt.Errorf("ssrfguard: %s (%s -> %s)", reason, host, ip.IP)
			}
		}

		// At least one safe IP - dial it
		if len(ips) == 0 {
			return nil, fmt.Errorf("ssrfguard: no IPs resolved for %s", host)
		}

		// Use the first resolved IP
		return base(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}

// WrapTransport wraps an http.Transport with SSRF protection.
func (g *Guard) WrapTransport(t *http.Transport) *http.Transport {
	if t == nil {
		t = &http.Transport{}
	}
	t.DialContext = g.GuardedDialContext(t.DialContext)
	return t
}

// ===== Default (global) guard =====

var defaultGuard = Default()

// IsSafeHost is a convenience for Default().IsSafeHost.
func IsSafeHost(host string) (bool, string) {
	return defaultGuard.IsSafeHost(host)
}

// IsSafeIP is a convenience for Default().IsSafeIP.
func IsSafeIP(ip net.IP) (bool, string) {
	return defaultGuard.IsSafeIP(ip)
}

// WrapTransport is a convenience for Default().WrapTransport.
func WrapTransport(t *http.Transport) *http.Transport {
	return defaultGuard.WrapTransport(t)
}
