package skill

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/sashabaranov/go-openai/jsonschema"
)

// PluginManifest is the parsed contents of a plugin.json file found inside a
// plugin directory. The Skills field lists paths to skill YAML files relative
// to the directory that contains plugin.json.
type PluginManifest struct {
	// Name is the human-readable display name of the plugin.
	Name string `json:"name"`
	// Version is an opaque version string (semver encouraged).
	Version string `json:"version"`
	// Author is a free-form author attribution string.
	Author string `json:"author"`
	// Skills lists skill files provided by this plugin, as paths relative to
	// the directory that contains plugin.json.
	Skills []string `json:"skills"`
	// Enabled controls whether the plugin is loaded. Discover() skips
	// manifests whose Enabled field is false.
	Enabled bool `json:"enabled"`

	// dir is the absolute path to the plugin directory; set by Discover.
	dir string
}

// PluginLoader discovers and loads skills from external plugin directories.
// A plugin is a directory containing a plugin.json manifest and one or more
// skill files (YAML or Markdown-with-frontmatter) listed in that manifest.
type PluginLoader struct {
	dirs   []string
	logger *slog.Logger
}

// NewPluginLoader creates a PluginLoader that searches the given directories
// for plugins. A nil logger falls back to the default slog logger.
func NewPluginLoader(dirs []string, logger *slog.Logger) *PluginLoader {
	if logger == nil {
		logger = slog.Default()
	}
	cp := make([]string, len(dirs))
	copy(cp, dirs)
	return &PluginLoader{dirs: cp, logger: logger}
}

// Discover walks each configured search directory, finds sub-directories that
// contain a plugin.json manifest, parses them, and returns only the manifests
// whose Enabled field is true.
//
// Individual errors (missing/malformed manifests, unreadable directories) are
// logged and skipped so one bad plugin cannot block the rest.
func (pl *PluginLoader) Discover() ([]PluginManifest, error) {
	var manifests []PluginManifest

	for _, dir := range pl.dirs {
		info, err := os.Stat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				pl.logger.Debug("plugin search dir not found, skipping",
					slog.String("dir", dir))
				continue
			}
			pl.logger.Warn("cannot stat plugin search dir",
				slog.String("dir", dir),
				slog.String("err", err.Error()))
			continue
		}
		if !info.IsDir() {
			pl.logger.Warn("plugin search path is not a directory",
				slog.String("path", dir))
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			pl.logger.Warn("cannot read plugin search dir",
				slog.String("dir", dir),
				slog.String("err", err.Error()))
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			pluginDir := filepath.Join(dir, entry.Name())
			manifestPath := filepath.Join(pluginDir, "plugin.json")

			data, err := os.ReadFile(manifestPath)
			if err != nil {
				if !os.IsNotExist(err) {
					pl.logger.Warn("cannot read plugin manifest",
						slog.String("path", manifestPath),
						slog.String("err", err.Error()))
				}
				continue
			}

			var m PluginManifest
			if err := json.Unmarshal(data, &m); err != nil {
				pl.logger.Warn("invalid plugin manifest JSON",
					slog.String("path", manifestPath),
					slog.String("err", err.Error()))
				continue
			}

			if !m.Enabled {
				pl.logger.Debug("plugin disabled, skipping",
					slog.String("plugin", m.Name),
					slog.String("dir", pluginDir))
				continue
			}

			m.dir = pluginDir
			manifests = append(manifests, m)
			pl.logger.Debug("discovered plugin",
				slog.String("plugin", m.Name),
				slog.String("version", m.Version),
				slog.String("dir", pluginDir))
		}
	}

	return manifests, nil
}

// LoadPlugin reads the skill files listed in manifest.Skills, builds a *Skill
// for each, and registers them into reg. Per-file errors are accumulated so
// one bad skill file does not block the others; a joined error is returned if
// any files failed.
func (pl *PluginLoader) LoadPlugin(manifest PluginManifest, reg interface{ Register(*Skill) }) error {
	baseDir := manifest.dir
	if baseDir == "" {
		var err error
		baseDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("plugin %q: cannot determine base dir: %w", manifest.Name, err)
		}
	}

	var failed []string
	for _, relPath := range manifest.Skills {
		skillPath := filepath.Join(baseDir, relPath)

		s, err := pl.loadSkillFile(skillPath)
		if err != nil {
			pl.logger.Warn("failed to load plugin skill",
				slog.String("plugin", manifest.Name),
				slog.String("file", skillPath),
				slog.String("err", err.Error()))
			failed = append(failed, fmt.Sprintf("%s: %v", relPath, err))
			continue
		}

		reg.Register(s)
		pl.logger.Debug("registered plugin skill",
			slog.String("plugin", manifest.Name),
			slog.String("skill", s.Name))
	}

	if len(failed) > 0 {
		return fmt.Errorf("plugin %q: %d skill(s) failed to load: %s",
			manifest.Name, len(failed), strings.Join(failed, "; "))
	}
	return nil
}

