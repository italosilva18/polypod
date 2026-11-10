//go:build !linux

package skill

func exchangePluginDirectories(_, _ string) (bool, error) {
	return false, nil
}
