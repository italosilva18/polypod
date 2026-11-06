package ai

import (
	"encoding/json"
	"fmt"

	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
	openai "github.com/sashabaranov/go-openai"
)

// ToolRegistryAdapter bridges tool.Registry (types.Tool interface) to the
// SkillRegistry interface expected by ai.Client. This allows the rich tool
// system (with permissions, validation, concurrency safety) to be used by
// the AI client's tool-calling loop.
type ToolRegistryAdapter struct {
	registry    *tool.Registry
	orchestrate func(name string, args map[string]any) (types.ToolResult, error)
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
	if len(names) == 0 {
		tools = a.registry.Enabled()
	} else {
		tools = a.registry.FilterByNames(names)
	}

	result := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		schema := t.InputSchema()

		// Build properties for jsonschema
		properties := make(map[string]any)
		required := make([]string, 0)

		if props, ok := schema["properties"]; ok {
			if pm, ok := props.(map[string]any); ok {
				properties = pm
			}
		}
		if req, ok := schema["required"]; ok {
			switch r := req.(type) {
			case []string:
				required = r
			case []any:
				for _, v := range r {
					if s, ok := v.(string); ok {
						required = append(required, s)
					}
				}
			}
		}

		// Build function parameters as raw JSON for go-openai
		params := map[string]any{
			"type":       "object",
			"properties": properties,
		}
		if len(required) > 0 {
			params["required"] = required
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
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		// Try as map[string]string fallback
		var strArgs map[string]string
		if err2 := json.Unmarshal([]byte(argsJSON), &strArgs); err2 != nil {
			return "", fmt.Errorf("parsing tool args: %w", err)
		}
		args = make(map[string]any, len(strArgs))
		for k, v := range strArgs {
			args[k] = v
		}
	}

	// Route through orchestrator if available
	if a.orchestrate != nil {
		result, err := a.orchestrate(name, args)
		if err != nil {
			return fmt.Sprintf("Erro: %v", err), nil
		}
		return truncateToolResult(result.Content), nil
	}

	// Direct execution via tool.Registry
	t, ok := a.registry.Get(name)
	if !ok {
		return fmt.Sprintf("ferramenta desconhecida: %s", name), nil
	}

	ctx := types.ToolContext{
		WorkingDir: ".",
	}
	result, err := t.Execute(ctx, args)
	if err != nil {
		return fmt.Sprintf("Erro: %v", err), nil
	}
	if result.IsError {
		return fmt.Sprintf("Erro: %s", result.Content), nil
	}
	return truncateToolResult(result.Content), nil
}

// truncateToolResult truncates long tool results to 50KB.
func truncateToolResult(s string) string {
	const maxLen = 50 * 1024
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... [saida truncada em 50KB]"
}

// Ensure ToolRegistryAdapter implements SkillRegistry
var _ SkillRegistry = (*ToolRegistryAdapter)(nil)