// LoadAll runs Discover and then LoadPlugin for every enabled manifest,
// registering all valid skills into reg. It returns the total number of
// skills successfully loaded. Per-plugin errors are logged but do not abort
// loading of remaining plugins.
func (pl *PluginLoader) LoadAll(reg interface{ Register(*Skill) }) (int, error) {
	manifests, err := pl.Discover()
	if err != nil {
		return 0, fmt.Errorf("plugin loader: discover: %w", err)
	}

	total := 0
	for _, m := range manifests {
		count, loadErr := pl.loadAndCount(m, reg)
		if loadErr != nil {
			pl.logger.Warn("plugin loaded with errors",
				slog.String("plugin", m.Name),
				slog.String("err", loadErr.Error()))
		}
		total += count
	}

	return total, nil
}

// loadAndCount is like LoadPlugin but also returns the number of skills that
// were successfully registered.
func (pl *PluginLoader) loadAndCount(manifest PluginManifest, reg interface{ Register(*Skill) }) (int, error) {
	baseDir := manifest.dir
	if baseDir == "" {
		var err error
		baseDir, err = os.Getwd()
		if err != nil {
			return 0, fmt.Errorf("plugin %q: cannot determine base dir: %w", manifest.Name, err)
		}
	}

	count := 0
	var failed []string

	for _, relPath := range manifest.Skills {
		skillPath := filepath.Join(baseDir, relPath)

		s, err := pl.loadSkillFile(skillPath)
		if err != nil {
			pl.logger.Warn("failed to load plugin skill",
				slog.String("plugin", manifest.Name),
				slog.String("file", skillPath),
				slog.String("err", err.Error()))
			failed = append(failed, fmt.Sprintf("%s: %v", relPath, err))
			continue
		}

		reg.Register(s)
		count++
		pl.logger.Debug("registered plugin skill",
			slog.String("plugin", manifest.Name),
			slog.String("skill", s.Name))
	}

	if len(failed) > 0 {
		return count, fmt.Errorf("plugin %q: %d skill(s) failed: %s",
			manifest.Name, len(failed), strings.Join(failed, "; "))
	}
	return count, nil
}

// loadSkillFile reads a single skill file and converts it to a *Skill.
//
// Supported formats:
//   - .md / .skill.md  — Markdown with YAML frontmatter, parsed via ParseSkillFile
//     (the same function used by FSSkillLoader)
//   - .yaml / .yml     — JSON-encoded skill definition (pluginSkillDef)
func (pl *PluginLoader) loadSkillFile(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	lower := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(lower, ".md") {
		return skillFromParsed(path, data)
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".yaml" || ext == ".yml" {
		return skillFromJSON(path, data)
	}

	return nil, fmt.Errorf("unsupported skill file extension %q (want .yaml, .yml, or .md)", filepath.Ext(path))
}

// skillFromParsed converts a Markdown-with-frontmatter skill file into a
// *Skill using ParseSkillFile — the same parser used by FSSkillLoader.
func skillFromParsed(path string, data []byte) (*Skill, error) {
	ps, err := ParseSkillFile(path, data)
	if err != nil {
		return nil, err
	}
	if err := ps.Frontmatter.Validate(); err != nil {
		return nil, err
	}
	body := ps.Body
	return &Skill{
		Name:        ps.Frontmatter.Name,
		Description: ps.Frontmatter.Description,
		Parameters:  jsonschema.Definition{Type: jsonschema.Object},
		Execute: func(_ map[string]string) (string, error) {
			return body, nil
		},
	}, nil
}

// pluginSkillDef is the JSON schema for a plugin skill file.
// It intentionally mirrors CustomSkillDef's fields so existing custom skill
// YAML files (when valid JSON-compatible YAML) can be used as plugin skills.
type pluginSkillDef struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  map[string]string `json:"parameters"` // param name → description
	Script      string            `json:"script"`
}

// skillFromJSON decodes a pluginSkillDef from a JSON-encoded .yaml file and
// builds a *Skill from it.
//
// Plugin skill YAML files must use JSON-compatible syntax (valid JSON) because
// this file uses only the standard library (encoding/json) for decoding.
func skillFromJSON(path string, data []byte) (*Skill, error) {
	var def pluginSkillDef
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if strings.TrimSpace(def.Name) == "" {
		return nil, fmt.Errorf("skill file %s: name is required", path)
	}
	if strings.TrimSpace(def.Description) == "" {
		return nil, fmt.Errorf("skill file %s: description is required", path)
	}

	props := make(map[string]jsonschema.Definition, len(def.Parameters))
	required := make([]string, 0, len(def.Parameters))
	for paramName, paramDesc := range def.Parameters {
		props[paramName] = jsonschema.Definition{
			Type:        jsonschema.String,
			Description: paramDesc,
		}
		required = append(required, paramName)
	}

	skillName := def.Name
	script := def.Script

	return &Skill{
		Name:        skillName,
		Description: def.Description,
		Parameters: jsonschema.Definition{
			Type:       jsonschema.Object,
			Properties: props,
			Required:   required,
		},
		Execute: func(_ map[string]string) (string, error) {
			if script == "" {
				return fmt.Sprintf("skill %q has no script defined", skillName), nil
			}
			return fmt.Sprintf("[plugin skill %q loaded; use run_command to invoke scripts]", skillName), nil
		},
	}, nil
}
