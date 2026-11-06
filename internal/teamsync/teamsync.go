// Package teamsync implements team-wide memory synchronization.
//
// It shares knowledge entries across team members via a remote sync
// endpoint using bidirectional push/pull with last-write-wins merge
// semantics (resolved by UpdatedAt).
//
// Ported from src/services/teamMemorySync (TypeScript).
package teamsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// SyncConfig holds the settings needed to connect to the sync endpoint.
type SyncConfig struct {
	// Endpoint is the base URL of the team memory sync API
	// (e.g. "https://api.example.com/team-memory").
	Endpoint string

	// TeamID identifies the team whose memory entries are being synced.
	TeamID string

	// AuthToken is the bearer token sent in the Authorization header.
	AuthToken string

	// SyncInterval is the cadence used by StartAutoSync.
	// A zero value defaults to 30 seconds.
	SyncInterval time.Duration

	// Logger receives structured log output. When nil a no-op logger is used.
	Logger *slog.Logger
}

func (c SyncConfig) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func (c SyncConfig) syncInterval() time.Duration {
	if c.SyncInterval > 0 {
		return c.SyncInterval
	}
	return 30 * time.Second
}

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

// MemoryEntry is a single knowledge record shared across the team.
type MemoryEntry struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// HTTP request/response envelopes
// ---------------------------------------------------------------------------

type pushRequest struct {
	TeamID  string        `json:"team_id"`
	Entries []MemoryEntry `json:"entries"`
}

type pushResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type pullResponse struct {
	Entries []MemoryEntry `json:"entries"`
	Error   string        `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// SyncClient
// ---------------------------------------------------------------------------

// SyncClient manages bidirectional synchronization of memory entries
// with a remote endpoint.
type SyncClient struct {
	cfg  SyncConfig
	http *http.Client
	log  *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc // non-nil while auto-sync is running
}

// NewSyncClient creates a ready-to-use SyncClient.
func NewSyncClient(cfg SyncConfig) *SyncClient {
	return &SyncClient{
		cfg: cfg,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
		log: cfg.logger(),
	}
}

// ---------------------------------------------------------------------------
// Push
// ---------------------------------------------------------------------------

// Push sends local entries to the sync endpoint via POST.
func (sc *SyncClient) Push(ctx context.Context, entries []MemoryEntry) error {
	if len(entries) == 0 {
		return nil
	}

	body, err := json.Marshal(pushRequest{
		TeamID:  sc.cfg.TeamID,
		Entries: entries,
	})
	if err != nil {
		return fmt.Errorf("teamsync: marshal push payload: %w", err)
	}

	url := sc.cfg.Endpoint + "/push"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("teamsync: build push request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sc.cfg.AuthToken)

	resp, err := sc.http.Do(req)
	if err != nil {
		return fmt.Errorf("teamsync: push request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("teamsync: push returned %d: %s", resp.StatusCode, string(respBody))
	}

	var pr pushResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return fmt.Errorf("teamsync: decode push response: %w", err)
	}
	if !pr.OK {
		return fmt.Errorf("teamsync: push rejected by server: %s", pr.Error)
	}

	sc.log.Info("pushed entries", "count", len(entries))
	return nil
}

// ---------------------------------------------------------------------------
// Pull
// ---------------------------------------------------------------------------

// Pull fetches remote entries from the sync endpoint via GET.
func (sc *SyncClient) Pull(ctx context.Context) ([]MemoryEntry, error) {
	url := fmt.Sprintf("%s/pull?team_id=%s", sc.cfg.Endpoint, sc.cfg.TeamID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("teamsync: build pull request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+sc.cfg.AuthToken)

	resp, err := sc.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("teamsync: pull request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("teamsync: pull returned %d: %s", resp.StatusCode, string(respBody))
	}

	var pr pullResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("teamsync: decode pull response: %w", err)
	}
	if pr.Error != "" {
		return nil, fmt.Errorf("teamsync: pull error from server: %s", pr.Error)
	}

	sc.log.Info("pulled entries", "count", len(pr.Entries))
	return pr.Entries, nil
}

// ---------------------------------------------------------------------------
// Sync (merge)
// ---------------------------------------------------------------------------

// Sync performs a full bidirectional synchronization. It pulls remote
// entries, merges them with local using last-write-wins by UpdatedAt,
// and pushes any locally-newer entries back to the server.
//
// The returned slice is the merged set of entries (suitable for
// replacing the caller's local state).
func (sc *SyncClient) Sync(ctx context.Context, local []MemoryEntry) ([]MemoryEntry, error) {
	remote, err := sc.Pull(ctx)
	if err != nil {
		return nil, fmt.Errorf("teamsync: sync pull phase: %w", err)
	}

	merged, toPush := merge(local, remote)

	if len(toPush) > 0 {
		if err := sc.Push(ctx, toPush); err != nil {
			return nil, fmt.Errorf("teamsync: sync push phase: %w", err)
		}
	}

	sc.log.Info("sync completed",
		"local", len(local),
		"remote", len(remote),
		"merged", len(merged),
		"pushed", len(toPush),
	)
	return merged, nil
}

// merge combines local and remote entries using last-write-wins by
// UpdatedAt. It returns the full merged set and the subset of local
// entries that are newer than their remote counterpart (or absent
// remotely), which need to be pushed.
func merge(local, remote []MemoryEntry) (merged []MemoryEntry, toPush []MemoryEntry) {
	index := make(map[string]MemoryEntry, len(remote))
	for _, e := range remote {
		index[e.Key] = e
	}

	seen := make(map[string]struct{}, len(local)+len(remote))

	for _, le := range local {
		seen[le.Key] = struct{}{}
		re, exists := index[le.Key]
		if !exists {
			// Local-only entry — keep it and mark for push.
			merged = append(merged, le)
			toPush = append(toPush, le)
			continue
		}
		if le.UpdatedAt.After(re.UpdatedAt) {
			// Local is newer — use local, push to remote.
			merged = append(merged, le)
			toPush = append(toPush, le)
		} else {
			// Remote is newer or equal — use remote.
			merged = append(merged, re)
		}
	}

	// Add remote-only entries.
	for _, re := range remote {
		if _, ok := seen[re.Key]; !ok {
			merged = append(merged, re)
		}
	}

	// Deterministic output order.
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Key < merged[j].Key
	})

	return merged, toPush
}

// ---------------------------------------------------------------------------
// Auto-sync
// ---------------------------------------------------------------------------

// StartAutoSync runs a background goroutine that periodically calls
// Sync. On each tick it obtains the current local entries via localFn
// and delivers the merged result to onUpdate.
//
// The goroutine stops when ctx is cancelled. Only one auto-sync loop
// can be active at a time; calling StartAutoSync again is a no-op
// until the previous one is stopped (by cancelling its context or
// calling StopAutoSync).
func (sc *SyncClient) StartAutoSync(
	ctx context.Context,
	localFn func() []MemoryEntry,
	onUpdate func([]MemoryEntry),
) {
	sc.mu.Lock()
	if sc.cancel != nil {
		sc.mu.Unlock()
		sc.log.Warn("auto-sync already running, ignoring duplicate start")
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	sc.cancel = cancel
	sc.mu.Unlock()

	interval := sc.cfg.syncInterval()
	sc.log.Info("starting auto-sync", "interval", interval)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer func() {
			sc.mu.Lock()
			sc.cancel = nil
			sc.mu.Unlock()
		}()

		// Run once immediately on start.
		sc.runOnce(ctx, localFn, onUpdate)

		for {
			select {
			case <-ctx.Done():
				sc.log.Info("auto-sync stopped")
				return
			case <-ticker.C:
				sc.runOnce(ctx, localFn, onUpdate)
			}
		}
	}()
}

// StopAutoSync cancels the background auto-sync goroutine.
func (sc *SyncClient) StopAutoSync() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.cancel != nil {
		sc.cancel()
		sc.cancel = nil
	}
}

func (sc *SyncClient) runOnce(
	ctx context.Context,
	localFn func() []MemoryEntry,
	onUpdate func([]MemoryEntry),
) {
	local := localFn()
	merged, err := sc.Sync(ctx, local)
	if err != nil {
		sc.log.Error("auto-sync cycle failed", "error", err)
		return
	}
	onUpdate(merged)
}
