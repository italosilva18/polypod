// Package betas provides beta header constants and model capability predicates
// for the Anthropic API. It mirrors the logic from the TypeScript betas.ts and
// constants/betas.ts source files.
package betas

import (
	"os"
	"strings"
	"sync"
)

// ── Beta header constants ────────────────────────────────────────────────────

const (
	// ClaudeCode20250219BetaHeader is the general claude-code beta header.
	ClaudeCode20250219BetaHeader = "claude-code-20250219"

	// InterleavedThinkingBetaHeader enables interleaved (ISP) thinking blocks.
	InterleavedThinkingBetaHeader = "interleaved-thinking-2025-05-14"

	// Context1MBetaHeader enables 1 M-token context window.
	Context1MBetaHeader = "context-1m-2025-08-07"

	// ContextManagementBetaHeader enables server-side context management (tool clearing / thinking preservation).
	ContextManagementBetaHeader = "context-management-2025-06-27"

	// StructuredOutputsBetaHeader enables strict structured outputs (JSON schema).
	StructuredOutputsBetaHeader = "structured-outputs-2025-12-15"

	// WebSearchBetaHeader enables the web-search tool.
	WebSearchBetaHeader = "web-search-2025-03-05"

	// ToolSearchBetaHeader1P is the tool-search beta for Claude API / Foundry.
	ToolSearchBetaHeader1P = "advanced-tool-use-2025-11-20"

	// ToolSearchBetaHeader3P is the tool-search beta for Vertex AI / Bedrock.
	ToolSearchBetaHeader3P = "tool-search-tool-2025-10-19"

	// EffortBetaHeader is the effort-level beta header.
	EffortBetaHeader = "effort-2025-11-24"

	// TaskBudgetsBetaHeader is the task-budgets beta header.
	TaskBudgetsBetaHeader = "task-budgets-2026-03-13"

	// PromptCachingScopeBetaHeader enables scoped prompt caching.
	PromptCachingScopeBetaHeader = "prompt-caching-scope-2026-01-05"

	// FastModeBetaHeader enables fast-mode responses.
	FastModeBetaHeader = "fast-mode-2026-02-01"

	// RedactThinkingBetaHeader enables server-side thinking summarisation
	// (API returns redacted_thinking blocks instead of full thinking content).
	RedactThinkingBetaHeader = "redact-thinking-2026-02-12"

	// TokenEfficientToolsBetaHeader enables the v2 JSON tool_use format (FC v3).
	TokenEfficientToolsBetaHeader = "token-efficient-tools-2026-03-28"

	// AdvisorBetaHeader enables the advisor tool.
	AdvisorBetaHeader = "advisor-tool-2026-03-01"

	// OAuthBetaHeader is required for Claude.ai subscriber (OAuth) sessions.
	OAuthBetaHeader = "oauth-2025-04-20"

	// CLIInternalBetaHeader is the ant-internal CLI beta, empty for external users.
	// The TS source sets this only when USER_TYPE === 'ant'.
	CLIInternalBetaHeader = "" // set at runtime via GetCLIInternalBetaHeader()
)

// GetCLIInternalBetaHeader returns the CLI-internal beta header for ant users,
// or an empty string for external users.
func GetCLIInternalBetaHeader() string {
	if os.Getenv("USER_TYPE") == "ant" {
		return "cli-internal-2026-02-09"
	}
	return ""
}

// ── Bedrock / Vertex sets ────────────────────────────────────────────────────

// BedrockExtraParamsHeaders is the set of beta headers that Bedrock requires
// to be sent via extraBodyParams rather than HTTP headers.
var BedrockExtraParamsHeaders = map[string]bool{
	InterleavedThinkingBetaHeader: true,
	Context1MBetaHeader:           true,
	ToolSearchBetaHeader3P:        true,
}

