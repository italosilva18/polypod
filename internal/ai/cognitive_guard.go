package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/projectchecks"
	providerapi "github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/smartroute"
	"github.com/costa/polypod/internal/workdir"
	openai "github.com/sashabaranov/go-openai"
)

const (
	// CognitiveImpasseHeader marks automatic stuck-loop replanning guidance.
	CognitiveImpasseHeader = "[ALERTA DE IMPASSE COGNITIVO]"
	// VerificationGateHeader marks pre-completion verification failures.
	VerificationGateHeader = "[PORTÃO DE VERIFICAÇÃO PRÉ-CONCLUSÃO]"
)

type requestEffortKey struct{}

// WithEffort attaches a reasoning effort level ("low", "medium", "high", "max")
// to ctx for the duration of a turn.
func WithEffort(ctx context.Context, effort string) context.Context {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestEffortKey{}, effort)
}

// EffortFromContext returns any turn-scoped reasoning effort level.
func EffortFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(requestEffortKey{}).(string)
	return v
}

func (c *Client) effectiveReasoning(ctx context.Context, boosted bool) (effort string, budget int) {
	if boosted {
		return "high", 8192
	}
	explicit := EffortFromContext(ctx)
	if explicit == "" {
		explicit = c.defaultEffort()
	}
	if explicit != "" {
		return smartroute.BudgetForEffort(explicit)
	}
	if q := requestQueryHintFromContext(ctx); q != "" {
		comp := smartroute.ClassifyComplexity(q)
		if comp == smartroute.ComplexityHigh {
			return smartroute.RecommendedEffort(comp)
		}
	}
	return "", 0
}

func (c *Client) applyReasoningToProviderReq(ctx context.Context, req *providerapi.ChatRequest, boosted bool) {
	if req == nil {
		return
	}
	eff, budget := c.effectiveReasoning(ctx, boosted)
	if eff != "" {
		req.ReasoningEffort = eff
	}
	if budget > 0 {
		req.ThinkingBudget = budget
	}
}

func (c *Client) applyReasoningToOpenAIReq(ctx context.Context, req *openai.ChatCompletionRequest, boosted bool) {
	if req == nil {
		return
	}
	eff, _ := c.effectiveReasoning(ctx, boosted)
	if eff == "" {
		return
	}
	m := strings.ToLower(strings.TrimSpace(req.Model))
	for _, prefix := range []string{"o1", "o3", "o4", "gpt-5", "gpt-6", "openai/o1", "openai/o3", "openai/o4"} {
		if strings.HasPrefix(m, prefix) {
			req.ReasoningEffort = eff
			return
		}
	}
	if strings.Contains(m, "reasoner") || strings.Contains(m, "qwq") || strings.Contains(m, "deepseek-r1") {
		req.ReasoningEffort = eff
	}
}

type guardedCall struct {
	Name      string
	Arguments string
}

type callFingerprint struct {
	name       string
	argsHash   string
	resultHash string
	failed     bool
}

// cognitiveGuard monitors a single multi-step AI turn for:
//  1. Stuck tool loops (identical failing calls, A->B->A oscillations, error streaks)
//  2. Unresolved post-edit syntax/type diagnostics (`[AUTO-DIAGNÓSTICO PÓS-EDIÇÃO`)
//  3. Turn-end project verification gates (`on_turn` verifiers)
//  4. Verified error-recovery episodes suitable for RepoPlaybook persistence.
type cognitiveGuard struct {
	history           []callFingerprint
	callFailCounts    map[string]int
	consecutiveErrors int
	editedFiles       map[string]bool
	dirtyDiagnostics  map[string]string
	sawFailure        bool
	firstFailure      string
	lastCleanSummary  string
	loopAlerts        int
	gateInterventions int
	boostedReasoning  bool
}

func newCognitiveGuard() *cognitiveGuard {
	return &cognitiveGuard{
		callFailCounts:   make(map[string]int),
		editedFiles:      make(map[string]bool),
		dirtyDiagnostics: make(map[string]string),
	}
}

var fileMutatingToolNames = map[string]bool{
	"write_file": true,
	"edit_file":  true,
	"file_write": true,
	"file_edit":  true,
	"multi_edit": true,
}

