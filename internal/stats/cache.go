// Package stats implements transcript scanning, aggregation, cache, and streaks
// for the /stats command. It mirrors the TypeScript statsCache.ts logic.
package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StatsCacheVersion is the current on-disk cache format version.
const StatsCacheVersion = 3

// minMigratableVersion is the oldest version we can migrate from.
const minMigratableVersion = 1

// statsCacheFileName is the filename within the config dir.
const statsCacheFileName = "stats-cache.json"

// cacheMu guards all cache I/O so concurrent callers can't corrupt the file.
var cacheMu sync.Mutex

// ModelStats holds per-model token/cost aggregates.
type ModelStats struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	WebSearchRequests        int     `json:"webSearchRequests"`
	CostUSD                  float64 `json:"costUSD"`
	ContextWindow            int     `json:"contextWindow"`
	MaxOutputTokens          int     `json:"maxOutputTokens"`
	RequestCount             int     `json:"requestCount"`
}

// DailyActivity holds activity counts for one day.
type DailyActivity struct {
	Date          string `json:"date"` // YYYY-MM-DD
	MessageCount  int    `json:"messageCount"`
	SessionCount  int    `json:"sessionCount"`
	ToolCallCount int    `json:"toolCallCount"`
}

// DailyModelTokens holds per-model token totals for one day.
type DailyModelTokens struct {
	Date          string         `json:"date"` // YYYY-MM-DD
	TokensByModel map[string]int `json:"tokensByModel"`
}

// CachedSessionStats is the minimal session info kept in the cache.
type CachedSessionStats struct {
	SessionID    string `json:"sessionId"`
	Duration     int64  `json:"duration"` // milliseconds
	MessageCount int    `json:"messageCount"`
	Timestamp    string `json:"timestamp"` // RFC3339 of first message
}

// StatsCacheV3 is the persisted stats cache (version 3).
// It mirrors PersistedStatsCache from statsCache.ts.
type StatsCacheV3 struct {
	Version                     int                   `json:"version"`
	LastComputedDate            string                `json:"lastComputedDate"` // YYYY-MM-DD or ""
	DailyActivity               []DailyActivity       `json:"dailyActivity"`
	DailyModelTokens            []DailyModelTokens    `json:"dailyModelTokens"`
	ModelUsage                  map[string]ModelStats `json:"modelUsage"`
	TotalSessions               int                   `json:"totalSessions"`
	TotalMessages               int                   `json:"totalMessages"`
	LongestSession              *CachedSessionStats   `json:"longestSession"`
	FirstSessionDate            string                `json:"firstSessionDate"` // ISO timestamp or ""
	HourCounts                  map[string]int        `json:"hourCounts"`       // "0".."23" → count
	TotalSpeculationTimeSavedMs int64                 `json:"totalSpeculationTimeSavedMs"`
}

// GetCachePath returns the path to the stats-cache.json file.
func GetCachePath() string {
	return filepath.Join(getConfigDir(), statsCacheFileName)
}

// getConfigDir returns ~/.config/polypod (creating it if needed).
func getConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".config", "polypod")
	}
	return filepath.Join(home, ".config", "polypod")
}

// emptyCache returns a freshly initialised cache at the current version.
func emptyCache() *StatsCacheV3 {
	return &StatsCacheV3{
		Version:          StatsCacheVersion,
		DailyActivity:    []DailyActivity{},
		DailyModelTokens: []DailyModelTokens{},
		ModelUsage:       map[string]ModelStats{},
		HourCounts:       map[string]int{},
	}
}