// VertexCountTokensAllowedBetas lists the only betas that Vertex countTokens
// accepts without returning a 400 error.
var VertexCountTokensAllowedBetas = map[string]bool{
	ClaudeCode20250219BetaHeader:  true,
	InterleavedThinkingBetaHeader: true,
	ContextManagementBetaHeader:   true,
}

// ── Model capability predicates ──────────────────────────────────────────────

// getCanonicalName returns a lowercase canonical model name stripped of
// version suffixes like @20250219, :latest, etc.
func getCanonicalName(model string) string {
	name := strings.ToLower(model)
	// Strip @<date> suffixes (e.g. claude-opus-4@20250219)
	if idx := strings.IndexByte(name, '@'); idx >= 0 {
		name = name[:idx]
	}
	// Strip :latest or similar
	if idx := strings.IndexByte(name, ':'); idx >= 0 {
		name = name[:idx]
	}
	return name
}

// ModelSupportsISP reports whether the model supports interleaved (streaming)
// thinking (ISP). Mirrors modelSupportsISP() in betas.ts.
//
// Provider is inferred from environment: if ANTHROPIC_VERTEX_PROJECT_ID is set
// → vertex; if AWS_BEDROCK_REGION or ANTHROPIC_BEDROCK is set → bedrock;
// otherwise → firstParty.
func ModelSupportsISP(model string) bool {
	canonical := getCanonicalName(model)
	provider := GetAPIProvider()

	switch provider {
	case "foundry":
		return true
	case "firstParty":
		return !strings.Contains(canonical, "claude-3-")
	}
	// bedrock / vertex
	return strings.Contains(canonical, "claude-opus-4") ||
		strings.Contains(canonical, "claude-sonnet-4")
}

// ModelSupportsExtendedThinking is an alias for ModelSupportsISP — models that
// support ISP also support extended thinking budgets.
func ModelSupportsExtendedThinking(model string) bool {
	return ModelSupportsISP(model)
}

// ModelSupportsContextManagement reports whether the model supports the
// server-side context-management beta (tool clearing / thinking preservation).
// Mirrors modelSupportsContextManagement() in betas.ts.
func ModelSupportsContextManagement(model string) bool {
	canonical := getCanonicalName(model)
	provider := GetAPIProvider()

	switch provider {
	case "foundry":
		return true
	case "firstParty":
		return !strings.Contains(canonical, "claude-3-")
	}
	return strings.Contains(canonical, "claude-opus-4") ||
		strings.Contains(canonical, "claude-sonnet-4") ||
		strings.Contains(canonical, "claude-haiku-4")
}

// ModelSupportsStructuredOutputs reports whether strict structured outputs are
// available for the given model. Only firstParty and Foundry are supported.
// Mirrors modelSupportsStructuredOutputs() in betas.ts.
func ModelSupportsStructuredOutputs(model string) bool {
	provider := GetAPIProvider()
	if provider != "firstParty" && provider != "foundry" {
		return false
	}
	canonical := getCanonicalName(model)
	return strings.Contains(canonical, "claude-sonnet-4-6") ||
		strings.Contains(canonical, "claude-sonnet-4-5") ||
		strings.Contains(canonical, "claude-opus-4-1") ||
		strings.Contains(canonical, "claude-opus-4-5") ||
		strings.Contains(canonical, "claude-opus-4-6") ||
		strings.Contains(canonical, "claude-haiku-4-5")
}

// ModelSupportsWebSearch reports whether the model supports the web-search tool
// for Vertex AI (Claude 4.0+ only) or Foundry (all models).
func ModelSupportsWebSearch(model string) bool {
	provider := GetAPIProvider()
	canonical := getCanonicalName(model)

	switch provider {
	case "vertex":
		return strings.Contains(canonical, "claude-opus-4") ||
			strings.Contains(canonical, "claude-sonnet-4") ||
			strings.Contains(canonical, "claude-haiku-4")
	case "foundry":
		return true
	}
	return false
}

