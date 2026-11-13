package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// EventProtocolVersion identifies the durable wire schema for session events
// and turn idempotency receipts shared by TUI, headless, ACP, and channels.
const EventProtocolVersion = 1

// SessionEvent is one immutable, monotonically sequenced state transition in a
// session stream.
type SessionEvent struct {
	Version   int            `json:"version"`
	Sequence  uint64         `json:"sequence"`
	SessionID string         `json:"session_id,omitempty"`
	Channel   string         `json:"channel,omitempty"`
	TurnID    string         `json:"turn_id,omitempty"`
	At        time.Time      `json:"at,omitempty"`
	Update    map[string]any `json:"update"`
}

// TurnReceipt records durable idempotency state for a client-supplied turn key
// before the model or any tool executes.
type TurnReceipt struct {
	Version      int    `json:"version"`
	PromptSHA256 string `json:"prompt_sha256"`
	Status       string `json:"status"`
	StopReason   string `json:"stop_reason,omitempty"`
	Content      string `json:"content,omitempty"`
	Error        string `json:"error,omitempty"`
	LastSequence uint64 `json:"last_sequence,omitempty"`
}

type activeTurnEntry struct {
	token  uint64
	cancel context.CancelFunc
}

// EventJournal persists versioned, sequenced session events, turn idempotency
// receipts, and explicit session-level cancellation across TUI, headless, ACP,
// and remote/messaging channels.
type EventJournal struct {
	mu          sync.Mutex
	root        string
	activeMu    sync.Mutex
	nextToken   uint64
	active      map[string]activeTurnEntry
	subMu       sync.Mutex
	nextSubID   uint64
	subscribers map[string]map[uint64]chan SessionEvent
}

// NewEventJournal creates a durable session event journal rooted at dir.
func NewEventJournal(dir string) *EventJournal {
	return &EventJournal{
		root:        strings.TrimSpace(dir),
		active:      make(map[string]activeTurnEntry),
		subscribers: make(map[string]map[uint64]chan SessionEvent),
	}
}

// Root returns the persistent root directory of the journal.
func (j *EventJournal) Root() string {
	if j == nil {
		return ""
	}
	return j.root
}

func isDirectSessionDirKey(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func (j *EventJournal) sessionDir(sessionID string) (string, error) {
	if j == nil || j.root == "" {
		return "", errors.New("diário de eventos de sessão indisponível")
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" || len(trimmed) > 512 {
		return "", errors.New("ID de sessão inválido")
	}
	if isDirectSessionDirKey(trimmed) {
		return filepath.Join(j.root, trimmed), nil
	}
	digest := sha256.Sum256([]byte(trimmed))
	return filepath.Join(j.root, "s-"+hex.EncodeToString(digest[:])), nil
}

// Append publishes one sequenced event for sessionID.
func (j *EventJournal) Append(sessionID string, update map[string]any) (SessionEvent, error) {
	return j.AppendTurnEvent(sessionID, "", "", update)
}

// AppendTurnEvent publishes one sequenced event annotated with channel and
// turn metadata. Each event is written to a synced temporary file and published
// via os.Link so another process can never silently overwrite an existing
// sequence number.
func (j *EventJournal) AppendTurnEvent(sessionID, channel, turnID string, update map[string]any) (SessionEvent, error) {
	if j == nil || j.root == "" {
		return SessionEvent{}, nil
	}
	if update == nil {
		return SessionEvent{}, errors.New("atualização de sessão vazia")
	}
	dir, err := j.sessionDir(sessionID)
	if err != nil {
		return SessionEvent{}, err
	}

	j.mu.Lock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		j.mu.Unlock()
		return SessionEvent{}, err
	}
	const maxLinkAttempts = 8
	var published SessionEvent
	var linkErr error
	for attempt := 0; attempt < maxLinkAttempts; attempt++ {
		next, seqErr := latestJournalSequence(dir)
		if seqErr != nil {
			j.mu.Unlock()
			return SessionEvent{}, seqErr
		}
		next++
		ev := SessionEvent{
			Version:   EventProtocolVersion,
			Sequence:  next,
			SessionID: sessionID,
			Channel:   channel,
			TurnID:    turnID,
			At:        time.Now().UTC(),
			Update:    update,
		}
		data, marshalErr := json.Marshal(ev)
		if marshalErr != nil {
			j.mu.Unlock()
			return SessionEvent{}, marshalErr
		}
		file, createErr := os.CreateTemp(dir, ".event-*.tmp")
		if createErr != nil {
			j.mu.Unlock()
			return SessionEvent{}, createErr
		}
		tmpName := file.Name()
		if chmodErr := file.Chmod(0o600); chmodErr != nil {
			file.Close()
			os.Remove(tmpName)
			j.mu.Unlock()
			return SessionEvent{}, chmodErr
		}
		if _, writeErr := file.Write(data); writeErr != nil {
			file.Close()
			os.Remove(tmpName)
			j.mu.Unlock()
			return SessionEvent{}, writeErr
		}
		if syncErr := file.Sync(); syncErr != nil {
			file.Close()
			os.Remove(tmpName)
			j.mu.Unlock()
			return SessionEvent{}, syncErr
		}
		if closeErr := file.Close(); closeErr != nil {
			os.Remove(tmpName)
			j.mu.Unlock()
			return SessionEvent{}, closeErr
		}
		target := filepath.Join(dir, fmt.Sprintf("%020d.json", next))
		linkErr = os.Link(tmpName, target)
		os.Remove(tmpName)
		if linkErr == nil {
			if syncErr := syncJournalDir(dir); syncErr != nil {
				j.mu.Unlock()
				return SessionEvent{}, syncErr
			}
			published = ev
			break
		}
		if !errors.Is(linkErr, os.ErrExist) {
			j.mu.Unlock()
			return SessionEvent{}, fmt.Errorf("publicar evento de sessão %d: %w", next, linkErr)
		}
	}
	j.mu.Unlock()
	if published.Sequence == 0 {
		return SessionEvent{}, fmt.Errorf("publicar evento de sessão após contenção: %w", linkErr)
	}
	j.notifySubscribers(sessionID, published)
	return published, nil
}

