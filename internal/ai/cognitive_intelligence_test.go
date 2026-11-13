package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
	"github.com/costa/polypod/internal/workdir"
)

type scriptedProvider struct {
	provider.Provider
	requests []provider.ChatRequest
	step     func(callIdx int, req provider.ChatRequest) (*provider.ChatResponse, error)
}

func (p *scriptedProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	return p.step(len(p.requests), req)
}

func (p *scriptedProvider) SupportsVision() bool { return false }

func TestStuckLoopDetector_InjectsImpasseAlertAndEscalatesThinkingBudget(t *testing.T) {
	backend := &scriptedProvider{}
	backend.step = func(callIdx int, req provider.ChatRequest) (*provider.ChatResponse, error) {
		switch callIdx {
		case 1, 2:
			return &provider.ChatResponse{
				ToolCalls: []provider.ToolCall{{
					ID:        fmt.Sprintf("call-%d", callIdx),
					Name:      "bash",
					Arguments: `{"command":"false"}`,
				}},
			}, nil
		default:
			return &provider.ChatResponse{Content: "mudei de estratégia e resolvi"}, nil
		}
	}

	reg := tool.NewRegistry()
	skills := &failingSkillRegistry{}
	_ = reg

	client := &Client{
		responseProvider: backend,
		model:            "gemini-2.5-pro",
		skills:           skills,
		toolsOn:          true,
		maxToolIter:      8,
	}

	resp, err := client.CompleteWithTools(context.Background(), []ChatMessage{
		{Role: "user", Content: "corrija o build"},
	}, slog.Default())
	if err != nil {
		t.Fatalf("CompleteWithTools error: %v", err)
	}
	if resp.Content != "mudei de estratégia e resolvi" {
		t.Fatalf("unexpected final response: %q", resp.Content)
	}
	if len(backend.requests) != 3 {
		t.Fatalf("expected 3 provider requests, got %d", len(backend.requests))
	}

	thirdReq := backend.requests[2]
	if thirdReq.ThinkingBudget != 8192 || thirdReq.ReasoningEffort != "high" {
		t.Fatalf("expected escalated ThinkingBudget=8192 & ReasoningEffort=high after impasse, got budget=%d effort=%q",
			thirdReq.ThinkingBudget, thirdReq.ReasoningEffort)
	}
	foundImpasse := false
	for _, m := range thirdReq.Messages {
		if strings.Contains(m.Content, CognitiveImpasseHeader) {
			foundImpasse = true
			break
		}
	}
	if !foundImpasse {
		t.Fatalf("expected %s injected into 3rd request messages", CognitiveImpasseHeader)
	}
}

type failingSkillRegistry struct {
	resumeTestSkills
}

func (f *failingSkillRegistry) Execute(name, _ string) (string, error) {
	return "Erro: exit status 1: comando falhou", nil
}

func TestVerificationGateAndVerifiedRepoPlaybook_EndToEnd(t *testing.T) {
	repoDir := t.TempDir()
	dataDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module example.com/selfheal\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}

	reg := tool.NewRegistry()
	tool.RegisterFileEditTools(reg, repoDir)
	adapter := NewToolRegistryAdapter(reg, nil)
	playbook := NewRepoPlaybookStore(dataDir)

	targetFile := filepath.Join(repoDir, "handler.go")
	brokenArgs, _ := json.Marshal(map[string]any{
		"file_path": targetFile,
		"content":   "package selfheal\n\n// token=sk-secret1234567890abcdefghijklmnop\nfunc Handle( {\n}\n",
	})
	fixedArgs, _ := json.Marshal(map[string]any{
		"file_path": targetFile,
		"content":   "package selfheal\n\nfunc Handle() string {\n\treturn \"ok\"\n}\n",
	})

	backend := &scriptedProvider{}
	backend.step = func(callIdx int, req provider.ChatRequest) (*provider.ChatResponse, error) {
		switch callIdx {
		case 1:
			// Step 1: Model writes broken Go file.
			return &provider.ChatResponse{
				ToolCalls: []provider.ToolCall{{
					ID:        "write-broken",
					Name:      "file_write",
					Arguments: string(brokenArgs),
				}},
			}, nil
		case 2:
			// Step 2: Model prematurely tries to finish without fixing syntax error!
			// Verification gate must block completion and demand repair.
			hasDiagInToolMsg := false
			for _, m := range req.Messages {
				if m.Role == "tool" && strings.Contains(m.Content, "[AUTO-DIAGNÓSTICO PÓS-EDIÇÃO") {
					hasDiagInToolMsg = true
				}
			}
			if !hasDiagInToolMsg {
				t.Errorf("expected post-edit auto-diagnostic in tool result on step 2")
			}
			return &provider.ChatResponse{Content: "Pronto, terminei!"}, nil
		case 3:
			// Step 3: Model receives [PORTÃO DE VERIFICAÇÃO PRÉ-CONCLUSÃO] and fixes the file.
			sawGate := false
			for _, m := range req.Messages {
				if strings.Contains(m.Content, VerificationGateHeader) {
					sawGate = true
				}
			}
			if !sawGate {
				t.Errorf("expected %s on step 3 after premature completion attempt", VerificationGateHeader)
			}
			return &provider.ChatResponse{
				ToolCalls: []provider.ToolCall{{
					ID:        "write-fixed",
					Name:      "file_write",
					Arguments: string(fixedArgs),
				}},
			}, nil
		default:
			// Step 4: File is now valid Go; completion succeeds cleanly.
			return &provider.ChatResponse{
				Content: "Corrigida a assinatura de func Handle() em handler.go após validação sintática.",
			}, nil
		}
	}

	client := &Client{
		responseProvider: backend,
		model:            "gemini-2.5-pro",
		skills:           adapter,
		toolsOn:          true,
		maxToolIter:      10,
		repoPlaybook:     playbook,
	}

	ctx := workdir.WithFixedDir(context.Background(), repoDir)
	ctx = withRequestQueryHint(ctx, "implementar handler.go no pacote selfheal com token=sk-secret1234567890abcdefghijklmnop")

	resp, err := client.CompleteWithTools(ctx, []ChatMessage{
		{Role: "user", Content: "implementar handler.go"},
	}, slog.Default())
	if err != nil {
		t.Fatalf("CompleteWithTools failed: %v", err)
	}
	if !strings.Contains(resp.Content, "Corrigida a assinatura") {
		t.Fatalf("unexpected final response: %q", resp.Content)
	}
	if len(backend.requests) != 4 {
		t.Fatalf("expected 4 steps (write broken -> blocked completion -> write fixed -> clean completion), got %d", len(backend.requests))
	}

	// Verify RepoPlaybook recorded the verified recovery episode with 0600 permissions and secret scrubbing!
	repos, totalLessons := playbook.Stats()
	if repos != 1 || totalLessons != 1 {
		t.Fatalf("expected 1 repo playbook with 1 verified lesson, got repos=%d lessons=%d", repos, totalLessons)
	}

	injected := playbook.RelevantLessons(repoDir, "ajustar handler.go", 4)
	if !strings.Contains(injected, "PLAYBOOK DE ENGENHARIA VERIFICADO") || !strings.Contains(injected, "handler.go") {
		t.Fatalf("expected RelevantLessons to include handler.go recovery lesson, got:\n%s", injected)
	}
	if strings.Contains(injected, "sk-secret1234567890") {
		t.Fatalf("secret leaked into RepoPlaybook lesson: %s", injected)
	}
}

