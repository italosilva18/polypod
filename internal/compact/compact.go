// Package compact implements context window compaction for long conversations.
//
// When a conversation grows too large for the model's context window, the compact
// service summarizes older messages into a condensed form while preserving recent
// context. This mirrors the Claude Code compaction system with full, partial, and
// session-memory-based compaction strategies.
package compact

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/conversation"
)

// Errors returned by the compact service.
var (
	ErrNotEnoughMessages    = errors.New("not enough messages to compact")
	ErrNoSummaryGenerated   = errors.New("failed to generate conversation summary")
	ErrPromptTooLong        = errors.New("conversation too long for compaction request itself")
	ErrUserAbort            = errors.New("compaction aborted by user")
	ErrIncompleteResponse   = errors.New("compaction interrupted; this may be due to network issues")
	ErrCompactionInProgress = errors.New("compaction already in progress")
)

// SummaryFunc is called to generate a summary of messages via an LLM.
// The implementation should send the formatted prompt to the model and return
// the raw summary text. The context allows cancellation.
type SummaryFunc func(ctx context.Context, systemPrompt string, messages []conversation.Message, userPrompt string) (string, error)

// CompactConfig controls the compact service behavior.
type CompactConfig struct {
	// ContextWindowSize is the total context window of the model in tokens.
	ContextWindowSize int

	// MaxOutputTokens is the model's maximum output token limit.
	MaxOutputTokens int

	// ReservedOutputTokens is reserved for the summary output (default: 20000).
	ReservedOutputTokens int

	// AutoCompactBufferTokens is subtracted from the effective window
	// to set the auto-compact trigger threshold (default: 13000).
	AutoCompactBufferTokens int

	// PreserveRecentGroups is how many recent API-round groups to keep
	// verbatim after compaction (default: 0 = keep none, let summary cover all).
	PreserveRecentGroups int

	// MaxConsecutiveFailures is the circuit-breaker limit for auto-compact.
	// After this many consecutive failures, auto-compact stops retrying
	// until the next session (default: 3).
	MaxConsecutiveFailures int

	// CooldownDuration is the minimum time between auto-compactions (default: 10s).
	CooldownDuration time.Duration

	// Enabled controls whether compaction is available at all (default: true).
	Enabled bool

	// AutoCompactEnabled controls proactive auto-compaction (default: true).
	// When false, manual /compact still works.
	AutoCompactEnabled bool
}

// DefaultConfig returns a CompactConfig with production-quality defaults.
func DefaultConfig() CompactConfig {
	return CompactConfig{
		ContextWindowSize:       200_000,
		MaxOutputTokens:         16_384,
		ReservedOutputTokens:    20_000,
		AutoCompactBufferTokens: 13_000,
		PreserveRecentGroups:    0,
		MaxConsecutiveFailures:  3,
		CooldownDuration:        10 * time.Second,
		Enabled:                 true,
		AutoCompactEnabled:      true,
	}
}

// CompactResult holds the output of a compaction operation.
type CompactResult struct {
	// Summary is the formatted summary text.
	Summary string

	// SummaryMessages are the new messages replacing the compacted portion.
	SummaryMessages []conversation.Message

	// PreservedMessages are recent messages kept verbatim.
	PreservedMessages []conversation.Message

	// PreCompactTokens is the estimated token count before compaction.
	PreCompactTokens int

	// PostCompactTokens is the estimated token count after compaction.
	PostCompactTokens int

	// MessagesRemoved is the count of messages replaced by the summary.
	MessagesRemoved int

	// IsAutoCompact indicates whether this was triggered automatically.
	IsAutoCompact bool

	// CompactedAt is when compaction completed.
	CompactedAt time.Time
}

// CompactService orchestrates context window compaction.
type CompactService struct {
	config CompactConfig
	logger *slog.Logger

	summarize SummaryFunc

	mu               sync.Mutex
	compacting       bool
	lastCompactTime  time.Time
	consecutiveFails int

	bridge *HookBridge
}

// SetHookBridge wires a HookBridge into the service so that
// FirePreCompact / FirePostCompact are called around each Compact
// invocation. Passing nil disables hook firing (same as the default).
func (s *CompactService) SetHookBridge(b *HookBridge) {
	s.bridge = b
}

