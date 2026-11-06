//go:build !linux

package browser

import (
	"errors"
	"os"
	"os/exec"
)

type launchIdentity struct {
	uid  uint32
	gid  uint32
	user string
	home string
}

func resolveLaunchIdentity(ManagedConfig) (*launchIdentity, error) { return nil, nil }
func applyLaunchIdentity(*exec.Cmd, *launchIdentity)               {}
func createDesktopProfile(string, *launchIdentity) error           { return nil }
func profileOwnedBy(os.FileInfo, *launchIdentity) bool             { return true }
func terminateBrowserProcess(cmd *exec.Cmd) error {
	err := cmd.Process.Signal(os.Interrupt)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
func killBrowserProcess(cmd *exec.Cmd) error {
	err := cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
