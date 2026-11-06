package tool

import (
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/types"
)

// ToolClassification describes the safety/mutability category of a tool call.
type ToolClassification string

const (
	// ClassReadOnly means the tool only reads data and has no side effects.
	ClassReadOnly ToolClassification = "read_only"

	// ClassWrite means the tool modifies state (files, databases, etc).
	ClassWrite ToolClassification = "write"

	// ClassDestructive means the tool performs potentially irreversible operations
	// (delete, overwrite without backup, send external message).
	ClassDestructive ToolClassification = "destructive"

	// ClassUnknown is the default when classification cannot be determined.
	ClassUnknown ToolClassification = "unknown"
)

// PermissionSuggestion is a recommendation for how a tool call should be permitted.
type PermissionSuggestion string

const (
	SuggestAutoAllow PermissionSuggestion = "auto_allow"
	SuggestAskUser   PermissionSuggestion = "ask_user"
	SuggestDeny      PermissionSuggestion = "deny"
)

// ClassificationResult holds the full classification output for a tool call.
type ClassificationResult struct {
	// Classification is the safety category.
	Classification ToolClassification

	// Suggestion is the recommended permission action.
	Suggestion PermissionSuggestion

	// Confidence is 0.0 to 1.0 indicating classifier certainty.
	Confidence float64

	// Reason explains why this classification was chosen.
	Reason string

	// ExtractedInput contains auto-extracted key parameters for auditing.
	ExtractedInput map[string]string
}

// ToolClassifier classifies tool calls by their safety and mutability characteristics.
// It combines static analysis (tool metadata) with pattern-based heuristics.
type ToolClassifier struct {
	registry *Registry

	// patternRules map tool name prefixes to classifications.
	patternRules []patternRule
}

type patternRule struct {
	prefix         string
	classification ToolClassification
	confidence     float64
}

// NewToolClassifier creates a classifier using the given tool registry.
func NewToolClassifier(registry *Registry) *ToolClassifier {
	return &ToolClassifier{
		registry: registry,
		patternRules: []patternRule{
			{prefix: "read_", classification: ClassReadOnly, confidence: 0.95},
			{prefix: "list_", classification: ClassReadOnly, confidence: 0.95},
			{prefix: "get_", classification: ClassReadOnly, confidence: 0.90},
			{prefix: "search_", classification: ClassReadOnly, confidence: 0.90},
			{prefix: "find_", classification: ClassReadOnly, confidence: 0.90},
			{prefix: "write_", classification: ClassWrite, confidence: 0.90},
			{prefix: "edit_", classification: ClassWrite, confidence: 0.90},
			{prefix: "create_", classification: ClassWrite, confidence: 0.85},
			{prefix: "update_", classification: ClassWrite, confidence: 0.85},
			{prefix: "delete_", classification: ClassDestructive, confidence: 0.95},
			{prefix: "remove_", classification: ClassDestructive, confidence: 0.90},
			{prefix: "send_", classification: ClassDestructive, confidence: 0.85},
		},
	}
}

// Classify determines the classification of a tool call.
func (c *ToolClassifier) Classify(toolName string, args map[string]any) ClassificationResult {
	result := ClassificationResult{
		ExtractedInput: ExtractKeyInput(toolName, args),
	}

	// 1. Try tool metadata from registry.
	if t, ok := c.registry.Get(toolName); ok {
		return c.classifyFromTool(t, args, result)
	}

	// 2. Try static lists.
	if constants.IsReadOnlyTool(toolName) {
		result.Classification = ClassReadOnly
		result.Suggestion = SuggestAutoAllow
		result.Confidence = 0.95
		result.Reason = "tool in static read-only list"
		return result
	}
	if constants.IsDestructiveTool(toolName) {
		result.Classification = ClassDestructive
		result.Suggestion = SuggestAskUser
		result.Confidence = 0.90
		result.Reason = "tool in static destructive list"
		return result
	}

	// 3. Try pattern rules.
	for _, rule := range c.patternRules {
		if strings.HasPrefix(toolName, rule.prefix) {
			result.Classification = rule.classification
			result.Confidence = rule.confidence
			result.Reason = "matched pattern: " + rule.prefix + "*"
			result.Suggestion = suggestFromClassification(rule.classification)
			return result
		}
	}

	// 4. MCP tools default to ask.
	if strings.HasPrefix(toolName, "mcp__") || strings.HasPrefix(toolName, "mcp_") {
		result.Classification = ClassUnknown
		result.Suggestion = SuggestAskUser
		result.Confidence = 0.50
		result.Reason = "MCP tool: unknown safety"
		return result
	}

	// 5. Fallback.
	result.Classification = ClassUnknown
	result.Suggestion = SuggestAskUser
	result.Confidence = 0.0
	result.Reason = "unable to classify"
	return result
}

