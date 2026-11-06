package observability

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Notification represents a desktop notification request.
type Notification struct {
	Title   string
	Message string
	// Icon is one of "info", "warning", "error", "success" (or a free-form
	// platform-specific value).
	Icon   string
	Sound  bool
	Urgent bool
}

// Notifier sends desktop notifications. Implementations are platform
// specific and best-effort: a missing backend should be reported via
// IsAvailable rather than panicking.
type Notifier interface {
	Notify(n Notification) error
	IsAvailable() bool
}

// NewNotifier returns the most capable notifier available on the current
// platform, falling back to a stderr logger when no native backend can
// be found.
func NewNotifier() Notifier {
	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		l := &LinuxNotifier{}
		if l.IsAvailable() {
			return l
		}
	case "darwin":
		m := &MacNotifier{}
		if m.IsAvailable() {
			return m
		}
	case "windows":
		w := &WindowsNotifier{}
		if w.IsAvailable() {
			return w
		}
	}
	return &FallbackNotifier{}
}

// ---------------------------------------------------------------------------
// Linux: notify-send
// ---------------------------------------------------------------------------

// LinuxNotifier dispatches notifications via notify-send (libnotify).
type LinuxNotifier struct{}

// Notify sends the notification through notify-send.
func (l *LinuxNotifier) Notify(n Notification) error {
	args := make([]string, 0, 8)
	if n.Urgent {
		args = append(args, "-u", "critical")
	} else {
		args = append(args, "-u", "normal")
	}
	if n.Icon != "" {
		args = append(args, "-i", iconForType(n.Icon))
	}
	args = append(args, n.Title, n.Message)
	return exec.Command("notify-send", args...).Run()
}

// IsAvailable reports whether notify-send is on PATH.
func (l *LinuxNotifier) IsAvailable() bool {
	_, err := exec.LookPath("notify-send")
	return err == nil
}

// ---------------------------------------------------------------------------
// macOS: osascript
// ---------------------------------------------------------------------------

// MacNotifier dispatches notifications via osascript.
type MacNotifier struct{}

// Notify uses AppleScript's `display notification` to show a banner.
func (m *MacNotifier) Notify(n Notification) error {
	title := escapeAppleScript(n.Title)
	msg := escapeAppleScript(n.Message)
	script := fmt.Sprintf(`display notification "%s" with title "%s"`, msg, title)
	if n.Sound {
		script += ` sound name "default"`
	}
	return exec.Command("osascript", "-e", script).Run()
}

// IsAvailable reports whether osascript is on PATH.
func (m *MacNotifier) IsAvailable() bool {
	_, err := exec.LookPath("osascript")
	return err == nil
}

// ---------------------------------------------------------------------------
// Windows: powershell BurntToast / MessageBox fallback
// ---------------------------------------------------------------------------

// WindowsNotifier dispatches notifications via PowerShell.
type WindowsNotifier struct{}

// Notify shows a balloon-style notification using the Windows Forms API
// available on every modern Windows installation. This avoids requiring
// the optional BurntToast PowerShell module.
func (w *WindowsNotifier) Notify(n Notification) error {
	icon := "Info"
	switch strings.ToLower(n.Icon) {
	case "warning", "warn":
		icon = "Warning"
	case "error":
		icon = "Error"
	}
	title := escapePowerShell(n.Title)
	msg := escapePowerShell(n.Message)
	script := fmt.Sprintf(
		`[void][System.Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms');`+
			`$n = New-Object System.Windows.Forms.NotifyIcon;`+
			`$n.Icon = [System.Drawing.SystemIcons]::%s;`+
			`$n.BalloonTipIcon = '%s';`+
			`$n.BalloonTipTitle = '%s';`+
			`$n.BalloonTipText = '%s';`+
			`$n.Visible = $true;`+
			`$n.ShowBalloonTip(5000);`+
			`Start-Sleep -Seconds 1;`+
			`$n.Dispose();`,
		icon, icon, title, msg,
	)
	return exec.Command("powershell", "-NoProfile", "-Command", script).Run()
}

// IsAvailable reports whether powershell is on PATH.
func (w *WindowsNotifier) IsAvailable() bool {
	_, err := exec.LookPath("powershell")
	return err == nil
}

// ---------------------------------------------------------------------------
// Fallback: log to stderr
// ---------------------------------------------------------------------------

// FallbackNotifier writes notifications to stderr. It is always available.
type FallbackNotifier struct{}

// Notify writes a single line to stderr describing the notification.
func (f *FallbackNotifier) Notify(n Notification) error {
	tag := strings.ToUpper(n.Icon)
	if tag == "" {
		tag = "INFO"
	}
	_, err := fmt.Fprintf(os.Stderr, "[notify:%s] %s — %s\n", tag, n.Title, n.Message)
	return err
}

// IsAvailable always returns true.
func (f *FallbackNotifier) IsAvailable() bool { return true }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// iconForType resolves a logical icon name to a freedesktop icon name
// suitable for notify-send. Unknown values are returned unchanged so
// callers can pass full icon paths if they want to.
func iconForType(name string) string {
	switch strings.ToLower(name) {
	case "info":
		return "dialog-information"
	case "warning", "warn":
		return "dialog-warning"
	case "error":
		return "dialog-error"
	case "success", "ok":
		return "emblem-default"
	default:
		return name
	}
}

func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func escapePowerShell(s string) string {
	return strings.ReplaceAll(s, `'`, `''`)
}

// ---------------------------------------------------------------------------
// Long-running command helper
// ---------------------------------------------------------------------------

// NotifyThreshold is the minimum tool duration before a completion
// notification fires. Tools that finish faster are silently ignored.
var NotifyThreshold = 5 * time.Second

// NotifyOnComplete returns a callback that fires a desktop notification
// when a tool's recorded duration exceeds NotifyThreshold. It is intended
// to be plugged into tool execution wrappers, e.g.
//
//	notify := observability.NotifyOnComplete(observability.NewNotifier())
//	start := time.Now()
//	runTool(...)
//	notify("bash", time.Since(start))
//
// The returned function is safe to call from any goroutine. A nil
// notifier is treated as no-op.
func NotifyOnComplete(notifier Notifier) func(toolName string, duration time.Duration) {
	if notifier == nil {
		return func(string, time.Duration) {}
	}
	return func(toolName string, duration time.Duration) {
		if duration < NotifyThreshold {
			return
		}
		_ = notifier.Notify(Notification{
			Title:   fmt.Sprintf("polypod: %s done", toolName),
			Message: fmt.Sprintf("Finished in %s", duration.Round(100*time.Millisecond)),
			Icon:    "success",
		})
	}
}
