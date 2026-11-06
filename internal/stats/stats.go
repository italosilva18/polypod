package stats

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ────────────────────────────────────────────────────────────────────────────
// Public types
// ────────────────────────────────────────────────────────────────────────────

// SessionStats holds per-session metadata derived from a JSONL transcript.
type SessionStats struct {
	SessionID    string `json:"sessionId"`
	Duration     int64  `json:"duration"` // milliseconds
	MessageCount int    `json:"messageCount"`
	Timestamp    string `json:"timestamp"` // ISO-8601 of first message
	Date         string `json:"date"`      // YYYY-MM-DD of first message
}

// StreakInfo describes the current and longest activity streak.
type StreakInfo struct {
	CurrentStreak      int    `json:"currentStreak"`
	LongestStreak      int    `json:"longestStreak"`
	CurrentStreakStart string `json:"currentStreakStart"` // YYYY-MM-DD or ""
	LongestStreakStart string `json:"longestStreakStart"` // YYYY-MM-DD or ""
	LongestStreakEnd   string `json:"longestStreakEnd"`   // YYYY-MM-DD or ""
}

// DayBreakdown is one row in the daily breakdown table.
type DayBreakdown struct {
	Date          string  `json:"date"`
	SessionCount  int     `json:"sessionCount"`
	MessageCount  int     `json:"messageCount"`
	ToolCallCount int     `json:"toolCallCount"`
	InputTokens   int     `json:"inputTokens"`
	OutputTokens  int     `json:"outputTokens"`
	CostUSD       float64 `json:"costUSD"`
}

// AggregatedStats is the full stats object returned by ComputeStats.
type AggregatedStats struct {
	// Activity overview
	TotalSessions int `json:"totalSessions"`
	TotalMessages int `json:"totalMessages"`
	TotalDays     int `json:"totalDays"`  // calendar span
	ActiveDays    int `json:"activeDays"` // days with at least one session

	// Streaks
	Streaks StreakInfo `json:"streaks"`

	// Daily activity (sorted by date)
	DailyActivity    []DailyActivity    `json:"dailyActivity"`
	DailyModelTokens []DailyModelTokens `json:"dailyModelTokens"`

	// Model usage
	ModelUsage map[string]ModelStats `json:"modelUsage"`

	// Session extremes
	LongestSession   *SessionStats `json:"longestSession"`
	FirstSessionDate string        `json:"firstSessionDate"` // ISO timestamp
	LastSessionDate  string        `json:"lastSessionDate"`  // ISO timestamp

	// Peak activity
	PeakActivityDay  string `json:"peakActivityDay"`  // YYYY-MM-DD
	PeakActivityHour int    `json:"peakActivityHour"` // 0-23, -1 if unknown

	// Derived
	MostUsedModel          string         `json:"mostUsedModel"`
	AverageSessionLengthMs int64          `json:"averageSessionLengthMs"`
	MostUsedTools          map[string]int `json:"mostUsedTools"`

	TotalSpeculationTimeSavedMs int64 `json:"totalSpeculationTimeSavedMs"`
}

// ────────────────────────────────────────────────────────────────────────────
// JSONL transcript types (minimal — only what we need to parse)
// ────────────────────────────────────────────────────────────────────────────

// transcriptEntry is a single line in a .jsonl transcript.
type transcriptEntry struct {
	Type        string         `json:"type"`
	Timestamp   string         `json:"timestamp"`
	IsSidechain bool           `json:"isSidechain"`
	Message     *transcriptMsg `json:"message,omitempty"`
	TimeSavedMs int64          `json:"timeSavedMs,omitempty"`
}

