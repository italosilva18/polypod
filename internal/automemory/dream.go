package automemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/memory"
)

// DreamConfig configures the background memory consolidation service.
type DreamConfig struct {
	Logger       *slog.Logger
	Memory       memory.Store
	SessionStore *conversation.Store

	// ExtractFn is the AI callback used to extract memories from a transcript.
	// Injected so the service does not depend on the ai package directly.
	ExtractFn func(ctx context.Context, transcript string) ([]ExtractedMemory, error)

	// IdleThreshold is how long the user must be inactive before a run is
	// considered. Defaults to 5 minutes when zero.
	IdleThreshold time.Duration

	// Interval is the minimum time between successive runs. Defaults to 10
	// minutes when zero.
	Interval time.Duration

	// MaxMessages caps how many recent messages a single run will process.
	// Defaults to 50 when zero.
	MaxMessages int

	// StateFile persists the timestamp through which conversations were
	// consolidated, preventing repeated model calls after restarts.
	StateFile string
}

// ExtractedMemory is the structured output the AI callback returns for each
// memory worth persisting.
type ExtractedMemory struct {
	FactKey    string `json:"fact_key,omitempty"` // stable subject/property key, never a value
	Type       string // "fact", "preference", "skill", "context"
	Content    string
	Tags       []string
	Importance int // 1-5
}

// DreamStats exposes runtime metrics for observability.
type DreamStats struct {
	Runs                int       `json:"runs"`
	SuccessfulRuns      int       `json:"successful_runs"`
	FailedRuns          int       `json:"failed_runs"`
	MemoriesExtracted   int       `json:"memories_extracted"`
	MemoriesAdded       int       `json:"memories_added"`
	MessagesProcessed   int       `json:"messages_processed"`
	NoUserBatches       int       `json:"no_user_batches"`
	EmptyExtractions    int       `json:"empty_extractions"`
	LastBatchMessages   int       `json:"last_batch_messages"`
	LastBatchExtracted  int       `json:"last_batch_extracted"`
	LastBatchSaved      int       `json:"last_batch_saved"`
	LastError           string    `json:"last_error,omitempty"`
	LastRunTime         time.Time `json:"last_run_time,omitempty"`
	LastSuccessfulRunAt time.Time `json:"last_successful_run_at,omitempty"`
}

// DreamService periodically (or on idle) processes recent conversations to
// extract long-term memories.
type DreamService struct {
	config DreamConfig

	runMu         sync.Mutex
	lastSequence  uint64
	mu            sync.Mutex
	lastRun       time.Time
	lastActivity  time.Time
	lastProcessed time.Time
	running       bool
	stats         DreamStats

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewDreamService creates a DreamService with sensible defaults filled in.
func NewDreamService(cfg DreamConfig) *DreamService {
	if cfg.IdleThreshold <= 0 {
		cfg.IdleThreshold = 5 * time.Minute
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Minute
	}
	if cfg.MaxMessages <= 0 {
		cfg.MaxMessages = 50
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	d := &DreamService{
		config:       cfg,
		lastActivity: time.Now(),
		stopCh:       make(chan struct{}),
	}
	d.loadState()
	return d
}

// Start begins the background service. Returns immediately. The service
// terminates when the provided context is cancelled or Stop is called.
func (d *DreamService) Start(ctx context.Context) {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return
	}
	d.running = true
	d.mu.Unlock()

	d.wg.Add(1)
	go d.loop(ctx)
}

// Stop gracefully stops the service and waits for the goroutine to exit.
func (d *DreamService) Stop() {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return
	}
	d.running = false
	d.mu.Unlock()

	select {
	case <-d.stopCh:
		// already closed
	default:
		close(d.stopCh)
	}
	d.wg.Wait()
}

// MarkActivity records that the user is active (resets the idle timer).
func (d *DreamService) MarkActivity() {
	d.mu.Lock()
	d.lastActivity = time.Now()
	d.mu.Unlock()
}

