//go:build windows

package session

func inheritSessionFileOwner(string, string) error { return nil }
func inheritSessionDirOwner(string) error          { return nil }
