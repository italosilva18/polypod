package stats

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── ComputeStreak ──────────────────────────────────────────────────────────

func TestComputeStreak_Empty(t *testing.T) {
	got := ComputeStreak(nil)
	if got.CurrentStreak != 0 || got.LongestStreak != 0 {
		t.Errorf("expected zero streak, got %+v", got)
	}
	if got.CurrentStreakStart != "" || got.LongestStreakStart != "" || got.LongestStreakEnd != "" {
		t.Errorf("expected empty start/end strings, got %+v", got)
	}
}

func TestComputeStreak_LongestWithGap(t *testing.T) {
	// Three consecutive days, gap, then two consecutive days in the past.
	// The longest run should be 3.
	activity := []DailyActivity{
		{Date: "2020-01-01"},
		{Date: "2020-01-02"},
		{Date: "2020-01-03"},
		{Date: "2020-01-10"},
		{Date: "2020-01-11"},
	}
	got := ComputeStreak(activity)
	if got.LongestStreak != 3 {
		t.Errorf("LongestStreak = %d, want 3", got.LongestStreak)
	}
	if got.LongestStreakStart != "2020-01-01" {
		t.Errorf("LongestStreakStart = %q, want 2020-01-01", got.LongestStreakStart)
	}
	if got.LongestStreakEnd != "2020-01-03" {
		t.Errorf("LongestStreakEnd = %q, want 2020-01-03", got.LongestStreakEnd)
	}
	// Current streak from "today" (real today) into the past — the dates are
	// in 2020 so there is no run ending today.
	if got.CurrentStreak != 0 {
		t.Errorf("CurrentStreak = %d, want 0 (dates in past)", got.CurrentStreak)
	}
}

func TestComputeStreak_FinalSegmentIsLongest(t *testing.T) {
	// The longest run is the final segment and must still be captured.
	activity := []DailyActivity{
		{Date: "2020-05-01"},
		{Date: "2020-05-03"},
		{Date: "2020-05-04"},
		{Date: "2020-05-05"},
		{Date: "2020-05-06"},
	}
	got := ComputeStreak(activity)
	if got.LongestStreak != 4 {
		t.Errorf("LongestStreak = %d, want 4", got.LongestStreak)
	}
	if got.LongestStreakStart != "2020-05-03" || got.LongestStreakEnd != "2020-05-06" {
		t.Errorf("longest start/end = %q/%q, want 2020-05-03/2020-05-06",
			got.LongestStreakStart, got.LongestStreakEnd)
	}
}

func TestComputeStreak_CurrentStreakIncludesToday(t *testing.T) {
	today := dateStringFromTime(time.Now())
	yesterday := dateStringFromTime(time.Now().AddDate(0, 0, -1))
	twoAgo := dateStringFromTime(time.Now().AddDate(0, 0, -2))

	activity := []DailyActivity{
		{Date: twoAgo},
		{Date: yesterday},
		{Date: today},
	}
	got := ComputeStreak(activity)
	if got.CurrentStreak != 3 {
		t.Errorf("CurrentStreak = %d, want 3", got.CurrentStreak)
	}
	if got.CurrentStreakStart != twoAgo {
		t.Errorf("CurrentStreakStart = %q, want %q", got.CurrentStreakStart, twoAgo)
	}
}

// ─── GetMostActiveDay ───────────────────────────────────────────────────────

func TestGetMostActiveDay(t *testing.T) {
	tests := []struct {
		name     string
		input    []DailyActivity
		expected string
	}{
		{"empty", nil, ""},
		{"single", []DailyActivity{{Date: "2024-01-01", MessageCount: 5}}, "2024-01-01"},
		{"pick max", []DailyActivity{
			{Date: "2024-01-01", MessageCount: 2},
			{Date: "2024-01-02", MessageCount: 10},
			{Date: "2024-01-03", MessageCount: 7},
		}, "2024-01-02"},
		{"ties pick first", []DailyActivity{
			{Date: "2024-01-01", MessageCount: 5},
			{Date: "2024-01-02", MessageCount: 5},
		}, "2024-01-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetMostActiveDay(tt.input)
			if got != tt.expected {
				t.Errorf("GetMostActiveDay = %q, want %q", got, tt.expected)
			}
		})
	}
}

