package hooks

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// SSRF guard for HTTP hooks.
//
// Blocks private, link-local, and other non-routable address ranges to prevent
// project-configured HTTP hooks from reaching cloud metadata endpoints
// (169.254.169.254) or internal infrastructure.
//
// Loopback (127.0.0.0/8, ::1) is intentionally ALLOWED — local dev policy
// servers are a primary HTTP hook use case.

// IsBlockedAddress returns true if the address is in a range that HTTP hooks
// should not reach.
//
// Blocked IPv4:
//
//	0.0.0.0/8        "this" network
//	10.0.0.0/8       private (RFC 1918)
//	100.64.0.0/10    shared address space / CGNAT (RFC 6598)
//	169.254.0.0/16   link-local (cloud metadata)
//	172.16.0.0/12    private (RFC 1918)
//	192.168.0.0/16   private (RFC 1918)
//
// Blocked IPv6:
//
//	::               unspecified
//	fc00::/7         unique local (RFC 4193)
//	fe80::/10        link-local
//	::ffff:<v4>      mapped IPv4 in a blocked range
//
// Allowed (returns false):
//
//	127.0.0.0/8      loopback (local dev hooks)
//	::1              loopback
//	everything else
func IsBlockedAddress(address string) bool {
	ip := net.ParseIP(address)
	if ip == nil {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		return isBlockedV4(ip4)
	}
	return isBlockedV6(ip)
}

func isBlockedV4(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	a, b := ip4[0], ip4[1]

	// Loopback explicitly allowed (127.0.0.0/8)
	if a == 127 {
		return false
	}

	// 0.0.0.0/8 — "this" network
	if a == 0 {
		return true
	}
	// 10.0.0.0/8 — private (RFC 1918)
	if a == 10 {
		return true
	}
	// 169.254.0.0/16 — link-local, cloud metadata
	if a == 169 && b == 254 {
		return true
	}
	// 172.16.0.0/12 — private (RFC 1918)
	if a == 172 && b >= 16 && b <= 31 {
		return true
	}
	// 100.64.0.0/10 — shared address space (RFC 6598, CGNAT)
	// Some cloud providers use this range for metadata endpoints
	// (e.g., Alibaba Cloud at 100.100.100.200).
	if a == 100 && b >= 64 && b <= 127 {
		return true
	}
	// 192.168.0.0/16 — private (RFC 1918)
	if a == 192 && b == 168 {
		return true
	}

	return false
}

func isBlockedV6(ip net.IP) bool {
	// Normalize to 16-byte representation.
	ip16 := ip.To16()
	if ip16 == nil {
		return false
	}

	// ::1 loopback explicitly allowed
	if ip.Equal(net.IPv6loopback) {
		return false
	}

	// :: unspecified
	if ip.Equal(net.IPv6unspecified) {
		return true
	}

	// IPv4-mapped IPv6 (::ffff:X.Y.Z.W) — extract the embedded IPv4 and
	// delegate to the v4 check. Without this, hex-form mapped addresses
	// (e.g., ::ffff:a9fe:a9fe = 169.254.169.254) bypass the guard.
	if v4 := extractMappedIPv4(ip16); v4 != nil {
		return isBlockedV4(v4)
	}

	// fc00::/7 — unique local addresses (fc00:: through fdff::) — RFC 4193
	if ip16[0] == 0xfc || ip16[0] == 0xfd {
		return true
	}

	// fe80::/10 — link-local
	// The /10 means fe80 through febf. The first hextet's high 10 bits must
	// be 1111111010.
	if ip16[0] == 0xfe && (ip16[1]&0xc0) == 0x80 {
		return true
	}

	return false
}

// extractMappedIPv4 returns the embedded IPv4 address from an IPv4-mapped
// IPv6 address (0:0:0:0:0:ffff:X:Y), or nil if not mapped.
func extractMappedIPv4(ip16 net.IP) net.IP {
	if len(ip16) != 16 {
		return nil
	}
	// First 80 bits must be zero, next 16 bits must be 0xffff
	for i := 0; i < 10; i++ {
		if ip16[i] != 0 {
			return nil
		}
	}
	if ip16[10] != 0xff || ip16[11] != 0xff {
		return nil
	}
	return net.IPv4(ip16[12], ip16[13], ip16[14], ip16[15])
}

// SSRFError is returned when an address is blocked by the SSRF guard.
type SSRFError struct {
	Hostname string
	Address  string
}

