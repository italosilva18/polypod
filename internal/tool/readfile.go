package tool

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/types"
)

// ---------------------------------------------------------------------------
// ReadFile tool — production-quality implementation matching Claude Code
// ---------------------------------------------------------------------------

const (
	readFileDefaultLimit    = 2000
	readFileMaxLineLen      = 2000
	readFileWarnSizeBytes   = 10 * 1024 * 1024 // 10 MB
	readFileRejectSizeBytes = 50 * 1024 * 1024 // 50 MB
	readFileBinaryCheckSize = 8192             // bytes to probe for null
)

// imageExtensions are extensions handled as image metadata rather than binary content.
var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".bmp": true, ".tiff": true, ".tif": true,
	".ico": true, ".svg": true,
}

// pdfExtensions are extensions handled as PDF metadata.
var pdfExtensions = map[string]bool{
	".pdf": true,
}

// RegisterReadFileTool registers the production read_file tool.
func RegisterReadFileTool(reg *Registry, workingDir string) {
	reg.RegisterDef(newReadFileTool(workingDir, reg))
}

func newReadFileTool(wd string, reg *Registry) types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return constants.ToolReadFile },
		DescriptionFn: func() string {
			return "Read a file or directory from the local filesystem. " +
				"Returns content with line numbers (e.g. '1: content'). " +
				"Supports offset/limit for large files. " +
				"For directories, lists entries with type/size/date. " +
				"For images, returns metadata. For PDFs, returns file info."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The absolute or relative path to the file or directory to read",
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": "The line number to start reading from (1-indexed, default 1)",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "The maximum number of lines to read (default 2000)",
					},
				},
				"required": []string{"file_path"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			p := argString(args, "file_path")
			if p == "" {
				return types.ValidationResult{Valid: false, Message: "file_path is required"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			return executeReadFile(wd, args, func(path string, data []byte) {
				reg.recordFileVersion(ctx.SessionID, path, data)
			})
		},
	}
}

// ---------------------------------------------------------------------------
// Execute
// ---------------------------------------------------------------------------

func executeReadFile(wd string, args map[string]any, record func(string, []byte)) (types.ToolResult, error) {
	filePath := argString(args, "file_path")
	resolved := resolvePath(wd, filePath)

	offset := argInt(args, "offset", 1)
	limit := argInt(args, "limit", readFileDefaultLimit)

	if offset < 1 {
		offset = 1
	}
	if limit < 1 {
		limit = readFileDefaultLimit
	}

	// Resolve symlinks and report target.
	var symlinkNote string
	realPath, err := filepath.EvalSymlinks(resolved)
	if err == nil && realPath != resolved {
		symlinkNote = fmt.Sprintf("(symlink -> %s)\n", realPath)
		resolved = realPath
	}

	// Stat the target.
	info, err := os.Lstat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return errResult("File not found: %s", resolved), nil
		}
		if os.IsPermission(err) {
			return errResult("Permission denied: cannot read %s", resolved), nil
		}
		return errResult("Error: %v", err), nil
	}

	// ---- Directory listing ------------------------------------------------
	if info.IsDir() {
		return readDirectory(resolved, symlinkNote)
	}

	ext := strings.ToLower(filepath.Ext(resolved))

	// ---- Image files: metadata only --------------------------------------
	if imageExtensions[ext] {
		return readImageMeta(resolved, info, symlinkNote)
	}

	// ---- PDF files: summary only -----------------------------------------
	if pdfExtensions[ext] {
		return okResult("%sPDF file: %s (%s). Use a PDF reader to view the content.",
			symlinkNote, resolved, formatSize(info.Size())), nil
	}

	// ---- Permission check ------------------------------------------------
	f, err := os.Open(resolved)
	if err != nil {
		if os.IsPermission(err) {
			return errResult("Permission denied: cannot read %s", resolved), nil
		}
		return errResult("Error opening file: %v", err), nil
	}
	defer f.Close()

	// ---- File size checks ------------------------------------------------
	size := info.Size()
	if size > readFileRejectSizeBytes {
		return errResult("File too large (%s). Maximum supported size is 50MB. "+
			"Use a different tool or read specific portions.", formatSize(size)), nil
	}

	// ---- Binary detection (first 8KB) ------------------------------------
	probe := make([]byte, readFileBinaryCheckSize)
	n, _ := f.Read(probe)
	probe = probe[:n]

	if isBinaryData(probe) {
		mimeType := http.DetectContentType(probe)
		return errResult("Binary file detected (%s, %s). Cannot display binary content. "+
			"Use appropriate tools for this file type.", mimeType, formatSize(size)), nil
	}

	// Seek back to start for full read.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return errResult("Error seeking file: %v", err), nil
	}

	// ---- Read full content -----------------------------------------------
	data, err := io.ReadAll(f)
	if err != nil {
		return errResult("Error reading file: %v", err), nil
	}
	if record != nil {
		record(resolved, data)
	}

	// Strip UTF-8 BOM if present.
	data = stripBOM(data)

	// Convert to string — UTF-8 first, then treat as latin-1 passthrough.
	content := toUTF8String(data)

	// ---- Size warning (after successful read) ----------------------------
	var sizeWarning string
	if size > readFileWarnSizeBytes {
		sizeWarning = fmt.Sprintf("Warning: Large file (%s). Consider using offset/limit for targeted reading.\n", formatSize(size))
	}

	// ---- Apply offset/limit with line numbering --------------------------
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	startIdx := offset - 1 // convert 1-indexed to 0-indexed
	if startIdx >= totalLines {
		return okResult("%s%s<file path=\"%s\">\n(offset %d exceeds total lines %d)\n</file>",
			symlinkNote, sizeWarning, filePath, offset, totalLines), nil
	}

	endIdx := startIdx + limit
	if endIdx > totalLines {
		endIdx = totalLines
	}

	var sb strings.Builder
	if symlinkNote != "" {
		sb.WriteString(symlinkNote)
	}
	if sizeWarning != "" {
		sb.WriteString(sizeWarning)
	}

	selectedLines := lines[startIdx:endIdx]
	for i, line := range selectedLines {
		lineNum := startIdx + i + 1 // 1-indexed
		if len(line) > readFileMaxLineLen {
			line = line[:readFileMaxLineLen] + " [truncated]"
		}
		sb.WriteString(fmt.Sprintf("%d: %s\n", lineNum, line))
	}

	numReturned := endIdx - startIdx
	if endIdx < totalLines {
		sb.WriteString(fmt.Sprintf("\n(%d lines total, showing lines %d-%d. Use offset to read more.)",
			totalLines, offset, offset+numReturned-1))
	}

	return types.ToolResult{Content: sb.String()}, nil
}

