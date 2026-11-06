// Package commands — batch26_workspace.go
//
// Workspace-oriented slash commands for the rodada 3 wave 1 batch 26 surface.
// Unlike the AI placeholder batch (batch25), these commands inspect the
// filesystem rooted at ctx.WorkingDir to give the user a quick overview of
// their workspace:
//
//   - /workspace        — prints the working directory and the number of
//     entries inside it. If a ".git" directory is
//     present the output is annotated with the suffix
//     "(repo git presente)".
//   - /project          — detects the project type from well-known manifest
//     files (go.mod, package.json, pyproject.toml,
//     Cargo.toml) and prints the type plus the filename
//     that identified it.
//   - /context-summary  — placeholder while the conversation-aware wiring is
//     being finished in a later agent.
//
// Identifiers carry the "B26" suffix so they stay isolated from the rest of
// the package even though there is currently no collision risk with the other
// batches under internal/commands.
package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch26Workspace registers the batch26 workspace helper commands on
// the supplied registry.
func RegisterBatch26Workspace(reg *command.Registry) {
	reg.Register(cmdWorkspaceB26())
	reg.Register(cmdProjectB26())
	reg.Register(cmdContextSummaryB26())
}

// ---------------------------------------------------------------------------
// /workspace — working dir overview
// ---------------------------------------------------------------------------

// workspaceGitSuffixB26 is the exact suffix appended to the /workspace output
// when a ".git" directory is detected in the working directory.
const workspaceGitSuffixB26 = " (repo git presente)"

// formatWorkspaceB26 builds the /workspace display line for the supplied
// working directory, file count and git presence flag. Kept as a helper so
// tests can lock in the exact wording without re-deriving the format string.
func formatWorkspaceB26(workingDir string, fileCount int, hasGit bool) string {
	base := fmt.Sprintf("workspace: %s (%d arquivos)", workingDir, fileCount)
	if hasGit {
		base += workspaceGitSuffixB26
	}
	return base
}

// hasGitDirB26 reports whether workingDir contains a ".git" subdirectory.
// It deliberately tolerates non-directory ".git" files (worktree pointers)
// by only returning true when the entry is an actual directory.
func hasGitDirB26(workingDir string) bool {
	info, err := os.Stat(filepath.Join(workingDir, ".git"))
	if err != nil {
		return false
	}
	return info.IsDir()
}

func cmdWorkspaceB26() *command.Command {
	return &command.Command{
		Name:        "workspace",
		Description: "Mostrar o diretorio de trabalho atual, contagem de arquivos e presenca de repo git",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			entries, err := os.ReadDir(ctx.WorkingDir)
			if err != nil {
				return errResult(fmt.Sprintf("nao foi possivel ler %s: %v", ctx.WorkingDir, err)), nil
			}
			return okResult(formatWorkspaceB26(ctx.WorkingDir, len(entries), hasGitDirB26(ctx.WorkingDir))), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /project — project-type detection
// ---------------------------------------------------------------------------

// projectTypeMarkerB26 pairs a marker filename with the human-readable
// project type label printed by /project. The slice is iterated in order so
// the first matching marker wins, matching the deterministic behaviour of the
// TypeScript reference.
type projectTypeMarkerB26 struct {
	filename string
	label    string
}

// projectTypeMarkersB26 is the ordered list of manifest files the /project
// command recognises. Kept as a package variable so tests can assert the set
// without duplicating the literals.
var projectTypeMarkersB26 = []projectTypeMarkerB26{
	{filename: "go.mod", label: "Go"},
	{filename: "package.json", label: "Node"},
	{filename: "pyproject.toml", label: "Python"},
	{filename: "Cargo.toml", label: "Rust"},
}

// projectUnknownMessageB26 is the exact display string returned by /project
// when none of the known manifest files are present.
const projectUnknownMessageB26 = "projeto: tipo desconhecido (nenhum manifesto reconhecido encontrado)"

// detectProjectTypeB26 walks projectTypeMarkersB26 in order and returns the
// first marker whose filename exists (as a regular file) inside workingDir.
// The second return value is false when no marker matches.
func detectProjectTypeB26(workingDir string) (projectTypeMarkerB26, bool) {
	for _, marker := range projectTypeMarkersB26 {
		info, err := os.Stat(filepath.Join(workingDir, marker.filename))
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		return marker, true
	}
	return projectTypeMarkerB26{}, false
}

// formatProjectB26 builds the /project display line for a resolved marker.
func formatProjectB26(marker projectTypeMarkerB26) string {
	return fmt.Sprintf("projeto: %s (%s)", marker.label, marker.filename)
}

func cmdProjectB26() *command.Command {
	return &command.Command{
		Name:        "project",
		Description: "Detectar o tipo do projeto a partir do manifesto presente (go.mod, package.json, pyproject.toml, Cargo.toml)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			marker, ok := detectProjectTypeB26(ctx.WorkingDir)
			if !ok {
				return okResult(projectUnknownMessageB26), nil
			}
			return okResult(formatProjectB26(marker)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /context-summary — placeholder
// ---------------------------------------------------------------------------

// contextSummaryPlaceholderMessageB26 is the exact display string returned by
// /context-summary while the conversation-aware wiring is still pending.
const contextSummaryPlaceholderMessageB26 = "resumo de contexto pendente wiring (precisa acessar conversação)"

func cmdContextSummaryB26() *command.Command {
	return &command.Command{
		Name:        "context-summary",
		Description: "Resumir o contexto atual da conversa (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(contextSummaryPlaceholderMessageB26), nil
		},
	}
}
