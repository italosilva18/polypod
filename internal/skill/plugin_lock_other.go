//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd

package skill

import "context"

func lockPluginRoot(ctx context.Context, _ string) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return func() error { return nil }, nil
}
