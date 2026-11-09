package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// ---------------------------------------------------------------------------
// File edit backup manager — tracks per-file edit history for undo support
// ---------------------------------------------------------------------------

// fileBackupEntry records a single backup of a file before an edit.
type fileBackupEntry struct {
	Content   []byte    // Original content before edit
	Timestamp time.Time // When the backup was created
	Tool      string    // Which tool triggered the backup ("file_edit" or "file_write")
}

// fileBackupManager manages per-file backup history for undo support.
type fileBackupManager struct {
	mu         sync.Mutex
	history    map[string][]fileBackupEntry // path → stack of backups (most recent last)
	maxPerFile int
}

// newFileBackupManager creates a backup manager with a per-file history limit.
func newFileBackupManager(maxPerFile int) *fileBackupManager {
	if maxPerFile <= 0 {
		maxPerFile = 50
	}
	return &fileBackupManager{
		history:    make(map[string][]fileBackupEntry),
		maxPerFile: maxPerFile,
	}
}

// saveBackup records the current content of a file before modification.
// If the file does not exist, it records a nil-content entry (for undo = delete).
func (m *fileBackupManager) saveBackup(filePath, toolName string) {
	var content []byte
	data, err := os.ReadFile(filePath)
	if err == nil {
		content = data
	}
	m.saveBackupContent(filePath, toolName, content)
}

// saveBackupContent records the exact version validated by a file tool.
func (m *fileBackupManager) saveBackupContent(filePath, toolName string, content []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if content != nil {
		copyOfContent := make([]byte, len(content))
		copy(copyOfContent, content)
		content = copyOfContent
	}

	entry := fileBackupEntry{
		Content:   content,
		Timestamp: time.Now(),
		Tool:      toolName,
	}

	stack := m.history[filePath]
	stack = append(stack, entry)
	if len(stack) > m.maxPerFile {
		stack = stack[len(stack)-m.maxPerFile:]
	}
	m.history[filePath] = stack
}

// popBackup removes and returns the most recent backup for a file.
// Returns nil, false if no backup exists.
func (m *fileBackupManager) popBackup(filePath string) (*fileBackupEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stack := m.history[filePath]
	if len(stack) == 0 {
		return nil, false
	}

	entry := stack[len(stack)-1]
	m.history[filePath] = stack[:len(stack)-1]
	return &entry, true
}

// hasBackup returns whether a backup exists for the file.
func (m *fileBackupManager) hasBackup(filePath string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.history[filePath]) > 0
}

// backupCount returns the number of backups for a file.
func (m *fileBackupManager) backupCount(filePath string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.history[filePath])
}

// ---------------------------------------------------------------------------
// Unified diff generation — pure Go, no external dependencies
// ---------------------------------------------------------------------------

// generateUnifiedDiff produces a standard unified diff between old and new content.
// Output format:
//
//	--- a/path/to/file
//	+++ b/path/to/file
//	@@ -startOld,countOld +startNew,countNew @@
//	 context line
//	-removed line
//	+added line
func generateUnifiedDiff(filename, oldContent, newContent string) string {
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)

	// Compute edit script using simple LCS-based diff
	hunks := computeHunks(oldLines, newLines, 3)
	if len(hunks) == 0 {
		return "" // No changes
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("--- a/%s\n", filename))
	sb.WriteString(fmt.Sprintf("+++ b/%s\n", filename))

	for _, h := range hunks {
		sb.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
			h.oldStart+1, h.oldCount, h.newStart+1, h.newCount))
		for _, dl := range h.lines {
			switch dl.op {
			case diffContext:
				sb.WriteString(" " + dl.text + "\n")
			case diffDelete:
				sb.WriteString("-" + dl.text + "\n")
			case diffInsert:
				sb.WriteString("+" + dl.text + "\n")
			}
		}
	}

	return sb.String()
}

// splitLines splits content into lines, handling trailing newline correctly.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// Remove trailing empty element from final newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type diffOp int

const (
	diffContext diffOp = iota
	diffDelete
	diffInsert
)

type diffLine struct {
	op   diffOp
	text string
}

type diffHunk struct {
	oldStart int
	oldCount int
	newStart int
	newCount int
	lines    []diffLine
}

// editOp represents a single edit operation in the diff algorithm.
type editOp struct {
	op     diffOp
	oldIdx int // index in oldLines (-1 for insert)
	newIdx int // index in newLines (-1 for delete)
	text   string
}

