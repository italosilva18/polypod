package commands

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/scheduler"
	"github.com/costa/polypod/internal/tasks"
)

// BindTaskRuntime replaces provisional task commands with direct operations
// against the live task registry and durable scheduler.
func BindTaskRuntime(reg *command.Registry, taskReg *tasks.TaskRegistry, sched *scheduler.Scheduler, logger *slog.Logger) {
	if reg == nil || taskReg == nil || sched == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	reg.Register(&command.Command{Name: "task-status", Description: "Mostrar estado real das tarefas",
		Type: command.CommandLocal, Source: command.SourceBuiltin,
		Execute: func(args []string, _ command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				all := taskReg.List()
				if len(all) == 0 {
					if err := taskReg.PersistenceError(); err != nil {
						return &command.CommandResult{Display: "Nenhuma tarefa carregada. Recuperacao indisponivel: " + err.Error()}, nil
					}
					return &command.CommandResult{Display: "Nenhuma tarefa em segundo plano."}, nil
				}
				sort.Slice(all, func(i, j int) bool { return all[i].StartTime.After(all[j].StartTime) })
				var out strings.Builder
				for _, task := range all {
					fmt.Fprintf(&out, "%s  %-10s  %s\n", task.ID, task.Status, task.Description)
				}
				if err := taskReg.PersistenceError(); err != nil {
					fmt.Fprintf(&out, "\nPersistencia indisponivel: %s\n", err)
				}
				return &command.CommandResult{Display: strings.TrimSpace(out.String())}, nil
			}
			state, ok := taskReg.Get(args[0])
			if !ok {
				return &command.CommandResult{Display: "Tarefa nao encontrada: " + args[0]}, nil
			}
			var out strings.Builder
			fmt.Fprintf(&out, "ID: %s\nTipo: %s\nEstado: %s\nDescricao: %s\nInicio: %s\n",
				state.ID, state.Type, state.Status, state.Description, state.StartTime.Format(time.RFC3339))
			if state.Attempt > 1 {
				fmt.Fprintf(&out, "Tentativa: %d (reinicio de %s)\n", state.Attempt, state.RestartOf)
			}
			if state.EndTime != nil {
				fmt.Fprintf(&out, "Fim: %s\n", state.EndTime.Format(time.RFC3339))
			}
			if state.Type == tasks.TaskTypeAgent {
				fmt.Fprintf(&out, "Agente: %s\n", state.AgentName)
				if state.Progress != nil {
					fmt.Fprintf(&out, "Progresso: %s; %d ferramentas; %d tokens\n", state.Progress.Summary,
						state.Progress.ToolUseCount, state.Progress.TokenCount)
				}
			}
			if state.Type == tasks.TaskTypeShell {
				if state.ProcessID != 0 {
					fmt.Fprintf(&out, "PID original: %d\n", state.ProcessID)
				}
				if state.ExitCode != nil {
					fmt.Fprintf(&out, "Codigo de saida: %d\n", *state.ExitCode)
				}
				if state.Status == tasks.StatusInterrupted && state.ProcessID != 0 {
					out.WriteString("A execucao anterior pode ter deixado um processo externo ativo.\n")
				}
			}
			if state.Error != "" {
				fmt.Fprintf(&out, "Erro: %s\n", state.Error)
			}
			if err := taskReg.PersistenceError(); err != nil {
				fmt.Fprintf(&out, "Persistencia indisponivel: %s\n", err)
			}
			return &command.CommandResult{Display: strings.TrimSpace(out.String())}, nil
		}})
	reg.Register(&command.Command{Name: "task-logs", Description: "Ler saida real de uma tarefa",
		Type: command.CommandLocal, Source: command.SourceBuiltin,
		Execute: func(args []string, _ command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return &command.CommandResult{Display: "Uso: /task-logs <id>"}, nil
			}
			state, ok := taskReg.Get(args[0])
			if !ok {
				return &command.CommandResult{Display: "Tarefa nao encontrada: " + args[0]}, nil
			}
			if state.OutputFile == "" {
				return &command.CommandResult{Display: "Essa tarefa nao possui arquivo de saida."}, nil
			}
			output, err := tasks.ReadTaskOutput(taskReg, state.ID, 64*1024)
			if err != nil {
				return nil, fmt.Errorf("lendo saida da tarefa %s: %w", state.ID, err)
			}
			if output == "" {
				output = "Ainda nao ha saida para esta tarefa."
			}
			return &command.CommandResult{Display: output}, nil
		}})
	reg.Register(&command.Command{Name: "task-restart", Description: "Reiniciar uma tarefa concluida",
		Type: command.CommandLocal, Source: command.SourceBuiltin,
		Execute: func(args []string, _ command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return &command.CommandResult{Display: "Uso: /task-restart <id>"}, nil
			}
			started, err := taskReg.Restart(context.Background(), args[0], logger)
			if err != nil {
				return nil, err
			}
			return &command.CommandResult{Display: fmt.Sprintf("Tarefa reiniciada com novo ID %s. Original: %s", started.ID, args[0])}, nil
		}})
	reg.Register(&command.Command{Name: "schedule", Description: "Gerenciar rotinas reais e persistentes",
		Type: command.CommandLocal, Source: command.SourceBuiltin,
		Execute: func(args []string, _ command.CommandContext) (*command.CommandResult, error) {
			return executeScheduleCommand(args, sched, logger)
		}})
}