func TestIntentAwareToolCatalog_ProgressiveSelection(t *testing.T) {
	tmp := t.TempDir()
	reg := tool.NewRegistry()
	tool.RegisterFileEditTools(reg, tmp)
	tool.RegisterImpactTool(reg, tmp)
	tool.RegisterCognitiveTools(reg, tmp, tool.CognitiveToolDeps{
		ArchitectExec:         func(context.Context, string, string) (string, error) { return "plano", nil },
		WorktreeCandidateExec: func(context.Context, string, string, string) (string, error) { return "patch", nil },
	})

	registerDummy := func(name string) {
		reg.RegisterDef(types.ToolDef{
			NameFn:        func() string { return name },
			DescriptionFn: func() string { return "ferramenta auxiliar " + name },
			InputSchemaFn: func() map[string]any { return map[string]any{"type": "object", "properties": map[string]any{}} },
			ExecuteFn:     func(types.ToolContext, map[string]any) (types.ToolResult, error) { return types.ToolResult{}, nil },
		})
	}

	// Register tool_search + dummy tools so catalog exceeds inlineToolLimit
	registerDummy("tool_search")
	for i := 0; i < 32; i++ {
		registerDummy(fmt.Sprintf("extra_mcp_tool_%02d", i))
	}
	registerDummy("browser_act")
	registerDummy("git_commit")

	adapter := NewToolRegistryAdapter(reg, nil)
	client := &Client{skills: adapter, toolsOn: true}

	// 1. Pure Go refactoring prompt: includes cognitive/impact tools, omits browser_act.
	codeCtx := withRequestQueryHint(context.Background(), "refatore o pacote auth e analise impacto")
	codeCat := client.catalogFor(codeCtx)
	if !codeCat.progressive {
		t.Fatalf("expected progressive catalog when >%d tools registered", inlineToolLimit)
	}
	codeActive := activeToolNameSet(codeCat)
	for _, mustHave := range []string{"code_impact", "architect_plan", "parallel_best_of_n", "tool_search"} {
		if !codeActive[mustHave] {
			t.Errorf("expected core intelligence tool %q active inline", mustHave)
		}
	}
	if codeActive["browser_act"] {
		t.Errorf("expected bulky GUI tool browser_act deferred on pure Go refactor prompt")
	}

	// 2. Web/Browser prompt: auto-activates browser_act without requiring extra tool_search turn!
	webCtx := withRequestQueryHint(context.Background(), "abra o navegador em https://localhost:3000 e teste login")
	webCat := client.catalogFor(webCtx)
	webActive := activeToolNameSet(webCat)
	if !webActive["browser_act"] {
		t.Fatalf("expected browser_act auto-activated by web/browser prompt intent")
	}
}

func activeToolNameSet(cat *toolCatalog) map[string]bool {
	out := make(map[string]bool)
	for _, td := range cat.definitions() {
		if td.Function != nil {
			out[td.Function.Name] = true
		}
	}
	return out
}
