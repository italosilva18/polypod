// Package vcr provides recording and replay of API calls and tool executions
// for testing and debugging. Analogous to VCR (Video Cassette Recorder)
// libraries, it caches request/response pairs keyed by a content hash.
//
// Migrated from TypeScript: services/vcr.ts
package vcr

import (
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// Message represents a single conversation turn (user or assistant).
type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp,omitempty"`
}

// ToolCall records a tool invocation and its result.
type ToolCall struct {
	Name   string `json:"name"`
	Input  any    `json:"input"`
	Output any    `json:"output"`
}

// APIResponse captures a model API response.
type APIResponse struct {
	Model  string `json:"model"`
	Text   string `json:"text"`
	Tokens int    `json:"tokens,omitempty"`
}

// Recording is a complete captured session that can be saved and replayed.
type Recording struct {
	ID           string            `json:"id"`
	CreatedAt    time.Time         `json:"createdAt"`
	Messages     []Message         `json:"messages"`
	ToolCalls    []ToolCall        `json:"toolCalls"`
	APIResponses []APIResponse     `json:"apiResponses"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// ---------------------------------------------------------------------------
// VCRRecorder — records API calls and tool executions.
// ---------------------------------------------------------------------------

// VCRRecorder captures interactions for later replay.
type VCRRecorder struct {
	mu        sync.Mutex
	recording Recording
	active    bool
}

// NewRecorder creates a new recorder with the given session ID.
func NewRecorder(id string) *VCRRecorder {
	return &VCRRecorder{
		recording: Recording{
			ID:        id,
			CreatedAt: time.Now(),
			Metadata:  make(map[string]string),
		},
		active: true,
	}
}

// IsActive reports whether recording is in progress.
func (r *VCRRecorder) IsActive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

// RecordMessage adds a message to the recording.
func (r *VCRRecorder) RecordMessage(msg Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return
	}
	if msg.Timestamp == "" {
		msg.Timestamp = time.Now().Format(time.RFC3339)
	}
	r.recording.Messages = append(r.recording.Messages, msg)
}

// RecordToolCall adds a tool invocation to the recording.
func (r *VCRRecorder) RecordToolCall(tc ToolCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return
	}
	r.recording.ToolCalls = append(r.recording.ToolCalls, tc)
}

// RecordAPIResponse adds an API response to the recording.
func (r *VCRRecorder) RecordAPIResponse(resp APIResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return
	}
	r.recording.APIResponses = append(r.recording.APIResponses, resp)
}

// SetMetadata stores an arbitrary key/value pair on the recording.
func (r *VCRRecorder) SetMetadata(key, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recording.Metadata[key] = value
}

// Stop terminates recording and returns the captured data.
func (r *VCRRecorder) Stop() Recording {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active = false
	return r.recording
}

// ---------------------------------------------------------------------------
// VCRPlayer — replays recorded sessions.
// ---------------------------------------------------------------------------

// VCRPlayer provides sequential access to a recorded session.
type VCRPlayer struct {
	recording Recording
	msgIdx    int
	toolIdx   int
	apiIdx    int
}

// NewPlayer creates a player from a loaded recording.
func NewPlayer(rec Recording) *VCRPlayer {
	return &VCRPlayer{recording: rec}
}

// NextMessage returns the next message, or false if exhausted.
func (p *VCRPlayer) NextMessage() (Message, bool) {
	if p.msgIdx >= len(p.recording.Messages) {
		return Message{}, false
	}
	msg := p.recording.Messages[p.msgIdx]
	p.msgIdx++
	return msg, true
}

// NextToolCall returns the next tool call, or false if exhausted.
func (p *VCRPlayer) NextToolCall() (ToolCall, bool) {
	if p.toolIdx >= len(p.recording.ToolCalls) {
		return ToolCall{}, false
	}
	tc := p.recording.ToolCalls[p.toolIdx]
	p.toolIdx++
	return tc, true
}

// NextAPIResponse returns the next API response, or false if exhausted.
func (p *VCRPlayer) NextAPIResponse() (APIResponse, bool) {
	if p.apiIdx >= len(p.recording.APIResponses) {
		return APIResponse{}, false
	}
	resp := p.recording.APIResponses[p.apiIdx]
	p.apiIdx++
	return resp, true
}

// HasMore reports whether any items remain.
func (p *VCRPlayer) HasMore() bool {
	return p.msgIdx < len(p.recording.Messages) ||
		p.toolIdx < len(p.recording.ToolCalls) ||
		p.apiIdx < len(p.recording.APIResponses)
}

// Reset resets all cursors to the beginning of the recording.
func (p *VCRPlayer) Reset() {
	p.msgIdx = 0
	p.toolIdx = 0
	p.apiIdx = 0
}

// ---------------------------------------------------------------------------
// Persistence
// ---------------------------------------------------------------------------

// SaveRecording writes a recording to disk as JSON.
func SaveRecording(rec Recording, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create dir: %w", err)
	}

	filename := filepath.Join(dir, rec.ID+".json")
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(filename, data, 0o644); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	return filename, nil
}

// LoadRecording reads a recording from disk.
func LoadRecording(path string) (Recording, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Recording{}, fmt.Errorf("read: %w", err)
	}
	var rec Recording
	if err := json.Unmarshal(data, &rec); err != nil {
		return Recording{}, fmt.Errorf("unmarshal: %w", err)
	}
	return rec, nil
}

// ---------------------------------------------------------------------------
// Fixture-based caching (for deterministic test replay)
// ---------------------------------------------------------------------------

// FixtureCache provides content-addressed fixture storage for VCR.
// It hashes the input to determine a cache key and stores/retrieves
// JSON fixtures on disk.
type FixtureCache struct {
	Dir string // root directory for fixture files
}

// NewFixtureCache creates a cache rooted at dir.
func NewFixtureCache(dir string) *FixtureCache {
	return &FixtureCache{Dir: dir}
}

// CacheKey computes a content hash for the given input.
func CacheKey(input any) string {
	data, _ := json.Marshal(input)
	h := sha1.Sum(data)
	return fmt.Sprintf("%x", h[:6])
}

// Get retrieves a cached fixture by name and hash. Returns nil if not found.
func (fc *FixtureCache) Get(name, hash string) (json.RawMessage, error) {
	filename := filepath.Join(fc.Dir, fmt.Sprintf("%s-%s.json", name, hash))
	data, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

// Put stores a fixture value for the given name and hash.
func (fc *FixtureCache) Put(name, hash string, value any) error {
	if err := os.MkdirAll(fc.Dir, 0o755); err != nil {
		return err
	}
	filename := filepath.Join(fc.Dir, fmt.Sprintf("%s-%s.json", name, hash))
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0o644)
}

// ---------------------------------------------------------------------------
// Value dehydration / hydration (for portable fixtures)
// ---------------------------------------------------------------------------

// Dehydrator replaces environment-specific paths with placeholders.
type Dehydrator struct {
	CWD        string
	ConfigHome string
}

// Dehydrate replaces paths in s with portable placeholders.
func (d *Dehydrator) Dehydrate(s string) string {
	if d.ConfigHome != "" {
		s = strings.ReplaceAll(s, d.ConfigHome, "[CONFIG_HOME]")
	}
	if d.CWD != "" {
		s = strings.ReplaceAll(s, d.CWD, "[CWD]")
	}
	return s
}

// Hydrate restores environment-specific paths from placeholders.
func (d *Dehydrator) Hydrate(s string) string {
	s = strings.ReplaceAll(s, "[CONFIG_HOME]", d.ConfigHome)
	s = strings.ReplaceAll(s, "[CWD]", d.CWD)
	return s
}