// NewCompactService creates a new compact service.
//
// The summarize function is called to generate summaries via the LLM. It receives
// a system prompt, the messages to summarize, and a user-facing prompt requesting
// the summary. It must return the raw summary text.
func NewCompactService(cfg CompactConfig, summarize SummaryFunc, logger *slog.Logger) *CompactService {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.ReservedOutputTokens == 0 {
		cfg.ReservedOutputTokens = 20_000
	}
	if cfg.AutoCompactBufferTokens == 0 {
		cfg.AutoCompactBufferTokens = 13_000
	}
	if cfg.MaxConsecutiveFailures == 0 {
		cfg.MaxConsecutiveFailures = 3
	}
	if cfg.CooldownDuration == 0 {
		cfg.CooldownDuration = 10 * time.Second
	}
	return &CompactService{
		config:    cfg,
		logger:    logger,
		summarize: summarize,
	}
}

// EffectiveContextWindow returns the usable context window size after reserving
// space for summary output. This is: min(MaxOutputTokens, ReservedOutputTokens)
// subtracted from ContextWindowSize.
func (s *CompactService) EffectiveContextWindow() int {
	reserved := s.config.ReservedOutputTokens
	if s.config.MaxOutputTokens < reserved {
		reserved = s.config.MaxOutputTokens
	}
	return s.config.ContextWindowSize - reserved
}

// AutoCompactThreshold returns the token count above which auto-compaction triggers.
func (s *CompactService) AutoCompactThreshold() int {
	return s.EffectiveContextWindow() - s.config.AutoCompactBufferTokens
}

// Compact performs a full compaction of the conversation. It summarizes all messages
// except optionally preserved recent groups, and returns a CompactResult with the
// summary and preserved messages.
//
// If customInstructions is non-empty, it is appended to the compaction prompt to
// guide the summarizer (e.g., "focus on TypeScript code changes").
func (s *CompactService) Compact(
	ctx context.Context,
	messages []conversation.Message,
	customInstructions string,
	isAuto bool,
) (*CompactResult, error) {
	if !s.config.Enabled {
		return nil, errors.New("compaction is disabled")
	}

	if len(messages) == 0 {
		return nil, ErrNotEnoughMessages
	}

	// Concurrency guard
	s.mu.Lock()
	if s.compacting {
		s.mu.Unlock()
		return nil, ErrCompactionInProgress
	}
	s.compacting = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.compacting = false
		s.mu.Unlock()
	}()

	preTokens := EstimateTokens(messages)

	// Fire PreCompact hook (non-fatal on error)
	if s.bridge != nil {
		trigger := "manual"
		if isAuto {
			trigger = "auto"
		}
		_ = s.bridge.FirePreCompact(ctx, CompactMeta{
			Trigger:            trigger,
			MessageCount:       len(messages),
			EstimatedTokens:    preTokens,
			CustomInstructions: customInstructions,
		})
	}

	// Group messages by API round
	groups := GroupMessagesByAPIRound(messages)

	if len(groups) < 2 {
		return nil, ErrNotEnoughMessages
	}

	// Determine which groups to summarize vs preserve
	preserveCount := s.config.PreserveRecentGroups
	if preserveCount >= len(groups) {
		preserveCount = len(groups) - 1 // always summarize at least 1 group
	}

	summarizeGroups := groups[:len(groups)-preserveCount]
	preserveGroups := groups[len(groups)-preserveCount:]

	// Flatten groups into message slices
	var messagesToSummarize []conversation.Message
	for _, g := range summarizeGroups {
		messagesToSummarize = append(messagesToSummarize, g...)
	}

	var preserved []conversation.Message
	for _, g := range preserveGroups {
		preserved = append(preserved, g...)
	}

	// Strip images from messages before summarization
	sanitized := StripImagesFromMessages(messagesToSummarize)

	// Build the compaction prompt
	prompt := GetCompactPrompt(customInstructions)
	systemPrompt := "You are a helpful AI assistant tasked with summarizing conversations."

	// Call the LLM to generate the summary
	rawSummary, err := s.summarize(ctx, systemPrompt, sanitized, prompt)
	if err != nil {
		if isAuto {
			s.mu.Lock()
			s.consecutiveFails++
			if s.consecutiveFails >= s.config.MaxConsecutiveFailures {
				s.logger.Warn("auto-compact circuit breaker tripped",
					"consecutive_failures", s.consecutiveFails,
				)
			}
			s.mu.Unlock()
		}
		return nil, fmt.Errorf("generating summary: %w", err)
	}

	if rawSummary == "" {
		return nil, ErrNoSummaryGenerated
	}

	// Format the summary (strip analysis tags, format summary tags)
	summary := FormatCompactSummary(rawSummary)

	// Build result messages
	now := time.Now()
	boundaryMsg := conversation.Message{
		Role:      conversation.RoleSystem,
		Content:   "[compact_boundary] Conversation compacted",
		Origin:    conversation.OriginCompaction,
		Timestamp: now,
	}

	summaryMsg := conversation.Message{
		Role: conversation.RoleUser,
		Content: fmt.Sprintf(
			"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.\n\n%s\n\nContinue the conversation from where it left off without asking the user any further questions. Resume directly — do not acknowledge the summary, do not recap what was happening, do not preface with \"I'll continue\" or similar. Pick up the last task as if the break never happened.",
			summary,
		),
		Origin:    conversation.OriginCompaction,
		Timestamp: now,
	}

	resultMessages := []conversation.Message{boundaryMsg, summaryMsg}
	resultMessages = append(resultMessages, preserved...)

	postTokens := EstimateTokens(resultMessages)

	// Reset failure count on success
	s.mu.Lock()
	s.consecutiveFails = 0
	s.lastCompactTime = now
	s.mu.Unlock()

	s.logger.Info("compaction completed",
		"messages_removed", len(messagesToSummarize),
		"messages_preserved", len(preserved),
		"pre_tokens", preTokens,
		"post_tokens", postTokens,
		"is_auto", isAuto,
	)

	// Fire PostCompact hook (non-fatal on error)
	if s.bridge != nil {
		trigger := "manual"
		if isAuto {
			trigger = "auto"
		}
		_ = s.bridge.FirePostCompact(ctx, CompactMeta{
			Trigger:            trigger,
			MessageCount:       len(messages),
			EstimatedTokens:    preTokens,
			CustomInstructions: customInstructions,
		}, CompactSummary{
			TokensBefore:   preTokens,
			TokensAfter:    postTokens,
			MessagesBefore: len(messages),
			MessagesAfter:  len(resultMessages),
			DurationMs:     time.Since(now).Milliseconds(),
			Success:        true,
		})
	}

	return &CompactResult{
		Summary:           summary,
		SummaryMessages:   resultMessages[:2], // boundary + summary
		PreservedMessages: preserved,
		PreCompactTokens:  preTokens,
		PostCompactTokens: postTokens,
		MessagesRemoved:   len(messagesToSummarize),
		IsAutoCompact:     isAuto,
		CompactedAt:       now,
	}, nil
}

