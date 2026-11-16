// Package task — Task Management System extracted from Claude Code
// Sources: src/tools/TaskCreateTool/, src/tools/TaskListTool/, etc.

package task

import (
	"fmt"
	"strings"
	"time"
)

// TaskStatus represents task status
type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"
	TaskInProgress TaskStatus = "in_progress"
	TaskCompleted  TaskStatus = "completed"
	TaskFailed     TaskStatus = "failed"
	TaskStopped    TaskStatus = "stopped"
)

// Task represents a task
type Task struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Status      TaskStatus        `json:"status"`
	Prompt      string            `json:"prompt"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	CompletedAt *time.Time        `json:"completed_at,omitempty"`
	MaxTurns    int               `json:"max_turns"`
	TurnsUsed   int               `json:"turns_used"`
	Model       string            `json:"model,omitempty"`
	Tools       []string          `json:"tools,omitempty"`
	ParentID    string            `json:"parent_id,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// TaskResult holds task execution result
type TaskResult struct {
	ID        string    `json:"id"`
	Output    string    `json:"output"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"`
	Duration  int64     `json:"duration_ms"`
	TurnsUsed int       `json:"turns_used"`
}

// TaskManager manages tasks
type TaskManager struct {
	tasks map[string]*Task
}

// NewTaskManager creates a new task manager
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks: make(map[string]*Task),
	}
}

// CreateTask creates a new task
func (m *TaskManager) CreateTask(name, description, prompt string, maxTurns int) (*Task, error) {
	if name == "" {
		return nil, fmt.Errorf("task name required")
	}
	if prompt == "" {
		return nil, fmt.Errorf("task prompt required")
	}

	task := &Task{
		ID:          generateTaskID(),
		Name:        name,
		Description: description,
		Prompt:      prompt,
		Status:      TaskPending,
		MaxTurns:    maxTurns,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Metadata:    make(map[string]string),
	}

	m.tasks[task.ID] = task
	return task, nil
}

// GetTask returns a task by ID
func (m *TaskManager) GetTask(id string) (*Task, error) {
	task, exists := m.tasks[id]
	if !exists {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	return task, nil
}

// ListTasks lists all tasks with optional status filter
func (m *TaskManager) ListTasks(status TaskStatus) []*Task {
	var tasks []*Task
	for _, task := range m.tasks {
		if status == "" || task.Status == status {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

// UpdateTask updates task status
func (m *TaskManager) UpdateTask(id string, status TaskStatus) error {
	task, err := m.GetTask(id)
	if err != nil {
		return err
	}

	task.Status = status
	task.UpdatedAt = time.Now()

	if status == TaskCompleted || status == TaskFailed || status == TaskStopped {
		now := time.Now()
		task.CompletedAt = &now
	}

	return nil
}

// StopTask stops a running task
func (m *TaskManager) StopTask(id string) error {
	return m.UpdateTask(id, TaskStopped)
}

// DeleteTask deletes a task
func (m *TaskManager) DeleteTask(id string) error {
	if _, exists := m.tasks[id]; !exists {
		return fmt.Errorf("task not found: %s", id)
	}
	delete(m.tasks, id)
	return nil
}

// GetTaskOutput returns task output
func (m *TaskManager) GetTaskOutput(id string) (*TaskResult, error) {
	task, err := m.GetTask(id)
	if err != nil {
		return nil, err
	}

	result := &TaskResult{
		ID:        task.ID,
		Success:   task.Status == TaskCompleted,
		TurnsUsed: task.TurnsUsed,
	}

	if task.Status == TaskFailed {
		result.Error = "Task failed"
	} else if task.Status == TaskStopped {
		result.Error = "Task stopped"
	} else if task.Status == TaskCompleted {
		result.Output = fmt.Sprintf("Task %s completed successfully", task.Name)
	} else {
		result.Output = fmt.Sprintf("Task %s in progress", task.Name)
	}

	return result, nil
}

// generateTaskID generates a unique task ID
func generateTaskID() string {
	return fmt.Sprintf("task_%d", time.Now().UnixNano())
}

// FormatTask formats task for display
func FormatTask(task *Task) string {
	var sb strings.Builder
	
	statusIcon := "⏳"
	switch task.Status {
	case TaskPending:
		statusIcon = "⏳"
	case TaskInProgress:
		statusIcon = "🔄"
	case TaskCompleted:
		statusIcon = "✅"
	case TaskFailed:
		statusIcon = "❌"
	case TaskStopped:
		statusIcon = "🛑"
	}
	
	sb.WriteString(fmt.Sprintf("%s **%s** (%s)\n", statusIcon, task.Name, task.ID))
	sb.WriteString(fmt.Sprintf("   %s\n", task.Description))
	sb.WriteString(fmt.Sprintf("   Status: %s\n", task.Status))
	sb.WriteString(fmt.Sprintf("   Turns: %d/%d\n", task.TurnsUsed, task.MaxTurns))
	
	if task.CompletedAt != nil {
		sb.WriteString(fmt.Sprintf("   Completed: %s\n", task.CompletedAt.Format(time.RFC3339)))
	}
	
	return sb.String()
}

// FormatTaskList formats task list for display
func FormatTaskList(tasks []*Task) string {
	var sb strings.Builder
	sb.WriteString("📋 **Tasks**:\n\n")
	
	if len(tasks) == 0 {
		sb.WriteString("No tasks found.\n")
		return sb.String()
	}
	
	for _, task := range tasks {
		sb.WriteString(FormatTask(task))
		sb.WriteString("\n")
	}
	
	return sb.String()
}

// Built-in task templates
var TaskTemplates = map[string]string{
	"code-review": "Review code for quality, security, and best practices",
	"debug": "Debug and fix the reported issue",
	"test": "Write and run tests for the code",
	"research": "Research and gather information on the topic",
	"implement": "Implement the specified feature",
	"refactor": "Refactor code for better structure and maintainability",
}

// GetTaskTemplate returns a task template by name
func GetTaskTemplate(name string) string {
	if template, exists := TaskTemplates[name]; exists {
		return template
	}
	return ""
}

// ListTaskTemplates returns all task template names
func ListTaskTemplates() []string {
	templates := make([]string, 0, len(TaskTemplates))
	for name := range TaskTemplates {
		templates = append(templates, name)
	}
	return templates
}
