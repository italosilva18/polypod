package hooks

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"time"
)

// ---------------------------------------------------------------------------
// NotificationHook — fires desktop/terminal notifications on specific agent
// lifecycle events (task completion, errors, long-running tools).
// ---------------------------------------------------------------------------

// NotificationHookConfig controls which events trigger notifications and how
// they are delivered.
type NotificationHookConfig struct {
	// OnTaskComplete sends a notification when an agent finishes a task.
	OnTaskComplete bool

	// OnError sends a notification when an agent encounters an error.
	OnError bool

	// OnLongRunning sends a notification when a tool execution exceeds
	// LongRunningThreshold.
	OnLongRunning bool

	// LongRunningThreshold is the minimum tool duration that triggers a
	// long-running notification. Defaults to 30s when zero.
	LongRunningThreshold time.Duration

	// NotifyCommand is the program used to send notifications on Linux.
	// Defaults to "notify-send" when empty. On macOS, osascript is used
	// instead. If the platform is neither Linux nor macOS, a BEL character
	// is printed to stdout as a fallback.
	NotifyCommand string
}

// defaults fills in zero-value fields with sensible defaults.
func (c *NotificationHookConfig) defaults() {
	if c.LongRunningThreshold == 0 {
		c.LongRunningThreshold = 30 * time.Second
	}
	if c.NotifyCommand == "" {
		c.NotifyCommand = "notify-send"
	}
}

// NotificationHook implements AgentHookHandler and sends desktop/terminal
// notifications on configured agent events.
type NotificationHook struct {
	cfg    NotificationHookConfig
	logger *slog.Logger
}

// Compile-time interface check.
var _ AgentHookHandler = (*NotificationHook)(nil)

// NewNotificationHook creates a NotificationHook with the given configuration
// and logger. Nil logger is replaced with slog.Default().
func NewNotificationHook(cfg NotificationHookConfig, logger *slog.Logger) *NotificationHook {
	cfg.defaults()
	if logger == nil {
		logger = slog.Default()
	}
	return &NotificationHook{
		cfg:    cfg,
		logger: logger,
	}
}

// OnAgentEvent is called by the AgentHookRegistry for every agent lifecycle
// event. It inspects the event type and, if the corresponding config flag is
// enabled, sends a desktop notification.
func (nh *NotificationHook) OnAgentEvent(ctx context.Context, event AgentEvent) {
	switch event.Type {
	case AgentEventFinish:
		if !nh.cfg.OnTaskComplete {
			return
		}
		dur := time.Duration(event.DurationMs) * time.Millisecond
		title := fmt.Sprintf("Task complete: %s", event.AgentName)
		body := fmt.Sprintf("Task %s finished in %s.", event.TaskID, dur.Round(time.Millisecond))
		nh.sendNotification(title, body)

	case AgentEventError:
		if !nh.cfg.OnError {
			return
		}
		errMsg := "<unknown error>"
		if event.Error != nil {
			errMsg = event.Error.Error()
		}
		title := fmt.Sprintf("Error: %s", event.AgentName)
		body := fmt.Sprintf("Task %s encountered an error: %s", event.TaskID, errMsg)
		nh.sendNotification(title, body)

	case AgentEventToolFinish:
		if !nh.cfg.OnLongRunning {
			return
		}
		dur := time.Duration(event.DurationMs) * time.Millisecond
		if dur < nh.cfg.LongRunningThreshold {
			return
		}
		title := fmt.Sprintf("Long-running tool: %s", event.ToolName)
		body := fmt.Sprintf("Tool %s on agent %s took %s (threshold %s).",
			event.ToolName, event.AgentName, dur.Round(time.Millisecond),
			nh.cfg.LongRunningThreshold)
		nh.sendNotification(title, body)
	}
}

// sendNotification delivers a desktop notification. On Linux it shells out to
// the configured NotifyCommand (default "notify-send"). On macOS it uses
// osascript. On other platforms it prints a BEL character to stdout.
func (nh *NotificationHook) sendNotification(title, body string) {
	nh.logger.Debug("sending notification",
		"title", title,
		"body", body,
		"platform", runtime.GOOS,
	)

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command(nh.cfg.NotifyCommand, title, body)

	case "darwin":
		script := fmt.Sprintf(
			`display notification %q with title %q`,
			body, title,
		)
		cmd = exec.Command("osascript", "-e", script)

	default:
		// BEL fallback: ring the terminal bell and log the message.
		fmt.Print("\a")
		nh.logger.Info("notification (BEL fallback)",
			"title", title,
			"body", body,
		)
		return
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		nh.logger.Warn("notification command failed",
			"error", err,
			"output", string(out),
			"title", title,
		)
	}
}

// ---------------------------------------------------------------------------
// Registration helper
// ---------------------------------------------------------------------------

// RegisterNotificationHook creates a NotificationHook from cfg and registers
// it with the global AgentHookRegistry under the name "notification".
func RegisterNotificationHook(cfg NotificationHookConfig, logger *slog.Logger) {
	nh := NewNotificationHook(cfg, logger)
	GetAgentHookRegistry().Register("notification", nh)
}