func latestJournalSequence(dir string) (uint64, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var latest uint64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		number, parseErr := strconv.ParseUint(strings.TrimSuffix(entry.Name(), ".json"), 10, 64)
		if parseErr != nil {
			return 0, fmt.Errorf("nome de evento inválido: %q", entry.Name())
		}
		if number > latest {
			latest = number
		}
	}
	return latest, nil
}

// LatestSequence returns the highest persisted event sequence for sessionID.
func (j *EventJournal) LatestSequence(sessionID string) (uint64, error) {
	if j == nil || j.root == "" {
		return 0, nil
	}
	dir, err := j.sessionDir(sessionID)
	if err != nil {
		return 0, err
	}
	return latestJournalSequence(dir)
}

// ReadAfter returns up to limit durable events with Sequence > after in
// strictly ascending sequence order.
func (j *EventJournal) ReadAfter(sessionID string, after uint64, limit int) ([]SessionEvent, error) {
	if j == nil || j.root == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	dir, err := j.sessionDir(sessionID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	result := make([]SessionEvent, 0, min(limit, len(names)))
	for _, name := range names {
		sequence, parseErr := strconv.ParseUint(strings.TrimSuffix(name, ".json"), 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("nome de evento inválido: %q", name)
		}
		if sequence <= after {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			return nil, readErr
		}
		var ev SessionEvent
		if err := json.Unmarshal(data, &ev); err != nil || ev.Version != EventProtocolVersion || ev.Sequence != sequence || ev.Update == nil {
			return nil, fmt.Errorf("evento de sessão corrompido: %s", name)
		}
		result = append(result, ev)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

// Subscribe registers an in-process listener for live events published to
// sessionID. Call the returned function to unsubscribe.
func (j *EventJournal) Subscribe(sessionID string, buffer int) (<-chan SessionEvent, func()) {
	if buffer <= 0 {
		buffer = 64
	}
	ch := make(chan SessionEvent, buffer)
	if j == nil {
		close(ch)
		return ch, func() {}
	}
	j.subMu.Lock()
	j.nextSubID++
	subID := j.nextSubID
	if j.subscribers[sessionID] == nil {
		j.subscribers[sessionID] = make(map[uint64]chan SessionEvent)
	}
	j.subscribers[sessionID][subID] = ch
	j.subMu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			j.subMu.Lock()
			if subs := j.subscribers[sessionID]; subs != nil {
				delete(subs, subID)
				if len(subs) == 0 {
					delete(j.subscribers, sessionID)
				}
			}
			j.subMu.Unlock()
		})
	}
	return ch, unsubscribe
}

func (j *EventJournal) notifySubscribers(sessionID string, ev SessionEvent) {
	j.subMu.Lock()
	subs := j.subscribers[sessionID]
	targets := make([]chan SessionEvent, 0, len(subs))
	for _, ch := range subs {
		targets = append(targets, ch)
	}
	j.subMu.Unlock()
	for _, ch := range targets {
		select {
		case ch <- ev:
		default:
		}
	}
}