// LoadStatsCache reads the stats cache from disk.
// Returns an empty cache on any error (missing file, parse failure, bad version).
// Migrates v1/v2 caches to v3 in-place on disk.
func LoadStatsCache() (*StatsCacheV3, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	path := GetCachePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyCache(), nil
		}
		return emptyCache(), fmt.Errorf("read stats cache: %w", err)
	}

	// Parse into a loose map first so we can inspect the version field.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return emptyCache(), fmt.Errorf("parse stats cache: %w", err)
	}

	var version int
	if vraw, ok := raw["version"]; ok {
		_ = json.Unmarshal(vraw, &version)
	}

	if version != StatsCacheVersion {
		migrated := migrateCache(raw, version)
		if migrated == nil {
			return emptyCache(), nil
		}
		// Persist the migrated cache so we don't re-migrate on every load.
		if saveErr := saveCacheNoLock(migrated); saveErr != nil {
			// Non-fatal: we have the in-memory copy.
			_ = saveErr
		}
		return migrated, nil
	}

	// Unmarshal into the typed struct.
	var cache StatsCacheV3
	if err := json.Unmarshal(data, &cache); err != nil {
		return emptyCache(), fmt.Errorf("unmarshal stats cache: %w", err)
	}

	// Basic structural validation.
	if cache.ModelUsage == nil {
		cache.ModelUsage = map[string]ModelStats{}
	}
	if cache.HourCounts == nil {
		cache.HourCounts = map[string]int{}
	}
	if cache.DailyActivity == nil {
		cache.DailyActivity = []DailyActivity{}
	}
	if cache.DailyModelTokens == nil {
		cache.DailyModelTokens = []DailyModelTokens{}
	}

	return &cache, nil
}

// migrateCache attempts to build a v3 cache from an older raw JSON map.
// Returns nil if the version is unknown or the required fields are absent.
func migrateCache(raw map[string]json.RawMessage, version int) *StatsCacheV3 {
	if version < minMigratableVersion || version > StatsCacheVersion {
		return nil
	}

	// We require at minimum dailyActivity, dailyModelTokens, totalSessions,
	// totalMessages — same guard as the TS migrateStatsCache().
	required := []string{"dailyActivity", "dailyModelTokens", "totalSessions", "totalMessages"}
	for _, k := range required {
		if _, ok := raw[k]; !ok {
			return nil
		}
	}

	c := emptyCache()
	c.Version = StatsCacheVersion

	if v, ok := raw["lastComputedDate"]; ok {
		_ = json.Unmarshal(v, &c.LastComputedDate)
	}
	if v, ok := raw["dailyActivity"]; ok {
		_ = json.Unmarshal(v, &c.DailyActivity)
	}
	if v, ok := raw["dailyModelTokens"]; ok {
		_ = json.Unmarshal(v, &c.DailyModelTokens)
	}
	if v, ok := raw["modelUsage"]; ok {
		_ = json.Unmarshal(v, &c.ModelUsage)
	}
	if v, ok := raw["totalSessions"]; ok {
		_ = json.Unmarshal(v, &c.TotalSessions)
	}
	if v, ok := raw["totalMessages"]; ok {
		_ = json.Unmarshal(v, &c.TotalMessages)
	}
	if v, ok := raw["longestSession"]; ok {
		_ = json.Unmarshal(v, &c.LongestSession)
	}
	if v, ok := raw["firstSessionDate"]; ok {
		_ = json.Unmarshal(v, &c.FirstSessionDate)
	}
	if v, ok := raw["hourCounts"]; ok {
		_ = json.Unmarshal(v, &c.HourCounts)
	}
	if v, ok := raw["totalSpeculationTimeSavedMs"]; ok {
		_ = json.Unmarshal(v, &c.TotalSpeculationTimeSavedMs)
	}

	if c.DailyActivity == nil {
		c.DailyActivity = []DailyActivity{}
	}
	if c.DailyModelTokens == nil {
		c.DailyModelTokens = []DailyModelTokens{}
	}
	if c.ModelUsage == nil {
		c.ModelUsage = map[string]ModelStats{}
	}
	if c.HourCounts == nil {
		c.HourCounts = map[string]int{}
	}

	return c
}

// SaveStatsCache writes the cache to disk atomically (temp-file + rename).
// Thread-safe: acquires the package-level mutex.
func SaveStatsCache(cache *StatsCacheV3) error {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	return saveCacheNoLock(cache)
}

// saveCacheNoLock performs the actual write; caller must hold cacheMu.
func saveCacheNoLock(cache *StatsCacheV3) error {
	dir := getConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}

	path := GetCachePath()
	tmpPath := fmt.Sprintf("%s.%d.tmp", path, time.Now().UnixNano())

	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal stats cache: %w", err)
	}

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open tmp cache file: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write tmp cache: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync tmp cache: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close tmp cache: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename cache file: %w", err)
	}

	return nil
}

