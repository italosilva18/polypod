package swarm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/tasks"
)

const workerCheckpointVersion = 1

// CheckpointBinding carries the durable model/tool boundary for one worker
// turn. A nil binding preserves the previous executor path.
type CheckpointBinding struct {
	Initial *tasks.AgentCheckpoint
	Save    func(*tasks.AgentCheckpoint) error
}

type checkpointBindingKey struct{}

// WithCheckpointBinding attaches the save callback the team runner owns.
// The worker executor must still apply WithTeammateIdentity itself.
func WithCheckpointBinding(ctx context.Context, binding CheckpointBinding) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, checkpointBindingKey{}, binding)
}

// CheckpointBindingFromContext returns the binding installed by the runner.
func CheckpointBindingFromContext(ctx context.Context) (CheckpointBinding, bool) {
	if ctx == nil {
		return CheckpointBinding{}, false
	}
	binding, ok := ctx.Value(checkpointBindingKey{}).(CheckpointBinding)
	return binding, ok
}

type workerCheckpointKey struct {
	Team  string
	RunID string
	Agent string
	Turn  int
}

type workerCheckpointSnapshot struct {
	Team       string
	RunID      string
	Agent      string
	Turn       int
	AgentType  string
	Identity   TeammateIdentity
	Checkpoint *tasks.AgentCheckpoint
}

// workerCheckpointIssue records an uncertain boundary in any turn, including
// an earlier turn that a newer checkpoint would otherwise conceal.
type workerCheckpointIssue struct {
	Turn              int
	PendingToolCallID string
	Err               error
}

type workerCheckpointFile struct {
	Version    int                   `json:"version"`
	Team       string                `json:"team"`
	RunID      string                `json:"run_id"`
	Agent      string                `json:"agent"`
	Turn       int                   `json:"turn"`
	AgentType  string                `json:"agent_type,omitempty"`
	Identity   TeammateIdentity      `json:"identity"`
	Checkpoint tasks.AgentCheckpoint `json:"checkpoint"`
	SavedAt    time.Time             `json:"saved_at"`
}

// workerCheckpointStore keeps the latest boundary of each teammate turn.
// The team journal is intentionally not used: it truncates prompts and is
// not a replay log.
type workerCheckpointStore struct {
	mu  sync.Mutex
	dir string
}

func newWorkerCheckpointStore(dir string) (*workerCheckpointStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("worker checkpoint path is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating worker checkpoint directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("protecting worker checkpoint directory: %w", err)
	}
	if err := syncWorkerCheckpointParents(dir); err != nil {
		return nil, fmt.Errorf("syncing worker checkpoint parents: %w", err)
	}
	return &workerCheckpointStore{dir: dir}, nil
}

