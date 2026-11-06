package lsp

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
)

// --- Diagnostic Registry ---

// DiagnosticFile groups diagnostics for a single file.
type DiagnosticFile struct {
	URI         string          `json:"uri"`
	Diagnostics []FormattedDiag `json:"diagnostics"`
}

// FormattedDiag is a diagnostic in a format suitable for tool output.
type FormattedDiag struct {
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Range    Range  `json:"range"`
	Source   string `json:"source,omitempty"`
	Code     string `json:"code,omitempty"`
}

// PendingDiagnostic is a diagnostic batch waiting for delivery.
type PendingDiagnostic struct {
	ServerName     string
	Files          []DiagnosticFile
	Timestamp      int64
	AttachmentSent bool
}

// Volume limiting constants.
const (
	maxDiagnosticsPerFile = 10
	maxTotalDiagnostics   = 30
	maxDeliveredFiles     = 500
)

// DiagnosticRegistry collects and deduplicates diagnostics from LSP servers.
type DiagnosticRegistry struct {
	mu        sync.Mutex
	pending   map[string]*PendingDiagnostic // id -> diagnostic
	delivered map[string]map[string]bool    // file URI -> set of diagnostic keys
	nextID    int
}

// NewDiagnosticRegistry creates a new registry.
func NewDiagnosticRegistry() *DiagnosticRegistry {
	return &DiagnosticRegistry{
		pending:   make(map[string]*PendingDiagnostic),
		delivered: make(map[string]map[string]bool),
	}
}

// RegisterDiagnostics stores diagnostics from a server for later delivery.
func (r *DiagnosticRegistry) RegisterDiagnostics(serverName string, files []DiagnosticFile) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextID++
	id := fmt.Sprintf("diag-%d", r.nextID)

	r.pending[id] = &PendingDiagnostic{
		ServerName:     serverName,
		Files:          files,
		AttachmentSent: false,
	}

	log.Printf("[LSP DIAG] Registered %d file(s) from %s (ID: %s)", len(files), serverName, id)
}

// RegisterFromPublishDiagnostics converts raw LSP publishDiagnostics params and registers them.
func (r *DiagnosticRegistry) RegisterFromPublishDiagnostics(serverName string, raw json.RawMessage) {
	var params PublishDiagnosticsParams
	if err := json.Unmarshal(raw, &params); err != nil {
		log.Printf("[LSP DIAG] Failed to parse diagnostics from %s: %v", serverName, err)
		return
	}

	if len(params.Diagnostics) == 0 {
		return
	}

	uri := params.URI
	if strings.HasPrefix(uri, "file://") {
		uri = fileURIToPath(uri)
	}

	formatted := make([]FormattedDiag, 0, len(params.Diagnostics))
	for _, d := range params.Diagnostics {
		fd := FormattedDiag{
			Message:  d.Message,
			Severity: DiagnosticSeverity(d.Severity).String(),
			Range:    d.Range,
			Source:   d.Source,
		}
		if d.Code != nil {
			fd.Code = fmt.Sprintf("%v", d.Code)
		}
		formatted = append(formatted, fd)
	}

	r.RegisterDiagnostics(serverName, []DiagnosticFile{
		{URI: uri, Diagnostics: formatted},
	})
}

// CheckPending retrieves and delivers pending diagnostics. Returned diagnostics
// are deduplicated and volume-limited. Marks them as delivered.
func (r *DiagnosticRegistry) CheckPending() []DiagnosticFile {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Collect all unsent files
	var allFiles []DiagnosticFile
	var toMark []*PendingDiagnostic
	for _, p := range r.pending {
		if !p.AttachmentSent {
			allFiles = append(allFiles, p.Files...)
			toMark = append(toMark, p)
		}
	}

	if len(allFiles) == 0 {
		return nil
	}

	// Deduplicate
	deduplicated := r.deduplicateFiles(allFiles)

	// Mark as sent and delete from pending
	for _, p := range toMark {
		p.AttachmentSent = true
	}
	for id, p := range r.pending {
		if p.AttachmentSent {
			delete(r.pending, id)
		}
	}

	// Volume limiting
	deduplicated = r.volumeLimit(deduplicated)

	// Track delivered diagnostics for cross-turn dedup
	for _, f := range deduplicated {
		if _, ok := r.delivered[f.URI]; !ok {
			r.delivered[f.URI] = make(map[string]bool)
		}
		for _, d := range f.Diagnostics {
			key := diagnosticKey(d)
			r.delivered[f.URI][key] = true
		}
	}

	// LRU eviction on delivered map
	if len(r.delivered) > maxDeliveredFiles {
		// Remove oldest entries (no ordering, just trim)
		for uri := range r.delivered {
			delete(r.delivered, uri)
			if len(r.delivered) <= maxDeliveredFiles {
				break
			}
		}
	}

	return deduplicated
}

// ClearForFile clears delivered diagnostics for a file (call after editing).
func (r *DiagnosticRegistry) ClearForFile(fileURI string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.delivered, fileURI)
}

// ClearAll resets all diagnostic state.
func (r *DiagnosticRegistry) ClearAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = make(map[string]*PendingDiagnostic)
	r.delivered = make(map[string]map[string]bool)
}

