package automemory

import (
	"context"
	"fmt"
	"log/slog"
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
}

// ExtractedMemory is the structured output the AI callback returns for each
// memory worth persisting.
type ExtractedMemory struct {
	Type       string // "fact", "preference", "skill", "context"
	Content    string
	Tags       []string
	Importance int // 1-5
}

// DreamStats exposes runtime metrics for observability.
type DreamStats struct {
	Runs              int
	MemoriesAdded     int
	MessagesProcessed int
	LastError         string
	LastRunTime       time.Time
}

// DreamService periodically (or on idle) processes recent conversations to
// extract long-term memories.
type DreamService struct {
	config DreamConfig

	mu           sync.Mutex
	lastRun      time.Time
	lastActivity time.Time
	running      bool
	stats        DreamStats

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
	return &DreamService{
		config:       cfg,
		lastActivity: time.Now(),
		stopCh:       make(chan struct{}),
	}
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
		d.mu.Unlock()

		if idleFor < d.config.IdleThreshold {
			// User is still active, keep waiting.
			continue
		}
		if !d.lastRun.IsZero() && sinceLastRun < d.config.Interval {
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
	logger := d.config.Logger

	d.mu.Lock()
	d.lastRun = time.Now()
	d.stats.LastRunTime = d.lastRun
	d.stats.Runs++
	d.mu.Unlock()

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

	transcript, msgCount := d.collectTranscript()
	if msgCount == 0 {
		logger.Debug("dream: no messages to process")
		return nil
	}

	logger.Info("dream: extracting memories", "messages", msgCount)

	memories, err := d.config.ExtractFn(ctx, transcript)
	if err != nil {
		d.recordError(err)
		return fmt.Errorf("extract callback: %w", err)
	}

	added := 0
	for _, m := range memories {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		if d.config.Memory == nil {
			continue
		}
		mem := memory.Memory{
			Topic:      d.topicFor(m),
			Content:    m.Content,
			Tags:       m.Tags,
			Priority:   normalisePriority(m.Importance),
			Source:     "dream",
			Confidence: float64(m.Importance) / 5.0,
		}
		if err := d.config.Memory.SaveWithMeta(ctx, mem); err != nil {
			logger.Warn("dream: failed saving memory", "error", err)
			continue
		}
		added++
	}

	d.mu.Lock()
	d.stats.MemoriesAdded += added
	d.stats.MessagesProcessed += msgCount
	d.stats.LastError = ""
	d.mu.Unlock()

	logger.Info("dream: extraction complete",
		"messages_processed", msgCount,
		"memories_added", added,
	)
	return nil
}

// collectTranscript gathers recent messages from all sessions, capped at
// MaxMessages, and renders them as a single transcript string.
func (d *DreamService) collectTranscript() (string, int) {
	store := d.config.SessionStore
	sessions := store.GetAllSessions()
	if len(sessions) == 0 {
		return "", 0
	}

	// Flatten all messages with timestamps so we can sort newest-first.
	type stamped struct {
		sessionID string
		msg       conversation.Message
	}
	var all []stamped
	for _, sess := range sessions {
		for _, m := range sess.Messages {
			all = append(all, stamped{sessionID: sess.ID, msg: m})
		}
	}
	if len(all) == 0 {
		return "", 0
	}

	// Sort by timestamp descending (most recent first).
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].msg.Timestamp.After(all[i].msg.Timestamp) {
				all[i], all[j] = all[j], all[i]
			}
		}
	}

	// Cap.
	max := d.config.MaxMessages
	if len(all) > max {
		all = all[:max]
	}

	// Reverse so the transcript reads chronologically.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}

	var sb strings.Builder
	for _, s := range all {
		fmt.Fprintf(&sb, "[%s] %s: %s\n",
			s.sessionID,
			string(s.msg.Role),
			strings.TrimSpace(s.msg.Content),
		)
	}
	return sb.String(), len(all)
}

func (d *DreamService) recordError(err error) {
	d.mu.Lock()
	d.stats.LastError = err.Error()
	d.mu.Unlock()
}

func (d *DreamService) topicFor(m ExtractedMemory) string {
	t := strings.TrimSpace(m.Type)
	if t == "" {
		t = "context"
	}
	return "dream_" + t
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
