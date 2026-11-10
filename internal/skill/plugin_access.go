package skill

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
)

// pluginSharedGID uses the desktop user's primary group when a root service
// publishes plugins, so that both the service and the desktop CLI can use them.
func pluginSharedGID() (int, error) {
	name := strings.TrimSpace(os.Getenv("POLYPOD_DESKTOP_USER"))
	if name == "" {
		return os.Getgid(), nil
	}
	account, err := user.Lookup(name)
	if err != nil {
		return 0, fmt.Errorf("usuario desktop de plugins indisponivel: %w", err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return 0, fmt.Errorf("grupo desktop de plugins invalido: %w", err)
	}
	return gid, nil
}