// transcriptMsg is the nested message object in an assistant entry.
type transcriptMsg struct {
	Model   string          `json:"model"`
	Usage   *tokenUsage     `json:"usage,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

// tokenUsage mirrors the Anthropic usage object.
type tokenUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// contentBlock is one element of the message content array.
type contentBlock struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// transcriptMessageTypes mirrors TRANSCRIPT_MESSAGE_TYPES in stats.ts.
var transcriptMessageTypes = map[string]bool{
	"user":       true,
	"assistant":  true,
	"attachment": true,
	"system":     true,
	"progress":   true,
}

// syntheticModel is never counted in stats.
const syntheticModel = "__synthetic__"

// subagentsDirName is the subdirectory name containing subagent transcripts.
const subagentsDirName = "subagents"

// ────────────────────────────────────────────────────────────────────────────
// processedStats is the intermediate accumulator for a batch of session files.
// ────────────────────────────────────────────────────────────────────────────
type processedStats struct {
	activityByDate map[string]*DailyActivity
	tokensByDate   map[string]map[string]int // date → model → tokens
	modelUsage     map[string]ModelStats
	sessions       []SessionStats
	hourCounts     map[string]int // "0".."23"
	totalMessages  int
	speculationMs  int64
	toolCounts     map[string]int
}

func newProcessedStats() *processedStats {
	return &processedStats{
		activityByDate: make(map[string]*DailyActivity),
		tokensByDate:   make(map[string]map[string]int),
		modelUsage:     make(map[string]ModelStats),
		hourCounts:     make(map[string]int),
		toolCounts:     make(map[string]int),
	}
}

// ────────────────────────────────────────────────────────────────────────────
// ComputeStats scans all JSONL transcripts under sessionsDir and returns
// aggregated stats. It uses cache to skip files whose mtime hasn't changed.
// ────────────────────────────────────────────────────────────────────────────
func ComputeStats(sessionsDir string, cache *StatsCacheV3) (*AggregatedStats, error) {
	allFiles, err := gatherSessionFiles(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("gather session files: %w", err)
	}

	if len(allFiles) == 0 {
		return buildAggregatedStats(emptyCache(), nil), nil
	}

	today := todayDateString()
	yesterday := yesterdayDateString()

	// ── Determine what needs to be (re-)processed ─────────────────────────────
	// Strategy (mirrors aggregateClaudeCodeStats in stats.ts):
	//   - Files with mtime newer than the cache's lastComputedDate are fresh.
	//   - Today's data is always re-processed live from disk.
	//   - Historical data (up to yesterday) is kept in the cache.

	var historicalFiles, todayFiles []string
	for _, f := range allFiles {
		fi, err := os.Stat(f)
		if err != nil {
			continue
		}
		mdate := dateStringFromTime(fi.ModTime())
		if mdate >= today {
			todayFiles = append(todayFiles, f)
		} else {
			// Only re-process if the file was modified after the cache's
			// lastComputedDate (i.e. cache is stale for that day).
			if cache.LastComputedDate == "" || mdate > cache.LastComputedDate {
				historicalFiles = append(historicalFiles, f)
			}
		}
	}

	// ── Process historical files not yet in cache ─────────────────────────────
	if len(historicalFiles) > 0 {
		hist, err := processFiles(historicalFiles, "", yesterday)
		if err == nil && (len(hist.sessions) > 0 || len(hist.activityByDate) > 0) {
			MergeIncrementalStats(
				cache,
				dailyActivitySlice(hist.activityByDate),
				dailyModelTokensSlice(hist.tokensByDate),
				hist.modelUsage,
				toCachedSessions(hist.sessions),
				hist.hourCounts,
				hist.speculationMs,
				yesterday,
			)
			_ = SaveStatsCache(cache)
		} else if cache.LastComputedDate == "" || cache.LastComputedDate < yesterday {
			cache.LastComputedDate = yesterday
			_ = SaveStatsCache(cache)
		}
	}

	// ── Process today's files live (never cached) ─────────────────────────────
	var todayProcessed *processedStats
	if len(todayFiles) > 0 {
		todayProcessed, err = processFiles(todayFiles, today, today)
		if err != nil {
			todayProcessed = nil
		}
	}

	return buildAggregatedStats(cache, todayProcessed), nil
}

// ────────────────────────────────────────────────────────────────────────────
// gatherSessionFiles returns all .jsonl files under sessionsDir, including
// subagent files (structure: {projectDir}/{sessionId}/subagents/agent-*.jsonl).
// ────────────────────────────────────────────────────────────────────────────
func gatherSessionFiles(sessionsDir string) ([]string, error) {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var files []string
	for _, e := range entries {
		projectDir := filepath.Join(sessionsDir, e.Name())
		if !e.IsDir() {
			continue
		}

		projectEntries, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}

		for _, pe := range projectEntries {
			full := filepath.Join(projectDir, pe.Name())
			if !pe.IsDir() {
				if strings.HasSuffix(pe.Name(), ".jsonl") {
					files = append(files, full)
				}
				continue
			}
			// Session subdirectory — look for subagents/agent-*.jsonl
			subagentsDir := filepath.Join(full, subagentsDirName)
			subs, err := os.ReadDir(subagentsDir)
			if err != nil {
				continue
			}
			for _, sub := range subs {
				if !sub.IsDir() &&
					strings.HasSuffix(sub.Name(), ".jsonl") &&
					strings.HasPrefix(sub.Name(), "agent-") {
					files = append(files, filepath.Join(subagentsDir, sub.Name()))
				}
			}
		}
	}
	return files, nil
}

// ────────────────────────────────────────────────────────────────────────────
// processFiles reads and accumulates stats from a list of .jsonl files.
// fromDate/toDate are inclusive YYYY-MM-DD bounds; empty string = no bound.
// ────────────────────────────────────────────────────────────────────────────
func processFiles(files []string, fromDate, toDate string) (*processedStats, error) {
	acc := newProcessedStats()

	for _, path := range files {
		if err := processOneFile(acc, path, fromDate, toDate); err != nil {
			// Non-fatal: skip bad files just like the TS version.
			continue
		}
	}
	return acc, nil
}

// processOneFile parses a single JSONL transcript and accumulates into acc.
func processOneFile(acc *processedStats, path, fromDate, toDate string) error {
	isSubagent := strings.Contains(path, string(os.PathSeparator)+subagentsDirName+string(os.PathSeparator))
	sessionID := strings.TrimSuffix(filepath.Base(path), ".jsonl")

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Read all entries.
	var allEntries []transcriptEntry
	scanner := bufio.NewScanner(f)
	// Increase buffer for large lines.
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var entry transcriptEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		allEntries = append(allEntries, entry)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// Collect transcript messages and speculation events.
	var messages []transcriptEntry
	for _, e := range allEntries {
		if e.Type == "speculation-accept" {
			acc.speculationMs += e.TimeSavedMs
			continue
		}
		if transcriptMessageTypes[e.Type] {
			messages = append(messages, e)
		}
	}

	if len(messages) == 0 {
		return nil
	}

	// For subagent files all messages are sidechain; we still count tokens.
	mainMessages := messages
	if !isSubagent {
		filtered := messages[:0]
		for _, m := range messages {
			if !m.IsSidechain {
				filtered = append(filtered, m)
			}
		}
		mainMessages = filtered
	}
	if len(mainMessages) == 0 {
		return nil
	}

	// Parse first/last timestamps.
	firstTime, err := time.Parse(time.RFC3339Nano, mainMessages[0].Timestamp)
	if err != nil {
		// Try without nanoseconds.
		firstTime, err = time.Parse(time.RFC3339, mainMessages[0].Timestamp)
		if err != nil {
			return nil // malformed timestamp
		}
	}
	lastTime, err := time.Parse(time.RFC3339Nano, mainMessages[len(mainMessages)-1].Timestamp)
	if err != nil {
		lastTime, err = time.Parse(time.RFC3339, mainMessages[len(mainMessages)-1].Timestamp)
		if err != nil {
			return nil
		}
	}

	dateKey := dateStringFromTime(firstTime)

	// Apply date bounds.
	if fromDate != "" && dateKey < fromDate {
		return nil
	}
	if toDate != "" && dateKey > toDate {
		return nil
	}

	// ── Daily activity ────────────────────────────────────────────────────────
	activity, exists := acc.activityByDate[dateKey]
	if !exists {
		activity = &DailyActivity{Date: dateKey}
		acc.activityByDate[dateKey] = activity
	}

	if !isSubagent {
		durationMs := lastTime.Sub(firstTime).Milliseconds()

		acc.sessions = append(acc.sessions, SessionStats{
			SessionID:    sessionID,
			Duration:     durationMs,
			MessageCount: len(mainMessages),
			Timestamp:    mainMessages[0].Timestamp,
			Date:         dateKey,
		})

		acc.totalMessages += len(mainMessages)
		activity.SessionCount++
		activity.MessageCount += len(mainMessages)

		hour := fmt.Sprintf("%d", firstTime.Hour())
		acc.hourCounts[hour]++
	}

	if !isSubagent || exists {
		// (subagent tokens are attached to the existing day entry)
	}

	// ── Per-message processing ────────────────────────────────────────────────
	for _, msg := range mainMessages {
		if msg.Type != "assistant" {
			continue
		}
		m := msg.Message
		if m == nil {
			continue
		}

		// Tool calls in content array.
		if len(m.Content) > 0 {
			var blocks []contentBlock
			if err := json.Unmarshal(m.Content, &blocks); err == nil {
				for _, b := range blocks {
					if b.Type == "tool_use" {
						activity.ToolCallCount++
						if b.Name != "" {
							acc.toolCounts[b.Name]++
						}
					}
				}
			}
		}

		// Token usage.
		if m.Usage == nil || m.Model == syntheticModel || m.Model == "" {
			continue
		}
		model := m.Model

		// Daily tokens by model.
		dayTokens, ok := acc.tokensByDate[dateKey]
		if !ok {
			dayTokens = make(map[string]int)
			acc.tokensByDate[dateKey] = dayTokens
		}
		total := m.Usage.InputTokens + m.Usage.OutputTokens
		if total > 0 {
			dayTokens[model] += total
		}

		// Aggregate model usage.
		existing := acc.modelUsage[model]
		acc.modelUsage[model] = ModelStats{
			InputTokens:              existing.InputTokens + m.Usage.InputTokens,
			OutputTokens:             existing.OutputTokens + m.Usage.OutputTokens,
			CacheReadInputTokens:     existing.CacheReadInputTokens + m.Usage.CacheReadInputTokens,
			CacheCreationInputTokens: existing.CacheCreationInputTokens + m.Usage.CacheCreationInputTokens,
			RequestCount:             existing.RequestCount + 1,
		}
	}

	return nil
}

// ────────────────────────────────────────────────────────────────────────────
// buildAggregatedStats combines the on-disk cache with freshly-processed
// today data to produce the final AggregatedStats.
// ────────────────────────────────────────────────────────────────────────────
func buildAggregatedStats(cache *StatsCacheV3, today *processedStats) *AggregatedStats {
	// ── Merge daily activity ──────────────────────────────────────────────────
	actMap := make(map[string]DailyActivity, len(cache.DailyActivity))
	for _, d := range cache.DailyActivity {
		actMap[d.Date] = d
	}
	if today != nil {
		for _, d := range dailyActivitySlice(today.activityByDate) {
			if ex, ok := actMap[d.Date]; ok {
				ex.MessageCount += d.MessageCount
				ex.SessionCount += d.SessionCount
				ex.ToolCallCount += d.ToolCallCount
				actMap[d.Date] = ex
			} else {
				actMap[d.Date] = d
			}
		}
	}
	actSlice := make([]DailyActivity, 0, len(actMap))
	for _, d := range actMap {
		actSlice = append(actSlice, d)
	}
	sortDailyActivity(actSlice)

	// ── Merge daily model tokens ──────────────────────────────────────────────
	tokMap := make(map[string]map[string]int, len(cache.DailyModelTokens))
	for _, d := range cache.DailyModelTokens {
		cp := make(map[string]int, len(d.TokensByModel))
		for k, v := range d.TokensByModel {
			cp[k] = v
		}
		tokMap[d.Date] = cp
	}
	if today != nil {
		for date, byModel := range today.tokensByDate {
			if ex, ok := tokMap[date]; ok {
				for m, t := range byModel {
					ex[m] += t
				}
			} else {
				cp := make(map[string]int, len(byModel))
				for k, v := range byModel {
					cp[k] = v
				}
				tokMap[date] = cp
			}
		}
	}
	tokSlice := dailyModelTokensSlice(tokMap)
	sortDailyModelTokens(tokSlice)

	// ── Merge model usage ─────────────────────────────────────────────────────
	modelUsage := make(map[string]ModelStats, len(cache.ModelUsage))
	for m, u := range cache.ModelUsage {
		modelUsage[m] = u
	}
	if today != nil {
		for m, u := range today.modelUsage {
			if ex, ok := modelUsage[m]; ok {
				modelUsage[m] = ModelStats{
					InputTokens:              ex.InputTokens + u.InputTokens,
					OutputTokens:             ex.OutputTokens + u.OutputTokens,
					CacheReadInputTokens:     ex.CacheReadInputTokens + u.CacheReadInputTokens,
					CacheCreationInputTokens: ex.CacheCreationInputTokens + u.CacheCreationInputTokens,
					RequestCount:             ex.RequestCount + u.RequestCount,
				}
			} else {
				modelUsage[m] = u
			}
		}
	}

	// ── Hour counts ───────────────────────────────────────────────────────────
	hourMap := make(map[string]int, 24)
	for h, c := range cache.HourCounts {
		hourMap[h] += c
	}
	if today != nil {
		for h, c := range today.hourCounts {
			hourMap[h] += c
		}
	}

	// ── Session aggregates ────────────────────────────────────────────────────
	totalSessions := cache.TotalSessions
	totalMessages := cache.TotalMessages
	var longestSession *SessionStats
	if cache.LongestSession != nil {
		cp := SessionStats{
			SessionID:    cache.LongestSession.SessionID,
			Duration:     cache.LongestSession.Duration,
			MessageCount: cache.LongestSession.MessageCount,
			Timestamp:    cache.LongestSession.Timestamp,
		}
		longestSession = &cp
	}
	firstSessionDate := cache.FirstSessionDate
	lastSessionDate := ""

	if today != nil {
		totalSessions += len(today.sessions)
		totalMessages += today.totalMessages

		for _, s := range today.sessions {
			if longestSession == nil || s.Duration > longestSession.Duration {
				cp := s
				longestSession = &cp
			}
			if firstSessionDate == "" || s.Timestamp < firstSessionDate {
				firstSessionDate = s.Timestamp
			}
			if lastSessionDate == "" || s.Timestamp > lastSessionDate {
				lastSessionDate = s.Timestamp
			}
		}
	}

	if lastSessionDate == "" && len(actSlice) > 0 {
		lastSessionDate = actSlice[len(actSlice)-1].Date
	}

	// ── Streaks ───────────────────────────────────────────────────────────────
	streaks := ComputeStreak(actSlice)

	// ── Peak activity day ─────────────────────────────────────────────────────
	peakDay := GetMostActiveDay(actSlice)

	// ── Peak activity hour ────────────────────────────────────────────────────
	peakHour := -1
	peakHourCount := -1
	for h, c := range hourMap {
		if c > peakHourCount {
			peakHourCount = c
			hInt := 0
			fmt.Sscanf(h, "%d", &hInt)
			peakHour = hInt
		}
	}

	// ── Total calendar days ───────────────────────────────────────────────────
	totalDays := 0
	if firstSessionDate != "" && lastSessionDate != "" {
		t1 := parseDateLoose(firstSessionDate)
		t2 := parseDateLoose(lastSessionDate)
		if !t1.IsZero() && !t2.IsZero() {
			diff := t2.Sub(t1)
			totalDays = int(diff.Hours()/24) + 1
		}
	}

	// ── Most-used model ───────────────────────────────────────────────────────
	mostUsedModel := ""
	maxRequests := 0
	for m, u := range modelUsage {
		if u.RequestCount > maxRequests {
			maxRequests = u.RequestCount
			mostUsedModel = m
		}
	}

	// ── Average session length ────────────────────────────────────────────────
	var avgSessionMs int64
	if today != nil && len(today.sessions) > 0 {
		var sum int64
		for _, s := range today.sessions {
			sum += s.Duration
		}
		avgSessionMs = sum / int64(len(today.sessions))
	}

	// ── Tool counts from today (cache doesn't store per-tool counts) ──────────
	mostUsedTools := make(map[string]int)
	if today != nil {
		for tool, count := range today.toolCounts {
			mostUsedTools[tool] = count
		}
	}

	return &AggregatedStats{
		TotalSessions:               totalSessions,
		TotalMessages:               totalMessages,
		TotalDays:                   totalDays,
		ActiveDays:                  len(actMap),
		Streaks:                     streaks,
		DailyActivity:               actSlice,
		DailyModelTokens:            tokSlice,
		ModelUsage:                  modelUsage,
		LongestSession:              longestSession,
		FirstSessionDate:            firstSessionDate,
		LastSessionDate:             lastSessionDate,
		PeakActivityDay:             peakDay,
		PeakActivityHour:            peakHour,
		MostUsedModel:               mostUsedModel,
		AverageSessionLengthMs:      avgSessionMs,
		MostUsedTools:               mostUsedTools,
		TotalSpeculationTimeSavedMs: cache.TotalSpeculationTimeSavedMs + todaySpeculation(today),
	}
}

func todaySpeculation(p *processedStats) int64 {
	if p == nil {
		return 0
	}
	return p.speculationMs
}

// ────────────────────────────────────────────────────────────────────────────
// ComputeStreak calculates current and longest consecutive-day streaks.
// Mirrors calculateStreaks() in stats.ts.
// ────────────────────────────────────────────────────────────────────────────
func ComputeStreak(activity []DailyActivity) StreakInfo {
	if len(activity) == 0 {
		return StreakInfo{}
	}

	// Build set of active dates.
	activeDates := make(map[string]bool, len(activity))
	for _, d := range activity {
		activeDates[d.Date] = true
	}

	today := dateStringFromTime(time.Now())

	// ── Current streak (walk backwards from today) ────────────────────────────
	var currentStreak int
	var currentStreakStart string
	checkDate := time.Now()
	for {
		ds := dateStringFromTime(checkDate)
		if !activeDates[ds] {
			break
		}
		currentStreak++
		currentStreakStart = ds
		checkDate = checkDate.AddDate(0, 0, -1)
	}
	_ = today // keep compiler happy

	// ── Longest streak ────────────────────────────────────────────────────────
	sortedDates := make([]string, 0, len(activeDates))
	for d := range activeDates {
		sortedDates = append(sortedDates, d)
	}
	sort.Strings(sortedDates)

	var longestStreak int
	var longestStreakStart, longestStreakEnd string

	if len(sortedDates) > 0 {
		tempStreak := 1
		tempStart := sortedDates[0]

		for i := 1; i < len(sortedDates); i++ {
			prev, _ := time.Parse("2006-01-02", sortedDates[i-1])
			curr, _ := time.Parse("2006-01-02", sortedDates[i])
			diff := int(curr.Sub(prev).Hours() / 24)

			if diff == 1 {
				tempStreak++
			} else {
				if tempStreak > longestStreak {
					longestStreak = tempStreak
					longestStreakStart = tempStart
					longestStreakEnd = sortedDates[i-1]
				}
				tempStreak = 1
				tempStart = sortedDates[i]
			}
		}
		// Final segment.
		if tempStreak > longestStreak {
			longestStreak = tempStreak
			longestStreakStart = tempStart
			longestStreakEnd = sortedDates[len(sortedDates)-1]
		}
	}

	return StreakInfo{
		CurrentStreak:      currentStreak,
		LongestStreak:      longestStreak,
		CurrentStreakStart: currentStreakStart,
		LongestStreakStart: longestStreakStart,
		LongestStreakEnd:   longestStreakEnd,
	}
}

// GetMostActiveDay returns the YYYY-MM-DD date with the highest message count.
func GetMostActiveDay(activity []DailyActivity) string {
	var best string
	bestCount := -1
	for _, d := range activity {
		if d.MessageCount > bestCount {
			bestCount = d.MessageCount
			best = d.Date
		}
	}
	return best
}

// ComputeDailyBreakdown returns activity sorted by date as DayBreakdown rows.
func ComputeDailyBreakdown(stats *AggregatedStats) []DayBreakdown {
	breakdown := make([]DayBreakdown, 0, len(stats.DailyActivity))
	for _, d := range stats.DailyActivity {
		breakdown = append(breakdown, DayBreakdown{
			Date:          d.Date,
			SessionCount:  d.SessionCount,
			MessageCount:  d.MessageCount,
			ToolCallCount: d.ToolCallCount,
		})
	}
	// DailyActivity is already sorted by date.
	return breakdown
}

// ────────────────────────────────────────────────────────────────────────────
// FormatStatsSummary renders a human-readable /stats summary.
// ────────────────────────────────────────────────────────────────────────────
func FormatStatsSummary(stats *AggregatedStats) string {
	var sb strings.Builder

	sb.WriteString("## Estatísticas de uso\n\n")

	// Overview
	fmt.Fprintf(&sb, "- **Sessões totais**: %d\n", stats.TotalSessions)
	fmt.Fprintf(&sb, "- **Mensagens totais**: %d\n", stats.TotalMessages)
	fmt.Fprintf(&sb, "- **Dias ativos**: %d\n", stats.ActiveDays)

	// Streaks
	sb.WriteString("\n### Sequências\n\n")
	fmt.Fprintf(&sb, "- **Sequência atual**: %d dia(s)", stats.Streaks.CurrentStreak)
	if stats.Streaks.CurrentStreakStart != "" {
		fmt.Fprintf(&sb, " (desde %s)", stats.Streaks.CurrentStreakStart)
	}
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "- **Maior sequência**: %d dia(s)", stats.Streaks.LongestStreak)
	if stats.Streaks.LongestStreakStart != "" && stats.Streaks.LongestStreakEnd != "" {
		fmt.Fprintf(&sb, " (%s → %s)", stats.Streaks.LongestStreakStart, stats.Streaks.LongestStreakEnd)
	}
	sb.WriteString("\n")

	// Model usage
	if len(stats.ModelUsage) > 0 {
		sb.WriteString("\n### Uso por modelo\n\n")
		type modelRow struct {
			name  string
			usage ModelStats
		}
		rows := make([]modelRow, 0, len(stats.ModelUsage))
		for m, u := range stats.ModelUsage {
			rows = append(rows, modelRow{m, u})
		}
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].usage.RequestCount > rows[j].usage.RequestCount
		})
		sb.WriteString("| Modelo | Requisições | Tokens entrada | Tokens saída |\n")
		sb.WriteString("|--------|-------------|----------------|--------------|\n")
		for _, r := range rows {
			fmt.Fprintf(&sb, "| %s | %d | %d | %d |\n",
				r.name, r.usage.RequestCount, r.usage.InputTokens, r.usage.OutputTokens)
		}
	}

	// Peak activity
	if stats.PeakActivityDay != "" {
		fmt.Fprintf(&sb, "\n- **Dia mais ativo**: %s\n", stats.PeakActivityDay)
	}
	if stats.PeakActivityHour >= 0 {
		fmt.Fprintf(&sb, "- **Hora de pico**: %02d:00\n", stats.PeakActivityHour)
	}

	// Most used model
	if stats.MostUsedModel != "" {
		fmt.Fprintf(&sb, "- **Modelo mais usado**: %s\n", stats.MostUsedModel)
	}

	// Session dates
	if stats.FirstSessionDate != "" {
		fmt.Fprintf(&sb, "\n- **Primeira sessão**: %s\n", stats.FirstSessionDate[:10])
	}
	if stats.LastSessionDate != "" {
		last := stats.LastSessionDate
		if len(last) > 10 {
			last = last[:10]
		}
		fmt.Fprintf(&sb, "- **Última sessão**: %s\n", last)
	}

	// Average session length
	if stats.AverageSessionLengthMs > 0 {
		d := time.Duration(stats.AverageSessionLengthMs) * time.Millisecond
		fmt.Fprintf(&sb, "- **Duração média de sessão**: %s\n", formatDur(d))
	}

	// Longest session
	if stats.LongestSession != nil {
		d := time.Duration(stats.LongestSession.Duration) * time.Millisecond
		fmt.Fprintf(&sb, "- **Sessão mais longa**: %s (ID: %s)\n",
			formatDur(d), stats.LongestSession.SessionID)
	}

	// Recent activity (last 7 days)
	if len(stats.DailyActivity) > 0 {
		sb.WriteString("\n### Atividade recente (7 dias)\n\n")
		sb.WriteString("| Data | Sessões | Mensagens | Tool calls |\n")
		sb.WriteString("|------|---------|-----------|------------|\n")
		start := len(stats.DailyActivity) - 7
		if start < 0 {
			start = 0
		}
		for _, d := range stats.DailyActivity[start:] {
			fmt.Fprintf(&sb, "| %s | %d | %d | %d |\n",
				d.Date, d.SessionCount, d.MessageCount, d.ToolCallCount)
		}
	}

	return sb.String()
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func dateStringFromTime(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

func todayDateString() string {
	return dateStringFromTime(time.Now())
}

func yesterdayDateString() string {
	return dateStringFromTime(time.Now().AddDate(0, 0, -1))
}

// parseDateLoose tries RFC3339 first, then YYYY-MM-DD.
func parseDateLoose(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	return time.Time{}
}

func formatDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func toCachedSessions(sessions []SessionStats) []CachedSessionStats {
	out := make([]CachedSessionStats, len(sessions))
	for i, s := range sessions {
		out[i] = CachedSessionStats{
			SessionID:    s.SessionID,
			Duration:     s.Duration,
			MessageCount: s.MessageCount,
			Timestamp:    s.Timestamp,
		}
	}
	return out
}

func dailyActivitySlice(m map[string]*DailyActivity) []DailyActivity {
	out := make([]DailyActivity, 0, len(m))
	for _, d := range m {
		out = append(out, *d)
	}
	sortDailyActivity(out)
	return out
}

func dailyModelTokensSlice(m map[string]map[string]int) []DailyModelTokens {
	out := make([]DailyModelTokens, 0, len(m))
	for date, byModel := range m {
		cp := make(map[string]int, len(byModel))
		for k, v := range byModel {
			cp[k] = v
		}
		out = append(out, DailyModelTokens{Date: date, TokensByModel: cp})
	}
	sortDailyModelTokens(out)
	return out
}

func sortDailyActivity(s []DailyActivity) {
	sort.Slice(s, func(i, j int) bool { return s[i].Date < s[j].Date })
}

func sortDailyModelTokens(s []DailyModelTokens) {
	sort.Slice(s, func(i, j int) bool { return s[i].Date < s[j].Date })
}
