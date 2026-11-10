package swarm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RunQuery selects a page of recorded team runs, newest created run first.
// Team empty means every team. Limit 0 uses the default page size.
type RunQuery struct {
	Team   string
	Limit  int
	Cursor string
}

// RunPage is one page of archived runs. NextCursor is empty on the last page.
type RunPage struct {
	Runs       []ArchivedTeam
	NextCursor string
}

// RetentionPolicy controls explicit journal pruning. A zero policy, and any
// policy with Apply false, deletes nothing. KeepRuns below 1 still retains
// the latest run of each team. MaxAge above zero also requires the winning
// created event to be older than that age. MaxAge zero does not apply an age floor.
type RetentionPolicy struct {
	Apply    bool
	KeepRuns int
	MaxAge   time.Duration
}

// PruneReport counts what an explicit prune changed. Leader messages that were
// never acknowledged, open turns, unknown event kinds, and any run that still
// has a record in a live or truncated file stay on disk.
type PruneReport struct {
	FilesRewritten   int
	RunsDropped      int
	RunsKept         int
	BytesFreed       int64
	SkippedLive      int
	SkippedTruncated int
	SkippedUnproven  int
}

type runKey struct {
	team string
	run  string
}

type runRef struct {
	team string
	run  string
	file string
	off  int64
	at   time.Time
}

type historyCursor struct {
	V    int    `json:"v"`
	At   int64  `json:"at"`
	File string `json:"file"`
	Off  int64  `json:"off"`
	Team string `json:"team"`
	Run  string `json:"run"`
}

// History lists created runs newest first and materializes only the requested
// page. It uses the same fold as Teams, including interrupted and running turns.
func (j *TeamJournal) History(query RunQuery) (RunPage, error) {
	if j == nil {
		return RunPage{}, nil
	}
	limit, err := normalizeHistoryLimit(query.Limit)
	if err != nil {
		return RunPage{}, err
	}
	cursor, err := decodeHistoryCursor(query.Cursor)
	if err != nil {
		return RunPage{}, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt == 1 {
			j.verified = nil
		}
		page, err := j.historyLocked(strings.TrimSpace(query.Team), limit, cursor, attempt == 0)
		if err == nil {
			return page, nil
		}
		if !errors.Is(err, errIndexMismatch) {
			return RunPage{}, err
		}
		last = err
	}
	return RunPage{}, fmt.Errorf("reading team journal: %w", last)
}

func normalizeHistoryLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultHistoryLimit, nil
	}
	if limit < 0 || limit > maxHistoryLimit {
		return 0, errors.New("team journal history limit must be between 1 and 100")
	}
	return limit, nil
}

func (j *TeamJournal) historyLocked(team string, limit int, cursor *runRef, trust bool) (RunPage, error) {
	files, err := j.listFiles(trust, false)
	if err != nil {
		return RunPage{}, err
	}
	runs, err := j.createdRuns(files, team)
	if err != nil {
		return RunPage{}, err
	}
	start := 0
	if cursor != nil {
		for start < len(runs) && !olderRun(runs[start], *cursor) {
			start++
		}
	}
	if start >= len(runs) {
		return RunPage{}, nil
	}
	page := RunPage{}
	next := start
	for next < len(runs) && len(page.Runs) < limit {
		end := next + limit - len(page.Runs)
		if end > len(runs) {
			end = len(runs)
		}
		batch := runs[next:end]
		wanted := make(map[runKey]struct{}, len(batch))
		for _, run := range batch {
			wanted[runKey{team: run.team, run: run.run}] = struct{}{}
		}
		records, err := j.locatedRecords(files, func(entry indexEntry) bool {
			_, ok := wanted[runKey{team: entry.Team, run: entry.Run}]
			return !entry.Gap && ok
		})
		if err != nil {
			return RunPage{}, err
		}
		grouped := make(map[runKey][]journalRecord, len(batch))
		for _, record := range records {
			key := runKey{team: record.record.event.Team, run: record.record.event.RunID}
			grouped[key] = append(grouped[key], record.record)
		}
		for _, run := range batch {
			// A created event with no summary is not an ArchivedTeam. Skip
			// it and fill the page from a later batch if necessary.
			folded := archivedTeams(grouped[runKey{team: run.team, run: run.run}])
			if len(folded) > 0 {
				page.Runs = append(page.Runs, folded...)
			}
		}
		next = end
	}
	if next < len(runs) {
		encoded, err := encodeHistoryCursor(runs[next-1])
		if err != nil {
			return RunPage{}, err
		}
		page.NextCursor = encoded
	}
	return page, nil
}

