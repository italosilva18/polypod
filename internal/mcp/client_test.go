package mcp

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ParseMCPAddress tests
// ---------------------------------------------------------------------------

func TestParseMCPAddress_Stdio(t *testing.T) {
	ep, err := ParseMCPAddress("stdio:mcp-filesystem /tmp")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ep.Scheme != "stdio" {
		t.Errorf("Scheme = %q; want stdio", ep.Scheme)
	}
	if ep.Command != "mcp-filesystem" {
		t.Errorf("Command = %q; want mcp-filesystem", ep.Command)
	}
	if len(ep.Args) != 1 || ep.Args[0] != "/tmp" {
		t.Errorf("Args = %v; want [/tmp]", ep.Args)
	}
	if ep.Raw != "stdio:mcp-filesystem /tmp" {
		t.Errorf("Raw not preserved: %q", ep.Raw)
	}
}

func TestParseMCPAddress_StdioNoArgs(t *testing.T) {
	ep, err := ParseMCPAddress("stdio:mcp-server")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ep.Command != "mcp-server" {
		t.Errorf("Command = %q", ep.Command)
	}
	if len(ep.Args) != 0 {
		t.Errorf("Args = %v; want empty", ep.Args)
	}
}

func TestParseMCPAddress_StdioManyArgs(t *testing.T) {
	ep, err := ParseMCPAddress("stdio:cmd arg1 arg2 arg3")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ep.Command != "cmd" {
		t.Errorf("Command = %q", ep.Command)
	}
	want := []string{"arg1", "arg2", "arg3"}
	if len(ep.Args) != len(want) {
		t.Fatalf("Args = %v; want %v", ep.Args, want)
	}
	for i, a := range want {
		if ep.Args[i] != a {
			t.Errorf("Args[%d] = %q; want %q", i, ep.Args[i], a)
		}
	}
}

func TestParseMCPAddress_HTTP(t *testing.T) {
	ep, err := ParseMCPAddress("https://api.example.com/mcp")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ep.Scheme != "https" {
		t.Errorf("Scheme = %q; want https", ep.Scheme)
	}
	if ep.Addr.Host != "api.example.com" {
		t.Errorf("Host = %q; want api.example.com", ep.Addr.Host)
	}
	if ep.Path != "/mcp" {
		t.Errorf("Path = %q; want /mcp", ep.Path)
	}
	if !ep.IsSecure() {
		t.Error("IsSecure() = false; want true for https")
	}
}

func TestParseMCPAddress_HTTPWithPort(t *testing.T) {
	ep, err := ParseMCPAddress("http://host.local:8080/path/to/mcp")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ep.Scheme != "http" {
		t.Errorf("Scheme = %q", ep.Scheme)
	}
	if ep.Addr.Host != "host.local" {
		t.Errorf("Host = %q", ep.Addr.Host)
	}
	if ep.Addr.Port != 8080 {
		t.Errorf("Port = %d; want 8080", ep.Addr.Port)
	}
	if ep.Path != "/path/to/mcp" {
		t.Errorf("Path = %q", ep.Path)
	}
}

func TestParseMCPAddress_BareHostPort(t *testing.T) {
	ep, err := ParseMCPAddress("localhost:9000")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ep.Scheme != "tcp" {
		t.Errorf("Scheme = %q; want tcp", ep.Scheme)
	}
	if ep.Addr.Host != "localhost" {
		t.Errorf("Host = %q; want localhost", ep.Addr.Host)
	}
	if ep.Addr.Port != 9000 {
		t.Errorf("Port = %d; want 9000", ep.Addr.Port)
	}
}

func TestParseMCPAddress_Invalid(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"stdio:",
		"stdio:   ",
	}
	for _, c := range cases {
		t.Run("case_"+c, func(t *testing.T) {
			if _, err := ParseMCPAddress(c); err == nil {
				t.Errorf("ParseMCPAddress(%q) = nil; want error", c)
			}
		})
	}
}

func TestParseMCPAddress_TrimsSpace(t *testing.T) {
	ep, err := ParseMCPAddress("  stdio:foo  ")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ep.Command != "foo" {
		t.Errorf("Command = %q; want foo", ep.Command)
	}
}

// ---------------------------------------------------------------------------
// ServerEndpoint method tests
// ---------------------------------------------------------------------------

func TestServerEndpoint_IsNetwork(t *testing.T) {
	tests := []struct {
		addr      string
		isNetwork bool
	}{
		{"stdio:foo", false},
		{"http://localhost:8080", true},
		{"https://example.com", true},
		{"tcp://localhost:9000", true},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			ep, err := ParseMCPAddress(tt.addr)
			if err != nil {
				t.Skipf("parse %q: %v", tt.addr, err)
			}
			if ep.IsNetwork() != tt.isNetwork {
				t.Errorf("IsNetwork = %v; want %v", ep.IsNetwork(), tt.isNetwork)
			}
		})
	}
}