func (e *SSRFError) Error() string {
	return fmt.Sprintf(
		"HTTP hook blocked: %s resolves to %s (private/link-local address). Loopback (127.0.0.1, ::1) is allowed for local dev.",
		e.Hostname, e.Address,
	)
}

// ValidateWebhookURL validates that a webhook URL does not target private/internal
// addresses. It resolves the hostname and checks all resolved IPs against the
// blocklist.
func ValidateWebhookURL(ctx context.Context, rawURL string) error {
	host, _, err := extractHostPort(rawURL)
	if err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}

	// If host is already an IP literal, validate directly.
	if ip := net.ParseIP(host); ip != nil {
		if IsBlockedAddress(host) {
			return &SSRFError{Hostname: host, Address: host}
		}
		return nil
	}

	// DNS resolution
	return ResolveAndValidate(ctx, host)
}

// ResolveAndValidate performs DNS resolution and validates all resolved IPs
// against the SSRF blocklist.
func ResolveAndValidate(ctx context.Context, hostname string) error {
	resolver := net.DefaultResolver
	addrs, err := resolver.LookupIPAddr(ctx, hostname)
	if err != nil {
		return fmt.Errorf("DNS lookup failed for %s: %w", hostname, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("DNS lookup returned no addresses for %s", hostname)
	}

	for _, addr := range addrs {
		if IsBlockedAddress(addr.IP.String()) {
			return &SSRFError{Hostname: hostname, Address: addr.IP.String()}
		}
	}
	return nil
}

// SSRFGuardedDialer returns a custom dialer that validates resolved IPs before
// connecting. This is the Go equivalent of the TS ssrfGuardedLookup — by
// validating at dial time, there's no rebinding window between DNS resolution
// and connection.
func SSRFGuardedDialer(baseDialer *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		// If host is already an IP literal, validate directly.
		if ip := net.ParseIP(host); ip != nil {
			if IsBlockedAddress(host) {
				return nil, &SSRFError{Hostname: host, Address: host}
			}
			return baseDialer.DialContext(ctx, network, addr)
		}

		// Resolve DNS and validate all addresses.
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("ENOTFOUND %s", host)
		}

		for _, ip := range ips {
			if IsBlockedAddress(ip.IP.String()) {
				return nil, &SSRFError{Hostname: host, Address: ip.IP.String()}
			}
		}

		// Connect to the first resolved address to avoid rebinding.
		resolved := net.JoinHostPort(ips[0].IP.String(), port)
		return baseDialer.DialContext(ctx, network, resolved)
	}
}

// DomainAllowlist manages an allow/block list for webhook domains.
type DomainAllowlist struct {
	// AllowedPatterns is nil for no restriction; empty slice blocks all;
	// non-empty requires a match. Patterns support * as wildcard.
	AllowedPatterns []string
	// BlockedDomains is an explicit blocklist checked first.
	BlockedDomains []string
}

// IsAllowed checks if a URL is allowed by the domain policy.
func (d *DomainAllowlist) IsAllowed(url string) bool {
	host, _, err := extractHostPort(url)
	if err != nil {
		return false
	}

	// Check blocklist first.
	for _, blocked := range d.BlockedDomains {
		if strings.EqualFold(host, blocked) {
			return false
		}
	}

	// If no allowlist is configured, allow by default.
	if d.AllowedPatterns == nil {
		return true
	}

	// Empty allowlist = block all.
	if len(d.AllowedPatterns) == 0 {
		return false
	}

	// Must match at least one pattern.
	for _, pattern := range d.AllowedPatterns {
		if urlMatchesPattern(url, pattern) {
			return true
		}
	}
	return false
}

// urlMatchesPattern matches a URL against a pattern with * as a wildcard.
// Same semantics as Claude Code's MCP server allowlist patterns.
func urlMatchesPattern(url, pattern string) bool {
	// Escape regex metacharacters except *, then replace * with .*
	var regexStr strings.Builder
	regexStr.WriteString("^")
	for _, ch := range pattern {
		if ch == '*' {
			regexStr.WriteString(".*")
		} else if strings.ContainsRune(".+?^${}()|[]\\", ch) {
			regexStr.WriteRune('\\')
			regexStr.WriteRune(ch)
		} else {
			regexStr.WriteRune(ch)
		}
	}
	regexStr.WriteString("$")

	// Simple implementation: convert pattern to a manual match since we
	// have simple wildcards. For correctness, use regexp but for performance
	// in a hot path, we do a simpler split-on-* approach.
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return url == pattern
	}

	// First part must be a prefix.
	if !strings.HasPrefix(url, parts[0]) {
		return false
	}
	remaining := url[len(parts[0]):]

	// Middle parts must appear in order.
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(remaining, parts[i])
		if idx < 0 {
			return false
		}
		remaining = remaining[idx+len(parts[i]):]
	}

	// Last part must be a suffix.
	return strings.HasSuffix(remaining, parts[len(parts)-1])
}