func executeScheduleCommand(args []string, sched *scheduler.Scheduler, logger *slog.Logger) (*command.CommandResult, error) {
	usage := "Uso: /schedule list | get <id> | history <id> | run <id> | remove <id> | enable <id> | disable <id> | add <cron> -- <tarefa> | update <id> <cron> -- <tarefa>"
	if len(args) == 0 {
		return &command.CommandResult{Display: usage}, nil
	}
	action := strings.ToLower(args[0])
	switch action {
	case "list":
		all := sched.ListTasks()
		if len(all) == 0 {
			return &command.CommandResult{Display: "Nenhuma rotina agendada."}, nil
		}
		sort.Slice(all, func(i, j int) bool { return all[i].NextRunAt.Before(all[j].NextRunAt) })
		var out strings.Builder
		for _, task := range all {
			status := "ativa"
			if !task.Enabled {
				status = "pausada"
			}
			fmt.Fprintf(&out, "%s  %s  %s  proxima: %s  %s\n", task.ID, status, task.Schedule,
				task.NextRunAt.Format(time.RFC3339), task.Name)
		}
		return &command.CommandResult{Display: strings.TrimSpace(out.String())}, nil
	case "add", "update":
		start := 1
		if action == "update" {
			start = 2
		}
		if len(args) <= start {
			return &command.CommandResult{Display: usage}, nil
		}
		spec := strings.Join(args[start:], " ")
		parts := strings.SplitN(spec, " -- ", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return &command.CommandResult{Display: usage}, nil
		}
		schedule := strings.TrimSpace(parts[0])
		prompt := strings.TrimSpace(parts[1])
		name := []rune(prompt)
		if len(name) > 64 {
			name = name[:64]
		}
		if action == "update" {
			if _, exists := sched.GetTask(args[1]); !exists {
				return &command.CommandResult{Display: "Rotina nao encontrada: " + args[1]}, nil
			}
			if err := sched.UpdateTask(args[1], map[string]string{"schedule": schedule, "prompt": prompt, "name": string(name)}); err != nil {
				return nil, err
			}
			return &command.CommandResult{Display: "Rotina atualizada: " + args[1]}, nil
		}
		task := &scheduler.Task{Name: string(name), Schedule: schedule, Prompt: prompt,
			TimeZone: time.Now().Location().String(), Channel: "cli", UserID: "owner", MisfirePolicy: "run_once", MaxRetries: 1}
		if err := sched.AddTask(task); err != nil {
			return nil, err
		}
		return &command.CommandResult{Display: fmt.Sprintf("Rotina criada: %s. Proxima execucao: %s", task.ID,
			task.NextRunAt.Format(time.RFC3339))}, nil
	case "get", "history", "run", "remove", "enable", "disable":
		if len(args) < 2 {
			return &command.CommandResult{Display: usage}, nil
		}
		id := args[1]
		task, exists := sched.GetTask(id)
		if !exists {
			return &command.CommandResult{Display: "Rotina nao encontrada: " + id}, nil
		}
		switch action {
		case "get":
			return &command.CommandResult{Display: fmt.Sprintf("ID: %s\nNome: %s\nExpressao: %s\nProxima execucao: %s\nAtiva: %t\nUltimo estado: %s\nUltimo erro: %s",
				task.ID, task.Name, task.Schedule, task.NextRunAt.Format(time.RFC3339), task.Enabled, task.LastStatus, task.LastError)}, nil
		case "history":
			history, err := sched.History(id)
			if err != nil {
				return nil, err
			}
			if len(history) == 0 {
				return &command.CommandResult{Display: "Rotina ainda nao executada."}, nil
			}
			if len(history) > 10 {
				history = history[len(history)-10:]
			}
			var out strings.Builder
			for _, run := range history {
				fmt.Fprintf(&out, "%s  tentativa %d  %s  %s", run.StartedAt.Format(time.RFC3339),
					run.Attempt, run.Status, run.Duration)
				if run.Error != "" {
					fmt.Fprintf(&out, "  erro: %s", run.Error)
				}
				out.WriteByte('\n')
			}
			return &command.CommandResult{Display: strings.TrimSpace(out.String())}, nil
		case "run":
			if task.CurrentRunID != "" {
				return &command.CommandResult{Display: "Rotina ja esta em execucao: " + id}, nil
			}
			go func() {
				if _, err := sched.RunNow(id); err != nil {
					logger.Error("manual routine failed", "routine_id", id, "error", err)
				}
			}()
			return &command.CommandResult{Display: "Execucao da rotina solicitada: " + id + ". Consulte /schedule history " + id}, nil
		case "remove":
			if err := sched.RemoveTask(id); err != nil {
				return nil, err
			}
			return &command.CommandResult{Display: "Rotina removida: " + id}, nil
		case "enable", "disable":
			if err := sched.SetEnabled(id, action == "enable"); err != nil {
				return nil, err
			}
			return &command.CommandResult{Display: "Estado atualizado: " + id}, nil
		}
	}
	return &command.CommandResult{Display: usage}, nil
}
