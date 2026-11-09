package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/costa/polypod/internal/apilimits"
	"github.com/costa/polypod/internal/initcmd"
)

// ── /effort ─────────────────────────────────────────────────────────────────

// EffortLevel represents the AI thinking effort/budget setting that
// influences max tokens, chain-of-thought prompting, and tool-call
// iteration limits for the next AI call.
type EffortLevel string

const (
	EffortLow    EffortLevel = "low"    // 2k max tokens, 5 iterations
	EffortMedium EffortLevel = "medium" // 4k max tokens, 10 iterations (DEFAULT)
	EffortHigh   EffortLevel = "high"   // 8k max tokens, 20 iterations
	EffortMax    EffortLevel = "max"    // 16k max tokens, 30 iterations
)

// effortBudget describes the runtime budget associated with an effort level.
type effortBudget struct {
	maxTokens     int
	maxIterations int
	useCoT        bool
	description   string
}

// effortBudgetFor returns the budget settings for a given effort level.
func effortBudgetFor(level EffortLevel) effortBudget {
	switch level {
	case EffortLow:
		return effortBudget{
			maxTokens:     2048,
			maxIterations: 5,
			useCoT:        false,
			description:   "Rapido, baixo budget de tokens",
		}
	case EffortHigh:
		return effortBudget{
			maxTokens:     8192,
			maxIterations: 20,
			useCoT:        true,
			description:   "Mais thoroughgo, usa mais tokens",
		}
	case EffortMax:
		return effortBudget{
			maxTokens:     16384,
			maxIterations: 30,
			useCoT:        true,
			description:   "Profundidade maxima de raciocinio",
		}
	default: // EffortMedium and unknown
		return effortBudget{
			maxTokens:     4096,
			maxIterations: 10,
			useCoT:        false,
			description:   "Balanco padrao entre velocidade e qualidade",
		}
	}
}

// parseEffortLevel parses a string into an EffortLevel, returning false
// for unknown values.
func parseEffortLevel(s string) (EffortLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low", "l":
		return EffortLow, true
	case "medium", "med", "m", "default":
		return EffortMedium, true
	case "high", "h":
		return EffortHigh, true
	case "max", "maximum", "x":
		return EffortMax, true
	}
	return "", false
}

func handleEffort(parts []string, deps commandDeps) commandResult {
	if deps.sessionState == nil {
		return commandResult{
			output:  cmdLabelStyle.Render("state de sessao nao disponivel"),
			handled: true,
		}
	}

	// /effort <level> — set
	if len(parts) >= 2 {
		level, ok := parseEffortLevel(parts[1])
		if !ok {
			return commandResult{
				output: errorStyle.Render("nivel desconhecido: ") +
					cmdValueStyle.Render(parts[1]) +
					cmdLabelStyle.Render("  (use low|medium|high|max)"),
				handled: true,
			}
		}
		deps.sessionState.setEffort(level)
		return formatEffortStatus(level, true)
	}

	// /effort — show current
	current := deps.sessionState.getEffort()
	return formatEffortStatus(current, false)
}

func formatEffortStatus(current EffortLevel, justChanged bool) commandResult {
	budget := effortBudgetFor(current)

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Effort"))
	b.WriteString(": ")
	b.WriteString(cmdActiveStyle.Render(strings.ToUpper(string(current))))
	if justChanged {
		b.WriteString(cmdLabelStyle.Render("  (atualizado)"))
	}
	b.WriteString("\n")

	rows := []struct{ label, value string }{
		{"Max tokens", fmt.Sprintf("%d", budget.maxTokens)},
		{"Max iterations", fmt.Sprintf("%d", budget.maxIterations)},
		{"Chain-of-thought", boolYesNo(budget.useCoT)},
		{"Tipo", budget.description},
	}
	maxLabel := 0
	for _, r := range rows {
		if len(r.label) > maxLabel {
			maxLabel = len(r.label)
		}
	}
	for _, r := range rows {
		padding := strings.Repeat(" ", maxLabel-len(r.label))
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render(r.label + padding + "  "))
		b.WriteString(cmdValueStyle.Render(r.value))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(cmdBadgeStyle.Render("  Niveis disponiveis:"))
	b.WriteString("\n")

	levels := []EffortLevel{EffortLow, EffortMedium, EffortHigh, EffortMax}
	short := map[EffortLevel]string{
		EffortLow:    "rapido (2k tokens)",
		EffortMedium: "balanceado (4k tokens) - DEFAULT",
		EffortHigh:   "thoroughgo (8k tokens)",
		EffortMax:    "profundo (16k tokens)",
	}
	for _, lv := range levels {
		b.WriteString("    ")
		marker := "  "
		if lv == current {
			marker = cmdActiveStyle.Render("● ")
		} else {
			marker = cmdLabelStyle.Render("  ")
		}
		b.WriteString(marker)
		b.WriteString(cmdValueStyle.Render(fmt.Sprintf("%-7s", lv)))
		b.WriteString(cmdLabelStyle.Render(" - " + short[lv]))
		if lv == current {
			b.WriteString(cmdActiveStyle.Render("  ATUAL"))
		}
		b.WriteString("\n")
	}

	return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}
}

