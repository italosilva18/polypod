package skill

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/sashabaranov/go-openai/jsonschema"
	"gopkg.in/yaml.v3"
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
	manifests, _ := pl.discover(false)
	return manifests, nil
}

// DiscoverAll includes disabled plugins and reports unreadable or malformed
// manifests. Runtime reconciliation uses errors to avoid mistaking a transient
// read failure for removal of an already loaded plugin.
func (pl *PluginLoader) DiscoverAll() ([]PluginManifest, error) {
	return pl.discover(true)
}

func (pl *PluginLoader) discover(includeDisabled bool) ([]PluginManifest, error) {
	var manifests []PluginManifest
	var errs []error

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
			errs = append(errs, fmt.Errorf("stat plugin dir %s: %w", dir, err))
			continue
		}
		if !info.IsDir() {
			pl.logger.Warn("plugin search path is not a directory",
				slog.String("path", dir))
			errs = append(errs, fmt.Errorf("plugin search path %s is not a directory", dir))
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			pl.logger.Warn("cannot read plugin search dir",
				slog.String("dir", dir),
				slog.String("err", err.Error()))
			errs = append(errs, fmt.Errorf("read plugin dir %s: %w", dir, err))
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
					errs = append(errs, fmt.Errorf("read plugin manifest %s: %w", manifestPath, err))
				}
				continue
			}

			var m PluginManifest
			if err := json.Unmarshal(data, &m); err != nil {
				pl.logger.Warn("invalid plugin manifest JSON",
					slog.String("path", manifestPath),
					slog.String("err", err.Error()))
				errs = append(errs, fmt.Errorf("invalid plugin manifest %s", manifestPath))
				continue
			}

			if !m.Enabled && !includeDisabled {
				pl.logger.Debug("plugin disabled, skipping",
					slog.String("plugin", m.Name),
					slog.String("dir", pluginDir))
				continue
			}

			m.dir, err = filepath.Abs(pluginDir)
			if err != nil {
				errs = append(errs, fmt.Errorf("absolute plugin path %s: %w", pluginDir, err))
				continue
			}
			manifests = append(manifests, m)
			pl.logger.Debug("discovered plugin",
				slog.String("plugin", m.Name),
				slog.String("version", m.Version),
				slog.String("dir", pluginDir))
		}
	}

	return manifests, errors.Join(errs...)
}

// resolveSkillPath keeps a plugin's declared skill paths within that plugin,
// including when a listed file or ancestor is a symlink.
func (pl *PluginLoader) resolveSkillPath(baseDir, relPath string) (string, error) {
	if !filepath.IsLocal(relPath) {
		return "", fmt.Errorf("skill path must stay within plugin: %q", relPath)
	}
	base, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		return "", err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(baseDir, relPath))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("skill path leaves plugin: %q", relPath)
	}
	return path, nil
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
		skillPath, err := pl.resolveSkillPath(baseDir, relPath)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", relPath, err))
			continue
		}
		s, err := pl.loadSkillFile(skillPath)
		if err != nil {
			pl.logger.Warn("failed to load plugin skill",
				slog.String("plugin", manifest.Name),
				slog.String("file", skillPath),
				slog.String("err", err.Error()))
			failed = append(failed, fmt.Sprintf("%s: %v", relPath, err))
			continue
		}

		if err := registerPluginSkill(reg, s); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", relPath, err))
			continue
		}
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
		skillPath, err := pl.resolveSkillPath(baseDir, relPath)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", relPath, err))
			continue
		}
		s, err := pl.loadSkillFile(skillPath)
		if err != nil {
			pl.logger.Warn("failed to load plugin skill",
				slog.String("plugin", manifest.Name),
				slog.String("file", skillPath),
				slog.String("err", err.Error()))
			failed = append(failed, fmt.Sprintf("%s: %v", relPath, err))
			continue
		}

		if err := registerPluginSkill(reg, s); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", relPath, err))
			continue
		}
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

func registerPluginSkill(reg interface{ Register(*Skill) }, s *Skill) error {
	if checked, ok := reg.(interface{ TryRegister(*Skill) error }); ok {
		return checked.TryRegister(s)
	}
	reg.Register(s)
	return nil
}

// loadSkillFile reads a single skill file and converts it to a *Skill.
//
// Supported formats:
//   - .md / .skill.md  — Markdown with YAML frontmatter, parsed via ParseSkillFile
//     (the same function used by FSSkillLoader)
//   - .yaml / .yml     — YAML skill definition (pluginSkillDef)
func (pl *PluginLoader) loadSkillFile(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	lower := strings.ToLower(filepath.Base(path))
	var s *Skill
	if strings.HasSuffix(lower, ".md") {
		s, err = skillFromParsed(path, data)
	} else {
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".yaml" || ext == ".yml" {
			s, err = skillFromYAML(path, data)
		} else {
			return nil, fmt.Errorf("unsupported skill file extension %q (want .yaml, .yml, or .md)", filepath.Ext(path))
		}
	}
	if err != nil {
		return nil, err
	}
	s.Origin = PluginSkillOrigin(path)
	return s, nil
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
		Origin:      FileSkillOrigin(path),
		Parameters:  jsonschema.Definition{Type: jsonschema.Object},
		Execute: func(_ map[string]string) (string, error) {
			return body, nil
		},
	}, nil
}

// pluginSkillDef is the schema for a YAML plugin skill file.
// It mirrors CustomSkillDef's fields so existing custom skills can be used as
// plugin skills. JSON documents remain valid YAML input.
type pluginSkillDef struct {
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description" yaml:"description"`
	Parameters  map[string]string `json:"parameters" yaml:"parameters"` // param name → description
	Script      string            `json:"script" yaml:"script"`
}

// skillFromYAML decodes a pluginSkillDef from YAML or JSON and builds a *Skill.
func skillFromYAML(path string, data []byte) (*Skill, error) {
	var def pluginSkillDef
	var err error
	if json.Valid(data) {
		// Preserve the existing JSON decoding behavior for JSON-formatted files.
		err = json.Unmarshal(data, &def)
	} else {
		err = yaml.Unmarshal(data, &def)
	}
	if err != nil {
		// YAML type errors can quote source values, which may contain secrets.
		return nil, fmt.Errorf("invalid plugin skill YAML or JSON syntax or field types")
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
		Origin:      FileSkillOrigin(path),
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
