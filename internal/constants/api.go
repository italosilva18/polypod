package constants

// API constants migrated from Claude Code TypeScript source.
// Source: constants/apiLimits.ts, constants/betas.ts, constants/product.ts, constants/oauth.ts

// ---------------------------------------------------------------------------
// Image limits
// ---------------------------------------------------------------------------

const (
	// APIImageMaxBase64Size is the max base64-encoded image size (API enforced).
	APIImageMaxBase64Size = 5 * 1024 * 1024 // 5 MB

	// ImageTargetRawSize is the max raw image size to stay under base64 limit.
	// Base64 encoding increases size by 4/3: raw * 4/3 = base64 → raw = base64 * 3/4.
	ImageTargetRawSize = (APIImageMaxBase64Size * 3) / 4 // 3.75 MB

	// ImageMaxWidth is the client-side max width for image resizing.
	ImageMaxWidth = 2000

	// ImageMaxHeight is the client-side max height for image resizing.
	ImageMaxHeight = 2000
)

// ---------------------------------------------------------------------------
// PDF limits
// ---------------------------------------------------------------------------

const (
	// PDFTargetRawSize is the max raw PDF size that fits within the API request
	// limit after base64 encoding (20 MB raw → ~27 MB base64).
	PDFTargetRawSize = 20 * 1024 * 1024 // 20 MB

	// APIPDFMaxPages is the max number of pages in a PDF accepted by the API.
	APIPDFMaxPages = 100

	// PDFExtractSizeThreshold is the size above which PDFs are extracted into
	// page images instead of being sent as base64 document blocks.
	PDFExtractSizeThreshold = 3 * 1024 * 1024 // 3 MB

	// PDFMaxExtractSize is the max PDF file size for the page extraction path.
	PDFMaxExtractSize = 100 * 1024 * 1024 // 100 MB

	// PDFMaxPagesPerRead is the max pages the Read tool extracts in a single call.
	PDFMaxPagesPerRead = 20

	// PDFAtMentionInlineThreshold — PDFs with more pages than this get the
	// reference treatment on @ mention instead of being inlined into context.
	PDFAtMentionInlineThreshold = 10
)

// ---------------------------------------------------------------------------
// Media limits
// ---------------------------------------------------------------------------

const (
	// APIMaxMediaPerRequest is the max number of media items (images + PDFs)
	// allowed per API request.
	APIMaxMediaPerRequest = 100
)

// ---------------------------------------------------------------------------
// Beta headers
// ---------------------------------------------------------------------------

const (
	ClaudeCode20250219Beta  = "claude-code-20250219"
	InterleavedThinkingBeta = "interleaved-thinking-2025-05-14"
	Context1MBeta           = "context-1m-2025-08-07"
	ContextManagementBeta   = "context-management-2025-06-27"
	StructuredOutputsBeta   = "structured-outputs-2025-12-15"
	WebSearchBeta           = "web-search-2025-03-05"
	ToolSearchBeta1P        = "advanced-tool-use-2025-11-20"
	ToolSearchBeta3P        = "tool-search-tool-2025-10-19"
	EffortBeta              = "effort-2025-11-24"
	TaskBudgetsBeta         = "task-budgets-2026-03-13"
	PromptCachingScopeBeta  = "prompt-caching-scope-2026-01-05"
	FastModeBeta            = "fast-mode-2026-02-01"
	RedactThinkingBeta      = "redact-thinking-2026-02-12"
	TokenEfficientToolsBeta = "token-efficient-tools-2026-03-28"
	AdvisorBeta             = "advisor-tool-2026-03-01"
	OAuthBeta               = "oauth-2025-04-20"
)

// BedrockExtraParamsHeaders are betas that should be in Bedrock extraBodyParams
// and not in Bedrock headers.
var BedrockExtraParamsHeaders = map[string]bool{
	InterleavedThinkingBeta: true,
	Context1MBeta:           true,
	ToolSearchBeta3P:        true,
}