func (j *TeamJournal) createdRuns(files []journalFile, team string) ([]runRef, error) {
	winners := make(map[runKey]runRef)
	for _, file := range files {
		for _, entry := range file.entries {
			if entry.Gap || entry.Kind != "created" || (team != "" && entry.Team != team) {
				continue
			}
			key := runKey{team: entry.Team, run: entry.Run}
			ref := runRef{team: key.team, run: key.run, file: filepath.Base(file.path), off: entry.Off, at: time.Unix(0, entry.At)}
			if old, ok := winners[key]; !ok || newerRun(ref, old) {
				winners[key] = ref
			}
		}
	}
	runs := make([]runRef, 0, len(winners))
	for _, ref := range winners {
		runs = append(runs, ref)
	}
	sort.SliceStable(runs, func(i, k int) bool { return newerRun(runs[i], runs[k]) })
	return runs, nil
}

func newerRun(a, b runRef) bool {
	if !a.at.Equal(b.at) {
		return a.at.After(b.at)
	}
	if a.file != b.file {
		return a.file > b.file
	}
	if a.off != b.off {
		return a.off > b.off
	}
	if a.team != b.team {
		return a.team > b.team
	}
	return a.run > b.run
}

func olderRun(item, cursor runRef) bool {
	return !newerRun(item, cursor) && !sameRun(item, cursor)
}

func sameRun(a, b runRef) bool {
	return a.at.Equal(b.at) && a.file == b.file && a.off == b.off && a.team == b.team && a.run == b.run
}