func boolYesNo(b bool) string {
	if b {
		return "sim"
	}
	return "nao"
}

// ── /branch ─────────────────────────────────────────────────────────────────

var (
	errBranchEmptyName = errors.New("nome de branch vazio")
	errBranchExists    = errors.New("branch ja existe")
	errBranchNotFound  = errors.New("branch nao encontrada")
	errBranchProtected = errors.New("nao e possivel deletar a branch main")
	errBranchInUse     = errors.New("nao e possivel deletar a branch ativa")
)

func handleBranch(parts []string, deps commandDeps, messages []chatEntry) commandResult {
	if deps.sessionState == nil {
		return commandResult{
			output:  cmdLabelStyle.Render("state de sessao nao disponivel"),
			handled: true,
		}
	}
	if !deps.sessionState.branchesBound() {
		if deps.bindBranches == nil {
			return commandResult{output: errorStyle.Render("erro: ") + cmdLabelStyle.Render(errBranchRuntimeUnavailable.Error()), handled: true}
		}
		if err := deps.bindBranches(deps.sessionState); err != nil {
			return commandResult{output: errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()), handled: true}
		}
	}
	if err := deps.sessionState.refreshPersistentBranches(); err != nil {
		return commandResult{output: errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()), handled: true}
	}

	sub := ""
	if len(parts) >= 2 {
		sub = strings.ToLower(parts[1])
	}

	switch sub {
	case "":
		return handleBranchList(deps)
	case "tree":
		return handleBranchTree(deps)
	case "new", "create":
		if len(parts) < 3 {
			return commandResult{
				output: cmdLabelStyle.Render("uso: ") +
					cmdValueStyle.Render("/branch new <name>"),
				handled: true,
			}
		}
		return handleBranchNew(deps, parts[2], messages)
	case "switch", "checkout":
		if len(parts) < 3 {
			return commandResult{
				output: cmdLabelStyle.Render("uso: ") +
					cmdValueStyle.Render("/branch switch <name>"),
				handled: true,
			}
		}
		return handleBranchSwitch(deps, parts[2], messages)
	case "delete", "rm":
		if len(parts) < 3 {
			return commandResult{
				output: cmdLabelStyle.Render("uso: ") +
					cmdValueStyle.Render("/branch delete <name>"),
				handled: true,
			}
		}
		return handleBranchDelete(deps, parts[2], messages)
	default:
		return commandResult{
			output: cmdLabelStyle.Render("uso: ") +
				cmdValueStyle.Render("/branch [new|switch|delete|tree] <name>"),
			handled: true,
		}
	}
}

func handleBranchList(deps commandDeps) commandResult {
	branches, current := deps.sessionState.snapshotBranches()

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Conversation tree"))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d)", len(branches))))
	b.WriteString("\n\n")

	for i, br := range branches {
		isLast := i == len(branches)-1
		isCurrent := br.name == current
		connector := "├── "
		if i == 0 {
			connector = "" // main is rendered as the root
		} else if isLast {
			connector = "└── "
		}

		b.WriteString("  ")
		if i > 0 {
			b.WriteString(cmdBadgeStyle.Render(connector))
		}
		if isCurrent {
			b.WriteString(cmdActiveStyle.Render("● "))
		} else {
			b.WriteString(cmdInactiveStyle.Render("○ "))
		}
		b.WriteString(cmdValueStyle.Render(br.name))

		// Stats
		msgCount := br.messageCount
		stats := fmt.Sprintf(" (%d messages", msgCount)
		if isCurrent {
			stats += ", current"
		}
		if br.parent != "" {
			stats += fmt.Sprintf(", from msg %d of %s", br.forkAtMsg, br.parent)
		}
		stats += ")"
		b.WriteString(cmdLabelStyle.Render(stats))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(cmdLabelStyle.Render("  comandos: "))
	b.WriteString(cmdValueStyle.Render("/branch [new|switch|delete|tree] <name>"))

	return commandResult{output: b.String(), handled: true}
}

