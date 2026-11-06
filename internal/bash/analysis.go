package bash

import (
	"regexp"
	"strings"
)

// SecurityIssue represents a security concern found during AST analysis.
type SecurityIssue struct {
	Type    string // "injection", "traversal", "privilege", "exfiltration", "obfuscation"
	Message string
	Command string // The command text that triggered the issue
	Node    *Node  // The AST node, if available
}

// AnalysisResult holds the results of security analysis on a command AST.
type AnalysisResult struct {
	Issues      []SecurityIssue
	AllPaths    []string // All file paths extracted from the AST
	AllCommands []string // All command names found in the AST
	IsReadOnly  bool     // Whether the AST represents read-only operations
}

// AnalyzeCommand walks the AST and performs comprehensive security analysis.
// Returns all detected security issues and extracted metadata.
func AnalyzeCommand(root *Node) *AnalysisResult {
	if root == nil {
		return &AnalysisResult{IsReadOnly: true}
	}

	result := &AnalysisResult{
		IsReadOnly: true,
	}

	Walk(root, func(n *Node, _ int) bool {
		switch n.Type {
		case NodeCommand:
			analyzeCommandNode(n, result)
		case NodeAssignment:
			// Assignments are generally safe
		case NodeRedirect:
			if n.RedirOp == RedirectOutput || n.RedirOp == RedirectAppend ||
				n.RedirOp == RedirectClobber || n.RedirOp == RedirectBothOut ||
				n.RedirOp == RedirectBothAppend {
				result.IsReadOnly = false
				if n.Target != "" {
					result.AllPaths = append(result.AllPaths, n.Target)
				}
			}
			if n.Target != "" {
				checkPathTraversal(n.Target, n, result)
			}
		case NodeCommandSubstitution, NodeProcessSubstitution:
			// Command substitutions are inherently complex
		case NodeHeredoc:
			if n.Target != "" {
				result.AllPaths = append(result.AllPaths, n.Target)
			}
		}
		return true
	})

	return result
}

func analyzeCommandNode(n *Node, result *AnalysisResult) {
	if n.Name == "" && len(n.Args) == 0 {
		return
	}

	name := n.Name
	if name == "" && len(n.Args) > 0 {
		name = n.Args[0]
	}
	result.AllCommands = append(result.AllCommands, name)

	// Check for read-only
	if !isReadOnlyCommandName(name) {
		result.IsReadOnly = false
	}

	// Check all arguments for paths
	for _, arg := range n.Args {
		if looksLikePath(arg) {
			result.AllPaths = append(result.AllPaths, arg)
		}
	}

	// Run specific detectors
	detectCommandInjectionNode(n, name, result)
	detectPrivilegeEscalationNode(n, name, result)
	detectDataExfiltrationNode(n, name, result)
	detectObfuscationNode(n, name, result)

	for _, arg := range n.Args {
		checkPathTraversal(arg, n, result)
	}
}

// DetectCommandInjection analyzes a command string for potential injection attacks.
func DetectCommandInjection(command string) []SecurityIssue {
	root := Parse(command)
	if root == nil {
		return nil
	}
	result := &AnalysisResult{}
	Walk(root, func(n *Node, _ int) bool {
		if n.Type == NodeCommand {
			name := n.Name
			if name == "" && len(n.Args) > 0 {
				name = n.Args[0]
			}
			detectCommandInjectionNode(n, name, result)
		}
		return true
	})
	return result.Issues
}

