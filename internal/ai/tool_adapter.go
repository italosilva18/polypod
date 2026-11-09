package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/tooloutput"
	"github.com/costa/polypod/internal/types"
	openai "github.com/sashabaranov/go-openai"
)

// ToolRegistryAdapter bridges tool.Registry (types.Tool interface) to the
// SkillRegistry interface expected by ai.Client. This allows the rich tool
// system (with permissions, validation, concurrency safety) to be used by
// the AI client's tool-calling loop.
type ToolRegistryAdapter struct {
	registry           *tool.Registry
	orchestrate        func(name string, args map[string]any) (types.ToolResult, error)
	orchestrateContext func(context.Context, string, map[string]any) (types.ToolResult, error)
}

// NewToolRegistryAdapterWithContext preserves the request's cancellation and
// deadline through the orchestrator and the tool it executes.
func NewToolRegistryAdapterWithContext(reg *tool.Registry, orchestrateFn func(context.Context, string, map[string]any) (types.ToolResult, error)) *ToolRegistryAdapter {
	return &ToolRegistryAdapter{registry: reg, orchestrateContext: orchestrateFn}
}

// NewToolRegistryAdapter creates an adapter that wraps tool.Registry.
// If orchestrateFn is provided, it is used instead of direct tool execution,
// allowing the Orchestrator pipeline (validate → permission → hooks → execute)
// to be used transparently.
func NewToolRegistryAdapter(reg *tool.Registry, orchestrateFn func(string, map[string]any) (types.ToolResult, error)) *ToolRegistryAdapter {
	return &ToolRegistryAdapter{
		registry:    reg,
		orchestrate: orchestrateFn,
	}
}

// ToolDefinitions returns OpenAI tool definitions for the given names.
// If names is empty, returns all enabled tools.
func (a *ToolRegistryAdapter) ToolDefinitions(names []string) []openai.Tool {
	var tools []types.Tool
	wildcard := len(names) == 0
	excluded := make(map[string]bool)
	for _, name := range names {
		if name == "*" {
			wildcard = true
		} else if strings.HasPrefix(name, "!") {
			excluded[strings.TrimPrefix(name, "!")] = true
		}
	}
	if wildcard {
		tools = a.registry.Enabled()
	} else {
		tools = a.registry.FilterByNames(names)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name() < tools[j].Name() })

	result := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		if excluded[t.Name()] {
			continue
		}
		schema := t.InputSchema()

		// Preserve the complete schema, including $defs and combinators.
		params := schema
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}

		result = append(result, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  params,
			},
		})
	}
	return result
}

// Execute runs a tool by name with JSON arguments string.
// If an orchestrate function was provided, it routes through the full
// Orchestrator pipeline. Otherwise, it calls the tool directly.
func (a *ToolRegistryAdapter) Execute(name string, argsJSON string) (string, error) {
	result, err := a.ExecuteResult(name, argsJSON)
	return result.Content, err
}

// ExecuteResult preserves multimodal output while Execute remains compatible
// with older text-only skill registries.
func (a *ToolRegistryAdapter) ExecuteResult(name string, argsJSON string) (types.ToolResult, error) {
	return a.ExecuteResultContext(context.Background(), name, argsJSON)
}

// ExecuteResultContext runs a tool with the request context when available.
func (a *ToolRegistryAdapter) ExecuteResultContext(ctx context.Context, name string, argsJSON string) (types.ToolResult, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return types.ToolResult{}, fmt.Errorf("parsing tool args: %w", err)
	}
	var result types.ToolResult
	var err error
	if a.orchestrateContext != nil {
		result, err = a.orchestrateContext(ctx, name, args)
	} else if a.orchestrate != nil {
		result, err = a.orchestrate(name, args)
	} else {
		result, err = a.registry.Execute(name, args, types.ToolContext{Context: ctx, WorkingDir: "."})
	}
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("Erro: %v", err), IsError: true}, nil
	}
	result.Content = truncateToolResult(result.Content)
	result.Content = appendProjectInstructions(ctx, name, args, result.Content, nil)
	if result.IsError {
		result.Content = "Erro: " + result.Content
	}
	return result, nil
}

// truncateToolResult keeps a bounded preview while retaining large results
// for retrieval through the normal tool permission path.
func truncateToolResult(s string) string {
	return tooloutput.Preview(s)
}

// Ensure ToolRegistryAdapter implements SkillRegistry
var _ SkillRegistry = (*ToolRegistryAdapter)(nil)