func encodeHistoryCursor(run runRef) (string, error) {
	data, err := json.Marshal(historyCursor{
		V: 1, At: run.at.UnixNano(), File: run.file, Off: run.off, Team: run.team, Run: run.run,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeHistoryCursor(value string) (*runRef, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, errors.New("team journal history cursor is invalid")
	}
	var cursor historyCursor
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.V != 1 {
		return nil, errors.New("team journal history cursor is invalid")
	}
	ref := runRef{
		team: cursor.Team, run: cursor.Run, file: cursor.File, off: cursor.Off,
		at: time.Unix(0, cursor.At),
	}
	return &ref, nil
}

// Prune drops settled runs outside the retention window. It never rewrites the
// process's own file, a file whose owner is live, a file whose lock is gone,
// or a file with a truncated tail. Unacknowledged leader messages and turns
// without a matching finish pin the whole run in every file.
func (j *TeamJournal) Prune(policy RetentionPolicy) (PruneReport, error) {
	if j == nil || !policy.Apply {
		return PruneReport{}, nil
	}
	if policy.KeepRuns < 0 {
		return PruneReport{}, errors.New("team journal retention count is negative")
	}
	if policy.MaxAge < 0 {
		return PruneReport{}, errors.New("team journal retention age is negative")
	}
	release, held, err := j.tryRetentionLock()
	if err != nil {
		return PruneReport{}, err
	}
	if !held {
		return PruneReport{}, errors.New("team journal prune already in progress")
	}
	defer release()
	j.mu.Lock()
	defer j.mu.Unlock()
	report, err := j.pruneLocked(policy, true)
	if errors.Is(err, errIndexMismatch) {
		j.verified = nil
		return j.pruneLocked(policy, false)
	}
	return report, err
}

func (j *TeamJournal) pruneLocked(policy RetentionPolicy, trust bool) (PruneReport, error) {
	files, err := j.listFiles(trust, true)
	if err != nil {
		return PruneReport{}, err
	}
	runs, err := j.createdRuns(files, "")
	if err != nil {
		return PruneReport{}, err
	}
	keepN := policy.KeepRuns
	if keepN < 1 {
		keepN = 1
	}
	var cutoff time.Time
	applyAge := policy.MaxAge > 0
	if applyAge {
		cutoff = time.Now().Add(-policy.MaxAge)
	}
	kept := make(map[runKey]bool, len(runs))
	perTeam := make(map[string]int)
	candidates := make([]runRef, 0)
	for _, run := range runs {
		key := runKey{team: run.team, run: run.run}
		perTeam[run.team]++
		if perTeam[run.team] <= keepN || (applyAge && !run.at.Before(cutoff)) {
			kept[key] = true
			continue
		}
		candidates = append(candidates, run)
	}
	report := PruneReport{}
	skippedLive := make(map[string]bool)
	skippedTruncated := make(map[string]bool)
	skippedUnproven := make(map[string]bool)
	drop := make(map[runKey]bool)
	for _, run := range candidates {
		key := runKey{team: run.team, run: run.run}
		if pinProtectedRun(files, key, j.path, skippedLive, skippedTruncated, skippedUnproven) {
			kept[key] = true
			continue
		}
		records, err := j.locatedRecords(files, func(entry indexEntry) bool {
			return !entry.Gap && entry.Team == key.team && entry.Run == key.run
		})
		if err != nil {
			return PruneReport{}, err
		}
		if len(records) == 0 || runBlocked(records) {
			kept[key] = true
			continue
		}
		drop[key] = true
	}
	plans, err := j.planRewrites(files, drop)
	if err != nil {
		discardRewritePlans(plans)
		return PruneReport{}, err
	}
	report.SkippedLive = len(skippedLive)
	report.SkippedTruncated = len(skippedTruncated)
	report.SkippedUnproven = len(skippedUnproven)
	for i := range plans {
		plan := &plans[i]
		// The sidecar is only a cache. Remove it before replacing JSONL so
		// a crash or publication failure cannot leave a trusted stale index.
		if err := os.Remove(journalIndexPath(plan.path)); err != nil && !errors.Is(err, os.ErrNotExist) {
			discardRewritePlans(plans)
			return report, fmt.Errorf("removing team journal index: %w", err)
		}
		if err := replaceJournalFile(plan.tmpJSONL, plan.path); err != nil {
			discardRewritePlans(plans)
			return report, err
		}
		plan.tmpJSONL = ""
		if err := syncTeamJournalDir(j.dir); err != nil {
			// The JSONL replacement already occurred. Keep the report honest.
			report.FilesRewritten++
			report.BytesFreed += plan.oldSize - plan.newSize
			discardRewritePlans(plans)
			return report, fmt.Errorf("syncing team journal directory: %w", err)
		}
		if err := publishJournalIndex(plan.path, plan.entries); err != nil {
			j.warnIndex(err)
		}
		delete(j.verified, plan.path)
		report.FilesRewritten++
		report.BytesFreed += plan.oldSize - plan.newSize
	}
	report.RunsDropped = len(drop)
	report.RunsKept = len(runs) - len(drop)
	return report, nil
}

func pinProtectedRun(files []journalFile, key runKey, ownPath string, live, truncated, unproven map[string]bool) bool {
	blocked := false
	for i := range files {
		meta := &files[i]
		if !fileHasRun(meta, key) {
			continue
		}
		switch protectedReason(meta, ownPath) {
		case "live":
			live[meta.path] = true
			blocked = true
		case "unproven":
			unproven[meta.path] = true
			blocked = true
		case "truncated":
			truncated[meta.path] = true
			blocked = true
		}
	}
	return blocked
}

func protectedReason(meta *journalFile, ownPath string) string {
	if meta.path == ownPath || meta.live {
		return "live"
	}
	if meta.lockMissing {
		return "unproven"
	}
	if meta.truncated || !entriesCover(meta.entries, meta.size) {
		return "truncated"
	}
	return ""
}

func entriesCover(entries []indexEntry, size int64) bool {
	end, ok := indexCoverage(entries)
	return ok && end == size
}

func fileHasRun(meta *journalFile, key runKey) bool {
	for _, entry := range meta.entries {
		if !entry.Gap && entry.Team == key.team && entry.Run == key.run {
			return true
		}
	}
	return false
}

func knownJournalKind(kind string) bool {
	switch kind {
	case "created", "state", "stopping", "member", "member_removed", "message", "ack", "turn_started", "turn_finished", "resumed", "turn_resumed":
		return true
	default:
		return false
	}
}

// runBlocked reports evidence that must survive an explicit prune. It uses
// owner-file offsets for terminal state: timestamps can collide or move
// backwards across processes. A different file may only add acknowledgements.
func runBlocked(records []locatedRecord) bool {
	var ownerFile string
	for _, record := range records {
		if record.record.event.Kind != "created" {
			continue
		}
		if ownerFile != "" && ownerFile != record.file {
			return true
		}
		ownerFile = record.file
	}
	if ownerFile == "" {
		return true
	}
	messages := make(map[string]struct{})
	acks := make(map[string]struct{})
	open := make(map[string][]int)
	var lastOwnerEvent journalEvent
	for _, record := range records {
		event := record.record.event
		if !knownJournalKind(event.Kind) {
			return true
		}
		if record.file != ownerFile && event.Kind != "ack" {
			return true
		}
		if record.file == ownerFile && event.Kind != "ack" {
			lastOwnerEvent = event
		}
		switch event.Kind {
		case "message":
			if event.Message == nil || event.Message.ID == "" {
				return true
			}
			if event.Message != nil && event.Message.Target == TeamLeaderName {
				if _, duplicate := messages[event.Message.ID]; duplicate {
					return true
				}
				messages[event.Message.ID] = struct{}{}
			}
		case "ack":
			for _, id := range event.IDs {
				acks[id] = struct{}{}
			}
		case "turn_started":
			open[event.Agent] = append(open[event.Agent], event.Turn)
		case "turn_finished":
			turns := open[event.Agent]
			for i := len(turns) - 1; i >= 0; i-- {
				if turns[i] == event.Turn {
					open[event.Agent] = append(turns[:i], turns[i+1:]...)
					break
				}
			}
		}
	}
	if lastOwnerEvent.Kind != "state" || lastOwnerEvent.Summary == nil || lastOwnerEvent.Summary.State != TeamStateStopped.String() {
		return true
	}
	for id := range messages {
		if _, ok := acks[id]; !ok {
			return true
		}
	}
	for _, turns := range open {
		if len(turns) > 0 {
			return true
		}
	}
	return false
}

type rewritePlan struct {
	path     string
	oldSize  int64
	newSize  int64
	tmpJSONL string
	entries  []indexEntry
}

func (j *TeamJournal) planRewrites(files []journalFile, drop map[runKey]bool) ([]rewritePlan, error) {
	var plans []rewritePlan
	for i := range files {
		meta := &files[i]
		if protectedReason(meta, j.path) != "" {
			continue
		}
		if !fileDrops(meta, drop) {
			continue
		}
		plan, err := j.stageRewrite(meta, drop)
		if err != nil {
			discardRewritePlans(plans)
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func fileDrops(meta *journalFile, drop map[runKey]bool) bool {
	for _, entry := range meta.entries {
		if entry.Gap {
			continue
		}
		if drop[runKey{team: entry.Team, run: entry.Run}] {
			return true
		}
	}
	return false
}

func (j *TeamJournal) stageRewrite(meta *journalFile, drop map[runKey]bool) (rewritePlan, error) {
	handle, err := os.Open(meta.path)
	if err != nil {
		return rewritePlan{}, err
	}
	defer handle.Close()
	jsonl, err := os.CreateTemp(j.dir, ".journal-tmp-*")
	if err != nil {
		return rewritePlan{}, err
	}
	plan := rewritePlan{path: meta.path, oldSize: meta.size, tmpJSONL: jsonl.Name()}
	success := false
	defer func() {
		if !success {
			_ = os.Remove(plan.tmpJSONL)
		}
	}()
	var offset int64
	for _, entry := range meta.entries {
		buf, err := readEntryBytes(handle, entry)
		if err != nil {
			_ = jsonl.Close()
			return rewritePlan{}, err
		}
		if !entry.Gap {
			var event journalEvent
			if err := json.Unmarshal(bytes.TrimSpace(buf), &event); err != nil {
				_ = jsonl.Close()
				return rewritePlan{}, fmt.Errorf("decoding team journal: %w", err)
			}
			if event.Version != teamJournalVersion {
				_ = jsonl.Close()
				return rewritePlan{}, fmt.Errorf("unsupported team journal version %d", event.Version)
			}
			if event.Kind != entry.Kind || event.Team != entry.Team || event.RunID != entry.Run || event.At.UnixNano() != entry.At {
				_ = jsonl.Close()
				return rewritePlan{}, errIndexMismatch
			}
		}
		if !entry.Gap && drop[runKey{team: entry.Team, run: entry.Run}] {
			continue
		}
		if _, err := jsonl.Write(buf); err != nil {
			_ = jsonl.Close()
			return rewritePlan{}, err
		}
		shifted := entry
		shifted.Off = offset
		shifted.End = offset + entry.Len
		plan.entries = append(plan.entries, stampEntry(shifted))
		offset += entry.Len
	}
	if err := jsonl.Sync(); err != nil {
		_ = jsonl.Close()
		return rewritePlan{}, err
	}
	if err := jsonl.Close(); err != nil {
		return rewritePlan{}, err
	}
	plan.newSize = offset
	success = true
	return plan, nil
}

func discardRewritePlans(plans []rewritePlan) {
	for i := range plans {
		if plans[i].tmpJSONL != "" {
			_ = os.Remove(plans[i].tmpJSONL)
		}
	}
}
