package query

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/types"
)

// ----- Test doubles -----

// fakeModel is a scripted ModelCaller that returns queued responses.
type fakeModel struct {
	responses []*ModelResponse
	errs      []error
	calls     int
}

func (f *fakeModel) Call(_ context.Context, _ []Message, _ []string) (*ModelResponse, error) {
	i := f.calls
	f.calls++
	if i >= len(f.responses) {
		return &ModelResponse{StopReason: "end_turn"}, nil
	}
	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return f.responses[i], err
}

// fakeTools records executions and returns a scripted result.
type fakeTools struct {
	result types.ToolResult
	err    error
	calls  int
	last   struct {
		name string
		args map[string]any
	}
}

func (f *fakeTools) Execute(name string, args map[string]any, _ types.ToolContext) (types.ToolResult, error) {
	f.calls++
	f.last.name = name
	f.last.args = args
	return f.result, f.err
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ----- QueryEngine -----

func TestNewQueryEngine(t *testing.T) {
	sid := types.NewSessionID()
	model := &fakeModel{}
	tools := &fakeTools{}
	qe := NewQueryEngine(sid, model, tools, []string{"bash"}, silentLogger())

	if qe == nil {
		t.Fatal("NewQueryEngine returned nil")
	}
	if got := qe.GetMessages(); len(got) != 0 {
		t.Errorf("expected 0 initial messages, got %d", len(got))
	}
	stats := qe.Stats()
	if stats["session_id"] != sid.String() {
		t.Errorf("Stats session_id=%v want %v", stats["session_id"], sid.String())
	}
	if stats["messages"].(int) != 0 {
		t.Errorf("expected 0 messages in stats")
	}
}

func TestQueryEngineSubmit_EndTurnImmediate(t *testing.T) {
	model := &fakeModel{
		responses: []*ModelResponse{
			{
				Content:    "hello back",
				StopReason: "end_turn",
				Usage:      TokenUsage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			},
		},
	}
	qe := NewQueryEngine(types.NewSessionID(), model, &fakeTools{}, nil, silentLogger())

	res, err := qe.Submit(context.Background(), "hi", QueryParams{SystemPrompt: "sys"})
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if res.Error != nil {
		t.Errorf("res.Error = %v, want nil", res.Error)
	}
	if res.Turns != 1 {
		t.Errorf("Turns = %d, want 1", res.Turns)
	}
	if res.TotalTokens != 8 {
		t.Errorf("TotalTokens = %d, want 8", res.TotalTokens)
	}
	if res.PromptTokens != 5 || res.CompletionTokens != 3 {
		t.Errorf("prompt/completion = %d/%d, want 5/3", res.PromptTokens, res.CompletionTokens)
	}
	// Expect: system, user, assistant
	if len(res.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(res.Messages))
	}
	if res.Messages[0].Role != "system" || res.Messages[0].Content != "sys" {
		t.Errorf("message[0] not system prompt: %+v", res.Messages[0])
	}
	if res.Messages[1].Role != "user" || res.Messages[1].Content != "hi" {
		t.Errorf("message[1] not user: %+v", res.Messages[1])
	}
	if res.Messages[2].Role != "assistant" || res.Messages[2].Content != "hello back" {
		t.Errorf("message[2] not assistant: %+v", res.Messages[2])
	}
}

