// Package commands — batch1.go
// Migrated slash-command implementations (batch 1 of N).
// Each function is registered via RegisterBatch1 into the command.Registry.
package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch1 registers the first 25 migrated slash commands.
func RegisterBatch1(reg *command.Registry) {
	reg.Register(cmdAddDir())
	reg.Register(cmdAgents())
	reg.Register(cmdBranch())
	reg.Register(cmdBtw())
	reg.Register(cmdClear())
	reg.Register(cmdColor())
	reg.Register(cmdCopy())
	reg.Register(cmdEffort())
	reg.Register(cmdExit())
	reg.Register(cmdFast())
	reg.Register(cmdFeedback())
	reg.Register(cmdFiles())
	reg.Register(cmdHelp())
	reg.Register(cmdIDE())
	reg.Register(cmdInsights())
	reg.Register(cmdKeybindings())
	reg.Register(cmdLogin())
	reg.Register(cmdLogout())
	reg.Register(cmdMobile())
	reg.Register(cmdOutputStyle())
	reg.Register(cmdPasses())
	reg.Register(cmdPrivacySettings())
	reg.Register(cmdRateLimitOptions())
	reg.Register(cmdReleaseNotes())
	reg.Register(cmdRename())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// configHomeDir returns ~/.polypod (or $POLYPOD_CONFIG_DIR).
func configHomeDir() string {
	if d := os.Getenv("POLYPOD_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".polypod")
}

// sessionConfigPath returns the path to the per-session metadata JSON.
func sessionConfigPath(sessionID string) string {
	return filepath.Join(configHomeDir(), "sessions", sessionID+".json")
}

// readSessionJSON loads a per-session JSON blob.
func readSessionJSON(sessionID string) (map[string]any, error) {
	data, err := os.ReadFile(sessionConfigPath(sessionID))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// writeSessionJSON persists a per-session JSON blob.
func writeSessionJSON(sessionID string, m map[string]any) error {
	dir := filepath.Dir(sessionConfigPath(sessionID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sessionConfigPath(sessionID), data, 0o600)
}

// settingsPath returns path to the global settings JSON.
func settingsPath() string {
	return filepath.Join(configHomeDir(), "settings.json")
}

// readSettings loads global settings.
func readSettings() (map[string]any, error) {
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// writeSettings saves global settings.
func writeSettings(m map[string]any) error {
	dir := filepath.Dir(settingsPath())
	os.MkdirAll(dir, 0o700)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(), data, 0o600)
}

// gitExec runs a git command in the working directory and returns stdout.
func gitExec(wd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = wd
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

// okResult builds a successful command result.
func okResult(msg string) *command.CommandResult {
	return &command.CommandResult{Display: msg}
}

// errResult builds an error message result.
func errResult(msg string) *command.CommandResult {
	return &command.CommandResult{Display: "error: " + msg}
}

// ---------------------------------------------------------------------------
// 1. /add-dir — add additional working directory
// ---------------------------------------------------------------------------

func cmdAddDir() *command.Command {
	return &command.Command{
		Name:        "add-dir",
		Description: "Add a new working directory",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return errResult("usage: /add-dir <path>"), nil
			}
			dirPath := args[0]

			// Expand ~ to home
			if strings.HasPrefix(dirPath, "~") {
				home, _ := os.UserHomeDir()
				dirPath = filepath.Join(home, dirPath[1:])
			}

			// Resolve to absolute
			if !filepath.IsAbs(dirPath) {
				dirPath = filepath.Join(ctx.WorkingDir, dirPath)
			}
			dirPath = filepath.Clean(dirPath)

			// Validate
			info, err := os.Stat(dirPath)
			if err != nil {
				return errResult(fmt.Sprintf("path not found: %s", dirPath)), nil
			}
			if !info.IsDir() {
				parent := filepath.Dir(dirPath)
				return errResult(fmt.Sprintf("%s is not a directory. Did you mean %s?", dirPath, parent)), nil
			}

			// Check if already a sub-path of the working directory
			rel, err := filepath.Rel(ctx.WorkingDir, dirPath)
			if err == nil && !strings.HasPrefix(rel, "..") {
				return errResult(fmt.Sprintf("%s is already within the working directory %s", dirPath, ctx.WorkingDir)), nil
			}

			// Persist to settings
			settings, _ := readSettings()
			dirs, _ := settings["additional_dirs"].([]any)
			for _, d := range dirs {
				if s, ok := d.(string); ok && s == dirPath {
					return okResult(fmt.Sprintf("%s is already registered as a working directory.", dirPath)), nil
				}
			}
			dirs = append(dirs, dirPath)
			settings["additional_dirs"] = dirs
			if err := writeSettings(settings); err != nil {
				return errResult(fmt.Sprintf("failed to save settings: %v", err)), nil
			}
			return okResult(fmt.Sprintf("Added %s as a working directory.", dirPath)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 2. /agents — list/manage agents
// ---------------------------------------------------------------------------

func cmdAgents() *command.Command {
	return &command.Command{
		Name:        "agents",
		Description: "Manage agent configurations",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// Scan .polypod/agents/ for .md files
			agentsDir := filepath.Join(ctx.WorkingDir, ".polypod", "agents")
			var agents []string

			entries, err := os.ReadDir(agentsDir)
			if err == nil {
				for _, e := range entries {
					if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
						agents = append(agents, strings.TrimSuffix(e.Name(), ".md"))
					}
				}
			}

			// Also check global agents
			globalDir := filepath.Join(configHomeDir(), "agents")
			gEntries, err := os.ReadDir(globalDir)
			if err == nil {
				for _, e := range gEntries {
					if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
						name := strings.TrimSuffix(e.Name(), ".md")
						// Deduplicate
						found := false
						for _, a := range agents {
							if a == name {
								found = true
								break
							}
						}
						if !found {
							agents = append(agents, name)
						}
					}
				}
			}

			if len(agents) == 0 {
				return okResult("No agents configured.\nCreate agents in .polypod/agents/<name>.md"), nil
			}

			sort.Strings(agents)

			var b strings.Builder
			b.WriteString("Agents\n\n")
			for _, name := range agents {
				b.WriteString("  - " + name + "\n")
			}
			b.WriteString(fmt.Sprintf("\n%d agent(s) found.", len(agents)))
			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 3. /branch — create git branch
// ---------------------------------------------------------------------------

func cmdBranch() *command.Command {
	return &command.Command{
		Name:        "branch",
		Aliases:     []string{"fork"},
		Description: "Create a new git branch from current HEAD",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return errResult("usage: /branch <name>"), nil
			}
			branchName := args[0]

			// Create and switch to the new branch
			out, err := gitExec(ctx.WorkingDir, "checkout", "-b", branchName)
			if err != nil {
				return errResult(fmt.Sprintf("git checkout -b %s failed: %s\n%s", branchName, err, out)), nil
			}
			return okResult(fmt.Sprintf("Created and switched to branch: %s", branchName)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 4. /btw — inject background note
// ---------------------------------------------------------------------------

func cmdBtw() *command.Command {
	return &command.Command{
		Name:        "btw",
		Description: "Inject a side note into the conversation context",
		Type:        command.CommandPrompt,
		Source:      command.SourceBuiltin,
		GetPrompt: func(args []string, ctx command.CommandContext) (string, error) {
			if len(args) == 0 {
				return "", fmt.Errorf("usage: /btw <note>")
			}
			note := strings.Join(args, " ")
			return fmt.Sprintf("[Side note from user]: %s", note), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 5. /clear — clear conversation history / caches
// ---------------------------------------------------------------------------

func cmdClear() *command.Command {
	return &command.Command{
		Name:        "clear",
		Aliases:     []string{"reset", "new"},
		Description: "Clear conversation history and free up context",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			sub := ""
			if len(args) > 0 {
				sub = strings.ToLower(args[0])
			}

			switch sub {
			case "caches", "cache":
				// Clear polypod cache directory
				cacheDir := filepath.Join(configHomeDir(), "cache")
				if err := os.RemoveAll(cacheDir); err != nil && !os.IsNotExist(err) {
					return errResult(fmt.Sprintf("failed to clear caches: %v", err)), nil
				}
				return okResult("Caches cleared."), nil

			default:
				// Clear conversation: remove session transcript if it exists
				if ctx.SessionID != "" {
					transcriptPath := filepath.Join(configHomeDir(), "projects", ctx.SessionID+".jsonl")
					_ = os.Remove(transcriptPath)
				}

				// Clear session env vars
				envDir := filepath.Join(configHomeDir(), "session-env", ctx.SessionID)
				_ = os.RemoveAll(envDir)

				return okResult("Conversation history cleared. Starting fresh."), nil
			}
		},
	}
}

// ---------------------------------------------------------------------------
// 6. /color — change session color scheme
// ---------------------------------------------------------------------------

func cmdColor() *command.Command {
	availableColors := []string{
		"blue", "cyan", "green", "magenta", "orange", "pink",
		"purple", "red", "teal", "yellow",
	}
	resetAliases := map[string]bool{
		"default": true, "reset": true, "none": true,
		"gray": true, "grey": true,
	}

	return &command.Command{
		Name:        "color",
		Description: "Set the prompt bar color for this session",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return errResult(fmt.Sprintf("usage: /color <color|default>\nAvailable: %s, default",
					strings.Join(availableColors, ", "))), nil
			}
			colorArg := strings.ToLower(args[0])

			if resetAliases[colorArg] {
				if ctx.SessionID != "" {
					m, _ := readSessionJSON(ctx.SessionID)
					if m == nil {
						m = map[string]any{}
					}
					m["color"] = "default"
					_ = writeSessionJSON(ctx.SessionID, m)
				}
				return okResult("Session color reset to default."), nil
			}

			valid := false
			for _, c := range availableColors {
				if c == colorArg {
					valid = true
					break
				}
			}
			if !valid {
				return errResult(fmt.Sprintf("invalid color %q. Available: %s, default",
					colorArg, strings.Join(availableColors, ", "))), nil
			}

			if ctx.SessionID != "" {
				m, _ := readSessionJSON(ctx.SessionID)
				if m == nil {
					m = map[string]any{}
				}
				m["color"] = colorArg
				_ = writeSessionJSON(ctx.SessionID, m)
			}
			return okResult(fmt.Sprintf("Session color set to: %s", colorArg)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 7. /copy — copy last response to clipboard
// ---------------------------------------------------------------------------

func cmdCopy() *command.Command {
	return &command.Command{
		Name:        "copy",
		Description: "Copy last assistant response to clipboard",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// Read session transcript to find the last assistant message
			if ctx.SessionID == "" {
				return errResult("no active session"), nil
			}

			transcriptPath := filepath.Join(configHomeDir(), "projects", ctx.SessionID+".jsonl")
			data, err := os.ReadFile(transcriptPath)
			if err != nil {
				return errResult("no conversation transcript found"), nil
			}

			lines := strings.Split(strings.TrimSpace(string(data)), "\n")

			// Parse the Nth-latest offset (default: 1 = last assistant message)
			offset := 1
			if len(args) > 0 {
				var n int
				if _, err := fmt.Sscanf(args[0], "%d", &n); err == nil && n > 0 {
					offset = n
				}
			}

			// Walk backwards to find assistant messages
			found := 0
			for i := len(lines) - 1; i >= 0; i-- {
				var entry map[string]any
				if err := json.Unmarshal([]byte(lines[i]), &entry); err != nil {
					continue
				}
				if entry["type"] == "assistant" {
					found++
					if found == offset {
						// Extract text content
						msg, _ := entry["message"].(map[string]any)
						if msg == nil {
							continue
						}
						content := extractTextFromMessage(msg)
						if content == "" {
							continue
						}
						if err := clipboard.WriteAll(content); err != nil {
							return errResult(fmt.Sprintf("failed to copy: %v", err)), nil
						}
						preview := content
						if len(preview) > 60 {
							preview = preview[:57] + "..."
						}
						return okResult(fmt.Sprintf("Copied to clipboard (%s)", preview)), nil
					}
				}
			}
			return errResult("no assistant response found to copy"), nil
		},
	}
}

// extractTextFromMessage pulls text blocks out of an API message.
func extractTextFromMessage(msg map[string]any) string {
	content := msg["content"]
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, block := range v {
			b, _ := block.(map[string]any)
			if b == nil {
				continue
			}
			if b["type"] == "text" {
				if t, ok := b["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// ---------------------------------------------------------------------------
// 8. /effort — set effort level
// ---------------------------------------------------------------------------

func cmdEffort() *command.Command {
	validLevels := map[string]bool{
		"low": true, "medium": true, "high": true, "max": true, "auto": true,
	}

	return &command.Command{
		Name:        "effort",
		Description: "Set effort level for model usage",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				// Show current effort
				settings, _ := readSettings()
				current, _ := settings["effort"].(string)
				if current == "" {
					current = "auto"
				}
				return okResult(fmt.Sprintf("Current effort level: %s\nUsage: /effort [low|medium|high|max|auto]", current)), nil
			}
			level := strings.ToLower(args[0])
			if !validLevels[level] {
				return errResult(fmt.Sprintf("invalid effort level %q. Options: low, medium, high, max, auto", level)), nil
			}
			settings, _ := readSettings()
			if settings == nil {
				settings = map[string]any{}
			}
			settings["effort"] = level
			if err := writeSettings(settings); err != nil {
				return errResult(fmt.Sprintf("failed to save: %v", err)), nil
			}
			return okResult(fmt.Sprintf("Effort level set to: %s", level)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 9. /exit — exit session
// ---------------------------------------------------------------------------

func cmdExit() *command.Command {
	return &command.Command{
		Name:                   "exit",
		Aliases:                []string{"quit", "q"},
		Description:            "Exit the session",
		Type:                   command.CommandLocal,
		Source:                 command.SourceBuiltin,
		DisableModelInvocation: true,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// The CLI layer should check for the exit signal; we return a
			// sentinel that the REPL loop recognises.
			return &command.CommandResult{Display: "__EXIT__"}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// 10. /fast — toggle fast mode
// ---------------------------------------------------------------------------

func cmdFast() *command.Command {
	return &command.Command{
		Name:        "fast",
		Description: "Toggle fast mode (smaller/faster model)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			settings, _ := readSettings()
			if settings == nil {
				settings = map[string]any{}
			}
			current, _ := settings["fast_mode"].(bool)

			if len(args) > 0 {
				switch strings.ToLower(args[0]) {
				case "on", "true", "1":
					settings["fast_mode"] = true
					_ = writeSettings(settings)
					return okResult("Fast mode enabled."), nil
				case "off", "false", "0":
					settings["fast_mode"] = false
					_ = writeSettings(settings)
					return okResult("Fast mode disabled."), nil
				default:
					return errResult("usage: /fast [on|off]"), nil
				}
			}

			// Toggle
			settings["fast_mode"] = !current
			_ = writeSettings(settings)
			if !current {
				return okResult("Fast mode enabled."), nil
			}
			return okResult("Fast mode disabled."), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 11. /feedback — submit feedback
// ---------------------------------------------------------------------------

func cmdFeedback() *command.Command {
	return &command.Command{
		Name:        "feedback",
		Aliases:     []string{"bug"},
		Description: "Submit feedback about Polypod",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return okResult("Submit feedback or bug reports at:\nhttps://github.com/costa/polypod/issues\n\nOr use: /feedback <your message>"), nil
			}
			report := strings.Join(args, " ")

			// Save feedback locally
			feedbackDir := filepath.Join(configHomeDir(), "feedback")
			os.MkdirAll(feedbackDir, 0o700)
			ts := time.Now().Format("20060102-150405")
			feedbackFile := filepath.Join(feedbackDir, fmt.Sprintf("feedback-%s.txt", ts))

			content := fmt.Sprintf("Date: %s\nSession: %s\nWorkDir: %s\n\n%s\n",
				time.Now().Format(time.RFC3339), ctx.SessionID, ctx.WorkingDir, report)

			if err := os.WriteFile(feedbackFile, []byte(content), 0o600); err != nil {
				return errResult(fmt.Sprintf("failed to save feedback: %v", err)), nil
			}
			return okResult(fmt.Sprintf("Feedback saved to %s\nThank you!", feedbackFile)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 12. /files — list project files in context
// ---------------------------------------------------------------------------

func cmdFiles() *command.Command {
	return &command.Command{
		Name:        "files",
		Description: "List files in the current working directory",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			wd := ctx.WorkingDir
			if wd == "" {
				var err error
				wd, err = os.Getwd()
				if err != nil {
					return errResult(fmt.Sprintf("cannot determine working directory: %v", err)), nil
				}
			}

			var files []string
			ignored := map[string]bool{
				".git": true, "node_modules": true, "vendor": true,
				"__pycache__": true, "dist": true, "build": true,
				".next": true, ".cache": true, "target": true,
			}

			err := filepath.Walk(wd, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil // skip errors
				}
				if info.IsDir() {
					if ignored[info.Name()] {
						return filepath.SkipDir
					}
					return nil
				}
				rel, _ := filepath.Rel(wd, path)
				files = append(files, rel)
				if len(files) >= 500 {
					return filepath.SkipAll
				}
				return nil
			})
			if err != nil {
				return errResult(fmt.Sprintf("walk error: %v", err)), nil
			}

			if len(files) == 0 {
				return okResult("No files found in working directory."), nil
			}

			var b strings.Builder
			b.WriteString(fmt.Sprintf("Files in %s (%d):\n\n", filepath.Base(wd), len(files)))
			for _, f := range files {
				b.WriteString("  " + f + "\n")
			}
			if len(files) >= 500 {
				b.WriteString("\n  ... (truncated at 500 files)")
			}
			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 13. /help — show help
// ---------------------------------------------------------------------------

func cmdHelp() *command.Command {
	return &command.Command{
		Name:        "help",
		Description: "Show help and available commands",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			commands := []struct{ cmd, desc string }{
				{"/add-dir <path>", "Add a working directory"},
				{"/agents", "List/manage agents"},
				{"/branch <name>", "Create a new git branch"},
				{"/btw <note>", "Inject a side note"},
				{"/clear", "Clear conversation history"},
				{"/color <color|default>", "Set session color"},
				{"/copy [N]", "Copy Nth-latest response to clipboard"},
				{"/effort [level]", "Set effort level (low/medium/high/max/auto)"},
				{"/exit", "Exit the session"},
				{"/fast [on|off]", "Toggle fast mode"},
				{"/feedback [msg]", "Submit feedback"},
				{"/files", "List project files"},
				{"/help", "Show this help"},
				{"/ide [open]", "IDE integration status"},
				{"/insights", "Show session insights"},
				{"/keybindings", "Open keybindings config"},
				{"/login", "Authenticate"},
				{"/logout", "Sign out"},
				{"/mobile", "Mobile app info"},
				{"/output-style <style>", "Set output style"},
				{"/passes", "Manage referral passes"},
				{"/privacy-settings", "View/update privacy settings"},
				{"/rate-limit-options", "Rate limit configuration"},
				{"/release-notes", "Show release notes"},
				{"/rename [name]", "Rename session"},
			}

			var b strings.Builder
			b.WriteString("Commands\n\n")

			maxCmd := 0
			for _, c := range commands {
				if len(c.cmd) > maxCmd {
					maxCmd = len(c.cmd)
				}
			}

			for _, c := range commands {
				padding := strings.Repeat(" ", maxCmd-len(c.cmd)+2)
				b.WriteString(fmt.Sprintf("  %s%s%s\n", c.cmd, padding, c.desc))
			}

			b.WriteString("\nShortcuts: Tab=complete  Esc=clear  Up/Down=history  Ctrl+L=clear screen")
			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 14. /ide — IDE integration status
// ---------------------------------------------------------------------------

func cmdIDE() *command.Command {
	return &command.Command{
		Name:        "ide",
		Description: "Manage IDE integrations and show status",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			sub := ""
			if len(args) > 0 {
				sub = strings.ToLower(args[0])
			}

			wd := ctx.WorkingDir
			if wd == "" {
				wd, _ = os.Getwd()
			}

			// Detect IDE project files
			type ideInfo struct {
				name   string
				marker string
			}
			ides := []ideInfo{
				{"VS Code", ".vscode"},
				{"IntelliJ/JetBrains", ".idea"},
				{"Vim/Neovim", ".vim"},
				{"Emacs", ".emacs.d"},
				{"Sublime Text", ".sublime-project"},
			}

			var detected []string
			for _, ide := range ides {
				checkPath := filepath.Join(wd, ide.marker)
				if _, err := os.Stat(checkPath); err == nil {
					detected = append(detected, ide.name)
				}
			}

			if sub == "open" {
				// Try to open directory in detected IDE
				var openCmd string
				switch runtime.GOOS {
				case "darwin":
					openCmd = "open"
				case "linux":
					openCmd = "xdg-open"
				case "windows":
					openCmd = "explorer"
				}
				if openCmd != "" {
					cmd := exec.Command(openCmd, wd)
					_ = cmd.Start()
					return okResult(fmt.Sprintf("Opening %s...", wd)), nil
				}
				return errResult("could not determine how to open directory on this OS"), nil
			}

			var b strings.Builder
			b.WriteString("IDE Integration Status\n\n")
			b.WriteString(fmt.Sprintf("  Working dir: %s\n", wd))

			if len(detected) == 0 {
				b.WriteString("  No IDE project files detected.\n")
			} else {
				b.WriteString("  Detected IDEs:\n")
				for _, ide := range detected {
					b.WriteString(fmt.Sprintf("    - %s\n", ide))
				}
			}

			b.WriteString("\n  Use /ide open to open the project directory.")
			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 15. /insights — show session insights
// ---------------------------------------------------------------------------

func cmdInsights() *command.Command {
	return &command.Command{
		Name:        "insights",
		Description: "Show session usage insights",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// Scan session files to gather basic stats
			sessionsDir := filepath.Join(configHomeDir(), "sessions")
			entries, err := os.ReadDir(sessionsDir)
			if err != nil {
				return okResult("No session data available for insights."), nil
			}

			totalSessions := 0
			totalSize := int64(0)
			var earliest, latest time.Time

			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
					continue
				}
				totalSessions++
				info, err := e.Info()
				if err != nil {
					continue
				}
				totalSize += info.Size()
				modTime := info.ModTime()
				if earliest.IsZero() || modTime.Before(earliest) {
					earliest = modTime
				}
				if latest.IsZero() || modTime.After(latest) {
					latest = modTime
				}
			}

			// Also scan transcript files
			projectsDir := filepath.Join(configHomeDir(), "projects")
			transcriptEntries, _ := os.ReadDir(projectsDir)
			transcriptCount := 0
			for _, e := range transcriptEntries {
				if strings.HasSuffix(e.Name(), ".jsonl") {
					transcriptCount++
				}
			}

			var b strings.Builder
			b.WriteString("Session Insights\n\n")
			b.WriteString(fmt.Sprintf("  Total sessions:     %d\n", totalSessions))
			b.WriteString(fmt.Sprintf("  Transcript files:   %d\n", transcriptCount))
			b.WriteString(fmt.Sprintf("  Session data size:  %.1f KB\n", float64(totalSize)/1024))
			if !earliest.IsZero() {
				b.WriteString(fmt.Sprintf("  Date range:         %s to %s\n",
					earliest.Format("2006-01-02"), latest.Format("2006-01-02")))
				days := int(latest.Sub(earliest).Hours()/24) + 1
				b.WriteString(fmt.Sprintf("  Days active:        %d\n", days))
				if days > 0 {
					b.WriteString(fmt.Sprintf("  Sessions/day:       %.1f\n", float64(totalSessions)/float64(days)))
				}
			}
			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 16. /keybindings — manage keybindings
// ---------------------------------------------------------------------------

func cmdKeybindings() *command.Command {
	return &command.Command{
		Name:        "keybindings",
		Description: "Open or create your keybindings configuration file",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			kbPath := filepath.Join(configHomeDir(), "keybindings.json")
			dir := filepath.Dir(kbPath)
			os.MkdirAll(dir, 0o700)

			// Check if file exists
			_, err := os.Stat(kbPath)
			fileExists := err == nil

			if !fileExists {
				// Create template
				template := `{
  "// Keybindings configuration for Polypod": "",
  "// Format: key-combo -> action": "",
  "bindings": {
    "ctrl+l": "clear_screen",
    "ctrl+c": "cancel",
    "ctrl+d": "exit",
    "tab": "complete",
    "up": "history_prev",
    "down": "history_next",
    "esc": "clear_input"
  }
}
`
				if err := os.WriteFile(kbPath, []byte(template), 0o600); err != nil {
					return errResult(fmt.Sprintf("failed to create keybindings file: %v", err)), nil
				}
			}

			// Try to open in $EDITOR
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = os.Getenv("VISUAL")
			}
			if editor != "" {
				cmd := exec.Command(editor, kbPath)
				cmd.Stdin = os.Stdin
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					msg := "Created"
					if fileExists {
						msg = "Opened"
					}
					return okResult(fmt.Sprintf("%s %s. Could not open in editor: %v", msg, kbPath, err)), nil
				}
				if fileExists {
					return okResult(fmt.Sprintf("Opened %s in your editor.", kbPath)), nil
				}
				return okResult(fmt.Sprintf("Created %s with template. Opened in your editor.", kbPath)), nil
			}

			if fileExists {
				return okResult(fmt.Sprintf("Keybindings file: %s\nSet $EDITOR to open it automatically.", kbPath)), nil
			}
			return okResult(fmt.Sprintf("Created keybindings template: %s\nSet $EDITOR to open it automatically.", kbPath)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 17. /login — authenticate
// ---------------------------------------------------------------------------

func cmdLogin() *command.Command {
	return &command.Command{
		Name:        "login",
		Description: "Sign in with your account",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// Check if API key is already configured
			apiKey := os.Getenv("POLYPOD_API_KEY")
			if apiKey == "" {
				apiKey = os.Getenv("ANTHROPIC_API_KEY")
			}
			if apiKey == "" {
				apiKey = os.Getenv("OPENAI_API_KEY")
			}

			if apiKey != "" {
				return okResult("Already authenticated via environment variable.\nTo switch accounts, update your API key environment variable."), nil
			}

			// Check for stored credentials
			credPath := filepath.Join(configHomeDir(), "credentials.json")
			if _, err := os.Stat(credPath); err == nil {
				return okResult("Already authenticated.\nTo switch accounts, run /logout first, then /login."), nil
			}

			// Prompt to set API key
			var b strings.Builder
			b.WriteString("Authentication Setup\n\n")
			b.WriteString("Set one of the following environment variables:\n\n")
			b.WriteString("  export POLYPOD_API_KEY=<your-key>\n")
			b.WriteString("  export ANTHROPIC_API_KEY=<your-key>\n")
			b.WriteString("  export OPENAI_API_KEY=<your-key>\n")
			b.WriteString("\nOr save credentials:\n\n")
			b.WriteString(fmt.Sprintf("  echo '{\"api_key\": \"<your-key>\"}' > %s\n", credPath))
			b.WriteString(fmt.Sprintf("  chmod 600 %s\n", credPath))
			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 18. /logout — clear auth
// ---------------------------------------------------------------------------

func cmdLogout() *command.Command {
	return &command.Command{
		Name:        "logout",
		Description: "Sign out and clear stored credentials",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			credPath := filepath.Join(configHomeDir(), "credentials.json")
			tokenPath := filepath.Join(configHomeDir(), "auth_token")

			removed := false
			if err := os.Remove(credPath); err == nil {
				removed = true
			}
			if err := os.Remove(tokenPath); err == nil {
				removed = true
			}

			if removed {
				return okResult("Credentials removed. You are now signed out.\nNote: environment variables (POLYPOD_API_KEY, etc.) are still active if set."), nil
			}
			return okResult("No stored credentials found.\nIf using environment variables, unset them manually:\n  unset POLYPOD_API_KEY"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 19. /mobile — mobile connection info
// ---------------------------------------------------------------------------

func cmdMobile() *command.Command {
	return &command.Command{
		Name:        "mobile",
		Aliases:     []string{"ios", "android"},
		Description: "Show mobile app connection info",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			var b strings.Builder
			b.WriteString("Mobile App\n\n")
			b.WriteString("  Polypod is available on mobile devices.\n\n")
			b.WriteString("  Download:\n")
			b.WriteString("    iOS:     https://apps.apple.com/app/polypod\n")
			b.WriteString("    Android: https://play.google.com/store/apps/details?id=com.polypod\n")
			b.WriteString("\n  To connect your mobile session with this desktop session,\n")
			b.WriteString("  ensure both devices use the same account credentials.")
			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 20. /output-style — set output style
// ---------------------------------------------------------------------------

func cmdOutputStyle() *command.Command {
	validStyles := map[string]bool{
		"concise": true, "normal": true, "detailed": true, "verbose": true,
	}

	return &command.Command{
		Name:        "output-style",
		Description: "Set output verbosity style",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Disabled:    false,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				settings, _ := readSettings()
				current, _ := settings["output_style"].(string)
				if current == "" {
					current = "normal"
				}
				return okResult(fmt.Sprintf("Current output style: %s\nUsage: /output-style [concise|normal|detailed|verbose]", current)), nil
			}

			style := strings.ToLower(args[0])
			if !validStyles[style] {
				return errResult(fmt.Sprintf("invalid style %q. Options: concise, normal, detailed, verbose", style)), nil
			}

			settings, _ := readSettings()
			if settings == nil {
				settings = map[string]any{}
			}
			settings["output_style"] = style
			if err := writeSettings(settings); err != nil {
				return errResult(fmt.Sprintf("failed to save: %v", err)), nil
			}
			return okResult(fmt.Sprintf("Output style set to: %s", style)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 21. /passes — manage referral passes
// ---------------------------------------------------------------------------

func cmdPasses() *command.Command {
	return &command.Command{
		Name:        "passes",
		Description: "Share free passes with friends and earn extra usage",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			settings, _ := readSettings()
			passes, _ := settings["passes"].([]any)

			var b strings.Builder
			b.WriteString("Referral Passes\n\n")

			if len(passes) == 0 {
				b.WriteString("  No passes available.\n")
				b.WriteString("  Passes are awarded when you reach usage milestones.\n")
			} else {
				b.WriteString(fmt.Sprintf("  Available passes: %d\n\n", len(passes)))
				for i, p := range passes {
					if ps, ok := p.(map[string]any); ok {
						code, _ := ps["code"].(string)
						status, _ := ps["status"].(string)
						if status == "" {
							status = "available"
						}
						b.WriteString(fmt.Sprintf("  %d. Code: %s  Status: %s\n", i+1, code, status))
					}
				}
			}

			b.WriteString("\n  Share your referral link to earn extra usage time.")
			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 22. /privacy-settings — manage privacy
// ---------------------------------------------------------------------------

func cmdPrivacySettings() *command.Command {
	return &command.Command{
		Name:        "privacy-settings",
		Description: "View and update your privacy settings",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			settings, _ := readSettings()

			telemetry, _ := settings["telemetry"].(bool)
			analytics, _ := settings["analytics"].(bool)
			dataSharing, _ := settings["data_sharing"].(bool)

			if len(args) > 0 {
				// Parse key=value arguments
				for _, arg := range args {
					parts := strings.SplitN(arg, "=", 2)
					if len(parts) != 2 {
						continue
					}
					key := strings.ToLower(parts[0])
					val := strings.ToLower(parts[1])
					boolVal := val == "true" || val == "on" || val == "1"

					switch key {
					case "telemetry":
						settings["telemetry"] = boolVal
					case "analytics":
						settings["analytics"] = boolVal
					case "data_sharing", "data-sharing":
						settings["data_sharing"] = boolVal
					default:
						return errResult(fmt.Sprintf("unknown privacy setting: %s", key)), nil
					}
				}
				if err := writeSettings(settings); err != nil {
					return errResult(fmt.Sprintf("failed to save: %v", err)), nil
				}
				return okResult("Privacy settings updated."), nil
			}

			boolStr := func(v bool) string {
				if v {
					return "on"
				}
				return "off"
			}

			var b strings.Builder
			b.WriteString("Privacy Settings\n\n")
			b.WriteString(fmt.Sprintf("  telemetry:    %s\n", boolStr(telemetry)))
			b.WriteString(fmt.Sprintf("  analytics:    %s\n", boolStr(analytics)))
			b.WriteString(fmt.Sprintf("  data_sharing: %s\n", boolStr(dataSharing)))
			b.WriteString("\nTo update: /privacy-settings telemetry=off analytics=off")
			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 23. /rate-limit-options — rate limit config
// ---------------------------------------------------------------------------

func cmdRateLimitOptions() *command.Command {
	return &command.Command{
		Name:        "rate-limit-options",
		Description: "Show options when rate limit is reached",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Disabled:    false,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			settings, _ := readSettings()

			var b strings.Builder
			b.WriteString("Rate Limit Options\n\n")
			b.WriteString("  When you hit a rate limit, you can:\n\n")
			b.WriteString("  1. Wait for the limit to reset (usually 1-5 minutes)\n")
			b.WriteString("  2. Switch to a different model: /model <name>\n")
			b.WriteString("  3. Enable fast mode: /fast on\n")
			b.WriteString("  4. Reduce effort level: /effort low\n")

			retryAfter, _ := settings["rate_limit_retry_after"].(float64)
			if retryAfter > 0 {
				b.WriteString(fmt.Sprintf("\n  Auto-retry after: %.0f seconds\n", retryAfter))
			}

			b.WriteString("\nTo configure auto-retry:\n")
			b.WriteString("  Set rate_limit_retry_after in settings.json")
			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 24. /release-notes — show release notes
// ---------------------------------------------------------------------------

func cmdReleaseNotes() *command.Command {
	return &command.Command{
		Name:        "release-notes",
		Description: "View release notes",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// Check for cached changelog
			changelogPath := filepath.Join(configHomeDir(), "changelog.md")
			data, err := os.ReadFile(changelogPath)
			if err == nil && len(data) > 0 {
				content := string(data)
				// Truncate to last 5000 chars for display
				if len(content) > 5000 {
					content = content[:5000] + "\n\n... (truncated, see full changelog file)"
				}
				return okResult(content), nil
			}

			// Check for a VERSION file in the project
			versionPath := filepath.Join(ctx.WorkingDir, "VERSION")
			vData, err := os.ReadFile(versionPath)
			if err == nil {
				return okResult(fmt.Sprintf("Current version: %s\n\nFull changelog: %s",
					strings.TrimSpace(string(vData)), changelogPath)), nil
			}

			return okResult(fmt.Sprintf("No release notes available locally.\nChangelog path: %s\n\nCheck the project repository for release notes.", changelogPath)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 25. /rename — rename session
// ---------------------------------------------------------------------------

func cmdRename() *command.Command {
	return &command.Command{
		Name:        "rename",
		Description: "Rename the current conversation session",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if ctx.SessionID == "" {
				return errResult("no active session to rename"), nil
			}

			if len(args) == 0 {
				return errResult("usage: /rename <name>"), nil
			}

			newName := strings.Join(args, " ")

			// Save the name in session metadata
			m, _ := readSessionJSON(ctx.SessionID)
			if m == nil {
				m = map[string]any{}
			}
			m["custom_title"] = newName
			m["agent_name"] = newName
			m["renamed_at"] = time.Now().Format(time.RFC3339)

			if err := writeSessionJSON(ctx.SessionID, m); err != nil {
				return errResult(fmt.Sprintf("failed to save session name: %v", err)), nil
			}
			return okResult(fmt.Sprintf("Session renamed to: %s", newName)), nil
		},
	}
}