func TestServerEndpoint_IsSecure(t *testing.T) {
	tests := []struct {
		addr   string
		secure bool
	}{
		{"https://example.com", true},
		{"http://example.com", false},
		{"stdio:foo", false},
		{"tcp://localhost:9000", false},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			ep, err := ParseMCPAddress(tt.addr)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if ep.IsSecure() != tt.secure {
				t.Errorf("IsSecure = %v; want %v", ep.IsSecure(), tt.secure)
			}
		})
	}
}

func TestServerEndpoint_UsesHTTP(t *testing.T) {
	tests := []struct {
		addr string
		http bool
	}{
		{"http://example.com", true},
		{"https://example.com", true},
		{"stdio:foo", false},
		{"tcp://localhost:9000", false},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			ep, err := ParseMCPAddress(tt.addr)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if ep.UsesHTTP() != tt.http {
				t.Errorf("UsesHTTP = %v; want %v", ep.UsesHTTP(), tt.http)
			}
		})
	}
}

func TestServerEndpoint_UsesStdio(t *testing.T) {
	ep, err := ParseMCPAddress("stdio:cmd arg")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !ep.UsesStdio() {
		t.Error("UsesStdio = false; want true for stdio endpoint")
	}

	ep2, err := ParseMCPAddress("http://example.com")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ep2.UsesStdio() {
		t.Error("UsesStdio = true; want false for http endpoint")
	}
}

func TestServerEndpoint_ConnectString_Stdio(t *testing.T) {
	orig := "stdio:cmd arg1 arg2"
	ep, err := ParseMCPAddress(orig)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := ep.ConnectString()
	if got != orig {
		t.Errorf("ConnectString = %q; want %q", got, orig)
	}
}

func TestServerEndpoint_ConnectString_HTTP(t *testing.T) {
	ep, err := ParseMCPAddress("http://host.local:8080/mcp")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := ep.ConnectString()
	// ConnectString uses Addr.String() which includes port; verify structure
	if !strings.HasPrefix(got, "http://") {
		t.Errorf("ConnectString = %q; want http:// prefix", got)
	}
	if !strings.Contains(got, "host.local") {
		t.Errorf("ConnectString = %q; want to contain host.local", got)
	}
	if !strings.HasSuffix(got, "/mcp") {
		t.Errorf("ConnectString = %q; want /mcp suffix", got)
	}
}

func TestServerEndpoint_DisplayName(t *testing.T) {
	tests := []struct {
		addr    string
		wantSub string // substring that must appear
	}{
		{"stdio:mycmd arg", "stdio:mycmd"},
		{"https://api.example.com/v1", "api.example.com"},
		{"http://host:1234/p", "host"},
		{"localhost:9000", "localhost"},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			ep, err := ParseMCPAddress(tt.addr)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			name := ep.DisplayName()
			if name == "" {
				t.Errorf("DisplayName() empty")
			}
			if !strings.Contains(name, tt.wantSub) {
				t.Errorf("DisplayName() = %q; want to contain %q", name, tt.wantSub)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Manager state tests (no real MCP server)
// ---------------------------------------------------------------------------

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewManager(t *testing.T) {
	m := NewManager(newTestLogger())
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.sessions == nil {
		t.Error("sessions map is nil")
	}
	if m.tools == nil {
		t.Error("tools map is nil")
	}
	if m.client == nil {
		t.Error("client is nil")
	}
}

func TestManager_ListServers_Empty(t *testing.T) {
	m := NewManager(newTestLogger())
	servers := m.ListServers()
	if servers == nil {
		t.Fatal("ListServers returned nil")
	}
	if len(servers) != 0 {
		t.Errorf("ListServers len = %d; want 0 on fresh manager", len(servers))
	}
}

func TestManager_Disconnect_Unknown(t *testing.T) {
	m := NewManager(newTestLogger())
	err := m.Disconnect("does-not-exist")
	if err == nil {
		t.Error("Disconnect unknown = nil; want error")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error = %v; want server name in message", err)
	}
}

func TestManager_Close_Empty(t *testing.T) {
	m := NewManager(newTestLogger())
	if err := m.Close(); err != nil {
		t.Errorf("Close() = %v; want nil on empty manager", err)
	}
	// After Close, maps should still be usable (not nil).
	if servers := m.ListServers(); servers == nil {
		t.Error("ListServers after Close returned nil map")
	}
}

func TestManager_Connect_UnknownTransport(t *testing.T) {
	m := NewManager(newTestLogger())
	cfg := ServerConfig{
		Name:      "bad",
		Transport: "websocket", // not supported
	}
	err := m.Connect(nil, cfg) //nolint:staticcheck // nil ctx acceptable for this error-path test
	if err == nil {
		t.Fatal("Connect with unknown transport = nil; want error")
	}
	if !strings.Contains(err.Error(), "transport") {
		t.Errorf("error = %v; want 'transport' in message", err)
	}
}
