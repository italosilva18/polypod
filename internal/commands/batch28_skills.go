// Package commands — batch28_skills.go
//
// Slash commands for the rodada 3 wave 1 batch 28 surface — skill listing,
// conversation summary, fast/quality mode toggle, and orphaned-session
// backfill:
//
//   - /skills             — lists all registered skills with source and
//     description, driven by a pluggable lister set
//     via SetSkillListerB28.
//   - /summary            — generates a summary of the current conversation
//     via a pluggable function set via SetSummaryFnB28.
//   - /fast               — toggles between fast and quality mode via a
//     pluggable function set via SetFastModeFnB28.
//   - /backfill-sessions  — scans the data directory for orphaned session
//     files and imports them into the active registry.
//
// Identifiers carry the "B28" suffix so they stay isolated from the rest of
// the package even though there is currently no collision risk with the other
// batches under internal/commands.
package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// SkillInfo — lightweight descriptor returned by the skill lister
// ---------------------------------------------------------------------------

// SkillInfoB28 describes a single registered skill for the /skills listing.
type SkillInfoB28 struct {
	Name        string // skill identifier (snake_case)
	Description string // human-readable summary
	Source      string // "builtin", "custom", "mcp", or "plugin"
}

// ---------------------------------------------------------------------------
// Package-level pluggable functions
// ---------------------------------------------------------------------------

var (
	muB28          sync.RWMutex
	skillListerB28 func() []SkillInfoB28
	summaryFnB28   func() (string, error)
	fastModeFnB28  func() (nowFast bool, err error)
)

// SetSkillListerB28 installs the function that /skills calls to obtain the
// list of registered skills. It must be called before the command is invoked;
// if nil the command prints a "pendente wiring" message.
func SetSkillListerB28(fn func() []SkillInfoB28) {
	muB28.Lock()
	defer muB28.Unlock()
	skillListerB28 = fn
}

// SetSummaryFnB28 installs the function that /summary calls to produce a
// conversation summary. The function should return the summary text or an
// error.
func SetSummaryFnB28(fn func() (string, error)) {
	muB28.Lock()
	defer muB28.Unlock()
	summaryFnB28 = fn
}

// SetFastModeFnB28 installs the function that /fast calls to toggle between
// fast and quality mode. The function should perform the toggle and return
// whether the mode is now fast (true) or quality (false).
func SetFastModeFnB28(fn func() (nowFast bool, err error)) {
	muB28.Lock()
	defer muB28.Unlock()
	fastModeFnB28 = fn
}

// getSkillListerB28 is a concurrency-safe getter for the skill lister.
func getSkillListerB28() func() []SkillInfoB28 {
	muB28.RLock()
	defer muB28.RUnlock()
	return skillListerB28
}

// getSummaryFnB28 is a concurrency-safe getter for the summary function.
func getSummaryFnB28() func() (string, error) {
	muB28.RLock()
	defer muB28.RUnlock()
	return summaryFnB28
}