// computeHunks computes unified-diff hunks with the given context lines.
func computeHunks(oldLines, newLines []string, contextLines int) []diffHunk {
	// Compute LCS table
	m, n := len(oldLines), len(newLines)

	// For very large files, fall back to a simpler approach
	if m*n > 10_000_000 {
		return computeHunksSimple(oldLines, newLines, contextLines)
	}

	// Standard LCS DP
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if oldLines[i-1] == newLines[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	// Backtrack to get edit operations
	var ops []editOp
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			ops = append(ops, editOp{diffContext, i - 1, j - 1, oldLines[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			ops = append(ops, editOp{diffInsert, -1, j - 1, newLines[j-1]})
			j--
		} else {
			ops = append(ops, editOp{diffDelete, i - 1, -1, oldLines[i-1]})
			i--
		}
	}

	// Reverse to get forward order
	for left, right := 0, len(ops)-1; left < right; left, right = left+1, right-1 {
		ops[left], ops[right] = ops[right], ops[left]
	}

	// Group into hunks with context
	return groupIntoHunks(ops, contextLines)
}

// groupIntoHunks converts a sequence of edit operations into unified diff hunks.
func groupIntoHunks(ops []editOp, contextLines int) []diffHunk {
	// Find change ranges (non-context operations)
	type changeRange struct {
		start, end int // indices into ops
	}
	var changes []changeRange
	inChange := false
	var current changeRange

	for i, op := range ops {
		if op.op != diffContext {
			if !inChange {
				current.start = i
				inChange = true
			}
			current.end = i
		} else if inChange {
			changes = append(changes, current)
			inChange = false
		}
	}
	if inChange {
		changes = append(changes, current)
	}

	if len(changes) == 0 {
		return nil
	}

	// Merge nearby changes into hunks (within 2*contextLines distance)
	type mergedRange struct {
		start, end int
	}
	merged := []mergedRange{{changes[0].start, changes[0].end}}

	for i := 1; i < len(changes); i++ {
		last := &merged[len(merged)-1]
		gap := changes[i].start - last.end
		if gap <= 2*contextLines+1 {
			last.end = changes[i].end
		} else {
			merged = append(merged, mergedRange{changes[i].start, changes[i].end})
		}
	}

	// Build hunks
	var hunks []diffHunk
	for _, mr := range merged {
		// Expand to include context
		start := mr.start - contextLines
		if start < 0 {
			start = 0
		}
		end := mr.end + contextLines
		if end >= len(ops) {
			end = len(ops) - 1
		}

		var h diffHunk
		var lines []diffLine
		oldStart, newStart := -1, -1
		oldCount, newCount := 0, 0

		for i := start; i <= end; i++ {
			op := ops[i]
			lines = append(lines, diffLine{op: op.op, text: op.text})

			switch op.op {
			case diffContext:
				if oldStart < 0 {
					oldStart = op.oldIdx
				}
				if newStart < 0 {
					newStart = op.newIdx
				}
				oldCount++
				newCount++
			case diffDelete:
				if oldStart < 0 {
					oldStart = op.oldIdx
				}
				if newStart < 0 && i+1 < len(ops) {
					// newStart from next insert/context
					for j := i + 1; j <= end; j++ {
						if ops[j].newIdx >= 0 {
							newStart = ops[j].newIdx
							break
						}
					}
				}
				oldCount++
			case diffInsert:
				if newStart < 0 {
					newStart = op.newIdx
				}
				if oldStart < 0 && i+1 < len(ops) {
					for j := i + 1; j <= end; j++ {
						if ops[j].oldIdx >= 0 {
							oldStart = ops[j].oldIdx
							break
						}
					}
				}
				newCount++
			}
		}

		if oldStart < 0 {
			oldStart = 0
		}
		if newStart < 0 {
			newStart = 0
		}

		h.oldStart = oldStart
		h.oldCount = oldCount
		h.newStart = newStart
		h.newCount = newCount
		h.lines = lines
		hunks = append(hunks, h)
	}

	return hunks
}

// computeHunksSimple is a fallback for large files using string-level matching.
// It locates the first differing region and produces a single hunk.
func computeHunksSimple(oldLines, newLines []string, contextLines int) []diffHunk {
	// Find first difference
	prefixLen := 0
	minLen := len(oldLines)
	if len(newLines) < minLen {
		minLen = len(newLines)
	}
	for prefixLen < minLen && oldLines[prefixLen] == newLines[prefixLen] {
		prefixLen++
	}

	// Find last difference (from end)
	suffixLen := 0
	for suffixLen < minLen-prefixLen &&
		oldLines[len(oldLines)-1-suffixLen] == newLines[len(newLines)-1-suffixLen] {
		suffixLen++
	}

	// Changed region
	oldChangeStart := prefixLen
	oldChangeEnd := len(oldLines) - suffixLen
	newChangeStart := prefixLen
	newChangeEnd := len(newLines) - suffixLen

	if oldChangeStart >= oldChangeEnd && newChangeStart >= newChangeEnd {
		return nil // identical
	}

	// Add context
	ctxStart := oldChangeStart - contextLines
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxEndOld := oldChangeEnd + contextLines
	if ctxEndOld > len(oldLines) {
		ctxEndOld = len(oldLines)
	}
	ctxEndNew := newChangeEnd + contextLines
	if ctxEndNew > len(newLines) {
		ctxEndNew = len(newLines)
	}

	var lines []diffLine

	// Leading context
	for i := ctxStart; i < oldChangeStart; i++ {
		lines = append(lines, diffLine{diffContext, oldLines[i]})
	}
	// Deleted lines
	for i := oldChangeStart; i < oldChangeEnd; i++ {
		lines = append(lines, diffLine{diffDelete, oldLines[i]})
	}
	// Inserted lines
	for i := newChangeStart; i < newChangeEnd; i++ {
		lines = append(lines, diffLine{diffInsert, newLines[i]})
	}
	// Trailing context
	trailingStart := oldChangeEnd
	trailingEnd := ctxEndOld
	for i := trailingStart; i < trailingEnd; i++ {
		lines = append(lines, diffLine{diffContext, oldLines[i]})
	}

	oldCount := (oldChangeStart - ctxStart) + (oldChangeEnd - oldChangeStart) + (trailingEnd - trailingStart)
	newCount := (oldChangeStart - ctxStart) + (newChangeEnd - newChangeStart) + (trailingEnd - trailingStart)

	return []diffHunk{{
		oldStart: ctxStart,
		oldCount: oldCount,
		newStart: ctxStart,
		newCount: newCount,
		lines:    lines,
	}}
}

// ---------------------------------------------------------------------------
// RegisterFileEditTools registers enhanced file_edit, file_write, and undo_edit
// ---------------------------------------------------------------------------

// RegisterFileEditTools registers production-quality file_edit, file_write, and
// undo_edit tools. These replace the simple versions in advanced_tools.go.
func RegisterFileEditTools(reg *Registry, workingDir string) {
	backups := newFileBackupManager(50)

	reg.RegisterDef(newEnhancedFileEditTool(workingDir, backups, reg))
	reg.RegisterDef(newEnhancedFileWriteTool(workingDir, backups, reg))
	reg.RegisterDef(newUndoEditTool(workingDir, backups, reg))
}

// ---------------------------------------------------------------------------
// file_edit — enhanced with unified diff preview, backup, and validation
// ---------------------------------------------------------------------------

func newEnhancedFileEditTool(wd string, backups *fileBackupManager, reg *Registry) types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "file_edit" },
		DescriptionFn: func() string {
			return "Edit a file by replacing an exact string with a new string. " +
				"Returns a unified diff showing the change. " +
				"Fails if old_string is not found or matches multiple times (unless replace_all is true). " +
				"Supports undo via undo_edit."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The path to the file to edit",
					},
					"old_string": map[string]any{
						"type":        "string",
						"description": "The exact text to find in the file",
					},
					"new_string": map[string]any{
						"type":        "string",
						"description": "The text to replace old_string with",
					},
					"replace_all": map[string]any{
						"type":        "boolean",
						"description": "Replace all occurrences instead of just the first (default: false)",
					},
				},
				"required": []string{"file_path", "old_string", "new_string"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			fp, _ := args["file_path"].(string)
			if fp == "" {
				return types.ValidationResult{Valid: false, Message: "file_path is required"}
			}
			if _, ok := args["old_string"].(string); !ok {
				return types.ValidationResult{Valid: false, Message: "old_string is required"}
			}
			if _, ok := args["new_string"].(string); !ok {
				return types.ValidationResult{Valid: false, Message: "new_string is required"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			path := resolvePath(wd, args["file_path"])
			oldStr, _ := args["old_string"].(string)
			newStr, _ := args["new_string"].(string)
			replaceAll := false
			if ra, ok := args["replace_all"].(bool); ok {
				replaceAll = ra
			}

			// --- Validation: identical strings ---
			if oldStr == newStr {
				return types.ToolResult{
					Content: "No changes to make: old_string and new_string are identical.",
					IsError: true,
				}, nil
			}

			// --- Read the file ---
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					// Empty old_string on non-existent file → create new file
					if oldStr == "" {
						return fileEditCreateNew(path, newStr, backups, reg, ctx)
					}
					return types.ToolResult{
						Content: fmt.Sprintf("File not found: %s", path),
						IsError: true,
					}, nil
				}
				if os.IsPermission(err) {
					return types.ToolResult{
						Content: fmt.Sprintf("Permission denied: %s", path),
						IsError: true,
					}, nil
				}
				return types.ToolResult{
					Content: fmt.Sprintf("Error reading file: %v", err),
					IsError: true,
				}, nil
			}

			content := string(data)
			if reg.hasStaleFileVersion(ctx.SessionID, path, data) {
				return staleFileResult(path), nil
			}

			// --- Empty old_string on existing file with content ---
			if oldStr == "" && strings.TrimSpace(content) != "" {
				return types.ToolResult{
					Content: "Cannot use empty old_string on a file that already has content. " +
						"Use file_write to overwrite the entire file, or provide the exact text to replace.",
					IsError: true,
				}, nil
			}

			// --- Empty old_string on empty/whitespace-only file → overwrite ---
			if oldStr == "" {
				matches, checkErr := fileStillMatches(path, true, data)
				if checkErr != nil {
					return types.ToolResult{Content: fmt.Sprintf("Erro ao verificar arquivo: %v", checkErr), IsError: true}, nil
				}
				if !matches {
					return staleFileResult(path), nil
				}
				if err := reg.runBeforeFileWrite(ctx, path, data, true); err != nil {
					return beforeFileWriteError(err), nil
				}
				backups.saveBackupContent(path, "file_edit", data)
				if wErr := os.WriteFile(path, []byte(newStr), 0644); wErr != nil {
					return types.ToolResult{
						Content: fmt.Sprintf("Error writing file: %v", wErr),
						IsError: true,
					}, nil
				}
				diff := generateUnifiedDiff(relativePath(wd, path), content, newStr)
				return reg.afterFileWrite(ctx, path, newStr, types.ToolResult{
					Content: fmt.Sprintf("File updated: %s\n\n%s", path, diff),
				}), nil
			}

			// --- Count occurrences ---
			count := strings.Count(content, oldStr)

			if count == 0 {
				// Provide a helpful error with the first 100 chars of old_string for debugging
				snippet := oldStr
				if len(snippet) > 100 {
					snippet = snippet[:100] + "..."
				}
				return types.ToolResult{
					Content: fmt.Sprintf(
						"oldString not found in %s. Make sure the string matches exactly, including whitespace and indentation.\n\nSearched for:\n%s",
						path, snippet,
					),
					IsError: true,
				}, nil
			}

			if count > 1 && !replaceAll {
				return types.ToolResult{
					Content: fmt.Sprintf(
						"Found %d matches of oldString in %s. "+
							"Provide more surrounding context to uniquely identify the match, "+
							"or set replace_all to true to replace all occurrences.",
						count, path,
					),
					IsError: true,
				}, nil
			}

			// --- Apply replacement ---
			var updated string
			if replaceAll {
				updated = strings.ReplaceAll(content, oldStr, newStr)
			} else {
				updated = strings.Replace(content, oldStr, newStr, 1)
			}

			// --- Preserve file permissions ---
			mode := os.FileMode(0644)
			if info, statErr := os.Stat(path); statErr == nil {
				mode = info.Mode()
			}
			matches, checkErr := fileStillMatches(path, true, data)
			if checkErr != nil {
				return types.ToolResult{Content: fmt.Sprintf("Erro ao verificar arquivo: %v", checkErr), IsError: true}, nil
			}
			if !matches {
				return staleFileResult(path), nil
			}
			if err := reg.runBeforeFileWrite(ctx, path, data, true); err != nil {
				return beforeFileWriteError(err), nil
			}
			backups.saveBackupContent(path, "file_edit", data)

			if wErr := os.WriteFile(path, []byte(updated), mode); wErr != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error writing file: %v", wErr),
					IsError: true,
				}, nil
			}

			// --- Generate unified diff ---
			relPath := relativePath(wd, path)
			diff := generateUnifiedDiff(relPath, content, updated)

			var msg string
			if replaceAll && count > 1 {
				msg = fmt.Sprintf("Replaced %d occurrences in %s", count, path)
			} else {
				msg = fmt.Sprintf("Updated %s", path)
			}

			if diff != "" {
				return reg.afterFileWrite(ctx, path, updated, types.ToolResult{
					Content: fmt.Sprintf("%s\n\n%s", msg, diff),
				}), nil
			}
			return reg.afterFileWrite(ctx, path, updated, types.ToolResult{Content: msg}), nil
		},
	}
}

