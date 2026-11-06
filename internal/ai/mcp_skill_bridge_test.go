package ai

import (
	"context"
	"reflect"
	"testing"

	"github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
)

func TestRichMCPSkillThroughMainBridge(t *testing.T) {
	skills := skill.NewRegistry()
	schema := map[string]any{"type": "object", "additionalProperties": false, "$defs": map[string]any{"target": map[string]any{"type": "string"}}}
	skills.Register(&skill.Skill{Name: "screenshot", InputSchema: schema, ExecuteResult: func(ctx context.Context, args map[string]any) (types.ToolResult, error) {
		if args["count"] != float64(2) || args["enabled"] != true {
			t.Fatalf("typed args lost: %v", args)
		}
		return types.ToolResult{Content: "screen", Images: []types.ToolImage{{MIMEType: "image/png", Base64: "cG5n"}}}, nil
	}})
	registry := tool.NewRegistry()
	// main.go registers MCP skills; wiring.WireAgentRunner bridges them exactly this way.
	tool.RegisterAllSkills(registry, skills)
	adapter := NewToolRegistryAdapter(registry, nil)
	expectedSchema := map[string]any{
		"type": "object", "additionalProperties": false,
		"$defs":      map[string]any{"target": map[string]any{"type": "string"}},
		"properties": map[string]any{}, "required": []string{},
	}
	if got := adapter.ToolDefinitions([]string{"skill_screenshot"})[0].Function.Parameters; !reflect.DeepEqual(got, expectedSchema) {
		t.Fatalf("schema lost: %v", got)
	}
	if _, ok := schema["required"]; ok {
		t.Fatal("normalization mutated the original MCP schema")
	}
	p := &mcpBridgeProvider{}
	client := &Client{responseProvider: p, skills: adapter, toolsOn: true}
	if _, err := client.completeWithResponseTools(context.Background(), []ChatMessage{{Role: "user", Content: "observe"}}, nil, silentLogger()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range p.requests[1].Messages {
		for _, part := range message.Parts {
			if part.Type == "image_base64" && part.ImageB64 == "cG5n" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("MCP image did not reach next request: %+v", p.requests[1].Messages)
	}
}

type mcpBridgeProvider struct {
	provider.Provider
	requests []provider.ChatRequest
}

func (p *mcpBridgeProvider) SupportsVision() bool { return true }
func (p *mcpBridgeProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if len(p.requests) == 1 {
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "screen", Name: "skill_screenshot", Arguments: `{"count":2,"enabled":true}`}}}, nil
	}
	return &provider.ChatResponse{Content: "done"}, nil
}
