package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxPluginFiles = 4096
	maxPluginBytes = 128 << 20
	maxPluginFile  = 32 << 20
)

// Install copies a local plugin directory or an explicit Git URL into the last
// configured plugin search root. The plugin becomes visible only after every
// declared skill has been validated in a staging directory outside that root.
func (pm *PluginManager) Install(ctx context.Context, source string) (PluginInfo, error) {
	if pm == nil || pm.reg == nil {
		return PluginInfo{}, errors.New("gerenciador de plugins indisponivel")
	}
	sourceDir, baseName, cleanup, err := preparePluginSource(ctx, source)
	if err != nil {
		return PluginInfo{}, err
	}
	defer cleanup()
	name, err := pluginDirectoryName(baseName)
	if err != nil {
		return PluginInfo{}, err
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if len(pm.loader.dirs) == 0 {
		return PluginInfo{}, errors.New("nenhum diretorio de plugins configurado")
	}
	root, err := ensurePluginRoot(pm.loader.dirs[len(pm.loader.dirs)-1])
	if err != nil {
		return PluginInfo{}, err
	}
	if pathContains(sourceDir, root) {
		return PluginInfo{}, errors.New("a origem contem o diretorio de instalacao")
	}
	unlock, err := lockPluginRoot(ctx, filepath.Dir(root))
	if err != nil {
		return PluginInfo{}, err
	}
	defer func() {
		if err := unlock(); err != nil {
			pm.loader.logger.Warn("plugin install lock release failed", "error", err)
		}
	}()
	dest := filepath.Join(root, name)
	if _, err := os.Lstat(dest); err == nil {
		return PluginInfo{}, fmt.Errorf("plugin %q ja existe; use update_local_plugin", name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return PluginInfo{}, fmt.Errorf("verificando destino do plugin: %w", err)
	}
	stage, manifest, err := pm.stagePlugin(ctx, sourceDir, filepath.Dir(root), true)
	if err != nil {
		return PluginInfo{}, err
	}
	defer func() {
		if err := os.RemoveAll(stage); err != nil {
			pm.loader.logger.Warn("plugin stage cleanup failed", "path", stage, "error", err)
		}
	}()
	if err := os.Rename(stage, dest); err != nil {
		return PluginInfo{}, fmt.Errorf("publicando plugin: %w", err)
	}
	if err := syncPluginPublication(root); err != nil {
		return PluginInfo{}, pm.rollbackNewPlugin(dest, stage, err)
	}
	manifest.dir = dest
	if err := pm.reconcileOneLocked(manifest); err != nil {
		return PluginInfo{}, pm.rollbackNewPlugin(dest, stage, err)
	}
	return pm.infoLocked(manifest), nil
}

// Update replaces the directory selected by a List ID. Old files remain in a
// hidden sibling until the new skill set has been published in the registry.
func (pm *PluginManager) Update(ctx context.Context, id, source string) (PluginInfo, error) {
	if pm == nil || pm.reg == nil {
		return PluginInfo{}, errors.New("gerenciador de plugins indisponivel")
	}
	if strings.TrimSpace(id) == "" {
		return PluginInfo{}, errors.New("id do plugin e obrigatorio")
	}
	sourceDir, _, cleanup, err := preparePluginSource(ctx, source)
	if err != nil {
		return PluginInfo{}, err
	}
	defer cleanup()
	pm.mu.Lock()
	defer pm.mu.Unlock()
	previous, err := pm.findLocked(ctx, id)
	if err != nil {
		return PluginInfo{}, err
	}
	root := filepath.Dir(previous.dir)
	if _, err := ensurePluginRoot(root); err != nil {
		return PluginInfo{}, err
	}
	if pathContains(sourceDir, root) {
		return PluginInfo{}, errors.New("a origem contem o diretorio de instalacao")
	}
	unlock, err := lockPluginRoot(ctx, filepath.Dir(root))
	if err != nil {
		return PluginInfo{}, err
	}
	defer func() {
		if err := unlock(); err != nil {
			pm.loader.logger.Warn("plugin update lock release failed", "error", err)
		}
	}()
	// A different process may have changed the plugin while the source was
	// fetched. Re-read it under the cross-process publication lock.
	previous, err = pm.findLocked(ctx, id)
	if err != nil {
		return PluginInfo{}, err
	}
	if filepath.Dir(previous.dir) != root {
		return PluginInfo{}, errors.New("diretorio do plugin mudou durante a atualizacao")
	}
	pm.adoptExistingLocked([]PluginManifest{previous})
	backup := pluginBackupPath(previous.dir)
	if info, err := os.Lstat(backup); err == nil {
		if !info.IsDir() {
			return PluginInfo{}, errors.New("backup de plugin pendente nao e diretorio")
		}
		if err := pm.reconcileOneLocked(previous); err != nil {
			return PluginInfo{}, fmt.Errorf("reconciliando atualizacao anterior: %w", err)
		}
		if err := os.RemoveAll(backup); err != nil {
			return PluginInfo{}, fmt.Errorf("limpando backup anterior do plugin: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return PluginInfo{}, fmt.Errorf("verificando backup do plugin: %w", err)
	}
	stage, manifest, err := pm.stagePlugin(ctx, sourceDir, filepath.Dir(root), previous.Enabled)
	if err != nil {
		return PluginInfo{}, err
	}
	removeStage := true
	defer func() {
		if removeStage {
			if err := os.RemoveAll(stage); err != nil {
				pm.loader.logger.Warn("plugin stage cleanup failed", "path", stage, "error", err)
			}
		}
	}()
	exchanged, err := exchangePluginDirectories(previous.dir, stage)
	if err != nil {
		return PluginInfo{}, err
	}
	if exchanged {
		if err := syncPluginPublication(root); err != nil {
			return PluginInfo{}, pm.rollbackExchangedPlugin(previous, stage, err, &removeStage)
		}
		manifest.dir = previous.dir
		if err := pm.reconcileOneLocked(manifest); err != nil {
			return PluginInfo{}, pm.rollbackExchangedPlugin(previous, stage, err, &removeStage)
		}
		return pm.infoLocked(manifest), nil
	}
	if err := os.Rename(previous.dir, backup); err != nil {
		return PluginInfo{}, fmt.Errorf("guardando versao anterior do plugin: %w", err)
	}
	if err := os.Rename(stage, previous.dir); err != nil {
		if restoreErr := os.Rename(backup, previous.dir); restoreErr != nil {
			removeStage = false
			return PluginInfo{}, errors.Join(err, fmt.Errorf("restaurando plugin em %s: %w", backup, restoreErr))
		}
		return PluginInfo{}, fmt.Errorf("publicando atualizacao: %w", err)
	}
	if err := syncPluginPublication(root); err != nil {
		return PluginInfo{}, pm.rollbackUpdatedPlugin(previous, backup, stage, err, &removeStage)
	}
	manifest.dir = previous.dir
	if err := pm.reconcileOneLocked(manifest); err != nil {
		return PluginInfo{}, pm.rollbackUpdatedPlugin(previous, backup, stage, err, &removeStage)
	}
	if err := os.RemoveAll(backup); err != nil {
		pm.loader.logger.Warn("plugin backup cleanup failed", "path", backup, "error", err)
	}
	if err := syncPluginDir(filepath.Dir(root)); err != nil {
		pm.loader.logger.Warn("plugin directory sync failed after update", "path", root, "error", err)
	}
	return pm.infoLocked(manifest), nil
}

func (pm *PluginManager) stagePlugin(ctx context.Context, sourceDir, parent string, defaultEnabled bool) (string, PluginManifest, error) {
	stage, err := os.MkdirTemp(parent, ".plugin-stage-")
	if err != nil {
		return "", PluginManifest{}, fmt.Errorf("criando area temporaria do plugin: %w", err)
	}
	if err := copyPluginTree(ctx, sourceDir, stage); err != nil {
		_ = os.RemoveAll(stage)
		return "", PluginManifest{}, err
	}
	manifest, err := pm.validateStagedPlugin(stage, defaultEnabled)
	if err != nil {
		_ = os.RemoveAll(stage)
		return "", PluginManifest{}, err
	}
	if err := sharePluginTree(stage); err != nil {
		_ = os.RemoveAll(stage)
		return "", PluginManifest{}, err
	}
	if err := syncPluginTree(stage); err != nil {
		_ = os.RemoveAll(stage)
		return "", PluginManifest{}, fmt.Errorf("sincronizando plugin temporario: %w", err)
	}
	return stage, manifest, nil
}

func (pm *PluginManager) validateStagedPlugin(stage string, defaultEnabled bool) (PluginManifest, error) {
	manifestPath := filepath.Join(stage, "plugin.json")
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() {
		return PluginManifest{}, errors.New("plugin.json regular e obrigatorio na raiz do plugin")
	}
	if info.Size() > 1<<20 {
		return PluginManifest{}, errors.New("plugin.json excede o tamanho permitido")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return PluginManifest{}, fmt.Errorf("lendo plugin.json: %w", err)
	}
	var manifest PluginManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return PluginManifest{}, errors.New("plugin.json invalido")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return PluginManifest{}, errors.New("plugin.json precisa ser um objeto")
	}
	if raw, present := fields["enabled"]; present {
		value := strings.TrimSpace(string(raw))
		if value != "true" && value != "false" {
			return PluginManifest{}, errors.New("enabled em plugin.json deve ser booleano")
		}
	} else {
		if err := writePluginEnabled(manifestPath, defaultEnabled); err != nil {
			return PluginManifest{}, fmt.Errorf("definindo estado padrao do plugin: %w", err)
		}
		manifest.Enabled = defaultEnabled
	}
	if strings.TrimSpace(manifest.Name) == "" || len(manifest.Skills) == 0 || len(manifest.Skills) > 128 {
		return PluginManifest{}, errors.New("plugin.json requer nome e entre 1 e 128 skills")
	}
	seenPaths := make(map[string]struct{}, len(manifest.Skills))
	seenNames := make(map[string]struct{}, len(manifest.Skills))
	for _, relPath := range manifest.Skills {
		if !filepath.IsLocal(relPath) || filepath.Clean(relPath) != relPath {
			return PluginManifest{}, fmt.Errorf("caminho de skill invalido: %q", relPath)
		}
		if _, duplicate := seenPaths[relPath]; duplicate {
			return PluginManifest{}, fmt.Errorf("skill declarada mais de uma vez: %q", relPath)
		}
		seenPaths[relPath] = struct{}{}
		path, err := pm.loader.resolveSkillPath(stage, relPath)
		if err != nil {
			return PluginManifest{}, fmt.Errorf("caminho de skill %q: %w", relPath, err)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return PluginManifest{}, fmt.Errorf("skill %q precisa ser arquivo regular", relPath)
		}
		s, err := pm.loader.loadSkillFile(path)
		if err != nil {
			return PluginManifest{}, fmt.Errorf("skill %q: %w", relPath, err)
		}
		if _, duplicate := seenNames[s.Name]; duplicate {
			return PluginManifest{}, fmt.Errorf("nome de skill duplicado: %q", s.Name)
		}
		seenNames[s.Name] = struct{}{}
	}
	manifest.dir = stage
	return manifest, nil
}

func (pm *PluginManager) rollbackNewPlugin(dest, stage string, cause error) error {
	if err := os.Rename(dest, stage); err != nil {
		return errors.Join(cause, fmt.Errorf("rollback do plugin em %s: %w", dest, err))
	}
	if err := syncPluginPublication(filepath.Dir(dest)); err != nil {
		return errors.Join(cause, fmt.Errorf("sincronizando rollback: %w", err))
	}
	return cause
}

func (pm *PluginManager) rollbackUpdatedPlugin(previous PluginManifest, backup, stage string, cause error, removeStage *bool) error {
	if err := os.Rename(previous.dir, stage); err != nil {
		*removeStage = false
		return errors.Join(cause, fmt.Errorf("movendo atualizacao falha para %s: %w", stage, err))
	}
	if err := os.Rename(backup, previous.dir); err != nil {
		if retryErr := os.Rename(stage, previous.dir); retryErr != nil {
			*removeStage = false
			return errors.Join(cause, fmt.Errorf("restaurando plugin de %s: %w", backup, err), retryErr)
		}
		return errors.Join(cause, fmt.Errorf("restaurando plugin de %s: %w", backup, err))
	}
	if err := syncPluginPublication(filepath.Dir(previous.dir)); err != nil {
		cause = errors.Join(cause, fmt.Errorf("sincronizando rollback: %w", err))
	}
	if err := pm.reconcileOneLocked(previous); err != nil {
		cause = errors.Join(cause, fmt.Errorf("reconciliando versao anterior: %w", err))
	}
	return cause
}

func (pm *PluginManager) rollbackExchangedPlugin(previous PluginManifest, stage string, cause error, removeStage *bool) error {
	exchanged, err := exchangePluginDirectories(previous.dir, stage)
	if err != nil || !exchanged {
		*removeStage = false // the former version remains in stage for manual recovery
		if err == nil {
			err = errors.New("troca atomica indisponivel durante rollback")
		}
		return errors.Join(cause, fmt.Errorf("restaurando plugin de %s: %w", stage, err))
	}
	if err := syncPluginPublication(filepath.Dir(previous.dir)); err != nil {
		cause = errors.Join(cause, fmt.Errorf("sincronizando rollback: %w", err))
	}
	if err := pm.reconcileOneLocked(previous); err != nil {
		cause = errors.Join(cause, fmt.Errorf("reconciliando versao anterior: %w", err))
	}
	return cause
}

func preparePluginSource(ctx context.Context, source string) (dir, base string, cleanup func(), err error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", "", nil, errors.New("origem do plugin e obrigatoria")
	}
	if isPluginGitURL(source) {
		base, err = pluginGitBase(source)
		if err != nil {
			return "", "", nil, err
		}
		temp, err := os.MkdirTemp("", "polypod-plugin-git-")
		if err != nil {
			return "", "", nil, fmt.Errorf("criando area para clone Git: %w", err)
		}
		cloneDir := filepath.Join(temp, "source")
		cmd := exec.CommandContext(ctx, "git",
			"-c", "protocol.file.allow=never",
			"-c", "protocol.ext.allow=never",
			"-c", "core.hooksPath=/dev/null",
			"clone", "--depth=1", "--no-recurse-submodules", "--no-tags", "--", source, cloneDir)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if err := cmd.Run(); err != nil {
			_ = os.RemoveAll(temp)
			return "", "", nil, errors.New("git clone falhou; verifique URL, acesso e credenciais Git")
		}
		return cloneDir, base, func() { _ = os.RemoveAll(temp) }, nil
	}
	if strings.Contains(source, "://") {
		return "", "", nil, errors.New("URL Git deve usar HTTPS ou SSH sem credenciais embutidas")
	}
	path, err := filepath.Abs(source)
	if err != nil {
		return "", "", nil, fmt.Errorf("resolvendo origem local: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", "", nil, fmt.Errorf("lendo origem local: %w", err)
	}
	if !info.IsDir() {
		return "", "", nil, errors.New("origem local precisa ser um diretorio regular")
	}
	return path, filepath.Base(path), func() {}, nil
}

func isPluginGitURL(source string) bool {
	if strings.HasPrefix(source, "git@") {
		return true
	}
	u, err := url.Parse(source)
	return err == nil && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
		(u.Scheme == "https" || u.Scheme == "ssh")
}

func pluginGitBase(source string) (string, error) {
	if strings.HasPrefix(source, "git@") {
		colon := strings.IndexByte(source, ':')
		if colon < 5 || colon == len(source)-1 {
			return "", errors.New("URL Git SSH invalida")
		}
		return filepath.Base(strings.TrimSuffix(source[colon+1:], ".git")), nil
	}
	u, err := url.Parse(source)
	if err != nil || u.Host == "" || u.Path == "" {
		return "", errors.New("URL Git invalida")
	}
	return filepath.Base(strings.TrimSuffix(u.Path, ".git")), nil
}

func pluginDirectoryName(base string) (string, error) {
	base = strings.ToLower(strings.TrimSuffix(base, ".git"))
	var b strings.Builder
	lastHyphen := false
	for _, r := range base {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastHyphen = false
		} else if b.Len() > 0 && !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" || len(name) > 100 {
		return "", errors.New("nome do diretorio do plugin invalido")
	}
	return name, nil
}

func ensurePluginRoot(path string) (string, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolvendo diretorio de plugins: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("criando diretorio de plugins: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return "", errors.New("diretorio de plugins invalido")
	}
	gid, err := pluginSharedGID()
	if err != nil {
		return "", err
	}
	if err := setPluginAccess(root, 0o770|os.ModeSetgid, gid); err != nil {
		return "", err
	}
	return root, nil
}

func sharePluginTree(root string) error {
	gid, err := pluginSharedGID()
	if err != nil {
		return err
	}
	var paths []string
	if err := filepath.WalkDir(root, func(path string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return err
	}
	for i := len(paths) - 1; i >= 0; i-- {
		info, err := os.Lstat(paths[i])
		if err != nil {
			return err
		}
		mode := os.FileMode(0o660)
		if info.IsDir() {
			mode = 0o770 | os.ModeSetgid
		} else if info.Mode().Perm()&0o100 != 0 {
			mode = 0o770
		}
		if err := setPluginAccess(paths[i], mode, gid); err != nil {
			return err
		}
	}
	return nil
}

func pathContains(parent, child string) bool {
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return false
	}
	resolvedChild, err := filepath.EvalSymlinks(child)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedParent, resolvedChild)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func copyPluginTree(ctx context.Context, source, target string) error {
	var entries int
	var total int64
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("lendo arquivos do plugin: %w", walkErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == source {
			return nil
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		entries++
		if entries > maxPluginFiles {
			return errors.New("plugin excede o limite de arquivos ou diretorios")
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || !filepath.IsLocal(rel) {
			return errors.New("caminho do plugin invalido")
		}
		destination := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.Mkdir(destination, 0o700)
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("plugin contem arquivo especial ou link: %q", rel)
		}
		if info.Size() > maxPluginFile || total+info.Size() > maxPluginBytes {
			return errors.New("plugin excede o limite de arquivos ou tamanho")
		}
		input, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("abrindo arquivo do plugin: %w", err)
		}
		mode := os.FileMode(0o600)
		if info.Mode()&0o100 != 0 {
			mode = 0o700
		}
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			_ = input.Close()
			return fmt.Errorf("criando arquivo temporario do plugin: %w", err)
		}
		written, copyErr := io.Copy(output, io.LimitReader(input, maxPluginFile+1))
		if closeErr := input.Close(); copyErr == nil {
			copyErr = closeErr
		}
		if written > maxPluginFile {
			copyErr = errors.New("arquivo do plugin excede o tamanho permitido")
		}
		if total+written > maxPluginBytes {
			copyErr = errors.New("plugin excede o tamanho permitido")
		}
		if copyErr == nil {
			copyErr = output.Sync()
		}
		if closeErr := output.Close(); copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			return fmt.Errorf("copiando arquivo do plugin: %w", copyErr)
		}
		total += written
		return nil
	})
}

