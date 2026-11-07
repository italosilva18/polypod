package evolution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
)

const maxObservedTools = 24

type toolTraceContextKey struct{}

// ToolTrace keeps the names of successful, actually executed tools for one run.
// It never stores tool arguments or results, which may contain user secrets.
type ToolTrace struct {
	mu        sync.Mutex
	names     []string
	truncated bool
}

// WithToolTrace associates a fresh trace with the context passed to a run.
// The same context must reach the orchestrator's tool execution path.
func WithToolTrace(ctx context.Context) (context.Context, *ToolTrace) {
	trace := &ToolTrace{}
	return context.WithValue(ctx, toolTraceContextKey{}, trace), trace
}

// ObserveTool is called by the orchestrator only after a registered tool
// returns without an execution error or a ToolResult marked as an error.
func ObserveTool(ctx context.Context, name string) {
	if ctx == nil || !toolNamePattern.MatchString(name) {
		return
	}
	trace, ok := ctx.Value(toolTraceContextKey{}).(*ToolTrace)
	if !ok || trace == nil {
		return
	}
	trace.mu.Lock()
	if len(trace.names) < maxObservedTools {
		trace.names = append(trace.names, name)
	} else {
		trace.truncated = true
	}
	trace.mu.Unlock()
}

// Names returns a snapshot in execution order. It can be saved before the
// terminal callback so a crash or callback retry can recover the same trace.
func (t *ToolTrace) Names() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.truncated {
		// A partial sequence could suggest a skill that omits the tool which
		// actually produced the verified result. Keep the outcome metric only.
		return nil
	}
	return append([]string(nil), t.names...)
}

// SaveToolTrace durably records the observed names for a single task or
// routine run. Identical retries are idempotent; a changed trace is rejected.
// Callers must supply names captured from ToolTrace, never model text.
func (s *Store) SaveToolTrace(ctx context.Context, sourceKind, runID string, names []string) error {
	sourceKind, runID = strings.TrimSpace(sourceKind), strings.TrimSpace(runID)
	if sourceKind != "task" && sourceKind != "routine" {
		return errors.New("trace exige fonte task ou routine")
	}
	if !evidenceTokenPattern.MatchString(runID) || len(runID) > 160 {
		return errors.New("ID da execução para trace inválido")
	}
	if len(names) > maxObservedTools {
		return fmt.Errorf("trace excede %d ferramentas", maxObservedTools)
	}
	for _, name := range names {
		if !toolNamePattern.MatchString(name) {
			return fmt.Errorf("nome de ferramenta inválido no trace: %q", name)
		}
	}
	names = append([]string{}, names...)
	raw, err := json.Marshal(names)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO tool_traces
	 (source_kind,run_id,tool_names_json,created_at) VALUES(?,?,?,?)`,
		sourceKind, runID, string(raw), time.Now().UnixMilli())
	if err != nil {
		return err
	}
	stored, err := s.toolTrace(ctx, sourceKind, runID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(stored, names) {
		return fmt.Errorf("trace %s/%s já registrado com outras ferramentas: %w",
			sourceKind, runID, ErrStateConflict)
	}
	return nil
}

func (s *Store) toolTrace(ctx context.Context, sourceKind, runID string) ([]string, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT tool_names_json FROM tool_traces
	 WHERE source_kind=? AND run_id=?`, sourceKind, runID).Scan(&raw); err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil, err
	}
	return names, nil
}

func (s *Store) observedOrStoredTools(ctx context.Context, sourceKind, runID string, observed []string) ([]string, error) {
	if len(observed) != 0 {
		return observed, nil
	}
	names, err := s.toolTrace(ctx, sourceKind, runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return names, err
}
