package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/turncheckpoint"
	openai "github.com/sashabaranov/go-openai"
)

type resumeTestProvider struct {
	provider.Provider
	requests []provider.ChatRequest
	answer   *provider.ChatResponse
}

func (p *resumeTestProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	return p.answer, nil
}
func (p *resumeTestProvider) SupportsVision() bool { return false }

type resumeTestSkills struct {
	called        []string
	onDefinitions func()
	definitions   []openai.Tool
}

func (s *resumeTestSkills) ToolDefinitions([]string) []openai.Tool {
	if s.onDefinitions != nil {
		s.onDefinitions()
	}
	return s.definitions
}
func (s *resumeTestSkills) Execute(name, _ string) (string, error) {
	s.called = append(s.called, name)
	return name + "-result", nil
}

func checkpointFixture(t *testing.T, manager *turncheckpoint.Manager, next int, pending string) {
	t.Helper()
	messages := []provider.Message{
		{Role: "system", Content: "act"},
		{Role: "user", Content: "do two things"},
		{Role: "assistant", ToolCalls: []provider.ToolCall{
			{ID: "first-id", Name: "first", Arguments: "{}"},
			{ID: "second-id", Name: "second", Arguments: "{}"},
		}},
	}
	if next > 0 {
		messages = append(messages, provider.Message{Role: "tool", ToolCallID: "first-id", Name: "first", Content: "first-result"})
	}
	raw, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := (&Client{skills: &resumeTestSkills{}}).toolDefinitionsDigest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cp := &turncheckpoint.AICheckpoint{
		Version: turncheckpoint.AIVersion, SessionID: "branch", TurnID: "turn", Format: "provider",
		Model: "model", Messages: raw, Phase: turncheckpoint.AIPhaseTools, NextToolIndex: next,
		PendingToolCallID: pending, ToolDefinitionsSHA256: digest,
	}
	if err := manager.SaveAI(cp); err != nil {
		t.Fatal(err)
	}
}

func TestSynchronousCLITurnUsesDurableCheckpoint(t *testing.T) {
	manager := turncheckpoint.New(t.TempDir(), nil)
	backend := &resumeTestProvider{answer: &provider.ChatResponse{Content: "done"}}
	client := &Client{responseProvider: backend, model: "model", skills: &resumeTestSkills{}, toolsOn: true}
	ctx := WithTurnCheckpoint(context.Background(), manager, "branch", "turn")
	response, err := client.CompleteWithTools(ctx, []ChatMessage{{Role: "user", Content: "work"}}, slog.Default())
	if err != nil || response.Content != "done" {
		t.Fatalf("synchronous CLI turn failed: %+v, %v", response, err)
	}
	checkpoint, err := manager.LoadAI("branch", "turn")
	if err != nil || checkpoint.Phase != turncheckpoint.AIPhaseDone || checkpoint.FinalContent != "done" {
		t.Fatalf("synchronous CLI turn skipped durable boundary: %+v, %v", checkpoint, err)
	}
}

