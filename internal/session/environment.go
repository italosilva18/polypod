package session

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// SessionEnvironment holds detected environment information for a session.
type SessionEnvironment struct {
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Shell       string `json:"shell"`
	ProjectRoot string `json:"project_root"`
	Cwd         string `json:"cwd"`

	// Git info (empty if not a git repo).
	GitBranch  string   `json:"git_branch,omitempty"`
	GitStatus  string   `json:"git_status,omitempty"`
	GitRemotes []string `json:"git_remotes,omitempty"`
	GitRoot    string   `json:"git_root,omitempty"`

	// Project detection.
	ProjectType      string `json:"project_type,omitempty"`
	ProjectLanguage  string `json:"project_language,omitempty"`
	ProjectFramework string `json:"project_framework,omitempty"`
}

// DetectEnvironment auto-detects all environment information for the current
// working directory (or the provided directory).
func DetectEnvironment(dir string) *SessionEnvironment {
	if dir == "" {
		dir, _ = os.Getwd()
	}

	env := &SessionEnvironment{
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Shell:       detectShell(),
		ProjectRoot: detectProjectRoot(dir),
		Cwd:         dir,
	}

	env.GitBranch = detectGitBranch(dir)
	env.GitStatus = detectGitStatus(dir)
	env.GitRemotes = detectGitRemotes(dir)
	env.GitRoot = detectGitRoot(dir)

	lang, framework, ptype := detectProjectType(dir)
	env.ProjectLanguage = lang
	env.ProjectFramework = framework
	env.ProjectType = ptype

	return env
}

// --- Session environment scripts (hook env files) ---

// HookEnvPriority maps hook event names to their sort order.
var hookEnvPriority = map[string]int{
	"setup":        0,
	"sessionstart": 1,
	"cwdchanged":   2,
	"filechanged":  3,
}

var hookEnvRegex = regexp.MustCompile(`^(setup|sessionstart|cwdchanged|filechanged)-hook-(\d+)\.sh$`)

// SessionEnvManager manages session environment scripts from hooks.
type SessionEnvManager struct {
	mu           sync.Mutex
	sessionID    string
	configDir    string
	cachedScript *string // nil = not loaded, empty = checked/none
	logger       *slog.Logger
}

// NewSessionEnvManager creates a manager for session environment scripts.
func NewSessionEnvManager(sessionID, configDir string, logger *slog.Logger) *SessionEnvManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &SessionEnvManager{
		sessionID: sessionID,
		configDir: configDir,
		logger:    logger,
	}
}

// SessionEnvDirPath returns the directory for session env files.
func (m *SessionEnvManager) SessionEnvDirPath() (string, error) {
	dir := filepath.Join(m.configDir, "session-env", m.sessionID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating session env dir: %w", err)
	}
	return dir, nil
}

// HookEnvFilePath returns the path for a specific hook env file.
func (m *SessionEnvManager) HookEnvFilePath(hookEvent string, hookIndex int) (string, error) {
	dir, err := m.SessionEnvDirPath()
	if err != nil {
		return "", err
	}
	prefix := strings.ToLower(hookEvent)
	return filepath.Join(dir, fmt.Sprintf("%s-hook-%d.sh", prefix, hookIndex)), nil
}

// ClearCwdEnvFiles clears all cwd-related and file-changed hook env files.
func (m *SessionEnvManager) ClearCwdEnvFiles() error {
	dir, err := m.SessionEnvDirPath()
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		if (strings.HasPrefix(name, "filechanged-hook-") || strings.HasPrefix(name, "cwdchanged-hook-")) &&
			hookEnvRegex.MatchString(name) {
			if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
				m.logger.Warn("failed to clear cwd env file", "file", name, "error", err)
			}
		}
	}
	return nil
}

// InvalidateCache forces the next GetEnvironmentScript call to reload.
func (m *SessionEnvManager) InvalidateCache() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cachedScript = nil
	m.logger.Debug("invalidated session environment cache")
}

