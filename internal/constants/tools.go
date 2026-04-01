package constants

// Tool name constants — single source of truth for all tool identifiers.
const (
	// File operations
	ToolReadFile  = "read_file"
	ToolWriteFile = "write_file"
	ToolEditFile  = "edit_file"
	ToolGlobTool  = "glob"
	ToolGrepTool  = "grep"

	// Shell
	ToolRunCommand = "run_command"
	ToolBash       = "bash"
	ToolPowerShell = "powershell"

	// Search
	ToolSearchFiles = "search_files"

	// Web
	ToolWebSearch = "web_search"
	ToolWebFetch  = "web_fetch"

	// Memory & Knowledge
	ToolMemoryAdd    = "memory_add"
	ToolMemorySearch = "memory_search"

	// Session
	ToolListSessions  = "list_sessions"
	ToolSessionInfo   = "session_info"
	ToolExportSession = "export_session"

	// Agent
	ToolAgentRun    = "agent_run"
	ToolSendMessage = "send_message"

	// Task management
	ToolTaskCreate = "task_create"
	ToolTaskGet    = "task_get"
	ToolTaskList   = "task_list"
	ToolTaskStop   = "task_stop"
	ToolTaskOutput = "task_output"

	// MCP
	ToolMCPTool = "mcp_tool"

	// Planner
	ToolTodoWrite = "todo_write"
	ToolPlanMode  = "plan_mode"

	// Misc
	ToolSkillSearch = "skill_search"
	ToolConfig      = "config"
)

// Default limits for tool operations.
const (
	// MaxOutputBytes is the default max size for tool output before truncation.
	MaxOutputBytes = 10 * 1024 // 10KB

	// MaxFileReadBytes is the max size for file read operations.
	MaxFileReadBytes = 500 * 1024 // 500KB

	// ExecTimeoutSecs is the default timeout for shell commands.
	ExecTimeoutSecs = 30

	// MaxToolIterations is the max number of tool-calling rounds per query.
	MaxToolIterations = 20

	// MaxGlobResults is the max number of files returned by glob.
	MaxGlobResults = 100

	// MaxGrepResults is the max number of matches returned by grep.
	MaxGrepResults = 100

	// MaxConcurrentTools is the max number of tools running in parallel.
	MaxConcurrentTools = 5
)

// Tool permission groups — tools that share common permission characteristics.
var (
	// ReadOnlyTools are tools that only read data.
	ReadOnlyTools = []string{
		ToolReadFile, ToolGlobTool, ToolGrepTool, ToolSearchFiles,
		ToolWebSearch, ToolWebFetch, ToolMemorySearch,
		ToolListSessions, ToolSessionInfo,
	}

	// DestructiveTools perform irreversible operations.
	DestructiveTools = []string{
		ToolWriteFile, ToolEditFile, ToolRunCommand, ToolBash,
		ToolPowerShell, ToolSendMessage,
	}

	// ConcurrencySafeTools can run multiple instances simultaneously.
	ConcurrencySafeTools = []string{
		ToolReadFile, ToolGlobTool, ToolGrepTool, ToolSearchFiles,
		ToolWebSearch, ToolWebFetch, ToolMemorySearch,
	}
)

// IsReadOnlyTool checks if a tool name is in the read-only set.
func IsReadOnlyTool(name string) bool {
	for _, t := range ReadOnlyTools {
		if t == name {
			return true
		}
	}
	return false
}

// IsDestructiveTool checks if a tool name is in the destructive set.
func IsDestructiveTool(name string) bool {
	for _, t := range DestructiveTools {
		if t == name {
			return true
		}
	}
	return false
}

// IsConcurrencySafeTool checks if a tool name is in the concurrency-safe set.
func IsConcurrencySafeTool(name string) bool {
	for _, t := range ConcurrencySafeTools {
		if t == name {
			return true
		}
	}
	return false
}
