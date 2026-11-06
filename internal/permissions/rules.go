package permissions

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/costa/polypod/internal/types"
)

// ShellPermissionRuleType classifies how a rule matches commands.
type ShellPermissionRuleType int

const (
	// RuleTypeExact matches the exact command string.
	RuleTypeExact ShellPermissionRuleType = iota
	// RuleTypePrefix matches commands starting with a prefix (legacy :* syntax).
	RuleTypePrefix
	// RuleTypeWildcard matches using glob-style wildcard patterns.
	RuleTypeWildcard
)

// ShellPermissionRule is a parsed permission rule for shell commands.
type ShellPermissionRule struct {
	Type  ShellPermissionRuleType
	Value string // The exact command, prefix, or wildcard pattern
	Raw   string // The original rule string
}

// PermissionRuleSet holds rules organized by behavior with source hierarchy.
type PermissionRuleSet struct {
	// AllowRules organized by source
	AllowRules map[types.PermissionRuleSource][]types.PermissionRule
	// DenyRules organized by source
	DenyRules map[types.PermissionRuleSource][]types.PermissionRule
	// AskRules organized by source
	AskRules map[types.PermissionRuleSource][]types.PermissionRule
}

// NewPermissionRuleSet creates an empty rule set.
func NewPermissionRuleSet() *PermissionRuleSet {
	return &PermissionRuleSet{
		AllowRules: make(map[types.PermissionRuleSource][]types.PermissionRule),
		DenyRules:  make(map[types.PermissionRuleSource][]types.PermissionRule),
		AskRules:   make(map[types.PermissionRuleSource][]types.PermissionRule),
	}
}

// AddRule adds a rule to the appropriate list based on its behavior.
func (rs *PermissionRuleSet) AddRule(rule types.PermissionRule) {
	switch rule.Behavior {
	case "allow":
		rs.AllowRules[rule.Source] = append(rs.AllowRules[rule.Source], rule)
	case "deny":
		rs.DenyRules[rule.Source] = append(rs.DenyRules[rule.Source], rule)
	case "ask":
		rs.AskRules[rule.Source] = append(rs.AskRules[rule.Source], rule)
	}
}

// GetAllRules returns all rules of a given behavior, flattened from all sources.
func (rs *PermissionRuleSet) GetAllRules(behavior string) []types.PermissionRule {
	var result []types.PermissionRule
	var ruleMap map[types.PermissionRuleSource][]types.PermissionRule

	switch behavior {
	case "allow":
		ruleMap = rs.AllowRules
	case "deny":
		ruleMap = rs.DenyRules
	case "ask":
		ruleMap = rs.AskRules
	default:
		return nil
	}

	for _, rules := range ruleMap {
		result = append(result, rules...)
	}
	return result
}

// AllSources is the ordered list of all permission rule sources.
var AllSources = []types.PermissionRuleSource{
	types.SourceUserSettings,
	types.SourceProjectSettings,
	types.SourceLocalSettings,
	types.SourceFlagSettings,
	types.SourcePolicySettings,
	types.SourceCLIArg,
	types.SourceCommand,
	types.SourceSession,
}

// LegacyToolNameAliases maps old tool names to their current canonical names.
var LegacyToolNameAliases = map[string]string{
	"Task":            "Agent",
	"KillShell":       "TaskStop",
	"AgentOutputTool": "TaskOutput",
	"BashOutputTool":  "TaskOutput",
}

// NormalizeLegacyToolName converts legacy tool names to their canonical form.
func NormalizeLegacyToolName(name string) string {
	if canonical, ok := LegacyToolNameAliases[name]; ok {
		return canonical
	}
	return name
}

// GetLegacyToolNames returns all legacy names for a canonical tool name.
func GetLegacyToolNames(canonicalName string) []string {
	var result []string
	for legacy, canonical := range LegacyToolNameAliases {
		if canonical == canonicalName {
			result = append(result, legacy)
		}
	}
	return result
}