// VertexCountTokensAllowedBetas are betas allowed on Vertex countTokens API.
var VertexCountTokensAllowedBetas = map[string]bool{
	ClaudeCode20250219Beta:  true,
	InterleavedThinkingBeta: true,
	ContextManagementBeta:   true,
}

// ---------------------------------------------------------------------------
// Rate limit tiers
// ---------------------------------------------------------------------------

// RateLimitTier defines rate limits per API tier.
type RateLimitTier struct {
	Name              string
	RequestsPerMinute int
	TokensPerMinute   int
	TokensPerDay      int
}

// RateLimitTiers defines the known API rate limit tiers.
var RateLimitTiers = []RateLimitTier{
	{Name: "free", RequestsPerMinute: 5, TokensPerMinute: 20_000, TokensPerDay: 300_000},
	{Name: "build_1", RequestsPerMinute: 50, TokensPerMinute: 40_000, TokensPerDay: 1_000_000},
	{Name: "build_2", RequestsPerMinute: 1000, TokensPerMinute: 80_000, TokensPerDay: 2_500_000},
	{Name: "build_3", RequestsPerMinute: 2000, TokensPerMinute: 160_000, TokensPerDay: 5_000_000},
	{Name: "build_4", RequestsPerMinute: 4000, TokensPerMinute: 400_000, TokensPerDay: 25_000_000},
	{Name: "scale", RequestsPerMinute: 8000, TokensPerMinute: 800_000, TokensPerDay: 50_000_000},
}

// ---------------------------------------------------------------------------
// Model context windows & knowledge cutoffs
// ---------------------------------------------------------------------------

// ModelContextWindow maps model family IDs to their context window sizes in tokens.
var ModelContextWindow = map[string]int{
	"claude-opus-4-6":   200_000,
	"claude-sonnet-4-6": 200_000,
	"claude-haiku-4-5":  200_000,
	"claude-opus-4-5":   200_000,
	"claude-opus-4":     200_000,
	"claude-sonnet-4":   200_000,
	"claude-3-5-sonnet": 200_000,
	"claude-3-5-haiku":  200_000,
}

// ModelKnowledgeCutoff returns the knowledge cutoff date for a model, or empty string.
func ModelKnowledgeCutoff(modelID string) string {
	cutoffs := map[string]string{
		"claude-sonnet-4-6": "August 2025",
		"claude-opus-4-6":   "May 2025",
		"claude-opus-4-5":   "May 2025",
		"claude-haiku-4":    "February 2025",
		"claude-opus-4":     "January 2025",
		"claude-sonnet-4":   "January 2025",
	}
	// Check for substring match like the TS version does
	for pattern, cutoff := range cutoffs {
		if containsSubstring(modelID, pattern) {
			return cutoff
		}
	}
	return ""
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > len(sub) && contains(s, sub))
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Product URLs
// ---------------------------------------------------------------------------

const (
	ProductURL             = "https://claude.com/claude-code"
	ClaudeAIBaseURL        = "https://claude.ai"
	ClaudeAIStagingBaseURL = "https://claude-ai.staging.ant.dev"
	ClaudeAILocalBaseURL   = "http://localhost:4000"
	APIBaseURL             = "https://api.anthropic.com"
	ClaudeCodeDocsMapURL   = "https://code.claude.com/docs/en/claude_code_docs_map.md"
	MCPClientMetadataURL   = "https://claude.ai/oauth/claude-code-client-metadata"
)

// ---------------------------------------------------------------------------
// API version
// ---------------------------------------------------------------------------

const (
	// AnthropicAPIVersion is the current API version string.
	AnthropicAPIVersion = "2023-06-01"
)

// ---------------------------------------------------------------------------
// Frontier model IDs
// ---------------------------------------------------------------------------

const (
	FrontierModelName = "Claude Opus 4.6"
	ModelOpus46       = "claude-opus-4-6"
	ModelSonnet46     = "claude-sonnet-4-6"
	ModelHaiku45      = "claude-haiku-4-5-20251001"
)