func (s *workerCheckpointStore) Save(key workerCheckpointKey, identity TeammateIdentity, agentType string, checkpoint *tasks.AgentCheckpoint) error {
	if s == nil {
		return errors.New("worker checkpoint store is unavailable")
	}
	if err := validateCheckpointKey(key); err != nil {
		return err
	}
	if checkpoint == nil {
		return errors.New("checkpoint ausente")
	}
	if checkpoint.Version != tasks.AgentCheckpointVersion || checkpoint.QuerySHA256 == "" || len(checkpoint.Messages) == 0 {
		return errors.New("checkpoint incompleto")
	}
	switch checkpoint.Phase {
	case tasks.AgentCheckpointPhaseModel, tasks.AgentCheckpointPhaseTools, tasks.AgentCheckpointPhaseDone:
	default:
		return fmt.Errorf("fase de checkpoint %q nao pode ser persistida", checkpoint.Phase)
	}
	if identity.TeamName != key.Team || identity.AgentName != key.Agent || identity.AgentID == "" {
		return errors.New("identidade do checkpoint nao corresponde ao integrante")
	}
	dir, err := s.agentDir(key.Team, key.RunID, key.Agent)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating worker checkpoint run: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("protecting worker checkpoint run: %w", err)
	}
	// A successful Save must also make newly created team/run/agent directory
	// entries durable before the executor is allowed to perform a tool call.
	if err := syncWorkerCheckpointParents(dir); err != nil {
		return fmt.Errorf("syncing worker checkpoint parents: %w", err)
	}
	stored := workerCheckpointFile{
		Version: workerCheckpointVersion, Team: key.Team, RunID: key.RunID, Agent: key.Agent,
		Turn: key.Turn, AgentType: agentType, Identity: identity, Checkpoint: *checkpoint,
		SavedAt: time.Now().UTC(),
	}
	stored.Checkpoint.Messages = append(json.RawMessage(nil), checkpoint.Messages...)
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding worker checkpoint: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".worker-checkpoint-*.tmp")
	if err != nil {
		return fmt.Errorf("creating worker checkpoint temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("protecting worker checkpoint: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("writing worker checkpoint: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("syncing worker checkpoint: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing worker checkpoint: %w", err)
	}
	target := filepath.Join(dir, turnFileName(key.Turn))
	if err := os.Rename(tempPath, target); err != nil {
		return fmt.Errorf("replacing worker checkpoint: %w", err)
	}
	if err := syncWorkerCheckpointDir(dir); err != nil {
		return fmt.Errorf("syncing worker checkpoint directory: %w", err)
	}
	return nil
}

// Inspect reads every turn so an earlier unresolved tool cannot be hidden by
// a later turn. A corrupt highest turn is never replaced by an older one.
func (s *workerCheckpointStore) Inspect(team, runID, agent string) (*workerCheckpointSnapshot, []workerCheckpointIssue, error) {
	if s == nil {
		return nil, nil, errors.New("worker checkpoint store is unavailable")
	}
	dir, err := s.agentDir(team, runID, agent)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("listing worker checkpoints: %w", err)
	}
	highest := 0
	var latest *workerCheckpointSnapshot
	var latestErr error
	var issues []workerCheckpointIssue
	for _, entry := range entries {
		turn, ok := parseTurnFile(entry.Name())
		if !ok {
			continue
		}
		snapshot, readErr := readWorkerCheckpoint(filepath.Join(dir, entry.Name()), team, runID, agent, turn)
		if readErr != nil {
			issues = append(issues, workerCheckpointIssue{Turn: turn, Err: readErr})
		} else if snapshot.Checkpoint.PendingToolCallID != "" {
			issues = append(issues, workerCheckpointIssue{Turn: turn,
				PendingToolCallID: snapshot.Checkpoint.PendingToolCallID})
		}
		if turn > highest {
			highest, latest, latestErr = turn, snapshot, readErr
		}
	}
	if highest == 0 {
		return nil, nil, nil
	}
	return latest, issues, latestErr
}

// Latest returns the highest turn boundary. Inspect is preferred when a
// caller must know about uncertainty in earlier turns.
func (s *workerCheckpointStore) Latest(team, runID, agent string) (*workerCheckpointSnapshot, error) {
	snapshot, _, err := s.Inspect(team, runID, agent)
	return snapshot, err
}

func syncWorkerCheckpointDir(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

// syncWorkerCheckpointParents covers every new ancestor, including the
// checkpoint root's parent on first creation. Syncing existing ancestors is
// harmless and closes the gap after a previous interrupted Save.
func syncWorkerCheckpointParents(path string) error {
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		if err := syncWorkerCheckpointDir(parent); err != nil {
			return fmt.Errorf("syncing %s: %w", parent, err)
		}
		if parent == filepath.Dir(parent) {
			return nil
		}
	}
}

