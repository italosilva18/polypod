//go:build !linux

package tool

import "os/exec"

func configureGrokProcess(cmd *exec.Cmd) error { return nil }

func setGrokPromptOwner(cmd *exec.Cmd, path string) error { return nil }