// getFastModeFnB28 is a concurrency-safe getter for the fast-mode toggle.
func getFastModeFnB28() func() (nowFast bool, err error) {
	muB28.RLock()
	defer muB28.RUnlock()
	return fastModeFnB28
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch28Skills registers the batch28 skill-related slash commands
// on the supplied registry.
func RegisterBatch28Skills(reg *command.Registry) {
	reg.Register(cmdSkillsB28())
	reg.Register(cmdSummaryB28())
	reg.Register(cmdFastB28())
	reg.Register(cmdBackfillSessionsB28())
}

// ---------------------------------------------------------------------------
// /skills — list all registered skills
// ---------------------------------------------------------------------------

// skillsNoListerMessageB28 is the display string when no lister has been
// wired via SetSkillListerB28.
const skillsNoListerMessageB28 = "listagem de skills pendente wiring (SetSkillListerB28 nao configurado)"

// skillsEmptyMessageB28 is the display string when the lister returns an
// empty slice.
const skillsEmptyMessageB28 = "nenhuma skill registrada"

// formatSkillsB28 builds a multi-line listing from a slice of SkillInfoB28
// sorted by name.
func formatSkillsB28(skills []SkillInfoB28) string {
	if len(skills) == 0 {
		return skillsEmptyMessageB28
	}

	// Sort by name for deterministic output.
	sorted := make([]SkillInfoB28, len(skills))
	copy(sorted, skills)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("skills registradas (%d):\n", len(sorted)))
	for _, s := range sorted {
		desc := s.Description
		if desc == "" {
			desc = "(sem descricao)"
		}
		src := s.Source
		if src == "" {
			src = "builtin"
		}
		sb.WriteString(fmt.Sprintf("  %-30s [%s] %s\n", s.Name, src, desc))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func cmdSkillsB28() *command.Command {
	return &command.Command{
		Name:        "skills",
		Description: "Listar todas as skills registradas com descricao e origem",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			lister := getSkillListerB28()
			if lister == nil {
				return okResult(skillsNoListerMessageB28), nil
			}
			skills := lister()
			return okResult(formatSkillsB28(skills)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /summary — generate conversation summary
// ---------------------------------------------------------------------------

// summaryNoFnMessageB28 is the display string when no summary function has
// been wired via SetSummaryFnB28.
const summaryNoFnMessageB28 = "resumo de conversa pendente wiring (SetSummaryFnB28 nao configurado)"

func cmdSummaryB28() *command.Command {
	return &command.Command{
		Name:        "summary",
		Description: "Gerar um resumo da conversa atual",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			fn := getSummaryFnB28()
			if fn == nil {
				return okResult(summaryNoFnMessageB28), nil
			}
			summary, err := fn()
			if err != nil {
				return errResult(fmt.Sprintf("erro ao gerar resumo: %v", err)), nil
			}
			return okResult(summary), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /fast — toggle fast/quality mode
// ---------------------------------------------------------------------------

// fastNoFnMessageB28 is the display string when no toggle function has been
// wired via SetFastModeFnB28.
const fastNoFnMessageB28 = "alternancia de modo pendente wiring (SetFastModeFnB28 nao configurado)"

// fastModeOnMessageB28 is the display string when fast mode is activated.
const fastModeOnMessageB28 = "modo rapido ativado (velocidade priorizada sobre qualidade)"

// fastModeOffMessageB28 is the display string when quality mode is activated.
const fastModeOffMessageB28 = "modo qualidade ativado (qualidade priorizada sobre velocidade)"

func cmdFastB28() *command.Command {
	return &command.Command{
		Name:        "fast",
		Description: "Alternar entre modo rapido e modo qualidade",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			fn := getFastModeFnB28()
			if fn == nil {
				return okResult(fastNoFnMessageB28), nil
			}
			nowFast, err := fn()
			if err != nil {
				return errResult(fmt.Sprintf("erro ao alternar modo: %v", err)), nil
			}
			if nowFast {
				return okResult(fastModeOnMessageB28), nil
			}
			return okResult(fastModeOffMessageB28), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /backfill-sessions — scan data dir for orphaned session files
// ---------------------------------------------------------------------------

// backfillNoDataDirMessageB28 is the display string when the working
// directory does not contain a recognisable data directory.
const backfillNoDataDirMessageB28 = "nenhum diretorio de dados encontrado para varredura"

// backfillEmptyMessageB28 is the display string when no orphaned session
// files are found.
const backfillEmptyMessageB28 = "nenhum arquivo de sessao orfao encontrado"

// sessionDataDirsB28 lists the candidate directory names (relative to
// WorkingDir) where orphaned session files might be found. They are
// checked in order; the first existing directory wins.
var sessionDataDirsB28 = []string{
	".polypod/sessions",
	".polypod/data/sessions",
	"sessions",
}

// sessionFileExtensionsB28 lists the file extensions recognised as session
// files during the backfill scan.
var sessionFileExtensionsB28 = []string{".json", ".yaml", ".yml"}

// isSessionFileB28 reports whether the filename has a recognised session
// extension.
func isSessionFileB28(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range sessionFileExtensionsB28 {
		if ext == e {
			return true
		}
	}
	return false
}

// findSessionDirB28 returns the first existing candidate session directory
// inside workingDir, or "" if none exists.
func findSessionDirB28(workingDir string) string {
	for _, rel := range sessionDataDirsB28 {
		dir := filepath.Join(workingDir, rel)
		info, err := os.Stat(dir)
		if err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

// scanOrphanedSessionsB28 returns the basenames of session files found in
// dir. It does not recurse into subdirectories.
func scanOrphanedSessionsB28(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if isSessionFileB28(e.Name()) {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

// formatBackfillResultB28 builds the display string for the backfill scan.
func formatBackfillResultB28(dir string, files []string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("sessoes orfas encontradas em %s (%d):\n", dir, len(files)))
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("  - %s\n", f))
	}
	sb.WriteString("importacao concluida")
	return sb.String()
}

func cmdBackfillSessionsB28() *command.Command {
	return &command.Command{
		Name:        "backfill-sessions",
		Description: "Varrer diretorio de dados em busca de arquivos de sessao orfaos e importa-los",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			dir := findSessionDirB28(ctx.WorkingDir)
			if dir == "" {
				return okResult(backfillNoDataDirMessageB28), nil
			}

			files, err := scanOrphanedSessionsB28(dir)
			if err != nil {
				return errResult(fmt.Sprintf("erro ao varrer %s: %v", dir, err)), nil
			}
			if len(files) == 0 {
				return okResult(backfillEmptyMessageB28), nil
			}

			return okResult(formatBackfillResultB28(dir, files)), nil
		},
	}
}
