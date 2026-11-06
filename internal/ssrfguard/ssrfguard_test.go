package ssrfguard

import (
	"net"
	"testing"
)

func TestIsSafeIP_Loopback(t *testing.T) {
	g := Default()

	// 127.0.0.1
	ip := net.ParseIP("127.0.0.1")
	if ok, _ := g.IsSafeIP(ip); ok {
		t.Error("127.0.0.1 should be blocked")
	}

	// ::1
	ip = net.ParseIP("::1")
	if ok, _ := g.IsSafeIP(ip); ok {
		t.Error("::1 should be blocked")
	}
}

func TestIsSafeIP_Private(t *testing.T) {
	g := Default()

	privateIPs := []string{
		"10.0.0.1",
		"172.16.0.1",
		"192.168.1.1",
	}

	for _, ipStr := range privateIPs {
		ip := net.ParseIP(ipStr)
		if ok, _ := g.IsSafeIP(ip); ok {
			t.Errorf("%s should be blocked", ipStr)
		}
	}
}

func TestIsSafeIP_LinkLocal(t *testing.T) {
	g := Default()

	ip := net.ParseIP("169.254.169.254")
	if ok, reason := g.IsSafeIP(ip); ok {
		t.Errorf("metadata IP should be blocked, reason: %s", reason)
	}
}

func TestIsSafeIP_Public(t *testing.T) {
	g := Default()

	publicIPs := []string{
		"8.8.8.8",     // Google DNS
		"1.1.1.1",     // Cloudflare DNS
		"151.101.0.1", // Fastly
	}

	for _, ipStr := range publicIPs {
		ip := net.ParseIP(ipStr)
		if ok, reason := g.IsSafeIP(ip); !ok {
			t.Errorf("%s should be allowed; reason: %s", ipStr, reason)
		}
	}
}

func TestIsSafeIP_Unspecified(t *testing.T) {
	g := Default()

	ip := net.ParseIP("0.0.0.0")
	if ok, _ := g.IsSafeIP(ip); ok {
		t.Error("0.0.0.0 should be blocked")
	}
}

func TestIsSafeHost_Hostname(t *testing.T) {
	g := Default()

	// Public hostname (can't verify without DNS, but should be allowed in pre-flight)
	if ok, _ := g.IsSafeHost("google.com"); !ok {
		t.Error("google.com should be allowed in pre-flight check")
	}
}

func TestIsSafeHost_Localhost(t *testing.T) {
	g := Default()

	if ok, _ := g.IsSafeHost("localhost"); ok {
		t.Error("localhost should be blocked")
	}
}

func TestIsSafeHost_IPLiteral(t *testing.T) {
	g := Default()

	if ok, _ := g.IsSafeHost("127.0.0.1"); ok {
		t.Error("IP literal 127.0.0.1 should be blocked")
	}
	if ok, _ := g.IsSafeHost("8.8.8.8"); !ok {
		t.Error("IP literal 8.8.8.8 should be allowed")
	}
}

func TestAllowLoopback(t *testing.T) {
	cfg := Config{AllowLoopback: true}
	g := NewGuard(cfg)

	ip := net.ParseIP("127.0.0.1")
	if ok, _ := g.IsSafeIP(ip); !ok {
		t.Error("loopback should be allowed when AllowLoopback=true")
	}
}

func TestAllowPrivate(t *testing.T) {
	cfg := Config{AllowPrivate: true}
	g := NewGuard(cfg)

	ip := net.ParseIP("192.168.1.1")
	if ok, _ := g.IsSafeIP(ip); !ok {
		t.Error("private IP should be allowed when AllowPrivate=true")
	}
}

func TestAllowlist(t *testing.T) {
	cfg := Config{
		Allowlist: []string{"127.0.0.1"},
	}
	g := NewGuard(cfg)

	ip := net.ParseIP("127.0.0.1")
	if ok, _ := g.IsSafeIP(ip); !ok {
		t.Error("allowlisted IP should be allowed")
	}
}

func TestDenylist(t *testing.T) {
	cfg := Config{
		Denylist: []string{"8.8.8.8"},
	}
	g := NewGuard(cfg)

	ip := net.ParseIP("8.8.8.8")
	if ok, _ := g.IsSafeIP(ip); ok {
		t.Error("denylisted IP should be blocked")
	}
}

func TestMetadataIPs(t *testing.T) {
	g := Default()

	metadataIPs := []string{
		"169.254.169.254", // AWS, GCP, Azure
		"169.254.170.2",   // AWS ECS
		"100.100.100.200", // Alibaba
	}

	for _, ipStr := range metadataIPs {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			t.Errorf("can't parse %s", ipStr)
			continue
		}
		if ok, _ := g.IsSafeIP(ip); ok {
			t.Errorf("metadata IP %s should be blocked", ipStr)
		}
	}
}

func TestNew(t *testing.T) {
	g := NewGuard(DefaultConfig())
	if g == nil {
		t.Error("NewGuard should return non-nil")
	}
}

func TestDefault(t *testing.T) {
	g := Default()
	if g == nil {
		t.Error("Default should return non-nil")
	}
}

func TestGlobalIsSafeHost(t *testing.T) {
	if ok, _ := IsSafeHost("google.com"); !ok {
		t.Error("global IsSafeHost should allow google.com")
	}
	if ok, _ := IsSafeHost("localhost"); ok {
		t.Error("global IsSafeHost should block localhost")
	}
}

func TestGlobalIsSafeIP(t *testing.T) {
	if ok, _ := IsSafeIP(net.ParseIP("8.8.8.8")); !ok {
		t.Error("global IsSafeIP should allow 8.8.8.8")
	}
	if ok, _ := IsSafeIP(net.ParseIP("127.0.0.1")); ok {
		t.Error("global IsSafeIP should block 127.0.0.1")
	}
}