// ─── ComputeDailyBreakdown ──────────────────────────────────────────────────

func TestComputeDailyBreakdown(t *testing.T) {
	stats := &AggregatedStats{
		DailyActivity: []DailyActivity{
			{Date: "2024-01-01", SessionCount: 1, MessageCount: 5, ToolCallCount: 2},
			{Date: "2024-01-02", SessionCount: 3, MessageCount: 9, ToolCallCount: 4},
		},
	}
	got := ComputeDailyBreakdown(stats)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Date != "2024-01-01" || got[0].SessionCount != 1 || got[0].MessageCount != 5 || got[0].ToolCallCount != 2 {
		t.Errorf("row 0 = %+v", got[0])
	}
	if got[1].Date != "2024-01-02" || got[1].SessionCount != 3 || got[1].MessageCount != 9 || got[1].ToolCallCount != 4 {
		t.Errorf("row 1 = %+v", got[1])
	}
}

// ─── FormatStatsSummary ─────────────────────────────────────────────────────

func TestFormatStatsSummary_Basic(t *testing.T) {
	stats := &AggregatedStats{
		TotalSessions: 7,
		TotalMessages: 42,
		ActiveDays:    3,
		Streaks: StreakInfo{
			CurrentStreak:      2,
			LongestStreak:      5,
			CurrentStreakStart: "2024-03-01",
			LongestStreakStart: "2024-02-10",
			LongestStreakEnd:   "2024-02-14",
		},
		ModelUsage: map[string]ModelStats{
			"claude-3-opus":  {InputTokens: 100, OutputTokens: 200, RequestCount: 3},
			"claude-3-haiku": {InputTokens: 10, OutputTokens: 20, RequestCount: 1},
		},
		PeakActivityDay:        "2024-03-01",
		PeakActivityHour:       14,
		MostUsedModel:          "claude-3-opus",
		FirstSessionDate:       "2024-02-01T10:00:00Z",
		LastSessionDate:        "2024-03-01T18:00:00Z",
		AverageSessionLengthMs: 90 * 1000, // 1m30s
		LongestSession: &SessionStats{
			SessionID: "sess-xyz",
			Duration:  3725 * 1000, // 1h2m5s
		},
		DailyActivity: []DailyActivity{
			{Date: "2024-02-29", SessionCount: 1, MessageCount: 4, ToolCallCount: 2},
			{Date: "2024-03-01", SessionCount: 2, MessageCount: 6, ToolCallCount: 3},
		},
	}

	out := FormatStatsSummary(stats)

	wantContains := []string{
		"Estatísticas de uso",
		"Sessões totais**: 7",
		"Mensagens totais**: 42",
		"Dias ativos**: 3",
		"Sequência atual**: 2",
		"desde 2024-03-01",
		"Maior sequência**: 5",
		"2024-02-10 → 2024-02-14",
		"claude-3-opus",
		"claude-3-haiku",
		"Dia mais ativo**: 2024-03-01",
		"Hora de pico**: 14:00",
		"Modelo mais usado**: claude-3-opus",
		"Primeira sessão**: 2024-02-01",
		"Última sessão**: 2024-03-01",
		"sess-xyz",
		"Atividade recente",
	}
	for _, s := range wantContains {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q\n--- full ---\n%s", s, out)
		}
	}

	// Opus has more requests than haiku, so it must appear before haiku in
	// the "Uso por modelo" table.
	if oi, hi := strings.Index(out, "claude-3-opus"), strings.Index(out, "claude-3-haiku"); oi < 0 || hi < 0 || oi > hi {
		t.Errorf("expected claude-3-opus to appear before claude-3-haiku (oi=%d hi=%d)", oi, hi)
	}
}

