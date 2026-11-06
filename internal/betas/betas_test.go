package betas

import (
	"reflect"
	"sort"
	"testing"
)

// clearProviderEnv wipes every environment variable consulted by GetAPIProvider,
// GetCLIInternalBetaHeader, isClaudeAISubscriber, has1MContext, and the rest
// of the computeAllModelBetas pipeline. It is called at the start of each test
// that depends on provider detection so tests are hermetic regardless of the
// host environment.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	vars := []string{
		"ANTHROPIC_VERTEX_PROJECT_ID",
		"CLOUD_ML_REGION",
		"ANTHROPIC_VERTEX_REGION",
		"AWS_BEDROCK_REGION",
		"ANTHROPIC_BEDROCK",
		"AWS_DEFAULT_REGION",
		"AWS_BEDROCK_ENABLED",
		"ANTHROPIC_FOUNDRY_URL",
		"CLAUDE_FOUNDRY",
		"USER_TYPE",
		"CLAUDE_CODE_ENTRYPOINT",
		"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS",
		"DISABLE_INTERLEAVED_THINKING",
		"CLAUDE_AI_SUBSCRIBER",
		"ANTHROPIC_BETAS",
	}
	for _, v := range vars {
		t.Setenv(v, "")
	}
	// Reset the memoised beta cache since several tests exercise different
	// envs for the same model name.
	ClearBetaCache()
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ── Provider detection ──────────────────────────────────────────────────────

func TestGetAPIProvider(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"default firstParty", nil, "firstParty"},
		{"vertex via project id", map[string]string{"ANTHROPIC_VERTEX_PROJECT_ID": "p"}, "vertex"},
		{"vertex via cloud ml region", map[string]string{"CLOUD_ML_REGION": "us-central1"}, "vertex"},
		{"vertex via vertex region", map[string]string{"ANTHROPIC_VERTEX_REGION": "us-east5"}, "vertex"},
		{"bedrock via region", map[string]string{"AWS_BEDROCK_REGION": "us-east-1"}, "bedrock"},
		{"bedrock via ANTHROPIC_BEDROCK", map[string]string{"ANTHROPIC_BEDROCK": "1"}, "bedrock"},
		{"bedrock via default region + enabled", map[string]string{
			"AWS_DEFAULT_REGION":  "us-west-2",
			"AWS_BEDROCK_ENABLED": "1",
		}, "bedrock"},
		{"foundry via url", map[string]string{"ANTHROPIC_FOUNDRY_URL": "https://x"}, "foundry"},
		{"foundry via flag", map[string]string{"CLAUDE_FOUNDRY": "1"}, "foundry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearProviderEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := GetAPIProvider(); got != tc.want {
				t.Fatalf("GetAPIProvider() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ── CLI internal beta ───────────────────────────────────────────────────────

func TestGetCLIInternalBetaHeader(t *testing.T) {
	clearProviderEnv(t)
	if got := GetCLIInternalBetaHeader(); got != "" {
		t.Fatalf("external user should get empty header, got %q", got)
	}
	t.Setenv("USER_TYPE", "ant")
	if got := GetCLIInternalBetaHeader(); got != "cli-internal-2026-02-09" {
		t.Fatalf("ant user should get cli-internal header, got %q", got)
	}
}

// ── Canonical model name normalisation (via ModelSupportsISP firstParty) ───

func TestModelSupportsISP(t *testing.T) {
	clearProviderEnv(t) // firstParty

	cases := []struct {
		model string
		want  bool
	}{
		// firstParty: anything that isn't claude-3-* is supported.
		{"claude-opus-4-6", true},
		{"claude-sonnet-4-5@20250514", true},
		{"CLAUDE-HAIKU-4-5:latest", true}, // canonicalisation: upper + :latest
		{"claude-3-5-sonnet", false},
		{"claude-3-opus", false},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			if got := ModelSupportsISP(tc.model); got != tc.want {
				t.Fatalf("ModelSupportsISP(%q) = %v, want %v", tc.model, got, tc.want)
			}
		})
	}

	// Bedrock/Vertex: only opus-4 / sonnet-4 allowed.
	t.Run("bedrock haiku-4 not allowed", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv("AWS_BEDROCK_REGION", "us-east-1")
		if ModelSupportsISP("claude-haiku-4-5") {
			t.Fatal("haiku-4 must not support ISP on bedrock")
		}
		if !ModelSupportsISP("claude-opus-4-6") {
			t.Fatal("opus-4 must support ISP on bedrock")
		}
	})

	// Foundry: always true.
	t.Run("foundry always true", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv("CLAUDE_FOUNDRY", "1")
		if !ModelSupportsISP("claude-3-opus") {
			t.Fatal("foundry must support ISP for all models, even claude-3")
		}
	})

	// ModelSupportsExtendedThinking is an alias.
	clearProviderEnv(t)
	if ModelSupportsExtendedThinking("claude-opus-4-6") != ModelSupportsISP("claude-opus-4-6") {
		t.Fatal("ModelSupportsExtendedThinking must alias ModelSupportsISP")
	}
}

