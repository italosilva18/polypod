package skill

// fswatch.go — hot-reload of skill YAML files via stdlib polling.
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
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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

// fileState tracks the last known state of a watched YAML file.
type fileState struct {
	modTime time.Time
	size    int64
	// skillName is the name derived from the parsed skill, used for Unregister.
	skillName string
}

// SkillWatcher watches a list of directories for .yaml/.yml changes and
// keeps the registry in sync.
type SkillWatcher struct {
	dirs   []string
	loader *FSSkillLoader // retained for future SKILL.md reloads; unused by polling path
	reg    skillRegistrar
	log    *slog.Logger

	mu       sync.Mutex
	states   map[string]*fileState  // abs path -> state
	debounce map[string]*time.Timer // abs path -> pending timer
}

// NewSkillWatcher creates a SkillWatcher that monitors dirs for YAML skill
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

// scan walks each configured directory, discovers .yaml/.yml files, and
// compares them against the last-known state. New or modified files are
// queued for reload; missing files trigger an unregister call.
func (sw *SkillWatcher) scan(ctx context.Context) {
	seen := make(map[string]struct{})

	for _, dir := range sw.dirs {
		if ctx.Err() != nil {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				sw.log.Warn("skill watcher: cannot read dir", "dir", dir, "err", err)
			}
			continue
		}

		for _, entry := range entries {
			if ctx.Err() != nil {
				return
			}
			ext := filepath.Ext(entry.Name())
			if ext != ".yaml" && ext != ".yml" {
				continue
			}

			abs := filepath.Join(dir, entry.Name())
			seen[abs] = struct{}{}

			info, err := entry.Info()
			if err != nil {
				sw.log.Warn("skill watcher: stat error", "path", abs, "err", err)
				continue
			}

			sw.mu.Lock()
			prev, exists := sw.states[abs]
			changed := !exists ||
				prev.modTime != info.ModTime() ||
				prev.size != info.Size()
			sw.mu.Unlock()

			if changed {
				sw.scheduleReload(abs)
			}
		}
	}

	// Detect deletions: files we knew about that are no longer on disk.
	sw.mu.Lock()
	var deleted []string
	for abs := range sw.states {
		if _, ok := seen[abs]; !ok {
			deleted = append(deleted, abs)
		}
	}
	sw.mu.Unlock()

	for _, abs := range deleted {
		sw.handleDelete(abs)
	}
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

// handleReload reads, parses, and re-registers the skill from the given YAML
// file. It updates the tracked state on success.
func (sw *SkillWatcher) handleReload(abs string) {
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

	var def CustomSkillDef
	if err := yaml.Unmarshal(data, &def); err != nil {
		sw.log.Warn("skill watcher: parse error", "path", abs, "err", err)
		return
	}

	if def.Name == "" || def.Script == "" {
		sw.log.Warn("skill watcher: skill missing name or script, skipping", "path", abs)
		return
	}

	// Unregister old version before registering new one (name may have changed).
	sw.mu.Lock()
	prev, hadPrev := sw.states[abs]
	sw.mu.Unlock()

	if hadPrev && prev.skillName != "" && prev.skillName != def.Name {
		sw.reg.Unregister(prev.skillName)
		sw.log.Info("skill watcher: unregistered renamed skill", "old_name", prev.skillName, "path", abs)
	}

	s := buildSkillFromDef(&def)
	sw.reg.Register(s)

	sw.mu.Lock()
	sw.states[abs] = &fileState{
		modTime:   info.ModTime(),
		size:      info.Size(),
		skillName: def.Name,
	}
	sw.mu.Unlock()

	sw.log.Info("skill watcher: reloaded skill", "name", def.Name, "path", abs)
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
		sw.reg.Unregister(st.skillName)
		sw.log.Info("skill watcher: unregistered deleted skill", "name", st.skillName, "path", abs)
	}
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
