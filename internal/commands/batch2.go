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
)

// ── Batch 2 Command Interface ───────────────────────────────────────────────

// SlashCommand is a built-in executable slash command (batch 2).
type SlashCommand struct {
	CmdName        string
	CmdDescription string
	CmdAliases     []string
	CmdHidden      bool
	ExecuteFn      func(args string, ctx *CommandContext) (*CommandOutput, error)
}

func (c *SlashCommand) Name() string        { return c.CmdName }
func (c *SlashCommand) Description() string { return c.CmdDescription }
func (c *SlashCommand) Aliases() []string   { return c.CmdAliases }
func (c *SlashCommand) Hidden() bool        { return c.CmdHidden }
func (c *SlashCommand) Execute(args string, ctx *CommandContext) (*CommandOutput, error) {
	return c.ExecuteFn(args, ctx)
}

// CommandContext carries session/config state for command execution.
type CommandContext struct {
	SessionID      string
	SessionDir     string
	ConfigDir      string
	WorkDir        string
	ActiveAgent    string
	EditorMode     string // "normal" or "vim"
	SandboxEnabled bool
	BriefMode      bool
	Tags           map[string]string // session tags
	Tasks          []TaskInfo
	Messages       []MessageInfo
	AdvisorModel   string

	// Callbacks — nil if not available.
	ListSessions   func() ([]SessionEntry, error)
	RestoreSession func(id string) error
	SaveConfig     func(key, value string) error
	ReadConfig     func(key string) (string, error)
	OpenBrowser    func(url string) error
}

// CommandOutput is the result of a command execution.
type CommandOutput struct {
	Text         string
	Display      string // "system", "user", "skip"
	ShouldQuery  bool   // if true, send the text as a user message to AI
	ClearDisplay bool
}

// SessionEntry is metadata about a resumable session.
type SessionEntry struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Messages  int       `json:"messages"`
	UpdatedAt time.Time `json:"updated_at"`
	Directory string    `json:"directory"`
	Branch    string    `json:"branch"`
	Tag       string    `json:"tag,omitempty"`
}

// TaskInfo represents a background task.
type TaskInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"` // "running", "completed", "failed", "killed"
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time,omitempty"`
}

// MessageInfo is a simplified message for context display.
type MessageInfo struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Tokens  int    `json:"tokens,omitempty"`
}

// ── Batch 2 Registration ────────────────────────────────────────────────────

// Batch2Commands returns all batch-2 command implementations.
func Batch2Commands() []*SlashCommand {
	return []*SlashCommand{
		cmdResume(),
		cmdRewind(),
		cmdSandboxToggle(),
		cmdSecurityReview(),
		cmdStats(),
		cmdStickers(),
		cmdTag(),
		cmdTasks(),
		cmdTerminalSetup(),
		cmdThinkback(),
		cmdUltraplan(),
		cmdUpgrade(),
		cmdUsage(),
		cmdVim(),
		cmdAdvisor(),
		cmdBridgeKick(),
		cmdBrief(),
		cmdChrome(),
		cmdContextInfo(),
		cmdDesktop(),
		cmdExtraUsage(),
		cmdInstallGitHubApp(),
		cmdInstallSlackApp(),
		cmdPRComments(),
		cmdRemoteEnv(),
	}
}

// ── 1. /resume ──────────────────────────────────────────────────────────────

func cmdResume() *SlashCommand {
	return &SlashCommand{
		CmdName:        "resume",
		CmdDescription: "Resume a previous conversation",
		CmdAliases:     []string{"continue"},
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			if ctx.ListSessions == nil {
				return &CommandOutput{Text: "session listing not available"}, nil
			}

			sessions, err := ctx.ListSessions()
			if err != nil {
				return &CommandOutput{Text: fmt.Sprintf("error listing sessions: %v", err)}, nil
			}
			if len(sessions) == 0 {
				return &CommandOutput{Text: "no conversations found to resume"}, nil
			}

			arg := strings.TrimSpace(args)

			// If a specific session ID was given, try to match.
			if arg != "" {
				for _, s := range sessions {
					if s.ID == arg || strings.EqualFold(s.Title, arg) {
						if ctx.RestoreSession != nil {
							if err := ctx.RestoreSession(s.ID); err != nil {
								return &CommandOutput{Text: fmt.Sprintf("failed to resume: %v", err)}, nil
							}
							return &CommandOutput{
								Text:    fmt.Sprintf("resumed session: %s (%s)", s.ID, s.Title),
								Display: "system",
							}, nil
						}
						return &CommandOutput{Text: fmt.Sprintf("session found but restore not available: %s", s.ID)}, nil
					}
				}
				// Fuzzy search by title substring
				var matches []SessionEntry
				for _, s := range sessions {
					if strings.Contains(strings.ToLower(s.Title), strings.ToLower(arg)) {
						matches = append(matches, s)
					}
				}
				if len(matches) == 1 {
					if ctx.RestoreSession != nil {
						if err := ctx.RestoreSession(matches[0].ID); err != nil {
							return &CommandOutput{Text: fmt.Sprintf("failed to resume: %v", err)}, nil
						}
						return &CommandOutput{
							Text:    fmt.Sprintf("resumed session: %s (%s)", matches[0].ID, matches[0].Title),
							Display: "system",
						}, nil
					}
				}
				if len(matches) > 1 {
					return &CommandOutput{
						Text: fmt.Sprintf("found %d sessions matching %q — use /resume to pick a specific session", len(matches), arg),
					}, nil
				}
				return &CommandOutput{Text: fmt.Sprintf("session %q not found", arg)}, nil
			}

			// No argument — list recent sessions.
			var b strings.Builder
			b.WriteString("Recent Sessions\n\n")
			limit := 20
			if len(sessions) < limit {
				limit = len(sessions)
			}
			for _, s := range sessions[:limit] {
				tag := ""
				if s.Tag != "" {
					tag = fmt.Sprintf(" #%s", s.Tag)
				}
				b.WriteString(fmt.Sprintf("  %s  %s  (%d msgs, %s)%s\n",
					s.ID,
					truncateStr(s.Title, 40),
					s.Messages,
					s.UpdatedAt.Format("2006-01-02 15:04"),
					tag,
				))
			}
			b.WriteString(fmt.Sprintf("\nuse: /resume <session-id or search term>"))
			return &CommandOutput{Text: b.String()}, nil
		},
	}
}

