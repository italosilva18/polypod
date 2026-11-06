package sshexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// writeSSHConfig creates a fake ~/.ssh/config under a temp HOME and
// returns the temp home dir. It sets HOME for the duration of the test.
func writeSSHConfig(t *testing.T, content string) string {
	t.Helper()
	tmp := t.TempDir()
	sshDir := filepath.Join(tmp, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	if content != "" {
		if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(content), 0600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	t.Setenv("HOME", tmp)
	return tmp
}

func TestDiscoverHosts_ParsesMultipleHosts(t *testing.T) {
	cfg := `# comment line
Host alpha
    HostName alpha.example.com
    User alice
    Port 2222

Host beta
    HostName beta.example.com
    User bob

Host gamma
    HostName 10.0.0.5
`
	writeSSHConfig(t, cfg)

	hosts := DiscoverHosts()
	if len(hosts) != 3 {
		t.Fatalf("expected 3 hosts, got %d: %+v", len(hosts), hosts)
	}

	byName := map[string]Host{}
	for _, h := range hosts {
		byName[h.Name] = h
	}

	if h, ok := byName["alpha"]; !ok {
		t.Error("alpha host not discovered")
	} else {
		if h.Hostname != "alpha.example.com" {
			t.Errorf("alpha hostname: got %q", h.Hostname)
		}
		if h.User != "alice" {
			t.Errorf("alpha user: got %q", h.User)
		}
		if h.Port != "2222" {
			t.Errorf("alpha port: got %q", h.Port)
		}
	}

	if h, ok := byName["beta"]; !ok {
		t.Error("beta host not discovered")
	} else if h.User != "bob" || h.Hostname != "beta.example.com" {
		t.Errorf("beta host unexpected: %+v", h)
	}

	if h, ok := byName["gamma"]; !ok {
		t.Error("gamma host not discovered")
	} else if h.Hostname != "10.0.0.5" {
		t.Errorf("gamma hostname: got %q", h.Hostname)
	}
}

func TestDiscoverHosts_SkipsWildcardHost(t *testing.T) {
	cfg := `Host *
    User defaultuser
    Port 22

Host realhost
    HostName real.example.com
    User real
`
	writeSSHConfig(t, cfg)

	hosts := DiscoverHosts()
	if len(hosts) != 1 {
		t.Fatalf("expected only 1 non-wildcard host, got %d: %+v", len(hosts), hosts)
	}
	if hosts[0].Name != "realhost" {
		t.Errorf("expected realhost, got %q", hosts[0].Name)
	}
	// Wildcard fields must NOT leak into the real host block.
	if hosts[0].User != "real" {
		t.Errorf("expected user=real, got %q", hosts[0].User)
	}
}

func TestDiscoverHosts_MissingConfigReturnsNil(t *testing.T) {
	// Empty content => no config file written at all.
	writeSSHConfig(t, "")

	hosts := DiscoverHosts()
	if hosts != nil {
		t.Errorf("expected nil when ~/.ssh/config absent, got %+v", hosts)
	}
}

func TestDiscoverHosts_IgnoresCommentsAndBlankLines(t *testing.T) {
	cfg := `
# top level comment

Host solo
    # inline comment line
    HostName solo.example.com

`
	writeSSHConfig(t, cfg)

	hosts := DiscoverHosts()
	if len(hosts) != 1 {
		t.Fatalf("expected 1 host, got %d", len(hosts))
	}
	if hosts[0].Name != "solo" || hosts[0].Hostname != "solo.example.com" {
		t.Errorf("unexpected host: %+v", hosts[0])
	}
}

func TestRegisterSkills_RegistersExpectedSkills(t *testing.T) {
	reg := skill.NewRegistry()
	RegisterSkills(reg)

	expected := []string{"ssh_exec", "ssh_hosts", "ssh_copy"}
	for _, name := range expected {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("skill %q was not registered", name)
		}
	}
}

func TestSSHExecSkill_ParameterSchema(t *testing.T) {
	reg := skill.NewRegistry()
	RegisterSkills(reg)

	s, ok := reg.Get("ssh_exec")
	if !ok {
		t.Fatal("ssh_exec skill not registered")
	}
	if s.Parameters.Type != jsonschema.Object {
		t.Errorf("expected Object parameter type, got %v", s.Parameters.Type)
	}
	for _, field := range []string{"host", "command", "user", "port"} {
		if _, ok := s.Parameters.Properties[field]; !ok {
			t.Errorf("ssh_exec missing property %q", field)
		}
	}
	req := map[string]bool{}
	for _, r := range s.Parameters.Required {
		req[r] = true
	}
	if !req["host"] || !req["command"] {
		t.Errorf("ssh_exec required fields should include host+command, got %v", s.Parameters.Required)
	}
	if req["user"] || req["port"] {
		t.Errorf("user/port should be optional, got required=%v", s.Parameters.Required)
	}
	if !strings.Contains(strings.ToLower(s.Description), "ssh") {
		t.Errorf("description should mention SSH: %q", s.Description)
	}
}

func TestSSHCopySkill_ParameterSchema(t *testing.T) {
	reg := skill.NewRegistry()
	RegisterSkills(reg)

	s, ok := reg.Get("ssh_copy")
	if !ok {
		t.Fatal("ssh_copy skill not registered")
	}
	for _, field := range []string{"source", "dest"} {
		if _, ok := s.Parameters.Properties[field]; !ok {
			t.Errorf("ssh_copy missing property %q", field)
		}
	}
	req := map[string]bool{}
	for _, r := range s.Parameters.Required {
		req[r] = true
	}
	if !req["source"] || !req["dest"] {
		t.Errorf("ssh_copy must require source+dest, got %v", s.Parameters.Required)
	}
}

func TestSSHHostsSkill_EmptyConfigMessage(t *testing.T) {
	// Point HOME at an empty dir so ssh_hosts sees no hosts.
	writeSSHConfig(t, "")

	reg := skill.NewRegistry()
	RegisterSkills(reg)

	s, ok := reg.Get("ssh_hosts")
	if !ok {
		t.Fatal("ssh_hosts skill not registered")
	}
	out, err := s.Execute(map[string]string{})
	if err != nil {
		t.Fatalf("execute ssh_hosts: %v", err)
	}
	if !strings.Contains(out, "Nenhum host") {
		t.Errorf("expected empty-host message, got %q", out)
	}
}

func TestSSHHostsSkill_FormatsConfiguredHosts(t *testing.T) {
	cfg := `Host dev
    HostName dev.example.com
    User devuser
    Port 2200

Host prod
    HostName prod.example.com
`
	writeSSHConfig(t, cfg)

	reg := skill.NewRegistry()
	RegisterSkills(reg)

	s, _ := reg.Get("ssh_hosts")
	out, err := s.Execute(map[string]string{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Expect markdown header and both hosts with formatted lines.
	if !strings.Contains(out, "## Hosts SSH") {
		t.Errorf("missing markdown header in output:\n%s", out)
	}
	if !strings.Contains(out, "**dev**") || !strings.Contains(out, "devuser@dev.example.com:2200") {
		t.Errorf("dev host not formatted correctly:\n%s", out)
	}
	// prod uses defaults: user "(default)" and port "22".
	if !strings.Contains(out, "**prod**") {
		t.Errorf("prod host missing from output:\n%s", out)
	}
	if !strings.Contains(out, "(default)@prod.example.com:22") {
		t.Errorf("prod host should use default user/port, got:\n%s", out)
	}
}

func TestHostStruct_ZeroValue(t *testing.T) {
	// Structural sanity: a zero Host is usable and has empty fields.
	var h Host
	if h.Name != "" || h.Hostname != "" || h.User != "" || h.Port != "" {
		t.Errorf("zero Host should have all empty fields, got %+v", h)
	}
	// And assignment works.
	h = Host{Name: "x", Hostname: "y", User: "u", Port: "p"}
	if h.Name != "x" || h.Hostname != "y" || h.User != "u" || h.Port != "p" {
		t.Errorf("unexpected field values: %+v", h)
	}
}
