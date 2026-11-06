package tool

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// TaskUpdateStore holds task metadata updates independent of the tasks package.
type TaskUpdateStore struct {
	mu    sync.Mutex
	tasks map[string]*TaskMetadata
}

type TaskMetadata struct {
	ID          string    `json:"id"`
	Title       string    `json:"title,omitempty"`
	Priority    int       `json:"priority"`
	Context     []string  `json:"context,omitempty"`
	MaxBudget   int       `json:"max_budget,omitempty"`
	Deadline    time.Time `json:"deadline,omitempty"`
	Status      string    `json:"status"`
	UpdatedAt   time.Time `json:"updated_at"`
	UpdateCount int       `json:"update_count"`
}

var globalTaskUpdateStore = &TaskUpdateStore{tasks: make(map[string]*TaskMetadata)}

func GetTaskUpdateStore() *TaskUpdateStore { return globalTaskUpdateStore }

func (s *TaskUpdateStore) Update(id string, updates map[string]any) (*TaskMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[id]
	if !ok {
		task = &TaskMetadata{ID: id, Status: "pending"}
		s.tasks[id] = task
	}

	if v, ok := updates["title"].(string); ok {
		task.Title = v
	}
	if v, ok := updates["priority"].(float64); ok {
		task.Priority = int(v)
	}
	if v, ok := updates["priority"].(int); ok {
		task.Priority = v
	}
	if v, ok := updates["add_context"].(string); ok && v != "" {
		task.Context = append(task.Context, v)
	}
	if v, ok := updates["max_budget"].(float64); ok {
		task.MaxBudget = int(v)
	}
	if v, ok := updates["max_budget"].(int); ok {
		task.MaxBudget = v
	}
	if v, ok := updates["deadline"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			task.Deadline = t
		}
	}
	if v, ok := updates["status"].(string); ok {
		task.Status = v
	}

	task.UpdatedAt = time.Now()
	task.UpdateCount++

	copy := *task
	return &copy, nil
}

func (s *TaskUpdateStore) Get(id string) (*TaskMetadata, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return nil, false
	}
	copy := *t
	return &copy, true
}

// NewTaskUpdateTool creates the task_update tool.
func NewTaskUpdateTool() types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "task_update" },
		DescriptionFn: func() string {
			return "Update metadata of a background task: title, priority, context, budget, deadline, or status."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "Task ID to update",
					},
					"title": map[string]any{
						"type":        "string",
						"description": "New title",
					},
					"priority": map[string]any{
						"type":        "integer",
						"description": "New priority (0 = normal, higher = more important)",
					},
					"add_context": map[string]any{
						"type":        "string",
						"description": "Additional context message to append",
					},
					"max_budget": map[string]any{
						"type":        "integer",
						"description": "Max token budget",
					},
					"deadline": map[string]any{
						"type":        "string",
						"description": "RFC3339 deadline",
					},
					"status": map[string]any{
						"type":        "string",
						"enum":        []string{"pending", "running", "paused", "completed", "cancelled", "failed"},
						"description": "New status",
					},
				},
				"required": []string{"task_id"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return false },
		IsDestructiveFn: func(map[string]any) bool { return false },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			taskID, _ := args["task_id"].(string)
			if taskID == "" {
				return types.ToolResult{IsError: true, Content: "task_id is required"}, nil
			}

			updates := make(map[string]any)
			for k, v := range args {
				if k == "task_id" {
					continue
				}
				updates[k] = v
			}

			task, err := GetTaskUpdateStore().Update(taskID, updates)
			if err != nil {
				return types.ToolResult{IsError: true, Content: err.Error()}, nil
			}

			data, _ := json.Marshal(task)
			return types.ToolResult{Content: fmt.Sprintf("Task updated (%d updates total):\n%s", task.UpdateCount, string(data))}, nil
		},
	}
}

// RegisterTaskUpdateTool registers the tool.
func RegisterTaskUpdateTool(reg *Registry) {
	reg.RegisterDef(NewTaskUpdateTool())
}
