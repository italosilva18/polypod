package tool

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/tasks"
	"github.com/costa/polypod/internal/tasktool"
	"github.com/costa/polypod/internal/types"
)

// RegisterTaskTools registers all task and todo management tools.
// This replaces the stub implementations with real ones backed by
// tasktool.TaskList (for todos) and tasks.TaskRegistry (for background tasks).
func RegisterTaskTools(reg *Registry, taskList *tasktool.TaskList, taskRegistry *tasks.TaskRegistry, logger *slog.Logger) {
	registerTodoWriteTool(reg, taskList)
	registerTodoReadTool(reg, taskList)
	registerTaskCreateTool(reg, taskRegistry, logger)
	registerTaskGetTool(reg, taskRegistry)
	registerTaskListTool(reg, taskRegistry)
	registerTaskStopTool(reg, taskRegistry, logger)
	registerTaskOutputTool(reg, taskRegistry)
}

// ---------------------------------------------------------------------------
// todo_write — Write/update the todo list
// ---------------------------------------------------------------------------

func registerTodoWriteTool(reg *Registry, tl *tasktool.TaskList) {
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return constants.ToolTodoWrite },
		DescriptionFn: func() string {
			return "Write or update the session todo list. Replaces the entire list with the provided items."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"todos": map[string]any{
						"type":        "array",
						"description": "The complete todo list. Each item has content, status, and priority.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"content":  map[string]any{"type": "string", "description": "Description of the todo item"},
								"status":   map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "cancelled"}, "description": "Current status"},
								"priority": map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}, "description": "Priority level (default: medium)"},
							},
							"required": []string{"content", "status"},
						},
					},
				},
				"required": []string{"todos"},
			}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			todosRaw, ok := args["todos"]
			if !ok {
				return types.ToolResult{Content: "todos parameter is required", IsError: true}, nil
			}
			todosArr, ok := todosRaw.([]any)
			if !ok {
				return types.ToolResult{Content: "todos must be a JSON array", IsError: true}, nil
			}

			// Clear existing list and rebuild
			tl.Clear()

			for _, item := range todosArr {
				obj, ok := item.(map[string]any)
				if !ok {
					continue
				}
				content, _ := obj["content"].(string)
				status, _ := obj["status"].(string)
				priority, _ := obj["priority"].(string)
				if content == "" {
					continue
				}
				if priority == "" {
					priority = "medium"
				}
				if status == "" {
					status = "pending"
				}

				task := tl.Add(content, priority)
				if status != "pending" {
					_ = tl.Update(task.ID, status)
				}
			}

			// If all items are completed, clear the list (match Claude Code behavior)
			allCompleted := true
			for _, t := range tl.List() {
				if t.Status != "completed" {
					allCompleted = false
					break
				}
			}
			if allCompleted && len(tl.List()) > 0 {
				tl.Clear()
			}

			return types.ToolResult{
				Content: "Todos updated successfully. " + tl.Format(),
			}, nil
		},
	})
}

// ---------------------------------------------------------------------------
// todo_read — Read the current todo list
// ---------------------------------------------------------------------------

func registerTodoReadTool(reg *Registry, tl *tasktool.TaskList) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return constants.ToolTodoRead },
		DescriptionFn: func() string { return "Read the current session todo list with status and priority." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			todoList := tl.List()
			if len(todoList) == 0 {
				return types.ToolResult{Content: "No todos. The todo list is empty."}, nil
			}

			var sb strings.Builder
			sb.WriteString("=== Todo List ===\n\n")

			stats := tl.Stats()
			sb.WriteString(fmt.Sprintf("Total: %d | Pending: %d | In Progress: %d | Completed: %d | Cancelled: %d\n\n",
				stats["total"], stats["pending"], stats["in_progress"], stats["completed"], stats["cancelled"]))

			for _, t := range todoList {
				icon := statusIcon(t.Status)
				priority := priorityTag(t.Priority)
				sb.WriteString(fmt.Sprintf("%s %s%s (ID: %s)\n", icon, t.Content, priority, t.ID))
			}

			return types.ToolResult{Content: sb.String()}, nil
		},
	})
}

// ---------------------------------------------------------------------------
// task_create — Create a background task (shell or agent)
// ---------------------------------------------------------------------------

