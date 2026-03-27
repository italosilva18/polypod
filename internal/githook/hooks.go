package githook

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const prepareCommitMsg = `#!/bin/sh
# Polypod AI commit message generator
# Installed by: polypod hook install

# Skip if message already provided (amend, merge, etc.)
if [ "$2" != "" ]; then
    exit 0
fi

# Generate commit message from staged diff
STAGED=$(git diff --cached --stat)
if [ -z "$STAGED" ]; then
    exit 0
fi

# Try polypod headless mode
if command -v polypod >/dev/null 2>&1; then
    MSG=$(git diff --cached | polypod -p "Gere uma mensagem de commit Conventional Commits para este diff. Retorne APENAS a mensagem, sem explicacao." --output-format text 2>/dev/null)
    if [ $? -eq 0 ] && [ -n "$MSG" ]; then
        echo "$MSG" > "$1"
    fi
fi
`

const prePush = `#!/bin/sh
# Polypod pre-push hook
# Installed by: polypod hook install

# Run tests before pushing
echo "Polypod: executando testes antes do push..."

# Detect and run tests
if [ -f "go.mod" ]; then
    go test ./... || exit 1
elif [ -f "package.json" ]; then
    npm test || exit 1
elif [ -f "pyproject.toml" ] || [ -f "requirements.txt" ]; then
    python3 -m pytest -q || exit 1
fi

echo "Polypod: testes OK, prosseguindo com push."
`

// Install installs git hooks in the current repository.
func Install() ([]string, error) {
	gitDir, err := findGitDir()
	if err != nil {
		return nil, fmt.Errorf("nao e um repositorio git: %w", err)
	}

	hooksDir := filepath.Join(gitDir, "hooks")
	os.MkdirAll(hooksDir, 0755)

	var installed []string

	// prepare-commit-msg
	path := filepath.Join(hooksDir, "prepare-commit-msg")
	if err := writeHook(path, prepareCommitMsg); err != nil {
		return installed, err
	}
	installed = append(installed, "prepare-commit-msg")

	// pre-push
	path = filepath.Join(hooksDir, "pre-push")
	if err := writeHook(path, prePush); err != nil {
		return installed, err
	}
	installed = append(installed, "pre-push")

	return installed, nil
}

// Uninstall removes polypod git hooks.
func Uninstall() ([]string, error) {
	gitDir, err := findGitDir()
	if err != nil {
		return nil, err
	}

	hooksDir := filepath.Join(gitDir, "hooks")
	var removed []string

	for _, name := range []string{"prepare-commit-msg", "pre-push"} {
		path := filepath.Join(hooksDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "Polypod") {
			os.Remove(path)
			removed = append(removed, name)
		}
	}

	return removed, nil
}

// Status shows which polypod hooks are installed.
func Status() ([]string, error) {
	gitDir, err := findGitDir()
	if err != nil {
		return nil, err
	}

	hooksDir := filepath.Join(gitDir, "hooks")
	var active []string

	for _, name := range []string{"prepare-commit-msg", "pre-push"} {
		path := filepath.Join(hooksDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "Polypod") {
			active = append(active, name)
		}
	}

	return active, nil
}

func writeHook(path, content string) error {
	// Backup existing hook
	if _, err := os.Stat(path); err == nil {
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), "Polypod") {
			// Not our hook — backup it
			os.Rename(path, path+".bak")
		}
	}

	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		return fmt.Errorf("escrevendo hook %s: %w", filepath.Base(path), err)
	}
	return nil
}

func findGitDir() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