func pluginBackupPath(destination string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(destination)), ".plugin-backup-"+pluginID(destination))
}

// lockScanRoots prevents another Polypod process from being scanned between
// the two renames of an update. Locks are acquired in path order.
func (pm *PluginManager) lockScanRoots(ctx context.Context) (func(), error) {
	parents := make(map[string]struct{}, len(pm.loader.dirs))
	for _, rawRoot := range pm.loader.dirs {
		root, err := filepath.Abs(rawRoot)
		if err != nil {
			return nil, fmt.Errorf("resolvendo raiz de plugins: %w", err)
		}
		parent := filepath.Dir(root)
		if info, err := os.Stat(parent); err == nil && info.IsDir() {
			parents[parent] = struct{}{}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("verificando raiz de plugins: %w", err)
		}
	}
	paths := make([]string, 0, len(parents))
	for parent := range parents {
		paths = append(paths, parent)
	}
	sort.Strings(paths)
	var unlocks []func() error
	release := func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			if err := unlocks[i](); err != nil {
				pm.loader.logger.Warn("plugin scan lock release failed", "error", err)
			}
		}
	}
	for _, parent := range paths {
		unlock, err := lockPluginRoot(ctx, parent)
		if err != nil {
			release()
			return nil, err
		}
		unlocks = append(unlocks, unlock)
	}
	return release, nil
}

