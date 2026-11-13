package projectchecks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Config controls project-local verification and formatting.
type Config struct {
	Verifiers     map[string]string   `yaml:"verifiers"`
	AutoRun       AutoRun             `yaml:"auto_run"`
	FormatOnWrite bool                `yaml:"format_on_write"`
	Formatters    map[string][]string `yaml:"formatters"`
}

type AutoRun struct {
	OnSave   bool `yaml:"on_save"`
	OnTurn   bool `yaml:"on_turn"`
	OnCommit bool `yaml:"on_commit"`
}

type configSnapshotKey struct{}

// WithConfigSnapshot pins the verifier configuration for one agent turn.
// A file rewritten during the turn cannot become an automatic shell command.
func WithConfigSnapshot(ctx context.Context, start string) context.Context {
	stamp, err := configStamp(start)
	if err != nil {
		stamp = "unreadable"
	}
	return context.WithValue(ctx, configSnapshotKey{}, stamp)
}

// ConfigChanged reports whether the project command configuration changed
// after the start of a turn. Contexts without a snapshot are unaffected.
func ConfigChanged(ctx context.Context, start string) bool {
	before, ok := ctx.Value(configSnapshotKey{}).(string)
	if !ok {
		return false
	}
	after, err := configStamp(start)
	return err != nil || before != after
}

func configStamp(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		path := filepath.Join(dir, ".polypod", "verifiers.yaml")
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			hash := sha256.Sum256(data)
			return fmt.Sprintf("%s:%x", path, hash), nil
		}
		if !os.IsNotExist(readErr) {
			return "", readErr
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// Find walks towards the filesystem root, allowing a CLI started in a subdir.
func Find(start string) (string, Config, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", Config{}, err
	}
	for {
		path := filepath.Join(dir, ".polypod", "verifiers.yaml")
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			var cfg Config
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return "", Config{}, fmt.Errorf("lendo %s: %w", path, err)
			}
			return dir, cfg, nil
		}
		if !os.IsNotExist(readErr) {
			return "", Config{}, readErr
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", Config{}, nil
		}
		dir = parent
	}
}

type Result struct {
	Name   string
	Output string
	Err    error
}

// Run executes the configured checks in stable order with a bound per command.
func Run(ctx context.Context, start, event string) ([]Result, error) {
	root, cfg, err := Find(start)
	if err != nil || root == "" {
		return nil, err
	}
	allowed := map[string]bool{"save": cfg.AutoRun.OnSave, "turn": cfg.AutoRun.OnTurn, "commit": cfg.AutoRun.OnCommit, "manual": true}
	if !allowed[event] {
		return nil, nil
	}
	names := make([]string, 0, len(cfg.Verifiers))
	for name := range cfg.Verifiers {
		names = append(names, name)
	}
	sort.Strings(names)
	results := make([]Result, 0, len(names))
	for _, name := range names {
		command := strings.TrimSpace(cfg.Verifiers[name])
		if command == "" {
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		cmd := exec.CommandContext(checkCtx, "sh", "-c", command)
		cmd.Dir = root
		cmd.WaitDelay = 2 * time.Second
		var out limitedBuffer
		cmd.Stdout, cmd.Stderr = &out, &out
		runErr := cmd.Run()
		if checkCtx.Err() != nil {
			runErr = checkCtx.Err()
		}
		cancel()
		results = append(results, Result{Name: name, Output: out.String(), Err: runErr})
		if ctx.Err() != nil {
			break
		}
	}
	return results, nil
}

// Format applies only an explicitly configured formatter inside its project.
func Format(ctx context.Context, path string) (string, error) {
	root, cfg, err := Find(filepath.Dir(path))
	if err != nil || root == "" || !cfg.FormatOnWrite {
		return "", err
	}
	rootAbs, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	fileAbs, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, fileAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("arquivo fora do projeto: %s", path)
	}
	args := cfg.Formatters[filepath.Ext(path)]
	if len(args) == 0 {
		return "", nil
	}
	resolved := make([]string, len(args))
	for i, arg := range args {
		resolved[i] = strings.ReplaceAll(arg, "{file}", fileAbs)
	}
	formatCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(formatCtx, resolved[0], resolved[1:]...)
	cmd.Dir = rootAbs
	cmd.WaitDelay = 2 * time.Second
	var out limitedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("formatando %s: %w: %s", rel, err, out.String())
	}
	return resolved[0], nil
}

type limitedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if b.Len() < 4096 {
		remaining := 4096 - b.Len()
		if len(p) > remaining {
			p = p[:remaining]
		}
		if _, err := b.Buffer.Write(p); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func FormatResults(results []Result) string {
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Verificações do projeto:\n")
	for _, result := range results {
		status := "OK"
		if result.Err != nil {
			status = "FALHOU: " + result.Err.Error()
		}
		fmt.Fprintf(&b, "- %s: %s\n", result.Name, status)
		if result.Err != nil && result.Output != "" {
			b.WriteString(result.Output)
			if !strings.HasSuffix(result.Output, "\n") {
				b.WriteByte('\n')
			}
		}
	}
	return strings.TrimSpace(b.String())
}
