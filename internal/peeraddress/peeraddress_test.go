package peeraddress

import (
	"testing"
)

func TestParse_IPv4(t *testing.T) {
	addr, err := Parse("1.2.3.4:80")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if addr.Port != 80 {
		t.Errorf("Port = %d; want 80", addr.Port)
	}
	if addr.IP == nil {
		t.Error("IP nil")
	}
	if addr.Family != FamilyIPv4 {
		t.Errorf("Family = %v; want IPv4", addr.Family)
	}
	if addr.Host != "1.2.3.4" {
		t.Errorf("Host = %q; want 1.2.3.4", addr.Host)
	}
}

func TestParse_IPv6Bracketed(t *testing.T) {
	addr, err := Parse("[::1]:80")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if addr.Port != 80 {
		t.Errorf("Port = %d; want 80", addr.Port)
	}
	if addr.Family != FamilyIPv6 {
		t.Errorf("Family = %v; want IPv6", addr.Family)
	}
	if addr.IP == nil {
		t.Error("IP nil")
	}
}

func TestParse_Hostname(t *testing.T) {
	addr, err := Parse("example.com:443")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if addr.Host != "example.com" {
		t.Errorf("Host = %q; want example.com", addr.Host)
	}
	if addr.Port != 443 {
		t.Errorf("Port = %d; want 443", addr.Port)
	}
	if addr.IP != nil {
		t.Error("hostname without IP literal should have nil IP")
	}
	if addr.Family != FamilyUnknown {
		t.Errorf("Family = %v; want Unknown", addr.Family)
	}
}

func TestParse_HostnameNoPort(t *testing.T) {
	addr, err := Parse("example.com")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if addr.Host != "example.com" {
		t.Errorf("Host = %q; want example.com", addr.Host)
	}
	if addr.Port != 0 {
		t.Errorf("Port = %d; want 0", addr.Port)
	}
}

func TestParse_Empty(t *testing.T) {
	_, err := Parse("")
	if err == nil {
		t.Error("empty should error")
	}
}

func TestParse_InvalidPort(t *testing.T) {
	_, err := Parse("1.2.3.4:notaport")
	if err == nil {
		t.Error("invalid port should error")
	}
}

func TestParse_PortOutOfRange(t *testing.T) {
	_, err := Parse("1.2.3.4:99999")
	if err == nil {
		t.Error("port out of range should error")
	}
}

func TestMustParse_Valid(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("MustParse panicked on valid input: %v", r)
		}
	}()
	addr := MustParse("1.2.3.4:80")
	if addr.Port != 80 {
		t.Errorf("Port = %d", addr.Port)
	}
}

func TestMustParse_PanicOnEmpty(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustParse should panic on empty input")
		}
	}()
	MustParse("")
}

func TestIsIPv6(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"::1", true},
		{"[::1]", true},
		{"127.0.0.1", false},
		{"example.com", false},
		{"2001:db8::1", true},
		{"[2001:db8::1]", true},
		{"", false},
	}

	for _, tt := range tests {
		if got := IsIPv6(tt.host); got != tt.want {
			t.Errorf("IsIPv6(%q) = %v; want %v", tt.host, got, tt.want)
		}
	}
}

func TestIsIPLiteral(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"[::1]", true},
		{"::1", true},
		{"1.2.3.4", true},
		{"example.com", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := IsIPLiteral(tt.host); got != tt.want {
			t.Errorf("IsIPLiteral(%q) = %v; want %v", tt.host, got, tt.want)
		}
	}
}

func TestFormatHost(t *testing.T) {
	// IPv4 - unchanged
	if got := FormatHost("127.0.0.1"); got != "127.0.0.1" {
		t.Errorf("FormatHost(v4) = %q; want 127.0.0.1", got)
	}

	// IPv6 - wrapped
	if got := FormatHost("::1"); got != "[::1]" {
		t.Errorf("FormatHost(v6) = %q; want [::1]", got)
	}

	// Already bracketed - unchanged
	if got := FormatHost("[::1]"); got != "[::1]" {
		t.Errorf("FormatHost(bracketed) = %q; want [::1]", got)
	}

	// Hostname - unchanged
	if got := FormatHost("example.com"); got != "example.com" {
		t.Errorf("FormatHost(host) = %q; want example.com", got)
	}
}

func TestAddr_String_IPv4(t *testing.T) {
	addr, err := Parse("1.2.3.4:80")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := addr.String()
	if got != "1.2.3.4:80" {
		t.Errorf("String = %q; want 1.2.3.4:80", got)
	}
}

func TestAddr_String_IPv6(t *testing.T) {
	addr, err := Parse("[::1]:80")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := addr.String()
	if got != "[::1]:80" {
		t.Errorf("String = %q; want [::1]:80", got)
	}
}

func TestAddr_IsResolved(t *testing.T) {
	resolved, _ := Parse("1.2.3.4:80")
	if !resolved.IsResolved() {
		t.Error("IP literal should be resolved")
	}

	unresolved, _ := Parse("example.com:80")
	if unresolved.IsResolved() {
		t.Error("hostname should not be resolved")
	}
}

func TestAddr_IsLiteral(t *testing.T) {
	literal, _ := Parse("1.2.3.4:80")
	if !literal.IsLiteral() {
		t.Error("1.2.3.4 should be literal")
	}

	hostname, _ := Parse("example.com:80")
	if hostname.IsLiteral() {
		t.Error("example.com should not be literal")
	}
}

func TestAddr_IsLoopback(t *testing.T) {
	loopback, _ := Parse("127.0.0.1:80")
	if !loopback.IsLoopback() {
		t.Error("127.0.0.1 should be loopback")
	}

	public, _ := Parse("8.8.8.8:80")
	if public.IsLoopback() {
		t.Error("8.8.8.8 should not be loopback")
	}

	v6loop, _ := Parse("[::1]:80")
	if !v6loop.IsLoopback() {
		t.Error("::1 should be loopback")
	}

	hostname, _ := Parse("example.com:80")
	if hostname.IsLoopback() {
		t.Error("unresolved hostname should not be loopback")
	}
}

func TestAddr_IsPrivate(t *testing.T) {
	private, _ := Parse("192.168.1.1:80")
	if !private.IsPrivate() {
		t.Error("192.168.1.1 should be private")
	}

	private2, _ := Parse("10.0.0.1:80")
	if !private2.IsPrivate() {
		t.Error("10.0.0.1 should be private")
	}

	public, _ := Parse("8.8.8.8:80")
	if public.IsPrivate() {
		t.Error("8.8.8.8 should not be private")
	}

	hostname, _ := Parse("example.com:80")
	if hostname.IsPrivate() {
		t.Error("unresolved hostname should not be private")
	}
}