// ModelSupportsAutoMode reports whether the model supports auto mode (PI probes).
// At launch this is firstParty-only for external users; ant users have a wider
// allow/deny list. Mirrors modelSupportsAutoMode() in betas.ts.
func ModelSupportsAutoMode(model string) bool {
	provider := GetAPIProvider()
	canonical := getCanonicalName(model)

	userType := os.Getenv("USER_TYPE")

	// External non-firstParty is always false.
	if userType != "ant" && provider != "firstParty" {
		return false
	}

	if userType == "ant" {
		// Denylist: block known-unsupported claude models.
		if strings.Contains(canonical, "claude-3-") {
			return false
		}
		// claude-*-4 not followed by -[6-9]
		if matchClaudeMinorFour(canonical) {
			return false
		}
		return true
	}

	// External firstParty allowlist.
	return strings.HasPrefix(canonical, "claude-opus-4-6") ||
		strings.HasPrefix(canonical, "claude-sonnet-4-6")
}

// matchClaudeMinorFour returns true for canonical names like
// claude-opus-4, claude-opus-4-0…-4-5 (but NOT -4-6 or higher).
func matchClaudeMinorFour(canonical string) bool {
	for _, family := range []string{"claude-opus-", "claude-sonnet-", "claude-haiku-"} {
		if !strings.HasPrefix(canonical, family) {
			continue
		}
		rest := canonical[len(family):]
		// rest starts with "4"
		if !strings.HasPrefix(rest, "4") {
			continue
		}
		afterFour := rest[1:]
		if afterFour == "" || afterFour == "-" {
			return true // bare -4 or dangling hyphen
		}
		if len(afterFour) >= 2 && afterFour[0] == '-' {
			minor := afterFour[1]
			if minor >= '0' && minor <= '5' {
				return true
			}
		}
	}
	return false
}

// ── Provider detection ───────────────────────────────────────────────────────

// GetAPIProvider returns the inferred API provider based on environment
// variables. The string matches the TS getAPIProvider() return values:
// "firstParty", "bedrock", "vertex", "foundry".
func GetAPIProvider() string {
	if os.Getenv("ANTHROPIC_VERTEX_PROJECT_ID") != "" ||
		os.Getenv("CLOUD_ML_REGION") != "" ||
		os.Getenv("ANTHROPIC_VERTEX_REGION") != "" {
		return "vertex"
	}
	if os.Getenv("AWS_BEDROCK_REGION") != "" ||
		os.Getenv("ANTHROPIC_BEDROCK") != "" ||
		os.Getenv("AWS_DEFAULT_REGION") != "" && os.Getenv("AWS_BEDROCK_ENABLED") != "" {
		return "bedrock"
	}
	if os.Getenv("ANTHROPIC_FOUNDRY_URL") != "" ||
		os.Getenv("CLAUDE_FOUNDRY") != "" {
		return "foundry"
	}
	return "firstParty"
}

// ── Beta list builders ───────────────────────────────────────────────────────

// shouldIncludeFirstPartyOnlyBetas mirrors shouldIncludeFirstPartyOnlyBetas()
// from betas.ts: true when provider is firstParty or foundry and the
// CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS env var is not set to a truthy value.
func shouldIncludeFirstPartyOnlyBetas() bool {
	provider := GetAPIProvider()
	if provider != "firstParty" && provider != "foundry" {
		return false
	}
	return !isEnvTruthy(os.Getenv("CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"))
}