// extractHostPort extracts host and port from a URL string.
func extractHostPort(rawURL string) (host, port string, err error) {
	// Strip scheme
	url := rawURL
	if idx := strings.Index(url, "://"); idx >= 0 {
		url = url[idx+3:]
	}
	// Strip path
	if idx := strings.Index(url, "/"); idx >= 0 {
		url = url[:idx]
	}
	// Strip userinfo
	if idx := strings.Index(url, "@"); idx >= 0 {
		url = url[idx+1:]
	}

	// Handle IPv6 bracket notation
	if strings.HasPrefix(url, "[") {
		end := strings.Index(url, "]")
		if end < 0 {
			return "", "", fmt.Errorf("invalid IPv6 address in URL")
		}
		host = url[1:end]
		rest := url[end+1:]
		if strings.HasPrefix(rest, ":") {
			port = rest[1:]
		}
		return host, port, nil
	}

	// Try to split host:port
	if h, p, splitErr := net.SplitHostPort(url); splitErr == nil {
		return h, p, nil
	}

	// No port — validate it's a valid host
	host = url
	if host == "" {
		return "", "", fmt.Errorf("empty host in URL")
	}
	return host, "", nil
}

// CloudMetadataEndpoints are well-known cloud metadata service addresses
// that are blocked by the SSRF guard via their IP ranges.
var CloudMetadataEndpoints = []string{
	"169.254.169.254", // AWS, GCP, Azure
	"100.100.100.200", // Alibaba Cloud
	"fd00:ec2::254",   // AWS IMDSv2 IPv6
	"metadata.google.internal",
}

// SanitizeHeaderValue strips CR, LF, and NUL bytes from a header value
// to prevent HTTP header injection (CRLF injection) via env var values.
func SanitizeHeaderValue(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if r == '\r' || r == '\n' || r == 0 {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// InterpolateEnvVars interpolates $VAR_NAME and ${VAR_NAME} patterns in a
// string using the provided env map. Only variable names present in the
// allowedVars set are resolved; others are replaced with empty strings.
// The result is sanitized to strip CR/LF/NUL.
func InterpolateEnvVars(value string, env map[string]string, allowedVars map[string]bool) string {
	result := interpolateEnvVarRefs(value, env, allowedVars)
	return SanitizeHeaderValue(result)
}

func interpolateEnvVarRefs(value string, env map[string]string, allowedVars map[string]bool) string {
	var b strings.Builder
	b.Grow(len(value))
	i := 0
	for i < len(value) {
		if value[i] != '$' {
			b.WriteByte(value[i])
			i++
			continue
		}
		// Try ${VAR_NAME}
		if i+1 < len(value) && value[i+1] == '{' {
			end := strings.Index(value[i+2:], "}")
			if end >= 0 {
				varName := value[i+2 : i+2+end]
				if isValidEnvVarName(varName) {
					if allowedVars[varName] {
						b.WriteString(env[varName])
					}
					// else: not allowed, replace with empty string
					i = i + 2 + end + 1
					continue
				}
			}
		}
		// Try $VAR_NAME
		j := i + 1
		for j < len(value) && isEnvVarChar(value[j]) {
			j++
		}
		if j > i+1 {
			varName := value[i+1 : j]
			if isValidEnvVarName(varName) {
				if allowedVars[varName] {
					b.WriteString(env[varName])
				}
				i = j
				continue
			}
		}
		b.WriteByte(value[i])
		i++
	}
	return b.String()
}

func isValidEnvVarName(s string) bool {
	if len(s) == 0 {
		return false
	}
	first := s[0]
	if !((first >= 'A' && first <= 'Z') || first == '_') {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func isEnvVarChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// IsIPv4Private checks if an IPv4 string is in a private/blocked range.
// Convenience wrapper for testing.
func IsIPv4Private(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	if v4 := parsed.To4(); v4 != nil {
		return isBlockedV4(v4)
	}
	return false
}

// ParsePort parses a port string to an integer, returning 0 for empty/invalid.
func ParsePort(s string) int {
	if s == "" {
		return 0
	}
	p, err := strconv.Atoi(s)
	if err != nil || p < 0 || p > 65535 {
		return 0
	}
	return p
}