// ── Context management ─────────────────────────────────────────────────────

func TestModelSupportsContextManagement(t *testing.T) {
	clearProviderEnv(t) // firstParty

	if !ModelSupportsContextManagement("claude-opus-4-6") {
		t.Fatal("opus-4 should support ctx mgmt on firstParty")
	}
	if ModelSupportsContextManagement("claude-3-5-sonnet") {
		t.Fatal("claude-3 must not support ctx mgmt on firstParty")
	}

	// Bedrock: opus-4, sonnet-4, haiku-4 allowed.
	clearProviderEnv(t)
	t.Setenv("AWS_BEDROCK_REGION", "us-east-1")
	for _, m := range []string{"claude-opus-4-6", "claude-sonnet-4-5", "claude-haiku-4-5"} {
		if !ModelSupportsContextManagement(m) {
			t.Fatalf("bedrock %s should support ctx mgmt", m)
		}
	}
	if ModelSupportsContextManagement("claude-3-7-sonnet") {
		t.Fatal("bedrock claude-3-7 should not support ctx mgmt")
	}
}

// ── Structured outputs ─────────────────────────────────────────────────────

func TestModelSupportsStructuredOutputs(t *testing.T) {
	clearProviderEnv(t) // firstParty

	want := map[string]bool{
		"claude-opus-4-6":   true,
		"claude-opus-4-5":   true,
		"claude-opus-4-1":   true,
		"claude-sonnet-4-6": true,
		"claude-sonnet-4-5": true,
		"claude-haiku-4-5":  true,
		"claude-opus-4":     false, // not in allow-list
		"claude-opus-4-0":   false,
		"claude-3-5-sonnet": false,
	}
	for model, exp := range want {
		if got := ModelSupportsStructuredOutputs(model); got != exp {
			t.Errorf("firstParty %s: got %v, want %v", model, got, exp)
		}
	}

	// Non-firstParty / non-foundry always false.
	clearProviderEnv(t)
	t.Setenv("AWS_BEDROCK_REGION", "us-east-1")
	if ModelSupportsStructuredOutputs("claude-opus-4-6") {
		t.Fatal("bedrock must never support structured outputs")
	}

	// Foundry: same allow-list applies.
	clearProviderEnv(t)
	t.Setenv("CLAUDE_FOUNDRY", "1")
	if !ModelSupportsStructuredOutputs("claude-opus-4-6") {
		t.Fatal("foundry opus-4-6 should support structured outputs")
	}
}

// ── Web search ──────────────────────────────────────────────────────────────

func TestModelSupportsWebSearch(t *testing.T) {
	clearProviderEnv(t)
	// firstParty → false (even for opus-4)
	if ModelSupportsWebSearch("claude-opus-4-6") {
		t.Fatal("firstParty should not advertise web search")
	}

	// Vertex: opus/sonnet/haiku 4.
	clearProviderEnv(t)
	t.Setenv("ANTHROPIC_VERTEX_PROJECT_ID", "p")
	if !ModelSupportsWebSearch("claude-sonnet-4-5") {
		t.Fatal("vertex sonnet-4 should support web search")
	}
	if ModelSupportsWebSearch("claude-3-5-sonnet") {
		t.Fatal("vertex claude-3 should not support web search")
	}

	// Foundry: always true.
	clearProviderEnv(t)
	t.Setenv("CLAUDE_FOUNDRY", "1")
	if !ModelSupportsWebSearch("claude-3-opus") {
		t.Fatal("foundry must support web search for all models")
	}
}

// ── Auto-mode (complex: ant denylist vs external allowlist) ────────────────

