//go:build !windows

package evolution

import "syscall"

const readOnlyNoFollowFlags = syscall.O_NONBLOCK | syscall.O_NOFOLLOW
