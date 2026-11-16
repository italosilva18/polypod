// Package function — Function Calling extracted from AICHAT
// Sources: aichat/src/function.rs

package function

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FunctionDefinition defines a callable function
type FunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
	Handler     FunctionHandler        `json:"-"`
}

// FunctionHandler is the function handler type
type FunctionHandler func(args map[string]interface{}) (interface{}, error)

// FunctionCall represents a function call
type FunctionCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// FunctionResult holds function execution result
type FunctionResult struct {
	Name    string      `json:"name"`
	Result  interface{} `json:"result"`
	Error   error       `json:"error,omitempty"`
}

// FunctionRegistry manages function definitions
type FunctionRegistry struct {
	functions map[string]*FunctionDefinition
}

// NewFunctionRegistry creates new registry
func NewFunctionRegistry() *FunctionRegistry {
	return &FunctionRegistry{
		functions: make(map[string]*FunctionDefinition),
	}
}

// Register registers a function
func (r *FunctionRegistry) Register(def *FunctionDefinition) error {
	if def.Name == "" {
		return fmt.Errorf("function name required")
	}
	r.functions[def.Name] = def
	return nil
}

// Call calls a function
func (r *FunctionRegistry) Call(name string, args map[string]interface{}) (*FunctionResult, error) {
	fn, exists := r.functions[name]
	if !exists {
		return nil, fmt.Errorf("function not found: %s", name)
	}
	
	result, err := fn.Handler(args)
	return &FunctionResult{
		Name:   name,
		Result: result,
		Error:  err,
	}, nil
}

// ListFunctions lists all registered functions
func (r *FunctionRegistry) ListFunctions() []*FunctionDefinition {
	functions := make([]*FunctionDefinition, 0, len(r.functions))
	for _, fn := range r.functions {
		functions = append(functions, fn)
	}
	return functions
}

// Built-in functions
func RegisterBuiltins(reg *FunctionRegistry) {
	// Search function
	reg.Register(&FunctionDefinition{
		Name:        "search",
		Description: "Search the web for information",
		Parameters: map[string]interface{}{
			"query": map[string]string{
				"type":        "string",
				"description": "Search query",
			},
		},
		Handler: func(args map[string]interface{}) (interface{}, error) {
			query, _ := args["query"].(string)
			return fmt.Sprintf("Search results for: %s", query), nil
		},
	})
	
	// Calculator function
	reg.Register(&FunctionDefinition{
		Name:        "calculate",
		Description: "Perform mathematical calculation",
		Parameters: map[string]interface{}{
			"expression": map[string]string{
				"type":        "string",
				"description": "Mathematical expression",
			},
		},
		Handler: func(args map[string]interface{}) (interface{}, error) {
			expr, _ := args["expression"].(string)
			return fmt.Sprintf("Result of: %s", expr), nil
		},
	})
	
	// File read function
	reg.Register(&FunctionDefinition{
		Name:        "read_file",
		Description: "Read file contents",
		Parameters: map[string]interface{}{
			"path": map[string]string{
				"type":        "string",
				"description": "File path",
			},
		},
		Handler: func(args map[string]interface{}) (interface{}, error) {
			path, _ := args["path"].(string)
			return fmt.Sprintf("Contents of: %s", path), nil
		},
	})
}

// FormatFunctionList formats function list
func FormatFunctionList(functions []*FunctionDefinition) string {
	var sb strings.Builder
	
	sb.WriteString("🔧 **Available Functions**:\n\n")
	
	for i, fn := range functions {
		sb.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, fn.Name))
		sb.WriteString(fmt.Sprintf("   %s\n", fn.Description))
		
		if len(fn.Parameters) > 0 {
			sb.WriteString("   Parameters:\n")
			for param, details := range fn.Parameters {
				if paramMap, ok := details.(map[string]string); ok {
					sb.WriteString(fmt.Sprintf("     - %s (%s): %s\n", 
						param, paramMap["type"], paramMap["description"]))
				}
			}
		}
	}
	
	return sb.String()
}

// ParseFunctionCall parses a function call from JSON
func ParseFunctionCall(jsonStr string) (*FunctionCall, error) {
	var call FunctionCall
	err := json.Unmarshal([]byte(jsonStr), &call)
	if err != nil {
		return nil, err
	}
	return &call, nil
}

// FormatFunctionResult formats function result
func FormatFunctionResult(result *FunctionResult) string {
	if result.Error != nil {
		return fmt.Sprintf("❌ **%s** failed: %v", result.Name, result.Error)
	}
	
	resultJSON, _ := json.MarshalIndent(result.Result, "", "  ")
	return fmt.Sprintf("✅ **%s**:\n```json\n%s\n```", result.Name, string(resultJSON))
}
