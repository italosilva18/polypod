package skill

// fswatch.go — hot-reload of YAML and Markdown skills via stdlib polling.
//
// fsnotify is NOT present in go.mod, so we fall back to a poll loop that
// checks for changes every 5 seconds using time.Ticker. Each tracked file
// is compared against its last-known os.FileInfo (size + modtime). A
// 100 ms debounce window per file prevents rapid successive writes from
// triggering multiple reloads.

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/sashabaranov/go-openai/jsonschema"
	"gopkg.in/yaml.v3"
)

const (
	pollInterval   = 5 * time.Second
	debounceWindow = 100 * time.Millisecond
)

// skillRegistrar is the minimal interface SkillWatcher needs from a registry.
// *Registry satisfies this interface.
type skillRegistrar interface {
	Register(s *Skill)
	Unregister(name string)
}

// fileState tracks the last known state of a watched skill file.
type fileState struct {
	modTime time.Time
	size    int64
	// skillName is the name derived from the parsed skill, used for Unregister.
	skillName string
	trusted   bool
	origin    string
}

// SkillWatcher watches a list of directories for .yaml/.yml and SKILL.md changes and
// keeps the registry in sync.
type SkillWatcher struct {
	dirs   []string
	loader *FSSkillLoader
	reg    skillRegistrar
	log    *slog.Logger

	mu             sync.Mutex
	states         map[string]*fileState  // abs path -> state
	debounce       map[string]*time.Timer // abs path -> pending timer
	onChange       func(name string, exists bool)
	beforeRegister func(path, name string, content []byte) (bool, error)
}

// NewSkillWatcher creates a SkillWatcher that monitors dirs for skill
// files. It does not start the background goroutine yet — call Start for that.
func NewSkillWatcher(
	dirs []string,
	loader *FSSkillLoader,
	reg interface {
		Register(*Skill)
		Unregister(name string)
	},
	logger *slog.Logger,
) (*SkillWatcher, error) {
	if logger == nil {
		logger = slog.Default()
	}

	cp := make([]string, len(dirs))
	copy(cp, dirs)

	sw := &SkillWatcher{
		dirs:     cp,
		loader:   loader,
		reg:      reg,
		log:      logger,
		states:   make(map[string]*fileState),
		debounce: make(map[string]*time.Timer),
	}
	return sw, nil
}

// SetOnChange is called after a skill is registered, updated, or removed.
// A caller can use it to refresh tool adapters that wrap registry entries.
// Set it before Start so the first scan also updates those adapters.
func (sw *SkillWatcher) SetOnChange(fn func(name string, exists bool)) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.onChange = fn
}

// SetBeforeRegister validates the exact file bytes before a watcher reload
// replaces a registered skill. Returning an error retains the last valid
// version, if any. Set this before Start.
func (sw *SkillWatcher) SetBeforeRegister(fn func(path, name string, content []byte) (bool, error)) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.beforeRegister = fn
}

func (sw *SkillWatcher) notifyChange(name string, exists bool) {
	sw.mu.Lock()
	fn := sw.onChange
	sw.mu.Unlock()
	if fn != nil {
		fn(name, exists)
	}
}

// Start begins the poll loop in a background goroutine. It returns
// immediately; the loop runs until ctx is cancelled.
func (sw *SkillWatcher) Start(ctx context.Context) {
	// Perform an initial scan so we know the baseline state without waiting
	// for the first tick.
	sw.scan(ctx)

	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				sw.log.Debug("skill watcher stopped", "reason", ctx.Err())
				return
			case <-ticker.C:
				sw.scan(ctx)
			}
		}
	}()
}

