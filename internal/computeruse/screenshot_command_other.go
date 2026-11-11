//go:build !linux

package computeruse

import "os/exec"

func superviseScreenshotCommand(_ *exec.Cmd) {}

func terminateScreenshotCommand(_ *exec.Cmd) error { return nil }
