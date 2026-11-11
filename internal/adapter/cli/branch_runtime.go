package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/session"
)

// persistentBranchController stores each branch as an independent CLI session.
// A small manifest records names and fork ancestry. The conversation manager
// always holds the active branch, so the next AI turn uses the selected history.
type persistentBranchController struct {
	mu               sync.Mutex
	directory        string
	storage          session.SessionStorage
	conversation     *conversation.Manager
	activeID         func() string
	activeAgent      func() string
	setActiveID      func(string, string)
	saveActive       func(*conversation.Session) error
	activate         func(session.SessionMetadata) []string
	forkMemory       func(string, string) error
	deleteMemory     func(string) error
	forkTranscript   func(string, string) error
	deleteTranscript func(string) error
	// checkPending refuses to copy or replace a conversation while its last
	// tool call has an unresolved outcome. The CLI adapter owns the check.
	checkPending func() error
	// acquireProfileLease is held through the complete branch mutation, not
	// only the checkpoint inspection before it.
	acquireProfileLease func() (func() error, error)
	// checkTarget refuses to activate a branch whose interrupted turn needs
	// the explicit /resume recovery flow instead of a silent history swap.
	checkTarget func(string, []session.Message) error
}

type branchManifest struct {
	Root     string                  `json:"root"`
	Active   string                  `json:"active"`
	Branches map[string]branchRecord `json:"branches"`
}