// PendingCount returns the number of pending diagnostic batches.
func (r *DiagnosticRegistry) PendingCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

// deduplicateFiles merges and deduplicates diagnostic files.
func (r *DiagnosticRegistry) deduplicateFiles(allFiles []DiagnosticFile) []DiagnosticFile {
	// Group by URI
	grouped := make(map[string]*DiagnosticFile)
	seen := make(map[string]map[string]bool) // uri -> set of keys

	for _, f := range allFiles {
		if _, ok := grouped[f.URI]; !ok {
			grouped[f.URI] = &DiagnosticFile{URI: f.URI}
			seen[f.URI] = make(map[string]bool)
		}

		// Get previously delivered for cross-turn dedup
		prevDelivered := r.delivered[f.URI]

		for _, d := range f.Diagnostics {
			key := diagnosticKey(d)
			if seen[f.URI][key] {
				continue // within-batch dedup
			}
			if prevDelivered != nil && prevDelivered[key] {
				continue // cross-turn dedup
			}
			seen[f.URI][key] = true
			grouped[f.URI].Diagnostics = append(grouped[f.URI].Diagnostics, d)
		}
	}

	// Collect non-empty files
	var result []DiagnosticFile
	for _, f := range grouped {
		if len(f.Diagnostics) > 0 {
			result = append(result, *f)
		}
	}
	return result
}

// volumeLimit caps diagnostics per file and total, prioritizing errors.
func (r *DiagnosticRegistry) volumeLimit(files []DiagnosticFile) []DiagnosticFile {
	total := 0
	for i := range files {
		// Sort by severity (Error first)
		sort.Slice(files[i].Diagnostics, func(a, b int) bool {
			return severityOrder(files[i].Diagnostics[a].Severity) <
				severityOrder(files[i].Diagnostics[b].Severity)
		})

		// Cap per file
		if len(files[i].Diagnostics) > maxDiagnosticsPerFile {
			files[i].Diagnostics = files[i].Diagnostics[:maxDiagnosticsPerFile]
		}

		// Cap total
		remaining := maxTotalDiagnostics - total
		if len(files[i].Diagnostics) > remaining {
			files[i].Diagnostics = files[i].Diagnostics[:remaining]
		}
		total += len(files[i].Diagnostics)
	}

	// Filter empty
	var result []DiagnosticFile
	for _, f := range files {
		if len(f.Diagnostics) > 0 {
			result = append(result, f)
		}
	}
	return result
}

// --- Formatting ---

// FormatDiagnostics formats diagnostics for tool result output.
func FormatDiagnostics(files []DiagnosticFile, severityFilter string) string {
	if len(files) == 0 {
		return "No diagnostics."
	}

	var sb strings.Builder
	totalCount := 0
	for _, f := range files {
		var filtered []FormattedDiag
		for _, d := range f.Diagnostics {
			if severityFilter != "" && !strings.EqualFold(d.Severity, severityFilter) {
				continue
			}
			filtered = append(filtered, d)
		}
		if len(filtered) == 0 {
			continue
		}

		fmt.Fprintf(&sb, "\n%s:\n", f.URI)
		for _, d := range filtered {
			line := d.Range.Start.Line + 1
			char := d.Range.Start.Character + 1
			src := ""
			if d.Source != "" {
				src = fmt.Sprintf(" [%s]", d.Source)
			}
			code := ""
			if d.Code != "" {
				code = fmt.Sprintf(" (%s)", d.Code)
			}
			fmt.Fprintf(&sb, "  %s Line %d:%d: %s%s%s\n", d.Severity, line, char, d.Message, src, code)
			totalCount++
		}
	}

	if totalCount == 0 {
		if severityFilter != "" {
			return fmt.Sprintf("No %s diagnostics.", severityFilter)
		}
		return "No diagnostics."
	}

	header := fmt.Sprintf("Found %d diagnostic(s):", totalCount)
	return header + sb.String()
}

// FormatDiagnosticsForFile formats diagnostics for a single file.
func FormatDiagnosticsForFile(file DiagnosticFile) string {
	return FormatDiagnostics([]DiagnosticFile{file}, "")
}

// --- Helpers ---

func diagnosticKey(d FormattedDiag) string {
	return fmt.Sprintf("%s|%s|%d:%d-%d:%d|%s|%s",
		d.Message, d.Severity,
		d.Range.Start.Line, d.Range.Start.Character,
		d.Range.End.Line, d.Range.End.Character,
		d.Source, d.Code)
}

func severityOrder(severity string) int {
	switch severity {
	case "Error":
		return 1
	case "Warning":
		return 2
	case "Info":
		return 3
	case "Hint":
		return 4
	default:
		return 5
	}
}

// RegisterDiagnosticHandlers registers publishDiagnostics notification handlers
// on all servers in a manager.
func RegisterDiagnosticHandlers(manager *LSPServerManager, registry *DiagnosticRegistry) {
	servers := manager.AllServers()
	for name, server := range servers {
		serverName := name // capture for closure
		server.OnNotification("textDocument/publishDiagnostics", func(params json.RawMessage) {
			registry.RegisterFromPublishDiagnostics(serverName, params)
		})
		log.Printf("[LSP DIAG] Registered handler for %s", serverName)
	}
}
