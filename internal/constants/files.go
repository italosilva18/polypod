package constants

import (
	"path/filepath"
	"strings"
)

// File constants migrated from Claude Code TypeScript source.
// Source: constants/files.ts, constants/toolLimits.ts

// ---------------------------------------------------------------------------
// File size limits
// ---------------------------------------------------------------------------

const (
	// DefaultMaxResultSizeChars is the default max size in characters for tool
	// results before they get persisted to disk.
	DefaultMaxResultSizeChars = 50_000

	// MaxToolResultTokens is the max size for tool results in tokens.
	// Based on ~4 bytes per token, this is approximately 400KB of text.
	MaxToolResultTokens = 100_000

	// BytesPerToken is the estimate for calculating token count from byte size.
	BytesPerToken = 4

	// MaxToolResultBytes is the max size for tool results in bytes.
	MaxToolResultBytes = MaxToolResultTokens * BytesPerToken

	// MaxToolResultsPerMessageChars is the default max aggregate size in
	// characters for tool_result blocks within a single user message.
	MaxToolResultsPerMessageChars = 200_000

	// ToolSummaryMaxLength is the max character length for tool summary strings.
	ToolSummaryMaxLength = 50

	// BinaryCheckSize is the number of bytes to read for binary content detection.
	BinaryCheckSize = 8192
)

// ---------------------------------------------------------------------------
// Binary file extensions
// ---------------------------------------------------------------------------

// BinaryExtensions is the set of file extensions for binary files that should
// be skipped for text-based operations.
var BinaryExtensions = map[string]bool{
	// Images
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".bmp": true, ".ico": true, ".webp": true, ".tiff": true, ".tif": true,
	// Videos
	".mp4": true, ".mov": true, ".avi": true, ".mkv": true,
	".webm": true, ".wmv": true, ".flv": true, ".m4v": true,
	".mpeg": true, ".mpg": true,
	// Audio
	".mp3": true, ".wav": true, ".ogg": true, ".flac": true,
	".aac": true, ".m4a": true, ".wma": true, ".aiff": true, ".opus": true,
	// Archives
	".zip": true, ".tar": true, ".gz": true, ".bz2": true,
	".7z": true, ".rar": true, ".xz": true, ".z": true,
	".tgz": true, ".iso": true,
	// Executables/binaries
	".exe": true, ".dll": true, ".so": true, ".dylib": true,
	".bin": true, ".o": true, ".a": true, ".obj": true,
	".lib": true, ".app": true, ".msi": true, ".deb": true, ".rpm": true,
	// Documents (PDF is here; FileReadTool excludes it at the call site)
	".pdf": true, ".doc": true, ".docx": true, ".xls": true,
	".xlsx": true, ".ppt": true, ".pptx": true, ".odt": true,
	".ods": true, ".odp": true,
	// Fonts
	".ttf": true, ".otf": true, ".woff": true, ".woff2": true, ".eot": true,
	// Bytecode / VM artifacts
	".pyc": true, ".pyo": true, ".class": true, ".jar": true,
	".war": true, ".ear": true, ".node": true, ".wasm": true, ".rlib": true,
	// Database files
	".sqlite": true, ".sqlite3": true, ".db": true, ".mdb": true, ".idx": true,
	// Design / 3D
	".psd": true, ".ai": true, ".eps": true, ".sketch": true,
	".fig": true, ".xd": true, ".blend": true, ".3ds": true, ".max": true,
	// Flash
	".swf": true, ".fla": true,
	// Lock/profiling data
	".lockb": true, ".dat": true, ".data": true,
}

// HasBinaryExtension returns true if the file path has a binary extension.
func HasBinaryExtension(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	return BinaryExtensions[ext]
}

// IsBinaryContent checks if a buffer contains binary content by looking for null
// bytes or a high proportion of non-printable characters.
func IsBinaryContent(data []byte) bool {
	checkSize := len(data)
	if checkSize > BinaryCheckSize {
		checkSize = BinaryCheckSize
	}

	nonPrintable := 0
	for i := 0; i < checkSize; i++ {
		b := data[i]
		// Null byte is a strong indicator of binary
		if b == 0 {
			return true
		}
		// Count non-printable, non-whitespace bytes
		if b < 32 && b != 9 && b != 10 && b != 13 {
			nonPrintable++
		}
	}

	// If more than 10% non-printable, likely binary
	if checkSize == 0 {
		return false
	}
	return float64(nonPrintable)/float64(checkSize) > 0.1
}

// ---------------------------------------------------------------------------
// Generated file patterns — files to skip for code indexing
// ---------------------------------------------------------------------------

// GeneratedFilePatterns are glob patterns for generated files that should
// generally be excluded from code analysis.
var GeneratedFilePatterns = []string{
	"*.min.js", "*.min.css", "*.bundle.js", "*.chunk.js",
	"*.generated.*", "*.auto.*",
	"package-lock.json", "yarn.lock", "pnpm-lock.yaml",
	"go.sum", "Cargo.lock", "Gemfile.lock", "poetry.lock",
	"*.pb.go", "*.pb.ts", "*_generated.go",
}

// ---------------------------------------------------------------------------
// Ignored directories — directories to skip for file traversal
// ---------------------------------------------------------------------------

// IgnoredDirectories are directory names that should be excluded from
// file traversal and code indexing.
var IgnoredDirectories = map[string]bool{
	".git":          true,
	"node_modules":  true,
	".next":         true,
	".nuxt":         true,
	"dist":          true,
	"build":         true,
	"out":           true,
	".cache":        true,
	".parcel-cache": true,
	"__pycache__":   true,
	".mypy_cache":   true,
	".pytest_cache": true,
	".tox":          true,
	"venv":          true,
	".venv":         true,
	"env":           true,
	".env":          true,
	".idea":         true,
	".vscode":       true,
	".DS_Store":     true,
	"coverage":      true,
	".nyc_output":   true,
	"vendor":        true,
	".terraform":    true,
	".serverless":   true,
	".aws-sam":      true,
	"target":        true, // Rust, Java
	"Pods":          true, // iOS
	".gradle":       true,
}

// IsIgnoredDirectory returns true if the directory name should be skipped.
func IsIgnoredDirectory(name string) bool {
	return IgnoredDirectories[name]
}