// RunNow forces an immediate consolidation run.
func (d *DreamService) RunNow(ctx context.Context) error {
	return d.extractMemories(ctx)
}

// Stats returns a snapshot of current service stats.
func (d *DreamService) Stats() DreamStats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stats
}

// loop runs the periodic consolidation loop until Stop or context cancel.
func (d *DreamService) loop(ctx context.Context) {
	defer d.wg.Done()

	logger := d.config.Logger
	logger.Info("dream service started",
		"idle_threshold", d.config.IdleThreshold,
		"interval", d.config.Interval,
	)

	for {
		select {
		case <-ctx.Done():
			logger.Info("dream service stopped (context cancelled)")
			return
		case <-d.stopCh:
			logger.Info("dream service stopped")
			return
		case <-time.After(d.config.IdleThreshold):
		}

		// Check idle and interval gates.
		d.mu.Lock()
		idleFor := time.Since(d.lastActivity)
		sinceLastRun := time.Since(d.lastRun)
		firstRun := d.lastRun.IsZero()
		d.mu.Unlock()

		if idleFor < d.config.IdleThreshold && !firstRun && sinceLastRun < d.config.Interval {
			// User is still active, keep waiting.
			continue
		}
		if !firstRun && sinceLastRun < d.config.Interval {
			// Too soon since last run.
			continue
		}

		if err := d.extractMemories(ctx); err != nil {
			logger.Warn("dream extraction failed", "error", err)
		}

		// Sleep at least Interval before checking again.
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case <-time.After(d.config.Interval):
		}
	}
}

// extractMemories pulls recent messages from the session store, asks the AI
// callback to distill memories, and persists them to the memory store.
func (d *DreamService) extractMemories(ctx context.Context) error {
	d.runMu.Lock()
	defer d.runMu.Unlock()
	logger := d.config.Logger

	d.mu.Lock()
	d.lastRun = time.Now()
	d.stats.LastRunTime = d.lastRun
	d.stats.Runs++
	d.mu.Unlock()
	if err := d.saveState(); err != nil {
		d.recordError(err)
		return fmt.Errorf("persisting dream run: %w", err)
	}

	if d.config.ExtractFn == nil {
		err := fmt.Errorf("dream: ExtractFn not configured")
		d.recordError(err)
		return err
	}
	if d.config.SessionStore == nil {
		err := fmt.Errorf("dream: SessionStore not configured")
		d.recordError(err)
		return err
	}

	transcript, msgCount, sequence, provenance, err := d.collectTranscript(ctx)
	if err != nil {
		d.recordError(err)
		return err
	}
	if sequence == 0 {
		logger.Debug("dream: no messages to process")
		return d.finishSuccess(0, 0, 0, 0)
	}

	logger.Info("dream: extracting memories", "messages", msgCount)

	memories, cached, err := d.loadPending(sequence)
	if err != nil {
		d.recordError(err)
		return err
	}
	if !cached && msgCount > 0 {
		memories, err = d.config.ExtractFn(ctx, transcript)
	}
	if err != nil {
		d.recordError(err)
		return fmt.Errorf("extract callback: %w", err)
	}

	if !cached {
		clean := make([]ExtractedMemory, 0, len(memories))
		for _, m := range memories {
			if strings.TrimSpace(m.Content) != "" && !looksSensitive(m.Content) && !looksSensitive(strings.Join(m.Tags, " ")) {
				clean = append(clean, m)
			}
		}
		memories = clean
		if err := d.savePending(sequence, memories); err != nil {
			d.recordError(err)
			return err
		}
	}

	added := 0
	saveFailed := false
	for _, m := range memories {
		if strings.TrimSpace(m.Content) == "" || looksSensitive(m.Content) || looksSensitive(strings.Join(m.Tags, " ")) {
			continue
		}
		if d.config.Memory == nil {
			saveFailed = true
			continue
		}
		tags := append(append([]string(nil), m.Tags...), provenance...)
		if m.FactKey != "" {
			tags = append(tags, "fact_key:"+m.FactKey)
		}
		mem := memory.Memory{
			Topic:      d.topicFor(m),
			Content:    m.Content,
			Tags:       tags,
			Priority:   normalisePriority(m.Importance),
			Source:     "dream",
			Confidence: float64(m.Importance) / 5.0,
		}
		if err := d.config.Memory.SaveWithMeta(ctx, mem); err != nil {
			logger.Warn("dream: failed saving memory", "error", err)
			saveFailed = true
			continue
		}
		added++
	}
	if saveFailed {
		d.mu.Lock()
		d.stats.LastBatchMessages = msgCount
		d.stats.LastBatchExtracted = len(memories)
		d.stats.LastBatchSaved = added
		d.mu.Unlock()
		err := fmt.Errorf("dream: uma ou mais memorias nao puderam ser salvas; lote sera reprocessado")
		d.recordError(err)
		return err
	}
	if err := d.finishSuccess(sequence, msgCount, len(memories), added); err != nil {
		return fmt.Errorf("persisting dream cursor: %w", err)
	}

	logger.Info("dream: extraction complete",
		"messages_processed", msgCount,
		"memories_added", added,
	)
	return nil
}

