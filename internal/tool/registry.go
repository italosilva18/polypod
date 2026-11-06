package tool

import (
	"context"
	"fmt"
	"sync"

	"github.com/costa/polypod/internal/types"
)

// Registry holds all registered tools and provides lookup/execution.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]types.Tool
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]types.Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t types.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// RegisterDef builds and registers a tool from a ToolDef.
func (r *Registry) RegisterDef(def types.ToolDef) types.Tool {
	t := types.BuildTool(def)
	r.Register(t)
	return t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (types.Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns all tool names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// Count returns the number of registered tools.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// Remove removes a tool by name.
func (r *Registry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

// Enabled returns only enabled tools.
func (r *Registry) Enabled() []types.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]types.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		if t.IsEnabled() {
			result = append(result, t)
		}
	}
	return result
}

// ReadOnly returns only read-only tools.
func (r *Registry) ReadOnly() []types.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []types.Tool
	for _, t := range r.tools {
		if t.IsReadOnly(nil) && t.IsEnabled() {
			result = append(result, t)
		}
	}
	return result
}

// ValidateInput validates input for a specific tool.
func (r *Registry) ValidateInput(name string, args map[string]any) types.ValidationResult {
	t, ok := r.Get(name)
	if !ok {
		return types.ValidationResult{Valid: false, Message: fmt.Sprintf("ferramenta desconhecida: %s", name)}
	}
	return t.ValidateInput(args)
}

// CheckPermissions checks if a tool call is permitted.
func (r *Registry) CheckPermissions(name string, args map[string]any, ctx types.ToolContext) types.PermissionResult {
	t, ok := r.Get(name)
	if !ok {
		return types.PermissionResult{Behavior: types.PermissionDeny, Message: fmt.Sprintf("ferramenta desconhecida: %s", name)}
	}
	return t.CheckPermissions(args, ctx)
}

// Execute runs a tool by name with full validation pipeline.
func (r *Registry) Execute(name string, args map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	t, ok := r.Get(name)
	if !ok {
		return types.ToolResult{}, fmt.Errorf("ferramenta desconhecida: %s", name)
	}

	// 1. Validate input
	vr := t.ValidateInput(args)
	if !vr.Valid {
		return types.ToolResult{
			Content: fmt.Sprintf("entrada invalida: %s", vr.Message),
			IsError: true,
		}, nil
	}

	// 2. Check permissions
	pr := t.CheckPermissions(args, ctx)
	if pr.Behavior == types.PermissionDeny {
		return types.ToolResult{
			Content: fmt.Sprintf("permissao negada: %s", pr.Message),
			IsError: true,
		}, nil
	}

	// 3. Execute
	return t.Execute(ctx, args)
}

// ExecuteRaw runs a tool without validation (for internal use).
func (r *Registry) ExecuteRaw(name string, args map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	t, ok := r.Get(name)
	if !ok {
		return types.ToolResult{}, fmt.Errorf("ferramenta desconhecida: %s", name)
	}
	return t.Execute(ctx, args)
}

// ToolSchemas returns JSON schemas for all tools (for API tool definitions).
func (r *Registry) ToolSchemas() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	schemas := make([]map[string]any, 0, len(r.tools))
	for _, t := range r.tools {
		if !t.IsEnabled() {
			continue
		}
		schemas = append(schemas, map[string]any{
			"name":        t.Name(),
			"description": t.Description(),
			"parameters":  t.InputSchema(),
		})
	}
	return schemas
}

// FilterByNames returns only tools whose names are in the given set.
func (r *Registry) FilterByNames(names []string) []types.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
		// Agent YAML uses skill names without the registry's collision-safe
		// prefix. Prefer a real builtin when one exists; otherwise expose the
		// bridged skill under its actual tool name.
		if _, direct := r.tools[n]; !direct {
			if _, bridged := r.tools["skill_"+n]; bridged {
				nameSet["skill_"+n] = true
			}
		}
	}
	var result []types.Tool
	for name, t := range r.tools {
		if nameSet[name] && t.IsEnabled() {
			result = append(result, t)
		}
	}
	return result
}

// FindByName returns a tool matching name or alias.
func (r *Registry) FindByName(name string) (types.Tool, bool) {
	// Direct lookup
	if t, ok := r.Get(name); ok {
		return t, true
	}
	// No alias support in interface, but the registry can be extended
	return nil, false
}

// ExecutionPipeline runs a full validate→permission→execute pipeline.
func (r *Registry) ExecutionPipeline(
	name string,
	args map[string]any,
	ctx types.ToolContext,
	hookCheck func(ctx context.Context, toolName string, args map[string]any) types.PermissionDecision,
) (types.ToolResult, error) {
	t, ok := r.Get(name)
	if !ok {
		return types.ToolResult{}, fmt.Errorf("ferramenta desconhecida: %s", name)
	}

	// 1. Validate
	vr := t.ValidateInput(args)
	if !vr.Valid {
		return types.ToolResult{
			Content: fmt.Sprintf("validacao falhou: %s", vr.Message),
			IsError: true,
		}, nil
	}

	// 2. Tool-level permissions
	pr := t.CheckPermissions(args, ctx)
	if pr.Behavior == types.PermissionDeny {
		return types.ToolResult{
			Content: fmt.Sprintf("permissao negada: %s", pr.Message),
			IsError: true,
		}, nil
	}

	// 3. Hook-based permission check
	if hookCheck != nil {
		decision := hookCheck(ctx.Context, name, args)
		if decision.Behavior == types.PermissionDeny {
			return types.ToolResult{
				Content: fmt.Sprintf("hook negou: %s", decision.Message),
				IsError: true,
			}, nil
		}
	}

	// 4. Execute with context
	result, err := t.Execute(ctx, args)
	if err != nil {
		return types.ToolResult{
			Content: fmt.Sprintf("erro na execucao: %v", err),
			IsError: true,
		}, nil
	}

	return result, nil
}

// ExecuteParallel runs multiple tool calls concurrently.
// Returns results in the same order as inputs.
func (r *Registry) ExecuteParallel(
	calls []ToolCall,
	ctx types.ToolContext,
) []ToolCallResult {
	results := make([]ToolCallResult, len(calls))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5) // max 5 concurrent

	for i, call := range calls {
		wg.Add(1)
		go func(idx int, c ToolCall) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result, err := r.Execute(c.Name, c.Args, ctx)
			results[idx] = ToolCallResult{
				Call:   c,
				Result: result,
				Error:  err,
			}
		}(i, call)
	}

	wg.Wait()
	return results
}

// ToolCall represents a single tool invocation.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// ToolCallResult is the result of a single tool call.
type ToolCallResult struct {
	Call   ToolCall
	Result types.ToolResult
	Error  error
}
