// Package preventsleep prevents the operating system from sleeping
// during long-running operations.
//
// Platform-specific mechanisms:
//   - macOS:   spawns caffeinate -i with a timeout
//   - Linux:   spawns systemd-inhibit (if available)
//   - Windows: invokes powershell SetThreadExecutionState
//
// The implementation uses reference counting: multiple callers can hold
// the wake lock simultaneously and sleep is only allowed when all callers
// release.
//
// Migrated from TypeScript: services/preventSleep.ts
package preventsleep

import (
	"os/exec"
	"runtime"
	"sync"
	"time"
)

const (
	// caffeinateTimeout is how long caffeinate runs before auto-exiting.
	caffeinateTimeout = 300 // 5 minutes in seconds

	// restartInterval is how often the sleep-inhibitor is restarted
	// (before the caffeinate timeout expires).
	restartInterval = 4 * time.Minute
)

// sleepGuard manages the lifecycle of sleep inhibition.
type sleepGuard struct {
	mu       sync.Mutex
	refCount int
	proc     *exec.Cmd
	ticker   *time.Ticker
	stopCh   chan struct{}
}

var guard = &sleepGuard{}

// PreventSleep increments the reference count and starts inhibiting sleep
// if this is the first caller.
func PreventSleep() {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	guard.refCount++
	if guard.refCount == 1 {
		guard.spawnInhibitor()
		guard.startRestart()
	}
}

// AllowSleep decrements the reference count and releases sleep inhibition
// when no more callers need it.
func AllowSleep() {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.refCount > 0 {
		guard.refCount--
	}
	if guard.refCount == 0 {
		guard.stopRestart()
		guard.killInhibitor()
	}
}

// ForceAllow resets the reference count and stops inhibition immediately.
// Intended for cleanup on shutdown.
func ForceAllow() {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	guard.refCount = 0
	guard.stopRestart()
	guard.killInhibitor()
}

// IsActive reports whether sleep inhibition is currently active.
func IsActive() bool {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	return guard.refCount > 0 && guard.proc != nil
}

// ---------------------------------------------------------------------------
// internal
// ---------------------------------------------------------------------------

func (g *sleepGuard) spawnInhibitor() {
	if g.proc != nil {
		return
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		// caffeinate -i prevents idle sleep; -t auto-exits for self-healing.
		cmd = exec.Command("caffeinate", "-i", "-t", itoa(caffeinateTimeout))

	case "linux":
		// systemd-inhibit prevents idle sleep until the child exits.
		// We use 'sleep' as the child process with the same timeout.
		if _, err := exec.LookPath("systemd-inhibit"); err == nil {
			cmd = exec.Command(
				"systemd-inhibit",
				"--what=idle",
				"--who=polypod",
				"--why=Long-running operation in progress",
				"sleep", itoa(caffeinateTimeout),
			)
		}

	case "windows":
		// ES_CONTINUOUS | ES_SYSTEM_REQUIRED keeps the system awake.
		// We run a PowerShell snippet that sets the state and then sleeps.
		script := `
Add-Type -TypeDefinition '
using System.Runtime.InteropServices;
public class SleepPrevention {
    [DllImport("kernel32.dll")]
    public static extern uint SetThreadExecutionState(uint esFlags);
}
';
[SleepPrevention]::SetThreadExecutionState(0x80000003);
Start-Sleep -Seconds ` + itoa(caffeinateTimeout)
		cmd = exec.Command("powershell", "-NoProfile", "-Command", script)
	}

	if cmd == nil {
		return
	}

	// Run detached so it doesn't keep the Go process alive.
	if err := cmd.Start(); err != nil {
		return
	}
	g.proc = cmd

	// Reap the process asynchronously.
	go func() {
		_ = cmd.Wait()
		g.mu.Lock()
		if g.proc == cmd {
			g.proc = nil
		}
		g.mu.Unlock()
	}()
}

func (g *sleepGuard) killInhibitor() {
	if g.proc == nil || g.proc.Process == nil {
		g.proc = nil
		return
	}
	_ = g.proc.Process.Kill()
	g.proc = nil
}

func (g *sleepGuard) startRestart() {
	if g.ticker != nil {
		return
	}
	ticker := time.NewTicker(restartInterval)
	stopCh := make(chan struct{})
	g.ticker = ticker
	g.stopCh = stopCh
	go func() {
		for {
			select {
			case <-ticker.C:
				g.mu.Lock()
				if g.ticker == ticker && g.refCount > 0 {
					g.killInhibitor()
					g.spawnInhibitor()
				}
				g.mu.Unlock()
			case <-stopCh:
				return
			}
		}
	}()
}

func (g *sleepGuard) stopRestart() {
	if g.ticker != nil {
		g.ticker.Stop()
		close(g.stopCh)
		g.ticker = nil
		g.stopCh = nil
	}
}

func itoa(n int) string {
	// Avoid importing strconv for a single int→string conversion.
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 12)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	// Reverse.
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
