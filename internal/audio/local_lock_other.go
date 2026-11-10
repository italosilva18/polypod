//go:build !linux

package audio

import "context"

// Other platforms retain the in-process gate until an OS-specific lock is
// implemented. Linux is the supported service platform for this integration.
func acquireModelLock(ctx context.Context, _ string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return func() {}, nil
}