// GetEnvironmentScript returns the combined session environment script from
// hook files and env file, or "" if none exist. Results are cached.
func (m *SessionEnvManager) GetEnvironmentScript() string {
	if runtime.GOOS == "windows" {
		m.logger.Debug("session environment not supported on Windows")
		return ""
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cachedScript != nil {
		return *m.cachedScript
	}

	var scripts []string

	// Check for env file from parent process.
	if envFile := os.Getenv("POLYPOD_ENV_FILE"); envFile != "" {
		data, err := os.ReadFile(envFile)
		if err == nil {
			content := strings.TrimSpace(string(data))
			if content != "" {
				scripts = append(scripts, content)
				m.logger.Debug("loaded env from POLYPOD_ENV_FILE", "file", envFile, "len", len(content))
			}
		} else if !os.IsNotExist(err) {
			m.logger.Warn("failed to read POLYPOD_ENV_FILE", "error", err)
		}
	}

	// Load hook environment files.
	dir, err := m.SessionEnvDirPath()
	if err == nil {
		entries, err := os.ReadDir(dir)
		if err == nil {
			var hookFiles []string
			for _, e := range entries {
				if hookEnvRegex.MatchString(e.Name()) {
					hookFiles = append(hookFiles, e.Name())
				}
			}
			sort.Slice(hookFiles, func(i, j int) bool {
				return sortHookEnvFiles(hookFiles[i], hookFiles[j])
			})

			for _, name := range hookFiles {
				data, err := os.ReadFile(filepath.Join(dir, name))
				if err == nil {
					content := strings.TrimSpace(string(data))
					if content != "" {
						scripts = append(scripts, content)
					}
				}
			}
			if len(hookFiles) > 0 {
				m.logger.Debug("loaded hook env files", "count", len(hookFiles))
			}
		}
	}

	if len(scripts) == 0 {
		m.logger.Debug("no session environment scripts found")
		empty := ""
		m.cachedScript = &empty
		return ""
	}

	combined := strings.Join(scripts, "\n")
	m.cachedScript = &combined
	m.logger.Debug("session environment script ready", "len", len(combined))
	return combined
}

func sortHookEnvFiles(a, b string) bool {
	aMatch := hookEnvRegex.FindStringSubmatch(a)
	bMatch := hookEnvRegex.FindStringSubmatch(b)
	aType, bType := "", ""
	if aMatch != nil {
		aType = aMatch[1]
	}
	if bMatch != nil {
		bType = bMatch[1]
	}
	if aType != bType {
		aPri, aOk := hookEnvPriority[aType]
		bPri, bOk := hookEnvPriority[bType]
		if !aOk {
			aPri = 99
		}
		if !bOk {
			bPri = 99
		}
		return aPri < bPri
	}
	// Same type, sort by index (embedded in filename).
	return a < b
}

// --- Detection helpers ---

func detectShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return filepath.Base(s)
	}
	if s := os.Getenv("COMSPEC"); s != "" {
		return filepath.Base(s)
	}
	return "unknown"
}

func detectProjectRoot(dir string) string {
	// Walk up looking for common project markers.
	markers := []string{".git", "go.mod", "package.json", "Cargo.toml", "pyproject.toml", "Makefile"}
	current := dir
	for {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(current, m)); err == nil {
				return current
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return dir // fallback to original dir
		}
		current = parent
	}
}

func detectGitBranch(dir string) string {
	out, err := execGit(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func detectGitStatus(dir string) string {
	out, err := execGit(dir, "status", "--porcelain")
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return "clean"
	}
	return fmt.Sprintf("%d changes", len(lines))
}

func detectGitRemotes(dir string) []string {
	out, err := execGit(dir, "remote", "-v")
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	var remotes []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			name := parts[0]
			if _, ok := seen[name]; !ok {
				seen[name] = struct{}{}
				remotes = append(remotes, name)
			}
		}
	}
	return remotes
}

func detectGitRoot(dir string) string {
	out, err := execGit(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func detectProjectType(dir string) (language, framework, ptype string) {
	// Check for language-specific files.
	checks := []struct {
		file      string
		lang      string
		framework string
		ptype     string
	}{
		{"go.mod", "go", "", "module"},
		{"package.json", "javascript", "", "node"},
		{"tsconfig.json", "typescript", "", "node"},
		{"Cargo.toml", "rust", "", "cargo"},
		{"pyproject.toml", "python", "", "python"},
		{"requirements.txt", "python", "", "python"},
		{"Gemfile", "ruby", "", "ruby"},
		{"pom.xml", "java", "", "maven"},
		{"build.gradle", "java", "", "gradle"},
		{"CMakeLists.txt", "c/c++", "", "cmake"},
	}

	for _, c := range checks {
		if _, err := os.Stat(filepath.Join(dir, c.file)); err == nil {
			language = c.lang
			ptype = c.ptype

			// Framework detection for node projects.
			if c.lang == "javascript" || c.lang == "typescript" {
				framework = detectNodeFramework(dir)
			}
			return language, framework, ptype
		}
	}
	return "", "", ""
}

func detectNodeFramework(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}
	content := string(data)
	switch {
	case strings.Contains(content, `"next"`):
		return "nextjs"
	case strings.Contains(content, `"react"`):
		return "react"
	case strings.Contains(content, `"vue"`):
		return "vue"
	case strings.Contains(content, `"svelte"`):
		return "svelte"
	case strings.Contains(content, `"express"`):
		return "express"
	case strings.Contains(content, `"fastify"`):
		return "fastify"
	}
	return ""
}

func execGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