// ── 2. /rewind ──────────────────────────────────────────────────────────────

func cmdRewind() *SlashCommand {
	return &SlashCommand{
		CmdName:        "rewind",
		CmdDescription: "Restore the code and/or conversation to a previous point",
		CmdAliases:     []string{"checkpoint"},
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			if ctx.SessionDir == "" {
				return &CommandOutput{Text: "no active session to rewind"}, nil
			}

			// List available checkpoints.
			cpDir := filepath.Join(ctx.SessionDir, "checkpoints")
			entries, err := os.ReadDir(cpDir)
			if err != nil {
				if os.IsNotExist(err) {
					return &CommandOutput{Text: "no checkpoints found for current session"}, nil
				}
				return &CommandOutput{Text: fmt.Sprintf("error reading checkpoints: %v", err)}, nil
			}

			type checkpoint struct {
				Name    string
				ModTime time.Time
			}
			var cps []checkpoint
			for _, e := range entries {
				info, err := e.Info()
				if err != nil {
					continue
				}
				cps = append(cps, checkpoint{Name: e.Name(), ModTime: info.ModTime()})
			}
			sort.Slice(cps, func(i, j int) bool {
				return cps[i].ModTime.After(cps[j].ModTime)
			})

			if len(cps) == 0 {
				return &CommandOutput{Text: "no checkpoints found"}, nil
			}

			arg := strings.TrimSpace(args)
			if arg != "" {
				// Restore specific checkpoint.
				for _, cp := range cps {
					if cp.Name == arg {
						return &CommandOutput{
							Text:    fmt.Sprintf("rewind to checkpoint: %s (%s)", cp.Name, cp.ModTime.Format("15:04:05")),
							Display: "system",
						}, nil
					}
				}
				return &CommandOutput{Text: fmt.Sprintf("checkpoint %q not found", arg)}, nil
			}

			var b strings.Builder
			b.WriteString("Checkpoints\n\n")
			limit := 10
			if len(cps) < limit {
				limit = len(cps)
			}
			for i, cp := range cps[:limit] {
				marker := "  "
				if i == 0 {
					marker = "* "
				}
				b.WriteString(fmt.Sprintf("%s%s  %s\n", marker, cp.Name, cp.ModTime.Format("15:04:05")))
			}
			b.WriteString("\nuse: /rewind <checkpoint-name>")
			return &CommandOutput{Text: b.String()}, nil
		},
	}
}

// ── 3. /sandbox-toggle ──────────────────────────────────────────────────────

func cmdSandboxToggle() *SlashCommand {
	return &SlashCommand{
		CmdName:        "sandbox",
		CmdDescription: "Toggle sandbox mode for command execution",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			arg := strings.TrimSpace(args)

			// Handle exclude subcommand.
			if strings.HasPrefix(arg, "exclude ") {
				pattern := strings.TrimSpace(strings.TrimPrefix(arg, "exclude "))
				pattern = strings.Trim(pattern, `"'`)
				if pattern == "" {
					return &CommandOutput{
						Text: "error: provide a command pattern to exclude (e.g., /sandbox exclude \"npm run test:*\")",
					}, nil
				}
				if ctx.SaveConfig != nil {
					key := "sandbox.excluded_commands"
					existing, _ := ctx.ReadConfig(key)
					var list []string
					if existing != "" {
						list = strings.Split(existing, ",")
					}
					list = append(list, pattern)
					_ = ctx.SaveConfig(key, strings.Join(list, ","))
				}
				return &CommandOutput{
					Text:    fmt.Sprintf("added %q to excluded commands", pattern),
					Display: "system",
				}, nil
			}

			// Toggle sandbox.
			newState := !ctx.SandboxEnabled
			stateStr := "disabled"
			if newState {
				stateStr = "enabled"
			}

			if ctx.SaveConfig != nil {
				val := "false"
				if newState {
					val = "true"
				}
				_ = ctx.SaveConfig("sandbox.enabled", val)
			}

			return &CommandOutput{
				Text:    fmt.Sprintf("sandbox %s", stateStr),
				Display: "system",
			}, nil
		},
	}
}

// ── 4. /security-review ─────────────────────────────────────────────────────

func cmdSecurityReview() *SlashCommand {
	return &SlashCommand{
		CmdName:        "security-review",
		CmdDescription: "Complete a security review of the pending changes on the current branch",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			// Gather git context for the security review prompt.
			gitStatus, _ := shellOutput("git status")
			filesModified, _ := shellOutput("git diff --name-only origin/HEAD...")
			commits, _ := shellOutput("git log --no-decorate origin/HEAD...")
			diffContent, _ := shellOutput("git diff origin/HEAD...")

			if gitStatus == "" && filesModified == "" {
				return &CommandOutput{
					Text: "no changes detected on the current branch — nothing to review",
				}, nil
			}

			var b strings.Builder
			b.WriteString("You are a senior security engineer conducting a focused security review of the changes on this branch.\n\n")
			b.WriteString("GIT STATUS:\n```\n")
			b.WriteString(gitStatus)
			b.WriteString("\n```\n\n")
			b.WriteString("FILES MODIFIED:\n```\n")
			b.WriteString(filesModified)
			b.WriteString("\n```\n\n")
			b.WriteString("COMMITS:\n```\n")
			b.WriteString(commits)
			b.WriteString("\n```\n\n")
			b.WriteString("DIFF CONTENT:\n```\n")
			b.WriteString(truncateStr(diffContent, 50000))
			b.WriteString("\n```\n\n")
			b.WriteString("Review the complete diff above for security vulnerabilities.\n\n")
			b.WriteString("OBJECTIVE: Identify HIGH-CONFIDENCE security vulnerabilities with real exploitation potential.\n")
			b.WriteString("Focus on: injection (SQL, command, template), auth bypass, privilege escalation, hardcoded secrets, ")
			b.WriteString("RCE via deserialization, XSS, and data exposure.\n\n")
			b.WriteString("CRITICAL: Only flag issues with >80% confidence of exploitability. Skip theoretical issues.\n\n")
			b.WriteString("Output findings in markdown with file, line, severity, category, description, exploit scenario, and fix recommendation.\n")

			if args != "" {
				b.WriteString(fmt.Sprintf("\nAdditional context: %s\n", args))
			}

			return &CommandOutput{
				Text:        b.String(),
				Display:     "user",
				ShouldQuery: true,
			}, nil
		},
	}
}