// classifyFromTool uses Tool interface metadata.
func (c *ToolClassifier) classifyFromTool(t types.Tool, args map[string]any, result ClassificationResult) ClassificationResult {
	if t.IsDestructive(args) {
		result.Classification = ClassDestructive
		result.Suggestion = SuggestAskUser
		result.Confidence = 1.0
		result.Reason = "tool reports destructive"
		return result
	}

	if t.IsReadOnly(args) {
		result.Classification = ClassReadOnly
		result.Suggestion = SuggestAutoAllow
		result.Confidence = 1.0
		result.Reason = "tool reports read-only"
		return result
	}

	// Not read-only and not destructive → write.
	result.Classification = ClassWrite
	result.Suggestion = SuggestAskUser
	result.Confidence = 0.80
	result.Reason = "tool is not read-only and not marked destructive"
	return result
}

func suggestFromClassification(class ToolClassification) PermissionSuggestion {
	switch class {
	case ClassReadOnly:
		return SuggestAutoAllow
	case ClassWrite:
		return SuggestAskUser
	case ClassDestructive:
		return SuggestAskUser
	default:
		return SuggestAskUser
	}
}

// ExtractKeyInput extracts the most important parameters from tool input
// for display, auditing, and permission prompts.
func ExtractKeyInput(toolName string, args map[string]any) map[string]string {
	extracted := make(map[string]string)

	// File path extraction.
	for _, key := range []string{"path", "file_path", "file", "notebook_path"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				extracted["file_path"] = s
				break
			}
		}
	}

	// Command extraction.
	if cmd, ok := args["command"]; ok {
		if s, ok := cmd.(string); ok && s != "" {
			extracted["command"] = truncateForDisplay(s, 100)
		}
	}

	// URL extraction.
	if url, ok := args["url"]; ok {
		if s, ok := url.(string); ok && s != "" {
			extracted["url"] = s
		}
	}

	// Query/pattern extraction.
	for _, key := range []string{"query", "pattern", "search"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				extracted[key] = truncateForDisplay(s, 80)
				break
			}
		}
	}

	return extracted
}

func truncateForDisplay(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\u2026"
}

