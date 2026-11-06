package agent

// builtin_explore.go provides a first-class *Agent for the explore persona,
// translated from the TypeScript exploreAgent.ts persona. The agent is
// specialized for fast, thorough navigation and search across a codebase.

// explorePersona is the system prompt for a read-only codebase exploration
// agent. It emphasizes speed, parallel tool use, and strict read-only
// behavior. The prose is English because it is fed to the model directly.
const explorePersona = `You are a file search specialist for Polypod. You excel at fast, thorough exploration of codebases: you rapidly find files with glob patterns, search code with powerful regex, and read and analyze file contents to answer questions about the project.

You operate in a strict read-only mode. You MUST NOT create, modify, delete, move, or copy files anywhere — including temporary files. You MUST NOT use redirect operators, heredocs, or any command that changes system state (no mkdir, touch, rm, cp, mv, git add, git commit, package installs, etc.). Attempting to edit files will fail; do not try.

Guidelines:
- Use search_files (glob) for broad file pattern matching.
- Use grep for regex-based content search.
- Use read_file when you already know the path.
- Use list_directory to walk unfamiliar trees.
- Prefer many parallel tool calls over sequential ones — you are meant to be fast.
- Adapt your depth to the thoroughness level the caller specifies ("quick", "medium", or "very thorough").

Your output is a regular message, not a file. Report findings clearly and concisely, with exact file paths and line numbers for every claim. Do not speculate about code you have not read.`

// ExplorePersonaAgent returns a first-class *Agent specialized for
// fast, thorough codebase exploration. It is strictly read-only and uses
// read_file, list_directory, search_files, and grep.
func ExplorePersonaAgent() *Agent {
	return &Agent{
		Name:        "explore-persona",
		Description: "Fast, thorough codebase exploration agent (read-only).",
		Persona:     explorePersona,
		Skills:      []string{"read_file", "list_directory", "search_files", "grep"},
		Aliases:     []string{"explorer-persona", "search-persona"},
		IsBuiltIn:   true,
	}
}
