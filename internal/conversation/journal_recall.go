package conversation

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

const journalSearchScanLimit = 2000

// JournalSearchResult is a bounded, newest-first page of original turns.
// NextBefore is a cursor for older rows; zero means the page was exhausted.
type JournalSearchResult struct {
	Entries    []JournalEntry
	Scanned    int
	NextBefore uint64
}

func validJournalScope(scope JournalScope) bool {
	return scope.SessionID != "" && scope.Channel != "" && scope.BranchID != "" && scope.AgentName != ""
}

func visibleJournalEntry(scope JournalScope, entry JournalEntry, branchLimits map[string]uint64) bool {
	if entry.SessionID != scope.SessionID || entry.Channel != scope.Channel {
		return false
	}
	if IsSyntheticMessage(entry.Message) {
		return false
	}
	// Old entries did not record branches or agents. They are available only
	// to the unbranched default agent; assigning them to a newer CLI branch
	// would expose turns that may belong to another branch.
	if entry.BranchID == "" {
		if scope.BranchID != scope.SessionID {
			return false
		}
	} else {
		last, allowed := branchLimits[entry.BranchID]
		if !allowed || entry.Sequence > last {
			return false
		}
	}
	if entry.AgentName == "" {
		return scope.AgentName == "default"
	}
	return entry.AgentName == scope.AgentName
}

// SearchJournal searches original journal content in the current conversation,
// branch and agent. It scans at most journalSearchScanLimit scoped rows per call
// so a sparse query cannot stall the agent indefinitely. Use NextBefore to
// continue toward older turns.
func (s *Store) SearchJournal(ctx context.Context, scope JournalScope, query string, before uint64, limit int) (JournalSearchResult, error) {
	if !validJournalScope(scope) {
		return JournalSearchResult{}, fmt.Errorf("conversation journal: unavailable request scope")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return JournalSearchResult{}, fmt.Errorf("conversation journal: search query is empty")
	}
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	if s.journalInitErr != nil {
		return JournalSearchResult{}, s.journalInitErr
	}
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	if err := s.loadJournal(ctx); err != nil {
		return JournalSearchResult{}, err
	}
	branchLimits, err := s.journalBranchLimitsLocked(ctx, scope)
	if err != nil {
		return JournalSearchResult{}, err
	}
	if s.sqliteDB != nil {
		return s.searchJournalSQLite(ctx, scope, branchLimits, query, before, limit)
	}
	return s.searchJournalMemory(ctx, scope, branchLimits, query, before, limit)
}

func (s *Store) searchJournalSQLite(ctx context.Context, scope JournalScope, branchLimits map[string]uint64, query string, before uint64, limit int) (JournalSearchResult, error) {
	maxSequence := int64(math.MaxInt64)
	if before > 0 && before <= math.MaxInt64 {
		maxSequence = int64(before)
	}
	rows, err := s.sqliteDB.QueryContext(ctx, `SELECT sequence, message_id, entry
		FROM conversation_journal
		WHERE json_extract(entry, '$.session_id') = ? AND sequence < ?
		ORDER BY sequence DESC LIMIT ?`, scope.SessionID, maxSequence, journalSearchScanLimit)
	if err != nil {
		return JournalSearchResult{}, fmt.Errorf("searching conversation journal: %w", err)
	}
	defer rows.Close()
	var result JournalSearchResult
	needle := strings.ToLower(query)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return JournalSearchResult{}, err
		}
		var sequence int64
		var messageID, raw string
		if err := rows.Scan(&sequence, &messageID, &raw); err != nil {
			return JournalSearchResult{}, fmt.Errorf("reading conversation journal search row: %w", err)
		}
		entry, err := decodeSQLiteEntry(sequence, messageID, raw)
		if err != nil {
			return JournalSearchResult{}, err
		}
		result.Scanned++
		result.NextBefore = entry.Sequence
		if visibleJournalEntry(scope, entry, branchLimits) && strings.Contains(strings.ToLower(entry.Message.Content), needle) {
			result.Entries = append(result.Entries, entry)
			if len(result.Entries) >= limit {
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return JournalSearchResult{}, fmt.Errorf("iterating conversation journal search: %w", err)
	}
	if result.Scanned < journalSearchScanLimit && len(result.Entries) < limit {
		result.NextBefore = 0
	}
	return result, nil
}

func (s *Store) searchJournalMemory(ctx context.Context, scope JournalScope, branchLimits map[string]uint64, query string, before uint64, limit int) (JournalSearchResult, error) {
	start := len(s.journalEntries)
	if before > 0 {
		start = sort.Search(len(s.journalEntries), func(i int) bool { return s.journalEntries[i].Sequence >= before })
	}
	var result JournalSearchResult
	needle := strings.ToLower(query)
	for i := start - 1; i >= 0 && result.Scanned < journalSearchScanLimit; i-- {
		if err := ctx.Err(); err != nil {
			return JournalSearchResult{}, err
		}
		entry := s.journalEntries[i]
		result.Scanned++
		result.NextBefore = entry.Sequence
		if visibleJournalEntry(scope, entry, branchLimits) && strings.Contains(strings.ToLower(entry.Message.Content), needle) {
			result.Entries = append(result.Entries, entry)
			if len(result.Entries) >= limit {
				break
			}
		}
	}
	if result.Scanned < journalSearchScanLimit && len(result.Entries) < limit {
		result.NextBefore = 0
	}
	return result, nil
}

// ReadJournalScoped returns an original turn only if it belongs to the bound
// conversation, branch, and agent. Unknown and out-of-scope IDs look identical.
func (s *Store) ReadJournalScoped(ctx context.Context, scope JournalScope, messageID string) (JournalEntry, bool, error) {
	if !validJournalScope(scope) {
		return JournalEntry{}, false, fmt.Errorf("conversation journal: unavailable request scope")
	}
	if messageID == "" {
		return JournalEntry{}, false, nil
	}
	if s.journalInitErr != nil {
		return JournalEntry{}, false, s.journalInitErr
	}
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	if err := s.loadJournal(ctx); err != nil {
		return JournalEntry{}, false, err
	}
	branchLimits, err := s.journalBranchLimitsLocked(ctx, scope)
	if err != nil {
		return JournalEntry{}, false, err
	}
	if s.sqliteDB != nil {
		entry, found, err := s.sqliteEntryByID(ctx, messageID)
		if err != nil || !found || !visibleJournalEntry(scope, entry, branchLimits) {
			return JournalEntry{}, false, err
		}
		return entry, true, nil
	}
	for _, entry := range s.journalEntries {
		if entry.Message.ID == messageID {
			if !visibleJournalEntry(scope, entry, branchLimits) {
				return JournalEntry{}, false, nil
			}
			return entry, true, nil
		}
	}
	return JournalEntry{}, false, nil
}