// MergeIncrementalStats merges freshly-computed day data into the cache.
// newLastComputedDate is the YYYY-MM-DD date through which data was processed.
func MergeIncrementalStats(
	cache *StatsCacheV3,
	newActivity []DailyActivity,
	newModelTokens []DailyModelTokens,
	newModelUsage map[string]ModelStats,
	newSessions []CachedSessionStats,
	newHourCounts map[string]int,
	newSpeculationMs int64,
	newLastComputedDate string,
) {
	// ── Daily activity ────────────────────────────────────────────────────────
	activityByDate := make(map[string]*DailyActivity, len(cache.DailyActivity))
	for i := range cache.DailyActivity {
		d := &cache.DailyActivity[i]
		activityByDate[d.Date] = d
	}
	for _, day := range newActivity {
		if existing, ok := activityByDate[day.Date]; ok {
			existing.MessageCount += day.MessageCount
			existing.SessionCount += day.SessionCount
			existing.ToolCallCount += day.ToolCallCount
		} else {
			cp := day
			cache.DailyActivity = append(cache.DailyActivity, cp)
			activityByDate[day.Date] = &cache.DailyActivity[len(cache.DailyActivity)-1]
		}
	}
	sortDailyActivity(cache.DailyActivity)

	// ── Daily model tokens ────────────────────────────────────────────────────
	tokensByDate := make(map[string]*DailyModelTokens, len(cache.DailyModelTokens))
	for i := range cache.DailyModelTokens {
		d := &cache.DailyModelTokens[i]
		tokensByDate[d.Date] = d
	}
	for _, day := range newModelTokens {
		if existing, ok := tokensByDate[day.Date]; ok {
			for model, tokens := range day.TokensByModel {
				existing.TokensByModel[model] += tokens
			}
		} else {
			cp := DailyModelTokens{
				Date:          day.Date,
				TokensByModel: make(map[string]int, len(day.TokensByModel)),
			}
			for k, v := range day.TokensByModel {
				cp.TokensByModel[k] = v
			}
			cache.DailyModelTokens = append(cache.DailyModelTokens, cp)
			tokensByDate[day.Date] = &cache.DailyModelTokens[len(cache.DailyModelTokens)-1]
		}
	}
	sortDailyModelTokens(cache.DailyModelTokens)

	// ── Model usage ───────────────────────────────────────────────────────────
	if cache.ModelUsage == nil {
		cache.ModelUsage = make(map[string]ModelStats)
	}
	for model, usage := range newModelUsage {
		if existing, ok := cache.ModelUsage[model]; ok {
			cache.ModelUsage[model] = ModelStats{
				InputTokens:              existing.InputTokens + usage.InputTokens,
				OutputTokens:             existing.OutputTokens + usage.OutputTokens,
				CacheReadInputTokens:     existing.CacheReadInputTokens + usage.CacheReadInputTokens,
				CacheCreationInputTokens: existing.CacheCreationInputTokens + usage.CacheCreationInputTokens,
				WebSearchRequests:        existing.WebSearchRequests + usage.WebSearchRequests,
				CostUSD:                  existing.CostUSD + usage.CostUSD,
				ContextWindow:            maxInt(existing.ContextWindow, usage.ContextWindow),
				MaxOutputTokens:          maxInt(existing.MaxOutputTokens, usage.MaxOutputTokens),
				RequestCount:             existing.RequestCount + usage.RequestCount,
			}
		} else {
			cache.ModelUsage[model] = usage
		}
	}

	// ── Hour counts ───────────────────────────────────────────────────────────
	if cache.HourCounts == nil {
		cache.HourCounts = make(map[string]int)
	}
	for hour, count := range newHourCounts {
		cache.HourCounts[hour] += count
	}

	// ── Session aggregates ────────────────────────────────────────────────────
	cache.TotalSessions += len(newSessions)
	for _, s := range newSessions {
		cache.TotalMessages += s.MessageCount

		if cache.LongestSession == nil || s.Duration > cache.LongestSession.Duration {
			cp := s
			cache.LongestSession = &cp
		}

		if cache.FirstSessionDate == "" || s.Timestamp < cache.FirstSessionDate {
			cache.FirstSessionDate = s.Timestamp
		}
	}

	// ── Totals ────────────────────────────────────────────────────────────────
	cache.TotalSpeculationTimeSavedMs += newSpeculationMs
	cache.LastComputedDate = newLastComputedDate
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