func TestModelSupportsAutoMode(t *testing.T) {
	// External firstParty: only opus-4-6 / sonnet-4-6.
	clearProviderEnv(t)
	if !ModelSupportsAutoMode("claude-opus-4-6") {
		t.Fatal("external firstParty opus-4-6 should support auto mode")
	}
	if !ModelSupportsAutoMode("claude-sonnet-4-6") {
		t.Fatal("external firstParty sonnet-4-6 should support auto mode")
	}
	if ModelSupportsAutoMode("claude-opus-4-5") {
		t.Fatal("external firstParty opus-4-5 should NOT support auto mode")
	}
	if ModelSupportsAutoMode("claude-3-5-sonnet") {
		t.Fatal("external firstParty claude-3 should NOT support auto mode")
	}

	// External non-firstParty: always false.
	clearProviderEnv(t)
	t.Setenv("AWS_BEDROCK_REGION", "us-east-1")
	if ModelSupportsAutoMode("claude-opus-4-6") {
		t.Fatal("external bedrock must never support auto mode")
	}

	// Ant user: denylist mode. claude-3-* and claude-*-4 / 4-0..4-5 blocked.
	clearProviderEnv(t)
	t.Setenv("USER_TYPE", "ant")
	blocked := []string{
		"claude-3-opus",
		"claude-3-5-sonnet",
		"claude-opus-4",
		"claude-opus-4-0",
		"claude-opus-4-5",
		"claude-sonnet-4-3",
		"claude-haiku-4-2",
	}
	for _, m := range blocked {
		if ModelSupportsAutoMode(m) {
			t.Errorf("ant user: %s should be blocked", m)
		}
	}
	allowed := []string{
		"claude-opus-4-6",
		"claude-opus-4-7",
		"claude-sonnet-4-9",
		"claude-haiku-4-8",
		"claude-opus-5-0",
	}
	for _, m := range allowed {
		if !ModelSupportsAutoMode(m) {
			t.Errorf("ant user: %s should be allowed", m)
		}
	}
}

// ── Beta headers computation ───────────────────────────────────────────────

func TestGetBetaHeaders_FirstPartyOpus4(t *testing.T) {
	clearProviderEnv(t)
	headers := GetBetaHeaders("claude-opus-4-6")

	// Non-haiku → claude-code header is present.
	if !contains(headers, ClaudeCode20250219BetaHeader) {
		t.Error("claude-code header missing")
	}
	// Opus-4 has 1M context.
	if !contains(headers, Context1MBetaHeader) {
		t.Error("1M context header missing")
	}
	// firstParty + opus-4 → interleaved thinking.
	if !contains(headers, InterleavedThinkingBetaHeader) {
		t.Error("interleaved thinking header missing")
	}
	// firstParty + opus-4 → redact thinking.
	if !contains(headers, RedactThinkingBetaHeader) {
		t.Error("redact thinking header missing")
	}
	// firstParty + opus-4 → context management.
	if !contains(headers, ContextManagementBetaHeader) {
		t.Error("context management header missing")
	}
	// firstParty + opus-4-6 → structured outputs.
	if !contains(headers, StructuredOutputsBetaHeader) {
		t.Error("structured outputs header missing")
	}
	// firstParty → prompt caching scope.
	if !contains(headers, PromptCachingScopeBetaHeader) {
		t.Error("prompt caching scope header missing")
	}
	// firstParty → no web search.
	if contains(headers, WebSearchBetaHeader) {
		t.Error("firstParty should not have web search header")
	}
	// External user → no cli-internal.
	if contains(headers, "cli-internal-2026-02-09") {
		t.Error("external user should not have cli-internal header")
	}
}

func TestGetBetaHeaders_Haiku_NoClaudeCode(t *testing.T) {
	clearProviderEnv(t)
	headers := GetBetaHeaders("claude-haiku-4-5")

	// Haiku models must NOT get the claude-code beta.
	if contains(headers, ClaudeCode20250219BetaHeader) {
		t.Error("haiku model should not get claude-code header")
	}
}

func TestGetBetaHeaders_DisabledExperimental(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS", "true")
	headers := GetBetaHeaders("claude-opus-4-6")

	// Experimental firstParty-only betas are suppressed.
	if contains(headers, RedactThinkingBetaHeader) {
		t.Error("redact thinking should be suppressed")
	}
	if contains(headers, ContextManagementBetaHeader) {
		t.Error("context management should be suppressed")
	}
	if contains(headers, StructuredOutputsBetaHeader) {
		t.Error("structured outputs should be suppressed")
	}
	if contains(headers, PromptCachingScopeBetaHeader) {
		t.Error("prompt caching scope should be suppressed")
	}
	// Non-experimental headers are kept.
	if !contains(headers, ClaudeCode20250219BetaHeader) {
		t.Error("claude-code header must remain")
	}
}