func TestQueryEngineSubmit_ToolLoop(t *testing.T) {
	model := &fakeModel{
		responses: []*ModelResponse{
			{
				Content:    "let me use a tool",
				StopReason: "tool_use",
				ToolCalls: []ModelToolCall{
					{ID: "t1", Name: "echo", Arguments: map[string]any{"msg": "x"}},
				},
				Usage: TokenUsage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6},
			},
			{
				Content:    "final answer",
				StopReason: "end_turn",
				Usage:      TokenUsage{PromptTokens: 6, CompletionTokens: 4, TotalTokens: 10},
			},
		},
	}
	tools := &fakeTools{result: types.ToolResult{Content: "tool output"}}
	qe := NewQueryEngine(types.NewSessionID(), model, tools, []string{"echo"}, silentLogger())

	res, err := qe.Submit(context.Background(), "do thing", QueryParams{})
	if err != nil {
		t.Fatalf("Submit error: %v", err)
	}
	if res.Error != nil {
		t.Errorf("res.Error = %v, want nil", res.Error)
	}
	if res.Turns != 2 {
		t.Errorf("Turns = %d, want 2", res.Turns)
	}
	if tools.calls != 1 {
		t.Errorf("tools.calls = %d, want 1", tools.calls)
	}
	if tools.last.name != "echo" {
		t.Errorf("tool name = %q, want echo", tools.last.name)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call record, got %d", len(res.ToolCalls))
	}
	if res.ToolCalls[0].Result != "tool output" {
		t.Errorf("tool result = %q", res.ToolCalls[0].Result)
	}
	if res.TotalTokens != 16 {
		t.Errorf("TotalTokens = %d, want 16", res.TotalTokens)
	}
}

func TestQueryEngineSubmit_ModelError(t *testing.T) {
	model := &fakeModel{
		responses: []*ModelResponse{{}},
		errs:      []error{errors.New("boom")},
	}
	qe := NewQueryEngine(types.NewSessionID(), model, &fakeTools{}, nil, silentLogger())

	res, err := qe.Submit(context.Background(), "hi", QueryParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "model call failed") {
		t.Errorf("error = %v", err)
	}
	if res == nil || res.Error == nil {
		t.Error("expected res.Error to be set")
	}
}

