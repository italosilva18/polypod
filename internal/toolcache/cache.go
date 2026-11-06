// Package toolcache provides caching for tool schemas, results, and instance pooling.
// Migrated from Claude Code's toolSchemaCache.ts, toolResultStorage.ts, toolPool.ts.
package toolcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ToolSchemaCache caches rendered tool schemas to avoid regeneration.
// Tool schemas render at server position 2 (before system prompt), so any
// byte-level change busts the entire tool block AND everything downstream.
// Memoizing per-session locks the schema bytes at first render.
type ToolSchemaCache struct {
	mu    sync.RWMutex
	cache map[string]CachedSchema
}

// CachedSchema holds a cached tool schema definition.
type CachedSchema struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema"`
	Strict       bool           `json:"strict,omitempty"`
	CacheControl *CacheControl  `json:"cache_control,omitempty"`
}

// CacheControl specifies API-level cache behavior.
type CacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// NewToolSchemaCache creates an empty schema cache.
func NewToolSchemaCache() *ToolSchemaCache {
	return &ToolSchemaCache{
		cache: make(map[string]CachedSchema),
	}
}

// Get retrieves a cached schema by tool name.
// Returns the cached schema and true if found, zero value and false otherwise.
func (c *ToolSchemaCache) Get(toolName string) (CachedSchema, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	schema, ok := c.cache[toolName]
	return schema, ok
}

// Set stores a schema in the cache.
func (c *ToolSchemaCache) Set(toolName string, schema CachedSchema) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[toolName] = schema
}

// Clear removes all cached schemas. Called on auth changes or MCP reconnects.
func (c *ToolSchemaCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[string]CachedSchema)
}

// Size returns the number of cached schemas.
func (c *ToolSchemaCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}

// ToolResultStorage persists large tool results to disk instead of truncating them.
// When a tool result exceeds the persistence threshold, the full content is saved
// to a file and replaced with a preview + file reference in the conversation.
type ToolResultStorage struct {
	mu        sync.Mutex
	baseDir   string
	sessionID string

	// seenIDs tracks tool_use_ids that have been processed.
	seenIDs map[string]bool

	// replacements maps tool_use_id -> replacement preview string.
	replacements map[string]string
}

// PersistedResult holds information about a tool result persisted to disk.
type PersistedResult struct {
	Filepath     string
	OriginalSize int
	IsJSON       bool
	Preview      string
	HasMore      bool
}

const (
	// ToolResultsSubdir is the subdirectory for tool results within a session.
	ToolResultsSubdir = "tool-results"

	// PersistedOutputTag wraps persisted output messages.
	PersistedOutputTag        = "<persisted-output>"
	PersistedOutputClosingTag = "</persisted-output>"

	// ToolResultClearedMessage is used when content is cleared without persisting.
	ToolResultClearedMessage = "[Old tool result content cleared]"

	// PreviewSizeBytes is the max bytes for the preview in the reference message.
	PreviewSizeBytes = 2000

	// DefaultMaxResultSizeChars is the default persistence threshold.
	DefaultMaxResultSizeChars = 50000
)

// NewToolResultStorage creates a new storage backend for the given session.
func NewToolResultStorage(baseDir, sessionID string) *ToolResultStorage {
	return &ToolResultStorage{
		baseDir:      baseDir,
		sessionID:    sessionID,
		seenIDs:      make(map[string]bool),
		replacements: make(map[string]string),
	}
}

// ResultsDir returns the directory path for tool result files.
func (s *ToolResultStorage) ResultsDir() string {
	return filepath.Join(s.baseDir, s.sessionID, ToolResultsSubdir)
}

// ResultPath returns the file path for a specific tool result.
func (s *ToolResultStorage) ResultPath(toolUseID string, isJSON bool) string {
	ext := "txt"
	if isJSON {
		ext = "json"
	}
	return filepath.Join(s.ResultsDir(), toolUseID+"."+ext)
}

// EnsureDir creates the tool results directory.
func (s *ToolResultStorage) EnsureDir() error {
	return os.MkdirAll(s.ResultsDir(), 0o755)
}

// Persist writes tool result content to disk and returns a PersistedResult.
// Returns nil if the content is under the threshold or persistence fails.
func (s *ToolResultStorage) Persist(content string, toolUseID string, isJSON bool) (*PersistedResult, error) {
	if err := s.EnsureDir(); err != nil {
		return nil, fmt.Errorf("creating tool results dir: %w", err)
	}

	fp := s.ResultPath(toolUseID, isJSON)

	// Write with O_EXCL to skip if already persisted (same tool_use_id is deterministic).
	f, err := os.OpenFile(fp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			// Already persisted on a prior turn, generate preview from content.
		} else {
			return nil, fmt.Errorf("writing tool result: %w", err)
		}
	} else {
		_, writeErr := f.WriteString(content)
		f.Close()
		if writeErr != nil {
			return nil, fmt.Errorf("writing tool result content: %w", writeErr)
		}
	}

	preview, hasMore := GeneratePreview(content, PreviewSizeBytes)
	return &PersistedResult{
		Filepath:     fp,
		OriginalSize: len(content),
		IsJSON:       isJSON,
		Preview:      preview,
		HasMore:      hasMore,
	}, nil
}