func TestGetBetaHeaders_DisableInterleavedThinking(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("DISABLE_INTERLEAVED_THINKING", "1")
	headers := GetBetaHeaders("claude-opus-4-6")
	if contains(headers, InterleavedThinkingBetaHeader) {
		t.Error("interleaved thinking should be disabled")
	}
}

func TestGetBetaHeaders_AnthropicBetasEnv(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("ANTHROPIC_BETAS", "custom-alpha,custom-beta , ,custom-gamma")
	headers := GetBetaHeaders("claude-opus-4-6")
	for _, want := range []string{"custom-alpha", "custom-beta", "custom-gamma"} {
		if !contains(headers, want) {
			t.Errorf("ANTHROPIC_BETAS entry %q missing", want)
		}
	}
	// Empty splits must not produce empty entries.
	for _, h := range headers {
		if h == "" {
			t.Error("empty header string should be filtered out")
		}
	}
}

func TestGetBetaHeaders_Subscriber(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("CLAUDE_AI_SUBSCRIBER", "1")
	headers := GetBetaHeaders("claude-opus-4-6")
	if !contains(headers, OAuthBetaHeader) {
		t.Error("subscriber should get oauth beta header")
	}
}

func TestGetBetaHeaders_AntCLI(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("USER_TYPE", "ant")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	headers := GetBetaHeaders("claude-opus-4-6")

	if !contains(headers, "cli-internal-2026-02-09") {
		t.Error("ant CLI user should get cli-internal header")
	}
	if !contains(headers, TokenEfficientToolsBetaHeader) {
		t.Error("ant user should get token-efficient-tools header")
	}
}

// ── Vertex / Bedrock / Foundry provider-specific tests ─────────────────────

func TestGetBetaHeaders_VertexWebSearch(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("ANTHROPIC_VERTEX_PROJECT_ID", "p")
	headers := GetBetaHeaders("claude-sonnet-4-5")

	if !contains(headers, WebSearchBetaHeader) {
		t.Error("vertex sonnet-4 should include web search")
	}
	// Vertex is not firstParty/foundry → no experimental firstParty betas.
	if contains(headers, PromptCachingScopeBetaHeader) {
		t.Error("vertex should not get prompt caching scope")
	}
}

// ── GetModelBetas: Bedrock filtering ───────────────────────────────────────

func TestGetModelBetas_BedrockFiltersExtraParamsHeaders(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("AWS_BEDROCK_REGION", "us-east-1")

	// sonnet-4 has 1M context + ISP, both are bedrock extra-params headers.
	filtered := GetModelBetas("claude-sonnet-4-5")
	for h := range BedrockExtraParamsHeaders {
		if contains(filtered, h) {
			t.Errorf("GetModelBetas must strip %q on bedrock", h)
		}
	}

	extra := GetBedrockExtraBodyParamsBetas("claude-sonnet-4-5")
	if !contains(extra, InterleavedThinkingBetaHeader) {
		t.Error("bedrock extra body params should include interleaved thinking for sonnet-4")
	}
	if !contains(extra, Context1MBetaHeader) {
		t.Error("bedrock extra body params should include 1M context for sonnet-4")
	}

	// Union of filtered + extra should equal the full list.
	all := GetBetaHeaders("claude-sonnet-4-5")
	seen := map[string]bool{}
	for _, h := range filtered {
		seen[h] = true
	}
	for _, h := range extra {
		seen[h] = true
	}
	for _, h := range all {
		if !seen[h] {
			t.Errorf("header %q missing from union of filtered+extra", h)
		}
	}
}

// ── MergeBetas ──────────────────────────────────────────────────────────────

func TestMergeBetas(t *testing.T) {
	auto := []string{"a", "b", "c"}

	// Empty sdk list returns auto unchanged.
	got := MergeBetas(auto, nil)
	if !reflect.DeepEqual(got, auto) {
		t.Errorf("empty sdkBetas: got %v, want %v", got, auto)
	}

	// Dedup and append.
	got = MergeBetas(auto, []string{"b", "d", "a", "e"})
	want := []string{"a", "b", "c", "d", "e"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merge: got %v, want %v", got, want)
	}

	// Does not mutate input.
	orig := []string{"x", "y"}
	snap := append([]string{}, orig...)
	_ = MergeBetas(orig, []string{"z"})
	if !reflect.DeepEqual(orig, snap) {
		t.Errorf("MergeBetas mutated input: %v != %v", orig, snap)
	}
}