func TestFormatStatsSummary_Empty(t *testing.T) {
	out := FormatStatsSummary(&AggregatedStats{})
	if !strings.Contains(out, "Estatísticas de uso") {
		t.Errorf("expected header, got %q", out)
	}
	if !strings.Contains(out, "Sessões totais**: 0") {
		t.Errorf("expected zero sessions line, got %q", out)
	}
	// No model table when ModelUsage is empty.
	if strings.Contains(out, "### Uso por modelo") {
		t.Errorf("did not expect model table for empty stats, got %q", out)
	}
}

// ─── formatDur ──────────────────────────────────────────────────────────────

func TestFormatDur(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{500 * time.Millisecond, "0s"},
		{30 * time.Second, "30s"},
		{59 * time.Second, "59s"},
		{time.Minute, "1m0s"},
		{90 * time.Second, "1m30s"},
		{59*time.Minute + 59*time.Second, "59m59s"},
		{time.Hour, "1h0m"},
		{2*time.Hour + 30*time.Minute, "2h30m"},
		{25 * time.Hour, "25h0m"},
	}
	for _, tt := range tests {
		got := formatDur(tt.in)
		if got != tt.want {
			t.Errorf("formatDur(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ─── date helpers ───────────────────────────────────────────────────────────

func TestDateStringFromTime(t *testing.T) {
	ts := time.Date(2024, 6, 15, 23, 59, 0, 0, time.UTC)
	got := dateStringFromTime(ts)
	if got != "2024-06-15" {
		t.Errorf("dateStringFromTime = %q, want 2024-06-15", got)
	}
}

func TestTodayAndYesterdayDateString(t *testing.T) {
	today := todayDateString()
	yesterday := yesterdayDateString()
	if today == "" || yesterday == "" {
		t.Fatalf("today/yesterday empty: %q %q", today, yesterday)
	}
	if today == yesterday {
		t.Errorf("today should differ from yesterday: %q", today)
	}
	// Parseable format.
	if _, err := time.Parse("2006-01-02", today); err != nil {
		t.Errorf("today not YYYY-MM-DD: %v", err)
	}
	if _, err := time.Parse("2006-01-02", yesterday); err != nil {
		t.Errorf("yesterday not YYYY-MM-DD: %v", err)
	}
}

func TestParseDateLoose(t *testing.T) {
	tests := []struct {
		in      string
		wantOK  bool
		wantISO string // expected UTC YYYY-MM-DD
	}{
		{"2024-06-15", true, "2024-06-15"},
		{"2024-06-15T12:00:00Z", true, "2024-06-15"},
		{"2024-06-15T12:00:00.123456789Z", true, "2024-06-15"},
		{"not-a-date", false, ""},
		{"", false, ""},
	}
	for _, tt := range tests {
		got := parseDateLoose(tt.in)
		if tt.wantOK {
			if got.IsZero() {
				t.Errorf("parseDateLoose(%q) returned zero time", tt.in)
				continue
			}
			if got.UTC().Format("2006-01-02") != tt.wantISO {
				t.Errorf("parseDateLoose(%q) = %v, want %q", tt.in, got, tt.wantISO)
			}
		} else {
			if !got.IsZero() {
				t.Errorf("parseDateLoose(%q) expected zero, got %v", tt.in, got)
			}
		}
	}
}

// ─── cache helpers ──────────────────────────────────────────────────────────

func TestEmptyCache(t *testing.T) {
	c := emptyCache()
	if c == nil {
		t.Fatal("emptyCache returned nil")
	}
	if c.Version != StatsCacheVersion {
		t.Errorf("Version = %d, want %d", c.Version, StatsCacheVersion)
	}
	if c.DailyActivity == nil || len(c.DailyActivity) != 0 {
		t.Errorf("DailyActivity = %v, want empty slice", c.DailyActivity)
	}
	if c.DailyModelTokens == nil || len(c.DailyModelTokens) != 0 {
		t.Errorf("DailyModelTokens = %v, want empty slice", c.DailyModelTokens)
	}
	if c.ModelUsage == nil {
		t.Error("ModelUsage is nil")
	}
	if c.HourCounts == nil {
		t.Error("HourCounts is nil")
	}
}

func TestMaxInt(t *testing.T) {
	cases := []struct{ a, b, want int }{
		{0, 0, 0},
		{5, 3, 5},
		{3, 5, 5},
		{-1, -2, -1},
		{-5, 10, 10},
	}
	for _, c := range cases {
		if got := maxInt(c.a, c.b); got != c.want {
			t.Errorf("maxInt(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// ─── MergeIncrementalStats ──────────────────────────────────────────────────

func TestMergeIncrementalStats_AddsNewDaysAndAccumulates(t *testing.T) {
	cache := emptyCache()
	// Pre-populate with one day and one model.
	cache.DailyActivity = []DailyActivity{
		{Date: "2024-01-01", MessageCount: 5, SessionCount: 1, ToolCallCount: 2},
	}
	cache.DailyModelTokens = []DailyModelTokens{
		{Date: "2024-01-01", TokensByModel: map[string]int{"opus": 100}},
	}
	cache.ModelUsage = map[string]ModelStats{
		"opus": {InputTokens: 50, OutputTokens: 50, RequestCount: 2, ContextWindow: 200_000},
	}
	cache.TotalSessions = 1
	cache.TotalMessages = 5
	cache.HourCounts = map[string]int{"10": 1}
	cache.FirstSessionDate = "2024-01-01T10:00:00Z"
	cache.LongestSession = &CachedSessionStats{
		SessionID: "s1", Duration: 60_000, MessageCount: 5, Timestamp: "2024-01-01T10:00:00Z",
	}

	newActivity := []DailyActivity{
		{Date: "2024-01-01", MessageCount: 3, SessionCount: 1, ToolCallCount: 1}, // same day
		{Date: "2024-01-02", MessageCount: 8, SessionCount: 2, ToolCallCount: 4}, // new day
	}
	newTokens := []DailyModelTokens{
		{Date: "2024-01-01", TokensByModel: map[string]int{"opus": 25, "haiku": 10}},
		{Date: "2024-01-02", TokensByModel: map[string]int{"opus": 50}},
	}
	newUsage := map[string]ModelStats{
		"opus":  {InputTokens: 10, OutputTokens: 20, RequestCount: 1, ContextWindow: 100_000},
		"haiku": {InputTokens: 5, OutputTokens: 7, RequestCount: 1},
	}
	newSessions := []CachedSessionStats{
		{SessionID: "s2", Duration: 120_000, MessageCount: 3, Timestamp: "2023-12-31T23:00:00Z"}, // earlier → updates FirstSessionDate
		{SessionID: "s3", Duration: 30_000, MessageCount: 8, Timestamp: "2024-01-02T09:00:00Z"},
	}
	newHours := map[string]int{"10": 2, "15": 1}

	MergeIncrementalStats(cache, newActivity, newTokens, newUsage, newSessions, newHours, 5000, "2024-01-02")

	// Daily activity: existing day merged, new day added, sorted.
	if len(cache.DailyActivity) != 2 {
		t.Fatalf("DailyActivity len = %d, want 2", len(cache.DailyActivity))
	}
	if cache.DailyActivity[0].Date != "2024-01-01" || cache.DailyActivity[0].MessageCount != 8 ||
		cache.DailyActivity[0].SessionCount != 2 || cache.DailyActivity[0].ToolCallCount != 3 {
		t.Errorf("day 0 = %+v, want merged day", cache.DailyActivity[0])
	}
	if cache.DailyActivity[1].Date != "2024-01-02" || cache.DailyActivity[1].MessageCount != 8 {
		t.Errorf("day 1 = %+v", cache.DailyActivity[1])
	}

	// Daily model tokens.
	if len(cache.DailyModelTokens) != 2 {
		t.Fatalf("DailyModelTokens len = %d, want 2", len(cache.DailyModelTokens))
	}
	if got := cache.DailyModelTokens[0].TokensByModel["opus"]; got != 125 {
		t.Errorf("day 0 opus tokens = %d, want 125", got)
	}
	if got := cache.DailyModelTokens[0].TokensByModel["haiku"]; got != 10 {
		t.Errorf("day 0 haiku tokens = %d, want 10", got)
	}
	if got := cache.DailyModelTokens[1].TokensByModel["opus"]; got != 50 {
		t.Errorf("day 1 opus tokens = %d, want 50", got)
	}

	// Model usage accumulated, max context window retained.
	opus := cache.ModelUsage["opus"]
	if opus.InputTokens != 60 || opus.OutputTokens != 70 || opus.RequestCount != 3 {
		t.Errorf("opus = %+v", opus)
	}
	if opus.ContextWindow != 200_000 {
		t.Errorf("opus ContextWindow = %d, want max(200000,100000)=200000", opus.ContextWindow)
	}
	if haiku, ok := cache.ModelUsage["haiku"]; !ok || haiku.RequestCount != 1 {
		t.Errorf("haiku = %+v ok=%v", haiku, ok)
	}

	// Hour counts.
	if cache.HourCounts["10"] != 3 || cache.HourCounts["15"] != 1 {
		t.Errorf("HourCounts = %v", cache.HourCounts)
	}

	// Sessions: totals and longest session update.
	if cache.TotalSessions != 3 {
		t.Errorf("TotalSessions = %d, want 3", cache.TotalSessions)
	}
	if cache.TotalMessages != 5+3+8 {
		t.Errorf("TotalMessages = %d, want 16", cache.TotalMessages)
	}
	if cache.LongestSession == nil || cache.LongestSession.SessionID != "s2" || cache.LongestSession.Duration != 120_000 {
		t.Errorf("LongestSession = %+v, want s2/120000", cache.LongestSession)
	}
	// FirstSessionDate should be updated to the earliest timestamp.
	if cache.FirstSessionDate != "2023-12-31T23:00:00Z" {
		t.Errorf("FirstSessionDate = %q, want 2023-12-31T23:00:00Z", cache.FirstSessionDate)
	}

	if cache.TotalSpeculationTimeSavedMs != 5000 {
		t.Errorf("TotalSpeculationTimeSavedMs = %d, want 5000", cache.TotalSpeculationTimeSavedMs)
	}
	if cache.LastComputedDate != "2024-01-02" {
		t.Errorf("LastComputedDate = %q, want 2024-01-02", cache.LastComputedDate)
	}
}

// ─── buildAggregatedStats (pure on cache, today=nil) ────────────────────────

func TestBuildAggregatedStats_CacheOnly(t *testing.T) {
	cache := emptyCache()
	cache.DailyActivity = []DailyActivity{
		{Date: "2024-01-01", MessageCount: 5, SessionCount: 1, ToolCallCount: 2},
		{Date: "2024-01-05", MessageCount: 10, SessionCount: 2, ToolCallCount: 4},
	}
	cache.DailyModelTokens = []DailyModelTokens{
		{Date: "2024-01-01", TokensByModel: map[string]int{"opus": 100}},
	}
	cache.ModelUsage = map[string]ModelStats{
		"opus":  {RequestCount: 5},
		"haiku": {RequestCount: 2},
	}
	cache.TotalSessions = 3
	cache.TotalMessages = 15
	cache.HourCounts = map[string]int{"10": 1, "14": 3, "9": 2}
	cache.FirstSessionDate = "2024-01-01T10:00:00Z"

	agg := buildAggregatedStats(cache, nil)

	if agg.TotalSessions != 3 || agg.TotalMessages != 15 {
		t.Errorf("totals = %d/%d, want 3/15", agg.TotalSessions, agg.TotalMessages)
	}
	if agg.ActiveDays != 2 {
		t.Errorf("ActiveDays = %d, want 2", agg.ActiveDays)
	}
	if agg.MostUsedModel != "opus" {
		t.Errorf("MostUsedModel = %q, want opus", agg.MostUsedModel)
	}
	if agg.PeakActivityDay != "2024-01-05" {
		t.Errorf("PeakActivityDay = %q, want 2024-01-05", agg.PeakActivityDay)
	}
	if agg.PeakActivityHour != 14 {
		t.Errorf("PeakActivityHour = %d, want 14", agg.PeakActivityHour)
	}
	// DailyActivity sorted.
	if len(agg.DailyActivity) != 2 || agg.DailyActivity[0].Date != "2024-01-01" ||
		agg.DailyActivity[1].Date != "2024-01-05" {
		t.Errorf("DailyActivity not sorted: %+v", agg.DailyActivity)
	}
	// totalDays is derived from a FirstSessionDate that has a time-of-day
	// component ("2024-01-01T10:00:00Z") and a LastSessionDate that comes
	// from the fallback to the last DailyActivity entry ("2024-01-05",
	// which parses at 00:00 UTC). Diff ≈ 3.58 days → floor+1 = 4.
	if agg.TotalDays != 4 {
		t.Errorf("TotalDays = %d, want 4", agg.TotalDays)
	}
	if agg.LastSessionDate != "2024-01-05" {
		t.Errorf("LastSessionDate = %q, want 2024-01-05", agg.LastSessionDate)
	}
}

func TestBuildAggregatedStats_EmptyCache(t *testing.T) {
	agg := buildAggregatedStats(emptyCache(), nil)
	if agg == nil {
		t.Fatal("nil aggregate")
	}
	if agg.TotalSessions != 0 || agg.TotalMessages != 0 || agg.ActiveDays != 0 {
		t.Errorf("expected zero aggregates, got %+v", agg)
	}
	if agg.PeakActivityHour != -1 {
		t.Errorf("PeakActivityHour = %d, want -1 (no hour data)", agg.PeakActivityHour)
	}
	if agg.MostUsedModel != "" {
		t.Errorf("MostUsedModel = %q, want empty", agg.MostUsedModel)
	}
}

// ─── Sort helpers ───────────────────────────────────────────────────────────

func TestSortDailyActivity(t *testing.T) {
	s := []DailyActivity{
		{Date: "2024-03-01"},
		{Date: "2024-01-15"},
		{Date: "2024-02-20"},
	}
	sortDailyActivity(s)
	want := []string{"2024-01-15", "2024-02-20", "2024-03-01"}
	for i, d := range s {
		if d.Date != want[i] {
			t.Errorf("index %d = %q, want %q", i, d.Date, want[i])
		}
	}
}

func TestSortDailyModelTokens(t *testing.T) {
	s := []DailyModelTokens{
		{Date: "2024-12-01"},
		{Date: "2024-11-01"},
	}
	sortDailyModelTokens(s)
	if s[0].Date != "2024-11-01" || s[1].Date != "2024-12-01" {
		t.Errorf("not sorted: %+v", s)
	}
}

// ─── Conversion helpers ─────────────────────────────────────────────────────

func TestToCachedSessions(t *testing.T) {
	in := []SessionStats{
		{SessionID: "a", Duration: 100, MessageCount: 2, Timestamp: "2024-01-01T00:00:00Z", Date: "2024-01-01"},
		{SessionID: "b", Duration: 200, MessageCount: 4, Timestamp: "2024-01-02T00:00:00Z", Date: "2024-01-02"},
	}
	out := toCachedSessions(in)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].SessionID != "a" || out[0].Duration != 100 || out[0].MessageCount != 2 || out[0].Timestamp != "2024-01-01T00:00:00Z" {
		t.Errorf("out[0] = %+v", out[0])
	}
	if out[1].SessionID != "b" || out[1].Duration != 200 {
		t.Errorf("out[1] = %+v", out[1])
	}
}

func TestDailyActivitySlice(t *testing.T) {
	m := map[string]*DailyActivity{
		"2024-02-01": {Date: "2024-02-01", MessageCount: 3},
		"2024-01-01": {Date: "2024-01-01", MessageCount: 1},
		"2024-03-01": {Date: "2024-03-01", MessageCount: 5},
	}
	out := dailyActivitySlice(m)
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}
	if out[0].Date != "2024-01-01" || out[1].Date != "2024-02-01" || out[2].Date != "2024-03-01" {
		t.Errorf("not sorted: %+v", out)
	}
}

func TestDailyModelTokensSlice(t *testing.T) {
	m := map[string]map[string]int{
		"2024-02-01": {"opus": 50, "haiku": 10},
		"2024-01-01": {"opus": 25},
	}
	out := dailyModelTokensSlice(m)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].Date != "2024-01-01" || out[1].Date != "2024-02-01" {
		t.Errorf("not sorted: %+v", out)
	}
	if out[0].TokensByModel["opus"] != 25 {
		t.Errorf("day 0 opus = %d, want 25", out[0].TokensByModel["opus"])
	}
	if out[1].TokensByModel["opus"] != 50 || out[1].TokensByModel["haiku"] != 10 {
		t.Errorf("day 1 tokens = %v", out[1].TokensByModel)
	}

	// Mutating the returned map must not affect the original.
	out[1].TokensByModel["opus"] = 999
	if m["2024-02-01"]["opus"] != 50 {
		t.Error("dailyModelTokensSlice did not deep-copy inner map")
	}
}

// ─── todaySpeculation ───────────────────────────────────────────────────────

func TestTodaySpeculation(t *testing.T) {
	if got := todaySpeculation(nil); got != 0 {
		t.Errorf("nil = %d, want 0", got)
	}
	p := &processedStats{speculationMs: 4242}
	if got := todaySpeculation(p); got != 4242 {
		t.Errorf("got %d, want 4242", got)
	}
}

// ─── ComputeStats on an empty (non-existent) directory ─────────────────────

func TestComputeStats_NonexistentDir(t *testing.T) {
	cache := emptyCache()
	agg, err := ComputeStats("/nonexistent/definitely/not/here/xyzzy", cache)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agg == nil {
		t.Fatal("nil aggregate")
	}
	if agg.TotalSessions != 0 || agg.TotalMessages != 0 {
		t.Errorf("expected empty aggregates, got %+v", agg)
	}
}

func TestComputeStats_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	cache := emptyCache()
	agg, err := ComputeStats(dir, cache)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agg == nil {
		t.Fatal("nil aggregate")
	}
	if agg.TotalSessions != 0 {
		t.Errorf("TotalSessions = %d, want 0", agg.TotalSessions)
	}
}

func TestGatherSessionFiles_Missing(t *testing.T) {
	files, err := gatherSessionFiles("/definitely/not/a/real/dir/for/polypod/stats")
	if err != nil {
		t.Errorf("unexpected error for missing dir: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected no files, got %v", files)
	}
}

func TestGatherSessionFiles_FindsJSONL(t *testing.T) {
	root := t.TempDir()

	// Create projectA/sess1.jsonl (direct file)
	projectA := filepath.Join(root, "projectA")
	if err := os.MkdirAll(projectA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectA, "sess1.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// Also a non-jsonl file that must be ignored.
	if err := os.WriteFile(filepath.Join(projectA, "notes.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// Create projectB/sess2/subagents/agent-1.jsonl
	subDir := filepath.Join(root, "projectB", "sess2", "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "agent-1.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// Subagent file not matching the agent- prefix must be ignored.
	if err := os.WriteFile(filepath.Join(subDir, "other.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := gatherSessionFiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("files = %v, want 2 entries", files)
	}
	foundSess1, foundAgent := false, false
	for _, f := range files {
		if strings.HasSuffix(f, "sess1.jsonl") {
			foundSess1 = true
		}
		if strings.HasSuffix(f, "agent-1.jsonl") {
			foundAgent = true
		}
	}
	if !foundSess1 || !foundAgent {
		t.Errorf("missing expected files: sess1=%v agent=%v files=%v", foundSess1, foundAgent, files)
	}
}