func registerTaskCreateTool(reg *Registry, taskReg *tasks.TaskRegistry, logger *slog.Logger) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return constants.ToolTaskCreate },
		DescriptionFn: func() string { return "Create and start a background task (shell command or agent)." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":        map[string]any{"type": "string", "enum": []string{"shell", "agent"}, "description": "Task type: 'shell' for bash commands, 'agent' for subagent queries"},
					"command":     map[string]any{"type": "string", "description": "Shell command to run (required when type=shell)"},
					"query":       map[string]any{"type": "string", "description": "Query/prompt for the agent (required when type=agent)"},
					"description": map[string]any{"type": "string", "description": "Human-readable description of the task"},
				},
				"required": []string{"type", "description"},
			}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			taskType, _ := args["type"].(string)
			description, _ := args["description"].(string)

			switch taskType {
			case "shell":
				command, _ := args["command"].(string)
				if command == "" {
					return types.ToolResult{Content: "command is required for shell tasks", IsError: true}, nil
				}

				handle, err := tasks.SpawnShellTask(taskReg, command, description, "", logger)
				if err != nil {
					return types.ToolResult{Content: fmt.Sprintf("Failed to create shell task: %v", err), IsError: true}, nil
				}

				return types.ToolResult{
					Content: fmt.Sprintf("Shell task created and running.\nTask ID: %s\nCommand: %s\nDescription: %s\n\nUse task_get with this ID to check status and output.", handle.TaskID, command, description),
				}, nil

			case "agent":
				query, _ := args["query"].(string)
				if query == "" {
					return types.ToolResult{Content: "query is required for agent tasks", IsError: true}, nil
				}

				agentID := tasks.GenerateTaskID(tasks.TaskTypeAgent)
				state := tasks.RegisterLocalAgent(taskReg, agentID, description, query, "general-purpose", "", logger)

				return types.ToolResult{
					Content: fmt.Sprintf("Agent task created and registered.\nTask ID: %s\nQuery: %s\nDescription: %s\n\nUse task_get with this ID to check status.", state.ID, query, description),
				}, nil

			default:
				return types.ToolResult{Content: fmt.Sprintf("Unknown task type: %s. Use 'shell' or 'agent'.", taskType), IsError: true}, nil
			}
		},
	})
}

// ---------------------------------------------------------------------------
// task_get — Get task status, progress, and output
// ---------------------------------------------------------------------------

func registerTaskGetTool(reg *Registry, taskReg *tasks.TaskRegistry) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return constants.ToolTaskGet },
		DescriptionFn: func() string { return "Get the status, progress, and output of a background task." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "description": "The task ID to query"},
				},
				"required": []string{"task_id"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			taskID, _ := args["task_id"].(string)
			if taskID == "" {
				return types.ToolResult{Content: "task_id is required", IsError: true}, nil
			}

			state, ok := taskReg.Get(taskID)
			if !ok {
				return types.ToolResult{Content: fmt.Sprintf("Task %s not found", taskID), IsError: true}, nil
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("=== Task %s ===\n", state.ID))
			sb.WriteString(fmt.Sprintf("Type:        %s\n", state.Type))
			sb.WriteString(fmt.Sprintf("Status:      %s\n", state.Status))
			sb.WriteString(fmt.Sprintf("Description: %s\n", state.Description))
			sb.WriteString(fmt.Sprintf("Started:     %s\n", state.StartTime.Format(time.RFC3339)))
			if state.EndTime != nil {
				sb.WriteString(fmt.Sprintf("Ended:       %s\n", state.EndTime.Format(time.RFC3339)))
				sb.WriteString(fmt.Sprintf("Duration:    %s\n", state.EndTime.Sub(state.StartTime).Round(time.Millisecond)))
			} else {
				sb.WriteString(fmt.Sprintf("Running for: %s\n", time.Since(state.StartTime).Round(time.Second)))
			}

			// For shell tasks, include output
			if state.Type == tasks.TaskTypeShell {
				output, err := tasks.GetShellOutput(taskReg, taskID, 10*1024) // 10KB max
				if err == nil && output != "" {
					sb.WriteString(fmt.Sprintf("\n--- Output (last %d bytes) ---\n%s", len(output), output))
				}
			}

			return types.ToolResult{Content: sb.String()}, nil
		},
	})
}

// ---------------------------------------------------------------------------
// task_list — List all background tasks
// ---------------------------------------------------------------------------