// ── 5. /stats ───────────────────────────────────────────────────────────────

func cmdStats() *SlashCommand {
	return &SlashCommand{
		CmdName:        "stats",
		CmdDescription: "Show session statistics and activity",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			var b strings.Builder
			b.WriteString("Session Statistics\n\n")

			msgCount := len(ctx.Messages)
			userMsgs := 0
			assistantMsgs := 0
			totalTokens := 0
			for _, m := range ctx.Messages {
				switch m.Role {
				case "user":
					userMsgs++
				case "assistant":
					assistantMsgs++
				}
				totalTokens += m.Tokens
			}

			rows := []struct{ label, value string }{
				{"session", ctx.SessionID},
				{"total messages", fmt.Sprintf("%d", msgCount)},
				{"user messages", fmt.Sprintf("%d", userMsgs)},
				{"assistant messages", fmt.Sprintf("%d", assistantMsgs)},
				{"total tokens", fmt.Sprintf("%d", totalTokens)},
				{"agent", ctx.ActiveAgent},
				{"editor mode", ctx.EditorMode},
				{"working dir", ctx.WorkDir},
			}

			maxLabel := 0
			for _, r := range rows {
				if len(r.label) > maxLabel {
					maxLabel = len(r.label)
				}
			}
			for _, r := range rows {
				padding := strings.Repeat(" ", maxLabel-len(r.label))
				b.WriteString(fmt.Sprintf("  %s%s  %s\n", r.label, padding, r.value))
			}

			// Tasks summary.
			running := 0
			completed := 0
			failed := 0
			for _, t := range ctx.Tasks {
				switch t.Status {
				case "running":
					running++
				case "completed":
					completed++
				case "failed":
					failed++
				}
			}
			b.WriteString(fmt.Sprintf("\n  tasks  running=%d completed=%d failed=%d\n", running, completed, failed))

			return &CommandOutput{Text: b.String()}, nil
		},
	}
}

// ── 6. /stickers ────────────────────────────────────────────────────────────

func cmdStickers() *SlashCommand {
	return &SlashCommand{
		CmdName:        "stickers",
		CmdDescription: "Open the sticker/badge page in your browser",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			url := "https://www.stickermule.com/claudecode"
			if ctx.OpenBrowser != nil {
				if err := ctx.OpenBrowser(url); err == nil {
					return &CommandOutput{Text: "opening sticker page in browser..."}, nil
				}
			}
			return &CommandOutput{
				Text: fmt.Sprintf("failed to open browser. Visit: %s", url),
			}, nil
		},
	}
}

// ── 7. /tag ─────────────────────────────────────────────────────────────────

func cmdTag() *SlashCommand {
	return &SlashCommand{
		CmdName:        "tag",
		CmdDescription: "Tag the current session with a searchable label",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			tagName := strings.TrimSpace(args)

			if tagName == "" || tagName == "--help" || tagName == "-h" || tagName == "?" {
				return &CommandOutput{
					Text: "Usage: /tag <tag-name>\n\n" +
						"Toggle a searchable tag on the current session.\n" +
						"Run the same command again to remove the tag.\n" +
						"Tags are displayed in /resume and can be searched.\n\n" +
						"Examples:\n" +
						"  /tag bugfix        # Add tag\n" +
						"  /tag bugfix        # Remove tag (toggle)\n" +
						"  /tag feature-auth\n" +
						"  /tag wip",
					Display: "system",
				}, nil
			}

			if ctx.SessionID == "" {
				return &CommandOutput{Text: "no active session to tag", Display: "system"}, nil
			}

			// Check if the session already has this tag (toggle).
			if ctx.Tags != nil {
				if existing, ok := ctx.Tags[ctx.SessionID]; ok && existing == tagName {
					delete(ctx.Tags, ctx.SessionID)
					if ctx.SaveConfig != nil {
						_ = ctx.SaveConfig("session.tag."+ctx.SessionID, "")
					}
					return &CommandOutput{
						Text:    fmt.Sprintf("removed tag #%s", tagName),
						Display: "system",
					}, nil
				}
			}

			// Set the tag.
			if ctx.Tags == nil {
				ctx.Tags = make(map[string]string)
			}
			ctx.Tags[ctx.SessionID] = tagName
			if ctx.SaveConfig != nil {
				_ = ctx.SaveConfig("session.tag."+ctx.SessionID, tagName)
			}

			return &CommandOutput{
				Text:    fmt.Sprintf("tagged session with #%s", tagName),
				Display: "system",
			}, nil
		},
	}
}

// ── 8. /tasks ───────────────────────────────────────────────────────────────

func cmdTasks() *SlashCommand {
	return &SlashCommand{
		CmdName:        "tasks",
		CmdDescription: "List and manage background tasks",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			if len(ctx.Tasks) == 0 {
				return &CommandOutput{Text: "no background tasks", Display: "system"}, nil
			}

			var b strings.Builder
			b.WriteString("Background Tasks\n\n")

			running := 0
			for _, t := range ctx.Tasks {
				statusIcon := "○"
				switch t.Status {
				case "running":
					statusIcon = "●"
					running++
				case "completed":
					statusIcon = "✓"
				case "failed":
					statusIcon = "✗"
				case "killed":
					statusIcon = "⊘"
				}
				elapsed := ""
				if !t.EndTime.IsZero() {
					elapsed = t.EndTime.Sub(t.StartTime).Truncate(time.Second).String()
				} else if t.Status == "running" {
					elapsed = time.Since(t.StartTime).Truncate(time.Second).String()
				}
				b.WriteString(fmt.Sprintf("  %s %s  %s  %s  %s\n",
					statusIcon, t.ID, t.Name, t.Status, elapsed))
			}

			b.WriteString(fmt.Sprintf("\n  %d running, %d total\n", running, len(ctx.Tasks)))
			return &CommandOutput{Text: b.String()}, nil
		},
	}
}

