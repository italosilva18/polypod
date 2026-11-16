// Package components — Task List V2 UI
package components

import "fmt"

// TaskStatus represents task status
type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"
	TaskInProgress TaskStatus = "in_progress"
	TaskCompleted  TaskStatus = "completed"
	TaskFailed     TaskStatus = "failed"
)

// TaskListV2 represents task list UI
type TaskListV2 struct {
	tasks    []TaskItem
	selected int
	showCompleted bool
}

// TaskItem represents a task item
type TaskItem struct {
	ID          string
	Name        string
	Description string
	Status      TaskStatus
	Assignee    string
	Priority    string
}

// NewTaskListV2 creates new task list
func NewTaskListV2() *TaskListV2 {
	return &TaskListV2{
		tasks: make([]TaskItem, 0),
	}
}

// AddTask adds a task
func (l *TaskListV2) AddTask(task TaskItem) {
	l.tasks = append(l.tasks, task)
}

// UpdateTask updates a task
func (l *TaskListV2) UpdateTask(id string, status TaskStatus) {
	for i, task := range l.tasks {
		if task.ID == id {
			l.tasks[i].Status = status
			break
		}
	}
}

// SetShowCompleted sets completed visibility
func (l *TaskListV2) SetShowCompleted(show bool) {
	l.showCompleted = show
}

// Up moves selection up
func (l *TaskListV2) Up() {
	if l.selected > 0 {
		l.selected--
	}
}

// Down moves selection down
func (l *TaskListV2) Down() {
	if l.selected < len(l.tasks)-1 {
		l.selected++
	}
}

// Render renders the task list
func (l *TaskListV2) Render() string {
	var sb strings.Builder
	sb.WriteString("📋 Task List\n\n")
	
	for i, task := range l.tasks {
		if task.Status == TaskCompleted && !l.showCompleted {
			continue
		}
		
		prefix := "  "
		if i == l.selected {
			prefix = "👉 "
		}
		
		icon := l.getStatusIcon(task.Status)
		sb.WriteString(fmt.Sprintf("%s%s **%s** - %s\n", 
			prefix, icon, task.Name, task.Description))
	}
	
	return sb.String()
}

// GetSelected returns selected task
func (l *TaskListV2) GetSelected() *TaskItem {
	if len(l.tasks) == 0 {
		return nil
	}
	return &l.tasks[l.selected]
}

func (l *TaskListV2) getStatusIcon(status TaskStatus) string {
	switch status {
	case TaskPending:
		return "⏳"
	case TaskInProgress:
		return "🔄"
	case TaskCompleted:
		return "✅"
	case TaskFailed:
		return "❌"
	default:
		return "  "
	}
}
