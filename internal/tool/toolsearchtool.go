package tool

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/types"
)

// ToolSearchToolName is the public name of the tool_search meta-tool.
// The agent uses it to discover deferred tools by keyword or direct
// selection, so it can request schemas on demand instead of loading
// the full tool catalog upfront.
const ToolSearchToolName = "tool_search"

// toolSearchMatch is a single search hit returned in the JSON output.
type toolSearchMatch struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Score       int            `json:"score,omitempty"`
}

// toolSearchOutput is the full envelope returned to the caller.
type toolSearchOutput struct {
	Query      string            `json:"query"`
	QueryType  string            `json:"query_type"`
	TotalTools int               `json:"total_tools"`
	Matches    []toolSearchMatch `json:"matches"`
}

// camelCaseSplit splits CamelCase and snake_case identifiers into lowercase
// word parts, matching the TS parseToolName behaviour.
var camelCaseSplit = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// splitToolName produces lowercase word parts for a tool name. It handles
// MCP-style `mcp__server__action`, snake_case, and CamelCase simultaneously.
func splitToolName(name string) []string {
	normalized := name
	if strings.HasPrefix(normalized, "mcp__") {
		normalized = strings.TrimPrefix(normalized, "mcp__")
	}
	normalized = camelCaseSplit.ReplaceAllString(normalized, "$1 $2")
	normalized = strings.ReplaceAll(normalized, "__", " ")
	normalized = strings.ReplaceAll(normalized, "_", " ")
	normalized = strings.ReplaceAll(normalized, "-", " ")
	normalized = strings.ToLower(normalized)
	parts := strings.Fields(normalized)
	return parts
}

// splitQueryTerms tokenises a free-text query into lowercase search terms.
func splitQueryTerms(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	return strings.Fields(query)
}

// scoreTool assigns a simple term-frequency score to a tool. Matches in the
// name carry weight 2 (they're high-signal), matches in the description carry
// weight 1. The score counts one weighted hit per term per field.
func scoreTool(name, description string, terms []string) int {
	if len(terms) == 0 {
		return 0
	}
	nameParts := splitToolName(name)
	nameJoined := strings.Join(nameParts, " ")
	descLower := strings.ToLower(description)

	score := 0
	for _, term := range terms {
		if term == "" {
			continue
		}
		// Name match: exact part (weight 2) or substring (weight 1).
		matched := false
		for _, part := range nameParts {
			if part == term {
				score += 2
				matched = true
				break
			}
		}
		if !matched && strings.Contains(nameJoined, term) {
			score += 1
			matched = true
		}
		// Description match: weight 1.
		if strings.Contains(descLower, term) {
			score += 1
		}
	}
	return score
}

// toolDescription safely reads the description off any types.Tool
// implementation. The interface exposes Description() directly, so this is a
// thin wrapper that tolerates nil / panicking implementations.
func toolDescription(t types.Tool) (desc string) {
	if t == nil {
		return ""
	}
	defer func() {
		if r := recover(); r != nil {
			desc = ""
		}
	}()
	return t.Description()
}

// toolInputSchema safely reads the input schema off any types.Tool.
func toolInputSchema(t types.Tool) (schema map[string]any) {
	if t == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			schema = nil
		}
	}()
	return t.InputSchema()
}

// registrySnapshot returns every enabled tool in the registry paired with its
// description and input schema. Used by the search path to build candidates.
func registrySnapshot(reg *Registry) []toolSearchMatch {
	if reg == nil {
		return nil
	}
	names := reg.List()
	out := make([]toolSearchMatch, 0, len(names))
	for _, name := range names {
		t, ok := reg.Get(name)
		if !ok || t == nil {
			continue
		}
		if !t.IsEnabled() {
			continue
		}
		out = append(out, toolSearchMatch{
			Name:        t.Name(),
			Description: toolDescription(t),
			InputSchema: toolInputSchema(t),
		})
	}
	return out
}

// parseSelectQuery detects the `select:a,b,c` prefix and returns the requested
// tool names. Returns (nil, false) when the query is not a select query.
func parseSelectQuery(query string) ([]string, bool) {
	trimmed := strings.TrimSpace(query)
	const prefix = "select:"
	if len(trimmed) < len(prefix) || !strings.EqualFold(trimmed[:len(prefix)], prefix) {
		return nil, false
	}
	rest := trimmed[len(prefix):]
	parts := strings.Split(rest, ",")
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			names = append(names, p)
		}
	}
	return names, true
}

// searchToolRegistry runs the full search pipeline against a registry. It is
// exported at package scope so tests can exercise it directly.
func searchToolRegistry(reg *Registry, query string, maxResults int) toolSearchOutput {
	if maxResults <= 0 {
		maxResults = 5
	}
	snapshot := registrySnapshot(reg)

	out := toolSearchOutput{
		Query:      query,
		TotalTools: len(snapshot),
		Matches:    []toolSearchMatch{},
	}

	// --- select: direct lookup --------------------------------------------------
	if requested, ok := parseSelectQuery(query); ok {
		out.QueryType = "select"
		byName := make(map[string]toolSearchMatch, len(snapshot))
		for _, m := range snapshot {
			byName[m.Name] = m
		}
		seen := make(map[string]bool, len(requested))
		for _, name := range requested {
			if seen[name] {
				continue
			}
			seen[name] = true
			if m, ok := byName[name]; ok {
				out.Matches = append(out.Matches, m)
			}
		}
		return out
	}

	// --- keyword search ---------------------------------------------------------
	out.QueryType = "keyword"
	terms := splitQueryTerms(query)
	if len(terms) == 0 {
		return out
	}

	scored := make([]toolSearchMatch, 0, len(snapshot))
	for _, m := range snapshot {
		s := scoreTool(m.Name, m.Description, terms)
		if s > 0 {
			m.Score = s
			scored = append(scored, m)
		}
	}

	// Stable ordering: score desc, then name asc for determinism.
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Name < scored[j].Name
	})

	if len(scored) > maxResults {
		scored = scored[:maxResults]
	}
	out.Matches = scored
	return out
}

// RegisterToolSearchTool wires the tool_search meta-tool into the registry.
// The closure captures the registry so the tool can introspect it at call time.
func RegisterToolSearchTool(reg *Registry) {
	if reg == nil {
		return
	}
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return ToolSearchToolName },
		DescriptionFn: func() string {
			return "Discover tools by keyword or direct selection. " +
				"Use `select:name1,name2` to fetch schemas for known tools, " +
				"or free-text keywords to find tools by name and description."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Free-text keywords or `select:name1,name2` to fetch specific tools.",
					},
					"max_results": map[string]any{
						"type":        "integer",
						"description": "Maximum number of matches to return (default 5).",
						"default":     5,
					},
				},
				"required": []string{"query"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			q, ok := args["query"].(string)
			if !ok || strings.TrimSpace(q) == "" {
				return types.ValidationResult{Valid: false, Message: "query is required"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			query, _ := args["query"].(string)
			maxResults := 5
			switch v := args["max_results"].(type) {
			case float64:
				maxResults = int(v)
			case int:
				maxResults = v
			case int64:
				maxResults = int(v)
			}
			if maxResults <= 0 {
				maxResults = 5
			}

			result := searchToolRegistry(reg, query, maxResults)
			payload, err := json.Marshal(result)
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("erro ao serializar resultado: %v", err),
					IsError: true,
				}, nil
			}
			return types.ToolResult{Content: string(payload)}, nil
		},
	})
}
