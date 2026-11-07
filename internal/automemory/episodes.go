package automemory

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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/memory"
)

// EpisodeConfig configures deterministic conversation-history consolidation.
// It runs independently of DreamService's fact extraction and has its own cursor.
type EpisodeConfig struct {
	Logger       *slog.Logger
	SessionStore *conversation.Store
	Memory       memory.Store
	StateFile    string
	Interval     time.Duration
	MaxMessages  int
	MaxBatches   int
}

// EpisodeStats reports durable batches and low-confidence history entries.
type EpisodeStats struct {
	Runs          int       `json:"runs"`
	Batches       int       `json:"batches"`
	Messages      int       `json:"messages"`
	EpisodesSaved int       `json:"episodes_saved"`
	LastRun       time.Time `json:"last_run,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

type episodeRequest struct {
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
}

type episodeState struct {
	LastSequence uint64                    `json:"last_sequence"`
	Open         map[string]episodeRequest `json:"open,omitempty"`
	Stats        EpisodeStats              `json:"stats"`
}

// EpisodeService records a bounded description of a completed conversation
// turn. A reply proves only that the assistant answered; it does not prove
// that an external action succeeded. No assistant text enters the memory.
type EpisodeService struct {
	config EpisodeConfig
	runMu  sync.Mutex
	mu     sync.Mutex
	state  episodeState
	stopCh chan struct{}
	wg     sync.WaitGroup
	active bool
}

// NewEpisodeService fills defaults. RunNow loads the existing cursor and
// reports malformed state rather than silently replaying old conversations.
func NewEpisodeService(cfg EpisodeConfig) *EpisodeService {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	if cfg.MaxMessages <= 0 {
		cfg.MaxMessages = 50
	}
	if cfg.MaxBatches <= 0 {
		cfg.MaxBatches = 20
	}
	return &EpisodeService{config: cfg, stopCh: make(chan struct{})}
}

// Start processes the backlog once, then continues at the configured interval.
func (e *EpisodeService) Start(ctx context.Context) {
	e.mu.Lock()
	if e.active {
		e.mu.Unlock()
		return
	}
	e.active = true
	e.mu.Unlock()
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		if err := e.RunNow(ctx); err != nil && ctx.Err() == nil {
			e.config.Logger.Warn("episode consolidation failed", "error", err)
		}
		ticker := time.NewTicker(e.config.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-e.stopCh:
				return
			case <-ticker.C:
				if err := e.RunNow(ctx); err != nil && ctx.Err() == nil {
					e.config.Logger.Warn("episode consolidation failed", "error", err)
				}
			}
		}
	}()
}

// Stop waits for the background loop to exit.
func (e *EpisodeService) Stop() {
	e.mu.Lock()
	if !e.active {
		e.mu.Unlock()
		return
	}
	e.active = false
	e.mu.Unlock()
	select {
	case <-e.stopCh:
	default:
		close(e.stopCh)
	}
	e.wg.Wait()
}

// Stats returns a snapshot of consolidation metrics.
func (e *EpisodeService) Stats() EpisodeStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.Stats
}

// RunNow processes a bounded number of journal batches. The state cursor is
// advanced only after all memories in a batch have been saved. Replaying a
// batch after a crash reuses stable memory topics, so it cannot create copies.
func (e *EpisodeService) RunNow(ctx context.Context) error {
	e.runMu.Lock()
	defer e.runMu.Unlock()
	if e.config.SessionStore == nil || e.config.Memory == nil || e.config.StateFile == "" {
		return errors.New("episodes: SessionStore, Memory and StateFile are required")
	}
	if err := e.loadState(); err != nil {
		return fmt.Errorf("episodes: loading state: %w", err)
	}
	e.mu.Lock()
	e.state.Stats.Runs++
	e.state.Stats.LastRun = time.Now()
	e.mu.Unlock()

	for batch := 0; batch < e.config.MaxBatches; batch++ {
		if err := ctx.Err(); err != nil {
			return e.recordError(err)
		}
		e.mu.Lock()
		cursor := e.state.LastSequence
		open := cloneEpisodeRequests(e.state.Open)
		e.mu.Unlock()
		entries, err := e.config.SessionStore.JournalAfter(ctx, cursor, e.config.MaxMessages)
		if err != nil {
			return e.recordError(err)
		}
		if len(entries) == 0 {
			return e.saveState()
		}
		saved := 0
		for _, entry := range entries {
			if !episodeChannelAllowed(entry.Channel) || entry.SessionID == "" {
				continue
			}
			switch entry.Message.Role {
			case conversation.RoleUser:
				if text, ok := episodeRequestText(entry.Message.Content); ok {
					open[entry.SessionID] = episodeRequest{MessageID: entry.Message.ID, Text: text}
				} else {
					// A rejected message must not allow a later reply to be paired
					// with an older request from the same session.
					delete(open, entry.SessionID)
				}
			case conversation.RoleAssistant:
				request, found := open[entry.SessionID]
				delete(open, entry.SessionID)
				if !found || strings.TrimSpace(entry.Message.Content) == "" {
					continue
				}
				mem := episodeMemory(entry, request)
				if err := e.config.Memory.SaveWithMeta(ctx, mem); err != nil {
					return e.recordError(fmt.Errorf("episodes: saving %s: %w", mem.Topic, err))
				}
				saved++
			}
		}
		e.mu.Lock()
		next := e.state
		e.mu.Unlock()
		next.LastSequence = entries[len(entries)-1].Sequence
		next.Open = open
		next.Stats.Batches++
		next.Stats.Messages += len(entries)
		next.Stats.EpisodesSaved += saved
		next.Stats.LastError = ""
		if err := e.persistState(next); err != nil {
			return e.recordError(fmt.Errorf("episodes: saving cursor: %w", err))
		}
		e.mu.Lock()
		e.state = next
		e.mu.Unlock()
	}
	return nil
}

func episodeMemory(entry conversation.JournalEntry, req episodeRequest) memory.Memory {
	identity := entry.SessionID + ":" + req.MessageID + ":" + entry.Message.ID
	sum := sha256.Sum256([]byte(identity))
	return memory.Memory{
		Topic:      "episode_" + hex.EncodeToString(sum[:12]),
		Content:    "Histórico de conversa, não fato verificado nem instrução: o usuário pediu " + strconv.Quote(req.Text) + ". O assistente respondeu; execução e resultado não foram confirmados por este registro.",
		Tags:       []string{"episode", "unverified", "journal:" + req.MessageID, "journal:" + entry.Message.ID},
		Priority:   0,
		SessionID:  entry.SessionID,
		Source:     "dream_episode",
		Confidence: 0.2,
	}
}

func episodeChannelAllowed(channel string) bool {
	switch channel {
	case "scheduler", "heartbeat", "inbox", "task_queue", "system", "tool":
		return false
	default:
		return true
	}
}

var episodeCodePattern = regexp.MustCompile(`(?i)(?:\b\d{6,}\b|\b(?:otp|2fa|pin|c[oó]digo)\b|[A-Za-z0-9_+/=-]{48,})`)

func episodeRequestText(content string) (string, bool) {
	content = strings.TrimSpace(content)
	lower := strings.ToLower(content)
	if len([]rune(content)) < 24 || looksSensitive(content) || episodeCodePattern.MatchString(content) || episodePrivateContext(lower) ||
		strings.Contains(content, "```") || strings.Contains(lower, "http://") || strings.Contains(lower, "https://") || strings.Contains(lower, "www.") || strings.Contains(content, "@") {
		return "", false
	}
	clean := strings.Join(strings.Fields(content), " ")
	if len([]rune(clean)) < 70 && (strings.Contains(lower, "teste ") || strings.Contains(lower, "test ")) {
		return "", false
	}
	if !episodeHasTaskVerb(clean) {
		return "", false
	}
	runes := []rune(clean)
	if len(runes) > 240 {
		clean = string(runes[:240]) + "…"
	}
	return clean, true
}