// ── GetMergedBetas ─────────────────────────────────────────────────────────

func TestGetMergedBetas_AgenticAddsClaudeCode(t *testing.T) {
	clearProviderEnv(t)
	// Haiku normally does NOT include claude-code header, but an agentic
	// query must force it in.
	merged := GetMergedBetas("claude-haiku-4-5", true, nil)
	if !contains(merged, ClaudeCode20250219BetaHeader) {
		t.Error("agentic query for haiku should force claude-code header")
	}

	// Non-agentic haiku keeps the default (no claude-code).
	clearProviderEnv(t)
	plain := GetMergedBetas("claude-haiku-4-5", false, nil)
	if contains(plain, ClaudeCode20250219BetaHeader) {
		t.Error("non-agentic haiku should not include claude-code header")
	}
}

func TestGetMergedBetas_AppendsSDKBetas(t *testing.T) {
	clearProviderEnv(t)
	sdk := []string{"sdk-one", "sdk-two"}
	merged := GetMergedBetas("claude-opus-4-6", false, sdk)
	for _, b := range sdk {
		if !contains(merged, b) {
			t.Errorf("merged betas missing sdk entry %q", b)
		}
	}
	// sdk entries should come AFTER the auto-computed ones.
	autoLen := len(GetModelBetas("claude-opus-4-6"))
	if len(merged) < autoLen+len(sdk) {
		t.Errorf("expected at least %d merged betas, got %d", autoLen+len(sdk), len(merged))
	}
}

// ── Caching ────────────────────────────────────────────────────────────────

func TestBetaCache_CachesAcrossCalls(t *testing.T) {
	clearProviderEnv(t)

	// First call populates the cache.
	first := GetBetaHeaders("claude-opus-4-6")

	// Flip an environment variable that WOULD affect the result if
	// recomputation happened.  Because of memoisation we should still see
	// the original headers for the already-cached model.
	t.Setenv("DISABLE_INTERLEAVED_THINKING", "1")
	second := GetBetaHeaders("claude-opus-4-6")
	if !reflect.DeepEqual(first, second) {
		t.Errorf("cached result changed despite memoisation: %v vs %v", first, second)
	}

	// After ClearBetaCache the new env should take effect.
	ClearBetaCache()
	third := GetBetaHeaders("claude-opus-4-6")
	if contains(third, InterleavedThinkingBetaHeader) {
		t.Error("after ClearBetaCache, interleaved thinking should be disabled")
	}
}

// ── Constant set sanity ─────────────────────────────────────────────────────

func TestBedrockExtraParamsHeadersAreConstants(t *testing.T) {
	// Exactly the three headers that Bedrock wants in extraBodyParams.
	wantKeys := []string{
		Context1MBetaHeader,
		InterleavedThinkingBetaHeader,
		ToolSearchBetaHeader3P,
	}
	if len(BedrockExtraParamsHeaders) != len(wantKeys) {
		t.Fatalf("BedrockExtraParamsHeaders size = %d, want %d",
			len(BedrockExtraParamsHeaders), len(wantKeys))
	}
	gotKeys := make([]string, 0, len(BedrockExtraParamsHeaders))
	for k := range BedrockExtraParamsHeaders {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)
	sort.Strings(wantKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Errorf("BedrockExtraParamsHeaders keys = %v, want %v", gotKeys, wantKeys)
	}

	// VertexCountTokensAllowedBetas contains exactly the three whitelisted
	// headers and nothing else.
	wantVertex := []string{
		ClaudeCode20250219BetaHeader,
		ContextManagementBetaHeader,
		InterleavedThinkingBetaHeader,
	}
	gotVertex := make([]string, 0, len(VertexCountTokensAllowedBetas))
	for k := range VertexCountTokensAllowedBetas {
		gotVertex = append(gotVertex, k)
	}
	sort.Strings(gotVertex)
	sort.Strings(wantVertex)
	if !reflect.DeepEqual(gotVertex, wantVertex) {
		t.Errorf("VertexCountTokensAllowedBetas keys = %v, want %v", gotVertex, wantVertex)
	}
}