func handleBranchTree(deps commandDeps) commandResult {
	branches, current := deps.sessionState.snapshotBranches()

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Conversation tree:"))
	b.WriteString("\n")

	// First line: main with stats.
	for i, br := range branches {
		if i == 0 {
			// main
			msgCount := br.messageCount
			b.WriteString("  ")
			if br.name == current {
				b.WriteString(cmdActiveStyle.Render("● "))
			} else {
				b.WriteString(cmdInactiveStyle.Render("○ "))
			}
			b.WriteString(cmdValueStyle.Render(br.name))
			label := fmt.Sprintf(" (%d messages", msgCount)
			if br.name == current {
				label += ", current"
			}
			label += ")"
			b.WriteString(cmdLabelStyle.Render(label))
			b.WriteString("\n")
			continue
		}

		isLast := i == len(branches)-1
		connector := "├── "
		if isLast {
			connector = "└── "
		}
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render(connector))
		if br.name == current {
			b.WriteString(cmdActiveStyle.Render("● "))
		} else {
			b.WriteString(cmdInactiveStyle.Render("○ "))
		}
		b.WriteString(cmdValueStyle.Render(br.name))

		msgCount := br.messageCount
		label := fmt.Sprintf(" (%d messages from msg %d", msgCount, br.forkAtMsg)
		if br.name == current {
			label += ", current branch"
		}
		label += ")"
		b.WriteString(cmdLabelStyle.Render(label))
		b.WriteString("\n")
	}

	return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}
}

func handleBranchNew(deps commandDeps, name string, messages []chatEntry) commandResult {
	name = strings.TrimSpace(name)
	active, err := deps.sessionState.mutatePersistentBranch("create", name, messages)
	if err != nil {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}
	forkAt := 0
	branches, current := deps.sessionState.snapshotBranches()
	for _, branch := range branches {
		if branch.name == current {
			forkAt = branch.forkAtMsg
			break
		}
	}
	return commandResult{
		output: notifySuccessStyle.Render("branch criada: ") +
			cmdValueStyle.Render(name) +
			cmdLabelStyle.Render(fmt.Sprintf(" (fork de msg %d)", forkAt)),
		replaceMessages: active,
		handled:         true,
	}
}

func handleBranchSwitch(deps commandDeps, name string, messages []chatEntry) commandResult {
	name = strings.TrimSpace(name)
	active, err := deps.sessionState.mutatePersistentBranch("switch", name, messages)
	if err != nil {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}
	return commandResult{
		output: notifySuccessStyle.Render("branch ativa: ") +
			cmdValueStyle.Render(name),
		replaceMessages: active,
		handled:         true,
	}
}

func handleBranchDelete(deps commandDeps, name string, messages []chatEntry) commandResult {
	name = strings.TrimSpace(name)
	if _, err := deps.sessionState.mutatePersistentBranch("delete", name, messages); err != nil {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}
	return commandResult{
		output: notifySuccessStyle.Render("branch deletada: ") +
			cmdValueStyle.Render(name),
		handled: true,
	}
}

// ── /usage ──────────────────────────────────────────────────────────────────

