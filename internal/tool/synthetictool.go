package tool

import (
	"fmt"
	"time"

	"github.com/costa/polypod/internal/types"
)

// NewSyntheticOutputTool creates a tool that emits arbitrary synthetic output.
// Useful for testing UI rendering, progress indicators, and diff displays without
// real side effects.
func NewSyntheticOutputTool() types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "synthetic_output" },
		DescriptionFn: func() string {
			return "Emit synthetic tool output for testing UI rendering. Supports text, markdown, diff, json, table kinds."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"output": map[string]any{
						"type":        "string",
						"description": "The content to emit",
					},
					"kind": map[string]any{
						"type":        "string",
						"enum":        []string{"text", "markdown", "diff", "json", "table", "code"},
						"description": "Content kind for proper rendering (default: text)",
					},
					"error": map[string]any{
						"type":        "boolean",
						"description": "If true, emit as an error result",
					},
					"delay_ms": map[string]any{
						"type":        "integer",
						"description": "Simulate processing delay in milliseconds (max 10000)",
					},
					"tokens": map[string]any{
						"type":        "integer",
						"description": "Synthetic token count to report",
					},
				},
				"required": []string{"output"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsDestructiveFn: func(map[string]any) bool { return false },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			output, _ := args["output"].(string)
			if output == "" {
				return types.ToolResult{IsError: true, Content: "output is required"}, nil
			}

			// Delay simulation
			if delayRaw, ok := args["delay_ms"]; ok {
				var delay int
				switch v := delayRaw.(type) {
				case float64:
					delay = int(v)
				case int:
					delay = v
				}
				if delay > 10000 {
					delay = 10000
				}
				if delay > 0 {
					time.Sleep(time.Duration(delay) * time.Millisecond)
				}
			}

			// Error simulation
			if isErr, ok := args["error"].(bool); ok && isErr {
				return types.ToolResult{
					IsError: true,
					Content: "[synthetic error] " + output,
				}, nil
			}

			// Kind-specific formatting
			kind, _ := args["kind"].(string)
			if kind == "" {
				kind = "text"
			}

			var content string
			switch kind {
			case "markdown":
				content = output
			case "diff":
				content = "```diff\n" + output + "\n```"
			case "json":
				content = "```json\n" + output + "\n```"
			case "table":
				content = output
			case "code":
				content = "```\n" + output + "\n```"
			default:
				content = output
			}

			// Tokens hint (not enforced, just for display)
			if tokensRaw, ok := args["tokens"]; ok {
				var tokens int
				switch v := tokensRaw.(type) {
				case float64:
					tokens = int(v)
				case int:
					tokens = v
				}
				if tokens > 0 {
					content += fmt.Sprintf("\n[synthetic: %d tokens]", tokens)
				}
			}

			return types.ToolResult{Content: content}, nil
		},
	}
}

// RegisterSyntheticOutputTool registers the tool.
func RegisterSyntheticOutputTool(reg *Registry) {
	reg.RegisterDef(NewSyntheticOutputTool())
}
