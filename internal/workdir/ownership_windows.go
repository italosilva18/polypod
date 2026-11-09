//go:build windows

package workdir

func inheritSharedOwner(_, _ string) error            { return nil }
func withProcessLock(_ string, fn func() error) error { return fn() }
