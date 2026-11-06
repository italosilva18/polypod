package constants

// System constants migrated from Claude Code TypeScript source.
// Source: constants/system.ts, constants/xml.ts, constants/figures.ts,
//         constants/outputStyles.ts, constants/product.ts

// ---------------------------------------------------------------------------
// Product identity
// ---------------------------------------------------------------------------

const (
	// ProductName is the user-visible product name.
	ProductName = "Polypod"

	// ProductNameCLI is the CLI binary name.
	ProductNameCLI = "polypod"

	// DefaultCLISyspromptPrefix is the default system prompt prefix.
	DefaultCLISyspromptPrefix = "You are Polypod, an AI-powered coding assistant."

	// AgentSDKPrefix is the prefix for Agent SDK mode.
	AgentSDKPrefix = "You are a Polypod agent, built on the Agent SDK."
)

// ---------------------------------------------------------------------------
// Config file names
// ---------------------------------------------------------------------------

const (
	// ConfigFileName is the name of the project config file (like CLAUDE.md).
	ConfigFileName = "POLYPOD.md"

	// ConfigLocalFileName is the local (git-ignored) config file.
	ConfigLocalFileName = "POLYPOD.local.md"

	// ConfigDirName is the project config directory (like .claude/).
	ConfigDirName = ".polypod"

	// SettingsFileName is the settings JSON file within the config directory.
	SettingsFileName = "settings.json"

	// SkillsDirName is the skills subdirectory within the config directory.
	SkillsDirName = "skills"

	// CommandsDirName is the legacy commands subdirectory.
	CommandsDirName = "commands"

	// AgentsDirName is the agents subdirectory.
	AgentsDirName = "agents"

	// SkillFileName is the required filename for directory-based skills.
	SkillFileName = "SKILL.md"
)

// ---------------------------------------------------------------------------
// XML tag names — used to mark metadata in messages
// ---------------------------------------------------------------------------

const (
	// Command metadata
	XMLCommandName    = "command-name"
	XMLCommandMessage = "command-message"
	XMLCommandArgs    = "command-args"

	// Terminal/bash output
	XMLBashInput          = "bash-input"
	XMLBashStdout         = "bash-stdout"
	XMLBashStderr         = "bash-stderr"
	XMLLocalCommandStdout = "local-command-stdout"
	XMLLocalCommandStderr = "local-command-stderr"
	XMLLocalCommandCaveat = "local-command-caveat"

	// Tick (keep-alive)
	XMLTick = "tick"

	// Task notifications
	XMLTaskNotification = "task-notification"
	XMLTaskID           = "task-id"
	XMLToolUseID        = "tool-use-id"
	XMLTaskType         = "task-type"
	XMLOutputFile       = "output-file"
	XMLStatus           = "status"
	XMLSummary          = "summary"
	XMLReason           = "reason"
	XMLWorktree         = "worktree"
	XMLWorktreePath     = "worktreePath"
	XMLWorktreeBranch   = "worktreeBranch"

	// Ultraplan mode
	XMLUltraplan = "ultraplan"

	// Remote review
	XMLRemoteReview         = "remote-review"
	XMLRemoteReviewProgress = "remote-review-progress"

	// Inter-agent communication
	XMLTeammateMessage     = "teammate-message"
	XMLChannelMessage      = "channel-message"
	XMLChannel             = "channel"
	XMLCrossSessionMessage = "cross-session-message"

	// Fork
	XMLForkBoilerplate  = "fork-boilerplate"
	ForkDirectivePrefix = "Your directive: "
)

// TerminalOutputTags are all tags that indicate a message is terminal output.
var TerminalOutputTags = []string{
	XMLBashInput, XMLBashStdout, XMLBashStderr,
	XMLLocalCommandStdout, XMLLocalCommandStderr, XMLLocalCommandCaveat,
}

// CommonHelpArgs are common argument patterns for slash commands requesting help.
var CommonHelpArgs = []string{"help", "-h", "--help"}

// CommonInfoArgs are common argument patterns for slash commands requesting info.
var CommonInfoArgs = []string{
	"list", "show", "display", "current", "view", "get", "check",
	"describe", "print", "version", "about", "status", "?",
}

// ---------------------------------------------------------------------------
// Figures — Unicode symbols used for UI display
// ---------------------------------------------------------------------------

const (
	FigBlackCircle      = "●"
	FigBulletOperator   = "∙"
	FigTeardropAsterisk = "✻"
	FigUpArrow          = "↑"
	FigDownArrow        = "↓"
	FigLightningBolt    = "↯"
	FigEffortLow        = "○"
	FigEffortMedium     = "◐"
	FigEffortHigh       = "●"
	FigEffortMax        = "◉"
	FigPlayIcon         = "▶"
	FigPauseIcon        = "⏸"
	FigRefreshArrow     = "↻"
	FigChannelArrow     = "←"
	FigInjectedArrow    = "→"
	FigForkGlyph        = "⑂"
	FigDiamondOpen      = "◇"
	FigDiamondFilled    = "◆"
	FigReferenceMark    = "※"
	FigFlagIcon         = "⚑"
	FigBlockquoteBar    = "▎"
	FigHeavyHorizontal  = "━"
	FigBridgeReady      = "·✔︎·"
	FigBridgeFailed     = "×"
)

// BridgeSpinnerFrames are the animation frames for bridge spinner.
var BridgeSpinnerFrames = []string{"·|·", "·/·", "·—·", "·\\·"}

// ---------------------------------------------------------------------------
// Output styles
// ---------------------------------------------------------------------------

const (
	// DefaultOutputStyleName is the name of the default output style.
	DefaultOutputStyleName = "default"
)

// OutputStyleConfig defines a named output style configuration.
type OutputStyleConfig struct {
	Name                   string
	Description            string
	Prompt                 string
	Source                 string // "built-in", "plugin", or setting source
	KeepCodingInstructions bool
	ForceForPlugin         bool
}