// ---------------------------------------------------------------------------
// Directory listing
// ---------------------------------------------------------------------------

func readDirectory(dirPath, symlinkNote string) (types.ToolResult, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsPermission(err) {
			return errResult("Permission denied: cannot list %s", dirPath), nil
		}
		return errResult("Error listing directory: %v", err), nil
	}

	var sb strings.Builder
	if symlinkNote != "" {
		sb.WriteString(symlinkNote)
	}

	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}

		typeChar := "-"
		if e.IsDir() {
			typeChar = "d"
		}
		if e.Type()&os.ModeSymlink != 0 {
			typeChar = "l"
			// Resolve symlink target.
			target, err := os.Readlink(filepath.Join(dirPath, e.Name()))
			if err == nil {
				name = fmt.Sprintf("%s -> %s", e.Name(), target)
			}
		}

		sb.WriteString(fmt.Sprintf("%s %8s %s %s\n",
			typeChar,
			formatSize(info.Size()),
			info.ModTime().Format(time.DateTime),
			name,
		))
	}

	if len(entries) == 0 {
		sb.WriteString("(empty directory)")
	}

	return types.ToolResult{Content: sb.String()}, nil
}

// ---------------------------------------------------------------------------
// Image metadata
// ---------------------------------------------------------------------------

func readImageMeta(filePath string, info os.FileInfo, symlinkNote string) (types.ToolResult, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "image/" + strings.TrimPrefix(ext, ".")
	}

	var sb strings.Builder
	if symlinkNote != "" {
		sb.WriteString(symlinkNote)
	}
	sb.WriteString(fmt.Sprintf("Image file: %s\n", filePath))
	sb.WriteString(fmt.Sprintf("  Type: %s\n", mimeType))
	sb.WriteString(fmt.Sprintf("  Size: %s\n", formatSize(info.Size())))
	sb.WriteString(fmt.Sprintf("  Modified: %s\n", info.ModTime().Format(time.DateTime)))

	// Attempt to decode dimensions (works for png, jpg, gif natively).
	if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" {
		f, err := os.Open(filePath)
		if err == nil {
			defer f.Close()
			cfg, _, err := image.DecodeConfig(f)
			if err == nil {
				sb.WriteString(fmt.Sprintf("  Dimensions: %dx%d\n", cfg.Width, cfg.Height))
			}
		}
	}

	return types.ToolResult{Content: sb.String()}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// isBinaryData returns true if the data contains null bytes (binary indicator).
func isBinaryData(data []byte) bool {
	return bytes.ContainsRune(data, 0)
}

// stripBOM removes a UTF-8 BOM (0xEF, 0xBB, 0xBF) from the start of data.
func stripBOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}

// toUTF8String converts bytes to a string. Go strings are UTF-8 by default;
// bytes that are invalid UTF-8 pass through as latin-1 (byte values map 1:1
// to the first 256 Unicode code points, which is what latin-1 is).
func toUTF8String(data []byte) string {
	return string(data)
}

// formatSize returns a human-readable file size.
func formatSize(b int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d bytes", b)
	}
}

// argString extracts a string argument, returning "" if missing.
func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// argInt extracts an integer argument (JSON numbers come as float64).
func argInt(args map[string]any, key string, defaultVal int) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	if v, ok := args[key].(int); ok {
		return v
	}
	return defaultVal
}

// errResult returns a ToolResult marked as an error.
func errResult(format string, a ...any) types.ToolResult {
	return types.ToolResult{Content: fmt.Sprintf(format, a...), IsError: true}
}

// okResult returns a successful ToolResult.
func okResult(format string, a ...any) types.ToolResult {
	return types.ToolResult{Content: fmt.Sprintf(format, a...)}
}