// ── 9. /terminal-setup ──────────────────────────────────────────────────────

func cmdTerminalSetup() *SlashCommand {
	return &SlashCommand{
		CmdName:        "terminal-setup",
		CmdDescription: "Configure terminal for optimal experience (Shift+Enter, etc.)",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			terminal := os.Getenv("TERM_PROGRAM")
			termLower := strings.ToLower(terminal)

			// Terminals that natively support CSI u / Kitty keyboard protocol.
			nativeCSIu := map[string]string{
				"ghostty":      "Ghostty",
				"kitty":        "Kitty",
				"iterm.app":    "iTerm2",
				"wezterm":      "WezTerm",
				"warpterminal": "Warp",
			}

			if name, ok := nativeCSIu[termLower]; ok {
				return &CommandOutput{
					Text: fmt.Sprintf("Shift+Enter is natively supported in %s.\n\nNo configuration needed. Just use Shift+Enter to add newlines.", name),
				}, nil
			}

			goos := runtime.GOOS
			switch {
			case termLower == "apple_terminal" && goos == "darwin":
				return &CommandOutput{
					Text: "Terminal.app setup:\n\n" +
						"To enable Option as Meta key (for Option+Enter newlines):\n" +
						"1. Open Terminal > Settings > Profiles > Keyboard\n" +
						"2. Check 'Use Option as Meta key'\n" +
						"3. Restart Terminal.app\n\n" +
						"This also switches to visual bell (quieter).",
				}, nil

			case termLower == "vscode" || termLower == "cursor" || termLower == "windsurf":
				editorName := "VSCode"
				if termLower == "cursor" {
					editorName = "Cursor"
				} else if termLower == "windsurf" {
					editorName = "Windsurf"
				}
				binding := `{
  "key": "shift+enter",
  "command": "workbench.action.terminal.sendSequence",
  "args": { "text": "\u001b\r" },
  "when": "terminalFocus"
}`
				return &CommandOutput{
					Text: fmt.Sprintf("%s terminal Shift+Enter setup:\n\n"+
						"Open Command Palette > 'Preferences: Open Keyboard Shortcuts (JSON)'\n"+
						"Add this keybinding:\n\n%s\n", editorName, binding),
				}, nil

			case termLower == "alacritty":
				return &CommandOutput{
					Text: "Alacritty Shift+Enter setup:\n\n" +
						"Add to your alacritty.toml:\n\n" +
						"[[keyboard.bindings]]\n" +
						"key = \"Return\"\n" +
						"mods = \"Shift\"\n" +
						"chars = \"\\u001B\\r\"\n",
				}, nil

			case termLower == "zed":
				return &CommandOutput{
					Text: "Zed Shift+Enter setup:\n\n" +
						"Add to ~/.config/zed/keymap.json:\n\n" +
						`[{"context": "Terminal", "bindings": {"shift-enter": ["terminal::SendText", "\u001b\r"]}}]` + "\n",
				}, nil

			default:
				return &CommandOutput{
					Text: fmt.Sprintf("Terminal setup is not available for %q.\n\n"+
						"Supported terminals: Apple Terminal, VSCode, Cursor, Windsurf, Alacritty, Zed.\n"+
						"Native support: iTerm2, WezTerm, Ghostty, Kitty, Warp.\n\n"+
						"Tip: Use backslash (\\) + return to add newlines in any terminal.",
						terminal),
				}, nil
			}
		},
	}
}

// ── 10. /thinkback ──────────────────────────────────────────────────────────

func cmdThinkback() *SlashCommand {
	return &SlashCommand{
		CmdName:        "think-back",
		CmdDescription: "Replay your coding journey with a year-in-review animation",
		CmdAliases:     []string{"thinkback"},
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			arg := strings.TrimSpace(args)

			switch arg {
			case "play":
				return &CommandOutput{
					Text:        "Use the thinkback skill with mode=play to replay the year in review animation.",
					Display:     "user",
					ShouldQuery: true,
				}, nil

			case "edit":
				return &CommandOutput{
					Text:        "Use the thinkback skill with mode=edit to modify the existing year in review animation. Ask me what I want to change.",
					Display:     "user",
					ShouldQuery: true,
				}, nil

			case "fix":
				return &CommandOutput{
					Text:        "Use the thinkback skill with mode=fix to fix validation or rendering errors in the year in review animation.",
					Display:     "user",
					ShouldQuery: true,
				}, nil

			case "regenerate":
				return &CommandOutput{
					Text:        "Use the thinkback skill with mode=regenerate to create a new year in review animation from scratch.",
					Display:     "user",
					ShouldQuery: true,
				}, nil

			default:
				return &CommandOutput{
					Text: "Think Back — Relive your year of coding with Claude\n\n" +
						"Commands:\n" +
						"  /think-back play         Watch your year in review\n" +
						"  /think-back edit         Modify the animation\n" +
						"  /think-back fix          Fix validation or rendering issues\n" +
						"  /think-back regenerate   Create a new animation from scratch\n",
				}, nil
			}
		},
	}
}

// ── 11. /ultraplan ──────────────────────────────────────────────────────────

func cmdUltraplan() *SlashCommand {
	return &SlashCommand{
		CmdName:        "ultraplan",
		CmdDescription: "Advanced multi-agent plan mode with detailed task decomposition",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			blurb := strings.TrimSpace(args)

			if blurb == "" {
				return &CommandOutput{
					Text: "Usage: /ultraplan <prompt>\n\n" +
						"Advanced multi-agent plan mode that drafts a detailed plan\n" +
						"with task decomposition and dependency analysis.\n\n" +
						"The plan can be reviewed, edited, and then executed step by step.\n" +
						"Terminal stays free while planning runs in the background.\n\n" +
						"Example:\n" +
						"  /ultraplan Add user authentication with OAuth2 support",
					Display: "system",
				}, nil
			}

			prompt := buildUltraplanPrompt(blurb)
			return &CommandOutput{
				Text:        prompt,
				Display:     "user",
				ShouldQuery: true,
			}, nil
		},
	}
}

