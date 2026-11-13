package ai

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type waterfallTurnActiveKey struct{}

func (c *Client) beginWaterfallTurn(ctx context.Context, messages []ChatMessage) (context.Context, func()) {
	if ctx != nil {
		if active, _ := ctx.Value(waterfallTurnActiveKey{}).(bool); active {
			return ctx, func() {}
		}
	}
	query := requestQueryHintFromContext(ctx)
	if query == "" {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" && strings.TrimSpace(messages[i].Content) != "" {
				query = messages[i].Content
				break
			}
		}
	}
	wf := c.Waterfall()
	wf.BeginTurn(query, c.modelFor(ctx))
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, waterfallTurnActiveKey{}, true), wf.EndTurn
}

// WaterfallSpan represents one timed step (model call or tool execution) inside an AI turn.
type WaterfallSpan struct {
	Kind             string        `json:"kind"` // "model" or "tool"
	Label            string        `json:"label"`
	ToolID           string        `json:"tool_id,omitempty"`
	StartOffset      time.Duration `json:"start_offset"`
	Duration         time.Duration `json:"duration"`
	PromptTokens     int           `json:"prompt_tokens,omitempty"`
	CompletionTokens int           `json:"completion_tokens,omitempty"`
	ResultBytes      int           `json:"result_bytes,omitempty"`
	Parallel         bool          `json:"parallel,omitempty"`
	IsError          bool          `json:"is_error,omitempty"`
}

// TurnWaterfall Snapshot of a completed or in-progress turn trajectory.
type TurnWaterfall struct {
	Query         string          `json:"query"`
	Model         string          `json:"model"`
	StartedAt     time.Time       `json:"started_at"`
	TotalDuration time.Duration   `json:"total_duration"`
	Spans         []WaterfallSpan `json:"spans"`
}

// WaterfallRecorder captures step-by-step execution timelines across turns for `/waterfall`.
type WaterfallRecorder struct {
	mu       sync.RWMutex
	active   *activeWaterfall
	history  []TurnWaterfall
	maxTurns int
}

type activeWaterfall struct {
	turn       TurnWaterfall
	inFlight   map[string]int // toolID -> index in turn.Spans
	openTools  int
}

// NewWaterfallRecorder creates a bounded trajectory waterfall recorder.
func NewWaterfallRecorder(maxTurns int) *WaterfallRecorder {
	if maxTurns <= 0 {
		maxTurns = 12
	}
	return &WaterfallRecorder{maxTurns: maxTurns}
}

// BeginTurn starts recording a new turn waterfall.
func (r *WaterfallRecorder) BeginTurn(query, model string) {
	if r == nil {
		return
	}
	q := strings.TrimSpace(query)
	if len(q) > 100 {
		q = q[:97] + "..."
	}
	r.mu.Lock()
	r.active = &activeWaterfall{
		turn: TurnWaterfall{
			Query:     q,
			Model:     model,
			StartedAt: time.Now(),
		},
		inFlight: make(map[string]int),
	}
	r.mu.Unlock()
}

// RecordModelSpan records an LLM inference step in the current turn.
func (r *WaterfallRecorder) RecordModelSpan(model string, started time.Time, dur time.Duration, promptTok, compTok int, isErr bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return
	}
	offset := started.Sub(r.active.turn.StartedAt)
	if offset < 0 {
		offset = 0
	}
	label := model
	if label == "" {
		label = r.active.turn.Model
	}
	r.active.turn.Spans = append(r.active.turn.Spans, WaterfallSpan{
		Kind:             "model",
		Label:            label,
		StartOffset:      offset,
		Duration:         dur,
		PromptTokens:     promptTok,
		CompletionTokens: compTok,
		IsError:          isErr,
	})
}

// ObserveToolEvent updates tool spans from streaming/loop ToolEvents.
func (r *WaterfallRecorder) ObserveToolEvent(ev ToolEvent) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return
	}
	now := time.Now()
	switch ev.Type {
	case ToolEventStart:
		offset := now.Sub(r.active.turn.StartedAt)
		if offset < 0 {
			offset = 0
		}
		parallel := r.active.openTools > 0
		if parallel {
			// Also mark any currently open tool spans as parallel.
			for _, idx := range r.active.inFlight {
				if idx >= 0 && idx < len(r.active.turn.Spans) {
					r.active.turn.Spans[idx].Parallel = true
				}
			}
		}
		r.active.openTools++
		idx := len(r.active.turn.Spans)
		r.active.turn.Spans = append(r.active.turn.Spans, WaterfallSpan{
			Kind:        "tool",
			Label:       ev.ToolName,
			ToolID:      ev.ToolID,
			StartOffset: offset,
			Parallel:    parallel,
		})
		if ev.ToolID != "" {
			r.active.inFlight[ev.ToolID] = idx
		}
	case ToolEventComplete, ToolEventError:
		if r.active.openTools > 0 {
			r.active.openTools--
		}
		idx, ok := r.active.inFlight[ev.ToolID]
		if ok {
			delete(r.active.inFlight, ev.ToolID)
		} else {
			// Match last unfinished span with same tool name.
			idx = -1
			for i := len(r.active.turn.Spans) - 1; i >= 0; i-- {
				if r.active.turn.Spans[i].Kind == "tool" && r.active.turn.Spans[i].Label == ev.ToolName && r.active.turn.Spans[i].Duration == 0 {
					idx = i
					break
				}
			}
		}
		if idx >= 0 && idx < len(r.active.turn.Spans) {
			dur := ev.Elapsed
			if dur <= 0 {
				dur = now.Sub(r.active.turn.StartedAt) - r.active.turn.Spans[idx].StartOffset
			}
			r.active.turn.Spans[idx].Duration = dur
			r.active.turn.Spans[idx].ResultBytes = ev.ResultBytes
			r.active.turn.Spans[idx].IsError = ev.Type == ToolEventError
		}
	}
}