// PartialCompact summarizes only a portion of the conversation. The pivotIndex
// determines the split point, and direction controls which side is summarized:
//   - "from": summarize messages[pivotIndex:], keep messages[:pivotIndex]
//   - "up_to": summarize messages[:pivotIndex], keep messages[pivotIndex:]
func (s *CompactService) PartialCompact(
	ctx context.Context,
	messages []conversation.Message,
	pivotIndex int,
	direction string, // "from" or "up_to"
	customInstructions string,
) (*CompactResult, error) {
	if !s.config.Enabled {
		return nil, errors.New("compaction is disabled")
	}

	if pivotIndex < 0 || pivotIndex >= len(messages) {
		return nil, fmt.Errorf("pivot index %d out of range [0, %d)", pivotIndex, len(messages))
	}

	var toSummarize, toKeep []conversation.Message
	switch direction {
	case "up_to":
		toSummarize = messages[:pivotIndex]
		toKeep = messages[pivotIndex:]
	case "from":
		toSummarize = messages[pivotIndex:]
		toKeep = messages[:pivotIndex]
	default:
		return nil, fmt.Errorf("invalid direction %q; must be \"from\" or \"up_to\"", direction)
	}

	if len(toSummarize) == 0 {
		return nil, ErrNotEnoughMessages
	}

	preTokens := EstimateTokens(messages)

	sanitized := StripImagesFromMessages(toSummarize)
	prompt := GetPartialCompactPrompt(customInstructions, direction)
	systemPrompt := "You are a helpful AI assistant tasked with summarizing conversations."

	rawSummary, err := s.summarize(ctx, systemPrompt, sanitized, prompt)
	if err != nil {
		return nil, fmt.Errorf("generating partial summary: %w", err)
	}

	if rawSummary == "" {
		return nil, ErrNoSummaryGenerated
	}

	summary := FormatCompactSummary(rawSummary)
	now := time.Now()

	boundaryMsg := conversation.Message{
		Role:      conversation.RoleSystem,
		Content:   "[compact_boundary] Partial conversation compacted",
		Origin:    conversation.OriginCompaction,
		Timestamp: now,
	}
	summaryMsg := conversation.Message{
		Role:      conversation.RoleUser,
		Content:   fmt.Sprintf("This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.\n\n%s", summary),
		Origin:    conversation.OriginCompaction,
		Timestamp: now,
	}

	var resultMessages []conversation.Message
	switch direction {
	case "up_to":
		// Summary comes first, then preserved messages
		resultMessages = append(resultMessages, boundaryMsg, summaryMsg)
		resultMessages = append(resultMessages, toKeep...)
	case "from":
		// Preserved messages first, then summary
		resultMessages = append(resultMessages, toKeep...)
		resultMessages = append(resultMessages, boundaryMsg, summaryMsg)
	}

	postTokens := EstimateTokens(resultMessages)

	s.logger.Info("partial compaction completed",
		"direction", direction,
		"messages_summarized", len(toSummarize),
		"messages_kept", len(toKeep),
		"pre_tokens", preTokens,
		"post_tokens", postTokens,
	)

	return &CompactResult{
		Summary:           summary,
		SummaryMessages:   []conversation.Message{boundaryMsg, summaryMsg},
		PreservedMessages: toKeep,
		PreCompactTokens:  preTokens,
		PostCompactTokens: postTokens,
		MessagesRemoved:   len(toSummarize),
		IsAutoCompact:     false,
		CompactedAt:       now,
	}, nil
}