func handleUsage(parts []string, deps commandDeps) commandResult {
	filter := ""
	if len(parts) >= 2 {
		filter = strings.ToLower(parts[1])
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("═══ USAGE LIMITS ═══"))
	b.WriteString("\n\n")

	// Build per-model breakdown from CostEngine if available; fall
	// back to a single synthetic entry built from the active model
	// when no breakdown exists.
	var rows []providerRow
	if deps.modelBreakdown != nil {
		for model, mc := range deps.modelBreakdown() {
			rows = append(rows, providerRow{
				provider: providerNameForModel(model),
				model:    model,
				input:    mc.PromptTokens,
				output:   mc.CompletionTokens,
				total:    mc.PromptTokens + mc.CompletionTokens,
				cost:     mc.TotalCostUSD,
			})
		}
	}

	// If nothing was tracked, synthesize a single row for the active
	// model so the user still sees the per-provider rate limits.
	if len(rows) == 0 {
		model := ""
		if deps.currentModel != nil {
			model = deps.currentModel()
		}
		if model == "" && deps.activeAgent != nil {
			model = deps.activeAgent()
		}
		if model == "" {
			model = "unknown"
		}
		rows = append(rows, providerRow{
			provider: providerNameForModel(model),
			model:    model,
		})
	}

	// Filter by provider name if requested.
	if filter != "" {
		var filtered []providerRow
		for _, r := range rows {
			if strings.Contains(strings.ToLower(r.provider), filter) ||
				strings.Contains(strings.ToLower(r.model), filter) {
				filtered = append(filtered, r)
			}
		}
		if len(filtered) == 0 {
			return commandResult{
				output: cmdLabelStyle.Render("nenhum provider encontrado para: ") +
					cmdValueStyle.Render(filter),
				handled: true,
			}
		}
		rows = filtered
	}

	// Stable order
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].provider == rows[j].provider {
			return rows[i].model < rows[j].model
		}
		return rows[i].provider < rows[j].provider
	})

	totalCost := 0.0
	for _, r := range rows {
		totalCost += r.cost

		limits := apilimits.LimitsForModel(r.model)
		ctxWindow := contextWindowForModel(r.model)
		dailyLimit := limits.TokensPerDay

		b.WriteString(cmdValueStyle.Render(r.provider))
		b.WriteString(cmdLabelStyle.Render(" ("))
		b.WriteString(cmdValueStyle.Render(r.model))
		b.WriteString(cmdLabelStyle.Render("):"))
		b.WriteString("\n")

		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render("Context window: "))
		b.WriteString(cmdValueStyle.Render(formatTokenCount(ctxWindow) + " tokens"))
		b.WriteString("\n")

		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render("Rate:           "))
		b.WriteString(cmdValueStyle.Render(fmt.Sprintf("%d req/min", limits.RequestsPerMinute)))
		b.WriteString("\n")

		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render("Cost today:     "))
		b.WriteString(cmdValueStyle.Render(fmt.Sprintf("$%.4f", r.cost)))
		b.WriteString("\n")

		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render("Tokens today:   "))
		used := r.total
		if dailyLimit > 0 {
			pct := float64(used) / float64(dailyLimit) * 100
			b.WriteString(cmdValueStyle.Render(fmt.Sprintf("%s / %s (%.1f%%)",
				formatTokenCount(used), formatTokenCount(dailyLimit), pct)))
		} else {
			b.WriteString(cmdValueStyle.Render(fmt.Sprintf("%s / unlimited",
				formatTokenCount(used))))
		}
		b.WriteString("\n\n")
	}

	b.WriteString(cmdBadgeStyle.Render(fmt.Sprintf("TOTAL HOJE: $%.4f across %d provider(s)",
		totalCost, len(uniqueProviders(rows)))))

	return commandResult{output: b.String(), handled: true}
}

// providerNameForModel returns a human-friendly provider name for a model identifier.
func providerNameForModel(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "claude"):
		return "Anthropic"
	case strings.Contains(m, "gpt") || strings.Contains(m, "o1") || strings.Contains(m, "o3"):
		return "OpenAI"
	case strings.Contains(m, "gemini"):
		return "Google"
	case strings.Contains(m, "deepseek"):
		return "DeepSeek"
	case strings.Contains(m, "llama"):
		return "Meta"
	case strings.Contains(m, "mistral"):
		return "Mistral"
	case strings.Contains(m, "qwen"):
		return "Qwen"
	case strings.Contains(m, "groq"):
		return "Groq"
	}
	return "Unknown"
}

// contextWindowForModel returns the published context window for a model.
func contextWindowForModel(model string) int {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "claude") && strings.Contains(m, "opus"):
		return 200_000
	case strings.Contains(m, "claude"):
		return 200_000
	case strings.Contains(m, "gpt-4o") || strings.Contains(m, "gpt-4-turbo"):
		return 128_000
	case strings.Contains(m, "gpt-4.1"):
		return 1_000_000
	case strings.Contains(m, "gemini-1.5") || strings.Contains(m, "gemini-2"):
		return 1_000_000
	case strings.Contains(m, "deepseek"):
		return 128_000
	case strings.Contains(m, "llama-3.1") || strings.Contains(m, "llama-3.3"):
		return 128_000
	}
	return 32_000
}

