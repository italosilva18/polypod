package tool

import "github.com/costa/polypod/internal/types"

func effectiveWorkingDir(ctx types.ToolContext, fallback string) string {
	if ctx.WorkingDir != "" {
		return ctx.WorkingDir
	}
	return fallback
}
