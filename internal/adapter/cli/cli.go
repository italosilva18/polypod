package cli

import (
	"context"
	"fmt"
	"log/slog"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/skill"
)

// Adapter implements an interactive CLI chat channel using Bubbletea.
type Adapter struct {
	streamHandler adapter.StreamHandler
	aiSvc         *ai.Service
	memStore      memory.Store
	convMgr       *conversation.Manager
	agents        *agent.Registry
	skills        *skill.Registry
	logger        *slog.Logger
	dataDir       string
}

// New creates a new CLI adapter with all dependencies.
func New(
	streamHandler adapter.StreamHandler,
	aiSvc *ai.Service,
	memStore memory.Store,
	convMgr *conversation.Manager,
	agents *agent.Registry,
	skills *skill.Registry,
	logger *slog.Logger,
	dataDir string,
) *Adapter {
	return &Adapter{
		streamHandler: streamHandler,
		aiSvc:         aiSvc,
		memStore:      memStore,
		convMgr:       convMgr,
		agents:        agents,
		skills:        skills,
		logger:        logger,
		dataDir:       dataDir,
	}
}

func (a *Adapter) Name() string { return "cli" }

func (a *Adapter) Start(ctx context.Context, handler adapter.MessageHandler) error {
	deps := a.buildCommandDeps()

	// Use stream handler if available, otherwise wrap the sync handler
	sh := a.streamHandler
	if sh == nil {
		sh = syncToStreamHandler(handler)
	}

	m := newModel(ctx, sh, deps, a.dataDir)
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Cancel program when context is done
	go func() {
		<-ctx.Done()
		p.Quit()
	}()

	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("cli bubbletea: %w", err)
	}
	return nil
}

func (a *Adapter) Send(ctx context.Context, msg adapter.OutMessage) error {
	return nil
}

// buildCommandDeps creates the closure-based command deps from real implementations.
func (a *Adapter) buildCommandDeps() commandDeps {
	deps := commandDeps{}

	if a.aiSvc != nil {
		deps.activeAgent = a.aiSvc.ActiveAgent
		deps.setAgent = a.aiSvc.SetAgent
	}

	if a.agents != nil {
		deps.listAgents = a.agents.List
	}

	if a.skills != nil {
		deps.listSkills = a.skills.List
	}

	if a.memStore != nil {
		deps.listMemories = func() ([]memoryEntry, error) {
			mems, err := a.memStore.List(context.Background())
			if err != nil {
				return nil, err
			}
			entries := make([]memoryEntry, len(mems))
			for i, m := range mems {
				entries[i] = memoryEntry{topic: m.Topic, content: m.Content}
			}
			return entries, nil
		}
		deps.searchMemories = func(query string) ([]memoryEntry, error) {
			mems, err := a.memStore.Search(context.Background(), query)
			if err != nil {
				return nil, err
			}
			entries := make([]memoryEntry, len(mems))
			for i, m := range mems {
				entries[i] = memoryEntry{topic: m.Topic, content: m.Content}
			}
			return entries, nil
		}
	}

	if a.convMgr != nil {
		deps.clearSession = func() error {
			sess, err := a.convMgr.GetSession(context.Background(), "cli", "local")
			if err != nil {
				return err
			}
			return a.convMgr.ClearSession(context.Background(), sess)
		}
		deps.sessionInfo = func() (string, int, error) {
			sess, err := a.convMgr.GetSession(context.Background(), "cli", "local")
			if err != nil {
				return "", 0, err
			}
			return sess.ID, len(sess.Messages), nil
		}
	}

	return deps
}

// syncToStreamHandler wraps a synchronous MessageHandler as a StreamHandler for fallback.
func syncToStreamHandler(handler adapter.MessageHandler) adapter.StreamHandler {
	return func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)

		out, err := handler(ctx, msg)
		if err != nil {
			chunks <- adapter.StreamChunk{Error: err, Done: true}
			return
		}
		chunks <- adapter.StreamChunk{Delta: out.Text}
		chunks <- adapter.StreamChunk{Done: true}
	}
}
