//go:build !windows

package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// inheritSubscriptionOwner lets a root service keep the private state file
// readable by the account that owns the shared data directory. An existing
// non-root owner takes precedence over the directory owner.
func inheritSubscriptionOwner(outputPath, destinationPath string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	parent, err := os.Stat(filepath.Dir(destinationPath))
	if err != nil {
		return err
	}
	parentStat, ok := parent.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("mcp: nao foi possivel ler o dono do diretorio de assinaturas")
	}
	uid, gid := int(parentStat.Uid), int(parentStat.Gid)
	if existing, err := os.Stat(destinationPath); err == nil {
		if stat, ok := existing.Sys().(*syscall.Stat_t); ok && stat.Uid != 0 {
			uid, gid = int(stat.Uid), int(stat.Gid)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Chown(outputPath, uid, gid)
}