type branchRecord struct {
	SessionID string    `json:"session_id"`
	Parent    string    `json:"parent,omitempty"`
	ForkAtMsg int       `json:"fork_at_msg,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func validateBranchManifest(manifest *branchManifest) error {
	main, exists := manifest.Branches["main"]
	if manifest.Root == "" || !exists || main.SessionID != manifest.Root {
		return errors.New("indice de branches sem sessao main valida")
	}
	for name, record := range manifest.Branches {
		if name == "" || record.SessionID == "" {
			return errors.New("indice de branches contem branch incompleta")
		}
	}
	return nil
}

func newPersistentBranchController(
	dataDir string,
	storage session.SessionStorage,
	conv *conversation.Manager,
	activeID func() string,
	setActiveID func(string, string),
	saveActive func(*conversation.Session) error,
) *persistentBranchController {
	return &persistentBranchController{
		directory:    filepath.Join(dataDir, "branches"),
		storage:      storage,
		conversation: conv,
		activeID:     activeID,
		setActiveID:  setActiveID,
		saveActive:   saveActive,
	}
}

func (r *persistentBranchController) callbacks() branchCallbacks {
	return branchCallbacks{
		load:     r.load,
		create:   r.create,
		switchTo: r.switchTo,
		delete:   r.delete,
	}
}

func (r *persistentBranchController) validate() error {
	if r.storage == nil || r.conversation == nil || r.activeID == nil || r.setActiveID == nil || r.saveActive == nil || r.directory == "" {
		return errBranchRuntimeUnavailable
	}
	if r.activeID() == "" {
		return errors.New("ID da sessao CLI indisponivel")
	}
	return nil
}

func (r *persistentBranchController) activeConversation() (*conversation.Session, error) {
	return r.conversation.GetSession(context.Background(), "cli", "local")
}

func (r *persistentBranchController) load() (branchSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validate(); err != nil {
		return branchSnapshot{}, err
	}
	manifest, err := r.manifestForActiveID()
	if err != nil {
		return branchSnapshot{}, err
	}
	active, err := r.activeConversation()
	if err != nil {
		return branchSnapshot{}, err
	}
	return r.snapshot(manifest, active.Messages)
}

func (r *persistentBranchController) create(name string, _ []chatEntry) (branchSnapshot, error) {
	if r.acquireProfileLease != nil {
		release, err := r.acquireProfileLease()
		if err != nil {
			return branchSnapshot{}, err
		}
		defer func() {
			if err := release(); err != nil {
				slog.Warn("CLI branch lease release failed", "error", err)
			}
		}()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validate(); err != nil {
		return branchSnapshot{}, err
	}
	if r.checkPending != nil {
		if err := r.checkPending(); err != nil {
			return branchSnapshot{}, err
		}
	}
	if name == "" {
		return branchSnapshot{}, errBranchEmptyName
	}
	manifest, err := r.manifestForMutation()
	if err != nil {
		return branchSnapshot{}, err
	}
	if _, exists := manifest.Branches[name]; exists {
		return branchSnapshot{}, errBranchExists
	}
	active, err := r.activeConversation()
	if err != nil {
		return branchSnapshot{}, err
	}
	if err := r.saveActive(active); err != nil {
		return branchSnapshot{}, fmt.Errorf("salvando branch atual: %w", err)
	}
	parentID := r.activeID()
	parent, err := r.storage.Load(parentID)
	if err != nil {
		return branchSnapshot{}, err
	}
	metadata := session.SessionMetadata{Tag: "cli", Mode: "interactive"}
	if parent != nil {
		metadata = parent.Metadata
	}
	childID := uuid.NewString()
	agentName := "default"
	if r.activeAgent != nil && r.activeAgent() != "" {
		agentName = r.activeAgent()
	}
	if err := r.conversation.RegisterJournalFork(context.Background(), parentID, childID, agentName, active); err != nil {
		return branchSnapshot{}, fmt.Errorf("registrando ancestralidade do historico: %w", err)
	}
	now := time.Now()
	child := &session.RestoredSession{
		ID: childID, CreatedAt: now, UpdatedAt: now,
		Messages: copySessionMessages(active.Messages), Metadata: metadata,
	}
	child.Title = session.TitleFromFirstMessage(child.Messages)
	if err := r.storage.Save(child); err != nil {
		return branchSnapshot{}, fmt.Errorf("salvando historico da branch: %w", err)
	}
	if r.forkMemory != nil {
		if err := r.forkMemory(parentID, childID); err != nil {
			r.cleanupNewChild(child, false, "falha de memória")
			return branchSnapshot{}, fmt.Errorf("copiando memoria da branch: %w", err)
		}
	}
	if r.forkTranscript != nil {
		if err := r.forkTranscript(parentID, childID); err != nil {
			r.cleanupNewChild(child, true, "falha de transcript")
			return branchSnapshot{}, fmt.Errorf("copiando transcript da branch: %w", err)
		}
	}
	previous := manifest.Active
	manifest.Branches[name] = branchRecord{
		SessionID: childID, Parent: previous,
		ForkAtMsg: len(active.Messages), CreatedAt: now,
	}
	manifest.Active = name
	if err := r.writeManifest(manifest); err != nil {
		delete(manifest.Branches, name)
		manifest.Active = previous
		r.cleanupNewChild(child, true, "falha do índice")
		return branchSnapshot{}, err
	}
	r.setActiveID(childID, child.Revision)
	return r.snapshot(manifest, active.Messages)
}

// cleanupNewChild removes only the child revision created by this operation.
// If another process has changed the child, its session and auxiliary stores
// are left intact for recovery instead of deleting newer work.
func (r *persistentBranchController) cleanupNewChild(child *session.RestoredSession, transcript bool, reason string) {
	var err error
	if pinned, ok := r.storage.(interface {
		DeleteAtRevision(string, string) error
	}); ok {
		if child.Revision == "" {
			slog.Warn("não foi possível remover sessão filha sem revisão", "session", child.ID, "reason", reason)
			return
		}
		err = pinned.DeleteAtRevision(child.ID, child.Revision)
	} else {
		err = r.storage.Delete(child.ID)
	}
	if err != nil {
		slog.Warn("não foi possível remover sessão filha após "+reason, "session", child.ID, "error", err)
		return
	}
	if r.deleteMemory != nil {
		if err := r.deleteMemory(child.ID); err != nil {
			slog.Warn("não foi possível remover memória filha após "+reason, "session", child.ID, "error", err)
		}
	}
	if transcript && r.deleteTranscript != nil {
		if err := r.deleteTranscript(child.ID); err != nil {
			slog.Warn("não foi possível remover transcript filho após "+reason, "session", child.ID, "error", err)
		}
	}
}

func (r *persistentBranchController) switchTo(name string, _ []chatEntry) (branchSnapshot, error) {
	if r.acquireProfileLease != nil {
		release, err := r.acquireProfileLease()
		if err != nil {
			return branchSnapshot{}, err
		}
		defer func() {
			if err := release(); err != nil {
				slog.Warn("CLI branch lease release failed", "error", err)
			}
		}()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validate(); err != nil {
		return branchSnapshot{}, err
	}
	if r.checkPending != nil {
		if err := r.checkPending(); err != nil {
			return branchSnapshot{}, err
		}
	}
	manifest, err := r.manifestForMutation()
	if err != nil {
		return branchSnapshot{}, err
	}
	target, exists := manifest.Branches[name]
	if !exists {
		return branchSnapshot{}, errBranchNotFound
	}
	active, err := r.activeConversation()
	if err != nil {
		return branchSnapshot{}, err
	}
	if target.SessionID == r.activeID() {
		return r.snapshot(manifest, active.Messages)
	}
	if err := r.saveActive(active); err != nil {
		return branchSnapshot{}, fmt.Errorf("salvando branch atual: %w", err)
	}
	stored, err := r.storage.Load(target.SessionID)
	if err != nil {
		return branchSnapshot{}, err
	}
	if stored == nil {
		return branchSnapshot{}, fmt.Errorf("historico da branch %q nao encontrado", name)
	}
	if r.checkTarget != nil {
		if err := r.checkTarget(target.SessionID, stored.Messages); err != nil {
			return branchSnapshot{}, err
		}
	}
	before := append([]conversation.Message(nil), active.Messages...)
	next := toConversationMessages(stored.Messages)
	if err := r.conversation.ReplaceMessages(context.Background(), active, next); err != nil {
		return branchSnapshot{}, fmt.Errorf("ativando historico da branch: %w", err)
	}
	previous := manifest.Active
	manifest.Active = name
	if err := r.writeManifest(manifest); err != nil {
		manifest.Active = previous
		if rollbackErr := r.conversation.ReplaceMessages(context.Background(), active, before); rollbackErr != nil {
			return branchSnapshot{}, fmt.Errorf("%w; falha ao restaurar conversa anterior: %v", err, rollbackErr)
		}
		return branchSnapshot{}, err
	}
	r.setActiveID(target.SessionID, stored.Revision)
	snapshot, err := r.snapshot(manifest, next)
	if err != nil {
		return branchSnapshot{}, err
	}
	if r.activate != nil {
		for _, warning := range r.activate(stored.Metadata) {
			snapshot.messages = append(snapshot.messages, chatEntry{role: "system", content: warning, timestamp: time.Now()})
		}
	}
	return snapshot, nil
}

func (r *persistentBranchController) delete(name string, _ []chatEntry) (branchSnapshot, error) {
	if r.acquireProfileLease != nil {
		release, err := r.acquireProfileLease()
		if err != nil {
			return branchSnapshot{}, err
		}
		defer func() {
			if err := release(); err != nil {
				slog.Warn("CLI branch lease release failed", "error", err)
			}
		}()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validate(); err != nil {
		return branchSnapshot{}, err
	}
	if r.checkPending != nil {
		if err := r.checkPending(); err != nil {
			return branchSnapshot{}, err
		}
	}
	manifest, err := r.manifestForMutation()
	if err != nil {
		return branchSnapshot{}, err
	}
	if name == "main" {
		return branchSnapshot{}, errBranchProtected
	}
	if name == manifest.Active {
		return branchSnapshot{}, errBranchInUse
	}
	if _, exists := manifest.Branches[name]; !exists {
		return branchSnapshot{}, errBranchNotFound
	}
	delete(manifest.Branches, name)
	if err := r.writeManifest(manifest); err != nil {
		return branchSnapshot{}, err
	}
	// Keep the transcript as an independent session so /resume can recover it.
	active, err := r.activeConversation()
	if err != nil {
		return branchSnapshot{}, err
	}
	return r.snapshot(manifest, active.Messages)
}

func copySessionMessages(messages []conversation.Message) []session.Message {
	out := make([]session.Message, len(messages))
	for i, message := range messages {
		out[i] = session.Message{ID: message.ID, Role: string(message.Role), Content: message.Content, Timestamp: message.Timestamp}
	}
	return out
}

func toConversationMessages(messages []session.Message) []conversation.Message {
	out := make([]conversation.Message, len(messages))
	for i, message := range messages {
		out[i] = conversation.Message{ID: message.ID, Role: conversation.Role(message.Role), Content: message.Content, Timestamp: message.Timestamp}
	}
	return out
}

func toBranchEntries(messages []conversation.Message) []chatEntry {
	out := make([]chatEntry, 0, len(messages))
	for _, message := range messages {
		out = append(out, chatEntry{role: string(message.Role), content: message.Content, timestamp: message.Timestamp})
	}
	return out
}

func (r *persistentBranchController) snapshot(manifest *branchManifest, active []conversation.Message) (branchSnapshot, error) {
	result := branchSnapshot{
		branches: make(map[string]*branchState, len(manifest.Branches)),
		current:  manifest.Active,
		messages: toBranchEntries(active),
	}
	for name, record := range manifest.Branches {
		count := len(active)
		if name != manifest.Active {
			stored, err := r.storage.Load(record.SessionID)
			if err != nil {
				slog.Warn("historico de branch indisponivel", "branch", name, "error", err)
				count = 0
			} else if stored == nil {
				slog.Warn("historico de branch ausente", "branch", name)
				count = 0
			} else {
				count = len(stored.Messages)
			}
		}
		result.branches[name] = &branchState{
			name: name, sessionID: record.SessionID, parent: record.Parent,
			forkAtMsg: record.ForkAtMsg, createdAt: record.CreatedAt, messageCount: count,
		}
	}
	return result, nil
}

func (r *persistentBranchController) manifestForActiveID() (*branchManifest, error) {
	return r.manifestForID(r.activeID(), false, true)
}

func (r *persistentBranchController) manifestForMutation() (*branchManifest, error) {
	return r.manifestForID(r.activeID(), true, true)
}

// activateSessionForResume records a deliberate /resume selection while the
// caller holds the profile lease. An independent session starts a new branch
// tree on its first branch command.
func (r *persistentBranchController) activateSessionForResume(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	manifest, err := r.manifestForID(id, false, false)
	if err != nil || manifest == nil {
		return err
	}
	return r.writeManifest(manifest)
}

func (r *persistentBranchController) manifestForID(id string, strict, create bool) (*branchManifest, error) {
	idDigest := sha256.Sum256([]byte(id))
	directName := hex.EncodeToString(idDigest[:]) + ".json"
	entries, err := os.ReadDir(r.directory)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("lendo indice de branches: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.directory, entry.Name()))
		if err != nil {
			if entry.Name() == directName {
				return nil, err
			}
			slog.Warn("ignorando indice de branches inacessivel", "file", entry.Name(), "error", err)
			continue
		}
		var manifest branchManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			if entry.Name() == directName {
				return nil, fmt.Errorf("indice de branches %s invalido: %w", entry.Name(), err)
			}
			slog.Warn("ignorando indice de branches invalido", "file", entry.Name(), "error", err)
			continue
		}
		if err := validateBranchManifest(&manifest); err != nil {
			if entry.Name() == directName {
				return nil, fmt.Errorf("indice de branches %s invalido: %w", entry.Name(), err)
			}
			slog.Warn("ignorando indice de branches invalido", "file", entry.Name(), "error", err)
			continue
		}
		for name, record := range manifest.Branches {
			if record.SessionID == id {
				if strict && manifest.Active != name {
					return nil, fmt.Errorf("%w: a branch ativa mudou em outra instância", session.ErrSessionConflict)
				}
				manifest.Active = name
				return &manifest, nil
			}
		}
	}
	if !create {
		return nil, nil
	}
	manifest := &branchManifest{
		Root: id, Active: "main",
		Branches: map[string]branchRecord{
			"main": {SessionID: id, CreatedAt: time.Now()},
		},
	}
	if err := r.writeManifest(manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func (r *persistentBranchController) writeManifest(manifest *branchManifest) error {
	if err := os.MkdirAll(r.directory, 0700); err != nil {
		return fmt.Errorf("criando diretorio de branches: %w", err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(r.directory, ".branch-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(manifest.Root))
	path := filepath.Join(r.directory, hex.EncodeToString(digest[:])+".json")
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("gravando indice de branches: %w", err)
	}
	// Rename makes updates atomic. Syncing the directory reduces the chance
	// of losing the new name after a sudden power loss. A sync failure cannot
	// be rolled back safely after rename, so report it without undoing state.
	if dir, err := os.Open(r.directory); err == nil {
		if err := dir.Sync(); err != nil {
			slog.Warn("nao foi possivel sincronizar indice de branches", "error", err)
		}
		_ = dir.Close()
	}
	return nil
}