func formatTokenCount(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%dk", n/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// providerRow is one entry in the /usage table — a single (provider, model)
// pair with the cumulative tokens and cost recorded for the session.
type providerRow struct {
	provider string
	model    string
	input    int
	output   int
	total    int
	cost     float64
}

func uniqueProviders(rows []providerRow) []string {
	seen := make(map[string]bool)
	var out []string
	for _, r := range rows {
		if !seen[r.provider] {
			seen[r.provider] = true
			out = append(out, r.provider)
		}
	}
	return out
}

// ── /init-verifiers ─────────────────────────────────────────────────────────

// verifiersConfig is the on-disk shape of .polypod/verifiers.yaml.
type verifiersConfig struct {
	Verifiers map[string]string `yaml:"verifiers"`
	AutoRun   verifiersAutoRun  `yaml:"auto_run"`
}

type verifiersAutoRun struct {
	OnSave   bool `yaml:"on_save"`
	OnCommit bool `yaml:"on_commit"`
}

// detectedVerifiers holds the verifiers detected for a project.
type detectedVerifiers struct {
	stackName string // e.g. "Go (go.mod)"
	test      string
	vet       string
	build     string
	lint      string
	notes     []string
}

func handleInitVerifiers(parts []string) commandResult {
	wd, err := os.Getwd()
	if err != nil {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}

	det := detectVerifiers(wd)
	if det.test == "" && det.lint == "" && det.build == "" && det.vet == "" {
		return commandResult{
			output:  cmdLabelStyle.Render("nenhum verificador detectado neste projeto"),
			handled: true,
		}
	}

	// Build the report.
	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("═══ VERIFIERS DETECTED ═══"))
	b.WriteString("\n\n")
	b.WriteString(cmdLabelStyle.Render("Project: "))
	b.WriteString(cmdValueStyle.Render(det.stackName))
	b.WriteString("\n")

	writeRow := func(label, value string) {
		if value == "" {
			return
		}
		b.WriteString("  ")
		b.WriteString(notifySuccessStyle.Render("✓ "))
		b.WriteString(cmdLabelStyle.Render(fmt.Sprintf("%-6s ", label+":")))
		b.WriteString(cmdValueStyle.Render(value))
		b.WriteString("\n")
	}
	writeRow("Test", det.test)
	writeRow("Vet", det.vet)
	writeRow("Build", det.build)
	writeRow("Lint", det.lint)

	for _, n := range det.notes {
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render("• " + n))
		b.WriteString("\n")
	}

	// Write the config file.
	cfg := verifiersConfig{
		Verifiers: map[string]string{},
		AutoRun: verifiersAutoRun{
			OnSave:   false,
			OnCommit: true,
		},
	}
	if det.test != "" {
		cfg.Verifiers["test"] = det.test
	}
	if det.vet != "" {
		cfg.Verifiers["vet"] = det.vet
	}
	if det.build != "" {
		cfg.Verifiers["build"] = det.build
	}
	if det.lint != "" {
		cfg.Verifiers["lint"] = det.lint
	}

	polypodDir := filepath.Join(wd, ".polypod")
	if err := os.MkdirAll(polypodDir, 0o755); err != nil {
		return commandResult{
			output: errorStyle.Render("erro ao criar .polypod/: ") +
				cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}
	cfgPath := filepath.Join(polypodDir, "verifiers.yaml")

	// Honour --dry-run if passed.
	dryRun := false
	for _, p := range parts[1:] {
		if p == "--dry-run" || p == "-n" {
			dryRun = true
		}
	}

	if !dryRun {
		data, err := yaml.Marshal(cfg)
		if err != nil {
			return commandResult{
				output: errorStyle.Render("erro ao serializar yaml: ") +
					cmdLabelStyle.Render(err.Error()),
				handled: true,
			}
		}
		if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
			return commandResult{
				output: errorStyle.Render("erro ao salvar: ") +
					cmdLabelStyle.Render(err.Error()),
				handled: true,
			}
		}
	}

	b.WriteString("\n")
	if dryRun {
		b.WriteString(cmdLabelStyle.Render("dry-run: nada foi escrito em "))
		b.WriteString(cmdValueStyle.Render(cfgPath))
	} else {
		b.WriteString(notifySuccessStyle.Render("Saved to "))
		rel, relErr := filepath.Rel(wd, cfgPath)
		if relErr != nil {
			rel = cfgPath
		}
		b.WriteString(cmdValueStyle.Render(rel))
	}
	b.WriteString("\n\n")
	b.WriteString(cmdLabelStyle.Render("Auto-run on save? (s/N): ") +
		cmdLabelStyle.Render("(edite ") +
		cmdValueStyle.Render(".polypod/verifiers.yaml") +
		cmdLabelStyle.Render(" para mudar)"))

	return commandResult{output: b.String(), handled: true}
}