// recoverMissingPluginBackupsLocked restores a plugin if the process stopped
// after moving the old directory away but before publishing the new one.
// The caller holds both pm.mu and the publication lock for each scanned root.
func (pm *PluginManager) recoverMissingPluginBackupsLocked(ctx context.Context) error {
	for _, rawRoot := range pm.loader.dirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		root, err := filepath.Abs(rawRoot)
		if err != nil {
			return err
		}
		parent := filepath.Dir(root)
		entries, err := os.ReadDir(parent)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("procurando backups de plugin: %w", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".plugin-backup-") {
				continue
			}
			id := strings.TrimPrefix(entry.Name(), ".plugin-backup-")
			at := strings.LastIndexByte(id, '@')
			if at <= 0 {
				continue
			}
			destination := filepath.Join(root, id[:at])
			if pluginID(destination) != id || pluginBackupPath(destination) != filepath.Join(parent, entry.Name()) {
				continue
			}
			if _, err := os.Lstat(destination); err == nil {
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("verificando plugin para recuperacao: %w", err)
			}
			if err := os.MkdirAll(root, 0o700); err != nil {
				return fmt.Errorf("recriando raiz de plugins: %w", err)
			}
			if err := os.Rename(filepath.Join(parent, entry.Name()), destination); err != nil {
				return fmt.Errorf("restaurando plugin apos interrupcao: %w", err)
			}
			if err := syncPluginPublication(root); err != nil {
				return fmt.Errorf("sincronizando recuperacao de plugin: %w", err)
			}
			pm.loader.logger.Info("plugin restored from interrupted update", "path", destination)
		}
	}
	return nil
}

func syncPluginDir(path string) error {
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

func syncPluginTree(root string) error {
	var dirs []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	}); err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := syncPluginDir(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

func syncPluginPublication(root string) error {
	if err := syncPluginDir(root); err != nil {
		return err
	}
	return syncPluginDir(filepath.Dir(root))
}