// fileEditCreateNew handles the case where old_string="" and file doesn't exist → create.
func fileEditCreateNew(path, newStr string, backups *fileBackupManager, reg *Registry, ctx types.ToolContext) (types.ToolResult, error) {
	dir := filepath.Dir(path)
	if mkErr := os.MkdirAll(dir, 0755); mkErr != nil {
		return types.ToolResult{
			Content: fmt.Sprintf("Error creating directory %s: %v", dir, mkErr),
			IsError: true,
		}, nil
	}
	if err := reg.runBeforeFileWrite(ctx, path, nil, false); err != nil {
		return beforeFileWriteError(err), nil
	}

	if wErr := writeNewFileExclusive(path, []byte(newStr), 0644); wErr != nil {
		return types.ToolResult{
			Content: fmt.Sprintf("Error creating file: %v", wErr),
			IsError: true,
		}, nil
	}
	backups.saveBackupContent(path, "file_edit", nil)

	// Show diff for new file creation
	diff := generateUnifiedDiff(filepath.Base(path), "", newStr)
	return reg.afterFileWrite(ctx, path, newStr, types.ToolResult{
		Content: fmt.Sprintf("Created new file: %s (%d bytes)\n\n%s", path, len(newStr), diff),
	}), nil
}

// ---------------------------------------------------------------------------
// file_write — enhanced with backup, diff preview, permission preservation
// ---------------------------------------------------------------------------

