//go:build linux

package audio

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureLocalProcess(cmd *exec.Cmd) {
	// Whisper may launch ffmpeg while decoding the converted WAV. Killing the
	// process group prevents that child from surviving a timeout or shutdown.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
}