func detectCommandInjectionNode(n *Node, name string, result *AnalysisResult) {
	// Eval-like builtins that evaluate arguments as code
	evalLike := map[string]bool{
		"eval": true, "source": true, ".": true, "exec": true,
		"command": true, "builtin": true, "fc": true, "coproc": true,
		"noglob": true, "nocorrect": true, "trap": true, "enable": true,
		"mapfile": true, "readarray": true, "hash": true, "bind": true,
		"complete": true, "compgen": true, "alias": true, "let": true,
	}
	if evalLike[name] {
		result.Issues = append(result.Issues, SecurityIssue{
			Type:    "injection",
			Message: name + " evaluates arguments as code",
			Command: n.Text,
			Node:    n,
		})
	}

	// Zsh dangerous builtins
	zshDangerous := map[string]bool{
		"zmodload": true, "emulate": true, "sysopen": true, "sysread": true,
		"syswrite": true, "sysseek": true, "zpty": true, "ztcp": true,
		"zsocket": true, "zf_rm": true, "zf_mv": true, "zf_ln": true,
		"zf_chmod": true, "zf_chown": true, "zf_mkdir": true, "zf_rmdir": true,
		"zf_chgrp": true,
	}
	if zshDangerous[name] {
		result.Issues = append(result.Issues, SecurityIssue{
			Type:    "injection",
			Message: name + " is a dangerous zsh builtin",
			Command: n.Text,
			Node:    n,
		})
	}

	// Check for variable expansion in arguments that could inject commands
	for _, arg := range n.Args {
		if strings.Contains(arg, "$(") || strings.Contains(arg, "`") {
			result.Issues = append(result.Issues, SecurityIssue{
				Type:    "injection",
				Message: "command substitution in argument: " + arg,
				Command: n.Text,
				Node:    n,
			})
		}
	}
}

// DetectPathTraversal scans for ../ patterns that could escape sandboxes.
func DetectPathTraversal(command string) []SecurityIssue {
	root := Parse(command)
	if root == nil {
		return checkPathTraversalString(command)
	}
	result := &AnalysisResult{}
	Walk(root, func(n *Node, _ int) bool {
		for _, arg := range n.Args {
			checkPathTraversal(arg, n, result)
		}
		if n.Target != "" {
			checkPathTraversal(n.Target, n, result)
		}
		return true
	})
	return result.Issues
}

var pathTraversalPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\.\./`),                            // ../
	regexp.MustCompile(`/\.\.`),                            // /..
	regexp.MustCompile(`(?i)/etc/(?:passwd|shadow|hosts)`), // sensitive files
	regexp.MustCompile(`/proc/.*/(environ|cmdline|maps)`),  // proc filesystem
	regexp.MustCompile(`/dev/(?:tcp|udp)/`),                // network devices
}

func checkPathTraversal(path string, n *Node, result *AnalysisResult) {
	for _, pat := range pathTraversalPatterns {
		if pat.MatchString(path) {
			result.Issues = append(result.Issues, SecurityIssue{
				Type:    "traversal",
				Message: "path traversal or sensitive path detected: " + path,
				Command: n.Text,
				Node:    n,
			})
			return
		}
	}
}

func checkPathTraversalString(command string) []SecurityIssue {
	var issues []SecurityIssue
	for _, pat := range pathTraversalPatterns {
		if pat.MatchString(command) {
			issues = append(issues, SecurityIssue{
				Type:    "traversal",
				Message: "path traversal detected in: " + command,
				Command: command,
			})
		}
	}
	return issues
}

// DetectPrivilegeEscalation checks for sudo, su, chmod, and similar commands.
func DetectPrivilegeEscalation(command string) []SecurityIssue {
	root := Parse(command)
	if root == nil {
		return nil
	}
	result := &AnalysisResult{}
	Walk(root, func(n *Node, _ int) bool {
		if n.Type == NodeCommand {
			name := n.Name
			if name == "" && len(n.Args) > 0 {
				name = n.Args[0]
			}
			detectPrivilegeEscalationNode(n, name, result)
		}
		return true
	})
	return result.Issues
}

func detectPrivilegeEscalationNode(n *Node, name string, result *AnalysisResult) {
	privCommands := map[string]string{
		"sudo":    "sudo executes commands with elevated privileges",
		"su":      "su switches to another user",
		"chmod":   "chmod changes file permissions",
		"chown":   "chown changes file ownership",
		"chgrp":   "chgrp changes file group",
		"setuid":  "setuid modifies set-user-ID bit",
		"pkexec":  "pkexec executes with polkit authorization",
		"doas":    "doas executes as another user",
		"gksudo":  "gksudo provides graphical sudo",
		"kdesudo": "kdesudo provides KDE sudo",
		"runas":   "runas executes as another user",
	}
	if msg, ok := privCommands[name]; ok {
		result.Issues = append(result.Issues, SecurityIssue{
			Type:    "privilege",
			Message: msg,
			Command: n.Text,
			Node:    n,
		})
	}

	// Check for chmod with dangerous permissions
	if name == "chmod" {
		for _, arg := range n.Args[1:] {
			if strings.Contains(arg, "+s") || strings.Contains(arg, "4755") ||
				strings.Contains(arg, "2755") || strings.Contains(arg, "6755") {
				result.Issues = append(result.Issues, SecurityIssue{
					Type:    "privilege",
					Message: "chmod with setuid/setgid bits",
					Command: n.Text,
					Node:    n,
				})
			}
		}
	}
}

// DetectDataExfiltration checks for curl/wget with data upload patterns.
func DetectDataExfiltration(command string) []SecurityIssue {
	root := Parse(command)
	if root == nil {
		return nil
	}
	result := &AnalysisResult{}
	Walk(root, func(n *Node, _ int) bool {
		if n.Type == NodeCommand {
			name := n.Name
			if name == "" && len(n.Args) > 0 {
				name = n.Args[0]
			}
			detectDataExfiltrationNode(n, name, result)
		}
		return true
	})
	return result.Issues
}

func detectDataExfiltrationNode(n *Node, name string, result *AnalysisResult) {
	exfilCommands := map[string]bool{
		"curl": true, "wget": true, "nc": true, "ncat": true,
		"socat": true, "telnet": true, "ftp": true, "scp": true,
		"rsync": true, "sftp": true,
	}

	if !exfilCommands[name] {
		return
	}

	// Check for data upload flags
	dataFlags := map[string][]string{
		"curl": {"-d", "--data", "--data-binary", "--data-raw", "--data-urlencode",
			"-F", "--form", "-T", "--upload-file", "-X POST", "-X PUT"},
		"wget":  {"--post-data", "--post-file", "--body-data", "--body-file"},
		"nc":    {}, // nc always can send data
		"rsync": {}, // rsync can push
		"scp":   {}, // scp can push
	}

	flags, hasFlagList := dataFlags[name]
	if !hasFlagList || len(flags) == 0 {
		// Commands that inherently transfer data
		result.Issues = append(result.Issues, SecurityIssue{
			Type:    "exfiltration",
			Message: name + " can transfer data to external hosts",
			Command: n.Text,
			Node:    n,
		})
		return
	}

	for _, arg := range n.Args {
		for _, flag := range flags {
			if arg == flag || strings.HasPrefix(arg, flag+"=") {
				result.Issues = append(result.Issues, SecurityIssue{
					Type:    "exfiltration",
					Message: name + " with data upload flag: " + flag,
					Command: n.Text,
					Node:    n,
				})
				return
			}
		}
	}
}

// DetectObfuscation checks for encoding/decoding commands that could hide payloads.
func DetectObfuscation(command string) []SecurityIssue {
	root := Parse(command)
	if root == nil {
		return nil
	}
	result := &AnalysisResult{}
	Walk(root, func(n *Node, _ int) bool {
		if n.Type == NodeCommand {
			name := n.Name
			if name == "" && len(n.Args) > 0 {
				name = n.Args[0]
			}
			detectObfuscationNode(n, name, result)
		}
		return true
	})
	return result.Issues
}

func detectObfuscationNode(n *Node, name string, result *AnalysisResult) {
	obfuscationCommands := map[string]string{
		"base64":  "base64 encoding can hide payloads",
		"base32":  "base32 encoding can hide payloads",
		"xxd":     "hex encoding can hide payloads",
		"od":      "octal dump can encode/decode payloads",
		"hexdump": "hex dump can encode payloads",
		"openssl": "openssl can encode/decode arbitrary data",
		"python":  "python can execute arbitrary code",
		"python3": "python3 can execute arbitrary code",
		"perl":    "perl can execute arbitrary code",
		"ruby":    "ruby can execute arbitrary code",
		"node":    "node can execute arbitrary code",
		"php":     "php can execute arbitrary code",
	}

	if msg, ok := obfuscationCommands[name]; ok {
		// For scripting languages, only flag when using -c/-e (inline code)
		if name == "python" || name == "python3" || name == "perl" ||
			name == "ruby" || name == "node" || name == "php" {
			for _, arg := range n.Args {
				if arg == "-c" || arg == "-e" || arg == "--eval" {
					result.Issues = append(result.Issues, SecurityIssue{
						Type:    "obfuscation",
						Message: msg + " (inline execution)",
						Command: n.Text,
						Node:    n,
					})
					return
				}
			}
			return // Script languages without inline exec are fine
		}
		result.Issues = append(result.Issues, SecurityIssue{
			Type:    "obfuscation",
			Message: msg,
			Command: n.Text,
			Node:    n,
		})
	}
}

// ExtractAllPaths collects all file paths from the AST.
func ExtractAllPaths(root *Node) []string {
	if root == nil {
		return nil
	}
	var paths []string
	Walk(root, func(n *Node, _ int) bool {
		for _, arg := range n.Args {
			if looksLikePath(arg) {
				paths = append(paths, arg)
			}
		}
		if n.Target != "" && looksLikePath(n.Target) {
			paths = append(paths, n.Target)
		}
		return true
	})
	return paths
}

func looksLikePath(s string) bool {
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") ||
		strings.HasPrefix(s, "../") || strings.HasPrefix(s, "~/") ||
		strings.HasPrefix(s, "~") {
		return true
	}
	if strings.Contains(s, "/") && !strings.HasPrefix(s, "-") &&
		!strings.Contains(s, "://") {
		return true
	}
	return false
}

// ExtractAllCommandNames returns all command names from the AST.
func ExtractAllCommandNames(root *Node) []string {
	if root == nil {
		return nil
	}
	var names []string
	WalkCommands(root, func(cmd *Node) {
		name := cmd.Name
		if name == "" && len(cmd.Args) > 0 {
			name = cmd.Args[0]
		}
		if name != "" {
			names = append(names, name)
		}
	})
	return names
}

// IsReadOnlyAST determines if the AST represents read-only operations.
func IsReadOnlyAST(root *Node) bool {
	if root == nil {
		return true
	}
	result := AnalyzeCommand(root)
	return result.IsReadOnly
}

func isReadOnlyCommandName(name string) bool {
	readOnlyCommands := map[string]bool{
		"ls": true, "pwd": true, "whoami": true, "hostname": true,
		"date": true, "cat": true, "head": true, "tail": true,
		"wc": true, "file": true, "stat": true, "type": true,
		"which": true, "echo": true, "printf": true, "uname": true,
		"env": true, "printenv": true, "history": true, "alias": true,
		"tree": true, "du": true, "df": true, "free": true,
		"uptime": true, "id": true, "groups": true, "find": true,
		"grep": true, "rg": true, "fd": true, "locate": true,
		"man": true, "help": true, "info": true, "base64": true,
		"sha256sum": true, "sha1sum": true, "md5sum": true,
		"sort": true, "uniq": true, "cut": true, "tr": true,
		"awk": true, "ps": true, "top": true, "pgrep": true,
		"lsof": true, "ss": true, "netstat": true, "diff": true,
		"test": true, "[": true, "[[": true, "true": true, "false": true,
		"read": true, "basename": true, "dirname": true, "realpath": true,
		"readlink": true, "less": true, "more": true, "strings": true,
	}
	return readOnlyCommands[name]
}

// QuoteContext describes how quoted content is distributed in a command.
type QuoteContext struct {
	// WithDoubleQuotes has single-quoted content removed but double-quoted preserved.
	WithDoubleQuotes string
	// FullyUnquoted has all quoted content removed.
	FullyUnquoted string
	// UnquotedKeepQuoteChars has content removed but quote characters kept.
	UnquotedKeepQuoteChars string
}

// ExtractQuoteContext strips quoted content from a command for pattern matching.
// This prevents patterns inside strings from triggering false positives.
func ExtractQuoteContext(command string) QuoteContext {
	var withDouble strings.Builder
	var fullyUnquoted strings.Builder
	var keepChars strings.Builder

	inSingle := false
	inDouble := false
	i := 0
	for i < len(command) {
		c := command[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
				keepChars.WriteByte('\'')
			}
			// Content inside single quotes: skip for fullyUnquoted, skip for withDouble
			i++
		case inDouble:
			if c == '\\' && i+1 < len(command) {
				next := command[i+1]
				if next == '"' || next == '\\' {
					withDouble.WriteByte(next)
					i += 2
					continue
				}
			}
			if c == '"' {
				inDouble = false
				keepChars.WriteByte('"')
				i++
				continue
			}
			withDouble.WriteByte(c)
			i++
		default:
			if c == '\\' && i+1 < len(command) {
				i += 2
				continue
			}
			if c == '\'' {
				inSingle = true
				keepChars.WriteByte('\'')
				i++
				continue
			}
			if c == '"' {
				inDouble = true
				keepChars.WriteByte('"')
				i++
				continue
			}
			withDouble.WriteByte(c)
			fullyUnquoted.WriteByte(c)
			keepChars.WriteByte(c)
			i++
		}
	}

	return QuoteContext{
		WithDoubleQuotes:       withDouble.String(),
		FullyUnquoted:          fullyUnquoted.String(),
		UnquotedKeepQuoteChars: keepChars.String(),
	}
}

// CompoundStructure describes the compound structure of a command.
type CompoundStructure struct {
	HasCompoundOperators bool
	HasPipeline          bool
	HasSubshell          bool
	HasCommandGroup      bool
	Operators            []string
	Segments             []string
}

// ExtractCompoundStructure analyzes the compound structure of a command AST.
func ExtractCompoundStructure(root *Node, command string) CompoundStructure {
	cs := CompoundStructure{}
	if root == nil {
		cs.Segments = []string{command}
		return cs
	}

	Walk(root, func(n *Node, _ int) bool {
		switch n.Type {
		case NodeAndOr:
			cs.HasCompoundOperators = true
			cs.Operators = append(cs.Operators, n.Operator.String())
		case NodePipeline:
			cs.HasPipeline = true
		case NodeSubshell:
			cs.HasSubshell = true
		case NodeGroup:
			cs.HasCommandGroup = true
		case NodeCommand:
			if n.Text != "" {
				cs.Segments = append(cs.Segments, strings.TrimSpace(n.Text))
			}
		}
		return true
	})

	if len(cs.Segments) == 0 {
		cs.Segments = []string{command}
	}
	return cs
}

// DangerousPatterns describes dangerous shell constructs found in a command.
type DangerousPatterns struct {
	HasCommandSubstitution bool
	HasProcessSubstitution bool
	HasParameterExpansion  bool
	HasHeredoc             bool
	HasComment             bool
}

// ExtractDangerousPatterns scans the AST for dangerous constructs.
func ExtractDangerousPatterns(root *Node) DangerousPatterns {
	dp := DangerousPatterns{}
	if root == nil {
		return dp
	}

	Walk(root, func(n *Node, _ int) bool {
		switch n.Type {
		case NodeCommandSubstitution:
			dp.HasCommandSubstitution = true
		case NodeProcessSubstitution:
			dp.HasProcessSubstitution = true
		case NodeHeredoc:
			dp.HasHeredoc = true
		case NodeComment:
			dp.HasComment = true
		}
		// Check text for parameter expansion
		if strings.Contains(n.Text, "${") {
			dp.HasParameterExpansion = true
		}
		return true
	})

	return dp
}