// collectTranscript gathers recent messages from all sessions, capped at
// MaxMessages, and renders them as a single transcript string.
func (d *DreamService) collectTranscript(ctx context.Context) (string, int, uint64, []string, error) {
	d.mu.Lock()
	cursor := d.lastSequence
	d.mu.Unlock()
	entries, err := d.config.SessionStore.JournalAfter(ctx, cursor, d.config.MaxMessages)
	if err != nil {
		return "", 0, 0, nil, err
	}
	// Keep an unacknowledged batch boundary stable if new turns arrived after
	// an interrupted write. Its cached extraction must remain replayable.
	if d.config.StateFile != "" {
		raw, readErr := os.ReadFile(d.config.StateFile + ".pending")
		if readErr != nil && !os.IsNotExist(readErr) {
			return "", 0, 0, nil, readErr
		}
		if readErr == nil {
			var pending pendingDream
			if err := json.Unmarshal(raw, &pending); err != nil {
				return "", 0, 0, nil, err
			}
			if pending.Cursor == cursor {
				for i, e := range entries {
					if e.Sequence > pending.Through {
						entries = entries[:i]
						break
					}
				}
			}
		}
	}
	var sb strings.Builder
	var sequence uint64
	var provenance []string
	count := 0
	for _, e := range entries {
		sequence = e.Sequence
		if e.Channel == "scheduler" || e.Channel == "heartbeat" || e.Channel == "inbox" {
			continue
		}
		// Only explicit user statements are evidence. Secrets never leave the
		// private journal through automatic extraction, including assistant echoes.
		if e.Message.Role != conversation.RoleUser || looksSensitive(e.Message.Content) {
			continue
		}
		fmt.Fprintf(&sb, "[%s message=%s] user: %s\n", e.SessionID, e.Message.ID, strings.TrimSpace(e.Message.Content))
		provenance = append(provenance, "journal:"+e.Message.ID)
		count++
	}
	return sb.String(), count, sequence, provenance, nil
}

func (d *DreamService) recordError(err error) {
	d.mu.Lock()
	d.stats.LastError = err.Error()
	d.stats.FailedRuns++
	d.mu.Unlock()
	if persistErr := d.saveState(); persistErr != nil {
		d.config.Logger.Warn("dream: failed persisting error metrics", "error", persistErr)
	}
}

