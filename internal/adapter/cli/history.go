package cli

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

const maxHistoryEntries = 1000

// inputHistory manages persistent command history for the CLI.
type inputHistory struct {
	entries []string
	cursor  int
	path    string
}

func newInputHistory(dataDir string) *inputHistory {
	h := &inputHistory{
		path: filepath.Join(dataDir, "cli_history"),
	}
	h.load()
	h.cursor = len(h.entries)
	return h
}

// Add appends a new entry to history.
func (h *inputHistory) Add(entry string) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return
	}
	// Avoid consecutive duplicates
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == entry {
		h.cursor = len(h.entries)
		return
	}
	h.entries = append(h.entries, entry)
	if len(h.entries) > maxHistoryEntries {
		h.entries = h.entries[len(h.entries)-maxHistoryEntries:]
	}
	h.cursor = len(h.entries)
	h.save()
}

// Previous returns the previous history entry (up arrow).
func (h *inputHistory) Previous() (string, bool) {
	if h.cursor <= 0 {
		return "", false
	}
	h.cursor--
	return h.entries[h.cursor], true
}

// Next returns the next history entry (down arrow).
func (h *inputHistory) Next() (string, bool) {
	if h.cursor >= len(h.entries)-1 {
		h.cursor = len(h.entries)
		return "", true
	}
	h.cursor++
	return h.entries[h.cursor], true
}

// ResetCursor resets the cursor to the end of history.
func (h *inputHistory) ResetCursor() {
	h.cursor = len(h.entries)
}

func (h *inputHistory) load() {
	if h.path == "" {
		return
	}
	f, err := os.Open(h.path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			h.entries = append(h.entries, line)
		}
	}
	if len(h.entries) > maxHistoryEntries {
		h.entries = h.entries[len(h.entries)-maxHistoryEntries:]
	}
}

func (h *inputHistory) save() {
	if h.path == "" {
		return
	}
	os.MkdirAll(filepath.Dir(h.path), 0755)
	f, err := os.Create(h.path)
	if err != nil {
		return
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, entry := range h.entries {
		w.WriteString(entry + "\n")
	}
	w.Flush()
}
