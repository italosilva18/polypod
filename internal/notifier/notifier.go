// Package notifier provides desktop and terminal notifications with
// platform-specific backends and debounce support.
package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// Default debounce interval to avoid spamming notifications.
const defaultDebounce = 5 * time.Second

// Notification holds the data for a single notification.
type Notification struct {
	Title   string // short summary (e.g. "Task Complete")
	Body    string // longer description
	Urgency string // "low", "normal", or "critical"
	Icon    string // icon name or path (optional, backend-dependent)
	Sound   bool   // whether to play a sound (backend-dependent)
}

// Backend is the interface every notification backend must implement.
type Backend interface {
	// Send delivers a notification. It returns an error if delivery fails.
	Send(ctx context.Context, n Notification) error
	// Available reports whether this backend can work on the current system.
	Available() bool
}

// ---------------------------------------------------------------------------
// NotifyBackend — OS-native desktop notifications
// ---------------------------------------------------------------------------

// NotifyBackend sends notifications through OS-specific command-line tools:
//   - Linux:   notify-send
//   - macOS:   osascript (display notification)
//   - Windows: powershell (New-BurntToastNotification or basic balloon)
type NotifyBackend struct{}

func (b *NotifyBackend) Available() bool {
	switch runtime.GOOS {
	case "linux":
		_, err := exec.LookPath("notify-send")
		return err == nil
	case "darwin":
		_, err := exec.LookPath("osascript")
		return err == nil
	case "windows":
		_, err := exec.LookPath("powershell")
		return err == nil
	default:
		return false
	}
}

func (b *NotifyBackend) Send(ctx context.Context, n Notification) error {
	switch runtime.GOOS {
	case "linux":
		return b.sendLinux(ctx, n)
	case "darwin":
		return b.sendDarwin(ctx, n)
	case "windows":
		return b.sendWindows(ctx, n)
	default:
		return fmt.Errorf("notifier: unsupported OS %q", runtime.GOOS)
	}
}

func (b *NotifyBackend) sendLinux(ctx context.Context, n Notification) error {
	args := []string{n.Title, n.Body}
	if n.Urgency != "" {
		args = append([]string{"--urgency=" + n.Urgency}, args...)
	}
	if n.Icon != "" {
		args = append([]string{"--icon=" + n.Icon}, args...)
	}
	cmd := exec.CommandContext(ctx, "notify-send", args...)
	return cmd.Run()
}

func (b *NotifyBackend) sendDarwin(ctx context.Context, n Notification) error {
	script := fmt.Sprintf(
		`display notification %q with title %q`,
		n.Body, n.Title,
	)
	if n.Sound {
		script += ` sound name "default"`
	}
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	return cmd.Run()
}

func (b *NotifyBackend) sendWindows(ctx context.Context, n Notification) error {
	// Use a basic .NET balloon notification that works without extra modules.
	script := fmt.Sprintf(
		`[System.Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms') | Out-Null;`+
			`$n = New-Object System.Windows.Forms.NotifyIcon;`+
			`$n.Icon = [System.Drawing.SystemIcons]::Information;`+
			`$n.Visible = $true;`+
			`$n.ShowBalloonTip(5000, '%s', '%s', 'Info');`+
			`Start-Sleep -Seconds 1;`+
			`$n.Dispose()`,
		escapePowerShell(n.Title),
		escapePowerShell(n.Body),
	)
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", script)
	return cmd.Run()
}

// escapePowerShell does minimal escaping for single-quoted PS strings.
func escapePowerShell(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '\'')
		} else {
			out = append(out, s[i])
		}
	}
	return string(out)
}

// ---------------------------------------------------------------------------
// BellBackend — terminal BEL character
// ---------------------------------------------------------------------------

// BellBackend writes the ASCII BEL character (\a) to the terminal, which
// causes most terminal emulators to flash or play a sound.
type BellBackend struct{}

func (b *BellBackend) Available() bool { return true }

func (b *BellBackend) Send(_ context.Context, _ Notification) error {
	_, err := os.Stderr.WriteString("\a")
	return err
}

// ---------------------------------------------------------------------------
// MultiBackend — tries backends in order
// ---------------------------------------------------------------------------

// MultiBackend tries each backend in order and returns as soon as one
// succeeds. If all backends fail, the last error is returned.
type MultiBackend struct {
	Backends []Backend
}

func (m *MultiBackend) Available() bool {
	for _, b := range m.Backends {
		if b.Available() {
			return true
		}
	}
	return false
}

func (m *MultiBackend) Send(ctx context.Context, n Notification) error {
	var lastErr error
	for _, b := range m.Backends {
		if !b.Available() {
			continue
		}
		if err := b.Send(ctx, n); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("notifier: all backends failed, last error: %w", lastErr)
	}
	return fmt.Errorf("notifier: no available backends")
}

// ---------------------------------------------------------------------------
// Notifier — high-level API with debounce
// ---------------------------------------------------------------------------

// Notifier is the main entry point. It sends notifications through its
// configured backends while enforcing a debounce interval so the user is
// not spammed with rapid-fire notifications.
type Notifier struct {
	backend  Backend
	logger   *slog.Logger
	debounce time.Duration

	mu       sync.Mutex
	lastSent time.Time
}

// NewNotifier creates a Notifier that auto-detects available backends.
// It prefers OS-native notifications and falls back to a terminal bell.
func NewNotifier(logger *slog.Logger) *Notifier {
	multi := &MultiBackend{
		Backends: []Backend{
			&NotifyBackend{},
			&BellBackend{},
		},
	}
	return &Notifier{
		backend:  multi,
		logger:   logger,
		debounce: defaultDebounce,
	}
}

// Notify sends a notification with the given title and body.
// Calls that arrive within the debounce window are silently dropped.
func (n *Notifier) Notify(ctx context.Context, title, body string) error {
	n.mu.Lock()
	if time.Since(n.lastSent) < n.debounce {
		n.mu.Unlock()
		n.logger.Debug("notification debounced", "title", title)
		return nil
	}
	n.lastSent = time.Now()
	n.mu.Unlock()

	notif := Notification{
		Title:   title,
		Body:    body,
		Urgency: "normal",
	}

	if err := n.backend.Send(ctx, notif); err != nil {
		n.logger.Warn("notification failed", "error", err)
		return err
	}
	n.logger.Debug("notification sent", "title", title)
	return nil
}

// NotifyTaskComplete is a convenience method that notifies the user about a
// completed task.
func (n *Notifier) NotifyTaskComplete(taskName string) error {
	return n.Notify(context.Background(), "Task Complete", fmt.Sprintf("Task %q finished.", taskName))
}

// NotifyError is a convenience method that notifies the user about an error.
func (n *Notifier) NotifyError(err error) error {
	if err == nil {
		return nil
	}
	return n.Notify(context.Background(), "Error", err.Error())
}
