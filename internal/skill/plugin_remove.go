package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Uninstall removes a discovered plugin from disk and the live skill registry.
// The directory is first moved outside every watched skill root. If unloading
// fails, it is restored before the publication lock is released.
func (pm *PluginManager) Uninstall(ctx context.Context, id string) (PluginInfo, error) {
	if pm == nil || pm.reg == nil || pm.loader == nil {
		return PluginInfo{}, errors.New("gerenciador de plugins indisponivel")
	}
	if strings.TrimSpace(id) == "" {
		return PluginInfo{}, errors.New("id do plugin e obrigatorio")
	}
	if err := ctx.Err(); err != nil {
		return PluginInfo{}, err
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	release, err := pm.lockScanRoots(ctx)
	if err != nil {
		return PluginInfo{}, err
	}
	defer release()
	if err := pm.recoverMissingPluginBackupsLocked(ctx); err != nil {
		return PluginInfo{}, err
	}
	manifest, err := pm.findLocked(ctx, strings.TrimSpace(id))
	if err != nil {
		return PluginInfo{}, err
	}
	if err := pm.validateRemovalTarget(manifest.dir); err != nil {
		return PluginInfo{}, err
	}
	pm.adoptExistingLocked([]PluginManifest{manifest})
	info := pm.infoLocked(manifest)
	root := filepath.Dir(manifest.dir)
	// A previous update may have left its old-version backup behind after the
	// new version was published. Remove it while the current plugin still
	// exists; otherwise startup recovery would resurrect an uninstalled plugin.
	backup := pluginBackupPath(manifest.dir)
	if backupInfo, err := os.Lstat(backup); err == nil {
		if !backupInfo.IsDir() || backupInfo.Mode()&os.ModeSymlink != 0 {
			return PluginInfo{}, errors.New("backup pendente do plugin nao e diretorio")
		}
		if err := os.RemoveAll(backup); err != nil {
			return PluginInfo{}, fmt.Errorf("limpando backup antigo do plugin: %w", err)
		}
		if err := syncPluginDir(filepath.Dir(root)); err != nil {
			return PluginInfo{}, fmt.Errorf("sincronizando limpeza do backup: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return PluginInfo{}, fmt.Errorf("verificando backup antigo do plugin: %w", err)
	}
	removed, err := reserveRemovedPluginPath(filepath.Dir(root), pluginID(manifest.dir))
	if err != nil {
		return PluginInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return PluginInfo{}, err
	}
	if err := os.Rename(manifest.dir, removed); err != nil {
		return PluginInfo{}, fmt.Errorf("removendo plugin do catalogo: %w", err)
	}
	if err := syncPluginPublication(root); err != nil {
		return PluginInfo{}, pm.restoreRemovedPlugin(manifest, removed, err)
	}
	if err := pm.unloadLocked(manifest.dir); err != nil {
		return PluginInfo{}, pm.restoreRemovedPlugin(manifest, removed, err)
	}
	if err := os.RemoveAll(removed); err != nil {
		pm.loader.logger.Warn("plugin removed but cleanup failed", "path", removed, "error", err)
	}
	if err := syncPluginDir(filepath.Dir(root)); err != nil {
		pm.loader.logger.Warn("plugin removal directory sync failed", "path", filepath.Dir(root), "error", err)
	}
	return info, nil
}

func reserveRemovedPluginPath(parent, id string) (string, error) {
	path, err := os.MkdirTemp(parent, ".plugin-removed-"+id+"-")
	if err != nil {
		return "", fmt.Errorf("reservando diretorio de remocao: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("preparando diretorio de remocao: %w", err)
	}
	return path, nil
}

func (pm *PluginManager) validateRemovalTarget(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("verificando plugin: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("diretorio do plugin invalido")
	}
	parent := filepath.Dir(dir)
	for _, configured := range pm.loader.dirs {
		root, err := filepath.Abs(configured)
		if err == nil && root == parent && filepath.Base(dir) != "." {
			return nil
		}
	}
	return errors.New("plugin fora das raizes configuradas")
}

func (pm *PluginManager) restoreRemovedPlugin(manifest PluginManifest, removed string, cause error) error {
	if err := os.Rename(removed, manifest.dir); err != nil {
		return errors.Join(cause, fmt.Errorf("restaurando plugin de %s: %w", removed, err))
	}
	if err := syncPluginPublication(filepath.Dir(manifest.dir)); err != nil {
		cause = errors.Join(cause, fmt.Errorf("sincronizando restauracao do plugin: %w", err))
	}
	if err := pm.reconcileOneLocked(manifest); err != nil {
		cause = errors.Join(cause, fmt.Errorf("recarregando plugin restaurado: %w", err))
	}
	return cause
}
