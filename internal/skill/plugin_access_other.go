//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd

package skill

import "os"

func setPluginAccess(path string, mode os.FileMode, _ int) error {
	return os.Chmod(path, mode)
}

func setPluginFileAccess(file *os.File, mode os.FileMode, _ int) error {
	return file.Chmod(mode)
}