// StripImagesFromMessages replaces image/document markers in messages with
// text placeholders before sending them for summarization. Images aren't needed
// for generating conversation summaries and can cause the compaction API call
// to exceed prompt limits.
func StripImagesFromMessages(messages []conversation.Message) []conversation.Message {
	result := make([]conversation.Message, len(messages))
	for i, msg := range messages {
		result[i] = msg
		// Simple heuristic: replace base64 image data patterns
		if strings.Contains(msg.Content, "data:image/") {
			result[i].Content = imageDataPattern.ReplaceAllString(msg.Content, "[image]")
		}
		if strings.Contains(msg.Content, "data:application/pdf") {
			result[i].Content = pdfDataPattern.ReplaceAllString(msg.Content, "[document]")
		}
	}
	return result
}

var (
	imageDataPattern = regexp.MustCompile(`data:image/[^;]+;base64,[A-Za-z0-9+/=]+`)
	pdfDataPattern   = regexp.MustCompile(`data:application/pdf;base64,[A-Za-z0-9+/=]+`)
)

// FormatCompactSummary strips the <analysis> drafting scratchpad and replaces
// <summary> XML tags with readable section headers. The analysis block improves
// summary quality during generation but has no informational value afterward.
func FormatCompactSummary(raw string) string {
	// Strip analysis section
	formatted := analysisPattern.ReplaceAllString(raw, "")

	// Extract and format summary section
	if match := summaryPattern.FindStringSubmatch(formatted); len(match) > 1 {
		content := strings.TrimSpace(match[1])
		formatted = summaryPattern.ReplaceAllString(formatted, "Summary:\n"+content)
	}

	// Clean up extra whitespace
	formatted = multiNewlinePattern.ReplaceAllString(formatted, "\n\n")

	return strings.TrimSpace(formatted)
}

var (
	analysisPattern     = regexp.MustCompile(`(?s)<analysis>.*?</analysis>`)
	summaryPattern      = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
	multiNewlinePattern = regexp.MustCompile(`\n{3,}`)
)

// EstimateTokens gives a rough token count for a slice of messages.
// Uses ~4 characters per token with overhead for role/structure, then
// applies a 4/3 conservative padding multiplier.
func EstimateTokens(messages []conversation.Message) int {
	total := 0
	for _, m := range messages {
		total += estimateStringTokens(m.Content) + 4 // +4 for role/overhead
	}
	// Conservative 4/3 padding since we're approximating
	return (total * 4) / 3
}

// estimateStringTokens estimates the token count for a string.
// Approximation: ~4 characters per token.
func estimateStringTokens(s string) int {
	return len(s) / 4
}

// BuildResultMessages constructs the final message array from a CompactResult,
// suitable for passing to the conversation session.
func BuildResultMessages(result *CompactResult) []conversation.Message {
	var messages []conversation.Message
	messages = append(messages, result.SummaryMessages...)
	messages = append(messages, result.PreservedMessages...)
	return messages
}
