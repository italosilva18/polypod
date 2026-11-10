package swarm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// TeamJournal retains team coordination evidence across process restarts.
// It records outcomes but deliberately never replays an in-flight agent turn:
// repeating a tool call after a crash could repeat an external side effect.
type TeamJournal struct {
	mu   sync.Mutex
	dir  string
	path string
	lock *os.File
}

type journalRecord struct {
	event     journalEvent
	ownerLive bool
}

type journalEvent struct {
	Version int          `json:"version"`
	Kind    string       `json:"kind"`
	Team    string       `json:"team"`
	RunID   string       `json:"run_id"`
	At      time.Time    `json:"at"`
	Summary *TeamSummary `json:"summary,omitempty"`
	Member  *MemberInfo  `json:"member,omitempty"`
	Message *Message     `json:"message,omitempty"`
	Agent   string       `json:"agent,omitempty"`
	Turn    int          `json:"turn,omitempty"`
	Prompt  string       `json:"prompt,omitempty"`
	Result  string       `json:"result,omitempty"`
	Error   string       `json:"error,omitempty"`
	IDs     []string     `json:"ids,omitempty"`
}

// ArchivedTurn is a completed or interrupted teammate turn from a past run.
type ArchivedTurn struct {
	Turn        int       `json:"turn"`
	Response    string    `json:"response,omitempty"`
	Error       string    `json:"error,omitempty"`
	Started     time.Time `json:"started_at"`
	Finished    time.Time `json:"finished_at,omitempty"`
	Running     bool      `json:"running,omitempty"`
	Interrupted bool      `json:"interrupted,omitempty"`
}

// ArchivedTeam is a read-only view of the latest recorded run of a team.
type ArchivedTeam struct {
	RunID     string
	OwnerLive bool
	Summary   TeamSummary
	Members   []MemberInfo
	Pending   []*Message
	Results   map[string][]ArchivedTurn
}

const maxJournalText = 256 << 10
const teamJournalVersion = 1
const maxJournalRecord = 4 << 20

// NewTeamJournal opens a private append-only event log. It rejects malformed
// complete records; an incomplete final line from a crash is ignored.
func NewTeamJournal(dir string) (*TeamJournal, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("team journal path is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating team journal directory: %w", err)
	}
	lock, err := os.CreateTemp(dir, "instance-*.lock")
	if err != nil {
		return nil, fmt.Errorf("creating team journal instance: %w", err)
	}
	if err := lockTeamJournalFile(lock); err != nil {
		_ = lock.Close()
		_ = os.Remove(lock.Name())
		return nil, fmt.Errorf("locking team journal instance: %w", err)
	}
	j := &TeamJournal{dir: dir, path: strings.TrimSuffix(lock.Name(), ".lock") + ".jsonl", lock: lock}
	if _, err := j.readLocked(); err != nil {
		_ = j.Close()
		return nil, err
	}
	file, err := os.OpenFile(j.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = j.Close()
		return nil, fmt.Errorf("creating team journal: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = j.Close()
		return nil, fmt.Errorf("closing new team journal: %w", err)
	}
	if err := syncTeamJournalDir(dir); err != nil {
		_ = j.Close()
		return nil, fmt.Errorf("syncing team journal directory: %w", err)
	}
	return j, nil
}

func syncTeamJournalDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
}

// Close releases this instance lease. Historical files remain for inspection.
func (j *TeamJournal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lock == nil {
		return nil
	}
	err := unlockTeamJournalFile(j.lock)
	err = errors.Join(err, j.lock.Close())
	j.lock = nil
	return err
}

func (j *TeamJournal) append(event journalEvent) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lock == nil {
		return errors.New("team journal is closed")
	}
	event.At = time.Now()
	event.Version = teamJournalVersion
	event.Prompt = trimJournalText(event.Prompt)
	event.Result = trimJournalText(event.Result)
	event.Error = trimJournalText(event.Error)
	if event.Message != nil {
		copy := *event.Message
		copy.Content = trimJournalText(copy.Content)
		copy.Summary = trimJournalText(copy.Summary)
		event.Message = &copy
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encoding team event: %w", err)
	}
	file, err := os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening team journal: %w", err)
	}
	before, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("seeking team journal: %w", err)
	}
	_, err = file.Write(append(data, '\n'))
	if err != nil {
		_ = file.Truncate(before)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("writing team journal: %w", err)
	}
	return nil
}

func trimJournalText(value string) string {
	if len(value) <= maxJournalText {
		return value
	}
	return value[:maxJournalText] + "\n[truncated in team journal]"
}

func (j *TeamJournal) readLocked() ([]journalRecord, error) {
	paths, err := filepath.Glob(filepath.Join(j.dir, "instance-*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("listing team journals: %w", err)
	}
	var records []journalRecord
	for _, path := range paths {
		live := path == j.path
		if !live {
			live, err = teamJournalOwnerLive(strings.TrimSuffix(path, ".jsonl") + ".lock")
			if err != nil {
				return nil, fmt.Errorf("checking team journal owner: %w", err)
			}
		}
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("opening team journal: %w", err)
		}
		reader := bufio.NewReaderSize(file, maxJournalRecord)
		for {
			line, readErr := reader.ReadSlice('\n')
			if errors.Is(readErr, io.EOF) {
				// Incomplete final record from a crash: do not interpret it.
				break
			}
			if readErr != nil {
				_ = file.Close()
				return nil, fmt.Errorf("reading bounded team journal record: %w", readErr)
			}
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var event journalEvent
			if err := json.Unmarshal(line, &event); err != nil {
				_ = file.Close()
				return nil, fmt.Errorf("decoding team journal: %w", err)
			}
			if event.Version != teamJournalVersion {
				_ = file.Close()
				return nil, fmt.Errorf("unsupported team journal version %d", event.Version)
			}
			records = append(records, journalRecord{event: event, ownerLive: live})
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("closing team journal: %w", err)
		}
	}
	sort.SliceStable(records, func(i, k int) bool { return records[i].event.At.Before(records[k].event.At) })
	return records, nil
}