// BuildLargeResultMessage creates the reference message for a persisted tool result.
func BuildLargeResultMessage(result *PersistedResult) string {
	var b strings.Builder
	b.WriteString(PersistedOutputTag)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Output too large (%s). Full output saved to: %s\n\n",
		formatSize(result.OriginalSize), result.Filepath))
	b.WriteString(fmt.Sprintf("Preview (first %s):\n", formatSize(PreviewSizeBytes)))
	b.WriteString(result.Preview)
	if result.HasMore {
		b.WriteString("\n...\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString(PersistedOutputClosingTag)
	return b.String()
}

// GeneratePreview truncates content at a newline boundary when possible.
func GeneratePreview(content string, maxBytes int) (preview string, hasMore bool) {
	if len(content) <= maxBytes {
		return content, false
	}

	truncated := content[:maxBytes]
	lastNewline := strings.LastIndex(truncated, "\n")

	// If newline is reasonably close to the limit, use it.
	if lastNewline > maxBytes/2 {
		return content[:lastNewline], true
	}
	return truncated, true
}

// MarkSeen records that a tool_use_id has been processed.
func (s *ToolResultStorage) MarkSeen(toolUseID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seenIDs[toolUseID] = true
}

// IsSeen returns true if a tool_use_id has already been processed.
func (s *ToolResultStorage) IsSeen(toolUseID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seenIDs[toolUseID]
}

// SetReplacement stores the replacement text for a tool_use_id.
func (s *ToolResultStorage) SetReplacement(toolUseID, replacement string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seenIDs[toolUseID] = true
	s.replacements[toolUseID] = replacement
}

// GetReplacement returns the stored replacement text for a tool_use_id.
func (s *ToolResultStorage) GetReplacement(toolUseID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.replacements[toolUseID]
	return r, ok
}

// Clone creates a deep copy of the storage state (for cache-sharing forks).
func (s *ToolResultStorage) Clone() *ToolResultStorage {
	s.mu.Lock()
	defer s.mu.Unlock()

	clone := &ToolResultStorage{
		baseDir:      s.baseDir,
		sessionID:    s.sessionID,
		seenIDs:      make(map[string]bool, len(s.seenIDs)),
		replacements: make(map[string]string, len(s.replacements)),
	}
	for k, v := range s.seenIDs {
		clone.seenIDs[k] = v
	}
	for k, v := range s.replacements {
		clone.replacements[k] = v
	}
	return clone
}

func formatSize(bytes int) string {
	const (
		kb = 1024
		mb = 1024 * 1024
	)
	switch {
	case bytes >= mb:
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1fKB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// ToolPool manages a pool of tool instances for reuse.
// Tools are keyed by name and can be checked out / returned.
type ToolPool struct {
	mu    sync.Mutex
	pools map[string][]any
}

// NewToolPool creates a new empty tool pool.
func NewToolPool() *ToolPool {
	return &ToolPool{
		pools: make(map[string][]any),
	}
}

// Get retrieves a tool instance from the pool. Returns nil if none available.
func (tp *ToolPool) Get(toolName string) any {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	pool := tp.pools[toolName]
	if len(pool) == 0 {
		return nil
	}
	// Pop the last element.
	instance := pool[len(pool)-1]
	tp.pools[toolName] = pool[:len(pool)-1]
	return instance
}

// Put returns a tool instance to the pool.
func (tp *ToolPool) Put(toolName string, instance any) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.pools[toolName] = append(tp.pools[toolName], instance)
}

// Size returns the number of pooled instances for a tool.
func (tp *ToolPool) Size(toolName string) int {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return len(tp.pools[toolName])
}

// Clear removes all pooled instances.
func (tp *ToolPool) Clear() {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.pools = make(map[string][]any)
}

// CacheKey generates a deterministic cache key from a tool name and input hash.
// Used to cache tool schemas and avoid redundant computation.
func CacheKey(toolName string, input any) string {
	data, err := json.Marshal(input)
	if err != nil {
		// Fallback: use just the tool name.
		return toolName
	}
	h := sha256.Sum256(data)
	return toolName + ":" + hex.EncodeToString(h[:8])
}

// CacheEntry represents a cached value with expiration.
type CacheEntry struct {
	Value     any
	ExpiresAt time.Time
}

// IsExpired returns true if the cache entry has expired.
func (e CacheEntry) IsExpired() bool {
	return time.Now().After(e.ExpiresAt)
}
