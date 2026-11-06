package tool

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// PlanModeRuntime tracks an in-process plan mode session for the alternate
// planmode_enter / planmode_exit tools defined in this file.
//
// Note: this is a separate, lighter-weight runtime than the PlanModeState
// struct in plantools.go which integrates with modes.State and the
// PlanSimulator. The two coexist intentionally so that callers can choose
// either the mode-integrated tools (enter_plan_mode/exit_plan_mode) or these
// standalone, dependency-free tools (planmode_enter/planmode_exit).
type PlanModeRuntime struct {
	mu       sync.Mutex
	Active   bool
	Title    string
	StartCWD string
	StartAt  time.Time
	Steps    []PlanRuntimeStep
}

// PlanRuntimeStep records a single simulated tool call during plan mode.
type PlanRuntimeStep struct {
	Timestamp time.Time      `json:"timestamp"`
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args"`
	Result    string         `json:"result,omitempty"`
}

var globalPlanRuntime = &PlanModeRuntime{}

// GetPlanModeRuntime returns the singleton runtime used by the planmode_*
// tools registered via RegisterPlanModeAltTools.
func GetPlanModeRuntime() *PlanModeRuntime {
	return globalPlanRuntime
}

// IsActive reports whether plan mode is currently on.
func (s *PlanModeRuntime) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Active
}

// Enter activates plan mode with the given title and working directory.
// Returns an error if plan mode is already active.
func (s *PlanModeRuntime) Enter(title, cwd string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Active {
		return fmt.Errorf("plan mode already active (started at %s)", s.StartAt.Format(time.RFC3339))
	}
	s.Active = true
	s.Title = title
	s.StartCWD = cwd
	s.StartAt = time.Now()
	s.Steps = nil
	return nil
}

// Exit deactivates plan mode and returns the number of recorded steps and
// the elapsed duration. Returns an error if plan mode is not active.
func (s *PlanModeRuntime) Exit() (stepCount int, duration time.Duration, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.Active {
		return 0, 0, fmt.Errorf("plan mode not active")
	}
	count := len(s.Steps)
	dur := time.Since(s.StartAt)
	s.Active = false
	s.Title = ""
	s.StartCWD = ""
	s.Steps = nil
	return count, dur, nil
}

// AddStep records a simulated tool call. No-op when plan mode is not active.
func (s *PlanModeRuntime) AddStep(step PlanRuntimeStep) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Active {
		s.Steps = append(s.Steps, step)
	}
}

// Summary returns a human-readable summary of the current plan mode session.
func (s *PlanModeRuntime) Summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.Active {
		return "Plan mode not active"
	}
	out := fmt.Sprintf("Plan: %s\nStarted: %s\nSteps: %d\n",
		s.Title, s.StartAt.Format(time.RFC3339), len(s.Steps))
	for i, step := range s.Steps {
		out += fmt.Sprintf("  %d. [%s] %s\n",
			i+1, step.Timestamp.Format("15:04:05"), step.Tool)
	}
	return out
}

// NewPlanModeEnterTool builds the planmode_enter tool definition.
func NewPlanModeEnterTool() types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "planmode_enter" },
		DescriptionFn: func() string {
			return "Enter plan mode: subsequent tool calls will be simulated, not executed. Use for safety when exploring solutions."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{
						"type":        "string",
						"description": "Optional title describing the plan being created",
					},
				},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsDestructiveFn: func(map[string]any) bool { return false },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			title, _ := args["title"].(string)
			if title == "" {
				title = "Unnamed plan"
			}
			cwd := ctx.WorkingDir
			if cwd == "" {
				cwd = "."
			}
			if err := globalPlanRuntime.Enter(title, cwd); err != nil {
				return types.ToolResult{IsError: true, Content: err.Error()}, nil
			}
			return types.ToolResult{
				Content: fmt.Sprintf(
					"Plan mode ACTIVATED: %q\n\nSubsequent tool calls will be SIMULATED only.\nUse planmode_exit when done.",
					title),
			}, nil
		},
	}
}

// NewPlanModeExitTool builds the planmode_exit tool definition.
func NewPlanModeExitTool() types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "planmode_exit" },
		DescriptionFn: func() string {
			return "Exit plan mode and return to normal execution. Optionally commit or abort the plan."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"approved": map[string]any{
						"type":        "boolean",
						"description": "If true, commit the plan; if false, abort it",
					},
					"plan": map[string]any{
						"type":        "string",
						"description": "Optional plan summary to record",
					},
				},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsDestructiveFn: func(map[string]any) bool { return false },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			approved, _ := args["approved"].(bool)
			plan, _ := args["plan"].(string)

			count, dur, err := globalPlanRuntime.Exit()
			if err != nil {
				return types.ToolResult{IsError: true, Content: err.Error()}, nil
			}

			status := "APPROVED"
			if !approved {
				status = "ABORTED"
			}

			msg := fmt.Sprintf(
				"Plan mode EXITED: %s\nDuration: %s\nSteps simulated: %d\n",
				status, dur.Round(time.Second), count,
			)
			if plan != "" {
				msg += "\nPlan summary:\n" + plan
			}
			return types.ToolResult{Content: msg}, nil
		},
	}
}

// RegisterPlanModeAltTools registers the alternate planmode_enter and
// planmode_exit tools on the given registry. Named "Alt" to avoid colliding
// with RegisterPlanModeTools defined in plantools.go.
func RegisterPlanModeAltTools(reg *Registry) {
	reg.Register(types.BuildTool(NewPlanModeEnterTool()))
	reg.Register(types.BuildTool(NewPlanModeExitTool()))
}

// MarshalPlanRuntimeJSON returns the current plan steps as JSON.
func MarshalPlanRuntimeJSON() ([]byte, error) {
	globalPlanRuntime.mu.Lock()
	defer globalPlanRuntime.mu.Unlock()
	return json.Marshal(globalPlanRuntime.Steps)
}