// isEnvTruthy returns true when the value is one of "1", "true", "yes", "on"
// (case-insensitive). Mirrors isEnvTruthy() from envUtils.ts.
func isEnvTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// isEnvDefinedFalsy returns true when the value is one of "0", "false", "no", "off"
// (case-insensitive). Mirrors isEnvDefinedFalsy() from envUtils.ts.
func isEnvDefinedFalsy(v string) bool {
	if v == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

// GetBetaHeaders returns the full list of beta headers for the given model,
// equivalent to getAllModelBetas() from betas.ts.
func GetBetaHeaders(model string) []string {
	return getAllModelBetasCached(model)
}

// ── Memoised beta computation ────────────────────────────────────────────────

var (
	betaCacheMu sync.RWMutex
	betaCache   = map[string][]string{}
)

// getAllModelBetasCached is the memoised implementation of getAllModelBetas.
func getAllModelBetasCached(model string) []string {
	betaCacheMu.RLock()
	if v, ok := betaCache[model]; ok {
		betaCacheMu.RUnlock()
		return v
	}
	betaCacheMu.RUnlock()

	result := computeAllModelBetas(model)

	betaCacheMu.Lock()
	betaCache[model] = result
	betaCacheMu.Unlock()
	return result
}

// ClearBetaCache clears the memoised beta header cache (mirrors clearBetasCaches()).
func ClearBetaCache() {
	betaCacheMu.Lock()
	betaCache = map[string][]string{}
	betaCacheMu.Unlock()
}

// computeAllModelBetas is the un-memoised implementation.
func computeAllModelBetas(model string) []string {
	var headers []string
	canonical := getCanonicalName(model)
	isHaiku := strings.Contains(canonical, "haiku")
	provider := GetAPIProvider()
	includeFirstPartyOnlyBetas := shouldIncludeFirstPartyOnlyBetas()

	if !isHaiku {
		headers = append(headers, ClaudeCode20250219BetaHeader)
		if os.Getenv("USER_TYPE") == "ant" && os.Getenv("CLAUDE_CODE_ENTRYPOINT") == "cli" {
			if cli := GetCLIInternalBetaHeader(); cli != "" {
				headers = append(headers, cli)
			}
		}
	}

	// OAuth / subscriber header
	if isClaudeAISubscriber() {
		headers = append(headers, OAuthBetaHeader)
	}

	// 1 M context
	if has1MContext(model) {
		headers = append(headers, Context1MBetaHeader)
	}

	// Interleaved thinking
	if !isEnvTruthy(os.Getenv("DISABLE_INTERLEAVED_THINKING")) && ModelSupportsISP(model) {
		headers = append(headers, InterleavedThinkingBetaHeader)
	}

	// Redact thinking (firstParty only, non-interactive, no showThinkingSummaries)
	// In Go we conservatively omit the non-interactive / settings checks since
	// those are runtime UI concerns. We mirror the firstParty + ISP gate.
	if includeFirstPartyOnlyBetas && ModelSupportsISP(model) {
		headers = append(headers, RedactThinkingBetaHeader)
	}

	// Context management
	if shouldIncludeFirstPartyOnlyBetas() && ModelSupportsContextManagement(model) {
		headers = append(headers, ContextManagementBetaHeader)
	}

	// Structured outputs (strict tool use)
	if includeFirstPartyOnlyBetas && ModelSupportsStructuredOutputs(model) {
		headers = append(headers, StructuredOutputsBetaHeader)
	}

	// Token efficient tools (ant-only while soaking)
	if os.Getenv("USER_TYPE") == "ant" && includeFirstPartyOnlyBetas {
		headers = append(headers, TokenEfficientToolsBetaHeader)
	}

	// Web search: Vertex Claude 4+ or Foundry
	if provider == "vertex" && ModelSupportsWebSearch(model) {
		headers = append(headers, WebSearchBetaHeader)
	}
	if provider == "foundry" {
		headers = append(headers, WebSearchBetaHeader)
	}

	// Prompt caching scope (firstParty / foundry always send; it's a no-op without a scope field)
	if includeFirstPartyOnlyBetas {
		headers = append(headers, PromptCachingScopeBetaHeader)
	}

	// ANTHROPIC_BETAS env var – explicit user opt-in
	if raw := os.Getenv("ANTHROPIC_BETAS"); raw != "" {
		for _, b := range strings.Split(raw, ",") {
			b = strings.TrimSpace(b)
			if b != "" {
				headers = append(headers, b)
			}
		}
	}

	return headers
}

// isClaudeAISubscriber returns true when the session is authenticated via
// OAuth (Claude.ai subscriber). We detect this from the AUTH_TYPE env var or
// the presence of a subscriber token file; the canonical check in TS uses
// keychain state. We expose a simple env hook here.
func isClaudeAISubscriber() bool {
	return os.Getenv("CLAUDE_AI_SUBSCRIBER") == "1" ||
		isEnvTruthy(os.Getenv("CLAUDE_AI_SUBSCRIBER"))
}

// has1MContext returns true when the model has a 1 M-token context window.
// Mirrors has1mContext() from context.ts — identified by model name.
func has1MContext(model string) bool {
	canonical := getCanonicalName(model)
	// Claude 3.7+, Claude 4+ have 1 M context on firstParty.
	return strings.Contains(canonical, "claude-3-7") ||
		strings.Contains(canonical, "claude-opus-4") ||
		strings.Contains(canonical, "claude-sonnet-4") ||
		strings.Contains(canonical, "claude-haiku-4")
}

// ── Bedrock helpers ──────────────────────────────────────────────────────────

// GetModelBetas returns the filtered list of beta headers for the given model,
// excluding Bedrock extra-params headers when the provider is Bedrock.
// Mirrors getModelBetas() from betas.ts.
func GetModelBetas(model string) []string {
	all := getAllModelBetasCached(model)
	if GetAPIProvider() == "bedrock" {
		filtered := all[:0:len(all)]
		for _, b := range all {
			if !BedrockExtraParamsHeaders[b] {
				filtered = append(filtered, b)
			}
		}
		return filtered
	}
	return all
}

// GetBedrockExtraBodyParamsBetas returns the betas for this model that Bedrock
// requires to be sent via extraBodyParams. Mirrors getBedrockExtraBodyParamsBetas().
func GetBedrockExtraBodyParamsBetas(model string) []string {
	all := getAllModelBetasCached(model)
	var out []string
	for _, b := range all {
		if BedrockExtraParamsHeaders[b] {
			out = append(out, b)
		}
	}
	return out
}

// ── Merge helper ─────────────────────────────────────────────────────────────

// MergeBetas merges sdkBetas into autoBetas without duplicates, appending any
// sdkBetas that are not already present. Mirrors the merge logic in
// getMergedBetas() from betas.ts.
func MergeBetas(autoBetas []string, sdkBetas []string) []string {
	if len(sdkBetas) == 0 {
		return autoBetas
	}
	present := make(map[string]bool, len(autoBetas))
	for _, b := range autoBetas {
		present[b] = true
	}
	result := make([]string, len(autoBetas))
	copy(result, autoBetas)
	for _, b := range sdkBetas {
		if !present[b] {
			result = append(result, b)
			present[b] = true
		}
	}
	return result
}

// GetMergedBetas returns the full merged beta list for a model, optionally
// ensuring agentic headers are present (isAgenticQuery). Mirrors getMergedBetas().
func GetMergedBetas(model string, isAgenticQuery bool, sdkBetas []string) []string {
	base := GetModelBetas(model)

	if isAgenticQuery {
		hasCC := false
		for _, b := range base {
			if b == ClaudeCode20250219BetaHeader {
				hasCC = true
				break
			}
		}
		if !hasCC {
			base = append(base, ClaudeCode20250219BetaHeader)
		}

		if os.Getenv("USER_TYPE") == "ant" && os.Getenv("CLAUDE_CODE_ENTRYPOINT") == "cli" {
			cli := GetCLIInternalBetaHeader()
			if cli != "" {
				hasCLI := false
				for _, b := range base {
					if b == cli {
						hasCLI = true
						break
					}
				}
				if !hasCLI {
					base = append(base, cli)
				}
			}
		}
	}

	return MergeBetas(base, sdkBetas)
}
