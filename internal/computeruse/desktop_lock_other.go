//go:build !linux && !darwin

package computeruse

import "context"

// On platforms without the Unix desktop lock, the tool's action mutex still
// serializes actions within a process.
func acquireDesktopLock(context.Context) (func() error, error) {
	return func() error { return nil }, nil
}