// EndTurn finalizes the current turn waterfall and pushes it onto bounded history.
func (r *WaterfallRecorder) EndTurn() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return
	}
	done := r.active.turn
	done.TotalDuration = time.Since(done.StartedAt)
	if done.TotalDuration <= 0 {
		for _, s := range done.Spans {
			if end := s.StartOffset + s.Duration; end > done.TotalDuration {
				done.TotalDuration = end
			}
		}
	}
	r.active = nil
	r.history = append(r.history, done)
	if len(r.history) > r.maxTurns {
		r.history = r.history[len(r.history)-r.maxTurns:]
	}
}

// Latest returns the active turn waterfall (if in flight) or the most recent completed turn.
func (r *WaterfallRecorder) Latest() (TurnWaterfall, bool) {
	if r == nil {
		return TurnWaterfall{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.active != nil {
		cp := r.active.turn
		cp.TotalDuration = time.Since(cp.StartedAt)
		cp.Spans = append([]WaterfallSpan(nil), r.active.turn.Spans...)
		return cp, true
	}
	if len(r.history) == 0 {
		return TurnWaterfall{}, false
	}
	last := r.history[len(r.history)-1]
	last.Spans = append([]WaterfallSpan(nil), last.Spans...)
	return last, true
}

// FormatWaterfall renders an ASCII/Unicode visual timeline cascade for terminal inspection.
func FormatWaterfall(w TurnWaterfall) string {
	if len(w.Spans) == 0 {
		return "Nenhum passo registrado na cascata de execução (/waterfall) ainda."
	}
	total := w.TotalDuration
	for _, s := range w.Spans {
		if end := s.StartOffset + s.Duration; end > total {
			total = end
		}
	}
	if total <= 0 {
		total = time.Millisecond
	}

	const barWidth = 24
	var b strings.Builder
	fmt.Fprintf(&b, "## Trajectory Waterfall — %s (total: %s)\n", w.Model, total.Round(time.Millisecond))
	if w.Query != "" {
		fmt.Fprintf(&b, "Pedido: %q\n", w.Query)
	}
	b.WriteString("\n")

	for i, s := range w.Spans {
		startRatio := float64(s.StartOffset) / float64(total)
		durRatio := float64(s.Duration) / float64(total)
		startCell := int(startRatio * barWidth)
		spanCells := int(durRatio * barWidth)
		if spanCells < 1 {
			spanCells = 1
		}
		if startCell >= barWidth {
			startCell = barWidth - 1
		}
		if startCell+spanCells > barWidth {
			spanCells = barWidth - startCell
		}

		fill := "█"
		if s.Kind == "tool" {
			fill = "▓"
		}
		if s.IsError {
			fill = "✖"
		}
		bar := strings.Repeat("░", startCell) + strings.Repeat(fill, spanCells) + strings.Repeat("░", barWidth-startCell-spanCells)

		prefix := "├─"
		if i == len(w.Spans)-1 {
			prefix = "└─"
		}
		icon := "🧠"
		if s.Kind == "tool" {
			icon = "🔧"
			if s.Parallel {
				icon = "⚡"
			}
		}
		status := "OK"
		if s.IsError {
			status = "ERRO"
		}
		meta := ""
		if s.Kind == "model" && (s.PromptTokens > 0 || s.CompletionTokens > 0) {
			meta = fmt.Sprintf(" (%d→%d tok)", s.PromptTokens, s.CompletionTokens)
		} else if s.Kind == "tool" {
			parTag := ""
			if s.Parallel {
				parTag = " [paralelo]"
			}
			if s.ResultBytes > 0 {
				meta = fmt.Sprintf(" (%d B)%s [%s]", s.ResultBytes, parTag, status)
			} else {
				meta = fmt.Sprintf("%s [%s]", parTag, status)
			}
		}
		fmt.Fprintf(&b, "%s [%6s] %s %s %-20s %7s%s\n",
			prefix,
			s.StartOffset.Round(10*time.Millisecond),
			bar,
			icon,
			s.Label,
			s.Duration.Round(time.Millisecond),
			meta,
		)
	}
	return strings.TrimSpace(b.String())
}