// scan walks each configured directory, discovers .yaml/.yml and SKILL.md files, and
// compares them against the last-known state. New or modified files are
// queued for reload; missing files trigger an unregister call.
func (sw *SkillWatcher) scan(ctx context.Context) {
	seen := make(map[string]struct{})
	var unreadable []string

	for _, dir := range sw.dirs {
		if ctx.Err() != nil {
			return
		}
		err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				sw.log.Warn("skill watcher: cannot read path", "path", path, "err", walkErr)
				unreadable = append(unreadable, path)
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if entry.IsDir() {
				if hasPluginManifest(path) {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(entry.Name())
			isDirectYAML := filepath.Dir(path) == dir && (ext == ".yaml" || ext == ".yml")
			if !isSkillFile(entry.Name()) && !isDirectYAML {
				return nil
			}
			seen[path] = struct{}{}

			info, err := entry.Info()
			if err != nil {
				sw.log.Warn("skill watcher: stat error", "path", path, "err", err)
				return nil
			}

			sw.mu.Lock()
			prev, exists := sw.states[path]
			changed := !exists ||
				prev.modTime != info.ModTime() ||
				prev.size != info.Size()
			sw.mu.Unlock()

			if changed {
				sw.scheduleReload(path)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) && ctx.Err() == nil {
			sw.log.Warn("skill watcher: cannot scan dir", "dir", dir, "err", err)
			unreadable = append(unreadable, dir)
		}
	}

	// Detect deletions: files we knew about that are no longer on disk.
	sw.mu.Lock()
	var deleted []string
	for abs := range sw.states {
		if _, ok := seen[abs]; !ok {
			// A temporary read failure is not evidence that a skill was deleted.
			if !withinAnyPath(abs, unreadable) {
				deleted = append(deleted, abs)
			}
		}
	}
	sw.mu.Unlock()

	for _, abs := range deleted {
		sw.handleDelete(abs)
	}
}

func withinAnyPath(path string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// scheduleReload debounces reload calls for a single file path. If another
// change arrives within the debounce window the timer is reset.
func (sw *SkillWatcher) scheduleReload(abs string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	if t, ok := sw.debounce[abs]; ok {
		t.Reset(debounceWindow)
		return
	}
	sw.debounce[abs] = time.AfterFunc(debounceWindow, func() {
		sw.mu.Lock()
		delete(sw.debounce, abs)
		sw.mu.Unlock()
		sw.handleReload(abs)
	})
}

// handleReload reads, parses, and re-registers the skill from the given
// file. It updates the tracked state on success.
func (sw *SkillWatcher) handleReload(abs string) {
	if insidePluginDirectory(abs) {
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		sw.log.Warn("skill watcher: read error", "path", abs, "err", err)
		return
	}

	info, err := os.Stat(abs)
	if err != nil {
		sw.log.Warn("skill watcher: stat after read error", "path", abs, "err", err)
		return
	}

	var s *Skill
	if isSkillFile(filepath.Base(abs)) {
		parsed, parseErr := skillFromParsed(abs, data)
		if parseErr != nil {
			sw.log.Warn("skill watcher: parse error", "path", abs, "err", parseErr)
			return
		}
		s = parsed
	} else {
		var def CustomSkillDef
		if err := yaml.Unmarshal(data, &def); err != nil {
			sw.log.Warn("skill watcher: parse error", "path", abs, "err", err)
			return
		}
		if def.Name == "" || def.Script == "" {
			sw.log.Warn("skill watcher: skill missing name or script, skipping", "path", abs)
			return
		}
		s = buildSkillFromDef(&def)
	}
	s.Origin = FileSkillOrigin(abs)

	sw.mu.Lock()
	beforeRegister := sw.beforeRegister
	sw.mu.Unlock()
	trusted := false
	if beforeRegister != nil {
		trusted, err = beforeRegister(abs, s.Name, data)
		if err != nil {
			sw.log.Warn("skill watcher: registration rejected", "path", abs, "name", s.Name, "err", err)
			return
		}
	}

	// A rejected replacement must leave the previous registered version intact.
	sw.mu.Lock()
	prev, hadPrev := sw.states[abs]
	sw.mu.Unlock()

	if trusted {
		if reg, ok := sw.reg.(interface{ RegisterTrusted(*Skill) error }); ok {
			if err := reg.RegisterTrusted(s); err != nil {
				sw.log.Warn("skill watcher: trusted registration rejected", "path", abs, "name", s.Name, "err", err)
				return
			}
		} else {
			sw.reg.Register(s)
		}
	} else if reg, ok := sw.reg.(interface{ TryRegister(*Skill) error }); ok {
		if err := reg.TryRegister(s); err != nil {
			sw.log.Warn("skill watcher: registration rejected", "path", abs, "name", s.Name, "err", err)
			return
		}
	} else {
		sw.reg.Register(s)
	}
	if hadPrev && prev.skillName != "" && prev.skillName != s.Name {
		if err := sw.unregisterPrevious(prev); err != nil {
			sw.log.Warn("skill watcher: old name protected", "name", prev.skillName, "err", err)
		} else {
			sw.log.Info("skill watcher: unregistered renamed skill", "old_name", prev.skillName, "path", abs)
			sw.notifyChange(prev.skillName, false)
		}
	}

	sw.mu.Lock()
	sw.states[abs] = &fileState{
		modTime:   info.ModTime(),
		size:      info.Size(),
		skillName: s.Name,
		trusted:   trusted,
		origin:    s.Origin,
	}
	sw.mu.Unlock()

	sw.notifyChange(s.Name, true)
	sw.log.Info("skill watcher: reloaded skill", "name", s.Name, "path", abs)
}

func insidePluginDirectory(path string) bool {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if hasPluginManifest(dir) {
			return true
		}
		if parent := filepath.Dir(dir); parent == dir {
			return false
		}
	}
}

// handleDelete unregisters the skill that was loaded from the given path and
// removes it from the tracked-state map.
func (sw *SkillWatcher) handleDelete(abs string) {
	sw.mu.Lock()
	st, ok := sw.states[abs]
	if ok {
		delete(sw.states, abs)
	}
	// Cancel any pending debounce timer for this file.
	if t, ok2 := sw.debounce[abs]; ok2 {
		t.Stop()
		delete(sw.debounce, abs)
	}
	sw.mu.Unlock()

	if ok && st.skillName != "" {
		if err := sw.unregisterPrevious(st); err != nil {
			sw.log.Warn("skill watcher: deletion protected", "name", st.skillName, "err", err)
		} else {
			sw.notifyChange(st.skillName, false)
			sw.log.Info("skill watcher: unregistered deleted skill", "name", st.skillName, "path", abs)
		}
	}
}

func (sw *SkillWatcher) unregisterPrevious(st *fileState) error {
	if reg, ok := sw.reg.(interface {
		UnregisterIfOrigin(string, string, bool) error
	}); ok {
		return reg.UnregisterIfOrigin(st.skillName, st.origin, st.trusted)
	}
	if reg, ok := sw.reg.(interface{ Get(string) (*Skill, bool) }); ok {
		current, exists := reg.Get(st.skillName)
		if !exists || current.Origin != st.origin {
			return fmt.Errorf("skill %q não pertence mais a %s", st.skillName, st.origin)
		}
	}
	if st.trusted {
		if reg, ok := sw.reg.(interface{ UnregisterTrusted(string) error }); ok {
			return reg.UnregisterTrusted(st.skillName)
		}
	}
	if reg, ok := sw.reg.(interface{ TryUnregister(string) error }); ok {
		return reg.TryUnregister(st.skillName)
	}
	sw.reg.Unregister(st.skillName)
	return nil
}

// buildSkillFromDef converts a CustomSkillDef into a *Skill without touching
// the Registry directly, so it can be used with any skillRegistrar.
func buildSkillFromDef(def *CustomSkillDef) *Skill {
	props := make(map[string]jsonschema.Definition, len(def.Parameters))
	required := make([]string, 0, len(def.Parameters))
	for name, desc := range def.Parameters {
		props[name] = jsonschema.Definition{
			Type:        jsonschema.String,
			Description: desc,
		}
		required = append(required, name)
	}

	scriptTemplate := def.Script // capture for closure

	return &Skill{
		Name:        def.Name,
		Description: def.Description,
		Parameters: jsonschema.Definition{
			Type:       jsonschema.Object,
			Properties: props,
			Required:   required,
		},
		Execute: func(args map[string]string) (string, error) {
			tmpl, err := template.New("script").Parse(scriptTemplate)
			if err != nil {
				return "", fmt.Errorf("parsing script template: %w", err)
			}

			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, args); err != nil {
				return "", fmt.Errorf("rendering script: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), ExecTimeout)
			defer cancel()

			cmd := exec.CommandContext(ctx, "bash", "-c", buf.String())
			out, err := cmd.CombinedOutput()
			if ctx.Err() == context.DeadlineExceeded {
				return string(out) + "\n[timeout: script excedeu 30s]", nil
			}
			if err != nil {
				return string(out) + "\n[exit: " + err.Error() + "]", nil
			}
			return string(out), nil
		},
	}
}