func newEnhancedFileWriteTool(wd string, backups *fileBackupManager, reg *Registry) types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "file_write" },
		DescriptionFn: func() string {
			return "Write content to a file, creating it (and parent directories) if necessary, " +
				"or overwriting the existing file. Returns a unified diff for existing files. " +
				"Supports undo via undo_edit."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The absolute or relative path to the file to write",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The content to write to the file",
					},
				},
				"required": []string{"file_path", "content"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			fp, _ := args["file_path"].(string)
			if fp == "" {
				return types.ValidationResult{Valid: false, Message: "file_path is required"}
			}
			if _, ok := args["content"].(string); !ok {
				return types.ValidationResult{Valid: false, Message: "content is required"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			path := resolvePath(wd, args["file_path"])
			content, _ := args["content"].(string)

			// Read existing content for diff (if file exists)
			var oldContent string
			var existed bool
			var oldMode os.FileMode
			var original []byte

			if info, statErr := os.Stat(path); statErr == nil {
				existed = true
				oldMode = info.Mode()
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					return types.ToolResult{Content: fmt.Sprintf("Error reading file: %v", readErr), IsError: true}, nil
				}
				original = data
				oldContent = string(data)
			} else if !os.IsNotExist(statErr) {
				return types.ToolResult{Content: fmt.Sprintf("Error checking file: %v", statErr), IsError: true}, nil
			}
			if existed && reg.hasStaleFileVersion(ctx.SessionID, path, original) {
				return staleFileResult(path), nil
			}

			// Ensure parent directories exist
			if mkErr := os.MkdirAll(filepath.Dir(path), 0755); mkErr != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error creating directories: %v", mkErr),
					IsError: true,
				}, nil
			}

			// Preserve permissions for existing files, use 0644 for new
			mode := os.FileMode(0644)
			if existed {
				mode = oldMode
			}

			matches, checkErr := fileStillMatches(path, existed, original)
			if checkErr != nil {
				return types.ToolResult{Content: fmt.Sprintf("Erro ao verificar arquivo: %v", checkErr), IsError: true}, nil
			}
			if !matches {
				return staleFileResult(path), nil
			}
			if existed && oldContent == content {
				reg.recordFileVersion(ctx.SessionID, path, original)
				return types.ToolResult{Content: fmt.Sprintf("File unchanged: %s (%d bytes)", path, len(content))}, nil
			}
			if err := reg.runBeforeFileWrite(ctx, path, original, existed); err != nil {
				return beforeFileWriteError(err), nil
			}
			if existed {
				backups.saveBackupContent(path, "file_write", original)
			}
			var wErr error
			if existed {
				wErr = os.WriteFile(path, []byte(content), mode)
			} else {
				wErr = writeNewFileExclusive(path, []byte(content), mode)
			}
			if wErr != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error writing file: %v", wErr),
					IsError: true,
				}, nil
			}
			if !existed {
				backups.saveBackupContent(path, "file_write", nil)
			}

			relPath := relativePath(wd, path)

			if existed {
				diff := generateUnifiedDiff(relPath, oldContent, content)
				return reg.afterFileWrite(ctx, path, content, types.ToolResult{
					Content: fmt.Sprintf("Updated %s (%d bytes)\n\n%s", path, len(content), diff),
				}), nil
			}

			diff := generateUnifiedDiff(relPath, "", content)
			return reg.afterFileWrite(ctx, path, content, types.ToolResult{
				Content: fmt.Sprintf("Created %s (%d bytes)\n\n%s", path, len(content), diff),
			}), nil
		},
	}
}