// ClassifyBashCommand performs deeper analysis of bash/shell commands to determine
// their classification. This is needed because "run_command" is always marked
// destructive, but many bash commands are actually read-only.
func (c *ToolClassifier) ClassifyBashCommand(command string) ClassificationResult {
	result := ClassificationResult{
		ExtractedInput: map[string]string{"command": truncateForDisplay(command, 100)},
	}

	parts := strings.Fields(command)
	if len(parts) == 0 {
		result.Classification = ClassUnknown
		result.Suggestion = SuggestAskUser
		result.Confidence = 0.0
		result.Reason = "empty command"
		return result
	}

	baseCmd := parts[0]

	// Read-only commands that are safe.
	readOnlyCmds := map[string]bool{
		"ls": true, "cat": true, "head": true, "tail": true,
		"wc": true, "find": true, "grep": true, "rg": true,
		"which": true, "whereis": true, "whoami": true,
		"pwd": true, "echo": true, "date": true, "uname": true,
		"file": true, "stat": true, "du": true, "df": true,
		"env": true, "printenv": true, "type": true,
		"git":  true, // Most git subcommands are read-only; handled below.
		"diff": true, "md5sum": true, "sha256sum": true,
		"tree": true, "less": true, "more": true,
		"python3": false, "python": false, "node": false, // explicitly not read-only
	}

	if readOnlyCmds[baseCmd] {
		// Special-case git: most subcommands are read-only, but some are not.
		if baseCmd == "git" && len(parts) > 1 {
			return c.classifyGitCommand(parts[1:], result)
		}

		result.Classification = ClassReadOnly
		result.Suggestion = SuggestAutoAllow
		result.Confidence = 0.85
		result.Reason = "known read-only command: " + baseCmd
		return result
	}

	// Definitely destructive commands.
	destructiveCmds := map[string]bool{
		"rm": true, "rmdir": true, "mv": true,
		"dd": true, "mkfs": true, "fdisk": true,
		"shutdown": true, "reboot": true, "halt": true,
		"kill": true, "killall": true, "pkill": true,
	}

	if destructiveCmds[baseCmd] {
		result.Classification = ClassDestructive
		result.Suggestion = SuggestAskUser
		result.Confidence = 0.95
		result.Reason = "known destructive command: " + baseCmd
		return result
	}

	// Write commands.
	writeCmds := map[string]bool{
		"cp": true, "mkdir": true, "touch": true,
		"chmod": true, "chown": true, "chgrp": true,
		"sed": true, "awk": true, "tee": true,
		"npm": true, "yarn": true, "pip": true,
		"apt": true, "brew": true, "cargo": true,
		"go": true, "make": true, "cmake": true,
	}

	if writeCmds[baseCmd] {
		result.Classification = ClassWrite
		result.Suggestion = SuggestAskUser
		result.Confidence = 0.80
		result.Reason = "known write command: " + baseCmd
		return result
	}

	// Default: unknown.
	result.Classification = ClassUnknown
	result.Suggestion = SuggestAskUser
	result.Confidence = 0.30
	result.Reason = "unrecognized command: " + baseCmd
	return result
}

// classifyGitCommand classifies git subcommands.
func (c *ToolClassifier) classifyGitCommand(args []string, result ClassificationResult) ClassificationResult {
	if len(args) == 0 {
		result.Classification = ClassReadOnly
		result.Suggestion = SuggestAutoAllow
		result.Confidence = 0.70
		result.Reason = "git with no subcommand"
		return result
	}

	subCmd := args[0]

	readOnlyGit := map[string]bool{
		"status": true, "log": true, "diff": true, "show": true,
		"branch": true, "remote": true, "describe": true, "blame": true,
		"ls-files": true, "ls-tree": true, "rev-parse": true,
		"config": true, "tag": true, "stash": true,
	}

	if readOnlyGit[subCmd] {
		// Check for flags that make these write commands.
		for _, a := range args[1:] {
			if a == "-d" || a == "-D" || a == "--delete" || a == "--force" {
				result.Classification = ClassDestructive
				result.Suggestion = SuggestAskUser
				result.Confidence = 0.90
				result.Reason = "git " + subCmd + " with destructive flag"
				return result
			}
		}
		result.Classification = ClassReadOnly
		result.Suggestion = SuggestAutoAllow
		result.Confidence = 0.90
		result.Reason = "read-only git subcommand: " + subCmd
		return result
	}

	destructiveGit := map[string]bool{
		"push":  true,
		"reset": true,
		"clean": true,
		"rm":    true,
	}

	if destructiveGit[subCmd] {
		// push --force is extra destructive.
		for _, a := range args[1:] {
			if a == "--force" || a == "-f" || a == "--force-with-lease" {
				result.Classification = ClassDestructive
				result.Suggestion = SuggestDeny
				result.Confidence = 0.95
				result.Reason = "git " + subCmd + " --force"
				return result
			}
		}
		result.Classification = ClassDestructive
		result.Suggestion = SuggestAskUser
		result.Confidence = 0.85
		result.Reason = "destructive git subcommand: " + subCmd
		return result
	}

	// git add, commit, checkout, merge, rebase, etc. are write operations.
	result.Classification = ClassWrite
	result.Suggestion = SuggestAskUser
	result.Confidence = 0.75
	result.Reason = "git write subcommand: " + subCmd
	return result
}

