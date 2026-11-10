package conversation

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

// journalFork records the precise parent journal boundary inherited by a new
// branch. A child may recall parent entries through ForkSequence only.
type journalFork struct {
	ChildBranch  string `json:"child_branch"`
	ParentBranch string `json:"parent_branch"`
	SessionID    string `json:"session_id"`
	Channel      string `json:"channel"`
	ForkSequence uint64 `json:"fork_sequence"`
}

func (s *Store) journalForksPath() string {
	return filepath.Join(s.dataDir, "journal-forks.jsonl")
}

func (s *Store) loadJournalForksFile() error {
	s.journalForks = make(map[string]journalFork)
	if s.dataDir == "" {
		return nil
	}
	f, err := os.Open(s.journalForksPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("opening conversation journal forks: %w", err)
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && readErr == io.EOF {
			return nil
		}
		if readErr != nil && readErr != io.EOF {
			return fmt.Errorf("reading conversation journal forks: %w", readErr)
		}
		if len(line) == 0 || line[len(line)-1] != '\n' {
			return fmt.Errorf("conversation journal forks: incomplete final record")
		}
		var fork journalFork
		if err := json.Unmarshal(line[:len(line)-1], &fork); err != nil {
			return fmt.Errorf("decoding conversation journal fork: %w", err)
		}
		if err := validateJournalFork(fork); err != nil {
			return err
		}
		if prior, exists := s.journalForks[fork.ChildBranch]; exists && prior != fork {
			return fmt.Errorf("conversation journal fork %q conflicts with an earlier record", fork.ChildBranch)
		}
		s.journalForks[fork.ChildBranch] = fork
	}
}

func validateJournalFork(fork journalFork) error {
	if fork.ChildBranch == "" || fork.ParentBranch == "" || fork.ChildBranch == fork.ParentBranch ||
		fork.SessionID == "" || fork.Channel == "" || fork.ForkSequence > math.MaxInt64 {
		return fmt.Errorf("conversation journal fork is invalid")
	}
	return nil
}

// RegisterJournalFork binds a child to the exact messages copied into its
// initial snapshot. The greatest inherited journal sequence freezes the
// parent's recall boundary, so later parent turns never appear in the child.
func (s *Store) RegisterJournalFork(ctx context.Context, channel, userID, parentBranch, childBranch string, inheritedIDs []string) error {
	fork := journalFork{ChildBranch: childBranch, ParentBranch: parentBranch,
		SessionID: channel + ":" + userID, Channel: channel}
	if channel == "" || userID == "" {
		return fmt.Errorf("conversation journal fork requires a channel and user")
	}
	if err := validateJournalFork(fork); err != nil {
		return err
	}
	if s.journalInitErr != nil {
		return s.journalInitErr
	}
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	if err := s.loadJournal(ctx); err != nil {
		return err
	}
	for _, id := range inheritedIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry, found, err := s.journalEntryByIDLocked(ctx, id)
		if err != nil {
			return err
		}
		if !found || entry.SessionID != fork.SessionID || entry.Channel != channel {
			return fmt.Errorf("conversation journal fork has an unjournaled inherited message")
		}
		if entry.Sequence > fork.ForkSequence {
			fork.ForkSequence = entry.Sequence
		}
	}
	if prior, found, err := s.journalForkByChildLocked(ctx, childBranch); err != nil {
		return err
	} else if found {
		if prior == fork {
			return nil
		}
		return fmt.Errorf("conversation journal fork %q already belongs to another snapshot", childBranch)
	}
	if s.sqliteDB != nil {
		_, err := s.sqliteDB.ExecContext(ctx, `INSERT INTO conversation_journal_forks
			(child_branch,parent_branch,session_id,channel,fork_sequence) VALUES(?,?,?,?,?)`,
			fork.ChildBranch, fork.ParentBranch, fork.SessionID, fork.Channel, int64(fork.ForkSequence))
		if err != nil {
			return fmt.Errorf("saving conversation journal fork: %w", err)
		}
		return nil
	}
	if s.dataDir != "" {
		if err := os.MkdirAll(s.dataDir, 0700); err != nil {
			return err
		}
		raw, err := json.Marshal(fork)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(s.journalForksPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(append(raw, '\n'))
		syncErr := f.Sync()
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	s.journalForks[childBranch] = fork
	return nil
}

func (s *Store) journalEntryByIDLocked(ctx context.Context, id string) (JournalEntry, bool, error) {
	if s.sqliteDB != nil {
		return s.sqliteEntryByID(ctx, id)
	}
	for _, entry := range s.journalEntries {
		if entry.Message.ID == id {
			return entry, true, nil
		}
	}
	return JournalEntry{}, false, nil
}

func (s *Store) journalForkByChildLocked(ctx context.Context, childBranch string) (journalFork, bool, error) {
	if s.sqliteDB == nil {
		fork, found := s.journalForks[childBranch]
		return fork, found, nil
	}
	var fork journalFork
	var sequence int64
	err := s.sqliteDB.QueryRowContext(ctx, `SELECT parent_branch,session_id,channel,fork_sequence
		FROM conversation_journal_forks WHERE child_branch=?`, childBranch).
		Scan(&fork.ParentBranch, &fork.SessionID, &fork.Channel, &sequence)
	if err == sql.ErrNoRows {
		return journalFork{}, false, nil
	}
	if err != nil {
		return journalFork{}, false, fmt.Errorf("reading conversation journal fork: %w", err)
	}
	if sequence < 0 {
		return journalFork{}, false, fmt.Errorf("conversation journal fork has negative sequence")
	}
	fork.ChildBranch, fork.ForkSequence = childBranch, uint64(sequence)
	if err := validateJournalFork(fork); err != nil {
		return journalFork{}, false, err
	}
	return fork, true, nil
}

func (s *Store) journalBranchLimitsLocked(ctx context.Context, scope JournalScope) (map[string]uint64, error) {
	limits := map[string]uint64{scope.BranchID: math.MaxUint64}
	branch := scope.BranchID
	for depth := 0; depth < 64; depth++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fork, found, err := s.journalForkByChildLocked(ctx, branch)
		if err != nil {
			return nil, err
		}
		if !found {
			return limits, nil
		}
		if fork.SessionID != scope.SessionID || fork.Channel != scope.Channel {
			return nil, fmt.Errorf("conversation journal fork belongs to another conversation")
		}
		if _, seen := limits[fork.ParentBranch]; seen {
			return nil, fmt.Errorf("conversation journal fork ancestry contains a cycle")
		}
		cutoff := min(limits[branch], fork.ForkSequence)
		limits[fork.ParentBranch] = cutoff
		branch = fork.ParentBranch
	}
	return nil, fmt.Errorf("conversation journal fork ancestry exceeds 64 levels")
}
