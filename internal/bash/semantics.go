package bash

import "strings"

// CommandSemantics maps command names to exit code interpretations.
// Migrated from Claude Code's commandSemantics.ts

// ExitCodeMeaning interprets exit codes based on command semantics.
type ExitCodeMeaning int

const (
	ExitSuccess        ExitCodeMeaning = iota // 0 = success
	ExitFailure                               // non-zero = failure
	ExitConditionFalse                        // condition false (not an error)
	ExitNoMatches                             // no matches found (not an error)
	ExitPartialSuccess                        // partial success
)

// CommandExitSemantics returns the meaning of an exit code for a command.
func CommandExitSemantics(command string, exitCode int) (ExitCodeMeaning, string) {
	if exitCode == 0 {
		return ExitSuccess, "sucesso"
	}

	base := extractBaseCommand(command)

	switch base {
	case "grep", "rg":
		if exitCode == 1 {
			return ExitNoMatches, "nenhuma correspondencia encontrada"
		}
		return ExitFailure, "erro na busca"

	case "find":
		if exitCode == 1 {
			return ExitPartialSuccess, "sucesso parcial (alguns diretorios inacessiveis)"
		}
		return ExitFailure, "erro no find"

	case "diff":
		if exitCode == 1 {
			return ExitConditionFalse, "arquivos diferem"
		}
		return ExitFailure, "erro no diff"

	case "test", "[":
		if exitCode == 1 {
			return ExitConditionFalse, "condicao falsa"
		}
		return ExitFailure, "erro no teste"
	}

	return ExitFailure, "comando falhou"
}

// IsReadOnlyExitCode returns true if the exit code should be treated as non-error.
func IsReadOnlyExitCode(command string, exitCode int) bool {
	meaning, _ := CommandExitSemantics(command, exitCode)
	return meaning == ExitSuccess || meaning == ExitNoMatches || meaning == ExitConditionFalse
}

// extractBaseCommand gets the last command from a pipe/chain.
func extractBaseCommand(command string) string {
	// Handle pipes: take last command
	if idx := strings.LastIndex(command, "|"); idx >= 0 {
		command = strings.TrimSpace(command[idx+1:])
	}
	// Handle && and ||
	for _, sep := range []string{"&&", "||"} {
		if idx := strings.LastIndex(command, sep); idx >= 0 {
			command = strings.TrimSpace(command[idx+len(sep):])
		}
	}
	// Extract first word
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return ""
	}
	// Handle sudo/wrappers
	for _, p := range parts {
		if p != "sudo" && p != "time" && p != "timeout" && p != "nice" && p != "env" {
			return p
		}
	}
	return parts[0]
}

// DestructiveWarning checks if a command has destructive patterns.
// Migrated from Claude Code's destructiveCommandWarning.ts
type DestructiveWarning struct {
	Warning  string
	Severity string // "high", "medium", "low"
}

// CheckDestructiveCommand returns warnings for destructive command patterns.
func CheckDestructiveCommand(command string) []DestructiveWarning {
	var warnings []DestructiveWarning
	cmd := strings.ToLower(command)

	// Git data loss
	for _, pattern := range []struct {
		pattern string
		msg     string
	}{
		{"git reset --hard", "reset --hard descarta commits permanentemente"},
		{"git push --force", "push --force pode sobrescrever historico remoto"},
		{"git push -f", "push -f pode sobrescrever historico remoto"},
		{"git clean -f", "git clean -f remove arquivos nao rastreados permanentemente"},
		{"git checkout .", "git checkout . descarta todas as modificacoes"},
		{"git restore .", "git restore . descarta todas as modificacoes"},
		{"git stash drop", "git stash drop remove stash permanentemente"},
		{"git stash clear", "git stash clear remove todos os stashes"},
		{"git branch -d", "git branch -d deleta branch"},
		{"git branch -D", "git branch -D forca delecao de branch"},
	} {
		if strings.Contains(cmd, pattern.pattern) {
			warnings = append(warnings, DestructiveWarning{pattern.msg, "high"})
		}
	}

	// Git safety bypass
	for _, pattern := range []struct {
		pattern string
		msg     string
	}{
		{"--no-verify", "pula verificacoes de pre-commit/pre-push"},
		{"--amend", "modifica o commit anterior"},
	} {
		if strings.Contains(cmd, pattern.pattern) {
			warnings = append(warnings, DestructiveWarning{pattern.msg, "medium"})
		}
	}

	// File deletion
	if strings.Contains(cmd, "rm -rf") || strings.Contains(cmd, "rm -r ") {
		warnings = append(warnings, DestructiveWarning{"remocao recursiva de arquivos/diretorios", "high"})
	}

	// Database
	for _, pattern := range []string{"drop table", "drop database", "delete from "} {
		if strings.Contains(cmd, pattern) {
			warnings = append(warnings, DestructiveWarning{"operacao destrutiva de banco de dados", "high"})
		}
	}

	return warnings
}

// IsReadOnlyCommand checks if a command is purely read-only.
// Migrated from Claude Code's readOnlyValidation.ts
func IsReadOnlyCommand(command string) bool {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return true
	}

	// Known read-only commands
	readonlyPrefixes := []string{
		"ls", "pwd", "whoami", "hostname", "date", "cat ", "head ", "tail ",
		"wc ", "file ", "stat ", "type ", "which ", "echo ", "printf ",
		"uname ", "env ", "printenv ", "history ", "alias ",
		"tree ", "du ", "df ", "free ", "uptime ", "id ", "groups ",
		"find ", "grep ", "rg ", "fd ", "locate ", "man ", "help ",
		"info ", "base64 ", "sha256sum ", "sha1sum ", "md5sum ",
		"sort ", "uniq ", "cut ", "tr ", "sed -n ", "awk ",
		"ps ", "top ", "pgrep ", "lsof ", "ss ", "netstat ",
		"git log", "git status", "git diff", "git show", "git branch",
		"git remote", "git config", "git blame", "git reflog",
		"git stash list", "git stash show",
		"docker ps", "docker images", "docker logs",
	}

	for _, prefix := range readonlyPrefixes {
		if strings.HasPrefix(cmd, prefix) {
			return true
		}
	}

	return false
}
