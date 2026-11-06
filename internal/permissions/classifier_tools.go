package permissions

import (
	"path/filepath"
	"strings"
)

// ToolRiskLevel represents the risk level of a tool call.
type ToolRiskLevel int

const (
	// ToolRiskSafe — read-only operations, no side effects.
	ToolRiskSafe ToolRiskLevel = iota
	// ToolRiskLow — minor side effects, easily reversible.
	ToolRiskLow
	// ToolRiskMedium — significant side effects, generally reversible.
	ToolRiskMedium
	// ToolRiskHigh — potentially dangerous, may be hard to reverse.
	ToolRiskHigh
	// ToolRiskCritical — extremely dangerous, potentially irreversible.
	ToolRiskCritical
)

// String returns the human-readable name for a ToolRiskLevel.
func (r ToolRiskLevel) String() string {
	switch r {
	case ToolRiskSafe:
		return "safe"
	case ToolRiskLow:
		return "low"
	case ToolRiskMedium:
		return "medium"
	case ToolRiskHigh:
		return "high"
	case ToolRiskCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// ToolRisk is the full risk assessment of a tool call.
type ToolRisk struct {
	Level         ToolRiskLevel
	Category      string // e.g. "read-only", "file-write", "command-exec", "destructive"
	Reason        string
	IsWrite       bool
	IsDestructive bool
}

// ClassifyToolRisk classifies the risk level of a tool call based on
// the tool name and its arguments.
func ClassifyToolRisk(toolName string, args map[string]any) ToolRisk {
	switch toolName {
	// ===== Safe (read-only) tools =====
	case "Read", "read_file", "ReadFile":
		return ToolRisk{Level: ToolRiskSafe, Category: "read-only", Reason: "read-only file access"}

	case "Glob", "glob", "ListDirectory", "list_directory", "list_dir":
		return ToolRisk{Level: ToolRiskSafe, Category: "read-only", Reason: "directory listing"}

	case "Grep", "grep", "search_files", "SearchFiles", "RipGrep":
		return ToolRisk{Level: ToolRiskSafe, Category: "read-only", Reason: "content search"}

	case "LSP", "lsp":
		return ToolRisk{Level: ToolRiskSafe, Category: "read-only", Reason: "language server protocol query"}

	case "ToolSearch", "TodoRead", "TodoWrite":
		return ToolRisk{Level: ToolRiskSafe, Category: "read-only", Reason: "metadata operation"}

	case "AskUserQuestion":
		return ToolRisk{Level: ToolRiskSafe, Category: "read-only", Reason: "user interaction"}

	case "EnterPlanMode", "ExitPlanMode":
		return ToolRisk{Level: ToolRiskSafe, Category: "mode-change", Reason: "mode transition"}

	case "Sleep":
		return ToolRisk{Level: ToolRiskSafe, Category: "no-op", Reason: "delay operation"}

	// ===== Low risk (file edits within project) =====
	case "Edit", "FileEdit", "edit_file":
		return classifyFileEdit(args)

	case "MultiEdit":
		return classifyFileEdit(args) // Same logic, just multiple edits

	case "Write", "write_file", "create_file":
		return classifyFileWrite(args)

	// ===== Low risk (network read) =====
	case "WebFetch", "web_fetch", "Fetch":
		return ToolRisk{Level: ToolRiskLow, Category: "network-read", Reason: "fetching web content (read-only)"}

	// ===== Low-Medium risk (MCP operations) =====
	case "ListMcpResources", "ReadMcpResource":
		return ToolRisk{Level: ToolRiskSafe, Category: "read-only", Reason: "MCP read operation"}

	case "McpCall", "mcp_call":
		return ToolRisk{Level: ToolRiskMedium, Category: "mcp-call", Reason: "MCP server tool call"}

	// ===== Medium risk (agent operations) =====
	case "Agent", "SubAgent", "agent_run", "TaskCreate":
		return ToolRisk{
			Level:    ToolRiskMedium,
			Category: "agent-spawn",
			Reason:   "spawning sub-agent — each agent action will be checked independently",
			IsWrite:  true,
		}

	case "TaskStop", "TaskGet", "TaskList", "TaskOutput", "TaskUpdate":
		return ToolRisk{Level: ToolRiskSafe, Category: "agent-management", Reason: "agent task management"}

	case "TeamCreate", "TeamDelete", "SendMessage":
		return ToolRisk{Level: ToolRiskLow, Category: "agent-coordination", Reason: "agent coordination"}

	// ===== Variable risk (bash commands) =====
	case "Bash", "bash", "run_command", "RunCommand", "Shell":
		return classifyBashRisk(args)

	// ===== Medium-High risk (destructive file operations) =====
	case "delete_file", "DeleteFile":
		return ToolRisk{
			Level:         ToolRiskHigh,
			Category:      "file-delete",
			Reason:        "deleting a file",
			IsWrite:       true,
			IsDestructive: true,
		}

	// ===== Git operations =====
	case "git_status", "git_log", "git_diff", "git_blame", "git_show":
		return ToolRisk{Level: ToolRiskSafe, Category: "git-read", Reason: "read-only git operation"}

	case "git_add", "git_commit", "git_checkout", "git_branch", "git_merge":
		return ToolRisk{Level: ToolRiskMedium, Category: "git-write", Reason: "git repository modification", IsWrite: true}

	case "git_push":
		return ToolRisk{Level: ToolRiskHigh, Category: "git-push", Reason: "pushing to remote", IsWrite: true}

	case "git_reset", "git_clean":
		return ToolRisk{
			Level:         ToolRiskHigh,
			Category:      "git-destructive",
			Reason:        "potentially destructive git operation",
			IsWrite:       true,
			IsDestructive: true,
		}

	// ===== Sandbox =====
	case "sandbox_run", "sandbox_script":
		return ToolRisk{Level: ToolRiskMedium, Category: "sandbox-exec", Reason: "sandboxed execution", IsWrite: true}

	// ===== MCP server management =====
	case "mcp_connect", "mcp_disconnect":
		return ToolRisk{Level: ToolRiskMedium, Category: "mcp-management", Reason: "MCP server lifecycle", IsWrite: true}

	default:
		// MCP tools (mcp__server__tool pattern)
		if strings.HasPrefix(toolName, "mcp__") {
			return ToolRisk{Level: ToolRiskMedium, Category: "mcp-tool", Reason: "MCP server tool: " + toolName, IsWrite: true}
		}

		// Unknown tools default to medium risk
		return ToolRisk{Level: ToolRiskMedium, Category: "unknown", Reason: "unrecognized tool: " + toolName, IsWrite: true}
	}
}

// classifyFileEdit classifies the risk of a file edit operation.
func classifyFileEdit(args map[string]any) ToolRisk {
	filePath := extractFilePathFromArgs(args)
	if filePath == "" {
		return ToolRisk{Level: ToolRiskLow, Category: "file-edit", Reason: "file edit (path unknown)", IsWrite: true}
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return ToolRisk{Level: ToolRiskMedium, Category: "file-edit", Reason: "file edit (unresolvable path)", IsWrite: true}
	}

	// Dangerous files get higher risk
	if isDangerousFile(absPath) {
		return ToolRisk{
			Level:    ToolRiskHigh,
			Category: "file-edit-dangerous",
			Reason:   "editing sensitive file: " + filepath.Base(absPath),
			IsWrite:  true,
		}
	}

	// System paths get higher risk
	if isSystemPath(absPath) {
		return ToolRisk{
			Level:    ToolRiskHigh,
			Category: "file-edit-system",
			Reason:   "editing file in system directory",
			IsWrite:  true,
		}
	}

	return ToolRisk{Level: ToolRiskLow, Category: "file-edit", Reason: "file edit within project", IsWrite: true}
}

// classifyFileWrite classifies the risk of a file write/create operation.
func classifyFileWrite(args map[string]any) ToolRisk {
	filePath := extractFilePathFromArgs(args)
	if filePath == "" {
		return ToolRisk{Level: ToolRiskLow, Category: "file-write", Reason: "file write (path unknown)", IsWrite: true}
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return ToolRisk{Level: ToolRiskMedium, Category: "file-write", Reason: "file write (unresolvable path)", IsWrite: true}
	}

	// Dangerous files
	if isDangerousFile(absPath) {
		return ToolRisk{
			Level:    ToolRiskHigh,
			Category: "file-write-dangerous",
			Reason:   "writing to sensitive file: " + filepath.Base(absPath),
			IsWrite:  true,
		}
	}

	// System paths
	if isSystemPath(absPath) {
		return ToolRisk{
			Level:         ToolRiskCritical,
			Category:      "file-write-system",
			Reason:        "writing to system directory",
			IsWrite:       true,
			IsDestructive: true,
		}
	}

	return ToolRisk{Level: ToolRiskLow, Category: "file-write", Reason: "new file creation within project", IsWrite: true}
}

// classifyBashRisk classifies the risk of a bash command.
func classifyBashRisk(args map[string]any) ToolRisk {
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return ToolRisk{Level: ToolRiskSafe, Category: "command-empty", Reason: "empty command"}
	}

	// Use the classifier from classifier.go
	cls := ClassifyBashCommand(command)

	// Also check for dangerous patterns
	dangerMatches := DetectDangerousPatterns(command)
	maxSeverity := MaxDangerSeverity(dangerMatches)

	// Map severity to risk level
	if maxSeverity >= SeverityCritical {
		reason := "critical danger pattern"
		if len(dangerMatches) > 0 {
			reason = dangerMatches[0].Description
		}
		return ToolRisk{
			Level:         ToolRiskCritical,
			Category:      "command-critical",
			Reason:        reason,
			IsWrite:       true,
			IsDestructive: true,
		}
	}

	if maxSeverity >= SeverityHigh {
		reason := "high danger pattern"
		if len(dangerMatches) > 0 {
			reason = dangerMatches[0].Description
		}
		return ToolRisk{
			Level:         ToolRiskHigh,
			Category:      "command-dangerous",
			Reason:        reason,
			IsWrite:       !cls.ReadOnly,
			IsDestructive: cls.Destructive,
		}
	}

	// Read-only commands
	if cls.ReadOnly {
		return ToolRisk{Level: ToolRiskSafe, Category: "command-readonly", Reason: cls.Reason}
	}

	// Destructive commands
	if cls.Destructive {
		return ToolRisk{
			Level:         ToolRiskHigh,
			Category:      "command-destructive",
			Reason:        cls.Reason,
			IsWrite:       true,
			IsDestructive: true,
		}
	}

	// Category-based classification
	switch cls.Category {
	case CategoryReadOnly:
		return ToolRisk{Level: ToolRiskSafe, Category: "command-readonly", Reason: cls.Reason}

	case CategoryFileWrite, CategoryRedirection:
		return ToolRisk{Level: ToolRiskMedium, Category: "command-write", Reason: cls.Reason, IsWrite: true}

	case CategoryFileDelete:
		return ToolRisk{
			Level:         ToolRiskHigh,
			Category:      "command-delete",
			Reason:        cls.Reason,
			IsWrite:       true,
			IsDestructive: true,
		}

	case CategoryFileMove:
		return ToolRisk{Level: ToolRiskMedium, Category: "command-move", Reason: cls.Reason, IsWrite: true}

	case CategoryNetworkRead:
		return ToolRisk{Level: ToolRiskLow, Category: "command-network-read", Reason: cls.Reason}

	case CategoryNetworkWrite:
		return ToolRisk{Level: ToolRiskHigh, Category: "command-network-write", Reason: cls.Reason, IsWrite: true}

	case CategoryGit:
		return ToolRisk{Level: ToolRiskMedium, Category: "command-git", Reason: cls.Reason, IsWrite: true}

	case CategoryGitDestructive:
		return ToolRisk{
			Level:         ToolRiskHigh,
			Category:      "command-git-destructive",
			Reason:        cls.Reason,
			IsWrite:       true,
			IsDestructive: true,
		}

	case CategoryPackageManager, CategoryPackageManagerInstall:
		return ToolRisk{Level: ToolRiskMedium, Category: "command-package", Reason: cls.Reason, IsWrite: true}

	case CategoryPackageManagerPublish:
		return ToolRisk{
			Level:         ToolRiskCritical,
			Category:      "command-publish",
			Reason:        cls.Reason,
			IsWrite:       true,
			IsDestructive: true,
		}

	case CategoryInterpreter:
		return ToolRisk{Level: ToolRiskHigh, Category: "command-interpreter", Reason: cls.Reason, IsWrite: true}

	case CategoryShellExec:
		return ToolRisk{
			Level:         ToolRiskCritical,
			Category:      "command-shell-exec",
			Reason:        cls.Reason,
			IsWrite:       true,
			IsDestructive: cls.Destructive,
		}

	case CategoryProcessManagement:
		return ToolRisk{Level: ToolRiskMedium, Category: "command-process", Reason: cls.Reason, IsWrite: true}

	case CategorySystemConfig:
		return ToolRisk{Level: ToolRiskHigh, Category: "command-sysconfig", Reason: cls.Reason, IsWrite: true}

	case CategoryPipeChain:
		if maxSeverity >= SeverityMedium {
			return ToolRisk{Level: ToolRiskHigh, Category: "command-pipe-chain", Reason: cls.Reason, IsWrite: true}
		}
		return ToolRisk{Level: ToolRiskMedium, Category: "command-pipe-chain", Reason: cls.Reason, IsWrite: true}

	default:
		// Unknown commands get medium risk
		return ToolRisk{Level: ToolRiskMedium, Category: "command-unknown", Reason: cls.Reason, IsWrite: true}
	}
}

// --- Helpers ---

// extractFilePathFromArgs gets the file path from tool arguments.
func extractFilePathFromArgs(args map[string]any) string {
	if p, ok := args["file_path"].(string); ok && p != "" {
		return p
	}
	if p, ok := args["path"].(string); ok && p != "" {
		return p
	}
	if p, ok := args["filePath"].(string); ok && p != "" {
		return p
	}
	return ""
}

// isSystemPath checks if a path is in a system directory.
func isSystemPath(absPath string) bool {
	systemPrefixes := []string{
		"/etc/", "/usr/", "/bin/", "/sbin/", "/lib/", "/lib64/",
		"/boot/", "/proc/", "/sys/", "/dev/",
		"/var/log/", "/var/run/",
		"/System/", "/Library/",
	}
	for _, prefix := range systemPrefixes {
		if strings.HasPrefix(absPath, prefix) {
			return true
		}
	}
	return false
}