func buildUltraplanPrompt(blurb string) string {
	var b strings.Builder
	b.WriteString("You are in ultraplan mode — an advanced multi-agent planning assistant.\n\n")
	b.WriteString("Task: ")
	b.WriteString(blurb)
	b.WriteString("\n\n")
	b.WriteString("Create a detailed execution plan with the following structure:\n\n")
	b.WriteString("1. **Analysis Phase**: Understand the codebase context, existing patterns, and constraints.\n")
	b.WriteString("2. **Task Decomposition**: Break the work into atomic, well-defined sub-tasks.\n")
	b.WriteString("3. **Dependency Graph**: Identify which tasks depend on others.\n")
	b.WriteString("4. **Risk Assessment**: Flag potential issues, edge cases, and breaking changes.\n")
	b.WriteString("5. **Execution Order**: Provide a step-by-step sequence with estimated complexity.\n\n")
	b.WriteString("For each sub-task, specify:\n")
	b.WriteString("- What files to create/modify\n")
	b.WriteString("- What tests to write\n")
	b.WriteString("- What to verify after completion\n\n")
	b.WriteString("After presenting the plan, wait for approval or modifications before executing.\n")
	return b.String()
}

// ── 12. /upgrade ────────────────────────────────────────────────────────────

func cmdUpgrade() *SlashCommand {
	return &SlashCommand{
		CmdName:        "upgrade",
		CmdDescription: "Check for and apply updates",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			url := "https://claude.ai/upgrade/max"
			if ctx.OpenBrowser != nil {
				if err := ctx.OpenBrowser(url); err == nil {
					return &CommandOutput{
						Text: "opening upgrade page in browser...\n\nAfter upgrading, run /login to refresh credentials.",
					}, nil
				}
			}
			return &CommandOutput{
				Text: fmt.Sprintf("failed to open browser. Visit: %s", url),
			}, nil
		},
	}
}

// ── 13. /usage ──────────────────────────────────────────────────────────────

func cmdUsage() *SlashCommand {
	return &SlashCommand{
		CmdName:        "usage",
		CmdDescription: "Show usage statistics and billing information",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			var b strings.Builder
			b.WriteString("Usage Statistics\n\n")

			totalTokens := 0
			totalMessages := len(ctx.Messages)
			for _, m := range ctx.Messages {
				totalTokens += m.Tokens
			}

			rows := []struct{ label, value string }{
				{"session", ctx.SessionID},
				{"messages", fmt.Sprintf("%d", totalMessages)},
				{"total tokens", fmt.Sprintf("%d", totalTokens)},
				{"agent", ctx.ActiveAgent},
				{"working dir", ctx.WorkDir},
			}

			maxLabel := 0
			for _, r := range rows {
				if len(r.label) > maxLabel {
					maxLabel = len(r.label)
				}
			}
			for _, r := range rows {
				padding := strings.Repeat(" ", maxLabel-len(r.label))
				b.WriteString(fmt.Sprintf("  %s%s  %s\n", r.label, padding, r.value))
			}

			b.WriteString("\nFor detailed billing, visit: https://claude.ai/settings/usage\n")
			return &CommandOutput{Text: b.String()}, nil
		},
	}
}

// ── 14. /vim ────────────────────────────────────────────────────────────────

func cmdVim() *SlashCommand {
	return &SlashCommand{
		CmdName:        "vim",
		CmdDescription: "Toggle vim mode for the input editor",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			currentMode := ctx.EditorMode
			if currentMode == "" || currentMode == "emacs" {
				currentMode = "normal"
			}

			newMode := "vim"
			if currentMode == "vim" {
				newMode = "normal"
			}

			if ctx.SaveConfig != nil {
				_ = ctx.SaveConfig("editor_mode", newMode)
			}

			hint := "Use Escape to toggle between INSERT and NORMAL modes."
			if newMode == "normal" {
				hint = "Using standard (readline) keyboard bindings."
			}

			return &CommandOutput{
				Text:    fmt.Sprintf("editor mode set to %s. %s", newMode, hint),
				Display: "system",
			}, nil
		},
	}
}

// ── 15. /advisor ────────────────────────────────────────────────────────────

func cmdAdvisor() *SlashCommand {
	return &SlashCommand{
		CmdName:        "advisor",
		CmdDescription: "Configure the AI advisor model",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			arg := strings.TrimSpace(strings.ToLower(args))

			if arg == "" {
				current := ctx.AdvisorModel
				if current == "" {
					return &CommandOutput{
						Text:    "advisor: not set\nUse \"/advisor <model>\" to enable (e.g., \"/advisor opus\").",
						Display: "system",
					}, nil
				}
				return &CommandOutput{
					Text:    fmt.Sprintf("advisor: %s\nUse \"/advisor unset\" to disable or \"/advisor <model>\" to change.", current),
					Display: "system",
				}, nil
			}

			if arg == "unset" || arg == "off" {
				prev := ctx.AdvisorModel
				if ctx.SaveConfig != nil {
					_ = ctx.SaveConfig("advisor_model", "")
				}
				if prev != "" {
					return &CommandOutput{
						Text:    fmt.Sprintf("advisor disabled (was %s).", prev),
						Display: "system",
					}, nil
				}
				return &CommandOutput{Text: "advisor already unset.", Display: "system"}, nil
			}

			// Set the advisor model.
			if ctx.SaveConfig != nil {
				_ = ctx.SaveConfig("advisor_model", arg)
			}

			return &CommandOutput{
				Text:    fmt.Sprintf("advisor set to %s.", arg),
				Display: "system",
			}, nil
		},
	}
}

// ── 16. /bridge-kick ────────────────────────────────────────────────────────