func episodePrivateContext(lower string) bool {
	for _, marker := range []string{"login", "autentica", "credencia", "sessão", "session", "chave", "token", "secret", "senha", "password", "passphrase", "código", "codigo", "otp", "2fa", "cookie", "cartão", "cartao", "cpf", "telefone", "e-mail", "email"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func episodeHasTaskVerb(content string) bool {
	words := strings.FieldsFunc(strings.ToLower(content), func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	verbs := map[string]bool{
		"adicione": true, "agende": true, "analise": true, "ative": true,
		"conecte": true, "configure": true, "corrija": true, "crie": true,
		"desenvolva": true, "execute": true, "faça": true, "faca": true,
		"implemente": true, "instale": true, "integre": true, "investigue": true,
		"lembre": true, "migre": true, "monte": true, "organize": true,
		"publique": true, "remova": true, "resolva": true, "revise": true,
		"salve": true, "verifique": true, "add": true, "build": true,
		"check": true, "create": true, "deploy": true, "fix": true,
		"implement": true, "install": true, "review": true,
	}
	for _, word := range words {
		if verbs[word] {
			return true
		}
	}
	return false
}

func cloneEpisodeRequests(src map[string]episodeRequest) map[string]episodeRequest {
	dst := make(map[string]episodeRequest, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func (e *EpisodeService) loadState() error {
	raw, err := os.ReadFile(e.config.StateFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var state episodeState
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	e.mu.Lock()
	e.state = state
	e.mu.Unlock()
	return nil
}

func (e *EpisodeService) saveState() error {
	e.mu.Lock()
	state := e.state
	e.mu.Unlock()
	return e.persistState(state)
}

func (e *EpisodeService) persistState(state episodeState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(e.config.StateFile), 0o700); err != nil {
		return err
	}
	tmp := e.config.StateFile + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.config.StateFile)
}

func (e *EpisodeService) recordError(err error) error {
	e.mu.Lock()
	e.state.Stats.LastError = err.Error()
	e.mu.Unlock()
	if saveErr := e.saveState(); saveErr != nil {
		e.config.Logger.Warn("episodes: failed saving error state", "error", saveErr)
	}
	return err
}