// ---------------------------------------------------------------------------
// undo_edit — restore a file from its most recent backup
// ---------------------------------------------------------------------------

func newUndoEditTool(wd string, backups *fileBackupManager, reg *Registry) types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "undo_edit" },
		DescriptionFn: func() string {
			return "Undo the last file_edit or file_write on a specific file. " +
				"Restores the file to its state before the last modification. " +
				"Can be called multiple times to step through edit history."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The path to the file to undo",
					},
				},
				"required": []string{"file_path"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			fp, _ := args["file_path"].(string)
			if fp == "" {
				return types.ValidationResult{Valid: false, Message: "file_path is required"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			path := resolvePath(wd, args["file_path"])

			backup, ok := backups.popBackup(path)
			if !ok {
				return types.ToolResult{
					Content: fmt.Sprintf("No edit history found for %s. Nothing to undo.", path),
					IsError: true,
				}, nil
			}

			// Read current content for diff
			var currentContent string
			if data, err := os.ReadFile(path); err == nil {
				currentContent = string(data)
			}

			relPath := relativePath(wd, path)

			if backup.Content == nil {
				// Original state was "file did not exist" → delete it
				if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
					return types.ToolResult{
						Content: fmt.Sprintf("Error removing file: %v", rmErr),
						IsError: true,
					}, nil
				}
				diff := generateUnifiedDiff(relPath, currentContent, "")
				remaining := backups.backupCount(path)
				return types.ToolResult{
					Content: fmt.Sprintf("Undo: removed %s (file did not exist before edit)\n"+
						"Remaining undo steps: %d\n\n%s", path, remaining, diff),
				}, nil
			}

			// Restore the backup content
			restoredContent := string(backup.Content)

			// Preserve current file permissions if file exists
			mode := os.FileMode(0644)
			if info, statErr := os.Stat(path); statErr == nil {
				mode = info.Mode()
			}

			// Ensure directory exists (in case file was moved)
			if mkErr := os.MkdirAll(filepath.Dir(path), 0755); mkErr != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error creating directory: %v", mkErr),
					IsError: true,
				}, nil
			}

			if wErr := os.WriteFile(path, backup.Content, mode); wErr != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error restoring file: %v", wErr),
					IsError: true,
				}, nil
			}

			diff := generateUnifiedDiff(relPath, currentContent, restoredContent)
			remaining := backups.backupCount(path)
			return reg.afterFileWrite(ctx, path, restoredContent, types.ToolResult{
				Content: fmt.Sprintf("Undo: restored %s (from %s backup)\n"+
					"Remaining undo steps: %d\n\n%s",
					path, backup.Tool, remaining, diff),
			}), nil
		},
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

// relativePath returns a relative path from base, or the original if it fails.
func relativePath(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}