func extractEditedFilePath(ctx context.Context, argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	raw, _ := args["path"].(string)
	if raw == "" {
		raw, _ = args["file_path"].(string)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(workdir.Dir(ctx, "."), raw)
	}
	if abs, err := filepath.Abs(raw); err == nil {
		return abs
	}
	return filepath.Clean(raw)
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(sum[:8])
}

// observe inspects a completed batch of tool calls and returns optional
// replanning guidance when a cognitive impasse is detected.
func (g *cognitiveGuard) observe(ctx context.Context, calls []guardedCall, results []ToolCallResult) (guidance string, boost bool) {
	if g == nil || len(results) == 0 {
		return "", false
	}

	var impasseReasons []string

	for i, res := range results {
		argsJSON := ""
		if i < len(calls) {
			argsJSON = calls[i].Arguments
		}
		argsH := shortHash(argsJSON)
		resH := shortHash(res.Content)

		hasPostEditDiag := strings.Contains(res.Content, projectchecks.AutoDiagnosticHeader)
		trimmedRes := strings.TrimSpace(res.Content)
		failed := res.IsError || hasPostEditDiag || strings.HasPrefix(trimmedRes, "Erro:") || strings.HasPrefix(trimmedRes, "Erro executando tool:")

		if fileMutatingToolNames[res.Name] && !res.IsError {
			if p := extractEditedFilePath(ctx, argsJSON); p != "" {
				g.editedFiles[p] = true
				if hasPostEditDiag {
					g.dirtyDiagnostics[p] = extractAutoDiagSection(res.Content)
				} else {
					delete(g.dirtyDiagnostics, p)
				}
			}
		}

		if failed {
			g.sawFailure = true
			if g.firstFailure == "" {
				g.firstFailure = summarizeFailureSnippet(res.Name, res.Content)
			}
			g.consecutiveErrors++
			sig := res.Name + ":" + argsH
			g.callFailCounts[sig]++
			if g.callFailCounts[sig] == 2 {
				impasseReasons = append(impasseReasons,
					fmt.Sprintf("A ferramenta %q falhou 2 vezes seguidas com exatamente os mesmos argumentos.", res.Name))
			} else if g.callFailCounts[sig] >= 3 {
				impasseReasons = append(impasseReasons,
					fmt.Sprintf("A ferramenta %q já falhou %d vezes com os mesmos argumentos — insistir nesta chamada é proibido.", res.Name, g.callFailCounts[sig]))
			}
		} else {
			g.consecutiveErrors = 0
			if fileMutatingToolNames[res.Name] || res.Name == "bash" || res.Name == "run_project_checks" {
				g.lastCleanSummary = fmt.Sprintf("%s concluída sem erros", res.Name)
			}
		}

		fp := callFingerprint{name: res.Name, argsHash: argsH, resultHash: resH, failed: failed}
		g.history = append(g.history, fp)

		// Detect A -> B -> A -> B oscillation on recent calls.
		n := len(g.history)
		if n >= 4 {
			a1, b1, a2, b2 := g.history[n-4], g.history[n-3], g.history[n-2], g.history[n-1]
			if a1.name == a2.name && a1.argsHash == a2.argsHash &&
				b1.name == b2.name && b1.argsHash == b2.argsHash &&
				(a1.name != b1.name || a1.argsHash != b1.argsHash) &&
				(a2.failed || b2.failed) {
				impasseReasons = append(impasseReasons,
					fmt.Sprintf("Oscilação A→B→A→B detectada entre %q e %q sem convergir.", a1.name, b1.name))
			}
		}
	}

	if g.consecutiveErrors >= 4 {
		impasseReasons = append(impasseReasons,
			fmt.Sprintf("Sequência de %d falhas consecutivas de ferramentas neste turno.", g.consecutiveErrors))
		g.boostedReasoning = true
	}

	if len(impasseReasons) == 0 || g.loopAlerts >= 3 {
		return "", g.boostedReasoning
	}
	g.loopAlerts++
	g.boostedReasoning = true

	var b strings.Builder
	b.WriteString(CognitiveImpasseHeader + "\n")
	for _, r := range impasseReasons {
		b.WriteString("- " + r + "\n")
	}
	b.WriteString("PARE e replaneje antes da próxima ação:\n")
	b.WriteString("1. NÃO repita a mesma chamada com os mesmos parâmetros.\n")
	b.WriteString("2. Inspecione o estado real do arquivo/símbolo (`read_file`, `grep` ou `code_impact`).\n")
	b.WriteString("3. Formule uma hipótese diferente para a causa-raiz antes de editar novamente.")
	return b.String(), g.boostedReasoning
}

