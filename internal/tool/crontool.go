package tool

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

type CronJob struct {
	ID       string    `json:"id"`
	Schedule string    `json:"schedule"`
	Prompt   string    `json:"prompt"`
	Enabled  bool      `json:"enabled"`
	Created  time.Time `json:"created"`
	NextRun  time.Time `json:"next_run,omitempty"`
	LastRun  time.Time `json:"last_run,omitempty"`
}

// CronStore holds scheduled jobs.
type CronStore struct {
	mu   sync.Mutex
	jobs map[string]*CronJob
}

var globalCronStore = &CronStore{jobs: make(map[string]*CronJob)}

func GetCronStore() *CronStore { return globalCronStore }

func (s *CronStore) Create(schedule, prompt string, enabled bool) (*CronJob, error) {
	if err := validateCronExpression(schedule); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := fmt.Sprintf("cron-%d", time.Now().UnixNano())
	job := &CronJob{ID: id, Schedule: schedule, Prompt: prompt, Enabled: enabled, Created: time.Now()}
	s.jobs[id] = job
	return job, nil
}

func (s *CronStore) List() []*CronJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*CronJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		copy := *j
		out = append(out, &copy)
	}
	return out
}

func (s *CronStore) Get(id string) (*CronJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	copy := *job
	return &copy, true
}

func (s *CronStore) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return false
	}
	delete(s.jobs, id)
	return true
}

func (s *CronStore) SetEnabled(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}
	job.Enabled = enabled
	return nil
}

// validateCronExpression does basic validation of a 5-field cron expression.
// Format: minute hour day month weekday
func validateCronExpression(expr string) error {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return fmt.Errorf("cron expression must have 5 fields (minute hour day month weekday), got %d", len(fields))
	}

	ranges := []struct {
		name string
		min  int
		max  int
	}{
		{"minute", 0, 59},
		{"hour", 0, 23},
		{"day", 1, 31},
		{"month", 1, 12},
		{"weekday", 0, 6},
	}

	for i, field := range fields {
		if err := validateCronField(field, ranges[i].min, ranges[i].max); err != nil {
			return fmt.Errorf("%s field: %w", ranges[i].name, err)
		}
	}
	return nil
}

var cronFieldRe = regexp.MustCompile(`^(\*|\*/\d+|\d+|\d+-\d+|\d+(,\d+)*)(/\d+)?$`)

func validateCronField(field string, min, max int) error {
	if field == "" {
		return fmt.Errorf("empty field")
	}
	if field == "*" {
		return nil
	}
	if !cronFieldRe.MatchString(field) {
		return fmt.Errorf("invalid field format: %s", field)
	}
	return nil
}

// NewScheduleCronTool creates the schedule_cron tool.
func NewScheduleCronTool() types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "schedule_cron" },
		DescriptionFn: func() string {
			return "Create, list, or delete scheduled cron jobs. Actions: create, list, get, delete, enable, disable."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"create", "list", "get", "delete", "enable", "disable"},
						"description": "The action to perform",
					},
					"id": map[string]any{
						"type":        "string",
						"description": "Job ID (required for get/delete/enable/disable)",
					},
					"schedule": map[string]any{
						"type":        "string",
						"description": "Cron expression (e.g. '0 9 * * 1' = every Monday at 9am). Required for create.",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "Prompt to execute when the job fires. Required for create.",
					},
					"enabled": map[string]any{
						"type":        "boolean",
						"description": "Whether the job is enabled (default: true)",
					},
				},
				"required": []string{"action"},
			}
		},
		IsReadOnlyFn: func(args map[string]any) bool {
			action, _ := args["action"].(string)
			return action == "list" || action == "get"
		},
		IsDestructiveFn: func(args map[string]any) bool {
			action, _ := args["action"].(string)
			return action == "delete"
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			action, _ := args["action"].(string)
			if action == "" {
				return types.ToolResult{IsError: true, Content: "action is required"}, nil
			}

			store := GetCronStore()

			switch action {
			case "create":
				schedule, _ := args["schedule"].(string)
				prompt, _ := args["prompt"].(string)
				if schedule == "" || prompt == "" {
					return types.ToolResult{IsError: true, Content: "schedule and prompt are required for create"}, nil
				}
				enabled := true
				if v, ok := args["enabled"].(bool); ok {
					enabled = v
				}
				job, err := store.Create(schedule, prompt, enabled)
				if err != nil {
					return types.ToolResult{IsError: true, Content: err.Error()}, nil
				}
				data, _ := json.Marshal(job)
				return types.ToolResult{Content: "Job created:\n" + string(data)}, nil

			case "list":
				jobs := store.List()
				data, _ := json.Marshal(jobs)
				return types.ToolResult{Content: fmt.Sprintf("%d jobs:\n%s", len(jobs), string(data))}, nil

			case "get":
				id, _ := args["id"].(string)
				if id == "" {
					return types.ToolResult{IsError: true, Content: "id is required for get"}, nil
				}
				job, ok := store.Get(id)
				if !ok {
					return types.ToolResult{IsError: true, Content: "job not found: " + id}, nil
				}
				data, _ := json.Marshal(job)
				return types.ToolResult{Content: string(data)}, nil

			case "delete":
				id, _ := args["id"].(string)
				if id == "" {
					return types.ToolResult{IsError: true, Content: "id is required for delete"}, nil
				}
				if !store.Delete(id) {
					return types.ToolResult{IsError: true, Content: "job not found: " + id}, nil
				}
				return types.ToolResult{Content: "Job deleted: " + id}, nil

			case "enable", "disable":
				id, _ := args["id"].(string)
				if id == "" {
					return types.ToolResult{IsError: true, Content: "id is required"}, nil
				}
				if err := store.SetEnabled(id, action == "enable"); err != nil {
					return types.ToolResult{IsError: true, Content: err.Error()}, nil
				}
				return types.ToolResult{Content: fmt.Sprintf("Job %s: %sd", id, action)}, nil

			default:
				return types.ToolResult{IsError: true, Content: "unknown action: " + action}, nil
			}
		},
	}
}

// RegisterScheduleCronTool registers the tool.
func RegisterScheduleCronTool(reg *Registry) {
	reg.RegisterDef(NewScheduleCronTool())
}
