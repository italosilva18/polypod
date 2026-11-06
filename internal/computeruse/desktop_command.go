package computeruse

import (
	"context"
	"os"
	"os/exec"
	"runtime"
)

// desktopCommandContext runs graphical Linux commands inside the logged-in
// desktop user's session when Polypod itself is running as root. This keeps the
// agent privileged while satisfying Wayland/DBus peer-identity requirements.
func desktopCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	user := os.Getenv("POLYPOD_DESKTOP_USER")
	if runtime.GOOS != "linux" || os.Geteuid() != 0 || user == "" {
		return exec.CommandContext(ctx, name, args...)
	}

	resolved, err := exec.LookPath(name)
	if err != nil {
		resolved = name
	}

	home := os.Getenv("POLYPOD_DESKTOP_HOME")
	if home == "" {
		home = "/home/" + user
	}

	envArgs := []string{
		"-u", user, "--", "/usr/bin/env",
		"HOME=" + home,
		"USER=" + user,
		"LOGNAME=" + user,
		"PATH=" + os.Getenv("PATH"),
	}
	for _, key := range []string{
		"YDOTOOL_SOCKET",
		"DISPLAY",
		"WAYLAND_DISPLAY",
		"XDG_RUNTIME_DIR",
		"DBUS_SESSION_BUS_ADDRESS",
		"XDG_CURRENT_DESKTOP",
		"XDG_SESSION_TYPE",
	} {
		if value := os.Getenv(key); value != "" {
			envArgs = append(envArgs, key+"="+value)
		}
	}
	envArgs = append(envArgs, resolved)
	envArgs = append(envArgs, args...)
	return exec.CommandContext(ctx, "/usr/sbin/runuser", envArgs...)
}

func desktopCommand(name string, args ...string) *exec.Cmd {
	return desktopCommandContext(context.Background(), name, args...)
}
