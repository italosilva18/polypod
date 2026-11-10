//go:build windows

package swarm

import "golang.org/x/sys/windows"

func replaceJournalFile(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// MoveFileEx with WRITE_THROUGH makes the replacement durable on Windows.
// Opening a directory with os.File.Sync is not supported by Windows.
func syncTeamJournalDir(string) error { return nil }
