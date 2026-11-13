package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/commands"
	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/projectchecks"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/turncheckpoint"
	"github.com/costa/polypod/internal/types"
)

func setupAutofixTestEnv(t *testing.T, writeSequence []string) (string, adapter.MessageHandler, adapter.StreamHandler) {
	t.Helper()
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	targetPath := filepath.Join(projectDir, "calc.go")
	if err := os.WriteFile(targetPath, []byte("package calc\n\nfunc Sum(a, b int) int { return a - b }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var round atomic.Int32
	var writeIdx atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		isStream := strings.Contains(string(bodyBytes), `"stream":true`)
		step := round.Add(1)
		// Odd rounds emit a file_write tool call; even rounds conclude the turn.
		if step%2 == 1 {
			idx := int(writeIdx.Add(1)) - 1
			if idx >= len(writeSequence) {
				idx = len(writeSequence) - 1
			}
			argsJSON, _ := json.Marshal(map[string]any{
				"path":    targetPath,
				"content": writeSequence[idx],
			})
			if isStream {
				w.Header().Set("Content-Type", "text/event-stream")
				chunk, _ := json.Marshal(map[string]any{
					"id": "1", "object": "chat.completion.chunk",
					"choices": []map[string]any{{
						"index": 0,
						"delta": map[string]any{
							"tool_calls": []map[string]any{{
								"index": 0, "id": fmt.Sprintf("call_%d", step), "type": "function",
								"function": map[string]any{"name": "write_file", "arguments": string(argsJSON)},
							}},
						},
					}},
				})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
				_, _ = io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			resp, _ := json.Marshal(map[string]any{
				"id": "1", "object": "chat.completion",
				"choices": []map[string]any{{
					"index":         0,
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []map[string]any{{
							"id": fmt.Sprintf("call_%d", step), "type": "function",
							"function": map[string]any{"name": "write_file", "arguments": string(argsJSON)},
						}},
					},
				}},
			})
			_, _ = w.Write(resp)
			return
		}

		if isStream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"2\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"proposta aplicada\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"id\":\"2\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"2","object":"chat.completion","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"proposta aplicada"}}]}`)
	}))
	t.Cleanup(srv.Close)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	turns := turncheckpoint.New(t.TempDir(), logger)
	reg := tool.NewRegistry()
	tool.RegisterBuiltinTools(reg, projectDir)
	tool.RegisterFileEditTools(reg, projectDir)
	reg.SetBeforeFileWrite(func(toolCtx types.ToolContext, path string, original []byte, existed bool) error {
		if toolCtx.Context == nil {
			return nil
		}
		return turns.CaptureBefore(toolCtx.Context, path, original, existed)
	})
	adapterReg := ai.NewToolRegistryAdapter(reg, nil)
	agents := agent.NewRegistry()
	agents.Register(&agent.Agent{
		Name:    "default",
		Persona: "assistente",
		Skills:  []string{"write_file", "edit_file", "file_write", "file_edit", "undo_edit"},
	})
	client := ai.NewClient(config.AIConfig{
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  srv.URL + "/v1",
		Model:    "gpt-test",
		MaxToks:  256,
		Tools:    true,
	}, adapterReg)
	aiSvc := ai.NewService(client, nil, agents, "default", nil, logger)

	convMgr := conversation.NewManager(conversation.NewStore(nil, nil, ""), logger)
	runChecks := func(checkCtx context.Context, dir, event string) (string, bool, error) {
		results, err := projectchecks.Run(checkCtx, dir, event)
		failed := false
		for _, r := range results {
			if r.Err != nil {
				failed = true
			}
		}
		return projectchecks.FormatResults(results), failed, err
	}
	msgH := makeHandlerWithChecks(convMgr, aiSvc, nil, nil, turns, nil, logger, runChecks)
	streamH := makeStreamHandlerWithEvents(convMgr, aiSvc, nil, nil, turns, nil, logger, runChecks)
	return projectDir, msgH, streamH
}

func TestAutofixPRGate_KeepsVerifiedFix(t *testing.T) {
	good := "package calc\n\nfunc Sum(a, b int) int { return a + b }\n"
	projectDir, _, streamH := setupAutofixTestEnv(t, []string{good})
	if err := os.MkdirAll(filepath.Join(projectDir, ".polypod"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".polypod", "verifiers.yaml"), []byte("verifiers:\n  check: \"grep -q 'a + b' calc.go\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	chunks := make(chan adapter.StreamChunk, 64)
	streamH(context.Background(), adapter.InMessage{
		Channel: "cli",
		UserID:  "local",
		Text:    "[COMANDO AUTONOMO /autofix-pr]\nCorrija o PR #1",
	}, chunks)
	var out strings.Builder
	for ch := range chunks {
		if ch.Error != nil {
			t.Fatalf("unexpected error: %v", ch.Error)
		}
		out.WriteString(ch.Delta)
	}
	if !strings.Contains(out.String(), "CORREÇÃO VERIFICADA") {
		t.Fatalf("expected verified banner, got:\n%s", out.String())
	}
	data, err := os.ReadFile(filepath.Join(projectDir, "calc.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != good {
		t.Fatalf("expected verified edit to stay on disk, got:\n%s", string(data))
	}
	if entries, _ := os.ReadDir(filepath.Join(projectDir, ".polypod", "held-proposals")); len(entries) != 0 {
		t.Fatalf("did not expect held proposal when verified, found %d", len(entries))
	}
}

func TestAutofixPRGate_RollsBackAndHoldsProposalWhenVerifiersFail(t *testing.T) {
	bad1 := "package calc\n\nfunc Sum(a, b int) int { return a * b }\n"
	bad2 := "package calc\n\nfunc Sum(a, b int) int { return a / b }\n"
	projectDir, msgH, _ := setupAutofixTestEnv(t, []string{bad1, bad2})
	if err := os.MkdirAll(filepath.Join(projectDir, ".polypod"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".polypod", "verifiers.yaml"), []byte("verifiers:\n  check: \"grep -q 'a + b' calc.go\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := msgH(context.Background(), adapter.InMessage{
		Channel: "headless",
		UserID:  "cli",
		Text:    "/autofix-pr 1",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !strings.Contains(out.Text, "PROPOSTA RETIDA — NÃO VERIFICADA") {
		t.Fatalf("expected retained banner, got:\n%s", out.Text)
	}
	data, err := os.ReadFile(filepath.Join(projectDir, "calc.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "return a - b") {
		t.Fatalf("expected unverified edit to be rolled back to original content, got:\n%s", string(data))
	}
	entries, err := os.ReadDir(filepath.Join(projectDir, ".polypod", "held-proposals"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 held proposal patch, got %v (err=%v)", len(entries), err)
	}
	patchBytes, err := os.ReadFile(filepath.Join(projectDir, ".polypod", "held-proposals", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patchBytes), "-func Sum(a, b int) int { return a - b }") ||
		!strings.Contains(string(patchBytes), "+func Sum(a, b int) int { return a / b }") {
		t.Fatalf("held patch did not capture full pre-turn -> post-attempt diff:\n%s", string(patchBytes))
	}
}

func TestAutofixPRGate_RollsBackWhenNoVerifiersConfigured(t *testing.T) {
	candidate := "package calc\n\nfunc Sum(a, b int) int { return a + b }\n"
	projectDir, _, streamH := setupAutofixTestEnv(t, []string{candidate})

	chunks := make(chan adapter.StreamChunk, 64)
	streamH(context.Background(), adapter.InMessage{
		Channel: "headless",
		UserID:  "cli",
		Text:    "[COMANDO AUTONOMO /autofix-pr]\nCorrija o PR #2",
	}, chunks)
	var out strings.Builder
	for ch := range chunks {
		if ch.Error != nil {
			t.Fatalf("unexpected error: %v", ch.Error)
		}
		out.WriteString(ch.Delta)
	}
	if !strings.Contains(out.String(), "PROPOSTA RETIDA — NÃO VERIFICADA") {
		t.Fatalf("expected retained banner when verifiers missing, got:\n%s", out.String())
	}
	data, _ := os.ReadFile(filepath.Join(projectDir, "calc.go"))
	if !strings.Contains(string(data), "return a - b") {
		t.Fatalf("expected rollback when verifiers missing, got:\n%s", string(data))
	}
}

func TestResolveHeadlessSlashCommand(t *testing.T) {
	reg := command.NewRegistry()
	commands.RegisterAutonomousCommands(reg)
	expanded, localOut, handled, err := resolveHeadlessSlashCommand(reg, "/tmp/work", "sess-1", "/autofix-pr 77")
	if err != nil {
		t.Fatalf("resolveHeadlessSlashCommand: %v", err)
	}
	if handled || localOut != "" || !strings.HasPrefix(expanded, "[COMANDO AUTONOMO /autofix-pr]") || !strings.Contains(expanded, "Solicitacao: 77") {
		t.Fatalf("unexpected expansion: handled=%v expanded=%q", handled, expanded)
	}
}
