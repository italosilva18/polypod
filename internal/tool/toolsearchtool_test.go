package tool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/types"
)

// buildDescribedStubTool creates a stub tool with a custom description so
// that the tool_search scorer has something meaningful to match against.
func buildDescribedStubTool(name, description string) types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn:        func() string { return name },
		DescriptionFn: func() string { return description },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"foo": map[string]any{"type": "string"},
				},
			}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			return types.ToolResult{Content: "ok"}, nil
		},
	})
}

// decodeSearchOutput unmarshals the JSON envelope returned by tool_search.
func decodeSearchOutput(t *testing.T, content string) toolSearchOutput {
	t.Helper()
	var out toolSearchOutput
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		t.Fatalf("failed to unmarshal tool_search output: %v\ncontent=%s", err, content)
	}
	return out
}

func TestToolSearch_KeywordMatchOrdering(t *testing.T) {
	reg := NewRegistry()
	RegisterToolSearchTool(reg)

	// The highest-scoring tool has the query term in both name AND description.
	reg.Register(buildDescribedStubTool("GitStatus", "Show git repository status and staged files"))
	// Name-only match for "git" (weight 2).
	reg.Register(buildDescribedStubTool("GitCommit", "Create a new snapshot of the working tree"))
	// Description-only match for "git" (weight 1).
	reg.Register(buildDescribedStubTool("ReadFile", "Read a file from disk using git-friendly paths"))
	// Unrelated tool should not appear in results.
	reg.Register(buildDescribedStubTool("Bash", "Execute arbitrary shell commands"))

	res, err := reg.Execute(ToolSearchToolName, map[string]any{
		"query":       "git status",
		"max_results": 5,
	}, testToolContext())
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %q", res.Content)
	}

	out := decodeSearchOutput(t, res.Content)
	if out.QueryType != "keyword" {
		t.Errorf("QueryType = %q, want keyword", out.QueryType)
	}
	if len(out.Matches) == 0 {
		t.Fatal("expected at least one match for keyword search")
	}

	// GitStatus matches both "git" and "status" in the name and "status" in
	// the description — it should rank first.
	if out.Matches[0].Name != "GitStatus" {
		t.Errorf("first match = %q, want GitStatus", out.Matches[0].Name)
	}
	// GitCommit only matches "git" — should rank above ReadFile and Bash.
	found := map[string]int{}
	for i, m := range out.Matches {
		found[m.Name] = i
	}
	if _, ok := found["GitCommit"]; !ok {
		t.Errorf("expected GitCommit in matches, got names: %v", matchNames(out.Matches))
	}
	if _, ok := found["Bash"]; ok {
		t.Errorf("Bash should not appear in results: got %v", matchNames(out.Matches))
	}
	if gi, ok := found["GitStatus"]; ok {
		if ci, ok := found["GitCommit"]; ok && gi >= ci {
			t.Errorf("GitStatus should rank strictly higher than GitCommit (got indices %d vs %d)", gi, ci)
		}
	}

	// Input schema should be included in the match for discoverability.
	if out.Matches[0].InputSchema == nil {
		t.Error("expected first match to carry an input_schema")
	}
	// Description should be propagated.
	if !strings.Contains(out.Matches[0].Description, "status") {
		t.Errorf("first match Description = %q, want contains 'status'", out.Matches[0].Description)
	}
}

func TestToolSearch_SelectForm(t *testing.T) {
	reg := NewRegistry()
	RegisterToolSearchTool(reg)
	reg.Register(buildDescribedStubTool("Alpha", "first tool"))
	reg.Register(buildDescribedStubTool("Beta", "second tool"))
	reg.Register(buildDescribedStubTool("Gamma", "third tool"))

	res, err := reg.Execute(ToolSearchToolName, map[string]any{
		"query": "select:Alpha,Gamma,Missing",
	}, testToolContext())
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %q", res.Content)
	}

	out := decodeSearchOutput(t, res.Content)
	if out.QueryType != "select" {
		t.Errorf("QueryType = %q, want select", out.QueryType)
	}
	if len(out.Matches) != 2 {
		t.Fatalf("Matches length = %d, want 2 (Alpha, Gamma); missing tools should be dropped; got %v",
			len(out.Matches), matchNames(out.Matches))
	}

	gotNames := matchNames(out.Matches)
	// Ordering should respect the request order (Alpha before Gamma).
	if gotNames[0] != "Alpha" {
		t.Errorf("Matches[0] = %q, want Alpha", gotNames[0])
	}
	if gotNames[1] != "Gamma" {
		t.Errorf("Matches[1] = %q, want Gamma", gotNames[1])
	}
	// Beta was not requested and should not appear.
	for _, n := range gotNames {
		if n == "Beta" {
			t.Errorf("Beta should not be selected; got %v", gotNames)
		}
	}

	// Schemas and descriptions should be populated on select hits.
	for _, m := range out.Matches {
		if m.InputSchema == nil {
			t.Errorf("match %q missing input_schema", m.Name)
		}
		if m.Description == "" {
			t.Errorf("match %q missing description", m.Name)
		}
	}
}

func TestToolSearch_ValidationRejectsEmptyQuery(t *testing.T) {
	reg := NewRegistry()
	RegisterToolSearchTool(reg)

	res, err := reg.Execute(ToolSearchToolName, map[string]any{"query": "   "}, testToolContext())
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for empty query, got content=%q", res.Content)
	}
}

// matchNames is a small helper used across test assertions.
func matchNames(matches []toolSearchMatch) []string {
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m.Name
	}
	return out
}