func TestQueryEngineSubmit_MaxTurnsExceeded(t *testing.T) {
	// Always ask for a tool call so the loop never terminates normally.
	loopResp := &ModelResponse{
		Content:    "again",
		StopReason: "tool_use",
		ToolCalls: []ModelToolCall{
			{ID: "t", Name: "noop", Arguments: nil},
		},
	}
	model := &fakeModel{
		responses: []*ModelResponse{loopResp, loopResp, loopResp, loopResp, loopResp},
	}
	qe := NewQueryEngine(types.NewSessionID(), model, &fakeTools{}, []string{"noop"}, silentLogger())

	res, err := qe.Submit(context.Background(), "go", QueryParams{MaxTurns: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error == nil {
		t.Fatal("expected res.Error for max turns exhaustion")
	}
	if !strings.Contains(res.Error.Error(), "maximo de iteracoes") {
		t.Errorf("res.Error = %v", res.Error)
	}
	if model.calls != 2 {
		t.Errorf("model.calls = %d, want 2", model.calls)
	}
}

func TestQueryEngineClearAndSetters(t *testing.T) {
	qe := NewQueryEngine(types.NewSessionID(), &fakeModel{
		responses: []*ModelResponse{{Content: "ok", StopReason: "end_turn"}},
	}, &fakeTools{}, nil, silentLogger())

	_, err := qe.Submit(context.Background(), "hi", QueryParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(qe.GetMessages()) == 0 {
		t.Fatal("expected messages before Clear")
	}
	qe.Clear()
	if len(qe.GetMessages()) != 0 {
		t.Errorf("after Clear, messages = %d", len(qe.GetMessages()))
	}
	// SetModel / SetTools should not panic.
	qe.SetModel(&fakeModel{})
	qe.SetTools(&fakeTools{}, []string{"new"})
}

// ----- QueryConfig builder -----

func TestBuildQueryConfigDefaultsAndOptions(t *testing.T) {
	sid := types.NewSessionID()
	cfg := BuildQueryConfig(
		WithSessionID(sid),
		WithModel("claude"),
		WithFallbackModel("haiku"),
		WithMaxTurns(5),
		WithMaxBudgetUSD(1.5),
		WithTaskBudget(8000),
		WithMaxOutputTokens(4096),
		WithThinking(true, 1024),
		WithSystemPrompt("sys"),
		WithAppendSystemPrompt("extra"),
		WithQuerySource("cli"),
		WithVerbose(true),
	)

	if cfg.SessionID != sid {
		t.Errorf("SessionID mismatch")
	}
	if cfg.Model != "claude" || cfg.FallbackModel != "haiku" {
		t.Errorf("model fields wrong: %+v", cfg)
	}
	if cfg.MaxTurns != 5 || cfg.MaxBudgetUSD != 1.5 {
		t.Errorf("limits wrong: %+v", cfg)
	}
	if cfg.TaskBudget == nil || cfg.TaskBudget.Total != 8000 || cfg.TaskBudget.Remaining != -1 {
		t.Errorf("TaskBudget wrong: %+v", cfg.TaskBudget)
	}
	if cfg.MaxOutputTokens != 4096 {
		t.Errorf("MaxOutputTokens = %d", cfg.MaxOutputTokens)
	}
	if !cfg.ThinkingEnabled || cfg.ThinkingBudget != 1024 {
		t.Errorf("Thinking fields wrong")
	}
	if cfg.CustomSystemPrompt != "sys" || cfg.AppendSystemPrompt != "extra" {
		t.Errorf("system prompt fields wrong")
	}
	if cfg.QuerySource != "cli" {
		t.Errorf("QuerySource = %q", cfg.QuerySource)
	}
	if !cfg.Gates.VerboseMode {
		t.Errorf("VerboseMode not set")
	}
	// Defaults preserved
	if !cfg.Gates.AutoCompactEnabled || !cfg.Gates.FastModeEnabled {
		t.Errorf("default gates lost: %+v", cfg.Gates)
	}
}

func TestBuildQueryConfigWithGatesOverride(t *testing.T) {
	gates := RuntimeGates{StreamingToolExecution: true}
	cfg := BuildQueryConfig(WithGates(gates))
	if !cfg.Gates.StreamingToolExecution {
		t.Error("StreamingToolExecution should be true")
	}
	if cfg.Gates.AutoCompactEnabled {
		t.Error("gates override should replace defaults")
	}
}

// ----- Token budget tracker -----

func TestCheckTokenBudget_SubagentSkipped(t *testing.T) {
	tracker := NewBudgetTracker()
	d := CheckTokenBudget(tracker, "subagent-1", 10000, 500)
	if d.Action != BudgetStop {
		t.Errorf("subagent should stop, got %v", d.Action)
	}
}

func TestCheckTokenBudget_NoBudget(t *testing.T) {
	tracker := NewBudgetTracker()
	d := CheckTokenBudget(tracker, "", 0, 100)
	if d.Action != BudgetStop {
		t.Errorf("no budget should stop, got %v", d.Action)
	}
}

func TestCheckTokenBudget_Continues(t *testing.T) {
	tracker := NewBudgetTracker()
	d := CheckTokenBudget(tracker, "", 10000, 1000) // 10% used
	if d.Action != BudgetContinue {
		t.Fatalf("expected continue, got %v", d.Action)
	}
	if d.NudgeMessage == "" {
		t.Error("expected nudge message")
	}
	if !strings.Contains(d.NudgeMessage, "Token budget") {
		t.Errorf("nudge missing marker: %q", d.NudgeMessage)
	}
	if tracker.ContinuationCount != 1 {
		t.Errorf("ContinuationCount = %d", tracker.ContinuationCount)
	}
}

func TestCheckTokenBudget_StopsAboveThreshold(t *testing.T) {
	tracker := NewBudgetTracker()
	// 95% used (above 0.9 threshold)
	d := CheckTokenBudget(tracker, "", 10000, 9500)
	if d.Action != BudgetStop {
		t.Errorf("expected stop above threshold, got %v", d.Action)
	}
}

// ----- Token estimation -----

func TestEstimateMessageTokens(t *testing.T) {
	m := Message{Role: "user", Content: "hello world"}
	got := EstimateMessageTokens(m)
	// 11 + 4 = 15 chars, /4 = 3, +4 overhead = 7
	if got != 7 {
		t.Errorf("EstimateMessageTokens = %d, want 7", got)
	}
}

func TestEstimateTokenCount(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "bye"},
	}
	total := EstimateTokenCount(msgs)
	// msg1: 2+4=6, /4=1, +4=5
	// msg2: 3+9=12, /4=3, +4=7
	want := 5 + 7
	if total != want {
		t.Errorf("EstimateTokenCount = %d, want %d", total, want)
	}
}

// ----- Context budget allocation -----

func TestAllocateContextBudget(t *testing.T) {
	b := AllocateContextBudget(200_000, 1000, 2000, 8192)
	if b.Total != 200_000 {
		t.Errorf("Total = %d", b.Total)
	}
	if b.OutputReservation != 8192 {
		t.Errorf("OutputReservation = %d", b.OutputReservation)
	}
	// System with 10% padding
	if b.SystemPromptBudget != 1100 {
		t.Errorf("SystemPromptBudget = %d, want 1100", b.SystemPromptBudget)
	}
	if b.ToolDescriptionBudget != 2200 {
		t.Errorf("ToolDescriptionBudget = %d, want 2200", b.ToolDescriptionBudget)
	}
	// Conversation is the remainder
	want := 200_000 - 1100 - 2200 - 8192
	if b.ConversationBudget != want {
		t.Errorf("ConversationBudget = %d, want %d", b.ConversationBudget, want)
	}
}

func TestAllocateContextBudget_DefaultOutput(t *testing.T) {
	b := AllocateContextBudget(100_000, 0, 0, 0)
	if b.OutputReservation != 20000 {
		t.Errorf("default OutputReservation = %d, want 20000", b.OutputReservation)
	}
}

func TestCalculateTokenWarningState(t *testing.T) {
	budget := ContextBudget{ConversationBudget: 1000}
	thr := DefaultCompactionThreshold()

	safe := CalculateTokenWarningState(100, budget, thr)
	if safe.IsAtWarningLimit || safe.IsAtCompactLimit || safe.IsAtBlockingLimit {
		t.Errorf("10%% should be safe: %+v", safe)
	}

	warn := CalculateTokenWarningState(800, budget, thr)
	if !warn.IsAtWarningLimit || warn.IsAtCompactLimit {
		t.Errorf("80%% should be warning only: %+v", warn)
	}

	compact := CalculateTokenWarningState(920, budget, thr)
	if !compact.IsAtCompactLimit || compact.IsAtBlockingLimit {
		t.Errorf("92%% should be compact only: %+v", compact)
	}

	blocked := CalculateTokenWarningState(960, budget, thr)
	if !blocked.IsAtBlockingLimit {
		t.Errorf("96%% should be blocking: %+v", blocked)
	}
}

func TestCalculateTokenWarningState_ZeroBudget(t *testing.T) {
	s := CalculateTokenWarningState(10, ContextBudget{ConversationBudget: 0}, DefaultCompactionThreshold())
	if !s.IsAtBlockingLimit {
		t.Error("zero budget should be blocking")
	}
}

func TestNeedsCompaction(t *testing.T) {
	budget := ContextBudget{ConversationBudget: 100}
	thr := DefaultCompactionThreshold()

	small := []Message{{Role: "u", Content: "hi"}}
	if NeedsCompaction(small, budget, thr) {
		t.Error("small messages should not need compaction")
	}

	big := []Message{{Role: "user", Content: strings.Repeat("x", 1000)}}
	if !NeedsCompaction(big, budget, thr) {
		t.Error("large messages should need compaction")
	}
}

// ----- Tool result truncation -----

func TestTruncateToolResult_NoTruncation(t *testing.T) {
	s := "short"
	if got := TruncateToolResult(s, 100); got != s {
		t.Errorf("should not truncate: %q", got)
	}
	if got := TruncateToolResult(s, 0); got != s {
		t.Errorf("maxChars 0 should disable: %q", got)
	}
}

func TestTruncateToolResult_Truncates(t *testing.T) {
	s := strings.Repeat("a", 500) + strings.Repeat("b", 500)
	got := TruncateToolResult(s, 100)
	if !strings.Contains(got, "truncated for context budget") {
		t.Errorf("missing truncation marker: %q", got)
	}
	if len(got) >= len(s) {
		t.Errorf("truncated length %d not less than original %d", len(got), len(s))
	}
}

// ----- Stop hooks -----

func TestMaxTurnsStopHook_UnderLimit(t *testing.T) {
	h := &MaxTurnsStopHook{MaxTurns: 5}
	req := StopHookRequest{Messages: []Message{{Role: "user"}, {Role: "user"}}}
	d, err := h.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if d != nil {
		t.Errorf("expected nil decision under limit, got %+v", d)
	}
}

func TestMaxTurnsStopHook_OverLimit(t *testing.T) {
	h := &MaxTurnsStopHook{MaxTurns: 2}
	req := StopHookRequest{Messages: []Message{
		{Role: "user"}, {Role: "user"}, {Role: "user"},
	}}
	d, err := h.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil || !d.PreventContinuation {
		t.Errorf("expected PreventContinuation, got %+v", d)
	}
	if !strings.Contains(d.StopReason, "maximum number of turns") {
		t.Errorf("StopReason = %q", d.StopReason)
	}
}

func TestMaxTurnsStopHook_ZeroDisabled(t *testing.T) {
	h := &MaxTurnsStopHook{MaxTurns: 0}
	d, err := h.Evaluate(context.Background(), StopHookRequest{
		Messages: []Message{{Role: "user"}, {Role: "user"}, {Role: "user"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d != nil {
		t.Errorf("MaxTurns=0 should disable, got %+v", d)
	}
}

func TestAPIErrorStopHook_Triggers(t *testing.T) {
	h := &APIErrorStopHook{}
	req := StopHookRequest{AssistantMessages: []Message{
		{Role: "assistant", IsAPIError: true, Content: "rate limited"},
	}}
	d, err := h.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil || !d.PreventContinuation {
		t.Errorf("expected prevent, got %+v", d)
	}
}

func TestAPIErrorStopHook_PassesThrough(t *testing.T) {
	h := &APIErrorStopHook{}
	d, err := h.Evaluate(context.Background(), StopHookRequest{
		AssistantMessages: []Message{{Role: "assistant", Content: "ok"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d != nil {
		t.Errorf("expected nil decision, got %+v", d)
	}
	// Empty assistant messages
	d2, _ := h.Evaluate(context.Background(), StopHookRequest{})
	if d2 != nil {
		t.Errorf("empty messages should return nil decision")
	}
}

func TestHandleStopHooks_PriorityAndPrevent(t *testing.T) {
	cfg := StopHookConfig{
		Hooks: []StopHook{
			&MaxTurnsStopHook{MaxTurns: 1},
		},
		Logger: silentLogger(),
	}
	req := StopHookRequest{Messages: []Message{
		{Role: "user"}, {Role: "user"},
	}}
	res := HandleStopHooks(context.Background(), cfg, req)
	if !res.PreventContinuation {
		t.Errorf("expected PreventContinuation, got %+v", res)
	}
	if res.StopReason == "" {
		t.Errorf("expected StopReason")
	}
}

func TestHandleStopHooks_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := StopHookConfig{
		Hooks:  []StopHook{&MaxTurnsStopHook{MaxTurns: 1}},
		Logger: silentLogger(),
	}
	res := HandleStopHooks(ctx, cfg, StopHookRequest{Messages: []Message{{Role: "user"}}})
	if !res.PreventContinuation {
		t.Error("cancelled context should prevent continuation")
	}
	if res.StopReason != "cancelled" {
		t.Errorf("StopReason = %q, want cancelled", res.StopReason)
	}
}

func TestHandleStopHooks_NoHooks(t *testing.T) {
	res := HandleStopHooks(context.Background(), StopHookConfig{Logger: silentLogger()}, StopHookRequest{})
	if res.PreventContinuation {
		t.Error("empty hooks should not prevent continuation")
	}
	if len(res.BlockingErrors) != 0 {
		t.Error("empty hooks should not produce blocking errors")
	}
}

// ----- Deps validation and defaults -----

func TestQueryDepsValidate(t *testing.T) {
	// Missing CallModel → error
	d := QueryDeps{}
	if err := d.Validate(); err == nil {
		t.Error("expected error for missing CallModel")
	}

	// With CallModel, other defaults are populated.
	d = QueryDeps{
		CallModel: func(_ context.Context, _ ModelStreamRequest) (<-chan StreamEvent, error) {
			ch := make(chan StreamEvent)
			close(ch)
			return ch, nil
		},
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	if d.Microcompact == nil || d.Autocompact == nil || d.UUID == nil || d.PersistSession == nil || d.Logger == nil {
		t.Error("Validate should populate default fields")
	}
	if id := d.UUID(); id == "" {
		t.Error("default UUID returned empty string")
	}
}

func TestDefaultDeps(t *testing.T) {
	d := DefaultDeps(silentLogger())
	if d.CallModel != nil {
		t.Error("DefaultDeps should leave CallModel nil")
	}
	if d.Microcompact == nil || d.Autocompact == nil {
		t.Error("DefaultDeps should populate compaction funcs")
	}
	// Default microcompact is a no-op that returns the input.
	mr, err := d.Microcompact(context.Background(), []Message{{Role: "user", Content: "x"}}, MicrocompactOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if mr.Modified || mr.TokensRemoved != 0 {
		t.Errorf("default microcompact should be no-op: %+v", mr)
	}
	// Default autocompact returns nil.
	ar, err := d.Autocompact(context.Background(), nil, AutocompactOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if ar != nil {
		t.Errorf("default autocompact should return nil")
	}
	// Default persist is no-op.
	if err := d.PersistSession(context.Background(), nil); err != nil {
		t.Errorf("default persist error: %v", err)
	}
}

// ----- Retry helper -----

func TestRetryWithBackoff_SuccessFirst(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:    3,
		BaseBackoff:   1,
		BackoffFactor: 1,
		IsRetryable:   func(error) bool { return true },
	}
	calls := 0
	err := RetryWithBackoff(context.Background(), cfg, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestRetryWithBackoff_NonRetryable(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:    3,
		BaseBackoff:   1,
		BackoffFactor: 1,
		IsRetryable:   func(error) bool { return false },
	}
	calls := 0
	target := errors.New("fatal")
	err := RetryWithBackoff(context.Background(), cfg, func() error {
		calls++
		return target
	})
	if !errors.Is(err, target) {
		t.Errorf("err = %v, want %v", err, target)
	}
	if calls != 1 {
		t.Errorf("non-retryable should call once, got %d", calls)
	}
}

func TestDefaultRetryConfigIsRetryable(t *testing.T) {
	cfg := DefaultRetryConfig()
	if cfg.IsRetryable(nil) {
		t.Error("nil should not be retryable")
	}
	if !cfg.IsRetryable(errors.New("status 503")) {
		t.Error("503 should be retryable")
	}
	if !cfg.IsRetryable(errors.New("request timeout")) {
		t.Error("timeout should be retryable")
	}
	if cfg.IsRetryable(errors.New("invalid api key")) {
		t.Error("auth error should not be retryable by default patterns")
	}
}
