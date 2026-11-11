package computeruse

import (
	"errors"
	"os/exec"
	"syscall"
)

// A portal client launched through runuser may outlive runuser when the
// context expires. Give each screenshot attempt its own process group so the
// client and its descendants can be stopped together.
func superviseScreenshotCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error { return terminateScreenshotCommand(cmd) }
}

func terminateScreenshotCommand(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return cmd.Process.Kill()
}
