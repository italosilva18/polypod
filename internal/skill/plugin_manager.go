package skill

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// PluginInfo describes a local plugin and the skills currently owned by it.
type PluginInfo struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version,omitempty"`
	Author       string   `json:"author,omitempty"`
	Path         string   `json:"path"`
	Enabled      bool     `json:"enabled"`
	SkillFiles   []string `json:"skill_files"`
	LoadedSkills []string `json:"loaded_skills"`
}

// PluginManager reconciles plugin manifests with the live skill registry.
// Registry change callbacks keep tool adapters and agent skill lists in sync.
type PluginManager struct {
	mu       sync.Mutex
	loader   *PluginLoader
	reg      *Registry
	loaded   map[string]map[string]string // plugin directory -> skill name -> exact origin
	watchMu  sync.Mutex
	watching bool
}

const (
	pluginPollInterval  = 5 * time.Second
	pluginRetryInterval = 30 * time.Second
)

func NewPluginManager(dirs []string, reg *Registry, logger *slog.Logger) *PluginManager {
	return &PluginManager{
		loader: NewPluginLoader(dirs, logger),
		reg:    reg,
		loaded: make(map[string]map[string]string),
	}
}

// Start watches local manifests and declared skill files. It returns
// immediately and reconciles only when their content changes. A failed
// reconciliation is retried at a slower pace so a watcher handoff or a
// temporary registry conflict can resolve without log spam.
func (pm *PluginManager) Start(ctx context.Context) {
	if pm == nil || pm.reg == nil || ctx.Err() != nil {
		return
	}
	pm.watchMu.Lock()
	if pm.watching {
		pm.watchMu.Unlock()
		return
	}
	pm.watching = true
	pm.watchMu.Unlock()
	known := pm.fingerprint()
	go func() {
		defer func() {
			pm.watchMu.Lock()
			pm.watching = false
			pm.watchMu.Unlock()
		}()
		ticker := time.NewTicker(pluginPollInterval)
		defer ticker.Stop()
		var retryAt time.Time
		var retryPending bool
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			next := pm.fingerprint()
			changed := next != known
			if !changed && (!retryPending || time.Since(retryAt) < pluginRetryInterval) {
				continue
			}
			count, err := pm.Reconcile(ctx)
			if err != nil {
				if changed {
					pm.loader.logger.Warn("plugin reconciliation failed", "error", err)
				}
				retryPending = true
				retryAt = time.Now()
			} else {
				if changed {
					pm.loader.logger.Info("plugins reconciled", "loaded_skills", count)
				}
				retryPending = false
			}
			known = next
		}
	}()
}

