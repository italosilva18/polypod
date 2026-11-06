package fileops

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Binary detection thresholds.
const (
	// BinarySampleSize is the number of bytes to read for binary detection.
	BinarySampleSize = 8192
	// BinaryNullThreshold is the maximum ratio of null bytes before a file is considered binary.
	BinaryNullThreshold = 0.01
	// LargeFileThreshold is the default threshold above which a file is "large" (10 MB).
	LargeFileThreshold = 10 * 1024 * 1024
)

// IsBinaryFile detects binary files by sampling content for null bytes.
// Returns true if the file appears to be binary, false for text files.
func IsBinaryFile(filePath string) (bool, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	buf := make([]byte, BinarySampleSize)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return false, err
	}
	buf = buf[:n]

	return IsBinaryContent(buf), nil
}

// IsBinaryContent checks if a byte slice appears to be binary content.
func IsBinaryContent(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	nullCount := 0
	for _, b := range data {
		if b == 0 {
			nullCount++
		}
	}

	return float64(nullCount)/float64(len(data)) > BinaryNullThreshold
}

// Lockfile and generated file lists (matching the TypeScript EXCLUDED_FILENAMES).
var excludedFilenames = map[string]bool{
	"package-lock.json":   true,
	"yarn.lock":           true,
	"pnpm-lock.yaml":      true,
	"bun.lockb":           true,
	"bun.lock":            true,
	"composer.lock":       true,
	"gemfile.lock":        true,
	"cargo.lock":          true,
	"poetry.lock":         true,
	"pipfile.lock":        true,
	"shrinkwrap.json":     true,
	"npm-shrinkwrap.json": true,
}

var excludedExtensions = map[string]bool{
	".lock":         true,
	".min.js":       true,
	".min.css":      true,
	".min.html":     true,
	".bundle.js":    true,
	".bundle.css":   true,
	".generated.ts": true,
	".generated.js": true,
	".d.ts":         true,
}

var excludedDirectories = []string{
	"/dist/", "/build/", "/out/", "/output/",
	"/node_modules/", "/vendor/", "/vendored/",
	"/third_party/", "/third-party/", "/external/",
	"/.next/", "/.nuxt/", "/.svelte-kit/",
	"/coverage/", "/__pycache__/", "/.tox/",
	"/venv/", "/.venv/",
	"/target/release/", "/target/debug/",
}

var excludedFilenamePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^.*\.min\.[a-z]+$`),
	regexp.MustCompile(`(?i)^.*-min\.[a-z]+$`),
	regexp.MustCompile(`(?i)^.*\.bundle\.[a-z]+$`),
	regexp.MustCompile(`(?i)^.*\.generated\.[a-z]+$`),
	regexp.MustCompile(`(?i)^.*\.gen\.[a-z]+$`),
	regexp.MustCompile(`(?i)^.*\.auto\.[a-z]+$`),
	regexp.MustCompile(`(?i)^.*_generated\.[a-z]+$`),
	regexp.MustCompile(`(?i)^.*_gen\.[a-z]+$`),
	regexp.MustCompile(`(?i)^.*\.pb\.(go|js|ts|py|rb)$`),
	regexp.MustCompile(`(?i)^.*_pb2?\.py$`),
	regexp.MustCompile(`(?i)^.*\.pb\.h$`),
	regexp.MustCompile(`(?i)^.*\.grpc\.[a-z]+$`),
	regexp.MustCompile(`(?i)^.*\.swagger\.[a-z]+$`),
	regexp.MustCompile(`(?i)^.*\.openapi\.[a-z]+$`),
}

// IsGeneratedFile checks if a file should be treated as generated/vendored.
// Based on GitHub Linguist rules: lockfiles, minified files, build output, etc.
func IsGeneratedFile(filePath string) bool {
	// Normalize to forward slashes for consistent matching.
	normalizedPath := "/" + strings.ReplaceAll(filepath.ToSlash(filePath), "//", "/")
	normalizedPath = strings.TrimPrefix(normalizedPath, "//")
	if !strings.HasPrefix(normalizedPath, "/") {
		normalizedPath = "/" + normalizedPath
	}

	fileName := strings.ToLower(filepath.Base(filePath))
	ext := strings.ToLower(filepath.Ext(filePath))

	// Exact filename match.
	if excludedFilenames[fileName] {
		return true
	}

	// Extension match.
	if excludedExtensions[ext] {
		return true
	}

	// Compound extension (e.g., .min.js).
	parts := strings.Split(fileName, ".")
	if len(parts) > 2 {
		compoundExt := "." + strings.Join(parts[len(parts)-2:], ".")
		if excludedExtensions[compoundExt] {
			return true
		}
	}

	// Directory patterns.
	for _, dir := range excludedDirectories {
		if strings.Contains(normalizedPath, dir) {
			return true
		}
	}

	// Regex filename patterns.
	for _, re := range excludedFilenamePatterns {
		if re.MatchString(fileName) {
			return true
		}
	}

	return false
}

// FilterGeneratedFiles removes generated files from the list.
func FilterGeneratedFiles(files []string) []string {
	result := make([]string, 0, len(files))
	for _, f := range files {
		if !IsGeneratedFile(f) {
			result = append(result, f)
		}
	}
	return result
}

// FileType describes the detected type of a file.
type FileType string

const (
	FileTypeText      FileType = "text"
	FileTypeBinary    FileType = "binary"
	FileTypeImage     FileType = "image"
	FileTypeGenerated FileType = "generated"
	FileTypeUnknown   FileType = "unknown"
)

// Common image extensions.
var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".svg": true, ".bmp": true, ".ico": true,
	".tiff": true, ".tif": true,
}

// DetectFileType determines the file type from extension and optional content.
func DetectFileType(filePath string) FileType {
	ext := strings.ToLower(filepath.Ext(filePath))

	if imageExtensions[ext] {
		return FileTypeImage
	}

	if IsGeneratedFile(filePath) {
		return FileTypeGenerated
	}

	binary, err := IsBinaryFile(filePath)
	if err != nil {
		return FileTypeUnknown
	}
	if binary {
		return FileTypeBinary
	}

	return FileTypeText
}

// IsLargeFile checks if a file exceeds the given size threshold.
// If threshold is 0, DefaultLargeFileThreshold is used.
func IsLargeFile(filePath string, threshold int64) (bool, error) {
	if threshold <= 0 {
		threshold = LargeFileThreshold
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return false, err
	}
	return info.Size() > threshold, nil
}

// ShouldSkipFile combines multiple skip criteria for search/read operations.
// Returns true if the file should be skipped, along with a reason string.
func ShouldSkipFile(filePath string, maxSize int64) (skip bool, reason string) {
	// Check if generated.
	if IsGeneratedFile(filePath) {
		return true, "generated/vendored file"
	}

	// Check size.
	if maxSize > 0 {
		info, err := os.Stat(filePath)
		if err != nil {
			return true, "cannot stat file"
		}
		if info.Size() > maxSize {
			return true, "file too large"
		}
	}

	// Check binary.
	binary, err := IsBinaryFile(filePath)
	if err != nil {
		return false, "" // Can't determine, don't skip.
	}
	if binary {
		return true, "binary file"
	}

	return false, ""
}
