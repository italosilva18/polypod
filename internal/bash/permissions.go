// Package bash provides the bash permission system.
// This file implements command classification, permission rule matching,
// and dangerous command detection. Migrated from Claude Code's bashPermissions.ts.
package bash

import (
	"path/filepath"
	"regexp"
	"strings"
)

// CommandCategory classifies commands by their risk level.
type CommandCategory int

const (
	// CategoryReadOnly commands only read data (ls, cat, grep, etc.).
	CategoryReadOnly CommandCategory = iota

	// CategoryWrite commands modify files or state (touch, mkdir, cp, etc.).
	CategoryWrite

	// CategoryDestructive commands can cause irreversible damage (rm -rf, git push --force).
	CategoryDestructive

	// CategoryNetwork commands access the network (curl, wget, ssh, etc.).
	CategoryNetwork

	// CategoryExecution commands execute arbitrary code (eval, source, python, etc.).
	CategoryExecution

	// CategoryPrivileged commands require elevated privileges (sudo, su, etc.).
	CategoryPrivileged
)

// String returns the human-readable name for a CommandCategory.
func (c CommandCategory) String() string {
	switch c {
	case CategoryReadOnly:
		return "read-only"
	case CategoryWrite:
		return "write"
	case CategoryDestructive:
		return "destructive"
	case CategoryNetwork:
		return "network"
	case CategoryExecution:
		return "execution"
	case CategoryPrivileged:
		return "privileged"
	default:
		return "unknown"
	}
}

// PermissionBehavior is the result of a permission rule check.
type PermissionBehavior string

const (
	BehaviorAllow       PermissionBehavior = "allow"
	BehaviorDeny        PermissionBehavior = "deny"
	BehaviorAsk         PermissionBehavior = "ask"
	BehaviorPassthrough PermissionBehavior = "passthrough"
)

// PermissionRuleType defines the kind of permission rule.
type PermissionRuleType int

const (
	RuleExact    PermissionRuleType = iota // exact command match
	RulePrefix                             // prefix:* match
	RuleWildcard                           // glob-style wildcard match
)

// PermissionRule represents a parsed Bash permission rule.
type PermissionRule struct {
	Type     PermissionRuleType
	Behavior PermissionBehavior
	Raw      string // original rule string
	Command  string // for exact: the command; for prefix: the prefix
	Pattern  string // for wildcard: the glob pattern
}

// ParsePermissionRule parses a rule string like "npm run:*" into a PermissionRule.
// Supports:
//   - Exact match: "npm install express"
//   - Prefix match: "npm run:*" (matches "npm run test", "npm run build", etc.)
//   - Wildcard: "git * --no-verify" (glob-style matching)
func ParsePermissionRule(rule string) PermissionRule {
	rule = strings.TrimSpace(rule)

	// Check for prefix pattern: "command:*" at the end
	if strings.HasSuffix(rule, ":*") {
		prefix := strings.TrimSuffix(rule, ":*")
		return PermissionRule{
			Type:    RulePrefix,
			Raw:     rule,
			Command: prefix,
		}
	}

	// Check for wildcard patterns (contains * or ?)
	if strings.ContainsAny(rule, "*?") {
		return PermissionRule{
			Type:    RuleWildcard,
			Raw:     rule,
			Pattern: rule,
		}
	}

	// Exact match
	return PermissionRule{
		Type:    RuleExact,
		Raw:     rule,
		Command: rule,
	}
}

// MatchCommand checks if a command matches this permission rule.
func (r *PermissionRule) MatchCommand(command string) bool {
	command = strings.TrimSpace(command)

	switch r.Type {
	case RuleExact:
		return command == r.Command
	case RulePrefix:
		if command == r.Command {
			return true
		}
		return strings.HasPrefix(command, r.Command+" ")
	case RuleWildcard:
		return MatchWildcardPattern(r.Pattern, command)
	}
	return false
}

// MatchWildcardPattern matches a command against a wildcard pattern (case-sensitive).
// The pattern uses * for any sequence and ? for any single char.
func MatchWildcardPattern(pattern, command string) bool {
	matched, _ := filepath.Match(pattern, command)
	if matched {
		return true
	}

	// filepath.Match doesn't support ** or mid-word * well.
	// Fall back to regex-based matching.
	regexStr := "^" + wildcardToRegex(pattern) + "$"
	re, err := regexp.Compile(regexStr)
	if err != nil {
		return false
	}
	return re.MatchString(command)
}

