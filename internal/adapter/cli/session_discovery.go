package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/session"
	"github.com/costa/polypod/internal/suggestions"
	"github.com/costa/polypod/internal/taskqueue"
)

func (a *Adapter) startPeerRegistration() func() {
	if a.dataDir == "" {
		return func() {}
	}
	cwd, err := os.Getwd()
	if err != nil {
		a.logger.Warn("diretório da sessão indisponível", "error", err)
	}
	manager := session.NewConcurrentSessionManager(filepath.Join(a.dataDir, "runtime"), a.logger)
	cleanup, err := manager.Register(a.ActiveSessionID(), cwd, session.KindInteractive)
	if err != nil {
		a.logger.Warn("registro de sessões simultâneas indisponível", "error", err)
		return func() {}
	}
	a.sessionPeers = manager
	if err := manager.UpdateActivity(session.PeerStatusIdle, ""); err != nil {
		a.logger.Warn("estado inicial da sessão indisponível", "error", err)
	}
	return func() {
		a.sessionPeers = nil
		cleanup()
	}
}

func (a *Adapter) trackPeerActivity(next adapter.StreamHandler) adapter.StreamHandler {
	manager := a.sessionPeers
	if manager == nil || next == nil {
		return next
	}
	return func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		if err := manager.UpdateActivity(session.PeerStatusBusy, "modelo"); err != nil {
			a.logger.Warn("estado da sessão não atualizado", "error", err)
		}
		defer func() {
			if err := manager.UpdateActivity(session.PeerStatusIdle, ""); err != nil {
				a.logger.Warn("estado da sessão não atualizado", "error", err)
			}
		}()
		next(ctx, msg, chunks)
	}
}

func (a *Adapter) attachDiscoveryDeps(deps *commandDeps, ctx context.Context) {
	if a.sessionPeers != nil {
		deps.listLiveSessions = func() (string, error) {
			peers, err := a.sessionPeers.DetectOtherSessions()
			if err != nil {
				return "", err
			}
			return formatLiveSessions(a.ActiveSessionID(), peers), nil
		}
	}
	deps.suggestPrompts = func() ([]string, error) {
		var lastUser, lastAssistant string
		if a.convMgr != nil {
			current, err := a.convMgr.GetSession(ctx, "cli", "local")
			if err != nil {
				return nil, err
			}
			for _, msg := range a.convMgr.GetHistory(current) {
				switch string(msg.Role) {
				case "user":
					lastUser = msg.Content
				case "assistant":
					lastAssistant = msg.Content
				}
			}
		}
		hints := suggestions.ContextHints{GitStatus: currentGitStatus(ctx)}
		if a.taskQueue != nil {
			tasks, err := a.taskQueue.ListByChannel(ctx, "cli_bg", 10)
			if err == nil {
				for _, task := range tasks {
					if task.Status == taskqueue.Running {
						hints.CurrentTask = task.Title
						break
					}
				}
			}
		}
		items := suggestions.SuggestFollowUps(lastUser, lastAssistant, hints)
		prompts := make([]string, 0, len(items))
		for _, item := range items {
			prompts = append(prompts, item.Text)
		}
		return prompts, nil
	}
}

func currentGitStatus(ctx context.Context) string {
	gitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, "git", "status", "--porcelain=v1", "--untracked-files=normal")
	output, err := cmd.Output()
	if err != nil || len(output) == 0 {
		return ""
	}
	for _, line := range strings.Split(string(output), "\n") {
		if len(line) > 1 && line[0] != ' ' && line[0] != '?' {
			return "staged"
		}
	}
	return "modified"
}

func formatLiveSessions(currentID string, peers []session.PeerSession) string {
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].StartedAt < peers[j].StartedAt
	})
	var builder strings.Builder
	builder.WriteString(cmdTitleStyle.Render("Sessões ativas"))
	builder.WriteString("\n\n")
	fmt.Fprintf(&builder, "  Esta sessão: %s (PID %d)\n", currentID, os.Getpid())
	if len(peers) == 0 {
		builder.WriteString("  Nenhuma outra sessão neste perfil.\n")
		return builder.String()
	}
	for _, peer := range peers {
		status := "ativo"
		switch peer.Status {
		case session.PeerStatusBusy:
			status = "ocupado"
		case session.PeerStatusIdle:
			status = "ocioso"
		case session.PeerStatusWaiting:
			status = "aguardando"
		}
		started := time.UnixMilli(peer.StartedAt).Local().Format("02/01 15:04")
		fmt.Fprintf(&builder, "  %s  PID %d  %s  desde %s\n", peer.SessionID, peer.PID, status, started)
		if peer.Cwd != "" {
			fmt.Fprintf(&builder, "    %s\n", peer.Cwd)
		}
	}
	return builder.String()
}

func handleSuggestions(deps commandDeps) commandResult {
	if deps.suggestPrompts == nil {
		return commandResult{output: "Sugestões indisponíveis nesta sessão.", handled: true}
	}
	items, err := deps.suggestPrompts()
	if err != nil {
		return commandResult{output: errorStyle.Render(fmt.Sprintf("erro: %v", err)), handled: true}
	}
	if len(items) == 0 {
		return commandResult{output: "Nenhuma sugestão útil para esta conversa.", handled: true}
	}
	var builder strings.Builder
	builder.WriteString(cmdTitleStyle.Render("Próximas ações sugeridas"))
	builder.WriteString("\n\n")
	for i, item := range items {
		fmt.Fprintf(&builder, "  %d. %s\n", i+1, cmdValueStyle.Render(item))
	}
	return commandResult{output: builder.String(), handled: true}
}
