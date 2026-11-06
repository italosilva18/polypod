// Package peeraddress provides utilities for parsing and resolving network addresses.
package peeraddress

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Family represents an IP version.
type Family int

const (
	FamilyUnknown Family = iota
	FamilyIPv4
	FamilyIPv6
)

// Addr represents a parsed network address.
type Addr struct {
	IP     net.IP
	Port   int
	Host   string // original host (could be hostname or IP literal)
	Family Family
}

// Parse parses a host:port string into an Addr.
// Supports: "1.2.3.4:80", "[::1]:80", "example.com:443", "example.com" (no port)
func Parse(s string) (Addr, error) {
	if s == "" {
		return Addr{}, fmt.Errorf("empty address")
	}

	var addr Addr

	// Try to split host:port
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		// Might not have a port
		addr.Host = s
	} else {
		addr.Host = host
		port, perr := strconv.Atoi(portStr)
		if perr != nil {
			return Addr{}, fmt.Errorf("invalid port: %s", portStr)
		}
		if port < 0 || port > 65535 {
			return Addr{}, fmt.Errorf("port out of range: %d", port)
		}
		addr.Port = port
	}

	// Try to parse as IP literal
	if ip := net.ParseIP(addr.Host); ip != nil {
		addr.IP = ip
		addr.Family = detectFamily(ip)
	}

	return addr, nil
}

// MustParse is like Parse but panics on error.
func MustParse(s string) Addr {
	a, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return a
}

// String returns the canonical host:port representation.
func (a Addr) String() string {
	host := a.Host
	if a.IP != nil {
		host = a.IP.String()
	}

	if a.Family == FamilyIPv6 {
		host = "[" + host + "]"
	}

	if a.Port == 0 {
		return host
	}
	return host + ":" + strconv.Itoa(a.Port)
}

// IsResolved returns true if the IP has been resolved.
func (a Addr) IsResolved() bool {
	return a.IP != nil
}

// IsLiteral returns true if the address was an IP literal.
func (a Addr) IsLiteral() bool {
	if a.IP == nil {
		return false
	}
	return net.ParseIP(a.Host) != nil
}

// IsLoopback returns true if the IP is a loopback address.
func (a Addr) IsLoopback() bool {
	if a.IP == nil {
		return false
	}
	return a.IP.IsLoopback()
}

// IsPrivate returns true if the IP is in a private range (RFC1918, link-local, etc.).
func (a Addr) IsPrivate() bool {
	if a.IP == nil {
		return false
	}
	return a.IP.IsPrivate() || a.IP.IsLinkLocalUnicast() || a.IP.IsLinkLocalMulticast()
}

// ==== Cached resolver ====

type cacheEntry struct {
	addrs  []Addr
	expiry time.Time
}

var (
	cacheMu  sync.RWMutex
	cache    = make(map[string]cacheEntry)
	cacheTTL = 60 * time.Second
)

// SetCacheTTL configures the resolver cache TTL.
func SetCacheTTL(d time.Duration) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cacheTTL = d
}

// ClearCache removes all cached resolutions.
func ClearCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cache = make(map[string]cacheEntry)
}

// Resolve returns all resolved addresses for a host.
// Results are cached for the configured TTL.
func Resolve(ctx context.Context, host string) ([]Addr, error) {
	// Check cache
	cacheMu.RLock()
	if entry, ok := cache[host]; ok && time.Now().Before(entry.expiry) {
		cacheMu.RUnlock()
		result := make([]Addr, len(entry.addrs))
		copy(result, entry.addrs)
		return result, nil
	}
	cacheMu.RUnlock()

	// Split host and port if present
	actualHost := host
	port := 0
	if h, p, err := net.SplitHostPort(host); err == nil {
		actualHost = h
		if n, perr := strconv.Atoi(p); perr == nil {
			port = n
		}
	}

	// Check if already an IP literal
	if ip := net.ParseIP(actualHost); ip != nil {
		addr := Addr{IP: ip, Port: port, Host: actualHost, Family: detectFamily(ip)}
		return []Addr{addr}, nil
	}

	// DNS lookup
	resolver := net.Resolver{}
	ips, err := resolver.LookupIPAddr(ctx, actualHost)
	if err != nil {
		return nil, err
	}

	result := make([]Addr, 0, len(ips))
	for _, ip := range ips {
		addr := Addr{
			IP:     ip.IP,
			Port:   port,
			Host:   actualHost,
			Family: detectFamily(ip.IP),
		}
		result = append(result, addr)
	}

	// Cache
	cacheMu.Lock()
	cache[host] = cacheEntry{
		addrs:  result,
		expiry: time.Now().Add(cacheTTL),
	}
	cacheMu.Unlock()

	return result, nil
}

// ResolveFirst returns the first resolved address for a host.
func ResolveFirst(ctx context.Context, host string) (Addr, error) {
	addrs, err := Resolve(ctx, host)
	if err != nil {
		return Addr{}, err
	}
	if len(addrs) == 0 {
		return Addr{}, fmt.Errorf("no addresses found for %s", host)
	}
	return addrs[0], nil
}

// Reverse performs a reverse DNS lookup.
func Reverse(ctx context.Context, ip net.IP) ([]string, error) {
	resolver := net.Resolver{}
	return resolver.LookupAddr(ctx, ip.String())
}

// detectFamily determines the IP family.
func detectFamily(ip net.IP) Family {
	if ip == nil {
		return FamilyUnknown
	}
	if ip.To4() != nil {
		return FamilyIPv4
	}
	return FamilyIPv6
}

// IsIPv6 returns true if s looks like an IPv6 literal (possibly in brackets).
func IsIPv6(s string) bool {
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	return ip.To4() == nil
}

// IsIPLiteral returns true if s is an IP literal (v4 or v6).
func IsIPLiteral(s string) bool {
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	return net.ParseIP(s) != nil
}

// FormatHost returns "host" or "[ipv6]" depending on the type.
func FormatHost(host string) string {
	if IsIPv6(host) {
		if !strings.HasPrefix(host, "[") {
			return "[" + host + "]"
		}
	}
	return host
}
