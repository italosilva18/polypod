// Package humanloop provides the durable inbox used to wake the full agent
// from background routines and remote relays without coupling them to the TUI.
package humanloop

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Direction identifies which side of the human loop owns a message.
type Direction string

const (
	DirectionInbound  Direction = "inbound"
	DirectionOutbound Direction = "outbound"
)

// Status is the durable lifecycle state of a message.
type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusResolved   Status = "resolved"
)

const MaxOutboundMediaBytes = 16 << 20

// MediaAttachment identifies one immutable, private blob persisted beside the
// outbox. Blob is an internal filename, never an arbitrary source path.
type MediaAttachment struct {
	Kind     string `json:"kind"`
	MIME     string `json:"mime"`
	Filename string `json:"filename,omitempty"`
	Blob     string `json:"blob"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

// Message is one durable inbox or outbox item.
type Message struct {
	ID            string            `json:"id"`
	ExternalID    string            `json:"external_id,omitempty"`
	Delivered     []string          `json:"delivered,omitempty"`
	Direction     Direction         `json:"direction"`
	DeliveryOwner string            `json:"delivery_owner,omitempty"`
	Status        Status            `json:"status"`
	Channel       string            `json:"channel"`
	UserID        string            `json:"user_id"`
	Text          string            `json:"text"`
	Media         *MediaAttachment  `json:"media,omitempty"`
	ReplyTo       string            `json:"reply_to,omitempty"`
	Response      string            `json:"response,omitempty"`
	ResponseReady bool              `json:"response_ready,omitempty"`
	Error         string            `json:"error,omitempty"`
	Attempts      int               `json:"attempts"`
	OfflineDefers int               `json:"offline_defers,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	ProcessingAt  *time.Time        `json:"processing_at,omitempty"`
	CompletedAt   *time.Time        `json:"completed_at,omitempty"`
	NextAttemptAt time.Time         `json:"next_attempt_at,omitempty"`
	ReplayHistory []ReplayRecord    `json:"replay_history,omitempty"`
	Resolution    *ResolutionRecord `json:"resolution,omitempty"`
}

// ReplayRecord preserves the previous failed run when an operator requeues it.
type ReplayRecord struct {
	RequeuedAt time.Time `json:"requeued_at"`
	FailedAt   time.Time `json:"failed_at"`
	Attempts   int       `json:"attempts"`
	Error      string    `json:"error,omitempty"`
}

// ReplayPreview contains only non-content metadata for operator dry runs.
type ReplayPreview struct {
	Channel      string
	Attempts     int
	FailedAt     time.Time
	PriorReplays int
}

// ResolutionRecord records why an operator closed a failed item without
// replaying its action or claiming it succeeded.
type ResolutionRecord struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Reason string    `json:"reason"`
}

// ResolutionPreview contains only metadata needed to review a failed item.
type ResolutionPreview struct {
	Direction    Direction
	Channel      string
	Attempts     int
	FailedAt     time.Time
	Delivered    []string
	HasMedia     bool
	HasResponse  bool
	PriorReplays int
}