func registerTaskListTool(reg *Registry, taskReg *tasks.TaskRegistry) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return constants.ToolTaskList },
		DescriptionFn: func() string { return "List all background tasks, optionally filtered by status." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{
						"type":        "string",
						"enum":        []string{"pending", "running", "completed", "failed", "killed"},
						"description": "Filter by status (optional). If omitted, returns all tasks.",
					},
				},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			var taskList []*tasks.TaskState

			statusFilter, _ := args["status"].(string)
			if statusFilter != "" {
				taskList = taskReg.ListByStatus(tasks.TaskStatus(statusFilter))
			} else {
				taskList = taskReg.List()
			}

			if len(taskList) == 0 {
				if statusFilter != "" {
					return types.ToolResult{Content: fmt.Sprintf("No tasks with status '%s'.", statusFilter)}, nil
				}
				return types.ToolResult{Content: "No background tasks."}, nil
			}

			var sb strings.Builder
			sb.WriteString("=== Background Tasks ===\n\n")

			// Count by status
			counts := make(map[tasks.TaskStatus]int)
			for _, t := range taskList {
				counts[t.Status]++
			}
			sb.WriteString(fmt.Sprintf("Total: %d", len(taskList)))
			for status, count := range counts {
				sb.WriteString(fmt.Sprintf(" | %s: %d", status, count))
			}
			sb.WriteString("\n\n")

			for _, t := range taskList {
				icon := taskStatusIcon(t.Status)
				duration := ""
				if t.EndTime != nil {
					duration = fmt.Sprintf(" (%s)", t.EndTime.Sub(t.StartTime).Round(time.Millisecond))
				} else if t.Status == tasks.StatusRunning {
					duration = fmt.Sprintf(" (running %s)", time.Since(t.StartTime).Round(time.Second))
				}
				sb.WriteString(fmt.Sprintf("%s [%s] %s — %s%s\n", icon, t.ID, t.Type, t.Description, duration))
			}

			return types.ToolResult{Content: sb.String()}, nil
		},
	})
}

// ---------------------------------------------------------------------------
// task_stop — Stop a running background task
// ---------------------------------------------------------------------------

func registerTaskStopTool(reg *Registry, taskReg *tasks.TaskRegistry, logger *slog.Logger) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return constants.ToolTaskStop },
		DescriptionFn: func() string { return "Stop a running background task by its ID." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "description": "The task ID to stop"},
				},
				"required": []string{"task_id"},
			}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			taskID, _ := args["task_id"].(string)
			if taskID == "" {
				return types.ToolResult{Content: "task_id is required", IsError: true}, nil
			}

			state, ok := taskReg.Get(taskID)
			if !ok {
				return types.ToolResult{Content: fmt.Sprintf("Task %s not found", taskID), IsError: true}, nil
			}

			if state.Status.IsTerminal() {
				return types.ToolResult{Content: fmt.Sprintf("Task %s is already in terminal state: %s", taskID, state.Status)}, nil
			}

			if err := taskReg.Kill(taskID); err != nil {
				logger.Warn("failed to kill task", "task_id", taskID, "error", err)
				// Fallback: force status update
				now := time.Now()
				taskReg.Update(taskID, func(t *tasks.TaskState) {
					t.Status = tasks.StatusKilled
					t.EndTime = &now
					t.Notified = true
				})
				return types.ToolResult{Content: fmt.Sprintf("Task %s force-stopped (killer error: %v)", taskID, err)}, nil
			}

			return types.ToolResult{Content: fmt.Sprintf("Task %s stopped successfully.", taskID)}, nil
		},
	})
}

// ---------------------------------------------------------------------------
// task_output — Get the output of a background task
// ---------------------------------------------------------------------------

func registerTaskOutputTool(reg *Registry, taskReg *tasks.TaskRegistry) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return constants.ToolTaskOutput },
		DescriptionFn: func() string { return "Get the output of a background task." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "description": "The task ID to get output from"},
				},
				"required": []string{"task_id"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			taskID, _ := args["task_id"].(string)
			if taskID == "" {
				return types.ToolResult{Content: "task_id is required", IsError: true}, nil
			}

			state, ok := taskReg.Get(taskID)
			if !ok {
				return types.ToolResult{Content: fmt.Sprintf("Task %s not found", taskID), IsError: true}, nil
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("=== Output for Task %s (%s) ===\n", state.ID, state.Status))

			// Try reading from output file
			if state.OutputFile != "" {
				data, err := os.ReadFile(state.OutputFile)
				if err == nil {
					output := string(data)
					if len(output) > 50*1024 { // 50KB max
						output = "... [truncated]\n" + output[len(output)-50*1024:]
					}
					if output != "" {
						sb.WriteString(output)
					} else {
						sb.WriteString("(no output yet)")
					}
				} else {
					sb.WriteString(fmt.Sprintf("(could not read output file: %v)", err))
				}
			} else {
				sb.WriteString("(no output file configured for this task)")
			}

			return types.ToolResult{Content: sb.String()}, nil
		},
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func statusIcon(status string) string {
	switch status {
	case "completed":
		return "[x]"
	case "in_progress":
		return "[>]"
	case "cancelled":
		return "[-]"
	default:
		return "[ ]"
	}
}

func priorityTag(priority string) string {
	switch priority {
	case "high":
		return " !!!"
	case "low":
		return " ~"
	default:
		return ""
	}
}

func taskStatusIcon(status tasks.TaskStatus) string {
	switch status {
	case tasks.StatusCompleted:
		return "[x]"
	case tasks.StatusRunning:
		return "[>]"
	case tasks.StatusPending:
		return "[ ]"
	case tasks.StatusFailed:
		return "[!]"
	case tasks.StatusKilled:
		return "[-]"
	default:
		return "[?]"
	}
}