// wildcardToRegex converts a wildcard pattern to a regex string.
func wildcardToRegex(pattern string) string {
	var sb strings.Builder
	for _, ch := range pattern {
		switch ch {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteString(".")
		case '.', '+', '^', '$', '(', ')', '[', ']', '{', '}', '|', '\\':
			sb.WriteRune('\\')
			sb.WriteRune(ch)
		default:
			sb.WriteRune(ch)
		}
	}
	return sb.String()
}

// PermissionRuleExtractPrefix extracts the prefix from a ":*" rule,
// or returns "" if the rule is not a prefix rule.
func PermissionRuleExtractPrefix(rule string) string {
	rule = strings.TrimSpace(rule)
	if strings.HasSuffix(rule, ":*") {
		return strings.TrimSuffix(rule, ":*")
	}
	return ""
}

// ClassifyCommand determines the risk category of a command.
func ClassifyCommand(command string) CommandCategory {
	base := GetBaseCommand(command)
	if base == "" {
		return CategoryReadOnly
	}

	if destructiveCmds[base] {
		return CategoryDestructive
	}
	if networkCmds[base] {
		return CategoryNetwork
	}
	if execCmds[base] {
		return CategoryExecution
	}
	if privilegedCmds[base] {
		return CategoryPrivileged
	}
	if writeCmds[base] {
		return CategoryWrite
	}
	if readOnlyCmds[base] {
		return CategoryReadOnly
	}

	// Check destructive flag patterns
	for _, p := range destructivePatterns {
		if p.re.MatchString(command) {
			return CategoryDestructive
		}
	}

	return CategoryWrite // default to write for unknown commands
}

// DangerousCommandInfo describes a specific danger in a command.
type DangerousCommandInfo struct {
	Pattern  string
	Message  string
	Severity string // "critical", "high", "medium"
}

// DetectDangerousCommand checks a command for dangerous patterns.
// Returns nil if no dangerous patterns are found.
func DetectDangerousCommand(command string) []DangerousCommandInfo {
	var dangers []DangerousCommandInfo

	for _, p := range dangerousCommandPatterns {
		if p.re.MatchString(command) {
			dangers = append(dangers, DangerousCommandInfo{
				Pattern:  p.pattern,
				Message:  p.message,
				Severity: p.severity,
			})
		}
	}

	return dangers
}

// IsAutoAllowed returns true if a command should be auto-allowed without prompting.
// Only pure read-only commands with no risk qualify.
func IsAutoAllowed(command string) bool {
	// Must be a simple command (no shell operators)
	if containsShellOperators(command) {
		return false
	}

	base := GetBaseCommand(command)
	if base == "" {
		return true // empty command
	}

	// Only auto-allow explicitly known read-only commands
	return readOnlyCmds[base] && !hasOutputRedirection(command)
}

// containsShellOperators checks if a command contains unquoted operators.
func containsShellOperators(cmd string) bool {
	inSingle := false
	inDouble := false
	escaped := false

	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if !inSingle && !inDouble {
			if ch == ';' || ch == '|' || ch == '&' {
				return true
			}
		}
	}
	return false
}

// hasOutputRedirection checks if a command has output redirection.
func hasOutputRedirection(cmd string) bool {
	inSingle := false
	inDouble := false
	escaped := false

	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if !inSingle && !inDouble && ch == '>' {
			return true
		}
	}
	return false
}

