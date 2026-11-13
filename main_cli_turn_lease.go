package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/session"
	"github.com/costa/polypod/internal/turncheckpoint"
)

const cliProfileLeaseID = "cli:local"

type cliTurnGuard struct {
	release  func() error
	branchID string
	header   *session.SessionHeader
	storage  session.SessionStorage
}

// beginCLITurnGuard must run before GetSession. The profile lease also covers
// startup restoration and /recover discard, so another CLI cannot execute a
// tool from an older in-memory transcript while this turn is in progress.
func beginCLITurnGuard(ctx context.Context, msg adapter.InMessage, turns *turncheckpoint.Manager, storage session.SessionStorage) (*cliTurnGuard, error) {
	if msg.Channel == "acp" {
		if msg.UserID == "" || turns == nil {
			return nil, errors.New("sessão ACP ou gerenciador de turnos indisponível")
		}
		release, err := turns.AcquireBranchLease(ctx, "acp:"+msg.UserID)
		if err != nil {
			return nil, err
		}
		return &cliTurnGuard{release: release}, nil
	}
	if msg.Channel != "cli" {
		return nil, nil
	}
	if msg.UserID != "local" || turns == nil {
		return nil, errors.New("sessão CLI local ou gerenciador de turnos indisponível")
	}
	release, err := turns.AcquireBranchLease(ctx, cliProfileLeaseID)
	if err != nil {
		return nil, err
	}
	guard := &cliTurnGuard{release: release, storage: storage}
	if msg.Metadata != nil {
		guard.branchID = msg.Metadata["polypod_session_id"]
	}
	if storage == nil {
		return guard, nil
	}
	if guard.branchID == "" {
		release()
		return nil, errors.New("ID da branch CLI ausente")
	}
	loader, ok := storage.(interface {
		LoadHeader(string) (*session.SessionHeader, error)
	})
	if !ok {
		release()
		return nil, errors.New("armazenamento CLI sem revisão de sessão")
	}
	guard.header, err = loader.LoadHeader(guard.branchID)
	if err != nil {
		release()
		return nil, err
	}
	expected := msg.Metadata["polypod_session_revision"]
	actual := ""
	if guard.header != nil {
		actual = guard.header.Revision
	}
	if actual != expected {
		release()
		return nil, fmt.Errorf("%w: recarregue a branch antes de executar ferramentas", session.ErrSessionConflict)
	}
	return guard, nil
}

func (g *cliTurnGuard) Close() error {
	if g == nil || g.release == nil {
		return nil
	}
	return g.release()
}

// PublishSnapshot advances the manifest before the profile lease is released.
// The returned revision must be delivered to the TUI before its next turn.
func (g *cliTurnGuard) PublishSnapshot(sess *conversation.Session, agentName string) (string, error) {
	if g == nil || g.storage == nil {
		return "", nil
	}
	if sess == nil || g.branchID == "" {
		return "", errors.New("snapshot CLI indisponível")
	}
	now := time.Now()
	metadata := session.SessionMetadata{Tag: "cli", Mode: "interactive"}
	created := sess.CreatedAt
	revision := ""
	if g.header != nil {
		metadata = g.header.Metadata
		if !g.header.CreatedAt.IsZero() {
			created = g.header.CreatedAt
		}
		revision = g.header.Revision
	}
	if metadata.Tag == "" {
		metadata.Tag = "cli"
	}
	cwd, _ := os.Getwd()
	if metadata.ProjectRoot == "" {
		metadata.ProjectRoot = cwd
	}
	if metadata.GitBranch == "" && filepath.Clean(metadata.ProjectRoot) == filepath.Clean(cwd) {
		if environment := session.DetectEnvironment(cwd); environment != nil {
			metadata.GitBranch = environment.GitBranch
		}
	}
	if agentName != "" {
		metadata.AgentName = agentName
	}
	messages := make([]session.Message, len(sess.Messages))
	for i, m := range sess.Messages {
		messages[i] = session.Message{ID: m.ID, Role: string(m.Role), Content: m.Content, Timestamp: m.Timestamp}
	}
	snapshot := &session.RestoredSession{ID: g.branchID, CreatedAt: created, UpdatedAt: now,
		Messages: messages, Metadata: metadata, Revision: revision}
	snapshot.Title = session.TitleFromFirstMessage(messages)
	paged, _ := g.storage.(pagedShutdownStorage)
	if err := saveShutdownSessionSnapshot(g.storage, paged, g.header, snapshot); err != nil {
		return "", err
	}
	g.header = &session.SessionHeader{ID: g.branchID, Revision: snapshot.Revision, Metadata: metadata,
		CreatedAt: created, UpdatedAt: now, MessageCount: len(messages), PrefixHash: session.MessagePrefixHash(messages)}
	return snapshot.Revision, nil
}