// Teams returns the latest recorded state of every team name.
func (j *TeamJournal) Teams() ([]ArchivedTeam, error) {
	if j == nil {
		return nil, nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	records, err := j.readLocked()
	if err != nil {
		return nil, err
	}
	latest := make(map[string]string)
	liveOwner := make(map[string]bool)
	for _, record := range records {
		event := record.event
		if event.Kind == "created" {
			latest[event.Team] = event.RunID
			liveOwner[event.Team] = record.ownerLive
		}
	}
	archive := make(map[string]*ArchivedTeam, len(latest))
	pending := make(map[string]map[string]*Message, len(latest))
	for _, record := range records {
		event := record.event
		if latest[event.Team] != event.RunID {
			continue
		}
		item := archive[event.Team]
		if event.Kind == "created" {
			if event.Summary == nil {
				continue
			}
			item = &ArchivedTeam{RunID: event.RunID, Summary: *event.Summary, Results: make(map[string][]ArchivedTurn)}
			archive[event.Team] = item
			pending[event.Team] = make(map[string]*Message)
			continue
		}
		if item == nil {
			continue
		}
		switch event.Kind {
		case "state":
			if event.Summary != nil {
				item.Summary = *event.Summary
			}
		case "member":
			if event.Member != nil {
				item.Members = append(item.Members, *event.Member)
				item.Summary.MemberCount = len(item.Members)
			}
		case "member_removed":
			kept := item.Members[:0]
			for _, member := range item.Members {
				if member.Name != event.Agent {
					kept = append(kept, member)
				}
			}
			item.Members = kept
			item.Summary.MemberCount = len(kept)
		case "message":
			if event.Message != nil && event.Message.Target == TeamLeaderName {
				pending[event.Team][event.Message.ID] = event.Message
			}
		case "ack":
			for _, id := range event.IDs {
				delete(pending[event.Team], id)
			}
		case "turn_started":
			item.Results[event.Agent] = append(item.Results[event.Agent], ArchivedTurn{
				Turn: event.Turn, Started: event.At, Running: true,
			})
		case "turn_finished":
			turns := item.Results[event.Agent]
			matched := false
			for i := len(turns) - 1; i >= 0; i-- {
				if turns[i].Turn == event.Turn && turns[i].Running {
					turns[i].Response = event.Result
					turns[i].Error = event.Error
					turns[i].Finished = event.At
					turns[i].Running = false
					turns[i].Interrupted = false
					matched = true
					break
				}
			}
			if !matched {
				turns = append(turns, ArchivedTurn{Turn: event.Turn, Response: event.Result,
					Error: event.Error, Finished: event.At})
			}
			if len(turns) > 100 {
				turns = turns[len(turns)-100:]
			}
			item.Results[event.Agent] = turns
		}
	}
	items := make([]ArchivedTeam, 0, len(archive))
	for name, item := range archive {
		item.OwnerLive = liveOwner[name]
		if !liveOwner[name] && item.Summary.State != TeamStateStopped.String() {
			item.Summary.State = "interrupted"
		}
		if !liveOwner[name] {
			for agent, turns := range item.Results {
				for i := range turns {
					if turns[i].Running {
						turns[i].Running = false
						turns[i].Interrupted = true
					}
				}
				item.Results[agent] = turns
			}
		}
		for _, msg := range pending[name] {
			item.Pending = append(item.Pending, msg)
		}
		sort.Slice(item.Pending, func(i, k int) bool {
			return item.Pending[i].Timestamp.Before(item.Pending[k].Timestamp)
		})
		items = append(items, *item)
	}
	sort.Slice(items, func(i, k int) bool { return items[i].Summary.Name < items[k].Summary.Name })
	return items, nil
}

func (j *TeamJournal) Team(name string) (ArchivedTeam, bool, error) {
	items, err := j.Teams()
	if err != nil {
		return ArchivedTeam{}, false, err
	}
	for _, item := range items {
		if item.Summary.Name == name {
			return item, true, nil
		}
	}
	return ArchivedTeam{}, false, nil
}

// Acknowledge records delivery of archived leader messages without requiring
// the original worker goroutines to exist.
func (j *TeamJournal) Acknowledge(name string, ids []string) (int, int, error) {
	item, ok, err := j.Team(name)
	if err != nil {
		return 0, 0, err
	}
	if !ok {
		return 0, 0, fmt.Errorf("equipe %s não encontrada", name)
	}
	if item.OwnerLive && item.Summary.State != TeamStateStopped.String() {
		return 0, len(item.Pending), fmt.Errorf("equipe %s ainda está ativa em outro processo", name)
	}
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	var matched []string
	for _, msg := range item.Pending {
		if selected[msg.ID] {
			matched = append(matched, msg.ID)
		}
	}
	if len(matched) == 0 {
		return 0, len(item.Pending), nil
	}
	if err := j.append(journalEvent{Kind: "ack", Team: name, RunID: item.RunID, IDs: matched}); err != nil {
		return 0, len(item.Pending), err
	}
	return len(matched), len(item.Pending) - len(matched), nil
}

// AcknowledgeLive mirrors a live mailbox acknowledgement into the journal.
func (j *TeamJournal) AcknowledgeLive(team *Team, ids []string) error {
	if j == nil || team == nil || len(ids) == 0 {
		return nil
	}
	return j.append(journalEvent{Kind: "ack", Team: team.Name, RunID: team.runID, IDs: ids})
}
