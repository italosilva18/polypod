//go:build !linux

package audio

import (
	"os/exec"
	"time"
)

func configureLocalProcess(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
}
