package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/costa/polypod/internal/types"
)

// WorkflowStep describes one step in a workflow: which tool to invoke,
// with which arguments, and whether a failure should halt the sequence.
type WorkflowStep struct {
	ToolName    string         `json:"tool_name"`
	Args        map[string]any `json:"args"`
	StopOnError bool           `json:"stop_on_error"`
}

// Workflow is a named sequence of tool invocations.
type Workflow struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Steps       []WorkflowStep `json:"steps"`
}

// WorkflowStore holds registered workflows, keyed by name. Safe for
// concurrent use.
type WorkflowStore struct {
	mu        sync.RWMutex
	workflows map[string]Workflow
}

// NewWorkflowStore creates an empty workflow store.
func NewWorkflowStore() *WorkflowStore {
	return &WorkflowStore{
		workflows: make(map[string]Workflow),
	}
}

// Define registers (or replaces) a workflow under its name.
func (s *WorkflowStore) Define(wf Workflow) error {
	if wf.Name == "" {
		return fmt.Errorf("workflow name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workflows[wf.Name] = wf
	return nil
}

// Get returns a copy of the workflow with the given name and whether it
// exists.
func (s *WorkflowStore) Get(name string) (Workflow, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wf, ok := s.workflows[name]
	return wf, ok
}

// List returns the sorted names of all registered workflows.
func (s *WorkflowStore) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.workflows))
	for name := range s.workflows {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ToolInvoker is the minimal interface WorkflowTool needs to dispatch
// step executions. It is injected, breaking any import cycle between the
// workflow tool and the full tool registry.
type ToolInvoker interface {
	Invoke(ctx context.Context, toolName string, args map[string]any) (result any, err error)
}

// StepResult captures the outcome of one executed workflow step.
type StepResult struct {
	ToolName string `json:"tool_name"`
	Result   any    `json:"result,omitempty"`
	Error    string `json:"error,omitempty"`
	Stopped  bool   `json:"stopped,omitempty"`
}

// NewWorkflowTool returns a meta-tool that can define, run, and list
// workflows. Steps are executed sequentially via the provided invoker.
func NewWorkflowTool(store *WorkflowStore, invoker ToolInvoker) types.Tool {
	if store == nil {
		store = NewWorkflowStore()
	}

	return types.BuildTool(types.ToolDef{
		NameFn:        func() string { return "workflow" },
		DescriptionFn: func() string { return "Define, list, and run named workflows composed of other tool calls" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"define", "run", "list"},
						"description": "What to do: define a workflow, run an existing workflow, or list all workflows",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Workflow name (required for define/run)",
					},
					"workflow": map[string]any{
						"type":        "object",
						"description": "Workflow definition (required for define). Shape: {name, description, steps:[{tool_name, args, stop_on_error}]}",
					},
				},
				"required": []string{"action"},
			}
		},

		IsReadOnlyFn: func(args map[string]any) bool {
			action, _ := args["action"].(string)
			return action == "list"
		},

		ValidateFn: func(args map[string]any) types.ValidationResult {
			action, _ := args["action"].(string)
			switch action {
			case "define":
				if _, ok := args["workflow"]; !ok {
					return types.ValidationResult{Valid: false, Message: "workflow object is required for define"}
				}
			case "run":
				if name, _ := args["name"].(string); name == "" {
					return types.ValidationResult{Valid: false, Message: "name is required for run"}
				}
			case "list":
				// no required fields
			default:
				return types.ValidationResult{Valid: false, Message: "action must be one of: define, run, list"}
			}
			return types.ValidationResult{Valid: true}
		},

		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			action, _ := args["action"].(string)
			switch action {
			case "define":
				return executeWorkflowDefine(store, args)
			case "run":
				return executeWorkflowRun(ctx, store, invoker, args)
			case "list":
				return executeWorkflowList(store)
			default:
				return types.ToolResult{
					Content: fmt.Sprintf("unknown action %q", action),
					IsError: true,
				}, nil
			}
		},
	})
}

func executeWorkflowDefine(store *WorkflowStore, args map[string]any) (types.ToolResult, error) {
	raw, ok := args["workflow"]
	if !ok {
		return types.ToolResult{Content: "workflow object is required", IsError: true}, nil
	}

	wf, err := decodeWorkflow(raw)
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("invalid workflow: %v", err), IsError: true}, nil
	}

	// Allow the top-level "name" arg to override the embedded one.
	if nameOverride, _ := args["name"].(string); nameOverride != "" {
		wf.Name = nameOverride
	}

	if err := store.Define(wf); err != nil {
		return types.ToolResult{Content: err.Error(), IsError: true}, nil
	}

	return types.ToolResult{
		Content: fmt.Sprintf("defined workflow %q with %d step(s)", wf.Name, len(wf.Steps)),
	}, nil
}

func executeWorkflowRun(
	ctx types.ToolContext,
	store *WorkflowStore,
	invoker ToolInvoker,
	args map[string]any,
) (types.ToolResult, error) {
	name, _ := args["name"].(string)
	wf, ok := store.Get(name)
	if !ok {
		return types.ToolResult{
			Content: fmt.Sprintf("workflow %q not found", name),
			IsError: true,
		}, nil
	}

	if invoker == nil {
		return types.ToolResult{
			Content: "no tool invoker configured; cannot run workflow",
			IsError: true,
		}, nil
	}

	runCtx := ctx.Context
	if runCtx == nil {
		runCtx = context.Background()
	}

	results := make([]StepResult, 0, len(wf.Steps))
	var halted bool
	var haltErr error

	for _, step := range wf.Steps {
		res, err := invoker.Invoke(runCtx, step.ToolName, step.Args)
		sr := StepResult{ToolName: step.ToolName, Result: res}
		if err != nil {
			sr.Error = err.Error()
			if step.StopOnError {
				sr.Stopped = true
				results = append(results, sr)
				halted = true
				haltErr = err
				break
			}
		}
		results = append(results, sr)
	}

	payload := map[string]any{
		"workflow":     wf.Name,
		"steps_total":  len(wf.Steps),
		"steps_ran":    len(results),
		"halted":       halted,
		"step_results": results,
	}
	if halted && haltErr != nil {
		payload["error"] = haltErr.Error()
	}

	content, mErr := json.MarshalIndent(payload, "", "  ")
	if mErr != nil {
		return types.ToolResult{Content: fmt.Sprintf("failed to marshal results: %v", mErr), IsError: true}, nil
	}

	return types.ToolResult{
		Content: string(content),
		IsError: halted,
	}, nil
}

func executeWorkflowList(store *WorkflowStore) (types.ToolResult, error) {
	names := store.List()
	payload := map[string]any{
		"count":     len(names),
		"workflows": names,
	}
	content, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("failed to marshal list: %v", err), IsError: true}, nil
	}
	return types.ToolResult{Content: string(content)}, nil
}

// decodeWorkflow turns an arbitrary map[string]any (as it would arrive
// from a tool-call JSON payload) into a Workflow value. We round-trip via
// JSON so that field name mapping matches the struct tags.
func decodeWorkflow(raw any) (Workflow, error) {
	var wf Workflow

	// Fast path: already the right type (unit tests).
	if typed, ok := raw.(Workflow); ok {
		return typed, nil
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return wf, fmt.Errorf("marshal: %w", err)
	}
	if err := json.Unmarshal(data, &wf); err != nil {
		return wf, fmt.Errorf("unmarshal: %w", err)
	}
	if wf.Name == "" {
		return wf, fmt.Errorf("workflow.name is required")
	}
	return wf, nil
}