func TestCheckpointedProviderResumesOnlyRemainingTool(t *testing.T) {
	manager := turncheckpoint.New(t.TempDir(), nil)
	checkpointFixture(t, manager, 1, "")
	p := &resumeTestProvider{answer: &provider.ChatResponse{Content: "finished"}}
	skills := &resumeTestSkills{}
	client := &Client{responseProvider: p, model: "other-model", skills: skills, toolsOn: true}
	ctx := WithResumeTurnCheckpoint(context.Background(), manager, "branch", "turn")
	resp, err := client.CompleteWithToolsStream(ctx, nil, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "finished" || len(skills.called) != 1 || skills.called[0] != "second" {
		t.Fatalf("resume response %q, tools %v", resp.Content, skills.called)
	}
	if len(p.requests) != 1 || p.requests[0].Model != "model" || len(p.requests[0].Messages) != 5 {
		t.Fatalf("model request lost saved transcript: %+v", p.requests)
	}
	if p.requests[0].Messages[3].Content != "first-result" || p.requests[0].Messages[4].Content != "second-result" {
		t.Fatalf("tool results were not preserved in order: %+v", p.requests[0].Messages)
	}
	cp, err := manager.LoadAI("branch", "turn")
	if err != nil || cp.Phase != turncheckpoint.AIPhaseDone || cp.FinalContent != "finished" {
		t.Fatalf("final checkpoint = %+v, %v", cp, err)
	}
}

func TestCheckpointedProviderNeverReplaysPendingTool(t *testing.T) {
	manager := turncheckpoint.New(t.TempDir(), nil)
	checkpointFixture(t, manager, 1, "second-id")
	p := &resumeTestProvider{answer: &provider.ChatResponse{Content: "wrong"}}
	skills := &resumeTestSkills{}
	client := &Client{responseProvider: p, model: "model", skills: skills, toolsOn: true}
	ctx := WithResumeTurnCheckpoint(context.Background(), manager, "branch", "turn")
	_, err := client.CompleteWithToolsStream(ctx, nil, nil, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "second-id") {
		t.Fatalf("pending call should block resume: %v", err)
	}
	if len(skills.called) != 0 || len(p.requests) != 0 {
		t.Fatalf("pending call was replayed: tools=%v requests=%v", skills.called, p.requests)
	}
}

func TestCheckpointedProviderBlocksChangedToolDefinition(t *testing.T) {
	manager := turncheckpoint.New(t.TempDir(), nil)
	checkpointFixture(t, manager, 0, "")
	p := &resumeTestProvider{answer: &provider.ChatResponse{Content: "wrong"}}
	skills := &resumeTestSkills{definitions: []openai.Tool{{Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{Name: "first", Description: "changed behavior", Parameters: map[string]any{"type": "object"}}}}}
	client := &Client{responseProvider: p, model: "model", skills: skills, toolsOn: true}
	ctx := WithResumeTurnCheckpoint(context.Background(), manager, "branch", "turn")
	_, err := client.CompleteWithToolsStream(ctx, nil, nil, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "ferramentas mudaram") {
		t.Fatalf("changed tool definition was accepted: %v", err)
	}
	if len(skills.called) != 0 || len(p.requests) != 0 {
		t.Fatalf("changed tool ran: tools=%v requests=%v", skills.called, p.requests)
	}
}

func TestCheckpointedProviderResumesAfterModelBeforeAnyTool(t *testing.T) {
	manager := turncheckpoint.New(t.TempDir(), nil)
	checkpointFixture(t, manager, 0, "")
	p := &resumeTestProvider{answer: &provider.ChatResponse{Content: "finished"}}
	skills := &resumeTestSkills{}
	client := &Client{responseProvider: p, model: "model", skills: skills, toolsOn: true}
	ctx := WithResumeTurnCheckpoint(context.Background(), manager, "branch", "turn")
	resp, err := client.CompleteWithToolsStream(ctx, nil, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "finished" || len(skills.called) != 2 || skills.called[0] != "first" || skills.called[1] != "second" {
		t.Fatalf("resume from model boundary: response %q, tools %v", resp.Content, skills.called)
	}
	if len(p.requests) != 1 || len(p.requests[0].Messages) != 5 {
		t.Fatalf("expected both tool results before next model request: %+v", p.requests)
	}
}

func TestCheckpointWriteFailureStopsBeforeToolEffect(t *testing.T) {
	root := t.TempDir()
	manager := turncheckpoint.New(root, nil)
	checkpointFixture(t, manager, 0, "")
	p := &resumeTestProvider{answer: &provider.ChatResponse{Content: "should not run"}}
	skills := &resumeTestSkills{onDefinitions: func() {
		var checkpointPath string
		if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.Name() == "ai.json" {
				checkpointPath = path
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if checkpointPath == "" {
			t.Fatal("checkpoint not found")
		}
		if err := os.Remove(checkpointPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(checkpointPath, 0700); err != nil {
			t.Fatal(err)
		}
	}}
	client := &Client{responseProvider: p, model: "model", skills: skills, toolsOn: true}
	ctx := WithResumeTurnCheckpoint(context.Background(), manager, "branch", "turn")
	_, err := client.CompleteWithToolsStream(ctx, nil, nil, slog.Default())
	if err == nil {
		t.Fatalf("expected persistence failure before tool: %v", err)
	}
	if len(skills.called) != 0 || len(p.requests) != 0 {
		t.Fatalf("tool or provider ran after checkpoint failure: tools=%v requests=%v", skills.called, p.requests)
	}
}

func TestCheckpointedOpenAIStreamPersistsDoneWithoutReplay(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"write-id","type":"function","function":{"name":"write","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"complete"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	manager := turncheckpoint.New(t.TempDir(), nil)
	skills := &resumeTestSkills{}
	client := NewClient(config.AIConfig{BaseURL: server.URL, Model: "test-model", Tools: true}, skills)
	ctx := WithTurnCheckpoint(context.Background(), manager, "branch", "turn")
	resp, err := client.CompleteWithToolsStream(ctx, []ChatMessage{{Role: "user", Content: "write once"}}, func(string) {}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "complete" || requests != 2 || len(skills.called) != 1 || skills.called[0] != "write" {
		t.Fatalf("fresh turn: response=%q requests=%d tools=%v", resp.Content, requests, skills.called)
	}
	cp, err := manager.LoadAI("branch", "turn")
	if err != nil || cp.Phase != turncheckpoint.AIPhaseDone || cp.FinalContent != "complete" {
		t.Fatalf("done checkpoint: %+v, %v", cp, err)
	}
	ctx = WithResumeTurnCheckpoint(context.Background(), manager, "branch", "turn")
	resp, err = client.CompleteWithToolsStream(ctx, nil, nil, slog.Default())
	if err != nil || resp.Content != "complete" || requests != 2 || len(skills.called) != 1 {
		t.Fatalf("completed turn replayed: response=%+v err=%v requests=%d tools=%v", resp, err, requests, skills.called)
	}
}
