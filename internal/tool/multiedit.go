package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/types"
)

// EditOp represents a single edit operation in a multi_edit batch.
type EditOp struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// NewMultiEditTool creates a multi_edit tool definition.
//
// multi_edit applies a list of string-replacement edits to a single file
// atomically: all edits are applied in memory in order, and the file is only
// written if every edit succeeds. If any edit fails (old_string not found,
// or non-unique without replace_all), the file is left untouched.
func NewMultiEditTool(regs ...*Registry) types.ToolDef {
	var reg *Registry
	if len(regs) > 0 {
		reg = regs[0]
	}
	return types.ToolDef{
		NameFn: func() string { return "multi_edit" },
		DescriptionFn: func() string {
			return "Apply multiple edits to a file atomically. All edits must succeed or none are applied. " +
				"Useful for coordinated changes that must happen together."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file to edit",
					},
					"edits": map[string]any{
						"type":        "array",
						"description": "List of edit operations to apply in order",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"old_string": map[string]any{
									"type":        "string",
									"description": "Text to find",
								},
								"new_string": map[string]any{
									"type":        "string",
									"description": "Replacement text",
								},
								"replace_all": map[string]any{
									"type":        "boolean",
									"description": "Replace all occurrences (default: false, must be unique)",
								},
							},
							"required": []string{"old_string", "new_string"},
						},
					},
				},
				"required": []string{"path", "edits"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return false },
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			path, _ := args["path"].(string)
			if path == "" {
				return types.ValidationResult{Valid: false, Message: "path is required"}
			}
			editsRaw, ok := args["edits"].([]any)
			if !ok || len(editsRaw) == 0 {
				return types.ValidationResult{Valid: false, Message: "edits array is required and must be non-empty"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			path, _ := args["path"].(string)
			if path == "" {
				return types.ToolResult{IsError: true, Content: "path is required"}, nil
			}

			editsRaw, ok := args["edits"].([]any)
			if !ok || len(editsRaw) == 0 {
				return types.ToolResult{IsError: true, Content: "edits array is required and must be non-empty"}, nil
			}

			// Parse edits
			edits := make([]EditOp, 0, len(editsRaw))
			for i, e := range editsRaw {
				m, ok := e.(map[string]any)
				if !ok {
					return types.ToolResult{
						IsError: true,
						Content: fmt.Sprintf("edit %d: not an object", i),
					}, nil
				}
				op := EditOp{}
				if s, ok := m["old_string"].(string); ok {
					op.OldString = s
				}
				if s, ok := m["new_string"].(string); ok {
					op.NewString = s
				}
				if b, ok := m["replace_all"].(bool); ok {
					op.ReplaceAll = b
				}
				if op.OldString == "" {
					return types.ToolResult{
						IsError: true,
						Content: fmt.Sprintf("edit %d: old_string is required", i),
					}, nil
				}
				if op.OldString == op.NewString {
					return types.ToolResult{
						IsError: true,
						Content: fmt.Sprintf("edit %d: old_string and new_string are identical", i),
					}, nil
				}
				edits = append(edits, op)
			}

			// Resolve path against the working directory if relative.
			if !filepath.IsAbs(path) && ctx.WorkingDir != "" {
				path = filepath.Join(ctx.WorkingDir, path)
			}

			// Read the original file.
			original, err := os.ReadFile(path)
			if err != nil {
				return types.ToolResult{
					IsError: true,
					Content: fmt.Sprintf("cannot read file: %v", err),
				}, nil
			}

			// Apply edits in memory (all-or-nothing semantics).
			content := string(original)
			results := make([]string, 0, len(edits))

			for i, op := range edits {
				if !strings.Contains(content, op.OldString) {
					return types.ToolResult{
						IsError: true,
						Content: fmt.Sprintf("edit %d: old_string not found in content:\n%s", i, multiEditPreview(op.OldString)),
					}, nil
				}

				if !op.ReplaceAll {
					count := strings.Count(content, op.OldString)
					if count > 1 {
						return types.ToolResult{
							IsError: true,
							Content: fmt.Sprintf("edit %d: old_string has %d occurrences, must be unique (use replace_all=true)", i, count),
						}, nil
					}
					content = strings.Replace(content, op.OldString, op.NewString, 1)
					results = append(results, fmt.Sprintf("edit %d: replaced 1 occurrence", i))
				} else {
					count := strings.Count(content, op.OldString)
					content = strings.ReplaceAll(content, op.OldString, op.NewString)
					results = append(results, fmt.Sprintf("edit %d: replaced %d occurrences", i, count))
				}
			}

			// Preserve original file mode where possible.
			mode := os.FileMode(0644)
			if stat, statErr := os.Stat(path); statErr == nil {
				mode = stat.Mode()
			}
			if reg != nil {
				if err := reg.runBeforeFileWrite(ctx, path, original, true); err != nil {
					return beforeFileWriteError(err), nil
				}
			}
			if err := os.WriteFile(path, []byte(content), mode); err != nil {
				return types.ToolResult{
					IsError: true,
					Content: fmt.Sprintf("cannot write file: %v", err),
				}, nil
			}

			var summary strings.Builder
			fmt.Fprintf(&summary, "multi_edit applied %d edits to %s:\n", len(edits), path)
			for _, r := range results {
				summary.WriteString("  ")
				summary.WriteString(r)
				summary.WriteString("\n")
			}
			res := types.ToolResult{Content: summary.String()}
			if reg != nil {
				return reg.afterFileWrite(ctx, path, content, res), nil
			}
			return res, nil
		},
	}
}

// multiEditPreview returns a short, single-line preview of a string for use
// in error messages. Newlines are escaped and long strings are truncated.
func multiEditPreview(s string) string {
	const max = 80
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

// RegisterMultiEditTool registers the multi_edit tool with the given registry.
func RegisterMultiEditTool(reg *Registry) {
	reg.RegisterDef(NewMultiEditTool(reg))
}
