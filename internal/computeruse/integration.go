// Package computeruse integration provides registration of computer use tools
// (screenshot, click, type, scroll, etc.) with the tool.Registry system.
// Tools are only registered when a display is available.
package computeruse

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"

	"github.com/costa/polypod/internal/types"
)

// ToolRegistry is the interface expected for tool registration.
// This avoids a direct import cycle with the tool package.
type ToolRegistry interface {
	RegisterDef(def types.ToolDef) types.Tool
}

// IsDisplayAvailable returns true if a graphical display is available
// for computer use operations.
func IsDisplayAvailable() bool {
	switch runtime.GOOS {
	case "darwin":
		// macOS always has a display in GUI sessions
		return true
	case "windows":
		return true
	case "linux":
		// Check for X11 or Wayland
		if os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" {
			return true
		}
		if os.Getenv("XDG_SESSION_TYPE") == "x11" || os.Getenv("XDG_SESSION_TYPE") == "wayland" {
			return true
		}
		return false
	default:
		return false
	}
}

// RegisterComputerUseTools registers all computer use tools with the given registry.
// Returns the number of tools registered. If no display is available, returns 0
// and registers nothing.
func RegisterComputerUseTools(reg ToolRegistry) int {
	if !IsDisplayAvailable() {
		return 0
	}

	cuTool := NewComputerUseTool()

	reg.RegisterDef(newScreenshotTool(cuTool))
	reg.RegisterDef(newClickTool(cuTool))
	reg.RegisterDef(newTypeTool(cuTool))
	reg.RegisterDef(newScrollTool(cuTool))
	reg.RegisterDef(newKeyPressTool(cuTool))
	reg.RegisterDef(newMoveTool(cuTool))
	reg.RegisterDef(newDragTool(cuTool))
	reg.RegisterDef(newDoubleClickTool(cuTool))
	reg.RegisterDef(newRightClickTool(cuTool))
	reg.RegisterDef(newDisplayInfoTool(cuTool))

	return 10
}

// ─── Tool definitions ──────────────────────────────────────────────────

func newScreenshotTool(cu *ComputerUseTool) types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "computer_screenshot" },
		DescriptionFn: func() string {
			return "Capture the current screen and return an OCR visual map with text bounding boxes and click-center coordinates"
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			action := ToolAction{Action: "screenshot"}
			result, err := cu.ExecuteAction(ctx.Context, action)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			return toolResultFromCU(result), nil
		},
	}
}

func newClickTool(cu *ComputerUseTool) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "computer_click" },
		DescriptionFn: func() string { return "Click the mouse at screen coordinates (x, y)" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x":      map[string]any{"type": "integer", "description": "X coordinate"},
					"y":      map[string]any{"type": "integer", "description": "Y coordinate"},
					"button": map[string]any{"type": "string", "description": "Mouse button: left, right, middle (default: left)"},
				},
				"required": []string{"x", "y"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			action := ToolAction{
				Action: "click",
				X:      intArg(args, "x"),
				Y:      intArg(args, "y"),
				Button: stringArg(args, "button", "left"),
			}
			result, err := cu.ExecuteAction(ctx.Context, action)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			return toolResultFromCU(result), nil
		},
	}
}

func newDoubleClickTool(cu *ComputerUseTool) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "computer_double_click" },
		DescriptionFn: func() string { return "Double-click the mouse at screen coordinates (x, y)" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x": map[string]any{"type": "integer", "description": "X coordinate"},
					"y": map[string]any{"type": "integer", "description": "Y coordinate"},
				},
				"required": []string{"x", "y"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			action := ToolAction{
				Action: "double_click",
				X:      intArg(args, "x"),
				Y:      intArg(args, "y"),
			}
			result, err := cu.ExecuteAction(ctx.Context, action)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			return toolResultFromCU(result), nil
		},
	}
}

func newRightClickTool(cu *ComputerUseTool) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "computer_right_click" },
		DescriptionFn: func() string { return "Right-click the mouse at screen coordinates (x, y)" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x": map[string]any{"type": "integer", "description": "X coordinate"},
					"y": map[string]any{"type": "integer", "description": "Y coordinate"},
				},
				"required": []string{"x", "y"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			action := ToolAction{
				Action: "right_click",
				X:      intArg(args, "x"),
				Y:      intArg(args, "y"),
			}
			result, err := cu.ExecuteAction(ctx.Context, action)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			return toolResultFromCU(result), nil
		},
	}
}

func newTypeTool(cu *ComputerUseTool) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "computer_type" },
		DescriptionFn: func() string { return "Type text using the keyboard" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{"type": "string", "description": "Text to type"},
				},
				"required": []string{"text"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			text, _ := args["text"].(string)
			if text == "" {
				return types.ValidationResult{Valid: false, Message: "text is required"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			action := ToolAction{
				Action: "type",
				Text:   stringArg(args, "text", ""),
			}
			result, err := cu.ExecuteAction(ctx.Context, action)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			return toolResultFromCU(result), nil
		},
	}
}