// State is the persisted heartbeat metadata associated with an inbox.
type State struct {
	LastHeartbeat time.Time `json:"last_heartbeat,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

type diskState struct {
	Version    int               `json:"version"`
	Dedup      map[string]string `json:"dedup,omitempty"`
	DedupOrder []string          `json:"dedup_order,omitempty"`
	State      State             `json:"state"`
	Messages   []Message         `json:"messages"`
}

// Store owns the durable inbox journal. All mutations are persisted before
// the method returns. Finished history is bounded; active work is never
// discarded by history trimming.
type Store struct {
	mu         sync.Mutex
	path       string
	maxHistory int
	state      State
	messages   []Message
	dedup      map[string]string
	dedupOrder []string
	// syncDir is injectable only by package tests. A nil value uses File.Sync.
	syncDir func(*os.File) error
}

// NewStore loads or creates a durable inbox. Any item left in processing by a
// crashed process is recovered to pending so it can be attempted again.
func NewStore(path string, maxHistory int) (*Store, error) {
	return newStore(path, maxHistory, true)
}

// OpenStoreForMaintenance loads the existing inbox without recovering
// in-flight work. It is for an offline operator command and never writes on
// open, including when that command only previews a replay.
func OpenStoreForMaintenance(path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("humanloop: caixa de entrada indisponivel: %w", err)
	}
	return newStore(path, 500, false)
}

func newStore(path string, maxHistory int, recoverProcessing bool) (*Store, error) {
	if path == "" {
		return nil, errors.New("humanloop: caminho da caixa de entrada e obrigatorio")
	}
	if maxHistory <= 0 {
		maxHistory = 500
	}
	s := &Store{path: path, maxHistory: maxHistory, dedup: make(map[string]string)}
	if err := s.load(recoverProcessing); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load(recoverProcessing bool) error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("humanloop: lendo caixa de entrada: %w", err)
	}
	var disk diskState
	if err := json.Unmarshal(raw, &disk); err != nil {
		return fmt.Errorf("humanloop: decodificando caixa de entrada: %w", err)
	}
	s.state = disk.State
	s.messages = disk.Messages
	if disk.Dedup != nil {
		s.dedup = disk.Dedup
	}
	s.dedupOrder = disk.DedupOrder
	for _, msg := range s.messages {
		if msg.ExternalID != "" {
			s.dedup[dedupKey(msg.Direction, msg.Channel, msg.ExternalID)] = msg.ID
		}
	}
	s.reconcileDedupOrderLocked()
	s.trimDedupLocked()

	if !recoverProcessing {
		return nil
	}
	now := time.Now()
	recovered := false
	for i := range s.messages {
		if s.messages[i].Status != StatusProcessing {
			continue
		}
		s.messages[i].Status = StatusPending
		s.messages[i].ProcessingAt = nil
		s.messages[i].NextAttemptAt = now
		s.messages[i].UpdatedAt = now
		s.messages[i].Error = "execucao interrompida; recuperada apos reinicio"
		recovered = true
	}
	if recovered {
		return s.saveLocked()
	}
	return nil
}

// EnqueueInbound records a message that must wake the agent.
func (s *Store) EnqueueInbound(channel, userID, text string) (Message, error) {
	return s.enqueue(DirectionInbound, channel, userID, text, "", "")
}

// EnqueueOutbound records a response or notification that must be delivered.
func (s *Store) EnqueueOutbound(channel, userID, text, replyTo string) (Message, error) {
	return s.enqueue(DirectionOutbound, channel, userID, text, replyTo, "")
}

// EnqueueOutboundMedia persists the bytes before publishing the outbox item.
// Retry and process restart therefore use the same immutable payload and ID.
func (s *Store) EnqueueOutboundMedia(channel, userID, caption, replyTo string, media MediaAttachment, data []byte) (Message, error) {
	if len(data) == 0 || len(data) > MaxOutboundMediaBytes {
		return Message{}, errors.New("humanloop: tamanho de midia invalido")
	}
	if media.Kind == "" || media.MIME == "" || channel == "" || userID == "" {
		return Message{}, errors.New("humanloop: metadados de midia incompletos")
	}
	if media.Filename != "" && (filepath.Base(media.Filename) != media.Filename || strings.ContainsAny(media.Filename, "\\/\x00")) {
		return Message{}, errors.New("humanloop: nome de arquivo de midia invalido")
	}
	now := time.Now()
	msg := Message{ID: newID(), Direction: DirectionOutbound, Status: StatusPending,
		Channel: channel, UserID: userID, Text: caption, ReplyTo: replyTo,
		CreatedAt: now, UpdatedAt: now, NextAttemptAt: now}
	media.Blob = msg.ID + ".blob"
	media.Size = int64(len(data))
	sum := sha256.Sum256(data)
	media.SHA256 = hex.EncodeToString(sum[:])
	msg.Media = &media

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeMediaLocked(media.Blob, data); err != nil {
		return Message{}, err
	}
	s.messages = append(s.messages, msg)
	if err := s.saveLocked(); err != nil {
		s.messages = s.messages[:len(s.messages)-1]
		// A failed directory sync can follow a successful journal rename.
		// Preserve the blob for crash recovery in that ambiguous case.
		return Message{}, err
	}
	return cloneMediaMessage(msg), nil
}

// ReadOutboundMedia verifies the blob before it is handed to a remote driver.
func (s *Store) ReadOutboundMedia(msg Message) ([]byte, error) {
	if msg.Direction != DirectionOutbound || msg.Media == nil || !validMediaBlob(msg.Media.Blob) ||
		msg.Media.Size <= 0 || msg.Media.Size > MaxOutboundMediaBytes || len(msg.Media.SHA256) != 64 {
		return nil, errors.New("humanloop: referencia de midia invalida")
	}
	if info, err := os.Lstat(s.mediaDir()); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("humanloop: diretorio privado de midia indisponivel")
	}
	path := filepath.Join(s.mediaDir(), msg.Media.Blob)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != msg.Media.Size {
		return nil, errors.New("humanloop: arquivo de midia ausente ou alterado")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("humanloop: abrindo midia: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxOutboundMediaBytes+1))
	if err != nil || int64(len(data)) != msg.Media.Size {
		return nil, errors.New("humanloop: midia truncada ou excede limite")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != msg.Media.SHA256 {
		return nil, errors.New("humanloop: integridade da midia falhou")
	}
	return data, nil
}

func (s *Store) mediaDir() string { return s.path + ".media" }

func validMediaBlob(name string) bool {
	if !strings.HasPrefix(name, "msg_") || !strings.HasSuffix(name, ".blob") || filepath.Base(name) != name {
		return false
	}
	core := strings.TrimSuffix(strings.TrimPrefix(name, "msg_"), ".blob")
	if core == "" {
		return false
	}
	for _, r := range core {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func (s *Store) writeMediaLocked(name string, data []byte) error {
	dir := s.mediaDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("humanloop: criando diretorio de midia: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("humanloop: diretorio de midia inseguro")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("humanloop: protegendo diretorio de midia: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("humanloop: criando midia privada: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return fmt.Errorf("humanloop: gravando midia: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return fmt.Errorf("humanloop: sincronizando midia: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("humanloop: fechando midia: %w", err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (s *Store) enqueue(direction Direction, channel, userID, text, replyTo, externalID string) (Message, error) {
	if direction != DirectionInbound && direction != DirectionOutbound {
		return Message{}, fmt.Errorf("humanloop: direcao invalida: %s", direction)
	}
	if text == "" {
		return Message{}, errors.New("humanloop: mensagem vazia")
	}
	now := time.Now()
	msg := Message{
		ID:            newID(),
		ExternalID:    externalID,
		Direction:     direction,
		Status:        StatusPending,
		Channel:       channel,
		UserID:        userID,
		Text:          text,
		ReplyTo:       replyTo,
		CreatedAt:     now,
		UpdatedAt:     now,
		NextAttemptAt: now,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := dedupKey(direction, channel, externalID)
	if externalID != "" {
		if id, exists := s.dedup[key]; exists {
			for _, existing := range s.messages {
				if existing.ID == id {
					return existing, nil
				}
			}
			return Message{ID: id, Direction: direction, Status: StatusCompleted, Channel: channel, ExternalID: externalID}, nil
		}
		s.rememberDedupLocked(key, msg.ID)
	}

	s.messages = append(s.messages, msg)
	if err := s.saveLocked(); err != nil {
		s.messages = s.messages[:len(s.messages)-1]
		if externalID != "" {
			s.forgetDedupLocked(key)
		}
		return Message{}, err
	}
	return msg, nil
}

// ClaimNextDue atomically moves the oldest due pending item to processing.
// The attempts counter is incremented before returning it to the worker.
func (s *Store) ClaimNextDue(now time.Time) (Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i := range s.messages {
		msg := &s.messages[i]
		if msg.Status != StatusPending || msg.DeliveryOwner != "" || msg.NextAttemptAt.After(now) {
			continue
		}
		if idx == -1 || msg.CreatedAt.Before(s.messages[idx].CreatedAt) {
			idx = i
		}
	}
	if idx == -1 {
		return Message{}, false, nil
	}

	msg := &s.messages[idx]
	previous := *msg
	msg.Status = StatusProcessing
	msg.Attempts++
	msg.UpdatedAt = now
	msg.ProcessingAt = timePtr(now)
	if err := s.saveLocked(); err != nil {
		*msg = previous
		return Message{}, false, err
	}
	return cloneMediaMessage(*msg), true, nil
}

// Complete marks an item successful and records any generated response.
func (s *Store) Complete(id, response string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, err := s.findLocked(id)
	if err != nil {
		return err
	}
	msg.Status = StatusCompleted
	msg.Response = response
	msg.Error = ""
	msg.UpdatedAt = now
	msg.CompletedAt = timePtr(now)
	msg.ProcessingAt = nil
	s.trimLocked()
	return s.saveLocked()
}

// Retry records a failed attempt. The item is returned to pending until
// maxAttempts is reached; at that point it becomes terminally failed.
func (s *Store) Retry(id string, cause error, next time.Time, maxAttempts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, err := s.findLocked(id)
	if err != nil {
		return err
	}
	msg.Error = cause.Error()
	msg.OfflineDefers = 0
	msg.UpdatedAt = time.Now()
	msg.ProcessingAt = nil
	if maxAttempts > 0 && msg.Attempts >= maxAttempts {
		msg.Status = StatusFailed
		msg.CompletedAt = timePtr(msg.UpdatedAt)
		s.trimLocked()
	} else {
		msg.Status = StatusPending
		msg.NextAttemptAt = next
	}
	return s.saveLocked()
}

// DeferUnavailable returns a turn waiting on a disconnected remote relay to
// pending without consuming the attempt ClaimNextDue just recorded. A bounded
// counter drives reconnect backoff without making the item terminal.
func (s *Store) DeferUnavailable(id string, cause error, next time.Time) error {
	if cause == nil || next.IsZero() {
		return errors.New("humanloop: causa e proxima tentativa obrigatorias")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, err := s.findLocked(id)
	if err != nil {
		return err
	}
	if (msg.Direction != DirectionInbound && msg.Direction != DirectionOutbound) || msg.Status != StatusProcessing {
		return errors.New("humanloop: somente mensagem em processamento pode aguardar relay")
	}
	previous := *msg
	msg.Status = StatusPending
	if msg.Attempts > 0 {
		msg.Attempts--
	}
	if msg.OfflineDefers < 6 {
		msg.OfflineDefers++
	}
	msg.Error = cause.Error()
	msg.UpdatedAt = time.Now()
	msg.ProcessingAt = nil
	msg.NextAttemptAt = next
	if err := s.saveLocked(); err != nil {
		*msg = previous
		return err
	}
	return nil
}

// ExpediteDeferredOutbounds makes disconnected deliveries due immediately
// after a relay authenticates, without touching unrelated retries.
func (s *Store) ExpediteDeferredOutbounds(channel string, now time.Time) error {
	if channel == "" || now.IsZero() {
		return errors.New("humanloop: canal e horario obrigatorios")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	type changed struct {
		index int
		next  time.Time
	}
	var changes []changed
	for i := range s.messages {
		msg := &s.messages[i]
		if msg.Channel == channel && msg.Direction == DirectionOutbound && msg.Status == StatusPending &&
			msg.OfflineDefers > 0 && msg.NextAttemptAt.After(now) {
			changes = append(changes, changed{index: i, next: msg.NextAttemptAt})
			msg.NextAttemptAt = now
		}
	}
	if len(changes) == 0 {
		return nil
	}
	if err := s.saveLocked(); err != nil {
		for _, change := range changes {
			s.messages[change.index].NextAttemptAt = change.next
		}
		return err
	}
	return nil
}

// RequeueFailedInbound retries one terminal inbound item without changing its
// stable identity or transport deduplication key. Call this only while the
// owning service is stopped; Store locking does not coordinate processes.
// A prior outbox entry makes a replay ambiguous, so it is refused.
func (s *Store) RequeueFailedInbound(id string, now time.Time) error {
	if id == "" || now.IsZero() {
		return errors.New("humanloop: id e horario obrigatorios para reprocessar")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, err := s.replayCandidateLocked(id)
	if err != nil {
		return err
	}
	previous := *msg
	failedAt := msg.UpdatedAt
	if msg.CompletedAt != nil {
		failedAt = *msg.CompletedAt
	}
	msg.ReplayHistory = append(append([]ReplayRecord(nil), msg.ReplayHistory...), ReplayRecord{
		RequeuedAt: now, FailedAt: failedAt, Attempts: msg.Attempts, Error: msg.Error,
	})
	msg.Status = StatusPending
	msg.Attempts = 0
	msg.Error = ""
	msg.UpdatedAt = now
	msg.NextAttemptAt = now
	msg.ProcessingAt = nil
	msg.CompletedAt = nil
	if err := s.saveLocked(); err != nil {
		*msg = previous
		return err
	}
	return nil
}

// PreviewFailedInbound validates a replay without changing the store or
// returning message text, recipient IDs or transport IDs.
func (s *Store) PreviewFailedInbound(id string) (ReplayPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, err := s.replayCandidateLocked(id)
	if err != nil {
		return ReplayPreview{}, err
	}
	failedAt := msg.UpdatedAt
	if msg.CompletedAt != nil {
		failedAt = *msg.CompletedAt
	}
	return ReplayPreview{Channel: msg.Channel, Attempts: msg.Attempts,
		FailedAt: failedAt, PriorReplays: len(msg.ReplayHistory)}, nil
}

// PreviewFailedResolution reports enough non-content metadata to decide
// whether closing a terminal failure is appropriate. It does not mutate state.
func (s *Store) PreviewFailedResolution(id string) (ResolutionPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, err := s.resolutionCandidateLocked(id)
	if err != nil {
		return ResolutionPreview{}, err
	}
	failedAt := msg.UpdatedAt
	if msg.CompletedAt != nil {
		failedAt = *msg.CompletedAt
	}
	return ResolutionPreview{Direction: msg.Direction, Channel: msg.Channel,
		Attempts: msg.Attempts, FailedAt: failedAt,
		Delivered: append([]string(nil), msg.Delivered...), HasMedia: msg.Media != nil,
		HasResponse: msg.Response != "" || msg.ResponseReady, PriorReplays: len(msg.ReplayHistory)}, nil
}

// ResolveFailed closes a reviewed terminal failure. It preserves the original
// error, delivery markers, identity and attempts. No handler or remote sender
// is called, and the status explicitly distinguishes it from success.
func (s *Store) ResolveFailed(id, actor, reason string, now time.Time) error {
	actor, reason = strings.TrimSpace(actor), strings.TrimSpace(reason)
	if now.IsZero() || actor == "" || len(actor) > 80 || reason == "" || len(reason) > 500 ||
		strings.ContainsAny(actor, "\r\n\x00") || strings.ContainsAny(reason, "\x00") {
		return errors.New("humanloop: ator, motivo e horario validos sao obrigatorios")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, err := s.resolutionCandidateLocked(id)
	if err != nil {
		return err
	}
	previous := *msg
	msg.Status = StatusResolved
	msg.Resolution = &ResolutionRecord{At: now, Actor: actor, Reason: reason}
	msg.UpdatedAt = now
	msg.NextAttemptAt = time.Time{}
	if err := s.saveLocked(); err != nil {
		*msg = previous
		return err
	}
	return nil
}

func (s *Store) resolutionCandidateLocked(id string) (*Message, error) {
	if id == "" {
		return nil, errors.New("humanloop: id obrigatorio para resolver falha")
	}
	msg, err := s.findLocked(id)
	if err != nil {
		return nil, err
	}
	if msg.Status != StatusFailed || (msg.Direction != DirectionInbound && msg.Direction != DirectionOutbound) {
		return nil, errors.New("humanloop: apenas item com falha terminal pode ser resolvido")
	}
	if msg.Direction == DirectionInbound {
		if msg.Response != "" || msg.ResponseReady {
			return nil, errors.New("humanloop: entrada possui resposta pendente; resolucao recusada")
		}
		for _, existing := range s.messages {
			if existing.Direction == DirectionOutbound && existing.ReplyTo == id {
				return nil, errors.New("humanloop: resposta ja enfileirada; resolucao recusada")
			}
		}
	}
	return msg, nil
}

func (s *Store) replayCandidateLocked(id string) (*Message, error) {
	if id == "" {
		return nil, errors.New("humanloop: id obrigatorio para reprocessar")
	}
	msg, err := s.findLocked(id)
	if err != nil {
		return nil, err
	}
	if msg.Direction != DirectionInbound || msg.Status != StatusFailed {
		return nil, errors.New("humanloop: apenas entrada com falha pode ser reprocessada")
	}
	for _, existing := range s.messages {
		if existing.Direction == DirectionOutbound && existing.ReplyTo == id {
			return nil, errors.New("humanloop: resposta ja enfileirada; reprocessamento recusado")
		}
	}
	if msg.Response != "" || msg.ResponseReady {
		return nil, errors.New("humanloop: resposta existente; reprocessamento recusado")
	}
	return msg, nil
}

// RecordHeartbeat persists liveness and the latest cycle error.
func (s *Store) RecordHeartbeat(at time.Time, cycleErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.LastHeartbeat = at
	s.state.LastError = ""
	if cycleErr != nil {
		s.state.LastError = cycleErr.Error()
	}
	return s.saveLocked()
}

// Snapshot returns copies suitable for status and audit views.
func (s *Store) Snapshot() (State, []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := append([]Message(nil), s.messages...)
	for i := range messages {
		messages[i] = cloneMediaMessage(messages[i])
	}
	sort.SliceStable(messages, func(i, j int) bool {
		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
	return s.state, messages
}

// GetByID returns a detached copy of one inbox or outbox item. It does not
// mutate state or recover interrupted work, so status readers can call it
// concurrently with the processor.
func (s *Store) GetByID(id string) (Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, msg := range s.messages {
		if msg.ID == id {
			return cloneMediaMessage(msg), true
		}
	}
	return Message{}, false
}

func cloneMediaMessage(msg Message) Message {
	if msg.Media != nil {
		media := *msg.Media
		msg.Media = &media
	}
	if msg.Resolution != nil {
		resolution := *msg.Resolution
		msg.Resolution = &resolution
	}
	msg.Delivered = append([]string(nil), msg.Delivered...)
	msg.ReplayHistory = append([]ReplayRecord(nil), msg.ReplayHistory...)
	return msg
}

func (s *Store) findLocked(id string) (*Message, error) {
	for i := range s.messages {
		if s.messages[i].ID == id {
			return &s.messages[i], nil
		}
	}
	return nil, fmt.Errorf("humanloop: mensagem %s nao encontrada", id)
}

func (s *Store) trimLocked() {
	terminal := 0
	for _, msg := range s.messages {
		if msg.Status == StatusCompleted || msg.Status == StatusFailed || msg.Status == StatusResolved {
			terminal++
		}
	}
	remove := terminal - s.maxHistory
	if remove <= 0 {
		return
	}
	kept := make([]Message, 0, len(s.messages)-remove)
	for _, msg := range s.messages {
		isTerminal := msg.Status == StatusCompleted || msg.Status == StatusFailed || msg.Status == StatusResolved
		if isTerminal && remove > 0 {
			remove--
			continue
		}
		kept = append(kept, msg)
	}
	s.messages = kept
	s.trimDedupLocked()
}

func (s *Store) rememberDedupLocked(key, id string) {
	if _, exists := s.dedup[key]; !exists {
		s.dedupOrder = append(s.dedupOrder, key)
	}
	s.dedup[key] = id
}

func (s *Store) forgetDedupLocked(key string) {
	delete(s.dedup, key)
	for i := len(s.dedupOrder) - 1; i >= 0; i-- {
		if s.dedupOrder[i] == key {
			s.dedupOrder = append(s.dedupOrder[:i], s.dedupOrder[i+1:]...)
			return
		}
	}
}

// Older inbox files have a dedup map without insertion order. Keep their
// tombstones in deterministic order, followed by keys written by new versions.
func (s *Store) reconcileDedupOrderLocked() {
	seen := make(map[string]bool, len(s.dedup))
	order := make([]string, 0, len(s.dedup))
	for _, key := range s.dedupOrder {
		if _, exists := s.dedup[key]; exists && !seen[key] {
			order = append(order, key)
			seen[key] = true
		}
	}
	missing := make([]string, 0)
	for key := range s.dedup {
		if !seen[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	s.dedupOrder = append(missing, order...)
}

// Retain every key still backed by a visible message. Discard the oldest
// tombstones until the total reaches 10x maxHistory (at least 128), unless
// visible messages alone exceed that cap. This preserves duplicate protection
// across normal history trimming while bounding completed tombstones.
func (s *Store) trimDedupLocked() {
	cap := s.maxHistory * 10
	if cap < 128 {
		cap = 128
	}
	if len(s.dedup) <= cap {
		return
	}
	visible := make(map[string]bool, len(s.messages))
	for _, msg := range s.messages {
		visible[msg.ID] = true
	}
	kept := make([]string, 0, len(s.dedupOrder))
	for _, key := range s.dedupOrder {
		if len(s.dedup) > cap && !visible[s.dedup[key]] {
			delete(s.dedup, key)
			continue
		}
		kept = append(kept, key)
	}
	s.dedupOrder = kept
}

func (s *Store) saveLocked() error {
	disk := diskState{Version: 2, State: s.state, Messages: s.messages, Dedup: s.dedup, DedupOrder: s.dedupOrder}
	raw, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return fmt.Errorf("humanloop: codificando caixa de entrada: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("humanloop: criando diretorio: %w", err)
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("humanloop: abrindo caixa temporaria: %w", err)
	}
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("humanloop: protegendo caixa temporaria: %w", err)
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return fmt.Errorf("humanloop: gravando caixa temporaria: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("humanloop: sincronizando caixa temporaria: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("humanloop: fechando caixa temporaria: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("humanloop: publicando caixa de entrada: %w", err)
	}
	// Rename is the publication point. After it succeeds, callers must keep
	// their in-memory mutation even if directory sync fails; rolling it back
	// would let a subsequent save erase work that is already in the journal.
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		slog.Warn("humanloop: caixa publicada; sincronizacao do diretorio indisponivel", "error", err)
		return nil
	}
	syncDir := s.syncDir
	if syncDir == nil {
		syncDir = (*os.File).Sync
	}
	if err := syncDir(dir); err != nil {
		_ = dir.Close()
		// Keep old media blobs: a power loss might restore the previous journal.
		slog.Warn("humanloop: caixa publicada; sincronizacao do diretorio falhou", "error", err)
		return nil
	}
	if err := dir.Close(); err != nil {
		slog.Warn("humanloop: caixa publicada; fechamento do diretorio falhou", "error", err)
	}
	s.pruneMediaLocked()
	return nil
}

// pruneMediaLocked only removes managed blobs no longer referenced by history.
// Cleanup is best effort after a successful journal commit; delivery state is
// never rolled back because a stale file could not be deleted.
func (s *Store) pruneMediaLocked() {
	if info, err := os.Lstat(s.mediaDir()); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	entries, err := os.ReadDir(s.mediaDir())
	if err != nil {
		return
	}
	referenced := make(map[string]bool)
	for _, msg := range s.messages {
		if msg.Media != nil && validMediaBlob(msg.Media.Blob) {
			referenced[msg.Media.Blob] = true
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() && validMediaBlob(entry.Name()) && !referenced[entry.Name()] {
			_ = os.Remove(filepath.Join(s.mediaDir(), entry.Name()))
		}
	}
}

func newID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return "msg_" + hex.EncodeToString(buf)
	}
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}

func timePtr(t time.Time) *time.Time { return &t }

// EnqueueInboundOnce deduplicates replayed transport events before waking the agent.
func (s *Store) EnqueueInboundOnce(channel, userID, text, externalID string) (Message, error) {
	return s.enqueue(DirectionInbound, channel, userID, text, "", externalID)
}

// DeliveredTo reports a durable acknowledgement for one independent destination.
func (s *Store) DeliveredTo(id, destination string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, err := s.findLocked(id)
	if err != nil {
		return false
	}
	for _, d := range msg.Delivered {
		if d == destination {
			return true
		}
	}
	return false
}

func (s *Store) MarkDelivered(id, destination string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, err := s.findLocked(id)
	if err != nil {
		return err
	}
	for _, d := range msg.Delivered {
		if d == destination {
			return nil
		}
	}
	msg.Delivered = append(msg.Delivered, destination)
	if err := s.saveLocked(); err != nil {
		msg.Delivered = msg.Delivered[:len(msg.Delivered)-1]
		return err
	}
	return nil
}

// CompleteInbound commits the generated response and its outbox item together.
// A crash can never leave a completed inbound without its delivery queued.
func (s *Store) CompleteInbound(id, response string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, err := s.findLocked(id)
	if err != nil {
		return err
	}
	if msg.Status == StatusCompleted {
		return nil
	}
	previous := append([]Message(nil), s.messages...)
	channel, userID := msg.Channel, msg.UserID
	msg.Status, msg.Response, msg.Error = StatusCompleted, response, ""
	msg.UpdatedAt, msg.CompletedAt, msg.ProcessingAt = now, timePtr(now), nil
	if response != "" {
		s.messages = append(s.messages, Message{ID: newID(), Direction: DirectionOutbound, Status: StatusPending,
			Channel: channel, UserID: userID, Text: response, ReplyTo: id, CreatedAt: now, UpdatedAt: now, NextAttemptAt: now})
	}
	s.trimLocked()
	if err := s.saveLocked(); err != nil {
		s.messages = previous
		// Keep the computed response in memory until storage recovers. The
		// next claim persists ResponseReady before retrying only the commit.
		for i := range s.messages {
			if s.messages[i].ID == id {
				pending := &s.messages[i]
				pending.Status, pending.Response, pending.ResponseReady = StatusPending, response, true
				pending.ProcessingAt = nil
				pending.NextAttemptAt = now.Add(time.Second)
				pending.Error = "resposta pronta; aguardando persistencia"
				break
			}
		}
		return err
	}
	return nil
}

// EnqueueOutboundOnce deduplicates retried durable producer callbacks.
func (s *Store) EnqueueOutboundOnce(channel, userID, text, replyTo, externalID string) (Message, error) {
	return s.enqueue(DirectionOutbound, channel, userID, text, replyTo, externalID)
}

func dedupKey(direction Direction, channel, externalID string) string {
	return string(direction) + "\x00" + channel + "\x00" + externalID
}