// checkCompletionGate verifies that files modified in this turn are free of
// syntax/type diagnostics and pass configured turn-level verifiers before the
// assistant is allowed to conclude its turn. Bounded to at most 2 gates/turn.
func (g *cognitiveGuard) checkCompletionGate(ctx context.Context) string {
	if g == nil || len(g.editedFiles) == 0 || g.gateInterventions >= 2 {
		return ""
	}

	// Re-verify any previously dirty file directly on disk (in case `bash` fixed it).
	var unresolved []string
	paths := g.editedFileList()
	for _, path := range paths {
		diag := projectchecks.FastDiagnoseFile(ctx, path)
		if diag != "" {
			g.dirtyDiagnostics[path] = diag
			unresolved = append(unresolved, fmt.Sprintf("### %s\n%s", filepath.Base(path), diag))
		} else {
			delete(g.dirtyDiagnostics, path)
		}
	}

	if len(unresolved) > 0 {
		g.gateInterventions++
		g.sawFailure = true
		if g.firstFailure == "" {
			g.firstFailure = summarizeFailureSnippet("fastcheck", unresolved[0])
		}
		g.boostedReasoning = true
		return fmt.Sprintf("%s\nVocê tentou encerrar o turno, mas há erros de sintaxe/tipo nos arquivos modificados:\n\n%s\n\nCorrija esses erros agora e confirme que compilam antes de responder ao usuário.",
			VerificationGateHeader, strings.Join(unresolved, "\n\n"))
	}

	// Also run .polypod/verifiers.yaml `on_turn` checks when configured and untampered.
	wd := workdir.Dir(ctx, ".")
	if !projectchecks.ConfigChanged(ctx, wd) {
		results, err := projectchecks.Run(ctx, wd, "turn")
		if err == nil && len(results) > 0 {
			var failed []projectchecks.Result
			for _, r := range results {
				if r.Err != nil {
					failed = append(failed, r)
				}
			}
			if len(failed) > 0 {
				g.gateInterventions++
				g.sawFailure = true
				formatted := projectchecks.FormatResults(failed)
				if g.firstFailure == "" {
					g.firstFailure = summarizeFailureSnippet("projectchecks", formatted)
				}
				g.boostedReasoning = true
				return fmt.Sprintf("%s\nOs verificadores automáticos de fim de turno falharam:\n\n%s\n\nCorrija a causa-raiz antes de concluir a resposta.",
					VerificationGateHeader, formatted)
			}
		}
	}

	return ""
}

func (g *cognitiveGuard) editedFileList() []string {
	if g == nil || len(g.editedFiles) == 0 {
		return nil
	}
	out := make([]string, 0, len(g.editedFiles))
	for p := range g.editedFiles {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (g *cognitiveGuard) shouldRecordPlaybook() bool {
	return g != nil && g.sawFailure && len(g.editedFiles) > 0 && len(g.dirtyDiagnostics) == 0
}

func extractAutoDiagSection(content string) string {
	idx := strings.Index(content, projectchecks.AutoDiagnosticHeader)
	if idx < 0 {
		return ""
	}
	section := content[idx:]
	if impactIdx := strings.Index(section, projectchecks.ImpactHeader); impactIdx > 0 {
		section = section[:impactIdx]
	}
	return strings.TrimSpace(section)
}

func summarizeFailureSnippet(toolName, content string) string {
	clean := strings.TrimSpace(content)
	lines := strings.Split(clean, "\n")
	var kept []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || t == projectchecks.AutoDiagnosticHeader {
			continue
		}
		kept = append(kept, t)
		if len(kept) >= 3 {
			break
		}
	}
	summary := strings.Join(kept, " | ")
	if len(summary) > 260 {
		summary = summary[:260] + "..."
	}
	return fmt.Sprintf("[%s] %s", toolName, summary)
}