func newScrollTool(cu *ComputerUseTool) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "computer_scroll" },
		DescriptionFn: func() string { return "Scroll the mouse wheel at screen coordinates" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x":         map[string]any{"type": "integer", "description": "X coordinate"},
					"y":         map[string]any{"type": "integer", "description": "Y coordinate"},
					"direction": map[string]any{"type": "string", "description": "Scroll direction: up, down, left, right (default: down)"},
					"amount":    map[string]any{"type": "integer", "description": "Number of scroll clicks (default: 3)"},
				},
				"required": []string{"x", "y"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			action := ToolAction{
				Action:    "scroll",
				X:         intArg(args, "x"),
				Y:         intArg(args, "y"),
				Direction: stringArg(args, "direction", "down"),
				Amount:    intArgDefault(args, "amount", 3),
			}
			result, err := cu.ExecuteAction(ctx.Context, action)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			return toolResultFromCU(result), nil
		},
	}
}

func newKeyPressTool(cu *ComputerUseTool) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "computer_key_press" },
		DescriptionFn: func() string { return "Press a key or key combination (e.g., 'enter', 'ctrl+c', 'alt+tab')" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key": map[string]any{"type": "string", "description": "Key or key combination to press"},
				},
				"required": []string{"key"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			key, _ := args["key"].(string)
			if key == "" {
				return types.ValidationResult{Valid: false, Message: "key is required"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			action := ToolAction{
				Action: "key_press",
				Key:    stringArg(args, "key", ""),
			}
			result, err := cu.ExecuteAction(ctx.Context, action)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			return toolResultFromCU(result), nil
		},
	}
}

func newMoveTool(cu *ComputerUseTool) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "computer_move_mouse" },
		DescriptionFn: func() string { return "Move the mouse cursor to screen coordinates (x, y)" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x": map[string]any{"type": "integer", "description": "X coordinate"},
					"y": map[string]any{"type": "integer", "description": "Y coordinate"},
				},
				"required": []string{"x", "y"},
			}
		},
		IsReadOnlyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			action := ToolAction{
				Action: "move_mouse",
				X:      intArg(args, "x"),
				Y:      intArg(args, "y"),
			}
			result, err := cu.ExecuteAction(ctx.Context, action)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			return toolResultFromCU(result), nil
		},
	}
}

func newDragTool(cu *ComputerUseTool) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "computer_drag" },
		DescriptionFn: func() string { return "Drag from one screen position to another" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from_x": map[string]any{"type": "integer", "description": "Start X coordinate"},
					"from_y": map[string]any{"type": "integer", "description": "Start Y coordinate"},
					"to_x":   map[string]any{"type": "integer", "description": "End X coordinate"},
					"to_y":   map[string]any{"type": "integer", "description": "End Y coordinate"},
				},
				"required": []string{"from_x", "from_y", "to_x", "to_y"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			action := ToolAction{
				Action: "drag",
				X:      intArg(args, "from_x"),
				Y:      intArg(args, "from_y"),
				ToX:    intArg(args, "to_x"),
				ToY:    intArg(args, "to_y"),
			}
			result, err := cu.ExecuteAction(ctx.Context, action)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			return toolResultFromCU(result), nil
		},
	}
}

func newDisplayInfoTool(cu *ComputerUseTool) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "computer_display_info" },
		DescriptionFn: func() string { return "Get information about the current display (resolution, platform)" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			display, err := cu.service.GetDisplay()
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Could not get display info: %v", err),
					IsError: true,
				}, nil
			}
			info := map[string]any{
				"width":        display.Width,
				"height":       display.Height,
				"scale_factor": display.ScaleFactor,
				"platform":     string(cu.service.Platform()),
			}
			data, _ := json.MarshalIndent(info, "", "  ")
			return types.ToolResult{Content: string(data)}, nil
		},
	}
}

// ─── Arg helpers ───────────────────────────────────────────────────────

func intArg(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, _ := strconv.Atoi(v)
		return n
	default:
		return 0
	}
}

func intArgDefault(args map[string]any, key string, def int) int {
	if _, ok := args[key]; !ok {
		return def
	}
	v := intArg(args, key)
	if v == 0 {
		return def
	}
	return v
}

func stringArg(args map[string]any, key, def string) string {
	if s, ok := args[key].(string); ok && s != "" {
		return s
	}
	return def
}

// toolResultFromCU converts a ComputerUseTool result to a types.ToolResult.
func toolResultFromCU(result *ToolResult) types.ToolResult {
	if !result.Success {
		msg := result.Error
		if msg == "" {
			msg = "computer use action failed"
		}
		return types.ToolResult{Content: msg, IsError: true}
	}

	content := result.Message
	if result.Screenshot != "" {
		// Include screenshot as base64 in the content.
		// The model can interpret this as an image.
		if content != "" {
			content += "\n"
		}
		content += fmt.Sprintf("[screenshot: %d bytes base64]", len(result.Screenshot))
	}
	if content == "" {
		content = "OK"
	}

	return types.ToolResult{Content: content}
}
