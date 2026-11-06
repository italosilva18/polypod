package skill

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// fakeMCPSource is a deterministic in-memory MCPSkillSource used by tests.
// Each server's prompts and errors can be configured independently; calls
// are recorded so tests can assert the loader dispatched correctly.
type fakeMCPSource struct {
	prompts  map[string][]MCPPrompt
	listErr  map[string]error
	promptFn func(server, name string, args map[string]any) (string, error)

	listCalls []string
	getCalls  []fakeGetCall
}

type fakeGetCall struct {
	server string
	name   string
	args   map[string]any
}

func (f *fakeMCPSource) ListPrompts(_ context.Context, server string) ([]MCPPrompt, error) {
	f.listCalls = append(f.listCalls, server)
	if err, ok := f.listErr[server]; ok && err != nil {
		return nil, err
	}
	return f.prompts[server], nil
}

func (f *fakeMCPSource) GetPrompt(_ context.Context, server, name string, args map[string]any) (string, error) {
	f.getCalls = append(f.getCalls, fakeGetCall{server: server, name: name, args: args})
	if f.promptFn != nil {
		return f.promptFn(server, name, args)
	}
	return "expanded:" + server + "/" + name, nil
}

func TestMCPSkillLoader_LoadAll_AggregatesMultipleServers(t *testing.T) {
	source := &fakeMCPSource{
		prompts: map[string][]MCPPrompt{
			"alpha": {
				{Name: "hello", Description: "Greet the world"},
				{Name: "review", Description: "Review code"},
			},
			"beta": {
				{Name: "summarize", Description: "Summarize a file"},
			},
		},
	}
	loader := NewMCPSkillLoader(source, "alpha", "beta")

	skills, err := loader.LoadAll(context.Background())
	if err != nil {
		t.Fatalf("LoadAll returned unexpected error: %v", err)
	}
	if len(skills) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(skills))
	}

	names := make([]string, 0, len(skills))
	for _, s := range skills {
		names = append(names, s.Frontmatter.Name)
		if s.Body != "" {
			t.Errorf("expected empty body for %q, got %q", s.Frontmatter.Name, s.Body)
		}
		if !strings.HasPrefix(s.SourcePath, "mcp://") {
			t.Errorf("unexpected SourcePath %q", s.SourcePath)
		}
	}
	sort.Strings(names)

	want := []string{
		"mcp__alpha__hello",
		"mcp__alpha__review",
		"mcp__beta__summarize",
	}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("skill[%d] name = %q, want %q", i, names[i], n)
		}
	}

	// Descriptions should be propagated verbatim.
	descByName := map[string]string{}
	for _, s := range skills {
		descByName[s.Frontmatter.Name] = s.Frontmatter.Description
	}
	if descByName["mcp__alpha__hello"] != "Greet the world" {
		t.Errorf("description for alpha/hello = %q", descByName["mcp__alpha__hello"])
	}
	if descByName["mcp__beta__summarize"] != "Summarize a file" {
		t.Errorf("description for beta/summarize = %q", descByName["mcp__beta__summarize"])
	}

	// The loader must query every configured server.
	if len(source.listCalls) != 2 {
		t.Errorf("expected 2 list calls, got %d (%v)", len(source.listCalls), source.listCalls)
	}
}

func TestMCPSkillLoader_LoadAll_ContinuesOnServerError(t *testing.T) {
	boom := errors.New("boom")
	source := &fakeMCPSource{
		prompts: map[string][]MCPPrompt{
			"good":    {{Name: "ok", Description: "fine"}},
			"healthy": {{Name: "ping", Description: "ping"}},
		},
		listErr: map[string]error{
			"broken": boom,
		},
	}
	loader := NewMCPSkillLoader(source, "good", "broken", "healthy")

	skills, err := loader.LoadAll(context.Background())
	if err == nil {
		t.Fatalf("expected joined error, got nil")
	}
	if !errors.Is(err, boom) {
		t.Errorf("joined error does not wrap boom: %v", err)
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("joined error missing server name: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills despite failure, got %d", len(skills))
	}

	// Both healthy servers must have been visited even though "broken"
	// failed in the middle of the slice.
	if len(source.listCalls) != 3 {
		t.Errorf("expected all 3 servers queried, got %d (%v)", len(source.listCalls), source.listCalls)
	}
}

func TestMCPSkillLoader_InvokeSkill_CallsGetPrompt(t *testing.T) {
	source := &fakeMCPSource{
		prompts: map[string][]MCPPrompt{
			"alpha": {{Name: "review", Description: "Review code"}},
		},
		promptFn: func(server, name string, args map[string]any) (string, error) {
			return fmt.Sprintf("srv=%s prompt=%s file=%v", server, name, args["file"]), nil
		},
	}
	loader := NewMCPSkillLoader(source, "alpha")

	skills, err := loader.LoadAll(context.Background())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}

	out, err := loader.InvokeSkill(context.Background(), skills[0], map[string]any{"file": "main.go"})
	if err != nil {
		t.Fatalf("InvokeSkill: %v", err)
	}
	want := "srv=alpha prompt=review file=main.go"
	if out != want {
		t.Errorf("InvokeSkill = %q, want %q", out, want)
	}

	if len(source.getCalls) != 1 {
		t.Fatalf("expected 1 GetPrompt call, got %d", len(source.getCalls))
	}
	call := source.getCalls[0]
	if call.server != "alpha" || call.name != "review" {
		t.Errorf("dispatched to %s/%s, want alpha/review", call.server, call.name)
	}
	if call.args["file"] != "main.go" {
		t.Errorf("args[file] = %v, want main.go", call.args["file"])
	}
}

func TestMCPSkillLoader_InvokeSkill_RejectsNonMCPName(t *testing.T) {
	loader := NewMCPSkillLoader(&fakeMCPSource{}, "alpha")
	fake := &ParsedSkill{Frontmatter: Frontmatter{Name: "read_file"}}
	if _, err := loader.InvokeSkill(context.Background(), fake, nil); err == nil {
		t.Fatalf("expected error for non-MCP skill name, got nil")
	}
}

func TestParseMCPSkillName(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantServer string
		wantPrompt string
		wantOK     bool
	}{
		{"simple", "mcp__alpha__hello", "alpha", "hello", true},
		{"prompt with underscores", "mcp__alpha__review_code", "alpha", "review_code", true},
		{"prompt with double underscore", "mcp__alpha__deep__nested", "alpha", "deep__nested", true},
		{"missing prefix", "alpha__hello", "", "", false},
		{"missing separator", "mcp__alpha_hello", "", "", false},
		{"empty server", "mcp____hello", "", "", false},
		{"empty prompt", "mcp__alpha__", "", "", false},
		{"empty string", "", "", "", false},
		{"just prefix", "mcp__", "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotServer, gotPrompt, gotOK := ParseMCPSkillName(tc.in)
			if gotOK != tc.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tc.wantOK)
			}
			if gotServer != tc.wantServer {
				t.Errorf("server = %q, want %q", gotServer, tc.wantServer)
			}
			if gotPrompt != tc.wantPrompt {
				t.Errorf("prompt = %q, want %q", gotPrompt, tc.wantPrompt)
			}
		})
	}
}