// GetSimpleCommandPrefix extracts a stable "cmd subcmd" prefix for rule generation.
// Returns "" if the command doesn't have a subcommand-style structure.
func GetSimpleCommandPrefix(command string) string {
	stripped := StripSafeWrappers(strings.TrimSpace(command))
	tokens := strings.Fields(stripped)
	if len(tokens) < 2 {
		return ""
	}

	subcmd := tokens[1]
	// Subcommand must look like a real word (lowercase alnum), not a flag/path/file
	matched, _ := regexp.MatchString(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`, subcmd)
	if !matched {
		return ""
	}

	prefix := tokens[0] + " " + tokens[1]

	// Don't generate prefix rules for shell/wrapper commands
	if bareShellPrefixes[tokens[0]] {
		return ""
	}

	return prefix
}

// bareShellPrefixes are commands that should never generate bare prefix rules.
var bareShellPrefixes = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "csh": true,
	"tcsh": true, "ksh": true, "dash": true, "cmd": true,
	"powershell": true, "pwsh": true,
	"env": true, "xargs": true,
	"nice": true, "stdbuf": true, "nohup": true, "timeout": true, "time": true,
	"sudo": true, "doas": true, "pkexec": true,
}

// Command classification tables.
var readOnlyCmds = map[string]bool{
	"ls": true, "pwd": true, "whoami": true, "hostname": true, "date": true,
	"cat": true, "head": true, "tail": true, "less": true, "more": true,
	"wc": true, "file": true, "stat": true, "type": true, "which": true,
	"echo": true, "printf": true, "uname": true, "env": true, "printenv": true,
	"history": true, "alias": true, "tree": true, "du": true, "df": true,
	"free": true, "uptime": true, "id": true, "groups": true,
	"find": true, "grep": true, "rg": true, "fd": true, "ag": true,
	"ack": true, "locate": true, "man": true, "help": true, "info": true,
	"base64": true, "sha256sum": true, "sha1sum": true, "md5sum": true,
	"sort": true, "uniq": true, "cut": true, "tr": true, "awk": true,
	"ps": true, "top": true, "pgrep": true, "lsof": true, "ss": true,
	"netstat": true, "diff": true, "strings": true, "hexdump": true,
	"od": true, "nl": true, "jq": true, "column": true, "paste": true,
	"test": true, "[": true, "true": true, "false": true, ":": true,
}

var writeCmds = map[string]bool{
	"touch": true, "mkdir": true, "cp": true, "mv": true, "ln": true,
	"chmod": true, "chown": true, "chgrp": true, "install": true,
	"tee": true, "sed": true, "patch": true, "rsync": true,
	"npm": true, "yarn": true, "pnpm": true, "pip": true, "pip3": true,
	"go": true, "cargo": true, "make": true, "cmake": true,
	"docker": true, "podman": true, "terraform": true,
	"git": true, // git has both read and write; default to write
}

var destructiveCmds = map[string]bool{
	"rm": true, "rmdir": true, "shred": true,
	"dd": true, "mkfs": true, "fdisk": true, "parted": true,
}

var networkCmds = map[string]bool{
	"curl": true, "wget": true, "ssh": true, "scp": true, "sftp": true,
	"rsync": true, "nc": true, "ncat": true, "telnet": true,
	"ftp": true, "ping": true, "traceroute": true,
}

var execCmds = map[string]bool{
	"eval": true, "exec": true, "source": true, ".": true,
	"python": true, "python3": true, "node": true, "ruby": true,
	"perl": true, "php": true, "java": true, "javac": true,
}

var privilegedCmds = map[string]bool{
	"sudo": true, "su": true, "doas": true, "pkexec": true,
	"mount": true, "umount": true, "chroot": true,
	"systemctl": true, "service": true, "journalctl": true,
}

type dangerousPattern struct {
	pattern  string
	re       *regexp.Regexp
	message  string
	severity string
}

var destructivePatterns = []dangerousPattern{
	{pattern: `rm\s+-\S*r\S*f`, re: regexp.MustCompile(`rm\s+-\S*r\S*f`), message: "recursive force delete", severity: "critical"},
	{pattern: `rm\s+-\S*f\S*r`, re: regexp.MustCompile(`rm\s+-\S*f\S*r`), message: "recursive force delete", severity: "critical"},
}

var dangerousCommandPatterns = []dangerousPattern{
	// File deletion
	{pattern: `rm\s+-rf\s+/`, re: regexp.MustCompile(`rm\s+-rf\s+/`), message: "recursive delete of root filesystem", severity: "critical"},
	{pattern: `rm\s+-rf\s+~`, re: regexp.MustCompile(`rm\s+-rf\s+~`), message: "recursive delete of home directory", severity: "critical"},
	{pattern: `rm\s+-rf\s+\*`, re: regexp.MustCompile(`rm\s+-rf\s+\*`), message: "recursive delete of all files", severity: "critical"},

	// Git destructive
	{pattern: `git\s+push\s+--force`, re: regexp.MustCompile(`git\s+push\s+--force`), message: "force push can overwrite remote history", severity: "high"},
	{pattern: `git\s+push\s+-f`, re: regexp.MustCompile(`git\s+push\s+-f\b`), message: "force push can overwrite remote history", severity: "high"},
	{pattern: `git\s+reset\s+--hard`, re: regexp.MustCompile(`git\s+reset\s+--hard`), message: "hard reset discards commits permanently", severity: "high"},
	{pattern: `git\s+clean\s+-f`, re: regexp.MustCompile(`git\s+clean\s+-f`), message: "git clean -f removes untracked files permanently", severity: "high"},
	{pattern: `--no-verify`, re: regexp.MustCompile(`--no-verify`), message: "skips pre-commit/pre-push hooks", severity: "medium"},

	// Disk/system operations
	{pattern: `dd\s+`, re: regexp.MustCompile(`dd\s+`), message: "dd can overwrite disk sectors directly", severity: "high"},
	{pattern: `mkfs`, re: regexp.MustCompile(`\bmkfs\b`), message: "mkfs creates filesystem (destroys existing data)", severity: "critical"},

	// Database destructive
	{pattern: `DROP\s+TABLE`, re: regexp.MustCompile(`(?i)DROP\s+TABLE`), message: "drops database table permanently", severity: "critical"},
	{pattern: `DROP\s+DATABASE`, re: regexp.MustCompile(`(?i)DROP\s+DATABASE`), message: "drops entire database permanently", severity: "critical"},
	{pattern: `DELETE\s+FROM`, re: regexp.MustCompile(`(?i)DELETE\s+FROM\s+\w+\s*$`), message: "DELETE without WHERE clause deletes all rows", severity: "high"},
	{pattern: `TRUNCATE`, re: regexp.MustCompile(`(?i)\bTRUNCATE\b`), message: "truncate removes all data from table", severity: "high"},

	// Security bypass
	{pattern: `chmod\s+777`, re: regexp.MustCompile(`chmod\s+777`), message: "chmod 777 makes files world-writable", severity: "medium"},
	{pattern: `chmod\s+-R\s+777`, re: regexp.MustCompile(`chmod\s+-R\s+777`), message: "recursive chmod 777 is extremely dangerous", severity: "high"},

	// Exfiltration risk
	{pattern: `/dev/tcp/`, re: regexp.MustCompile(`/dev/tcp/`), message: "bash /dev/tcp can exfiltrate data", severity: "high"},
	{pattern: `/dev/udp/`, re: regexp.MustCompile(`/dev/udp/`), message: "bash /dev/udp can exfiltrate data", severity: "high"},

	// Code injection
	{pattern: `\beval\b`, re: regexp.MustCompile(`\beval\b`), message: "eval executes arbitrary code", severity: "high"},
}

// PermissionRuleSet holds categorized permission rules for matching.
type PermissionRuleSet struct {
	AllowRules []PermissionRule
	DenyRules  []PermissionRule
	AskRules   []PermissionRule
}

// CheckCommand evaluates a command against a rule set.
// Returns (behavior, matchedRule). If no rule matches, returns BehaviorPassthrough.
func (rs *PermissionRuleSet) CheckCommand(command string) (PermissionBehavior, *PermissionRule) {
	command = strings.TrimSpace(command)
	stripped := StripSafeWrappers(command)

	commandsToCheck := []string{command}
	if stripped != command {
		commandsToCheck = append(commandsToCheck, stripped)
	}

	// 1. Check deny rules first (highest priority)
	for i := range rs.DenyRules {
		for _, cmd := range commandsToCheck {
			if rs.DenyRules[i].MatchCommand(cmd) {
				return BehaviorDeny, &rs.DenyRules[i]
			}
		}
	}

	// 2. Check ask rules
	for i := range rs.AskRules {
		for _, cmd := range commandsToCheck {
			if rs.AskRules[i].MatchCommand(cmd) {
				return BehaviorAsk, &rs.AskRules[i]
			}
		}
	}

	// 3. Check allow rules
	for i := range rs.AllowRules {
		for _, cmd := range commandsToCheck {
			if rs.AllowRules[i].MatchCommand(cmd) {
				return BehaviorAllow, &rs.AllowRules[i]
			}
		}
	}

	return BehaviorPassthrough, nil
}

// CheckCompoundCommand evaluates each subcommand in a compound command.
// Returns deny if ANY subcommand is denied, ask if any requires asking,
// allow only if ALL subcommands are allowed.
func (rs *PermissionRuleSet) CheckCompoundCommand(command string) (PermissionBehavior, *PermissionRule) {
	subs := SplitCommand(command)
	if len(subs) <= 1 {
		return rs.CheckCommand(command)
	}

	allAllowed := true
	var firstAsk *PermissionRule

	for _, sub := range subs {
		behavior, rule := rs.CheckCommand(sub)
		switch behavior {
		case BehaviorDeny:
			return BehaviorDeny, rule
		case BehaviorAsk:
			allAllowed = false
			if firstAsk == nil {
				firstAsk = rule
			}
		case BehaviorPassthrough:
			allAllowed = false
		}
	}

	if firstAsk != nil {
		return BehaviorAsk, firstAsk
	}
	if allAllowed {
		return BehaviorAllow, nil
	}
	return BehaviorPassthrough, nil
}

// SuggestRuleForCommand generates a permission rule suggestion for a command.
func SuggestRuleForCommand(command string) string {
	prefix := GetSimpleCommandPrefix(command)
	if prefix != "" {
		return prefix + ":*"
	}

	// For heredoc commands, extract the prefix before <<
	if idx := strings.Index(command, "<<"); idx > 0 {
		before := strings.TrimSpace(command[:idx])
		p := GetSimpleCommandPrefix(before)
		if p != "" {
			return p + ":*"
		}
	}

	// Fall back to the exact command
	return command
}
