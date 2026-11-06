package httputil

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// ProxyConfig represents proxy settings for HTTP clients.
type ProxyConfig struct {
	// URL is the proxy URL (e.g. "http://proxy.corp:8080").
	URL string
	// NoProxy is a comma-separated list of hosts/domains/CIDRs that bypass the proxy.
	NoProxy string
}

// DetectProxy reads proxy configuration from environment variables.
// Preference: https_proxy > HTTPS_PROXY > http_proxy > HTTP_PROXY.
func DetectProxy() *ProxyConfig {
	proxyURL := firstNonEmpty(
		os.Getenv("https_proxy"),
		os.Getenv("HTTPS_PROXY"),
		os.Getenv("http_proxy"),
		os.Getenv("HTTP_PROXY"),
	)
	if proxyURL == "" {
		return nil
	}
	noProxy := firstNonEmpty(os.Getenv("no_proxy"), os.Getenv("NO_PROXY"))
	return &ProxyConfig{URL: proxyURL, NoProxy: noProxy}
}

// ProxyFunc returns an http.Transport-compatible proxy function that respects
// the NoProxy list.
func (p *ProxyConfig) ProxyFunc() func(*http.Request) (*url.URL, error) {
	if p == nil || p.URL == "" {
		return nil
	}
	proxyURL, err := url.Parse(p.URL)
	if err != nil {
		return nil
	}
	return func(req *http.Request) (*url.URL, error) {
		if ShouldBypassProxy(req.URL.String(), p.NoProxy) {
			return nil, nil
		}
		return proxyURL, nil
	}
}

// ShouldBypassProxy checks if a URL should bypass the proxy based on noProxy.
// Supports:
//   - Exact hostname ("localhost")
//   - Domain suffix (".example.com")
//   - Wildcard "*"
//   - Port-specific ("example.com:8080")
//   - IP addresses ("127.0.0.1")
//   - CIDR ranges ("10.0.0.0/8")
func ShouldBypassProxy(rawURL, noProxy string) bool {
	if noProxy == "" {
		return false
	}
	if noProxy == "*" {
		return true
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	hostname := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	hostWithPort := hostname + ":" + port

	entries := strings.FieldsFunc(noProxy, func(r rune) bool {
		return r == ',' || r == ' '
	})

	hostIP := net.ParseIP(hostname)

	for _, pattern := range entries {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}

		// CIDR match
		if strings.Contains(pattern, "/") {
			_, cidr, err := net.ParseCIDR(pattern)
			if err == nil && hostIP != nil && cidr.Contains(hostIP) {
				return true
			}
			continue
		}

		// Port-specific match
		if strings.Contains(pattern, ":") {
			if hostWithPort == pattern {
				return true
			}
			continue
		}

		// Domain suffix match (.example.com)
		if strings.HasPrefix(pattern, ".") {
			if hostname == pattern[1:] || strings.HasSuffix(hostname, pattern) {
				return true
			}
			continue
		}

		// Exact hostname / IP match
		if hostname == pattern {
			return true
		}
	}
	return false
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