// EscapeRuleContent escapes special characters in rule content for safe storage.
// Permission rules use the format "Tool(content)", so parentheses in content must be escaped.
func EscapeRuleContent(content string) string {
	// Escape backslashes first, then parentheses
	content = strings.ReplaceAll(content, `\`, `\\`)
	content = strings.ReplaceAll(content, `(`, `\(`)
	content = strings.ReplaceAll(content, `)`, `\)`)
	return content
}

// UnescapeRuleContent reverses the escaping done by EscapeRuleContent.
func UnescapeRuleContent(content string) string {
	// Unescape parentheses first, then backslashes
	content = strings.ReplaceAll(content, `\(`, `(`)
	content = strings.ReplaceAll(content, `\)`, `)`)
	content = strings.ReplaceAll(content, `\\`, `\`)
	return content
}

// ParsePermissionRuleValue parses a permission rule string into its components.
// Format: "ToolName" or "ToolName(content)"
// Content may contain escaped parentheses: \( and \)
func ParsePermissionRuleValue(ruleString string) types.PermissionRule {
	// Find the first unescaped opening parenthesis
	openIdx := findFirstUnescapedChar(ruleString, '(')
	if openIdx == -1 {
		return types.PermissionRule{
			ToolName: NormalizeLegacyToolName(ruleString),
		}
	}

	// Find the last unescaped closing parenthesis
	closeIdx := findLastUnescapedChar(ruleString, ')')
	if closeIdx == -1 || closeIdx <= openIdx {
		return types.PermissionRule{
			ToolName: NormalizeLegacyToolName(ruleString),
		}
	}

	// Ensure the closing paren is at the end
	if closeIdx != len(ruleString)-1 {
		return types.PermissionRule{
			ToolName: NormalizeLegacyToolName(ruleString),
		}
	}

	toolName := ruleString[:openIdx]
	rawContent := ruleString[openIdx+1 : closeIdx]

	// Missing toolName
	if toolName == "" {
		return types.PermissionRule{
			ToolName: NormalizeLegacyToolName(ruleString),
		}
	}

	// Empty content or standalone wildcard treated as tool-wide rule
	if rawContent == "" || rawContent == "*" {
		return types.PermissionRule{
			ToolName: NormalizeLegacyToolName(toolName),
		}
	}

	ruleContent := UnescapeRuleContent(rawContent)
	return types.PermissionRule{
		ToolName:    NormalizeLegacyToolName(toolName),
		RuleContent: ruleContent,
	}
}

// PermissionRuleValueToString converts a permission rule to its string representation.
func PermissionRuleValueToString(rule types.PermissionRule) string {
	if rule.RuleContent == "" {
		return rule.ToolName
	}
	escaped := EscapeRuleContent(rule.RuleContent)
	return fmt.Sprintf("%s(%s)", rule.ToolName, escaped)
}

// findFirstUnescapedChar finds the index of the first unescaped occurrence of a character.
func findFirstUnescapedChar(s string, ch byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ch {
			backslashCount := 0
			j := i - 1
			for j >= 0 && s[j] == '\\' {
				backslashCount++
				j--
			}
			if backslashCount%2 == 0 {
				return i
			}
		}
	}
	return -1
}

// findLastUnescapedChar finds the index of the last unescaped occurrence of a character.
func findLastUnescapedChar(s string, ch byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ch {
			backslashCount := 0
			j := i - 1
			for j >= 0 && s[j] == '\\' {
				backslashCount++
				j--
			}
			if backslashCount%2 == 0 {
				return i
			}
		}
	}
	return -1
}

// ParseShellPermissionRule parses a shell-specific permission rule string.
func ParseShellPermissionRule(rule string) ShellPermissionRule {
	// Check for legacy :* prefix syntax first
	if prefix := extractLegacyPrefix(rule); prefix != "" {
		return ShellPermissionRule{
			Type:  RuleTypePrefix,
			Value: prefix,
			Raw:   rule,
		}
	}

	// Check for new wildcard syntax
	if hasWildcards(rule) {
		return ShellPermissionRule{
			Type:  RuleTypeWildcard,
			Value: rule,
			Raw:   rule,
		}
	}

	// Exact match
	return ShellPermissionRule{
		Type:  RuleTypeExact,
		Value: rule,
		Raw:   rule,
	}
}

// MatchRule checks if a command matches a shell permission rule.
func MatchRule(rule ShellPermissionRule, command string) bool {
	switch rule.Type {
	case RuleTypeExact:
		return rule.Value == command
	case RuleTypePrefix:
		return strings.HasPrefix(command, rule.Value) &&
			(len(command) == len(rule.Value) ||
				command[len(rule.Value)] == ' ' ||
				command[len(rule.Value)] == '\t')
	case RuleTypeWildcard:
		return matchWildcardPattern(rule.Value, command)
	}
	return false
}

// MatchToolRule checks if a tool name matches a permission rule.
// Supports MCP server-level matching: rule "mcp__server1" matches "mcp__server1__tool1".
func MatchToolRule(rule types.PermissionRule, toolName string) bool {
	// Rule must not have content to match the entire tool
	if rule.RuleContent != "" {
		return false
	}

	// Direct tool name match
	if rule.ToolName == toolName {
		return true
	}

	// MCP server-level matching: rule "mcp__server1" matches "mcp__server1__tool1"
	if strings.HasPrefix(rule.ToolName, "mcp__") && strings.HasPrefix(toolName, "mcp__") {
		ruleParts := strings.SplitN(rule.ToolName, "__", 3)
		toolParts := strings.SplitN(toolName, "__", 3)

		if len(ruleParts) == 2 && len(toolParts) == 3 {
			// Rule is server-level, tool is server+tool
			return ruleParts[1] == toolParts[1]
		}
		// Rule has wildcard tool: mcp__server__*
		if len(ruleParts) == 3 && ruleParts[2] == "*" && len(toolParts) == 3 {
			return ruleParts[1] == toolParts[1]
		}
	}

	return false
}

// GetRulesByContentForTool returns rules for a specific tool that have content.
// Used to find content-specific rules like Bash(ls:*), Bash(npm install), etc.
func GetRulesByContentForTool(ruleSet *PermissionRuleSet, toolName string, behavior string) map[string]types.PermissionRule {
	result := make(map[string]types.PermissionRule)
	rules := ruleSet.GetAllRules(behavior)

	for _, rule := range rules {
		if rule.ToolName == toolName && rule.RuleContent != "" {
			result[rule.RuleContent] = rule
		}
	}
	return result
}

// DetectShadowedRules finds rules that conflict with each other (unreachable rules).
func DetectShadowedRules(ruleSet *PermissionRuleSet) []ShadowedRule {
	var results []ShadowedRule

	allowRules := ruleSet.GetAllRules("allow")
	denyRules := ruleSet.GetAllRules("deny")
	askRules := ruleSet.GetAllRules("ask")

	for _, allowRule := range allowRules {
		// Skip tool-wide rules (they can't be shadowed)
		if allowRule.RuleContent == "" {
			continue
		}

		// Check for deny-shadowing (more severe)
		for _, denyRule := range denyRules {
			if denyRule.ToolName == allowRule.ToolName && denyRule.RuleContent == "" {
				results = append(results, ShadowedRule{
					Rule:       allowRule,
					ShadowedBy: denyRule,
					ShadowType: "deny",
					Reason:     fmt.Sprintf("blocked by \"%s\" deny rule (from %s)", denyRule.ToolName, denyRule.Source),
					Fix:        fmt.Sprintf("remove the \"%s\" deny rule from %s, or remove the specific allow rule from %s", denyRule.ToolName, denyRule.Source, allowRule.Source),
				})
				break
			}
		}

		// Check for ask-shadowing
		for _, askRule := range askRules {
			if askRule.ToolName == allowRule.ToolName && askRule.RuleContent == "" {
				results = append(results, ShadowedRule{
					Rule:       allowRule,
					ShadowedBy: askRule,
					ShadowType: "ask",
					Reason:     fmt.Sprintf("shadowed by \"%s\" ask rule (from %s)", askRule.ToolName, askRule.Source),
					Fix:        fmt.Sprintf("remove the \"%s\" ask rule from %s, or remove the specific allow rule from %s", askRule.ToolName, askRule.Source, allowRule.Source),
				})
				break
			}
		}
	}

	return results
}

// ShadowedRule represents an unreachable permission rule.
type ShadowedRule struct {
	Rule       types.PermissionRule
	ShadowedBy types.PermissionRule
	ShadowType string // "deny" or "ask"
	Reason     string
	Fix        string
}

// --- Internal Helpers ---

// extractLegacyPrefix extracts the prefix from legacy :* syntax (e.g., "npm:*" -> "npm").
func extractLegacyPrefix(rule string) string {
	if strings.HasSuffix(rule, ":*") {
		return rule[:len(rule)-2]
	}
	return ""
}

// hasWildcards checks if a pattern contains unescaped wildcards (not legacy :* syntax).
func hasWildcards(pattern string) bool {
	if strings.HasSuffix(pattern, ":*") {
		return false
	}
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '*' {
			backslashCount := 0
			j := i - 1
			for j >= 0 && pattern[j] == '\\' {
				backslashCount++
				j--
			}
			if backslashCount%2 == 0 {
				return true
			}
		}
	}
	return false
}

// Placeholders for wildcard pattern escaping
const (
	escapedStarPlaceholder      = "\x00ESCAPED_STAR\x00"
	escapedBackslashPlaceholder = "\x00ESCAPED_BACKSLASH\x00"
)

// matchWildcardPattern matches a command against a wildcard pattern.
// Wildcards (*) match any sequence of characters.
// Use \* to match a literal asterisk character.
// Use \\ to match a literal backslash.
func matchWildcardPattern(pattern, command string) bool {
	pattern = strings.TrimSpace(pattern)

	// Process escape sequences
	var processed strings.Builder
	i := 0
	for i < len(pattern) {
		ch := pattern[i]
		if ch == '\\' && i+1 < len(pattern) {
			next := pattern[i+1]
			switch next {
			case '*':
				processed.WriteString(escapedStarPlaceholder)
				i += 2
				continue
			case '\\':
				processed.WriteString(escapedBackslashPlaceholder)
				i += 2
				continue
			}
		}
		processed.WriteByte(ch)
		i++
	}

	processedStr := processed.String()

	// Escape regex special characters except *
	escaped := regexp.QuoteMeta(processedStr)
	// QuoteMeta also escapes * — undo that
	escaped = strings.ReplaceAll(escaped, `\*`, `*`)
	// Now convert unescaped * to .*
	withWildcards := strings.ReplaceAll(escaped, "*", ".*")

	// Convert placeholders back
	regexPattern := strings.ReplaceAll(withWildcards, regexp.QuoteMeta(escapedStarPlaceholder), `\*`)
	regexPattern = strings.ReplaceAll(regexPattern, regexp.QuoteMeta(escapedBackslashPlaceholder), `\\`)

	// When pattern ends with ' *' (space + wildcard) and that's the only wildcard,
	// make the trailing space-and-args optional.
	unescapedStars := strings.Count(processedStr, "*")
	if strings.HasSuffix(regexPattern, " .*") && unescapedStars == 1 {
		regexPattern = regexPattern[:len(regexPattern)-3] + "( .*)?"
	}

	// Match entire string, dotAll mode
	re, err := regexp.Compile(`(?s)^` + regexPattern + `$`)
	if err != nil {
		return false
	}
	return re.MatchString(command)
}

// RuleSetToJSON serializes a PermissionRuleSet to JSON.
func RuleSetToJSON(rs *PermissionRuleSet) ([]byte, error) {
	type jsonRuleSet struct {
		Allow map[string][]string `json:"allow,omitempty"`
		Deny  map[string][]string `json:"deny,omitempty"`
		Ask   map[string][]string `json:"ask,omitempty"`
	}

	out := jsonRuleSet{
		Allow: make(map[string][]string),
		Deny:  make(map[string][]string),
		Ask:   make(map[string][]string),
	}

	for source, rules := range rs.AllowRules {
		for _, r := range rules {
			out.Allow[string(source)] = append(out.Allow[string(source)], PermissionRuleValueToString(r))
		}
	}
	for source, rules := range rs.DenyRules {
		for _, r := range rules {
			out.Deny[string(source)] = append(out.Deny[string(source)], PermissionRuleValueToString(r))
		}
	}
	for source, rules := range rs.AskRules {
		for _, r := range rules {
			out.Ask[string(source)] = append(out.Ask[string(source)], PermissionRuleValueToString(r))
		}
	}

	return json.MarshalIndent(out, "", "  ")
}

// RuleSetFromJSON deserializes a PermissionRuleSet from JSON.
func RuleSetFromJSON(data []byte) (*PermissionRuleSet, error) {
	type jsonRuleSet struct {
		Allow map[string][]string `json:"allow,omitempty"`
		Deny  map[string][]string `json:"deny,omitempty"`
		Ask   map[string][]string `json:"ask,omitempty"`
	}

	var in jsonRuleSet
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, err
	}

	rs := NewPermissionRuleSet()

	for source, rules := range in.Allow {
		for _, ruleStr := range rules {
			parsed := ParsePermissionRuleValue(ruleStr)
			parsed.Source = types.PermissionRuleSource(source)
			parsed.Behavior = "allow"
			rs.AddRule(parsed)
		}
	}
	for source, rules := range in.Deny {
		for _, ruleStr := range rules {
			parsed := ParsePermissionRuleValue(ruleStr)
			parsed.Source = types.PermissionRuleSource(source)
			parsed.Behavior = "deny"
			rs.AddRule(parsed)
		}
	}
	for source, rules := range in.Ask {
		for _, ruleStr := range rules {
			parsed := ParsePermissionRuleValue(ruleStr)
			parsed.Source = types.PermissionRuleSource(source)
			parsed.Behavior = "ask"
			rs.AddRule(parsed)
		}
	}

	return rs, nil
}