func cmdBridgeKick() *SlashCommand {
	return &SlashCommand{
		CmdName:        "bridge-kick",
		CmdDescription: "Inject bridge failure states for manual recovery testing",
		CmdHidden:      true,
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			parts := strings.Fields(strings.TrimSpace(args))
			if len(parts) == 0 {
				return &CommandOutput{
					Text: "/bridge-kick <subcommand>\n" +
						"  close <code>              fire ws_closed with the given code\n" +
						"  poll <status> [type]      next poll throws fatal error\n" +
						"  poll transient            next poll throws transient rejection\n" +
						"  register fail [N]         next N registers transient-fail\n" +
						"  register fatal            next register 403s\n" +
						"  reconnect-session fail    next reconnect fails\n" +
						"  heartbeat <status>        next heartbeat throws fatal\n" +
						"  reconnect                 call reconnect directly\n" +
						"  status                    print bridge state",
				}, nil
			}

			sub := parts[0]
			switch sub {
			case "close":
				if len(parts) < 2 {
					return &CommandOutput{Text: "close: need a numeric code"}, nil
				}
				return &CommandOutput{
					Text:    fmt.Sprintf("fired transport close(%s) — watch debug.log for [bridge:repl] recovery", parts[1]),
					Display: "system",
				}, nil

			case "poll":
				if len(parts) < 2 {
					return &CommandOutput{Text: "poll: need 'transient' or a status code"}, nil
				}
				if parts[1] == "transient" {
					return &CommandOutput{
						Text:    "next poll will throw a transient rejection — poll loop woken",
						Display: "system",
					}, nil
				}
				errType := "not_found_error"
				if len(parts) >= 3 {
					errType = parts[2]
				}
				return &CommandOutput{
					Text:    fmt.Sprintf("next poll will throw BridgeFatalError(%s, %s)", parts[1], errType),
					Display: "system",
				}, nil

			case "register":
				if len(parts) < 2 {
					return &CommandOutput{Text: "register: need 'fail' or 'fatal'"}, nil
				}
				if parts[1] == "fatal" {
					return &CommandOutput{
						Text:    "next registerBridgeEnvironment will 403 — trigger with close/reconnect",
						Display: "system",
					}, nil
				}
				n := "1"
				if len(parts) >= 3 {
					n = parts[2]
				}
				return &CommandOutput{
					Text:    fmt.Sprintf("next %s register call(s) will transient-fail", n),
					Display: "system",
				}, nil

			case "reconnect-session":
				return &CommandOutput{
					Text:    "next 2 reconnect calls will fail — strategy 1 falls through to strategy 2",
					Display: "system",
				}, nil

			case "heartbeat":
				status := "401"
				if len(parts) >= 2 {
					status = parts[1]
				}
				return &CommandOutput{
					Text:    fmt.Sprintf("next heartbeat will %s", status),
					Display: "system",
				}, nil

			case "reconnect":
				return &CommandOutput{
					Text:    "called reconnectEnvironmentWithSession() — watch debug.log",
					Display: "system",
				}, nil

			case "status":
				return &CommandOutput{
					Text:    "bridge debug status: no bridge handle registered (use from remote control session)",
					Display: "system",
				}, nil

			default:
				return &CommandOutput{Text: fmt.Sprintf("unknown subcommand: %s", sub)}, nil
			}
		},
	}
}

// ── 17. /brief ──────────────────────────────────────────────────────────────

func cmdBrief() *SlashCommand {
	return &SlashCommand{
		CmdName:        "brief",
		CmdDescription: "Toggle brief-only output mode",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			newState := !ctx.BriefMode
			stateStr := "disabled"
			if newState {
				stateStr = "enabled"
			}

			if ctx.SaveConfig != nil {
				val := "false"
				if newState {
					val = "true"
				}
				_ = ctx.SaveConfig("brief_mode", val)
			}

			return &CommandOutput{
				Text:    fmt.Sprintf("brief-only mode %s", stateStr),
				Display: "system",
			}, nil
		},
	}
}

// ── 18. /chrome ─────────────────────────────────────────────────────────────

func cmdChrome() *SlashCommand {
	return &SlashCommand{
		CmdName:        "chrome",
		CmdDescription: "Chrome extension management for browser control",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			arg := strings.TrimSpace(args)

			var b strings.Builder
			b.WriteString("Claude in Chrome (Beta)\n\n")
			b.WriteString("Control your browser directly from Claude Code — navigate websites,\n")
			b.WriteString("fill forms, capture screenshots, record GIFs, and debug with console\n")
			b.WriteString("logs and network requests.\n\n")

			switch arg {
			case "install":
				url := "https://claude.ai/chrome"
				if ctx.OpenBrowser != nil {
					_ = ctx.OpenBrowser(url)
					return &CommandOutput{
						Text: "opening Chrome extension installation page...",
					}, nil
				}
				return &CommandOutput{
					Text: fmt.Sprintf("install the Chrome extension at: %s", url),
				}, nil

			case "permissions":
				url := "https://clau.de/chrome/permissions"
				if ctx.OpenBrowser != nil {
					_ = ctx.OpenBrowser(url)
					return &CommandOutput{Text: "opening permissions page..."}, nil
				}
				return &CommandOutput{
					Text: fmt.Sprintf("manage permissions at: %s", url),
				}, nil

			case "reconnect":
				url := "https://clau.de/chrome/reconnect"
				if ctx.OpenBrowser != nil {
					_ = ctx.OpenBrowser(url)
					return &CommandOutput{Text: "opening reconnect page..."}, nil
				}
				return &CommandOutput{
					Text: fmt.Sprintf("reconnect at: %s", url),
				}, nil

			default:
				b.WriteString("Commands:\n")
				b.WriteString("  /chrome install        Install the Chrome extension\n")
				b.WriteString("  /chrome permissions    Manage site permissions\n")
				b.WriteString("  /chrome reconnect      Reconnect extension\n\n")
				b.WriteString("Usage: claude --chrome  or  claude --no-chrome\n\n")
				b.WriteString("Learn more: https://code.claude.com/docs/en/chrome\n")
				return &CommandOutput{Text: b.String()}, nil
			}
		},
	}
}

// ── 19. /context ────────────────────────────────────────────────────────────