func promptReceiptDigest(text string) string {
	digest := sha256.Sum256([]byte(text))
	return hex.EncodeToString(digest[:])
}

func (j *EventJournal) turnReceiptPath(sessionID, key string) (string, error) {
	if j == nil || j.root == "" {
		return "", errors.New("idempotência de sessão requer diário de eventos persistente")
	}
	trimmedKey := strings.TrimSpace(key)
	if trimmedKey == "" || len(trimmedKey) > 128 {
		return "", errors.New("turnId inválido")
	}
	sessionPath, err := j.sessionDir(sessionID)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(sessionPath, "turns")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(trimmedKey))
	return filepath.Join(dir, hex.EncodeToString(digest[:])+".json"), nil
}

// ClaimTurn writes an exclusive durable receipt before model inference or any
// tool call executes. If the same (sessionID, key, text) already completed,
// fresh is false and the completed receipt is returned so the caller can reply
// without re-running tools.
func (j *EventJournal) ClaimTurn(sessionID, key, text string) (TurnReceipt, bool, error) {
	path, err := j.turnReceiptPath(sessionID, key)
	if err != nil {
		return TurnReceipt{}, false, err
	}
	expected := promptReceiptDigest(text)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return TurnReceipt{}, false, readErr
		}
		var existing TurnReceipt
		if json.Unmarshal(data, &existing) != nil || existing.Version != EventProtocolVersion || existing.PromptSHA256 != expected {
			return TurnReceipt{}, false, errors.New("turnId já usado por outro prompt ou registro incompleto")
		}
		if existing.Status != "completed" {
			return TurnReceipt{}, false, errors.New("turnId já iniciado; consulte os eventos antes de tentar novamente")
		}
		return existing, false, nil
	}
	if err != nil {
		return TurnReceipt{}, false, err
	}
	receipt := TurnReceipt{
		Version:      EventProtocolVersion,
		PromptSHA256: expected,
		Status:       "started",
	}
	if err := json.NewEncoder(file).Encode(receipt); err != nil {
		file.Close()
		return TurnReceipt{}, false, err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return TurnReceipt{}, false, err
	}
	if err := file.Close(); err != nil {
		return TurnReceipt{}, false, err
	}
	if err := syncJournalDir(filepath.Dir(path)); err != nil {
		return TurnReceipt{}, false, err
	}
	return receipt, true, nil
}

// CompleteTurn atomically finalizes a previously claimed turn receipt.
func (j *EventJournal) CompleteTurn(sessionID, key, text, stopReason, content, errMsg string) error {
	path, err := j.turnReceiptPath(sessionID, key)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var current TurnReceipt
	if json.Unmarshal(data, &current) != nil || current.Version != EventProtocolVersion || current.PromptSHA256 != promptReceiptDigest(text) || current.Status != "started" {
		return fmt.Errorf("registro do turno mudou antes da conclusão")
	}
	lastSeq, _ := j.LatestSequence(sessionID)
	current.Status = "completed"
	current.StopReason = stopReason
	current.Content = content
	current.Error = errMsg
	current.LastSequence = lastSeq

	tmp, err := os.CreateTemp(filepath.Dir(path), ".turn-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := json.NewEncoder(tmp).Encode(current); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return syncJournalDir(filepath.Dir(path))
}

// BeginTurnContext registers an active cancellable turn for sessionID so any
// client or channel can explicitly cancel it via CancelSession.
func (j *EventJournal) BeginTurnContext(ctx context.Context, sessionID string) (context.Context, func()) {
	turnCtx, cancel := context.WithCancel(ctx)
	if j == nil || strings.TrimSpace(sessionID) == "" {
		return turnCtx, cancel
	}
	j.activeMu.Lock()
	j.nextToken++
	token := j.nextToken
	j.active[sessionID] = activeTurnEntry{token: token, cancel: cancel}
	j.activeMu.Unlock()

	release := func() {
		j.activeMu.Lock()
		if cur, ok := j.active[sessionID]; ok && cur.token == token {
			delete(j.active, sessionID)
		}
		j.activeMu.Unlock()
		cancel()
	}
	return turnCtx, release
}

// CancelSession explicitly cancels any active in-flight turn for sessionID.
func (j *EventJournal) CancelSession(sessionID string) bool {
	if j == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	j.activeMu.Lock()
	entry, ok := j.active[sessionID]
	j.activeMu.Unlock()
	if !ok || entry.cancel == nil {
		return false
	}
	entry.cancel()
	return true
}

func syncJournalDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