// detectVerifiers walks the project root looking for known toolchain
// markers and assembles the corresponding verifier commands.
func detectVerifiers(wd string) detectedVerifiers {
	det := detectedVerifiers{}

	// Use initcmd to bootstrap with the existing stack detection, then
	// layer additional information on top.
	stack := initcmd.Detect(wd)

	switch {
	case fileExists(filepath.Join(wd, "go.mod")):
		det.stackName = "Go (go.mod)"
		det.test = "go test ./..."
		det.vet = "go vet ./..."
		det.build = "go build ./..."
		if fileExists(filepath.Join(wd, ".golangci.yml")) ||
			fileExists(filepath.Join(wd, ".golangci.yaml")) {
			det.lint = "golangci-lint run"
			det.notes = append(det.notes, "detectado .golangci.yml")
		}

	case fileExists(filepath.Join(wd, "package.json")):
		det.stackName = "JavaScript/TypeScript (package.json)"
		det.test, det.lint = detectNodeScripts(filepath.Join(wd, "package.json"))
		if det.test == "" {
			det.test = "npm test"
		}
		if det.lint == "" {
			det.lint = "npm run lint"
		}
		if fileExists(filepath.Join(wd, "tsconfig.json")) {
			det.build = "tsc --noEmit"
			det.notes = append(det.notes, "tsconfig.json detectado, build via tsc")
		}

	case fileExists(filepath.Join(wd, "Cargo.toml")):
		det.stackName = "Rust (Cargo.toml)"
		det.test = "cargo test"
		det.build = "cargo build"
		det.lint = "cargo clippy"

	case fileExists(filepath.Join(wd, "pyproject.toml")):
		det.stackName = "Python (pyproject.toml)"
		det.test = "pytest"
		det.lint = "ruff check"
		if fileExists(filepath.Join(wd, "mypy.ini")) ||
			fileExists(filepath.Join(wd, ".mypy.ini")) {
			det.vet = "mypy ."
			det.notes = append(det.notes, "mypy.ini detectado")
		}

	case fileExists(filepath.Join(wd, "pom.xml")):
		det.stackName = "Java (pom.xml)"
		det.test = "mvn test"
		det.build = "mvn compile"

	case fileExists(filepath.Join(wd, "build.gradle")) ||
		fileExists(filepath.Join(wd, "build.gradle.kts")):
		det.stackName = "Java/Kotlin (build.gradle)"
		det.test = "./gradlew test"
		det.build = "./gradlew build"

	default:
		// Fall back to whatever initcmd inferred so we still have
		// something to work with for less common stacks.
		det.stackName = stack.Language
		if det.stackName == "" {
			det.stackName = "(desconhecido)"
		}
		det.test = stack.TestCmd
		det.lint = stack.LintCmd
	}

	// Makefile augmentation: if a Makefile defines `test` / `lint` /
	// `build` targets, prefer them since they typically encode the
	// project's actual conventions.
	if mk := filepath.Join(wd, "Makefile"); fileExists(mk) {
		targets := makefileTargets(mk)
		if targets["test"] && (det.test == "" || strings.HasPrefix(det.test, "go test")) {
			det.test = "make test"
			det.notes = append(det.notes, "Makefile target 'test' detectado")
		}
		if targets["lint"] {
			det.lint = "make lint"
			det.notes = append(det.notes, "Makefile target 'lint' detectado")
		}
		if targets["build"] && det.build == "" {
			det.build = "make build"
		}
	}

	return det
}

// detectNodeScripts reads package.json and extracts the test/lint scripts.
// This is a lightweight string scan; we deliberately avoid pulling in a
// full JSON parser dependency for what is essentially a hint extractor.
func detectNodeScripts(path string) (testCmd, lintCmd string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	s := string(data)
	if strings.Contains(s, `"test"`) {
		testCmd = "npm test"
	}
	if strings.Contains(s, `"lint"`) {
		lintCmd = "npm run lint"
	}
	return
}

// makefileTargets returns the set of declared top-level targets in a Makefile.
func makefileTargets(path string) map[string]bool {
	out := make(map[string]bool)
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Target lines look like "name: deps" at column 0 (no indent).
		if strings.HasPrefix(line, "\t") {
			continue
		}
		if idx := strings.Index(line, ":"); idx > 0 {
			name := strings.TrimSpace(line[:idx])
			// Skip variable assignments like FOO := bar
			if strings.ContainsAny(name, " \t=") {
				continue
			}
			out[name] = true
		}
	}
	return out
}