// lockRun excludes a second process from continuing the same team run.
// The caller holds the file until the restored team stops.
func (s *workerCheckpointStore) lockRun(team, runID string) (*os.File, error) {
	if s == nil {
		return nil, errors.New("worker checkpoint store is unavailable")
	}
	dir, err := s.runDir(team, runID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating worker resume lock directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, ".resume.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("creating worker resume lock: %w", err)
	}
	if err := lockTeamJournalFile(file); err != nil {
		_ = file.Close()
		if teamJournalLockBusy(err) {
			return nil, errors.New("retomada da equipe já está em andamento")
		}
		return nil, fmt.Errorf("locking team resume: %w", err)
	}
	return file, nil
}

func releaseResumeLock(file *os.File) {
	if file == nil {
		return
	}
	_ = unlockTeamJournalFile(file)
	_ = file.Close()
}

func readWorkerCheckpoint(path, team, runID, agent string, turn int) (*workerCheckpointSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading worker checkpoint: %w", err)
	}
	var stored workerCheckpointFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("decoding worker checkpoint: %w", err)
	}
	if stored.Version != workerCheckpointVersion {
		return nil, fmt.Errorf("unsupported worker checkpoint version %d", stored.Version)
	}
	if stored.Team != team || stored.RunID != runID || stored.Agent != agent || stored.Turn != turn {
		return nil, errors.New("worker checkpoint identity mismatch")
	}
	if stored.Identity.TeamName != team || stored.Identity.AgentName != agent || stored.Identity.AgentID == "" {
		return nil, errors.New("worker checkpoint teammate mismatch")
	}
	if stored.Checkpoint.Version != tasks.AgentCheckpointVersion || stored.Checkpoint.AgentName == "" ||
		stored.Checkpoint.QuerySHA256 == "" || len(stored.Checkpoint.Messages) == 0 {
		return nil, errors.New("worker checkpoint is incomplete")
	}
	stored.Checkpoint.Messages = append(json.RawMessage(nil), stored.Checkpoint.Messages...)
	checkpoint := stored.Checkpoint
	return &workerCheckpointSnapshot{
		Team: team, RunID: runID, Agent: agent, Turn: turn, AgentType: stored.AgentType,
		Identity: stored.Identity, Checkpoint: &checkpoint,
	}, nil
}

func (s *workerCheckpointStore) runDir(team, runID string) (string, error) {
	teamSeg, err := checkpointSegment(team)
	if err != nil {
		return "", err
	}
	runSeg, err := checkpointSegment(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.dir, teamSeg, runSeg), nil
}

func (s *workerCheckpointStore) agentDir(team, runID, agent string) (string, error) {
	dir, err := s.runDir(team, runID)
	if err != nil {
		return "", err
	}
	agentSeg, err := checkpointSegment(agent)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, agentSeg), nil
}

func validateCheckpointKey(key workerCheckpointKey) error {
	if strings.TrimSpace(key.Team) == "" || strings.TrimSpace(key.RunID) == "" || strings.TrimSpace(key.Agent) == "" || key.Turn < 1 {
		return errors.New("worker checkpoint key is incomplete")
	}
	return nil
}

func checkpointSegment(value string) (string, error) {
	if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) {
		return "", errors.New("worker checkpoint name is invalid")
	}
	// A POSIX path component is commonly limited to 255 bytes. Preserve the
	// existing encoding for short names and hash longer valid team/run/agent
	// names so a worker never fails at its first checkpoint because of NAME_MAX.
	if len(value) > 127 {
		digest := sha256.Sum256([]byte(value))
		return "sha256-" + hex.EncodeToString(digest[:]), nil
	}
	return hex.EncodeToString([]byte(value)), nil
}

func turnFileName(turn int) string {
	return "turn-" + strconv.Itoa(turn) + ".json"
}

func parseTurnFile(name string) (int, bool) {
	if !strings.HasPrefix(name, "turn-") || !strings.HasSuffix(name, ".json") {
		return 0, false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(name, "turn-"), ".json")
	turn, err := strconv.Atoi(raw)
	if err != nil || turn < 1 || turnFileName(turn) != name {
		return 0, false
	}
	return turn, true
}
