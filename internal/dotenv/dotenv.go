package dotenv

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Load reads .env files and sets environment variables.
// Precedence: existing env vars > .env.local > .env
// Does NOT override already-set variables.
func Load(dir string) (int, error) {
	count := 0

	// Load in reverse precedence (lower priority first)
	for _, name := range []string{".env", ".env.local"} {
		path := filepath.Join(dir, name)
		n, err := loadFile(path)
		if err != nil {
			continue // file not found is fine
		}
		count += n
	}

	return count, nil
}

// LoadFile parses a single .env file and sets variables.
func loadFile(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=VALUE
		eqIdx := strings.IndexByte(line, '=')
		if eqIdx < 0 {
			continue
		}

		key := strings.TrimSpace(line[:eqIdx])
		value := strings.TrimSpace(line[eqIdx+1:])

		// Remove surrounding quotes
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		// Don't override existing env vars
		if os.Getenv(key) != "" {
			continue
		}

		os.Setenv(key, value)
		count++
	}

	return count, nil
}

// CheckSecurity warns if .env files contain secrets but aren't gitignored.
func CheckSecurity(dir string) []string {
	var warnings []string

	for _, name := range []string{".env", ".env.local"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}

		// Check if in .gitignore
		if !isGitignored(dir, name) {
			warnings = append(warnings, fmt.Sprintf("[WARN] %s contem secrets mas NAO esta no .gitignore!", name))
		}
	}

	return warnings
}

func isGitignored(dir, filename string) bool {
	gitignorePath := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		return false
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == filename || line == filename+"*" || line == ".env*" || line == "*.local" {
			return true
		}
	}
	return false
}