func (d *DreamService) finishSuccess(sequence uint64, messages, extracted, added int) error {
	d.mu.Lock()
	previousSequence, previousProcessed, previousStats := d.lastSequence, d.lastProcessed, d.stats
	if sequence != 0 {
		d.lastSequence = sequence
	}
	d.lastProcessed = time.Now()
	d.stats.SuccessfulRuns++
	d.stats.MessagesProcessed += messages
	d.stats.MemoriesExtracted += extracted
	d.stats.MemoriesAdded += added
	d.stats.LastBatchMessages = messages
	d.stats.LastBatchExtracted = extracted
	d.stats.LastBatchSaved = added
	d.stats.LastSuccessfulRunAt = d.lastProcessed
	d.stats.LastError = ""
	if messages == 0 {
		d.stats.NoUserBatches++
	} else if extracted == 0 {
		d.stats.EmptyExtractions++
	}
	d.mu.Unlock()
	if err := d.saveState(); err != nil {
		d.mu.Lock()
		d.lastSequence, d.lastProcessed, d.stats = previousSequence, previousProcessed, previousStats
		d.mu.Unlock()
		d.recordError(err)
		return err
	}
	return nil
}

func (d *DreamService) topicFor(m ExtractedMemory) string {
	t := strings.TrimSpace(m.Type)
	if t == "" {
		t = "context"
	}
	identity := strings.ToLower(strings.TrimSpace(m.Content))
	if m.FactKey != "" {
		identity = "fact_key:" + m.FactKey
		t = "fact"
	}
	sum := sha256.Sum256([]byte(identity))
	return "dream_" + t + "_" + hex.EncodeToString(sum[:6])
}

type dreamState struct {
	LastSequence  uint64     `json:"last_sequence"`
	LastProcessed time.Time  `json:"last_processed"`
	Stats         DreamStats `json:"stats"`
}

func (d *DreamService) loadState() {
	if d.config.StateFile == "" {
		return
	}
	raw, err := os.ReadFile(d.config.StateFile)
	if err != nil {
		return
	}
	var state dreamState
	if json.Unmarshal(raw, &state) == nil {
		d.lastProcessed = state.LastProcessed
		d.lastSequence = state.LastSequence
		d.stats = state.Stats
		d.lastRun = state.Stats.LastRunTime
	}
}

func (d *DreamService) saveState() error {
	if d.config.StateFile == "" {
		return nil
	}
	d.mu.Lock()
	state := dreamState{LastProcessed: d.lastProcessed, LastSequence: d.lastSequence, Stats: d.stats}
	d.mu.Unlock()
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.config.StateFile), 0o700); err != nil {
		return err
	}
	tmp := d.config.StateFile + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.config.StateFile)
}

func normalisePriority(importance int) int {
	switch {
	case importance >= 5:
		return 2 // critical
	case importance >= 3:
		return 1 // high
	default:
		return 0
	}
}

// Persist model output before applying it. A crash after a partial memory write
// reuses the same topics/content instead of invoking a nondeterministic model.
type pendingDream struct {
	Cursor   uint64            `json:"cursor"`
	Through  uint64            `json:"through"`
	Memories []ExtractedMemory `json:"memories"`
}

func (d *DreamService) loadPending(through uint64) ([]ExtractedMemory, bool, error) {
	if d.config.StateFile == "" {
		return nil, false, nil
	}
	raw, err := os.ReadFile(d.config.StateFile + ".pending")
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var p pendingDream
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, false, fmt.Errorf("reading pending dream batch: %w", err)
	}
	d.mu.Lock()
	cursor := d.lastSequence
	d.mu.Unlock()
	if p.Cursor != cursor || p.Through != through {
		return nil, false, nil
	}
	return p.Memories, true, nil
}

func (d *DreamService) savePending(through uint64, memories []ExtractedMemory) error {
	if d.config.StateFile == "" {
		return nil
	}
	d.mu.Lock()
	cursor := d.lastSequence
	d.mu.Unlock()
	raw, err := json.Marshal(pendingDream{Cursor: cursor, Through: through, Memories: memories})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.config.StateFile), 0700); err != nil {
		return err
	}
	path := d.config.StateFile + ".pending"
	if err := os.WriteFile(path+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