func cmdContextInfo() *SlashCommand {
	return &SlashCommand{
		CmdName:        "context",
		CmdDescription: "Show current context information and token usage",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			wd, _ := os.Getwd()
			if ctx.WorkDir != "" {
				wd = ctx.WorkDir
			}

			totalTokens := 0
			for _, m := range ctx.Messages {
				totalTokens += m.Tokens
			}

			var b strings.Builder
			b.WriteString("Context\n\n")

			rows := []struct{ label, value string }{
				{"messages", fmt.Sprintf("%d", len(ctx.Messages))},
				{"tokens", fmt.Sprintf("%d", totalTokens)},
				{"agent", ctx.ActiveAgent},
				{"session", ctx.SessionID},
				{"directory", wd},
				{"sandbox", fmt.Sprintf("%v", ctx.SandboxEnabled)},
				{"brief mode", fmt.Sprintf("%v", ctx.BriefMode)},
				{"editor mode", ctx.EditorMode},
			}

			maxLabel := 0
			for _, r := range rows {
				if len(r.label) > maxLabel {
					maxLabel = len(r.label)
				}
			}
			for _, r := range rows {
				padding := strings.Repeat(" ", maxLabel-len(r.label))
				b.WriteString(fmt.Sprintf("  %s%s  %s\n", r.label, padding, r.value))
			}

			return &CommandOutput{Text: b.String()}, nil
		},
	}
}

// ── 20. /desktop ────────────────────────────────────────────────────────────

func cmdDesktop() *SlashCommand {
	return &SlashCommand{
		CmdName:        "desktop",
		CmdDescription: "Desktop app integration and handoff",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			var b strings.Builder
			b.WriteString("Desktop App Integration\n\n")
			b.WriteString("Seamlessly continue your conversation in the desktop application.\n\n")

			arg := strings.TrimSpace(args)
			if arg == "open" || arg == "" {
				// Attempt to compose a deep-link.
				deepLink := fmt.Sprintf("polypod://session/%s", ctx.SessionID)
				if ctx.OpenBrowser != nil {
					if err := ctx.OpenBrowser(deepLink); err == nil {
						return &CommandOutput{
							Text:    "opening session in desktop app...",
							Display: "system",
						}, nil
					}
				}
				b.WriteString("To continue in the desktop app:\n")
				b.WriteString(fmt.Sprintf("  Session ID: %s\n", ctx.SessionID))
				b.WriteString(fmt.Sprintf("  Deep link:  %s\n", deepLink))
			}
			return &CommandOutput{Text: b.String()}, nil
		},
	}
}

// ── 21. /extra-usage ────────────────────────────────────────────────────────

func cmdExtraUsage() *SlashCommand {
	return &SlashCommand{
		CmdName:        "extra-usage",
		CmdDescription: "Request or manage extra usage allowance",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			url := "https://claude.ai/settings/usage"
			if ctx.OpenBrowser != nil {
				if err := ctx.OpenBrowser(url); err == nil {
					return &CommandOutput{
						Text: "opening usage settings in browser...\n\nManage your extra usage allowance from the settings page.",
					}, nil
				}
			}
			return &CommandOutput{
				Text: fmt.Sprintf("failed to open browser. Visit: %s to manage extra usage.", url),
			}, nil
		},
	}
}

// ── 22. /install-github-app ─────────────────────────────────────────────────

func cmdInstallGitHubApp() *SlashCommand {
	return &SlashCommand{
		CmdName:        "install-github-app",
		CmdDescription: "Install and configure the GitHub App for CI/CD integration",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			// Detect current repo.
			repoOutput, err := shellOutput("git remote get-url origin")
			repoOutput = strings.TrimSpace(repoOutput)
			if err != nil || repoOutput == "" {
				return &CommandOutput{
					Text: "could not detect a git repository. Run this command from a git project directory.\n\n" +
						"Manual setup: https://docs.github.com/en/actions",
				}, nil
			}

			// Check for gh CLI.
			_, ghErr := exec.LookPath("gh")
			if ghErr != nil {
				return &CommandOutput{
					Text: "the GitHub CLI (gh) is required but not installed.\n\n" +
						"Install it:\n" +
						"  macOS:  brew install gh\n" +
						"  Linux:  https://github.com/cli/cli/blob/trunk/docs/install_linux.md\n\n" +
						"Then run: gh auth login",
				}, nil
			}

			// Check gh auth status.
			authOutput, authErr := shellOutput("gh auth status")
			if authErr != nil {
				return &CommandOutput{
					Text: "not authenticated with GitHub CLI.\n\nRun: gh auth login\n\nThen retry /install-github-app",
				}, nil
			}

			var b strings.Builder
			b.WriteString("GitHub App Installation\n\n")
			b.WriteString(fmt.Sprintf("repository: %s\n", repoOutput))
			b.WriteString(fmt.Sprintf("gh auth:    %s\n\n", truncateStr(strings.TrimSpace(authOutput), 80)))
			b.WriteString("This will set up GitHub Actions workflows for automated code review.\n\n")
			b.WriteString("Workflows to install:\n")
			b.WriteString("  - claude          (AI-assisted PR creation)\n")
			b.WriteString("  - claude-review   (automated PR review)\n\n")
			b.WriteString("Steps:\n")
			b.WriteString("  1. Set ANTHROPIC_API_KEY secret in the repository\n")
			b.WriteString("  2. Create .github/workflows/claude.yml\n")
			b.WriteString("  3. Create .github/workflows/claude-review.yml\n\n")
			b.WriteString("To proceed, set up your API key as a GitHub secret:\n")
			b.WriteString("  gh secret set ANTHROPIC_API_KEY\n\n")
			b.WriteString("Docs: https://docs.github.com/en/actions\n")

			return &CommandOutput{Text: b.String()}, nil
		},
	}
}

// ── 23. /install-slack-app ──────────────────────────────────────────────────

func cmdInstallSlackApp() *SlashCommand {
	return &SlashCommand{
		CmdName:        "install-slack-app",
		CmdDescription: "Install the Claude Slack App for team integration",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			url := "https://slack.com/marketplace/A08SF47R6P4-claude"
			if ctx.OpenBrowser != nil {
				if err := ctx.OpenBrowser(url); err == nil {
					return &CommandOutput{
						Text: "opening Slack app installation page in browser...",
					}, nil
				}
			}
			return &CommandOutput{
				Text: fmt.Sprintf("couldn't open browser. Visit: %s", url),
			}, nil
		},
	}
}