// ToolUsePattern tracks usage patterns for a specific tool.
type ToolUsePattern struct {
	ToolName        string
	TotalCalls      int64
	ErrorCalls      int64
	DeniedCalls     int64
	AutoAllowed     int64
	UserApproved    int64
	AvgDuration     time.Duration
	totalDuration   time.Duration
	LastUsed        time.Time
	Classifications map[ToolClassification]int64
}

// ToolUseTracker maintains per-tool usage statistics for pattern analysis.
type ToolUseTracker struct {
	mu       sync.Mutex
	patterns map[string]*ToolUsePattern
}

// NewToolUseTracker creates a usage pattern tracker.
func NewToolUseTracker() *ToolUseTracker {
	return &ToolUseTracker{
		patterns: make(map[string]*ToolUsePattern),
	}
}

// Record records a tool usage event.
func (t *ToolUseTracker) Record(
	toolName string,
	classification ToolClassification,
	duration time.Duration,
	isError bool,
	wasDenied bool,
	wasAutoAllowed bool,
) {
	t.mu.Lock()
	defer t.mu.Unlock()

	p, ok := t.patterns[toolName]
	if !ok {
		p = &ToolUsePattern{
			ToolName:        toolName,
			Classifications: make(map[ToolClassification]int64),
		}
		t.patterns[toolName] = p
	}

	p.TotalCalls++
	p.totalDuration += duration
	p.AvgDuration = p.totalDuration / time.Duration(p.TotalCalls)
	p.LastUsed = time.Now()
	p.Classifications[classification]++

	if isError {
		p.ErrorCalls++
	}
	if wasDenied {
		p.DeniedCalls++
	}
	if wasAutoAllowed {
		p.AutoAllowed++
	} else if !wasDenied && !isError {
		p.UserApproved++
	}
}

// GetPattern returns the usage pattern for a specific tool.
func (t *ToolUseTracker) GetPattern(toolName string) (ToolUsePattern, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.patterns[toolName]
	if !ok {
		return ToolUsePattern{}, false
	}
	return *p, true
}

// AllPatterns returns a copy of all tracked patterns.
func (t *ToolUseTracker) AllPatterns() map[string]ToolUsePattern {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make(map[string]ToolUsePattern, len(t.patterns))
	for k, v := range t.patterns {
		result[k] = *v
	}
	return result
}

// SuggestPermission uses historical patterns to suggest a permission level.
// If a tool has been auto-allowed many times with no errors, it suggests auto-allow.
// If a tool frequently errors, it suggests asking the user.
func (t *ToolUseTracker) SuggestPermission(toolName string) PermissionSuggestion {
	t.mu.Lock()
	defer t.mu.Unlock()

	p, ok := t.patterns[toolName]
	if !ok {
		return SuggestAskUser
	}

	// If the tool has been denied frequently, suggest deny.
	if p.TotalCalls > 5 && float64(p.DeniedCalls)/float64(p.TotalCalls) > 0.5 {
		return SuggestDeny
	}

	// If the tool has a high error rate, suggest asking.
	if p.TotalCalls > 3 && float64(p.ErrorCalls)/float64(p.TotalCalls) > 0.3 {
		return SuggestAskUser
	}

	// If the tool has been auto-allowed many times without issues, keep auto-allowing.
	if p.AutoAllowed > 10 && p.ErrorCalls == 0 {
		return SuggestAutoAllow
	}

	// If user has approved it many times, suggest auto-allow.
	if p.UserApproved > 5 && p.ErrorCalls == 0 {
		return SuggestAutoAllow
	}

	return SuggestAskUser
}