func (pm *PluginManager) fingerprint() [32]byte {
	h := sha256.New()
	for _, root := range pm.loader.dirs {
		_, _ = h.Write([]byte("root:" + root + "\n"))
		entries, err := os.ReadDir(root)
		if err != nil {
			_, _ = h.Write([]byte("root-error:" + err.Error() + "\n"))
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			_, _ = h.Write([]byte("plugin-dir:" + dir + "\n"))
			manifestData, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
			if err != nil {
				_, _ = h.Write([]byte("manifest-error:" + err.Error() + "\n"))
				continue
			}
			_, _ = h.Write(manifestData)
			var manifest PluginManifest
			if json.Unmarshal(manifestData, &manifest) != nil {
				continue
			}
			for _, relPath := range manifest.Skills {
				_, _ = h.Write([]byte("skill:" + relPath + "\n"))
				path, err := pm.loader.resolveSkillPath(dir, relPath)
				if err != nil {
					_, _ = h.Write([]byte("skill-path-error:" + err.Error() + "\n"))
					continue
				}
				data, err := os.ReadFile(path)
				if err != nil {
					_, _ = h.Write([]byte("skill-read-error:" + err.Error() + "\n"))
					continue
				}
				_, _ = h.Write(data)
			}
		}
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// Reconcile loads enabled plugins, unloads disabled or removed plugins, and
// reloads changed skill files. Invalid plugin files leave the last valid
// version active. Discovery errors prevent pruning plugins that merely became
// unreadable during the scan.
func (pm *PluginManager) Reconcile(ctx context.Context) (int, error) {
	if pm == nil || pm.reg == nil {
		return 0, errors.New("gerenciador de plugins indisponivel")
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	manifests, scanErr := pm.loader.DiscoverAll()
	pm.adoptExistingLocked(manifests)
	seen := make(map[string]struct{}, len(manifests))
	var errs []error
	if scanErr != nil {
		errs = append(errs, scanErr)
	}
	for _, manifest := range manifests {
		if err := ctx.Err(); err != nil {
			return pm.loadedCountLocked(), errors.Join(append(errs, err)...)
		}
		seen[manifest.dir] = struct{}{}
		if err := pm.reconcileOneLocked(manifest); err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: %w", manifest.dir, err))
		}
	}
	if scanErr == nil {
		for dir := range pm.loaded {
			if _, ok := seen[dir]; !ok {
				if err := pm.unloadLocked(dir); err != nil {
					errs = append(errs, fmt.Errorf("removing plugin %s: %w", dir, err))
				}
			}
		}
	}
	return pm.loadedCountLocked(), errors.Join(errs...)
}

// List includes disabled plugins so they can be enabled during this process.
func (pm *PluginManager) List(ctx context.Context) ([]PluginInfo, error) {
	if pm == nil || pm.reg == nil {
		return nil, errors.New("gerenciador de plugins indisponivel")
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	manifests, err := pm.loader.DiscoverAll()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	pm.adoptExistingLocked(manifests)
	infos := make([]PluginInfo, 0, len(manifests))
	for _, manifest := range manifests {
		infos = append(infos, pm.infoLocked(manifest))
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
	return infos, err
}

// SetEnabled atomically updates the selected plugin.json and immediately
// applies its new state to the live registry. The ID must come from List.
func (pm *PluginManager) SetEnabled(ctx context.Context, id string, enabled bool) (PluginInfo, error) {
	if pm == nil || pm.reg == nil {
		return PluginInfo{}, errors.New("gerenciador de plugins indisponivel")
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	manifest, err := pm.findLocked(ctx, id)
	if err != nil {
		return PluginInfo{}, err
	}
	pm.adoptExistingLocked([]PluginManifest{manifest})
	if manifest.Enabled != enabled {
		if err := writePluginEnabled(filepath.Join(manifest.dir, "plugin.json"), enabled); err != nil {
			return pm.infoLocked(manifest), err
		}
		manifest.Enabled = enabled
	}
	err = pm.reconcileOneLocked(manifest)
	return pm.infoLocked(manifest), err
}

// Reload refreshes one plugin; an empty ID reconciles all plugins, including
// newly added and removed directories.
func (pm *PluginManager) Reload(ctx context.Context, id string) (int, error) {
	if strings.TrimSpace(id) == "" {
		return pm.Reconcile(ctx)
	}
	if pm == nil || pm.reg == nil {
		return 0, errors.New("gerenciador de plugins indisponivel")
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	manifest, err := pm.findLocked(ctx, id)
	if err != nil {
		return 0, err
	}
	pm.adoptExistingLocked([]PluginManifest{manifest})
	if err := pm.reconcileOneLocked(manifest); err != nil {
		return len(pm.loaded[manifest.dir]), err
	}
	return len(pm.loaded[manifest.dir]), nil
}

func (pm *PluginManager) findLocked(ctx context.Context, id string) (PluginManifest, error) {
	if err := ctx.Err(); err != nil {
		return PluginManifest{}, err
	}
	manifests, scanErr := pm.loader.DiscoverAll()
	for _, manifest := range manifests {
		if pluginID(manifest.dir) == id {
			return manifest, nil
		}
	}
	if scanErr != nil {
		return PluginManifest{}, fmt.Errorf("plugin %q nao encontrado; falha na descoberta: %w", id, scanErr)
	}
	return PluginManifest{}, fmt.Errorf("plugin %q nao encontrado", id)
}

func (pm *PluginManager) reconcileOneLocked(manifest PluginManifest) error {
	if !manifest.Enabled {
		return pm.unloadLocked(manifest.dir)
	}
	desired := make(map[string]*Skill, len(manifest.Skills))
	for _, relPath := range manifest.Skills {
		path, err := pm.loader.resolveSkillPath(manifest.dir, relPath)
		if err != nil {
			return fmt.Errorf("skill %s: %w", relPath, err)
		}
		s, err := pm.loader.loadSkillFile(path)
		if err != nil {
			return fmt.Errorf("skill %s: %w", relPath, err)
		}
		if previous, dup := desired[s.Name]; dup && previous.Origin != s.Origin {
			return fmt.Errorf("nome de skill duplicado %q no plugin", s.Name)
		}
		desired[s.Name] = s
	}
	// Check every origin before changing any entry. A plugin can update its own
	// file, but cannot replace a builtin, an MCP skill, or another plugin.
	for name, s := range desired {
		if existing, ok := pm.reg.Get(name); ok && existing.Origin != s.Origin {
			return fmt.Errorf("skill %q pertence a outra origem", name)
		}
	}
	current := pm.loaded[manifest.dir]
	if current == nil {
		current = make(map[string]string)
		pm.loaded[manifest.dir] = current
	}
	names := make([]string, 0, len(desired))
	for name := range desired {
		names = append(names, name)
	}
	sort.Strings(names)
	var errs []error
	for _, name := range names {
		if err := pm.reg.TryRegister(desired[name]); err != nil {
			errs = append(errs, fmt.Errorf("registrando skill %q: %w", name, err))
			continue
		}
		current[name] = desired[name].Origin
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	for name, origin := range current {
		if _, stillDeclared := desired[name]; stillDeclared {
			continue
		}
		if err := pm.removeOwnedSkillLocked(name, origin); err != nil {
			errs = append(errs, err)
			continue
		}
		delete(current, name)
	}
	if len(current) == 0 {
		delete(pm.loaded, manifest.dir)
	}
	return errors.Join(errs...)
}

func (pm *PluginManager) unloadLocked(dir string) error {
	var errs []error
	for name, origin := range pm.loaded[dir] {
		if err := pm.removeOwnedSkillLocked(name, origin); err != nil {
			errs = append(errs, err)
			continue
		}
		delete(pm.loaded[dir], name)
	}
	if len(pm.loaded[dir]) == 0 {
		delete(pm.loaded, dir)
	}
	return errors.Join(errs...)
}

func (pm *PluginManager) removeOwnedSkillLocked(name, origin string) error {
	current, exists := pm.reg.Get(name)
	if !exists || current.Origin != origin {
		return nil // already removed or replaced by another source
	}
	return pm.reg.UnregisterIfOrigin(name, origin, false)
}

func (pm *PluginManager) loadedCountLocked() int {
	total := 0
	for _, names := range pm.loaded {
		for name, origin := range names {
			if current, ok := pm.reg.Get(name); ok && current.Origin == origin {
				total++
			}
		}
	}
	return total
}

func (pm *PluginManager) infoLocked(manifest PluginManifest) PluginInfo {
	loaded := make([]string, 0, len(pm.loaded[manifest.dir]))
	for name, origin := range pm.loaded[manifest.dir] {
		if current, ok := pm.reg.Get(name); ok && current.Origin == origin {
			loaded = append(loaded, name)
		}
	}
	sort.Strings(loaded)
	return PluginInfo{
		ID: pluginID(manifest.dir), Name: manifest.Name, Version: manifest.Version,
		Author: manifest.Author, Path: manifest.dir, Enabled: manifest.Enabled,
		SkillFiles: append([]string(nil), manifest.Skills...), LoadedSkills: loaded,
	}
}

func pluginID(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return filepath.Base(dir) + "@" + fmt.Sprintf("%x", sum[:4])
}

func (pm *PluginManager) adoptExistingLocked(manifests []PluginManifest) {
	for _, manifest := range manifests {
		declaredOrigins := make(map[string]struct{}, len(manifest.Skills))
		for _, relPath := range manifest.Skills {
			path, err := pm.loader.resolveSkillPath(manifest.dir, relPath)
			if err == nil {
				declaredOrigins[PluginSkillOrigin(path)] = struct{}{}
			}
		}
		for _, name := range pm.reg.List() {
			s, ok := pm.reg.Get(name)
			if !ok {
				continue
			}
			if _, declared := declaredOrigins[s.Origin]; !declared {
				continue
			}
			if pm.loaded[manifest.dir] == nil {
				pm.loaded[manifest.dir] = make(map[string]string)
			}
			pm.loaded[manifest.dir][name] = s.Origin
		}
	}
}

func writePluginEnabled(path string, enabled bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return fmt.Errorf("manifesto JSON invalido: %s", path)
	}
	fields["enabled"] = json.RawMessage("false")
	if enabled {
		fields["enabled"] = json.RawMessage("true")
	}
	updated, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	updated = append(updated, '\n')
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".plugin-manifest-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(info.Mode().Perm()); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(updated); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