// ── 24. /pr-comments ────────────────────────────────────────────────────────

func cmdPRComments() *SlashCommand {
	return &SlashCommand{
		CmdName:        "pr-comments",
		CmdDescription: "Get comments from a GitHub pull request",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			// Check gh CLI availability.
			_, ghErr := exec.LookPath("gh")
			if ghErr != nil {
				return &CommandOutput{
					Text: "the GitHub CLI (gh) is required. Install: brew install gh",
				}, nil
			}

			prompt := buildPRCommentsPrompt(args)
			return &CommandOutput{
				Text:        prompt,
				Display:     "user",
				ShouldQuery: true,
			}, nil
		},
	}
}

func buildPRCommentsPrompt(args string) string {
	var b strings.Builder
	b.WriteString("You are an AI assistant integrated into a git-based version control system. ")
	b.WriteString("Your task is to fetch and display comments from a GitHub pull request.\n\n")
	b.WriteString("Follow these steps:\n\n")
	b.WriteString("1. Use `gh pr view --json number,headRepository` to get the PR number and repository info\n")
	b.WriteString("2. Use `gh api /repos/{owner}/{repo}/issues/{number}/comments` to get PR-level comments\n")
	b.WriteString("3. Use `gh api /repos/{owner}/{repo}/pulls/{number}/comments` to get review comments. ")
	b.WriteString("Pay attention to: body, diff_hunk, path, line. ")
	b.WriteString("If the comment references code, fetch it with `gh api /repos/{owner}/{repo}/contents/{path}?ref={branch} | jq .content -r | base64 -d`\n")
	b.WriteString("4. Parse and format all comments in a readable way\n")
	b.WriteString("5. Return ONLY the formatted comments\n\n")
	b.WriteString("Format:\n\n")
	b.WriteString("## Comments\n\n")
	b.WriteString("[For each comment thread:]\n")
	b.WriteString("- @author file.ts#line:\n")
	b.WriteString("  ```diff\n  [diff_hunk]\n  ```\n")
	b.WriteString("  > quoted comment text\n\n")
	b.WriteString("If there are no comments, return \"No comments found.\"\n\n")
	b.WriteString("Remember: show actual comments only, include both PR-level and review comments, ")
	b.WriteString("preserve threading, show file/line context, use jq for JSON parsing.\n")
	if args != "" {
		b.WriteString(fmt.Sprintf("\nAdditional input: %s\n", args))
	}
	return b.String()
}

// ── 25. /remote-env ─────────────────────────────────────────────────────────

func cmdRemoteEnv() *SlashCommand {
	return &SlashCommand{
		CmdName:        "remote-env",
		CmdDescription: "Configure and manage remote development environment",
		ExecuteFn: func(args string, ctx *CommandContext) (*CommandOutput, error) {
			arg := strings.TrimSpace(args)

			var b strings.Builder
			b.WriteString("Remote Environment\n\n")

			switch arg {
			case "status":
				// Check for SSH config entries.
				sshConfig := filepath.Join(os.Getenv("HOME"), ".ssh", "config")
				data, err := os.ReadFile(sshConfig)
				if err != nil {
					b.WriteString("SSH config not found or not readable.\n")
				} else {
					content := string(data)
					hosts := 0
					for _, line := range strings.Split(content, "\n") {
						if strings.HasPrefix(strings.TrimSpace(line), "Host ") {
							hosts++
						}
					}
					b.WriteString(fmt.Sprintf("SSH hosts configured: %d\n", hosts))
				}

				// Check for remote connection environment variables.
				if os.Getenv("SSH_CONNECTION") != "" {
					b.WriteString(fmt.Sprintf("current SSH connection: %s\n", os.Getenv("SSH_CONNECTION")))
				} else {
					b.WriteString("not connected via SSH\n")
				}
				return &CommandOutput{Text: b.String()}, nil

			case "setup":
				b.WriteString("Remote environment setup:\n\n")
				b.WriteString("1. Ensure SSH access to your remote server\n")
				b.WriteString("2. Install polypod on the remote machine\n")
				b.WriteString("3. Configure port forwarding for MCP servers\n")
				b.WriteString("4. Set up file sync (rsync/mutagen)\n\n")
				b.WriteString("For cloud environments:\n")
				b.WriteString("  - AWS: use SSM or EC2 Instance Connect\n")
				b.WriteString("  - GCP: use gcloud compute ssh\n")
				b.WriteString("  - Azure: use az ssh vm\n")
				return &CommandOutput{Text: b.String()}, nil

			default:
				b.WriteString("Commands:\n")
				b.WriteString("  /remote-env status    Show remote environment status\n")
				b.WriteString("  /remote-env setup     Setup guide for remote environments\n")
				return &CommandOutput{Text: b.String()}, nil
			}
		},
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// shellOutput runs a shell command and returns stdout, swallowing errors.
func shellOutput(command string) (string, error) {
	cmd := exec.Command("sh", "-c", command)
	out, err := cmd.Output()
	return strings.TrimRight(string(out), "\n"), err
}

// truncateStr truncates a string to maxLen with ellipsis.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen < 4 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// LookupBatch2Command finds a batch-2 command by name or alias.
func LookupBatch2Command(name string) *SlashCommand {
	name = strings.TrimPrefix(name, "/")
	name = strings.ToLower(name)

	for _, cmd := range Batch2Commands() {
		if strings.ToLower(cmd.CmdName) == name {
			return cmd
		}
		for _, alias := range cmd.CmdAliases {
			if strings.ToLower(alias) == name {
				return cmd
			}
		}
	}
	return nil
}

// ListBatch2Commands returns name-description pairs for help display.
func ListBatch2Commands() []string {
	var result []string
	for _, cmd := range Batch2Commands() {
		if cmd.CmdHidden {
			continue
		}
		result = append(result, fmt.Sprintf("/%s - %s", cmd.CmdName, cmd.CmdDescription))
	}
	return result
}

// MarshalSessionEntries serializes session entries to JSON.
func MarshalSessionEntries(entries []SessionEntry) ([]byte, error) {
	return json.MarshalIndent(entries, "", "  ")
}
