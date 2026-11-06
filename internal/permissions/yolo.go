package permissions

import (
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DangerLevel classifies the risk level of a tool call in YOLO/auto-approve mode.
type DangerLevel int

const (
	// DangerSafe means the operation is safe and can be auto-approved.
	DangerSafe DangerLevel = iota
	// DangerCaution means the operation needs some scrutiny but may be auto-approved.
	DangerCaution
	// DangerDangerous means the operation is risky and should prompt the user.
	DangerDangerous
	// DangerForbidden means the operation must never be auto-approved.
	DangerForbidden
)

// String returns the human-readable name.
func (d DangerLevel) String() string {
	switch d {
	case DangerSafe:
		return "safe"
	case DangerCaution:
		return "caution"
	case DangerDangerous:
		return "dangerous"
	case DangerForbidden:
		return "forbidden"
	default:
		return "unknown"
	}
}

// YoloClassification is the result of classifying a tool call for auto-approval.
type YoloClassification struct {
	Level        DangerLevel
	Reason       string
	ShouldBlock  bool
	ToolName     string
	ToolInput    map[string]interface{}
	CommandClass *CommandClassification // only for bash commands
}

// YoloClassifier determines if tool calls can be auto-approved.
type YoloClassifier struct {
	mu                sync.RWMutex
	logger            *slog.Logger
	projectDir        string
	allowedDirs       map[string]bool
	cumulativeWrites  int
	cumulativeDeletes int
	sessionStart      time.Time

	// Per-tool classification config
	safeTools      map[string]bool
	alwaysAskTools map[string]bool
	forbiddenTools map[string]bool

	// Risk accumulation limits
	maxWritesBeforeAsk  int
	maxDeletesBeforeAsk int
}

// NewYoloClassifier creates a new YOLO classifier with safety boundaries.
func NewYoloClassifier(logger *slog.Logger, projectDir string) *YoloClassifier {
	yc := &YoloClassifier{
		logger:              logger,
		projectDir:          projectDir,
		allowedDirs:         make(map[string]bool),
		sessionStart:        time.Now(),
		safeTools:           make(map[string]bool),
		alwaysAskTools:      make(map[string]bool),
		forbiddenTools:      make(map[string]bool),
		maxWritesBeforeAsk:  50,
		maxDeletesBeforeAsk: 10,
	}

	// Initialize safe tools (always auto-approve) — corresponds to SAFE_YOLO_ALLOWLISTED_TOOLS
	for _, t := range SafeAutoApproveTools {
		yc.safeTools[t] = true
	}

	// Tools that always need user approval
	for _, t := range AlwaysAskToolsList {
		yc.alwaysAskTools[t] = true
	}

	// Tools that must never be auto-approved
	for _, t := range ForbiddenAutoApproveTools {
		yc.forbiddenTools[t] = true
	}

	// Project directory is always allowed
	if projectDir != "" {
		absDir, err := filepath.Abs(projectDir)
		if err == nil {
			yc.allowedDirs[absDir] = true
		}
	}

	return yc
}

// SafeAutoApproveTools is the list of tools that are always safe to auto-approve.
// Corresponds to SAFE_YOLO_ALLOWLISTED_TOOLS in classifierDecision.ts
var SafeAutoApproveTools = []string{
	"Read",             // Read-only file operations
	"Grep",             // Search (read-only)
	"Glob",             // Search (read-only)
	"LSP",              // Language server protocol (read-only)
	"ToolSearch",       // Tool search (read-only)
	"ListMcpResources", // MCP read
	"ReadMcpResource",  // MCP read
	"TodoWrite",        // Task management (metadata)
	"TaskCreate",       // Task management
	"TaskGet",          // Task management
	"TaskUpdate",       // Task management
	"TaskList",         // Task management
	"TaskStop",         // Task management
	"TaskOutput",       // Task management
	"AskUserQuestion",  // UI interaction
	"EnterPlanMode",    // Plan mode
	"ExitPlanMode",     // Plan mode
	"TeamCreate",       // Swarm coordination
	"TeamDelete",       // Agent cleanup
	"SendMessage",      // Messaging
	"Sleep",            // Misc safe
}

// AlwaysAskToolsList is the list of tools that always need user confirmation.
var AlwaysAskToolsList = []string{
	// No default always-ask tools — these are added by user configuration
}

// ForbiddenAutoApproveTools is the list of tools that must never be auto-approved.
var ForbiddenAutoApproveTools = []string{
	// No default forbidden tools — these are added by user configuration
}

// AddAllowedDir adds a directory to the set of allowed write directories.
func (yc *YoloClassifier) AddAllowedDir(dir string) {
	yc.mu.Lock()
	defer yc.mu.Unlock()
	absDir, err := filepath.Abs(dir)
	if err == nil {
		yc.allowedDirs[absDir] = true
	}
}

// SetMaxWrites sets the cumulative write limit before auto-approve stops.
func (yc *YoloClassifier) SetMaxWrites(n int) {
	yc.mu.Lock()
	defer yc.mu.Unlock()
	yc.maxWritesBeforeAsk = n
}

// SetMaxDeletes sets the cumulative delete limit before auto-approve stops.
func (yc *YoloClassifier) SetMaxDeletes(n int) {
	yc.mu.Lock()
	defer yc.mu.Unlock()
	yc.maxDeletesBeforeAsk = n
}

// ClassifyForAutoApprove determines if a tool call can be auto-approved.
// This is the main entry point for YOLO mode classification.
func (yc *YoloClassifier) ClassifyForAutoApprove(toolName string, toolInput map[string]interface{}) YoloClassification {
	yc.mu.Lock()
	defer yc.mu.Unlock()

	result := YoloClassification{
		ToolName:  toolName,
		ToolInput: toolInput,
	}

	// 1. Forbidden tools: never auto-approve
	if yc.forbiddenTools[toolName] {
		result.Level = DangerForbidden
		result.ShouldBlock = true
		result.Reason = "tool " + toolName + " is in the forbidden list"
		return result
	}

	// 2. Always-ask tools
	if yc.alwaysAskTools[toolName] {
		result.Level = DangerDangerous
		result.ShouldBlock = true
		result.Reason = "tool " + toolName + " always requires approval"
		return result
	}

	// 3. Safe tools: always auto-approve
	if yc.safeTools[toolName] {
		result.Level = DangerSafe
		result.ShouldBlock = false
		result.Reason = "tool " + toolName + " is on the safe allowlist"
		return result
	}

	// 4. Tool-specific classification
	switch toolName {
	case "Bash", "bash":
		return yc.classifyBashForAutoApprove(toolInput)
	case "Write", "Edit", "MultiEdit", "FileEdit":
		return yc.classifyFileEditForAutoApprove(toolName, toolInput)
	case "Agent", "SubAgent":
		return yc.classifyAgentForAutoApprove(toolName, toolInput)
	default:
		// Unknown tools default to caution
		result.Level = DangerCaution
		result.ShouldBlock = true
		result.Reason = "unknown tool " + toolName + " requires classification"
		return result
	}
}

// classifyBashForAutoApprove handles bash commands in YOLO mode.
func (yc *YoloClassifier) classifyBashForAutoApprove(input map[string]interface{}) YoloClassification {
	result := YoloClassification{
		ToolName:  "Bash",
		ToolInput: input,
	}

	command, _ := input["command"].(string)
	if command == "" {
		result.Level = DangerSafe
		result.ShouldBlock = false
		result.Reason = "empty command"
		return result
	}

	// Classify the command
	cls := ClassifyBashCommand(command)
	result.CommandClass = &cls

	// Check for dangerous patterns first
	dangerMatches := DetectDangerousPatterns(command)
	maxSeverity := MaxDangerSeverity(dangerMatches)

	if maxSeverity >= SeverityCritical {
		result.Level = DangerForbidden
		result.ShouldBlock = true
		result.Reason = "command matches critical danger pattern: " + dangerMatches[0].Description
		return result
	}

	if maxSeverity >= SeverityHigh {
		result.Level = DangerDangerous
		result.ShouldBlock = true
		result.Reason = "command matches high danger pattern: " + dangerMatches[0].Description
		return result
	}

	// Check system path writes
	if isSystemPathWrite(command) {
		result.Level = DangerDangerous
		result.ShouldBlock = true
		result.Reason = "command writes to system paths"
		return result
	}

	// Read-only commands are safe
	if cls.ReadOnly {
		result.Level = DangerSafe
		result.ShouldBlock = false
		result.Reason = cls.Reason
		return result
	}

	// Destructive commands are dangerous
	if cls.Destructive {
		result.Level = DangerDangerous
		result.ShouldBlock = true
		result.Reason = "destructive command: " + cls.Reason
		return result
	}

	// Interpreters are dangerous (arbitrary code execution)
	if cls.Category == CategoryInterpreter {
		result.Level = DangerDangerous
		result.ShouldBlock = true
		result.Reason = cls.Reason
		return result
	}

	// Shell exec (sudo, etc.) is dangerous
	if cls.Category == CategoryShellExec {
		result.Level = DangerDangerous
		result.ShouldBlock = true
		result.Reason = cls.Reason
		return result
	}

	// Package manager publish is dangerous
	if cls.Category == CategoryPackageManagerPublish {
		result.Level = DangerDangerous
		result.ShouldBlock = true
		result.Reason = cls.Reason
		return result
	}

	// Network writes need approval
	if cls.Category == CategoryNetworkWrite {
		result.Level = DangerDangerous
		result.ShouldBlock = true
		result.Reason = cls.Reason
		return result
	}

	// Git push/destructive need approval
	if cls.Category == CategoryGitDestructive {
		result.Level = DangerDangerous
		result.ShouldBlock = true
		result.Reason = cls.Reason
		return result
	}

	// Check cumulative risk
	if cls.Category == CategoryFileWrite || cls.Category == CategoryRedirection {
		yc.cumulativeWrites++
		if yc.cumulativeWrites > yc.maxWritesBeforeAsk {
			result.Level = DangerCaution
			result.ShouldBlock = true
			result.Reason = "too many write operations in this session (cumulative risk)"
			return result
		}
	}

	if cls.Category == CategoryFileDelete {
		yc.cumulativeDeletes++
		if yc.cumulativeDeletes > yc.maxDeletesBeforeAsk {
			result.Level = DangerCaution
			result.ShouldBlock = true
			result.Reason = "too many delete operations in this session (cumulative risk)"
			return result
		}
	}

	// Medium-severity patterns still block
	if maxSeverity >= SeverityMedium {
		result.Level = DangerCaution
		result.ShouldBlock = true
		result.Reason = "command matches medium danger pattern: " + dangerMatches[0].Description
		return result
	}

	// File writes within project are allowed in YOLO mode
	if cls.Category == CategoryFileWrite || cls.Category == CategoryFileMove ||
		cls.Category == CategoryPackageManagerInstall || cls.Category == CategoryGit {
		// Check if command targets are within the project
		if yc.isCommandWithinProject(command) {
			result.Level = DangerSafe
			result.ShouldBlock = false
			result.Reason = "write operation within project directory"
			return result
		}
	}

	// Network reads are safe
	if cls.Category == CategoryNetworkRead {
		result.Level = DangerSafe
		result.ShouldBlock = false
		result.Reason = cls.Reason
		return result
	}

	// Default: caution
	result.Level = DangerCaution
	result.ShouldBlock = false
	result.Reason = "command classified as: " + cls.Category.String()
	return result
}

// classifyFileEditForAutoApprove handles file edit/write tool calls in YOLO mode.
func (yc *YoloClassifier) classifyFileEditForAutoApprove(toolName string, input map[string]interface{}) YoloClassification {
	result := YoloClassification{
		ToolName:  toolName,
		ToolInput: input,
	}

	filePath, _ := input["file_path"].(string)
	if filePath == "" {
		filePath, _ = input["path"].(string)
	}

	if filePath == "" {
		result.Level = DangerCaution
		result.ShouldBlock = true
		result.Reason = "file path not provided"
		return result
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		result.Level = DangerCaution
		result.ShouldBlock = true
		result.Reason = "cannot resolve file path"
		return result
	}

	// Check if the file is within an allowed directory
	for allowedDir := range yc.allowedDirs {
		if isPathWithin(absPath, allowedDir) {
			// Check for dangerous files even within the project
			if isDangerousFile(absPath) {
				result.Level = DangerDangerous
				result.ShouldBlock = true
				result.Reason = "editing a dangerous/sensitive file: " + filepath.Base(absPath)
				return result
			}

			result.Level = DangerSafe
			result.ShouldBlock = false
			result.Reason = "file edit within allowed project directory"

			// Track cumulative writes
			yc.cumulativeWrites++
			if yc.cumulativeWrites > yc.maxWritesBeforeAsk {
				result.Level = DangerCaution
				result.ShouldBlock = true
				result.Reason = "too many write operations in this session"
			}

			return result
		}
	}

	// Outside project directory
	result.Level = DangerDangerous
	result.ShouldBlock = true
	result.Reason = "file edit outside of allowed directories: " + filePath
	return result
}

// classifyAgentForAutoApprove handles agent tool calls.
func (yc *YoloClassifier) classifyAgentForAutoApprove(toolName string, input map[string]interface{}) YoloClassification {
	// Agents get their own permission checks for each tool they use,
	// so we allow the agent spawn itself, but each agent action will be checked independently.
	return YoloClassification{
		ToolName:    toolName,
		ToolInput:   input,
		Level:       DangerCaution,
		ShouldBlock: true,
		Reason:      "agent tool calls require classifier evaluation",
	}
}

// ResetCumulativeRisk resets the cumulative risk counters (e.g., on new conversation turn).
func (yc *YoloClassifier) ResetCumulativeRisk() {
	yc.mu.Lock()
	defer yc.mu.Unlock()
	yc.cumulativeWrites = 0
	yc.cumulativeDeletes = 0
}

// CumulativeWrites returns the current cumulative write count.
func (yc *YoloClassifier) CumulativeWrites() int {
	yc.mu.RLock()
	defer yc.mu.RUnlock()
	return yc.cumulativeWrites
}

// CumulativeDeletes returns the current cumulative delete count.
func (yc *YoloClassifier) CumulativeDeletes() int {
	yc.mu.RLock()
	defer yc.mu.RUnlock()
	return yc.cumulativeDeletes
}

// IsAutoModeAllowlistedTool checks if a tool is on the safe allowlist.
func IsAutoModeAllowlistedTool(toolName string) bool {
	for _, t := range SafeAutoApproveTools {
		if t == toolName {
			return true
		}
	}
	return false
}

// --- Helpers ---

// isSystemPathWrite checks if a command writes to system directories.
func isSystemPathWrite(command string) bool {
	systemPaths := []string{
		"/etc/", "/usr/", "/bin/", "/sbin/", "/lib/", "/lib64/",
		"/boot/", "/proc/", "/sys/", "/dev/",
		"/var/log/", "/var/run/",
		"/System/", "/Library/",
	}

	tc := TokenizeCommand(command)

	// Only check write commands
	if !WriteCommands[tc.Executable] && !tc.RedirectOut {
		return false
	}

	for _, arg := range tc.Args {
		for _, sp := range systemPaths {
			if strings.HasPrefix(arg, sp) {
				return true
			}
		}
	}

	// Check redirect targets
	if tc.RedirectOut {
		// Simple check: look for > followed by a system path
		parts := strings.SplitAfter(command, ">")
		if len(parts) > 1 {
			target := strings.TrimSpace(parts[len(parts)-1])
			for _, sp := range systemPaths {
				if strings.HasPrefix(target, sp) {
					return true
				}
			}
		}
	}

	return false
}

// isPathWithin checks if a path is within a base directory.
func isPathWithin(path, baseDir string) bool {
	rel, err := filepath.Rel(baseDir, path)
	if err != nil {
		return false
	}
	// Must not start with ".." to be within
	return !strings.HasPrefix(rel, "..")
}

// DangerousFiles lists file names/patterns that are dangerous to edit.
// Corresponds to DANGEROUS_FILES in filesystem.ts
var DangerousFiles = []string{
	".gitconfig", ".gitmodules",
	".bashrc", ".bash_profile", ".zshrc", ".zprofile", ".profile",
	".ripgreprc", ".mcp.json", ".claude.json",
}

// DangerousDirectories lists directory names that are sensitive.
// Corresponds to DANGEROUS_DIRECTORIES in filesystem.ts
var DangerousDirectories = []string{
	".git", ".vscode", ".idea", ".claude",
}

// isDangerousFile checks if a file path is a known dangerous file.
func isDangerousFile(absPath string) bool {
	baseName := strings.ToLower(filepath.Base(absPath))
	for _, df := range DangerousFiles {
		if strings.ToLower(df) == baseName {
			return true
		}
	}

	// Check if path is within a dangerous directory
	pathLower := strings.ToLower(absPath)
	for _, dd := range DangerousDirectories {
		ddLower := strings.ToLower(dd)
		if strings.Contains(pathLower, string(filepath.Separator)+ddLower+string(filepath.Separator)) {
			return true
		}
	}

	return false
}

// isCommandWithinProject checks if a bash command's file arguments are within the project dir.
func (yc *YoloClassifier) isCommandWithinProject(command string) bool {
	tc := TokenizeCommand(command)

	// If no args with path-like content, assume within project
	hasPathArgs := false
	for _, arg := range tc.Args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, "~") {
			hasPathArgs = true
			absPath, err := filepath.Abs(arg)
			if err != nil {
				return false
			}
			inProject := false
			for allowedDir := range yc.allowedDirs {
				if isPathWithin(absPath, allowedDir) {
					inProject = true
					break
				}
			}
			if !inProject {
				return false
			}
		}
	}

	// If no absolute paths found, assume relative to project directory (safe)
	if !hasPathArgs {
		return true
	}

	return true
}
