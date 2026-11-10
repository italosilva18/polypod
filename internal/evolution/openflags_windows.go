//go:build windows

package evolution

// os.Root confines the opened path to the skills directory on Windows.
const readOnlyNoFollowFlags = 0
