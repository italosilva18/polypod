//go:build !windows

package swarm

import "os"

func replaceJournalFile(source, destination string) error {
	return os.Rename(source, destination)
}

func syncTeamJournalDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
}
