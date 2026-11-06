//go:build linux

package browser

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

type launchIdentity struct {
	uid  uint32
	gid  uint32
	user string
	home string
}

func resolveLaunchIdentity(config ManagedConfig) (*launchIdentity, error) {
	if os.Geteuid() != 0 || config.DesktopUser == "" {
		return nil, nil
	}
	account, err := user.Lookup(config.DesktopUser)
	if err != nil {
		return nil, fmt.Errorf("usuário do desktop não encontrado: %w", err)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("UID do desktop inválido: %w", err)
	}
	if uid == 0 {
		return nil, fmt.Errorf("usuário do desktop deve ser não root")
	}
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("GID do desktop inválido: %w", err)
	}
	home := config.DesktopHome
	if home == "" {
		home = account.HomeDir
	}
	if home == "" {
		return nil, fmt.Errorf("HOME do usuário do desktop não encontrado")
	}
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("HOME do usuário do desktop deve ser absoluto")
	}
	info, err := os.Stat(home)
	if err != nil {
		return nil, fmt.Errorf("HOME do usuário do desktop indisponível: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("HOME do usuário do desktop não é diretório")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(uid) {
		return nil, fmt.Errorf("HOME do usuário do desktop tem proprietário diferente")
	}
	return &launchIdentity{uid: uint32(uid), gid: uint32(gid), user: account.Username, home: home}, nil
}

func applyLaunchIdentity(cmd *exec.Cmd, identity *launchIdentity) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if identity == nil {
		return
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{Uid: identity.uid, Gid: identity.gid}
	cmd.Env = append(os.Environ(), "HOME="+identity.home, "USER="+identity.user, "LOGNAME="+identity.user)
	cmd.Dir = identity.home
}

func createDesktopProfile(path string, identity *launchIdentity) error {
	cmd := exec.Command("mkdir", "-p", "-m", "0700", "--", path)
	applyLaunchIdentity(cmd, identity)
	return cmd.Run()
}

func profileOwnedBy(info os.FileInfo, identity *launchIdentity) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == identity.uid
}

func terminateBrowserProcess(cmd *exec.Cmd) error {
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

func killBrowserProcess(cmd *exec.Cmd) error {
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}
