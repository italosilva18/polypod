package fileops

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// MaxScanBytes is the maximum number of bytes scanned when searching for content.
const MaxScanBytes = 10 * 1024 * 1024

// ChunkSize is the read buffer size for scanning operations.
const ChunkSize = 8 * 1024

// LineRange represents a range of lines from a file.
type LineRange struct {
	Start   int    // 1-based start line number.
	End     int    // 1-based end line number (inclusive).
	Content string // The content of the lines in this range.
}

// ReadFileRangeResult is the result of reading a line range from a file.
type ReadFileRangeResult struct {
	Content          string
	LineCount        int   // Number of lines returned.
	TotalLines       int   // Total lines in the file.
	TotalBytes       int64 // Total file size in bytes.
	ReadBytes        int64 // Bytes actually read/returned.
	MtimeNs          int64 // Modification time in nanoseconds.
	TruncatedByBytes bool  // True if output was clipped to maxBytes.
}

// FileTooLargeError is returned when a file exceeds the maximum allowed size.
type FileTooLargeError struct {
	SizeInBytes int64
	MaxBytes    int64
}

func (e *FileTooLargeError) Error() string {
	return fmt.Sprintf("file content (%s) exceeds maximum allowed size (%s)",
		FormatFileSize(e.SizeInBytes), FormatFileSize(e.MaxBytes))
}

// ReadFileInRange reads specific line ranges from a file.
// offset is 0-based line index, maxLines limits the number of lines returned.
// maxBytes limits the total output size (0 for unlimited).
func ReadFileInRange(filePath string, offset, maxLines int, maxBytes int64, truncateOnByteLimit bool) (*ReadFileRangeResult, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("EISDIR: illegal operation on a directory, read '%s'", filePath)
	}

	mtimeNs := info.ModTime().UnixNano()

	// Fast path: small files read entirely into memory.
	if info.Size() < 10*1024*1024 { // 10 MB
		if !truncateOnByteLimit && maxBytes > 0 && info.Size() > maxBytes {
			return nil, &FileTooLargeError{SizeInBytes: info.Size(), MaxBytes: maxBytes}
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}

		return readRangeFast(data, mtimeNs, offset, maxLines, maxBytes, truncateOnByteLimit)
	}

	// Streaming path for large files.
	return readRangeStreaming(filePath, mtimeNs, offset, maxLines, maxBytes, truncateOnByteLimit)
}

func readRangeFast(data []byte, mtimeNs int64, offset, maxLines int, maxBytes int64, truncate bool) (*ReadFileRangeResult, error) {
	// Strip BOM.
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}

	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")

	endLine := len(lines)
	if maxLines > 0 && offset+maxLines < endLine {
		endLine = offset + maxLines
	}
	if offset > len(lines) {
		offset = len(lines)
	}

	var selected []string
	var selectedBytes int64
	truncatedByBytes := false

	for i := offset; i < endLine; i++ {
		line := lines[i]
		if truncate && maxBytes > 0 {
			sep := int64(0)
			if len(selected) > 0 {
				sep = 1
			}
			nextBytes := selectedBytes + sep + int64(len(line))
			if nextBytes > maxBytes {
				truncatedByBytes = true
				break
			}
			selectedBytes = nextBytes
		}
		selected = append(selected, line)
	}

	content := strings.Join(selected, "\n")
	readBytes := int64(len(content))

	return &ReadFileRangeResult{
		Content:          content,
		LineCount:        len(selected),
		TotalLines:       len(lines),
		TotalBytes:       int64(len(text)),
		ReadBytes:        readBytes,
		MtimeNs:          mtimeNs,
		TruncatedByBytes: truncatedByBytes,
	}, nil
}

func readRangeStreaming(filePath string, mtimeNs int64, offset, maxLines int, maxBytes int64, truncateOnByteLimit bool) (*ReadFileRangeResult, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	endLine := offset + maxLines
	if maxLines <= 0 {
		endLine = int(^uint(0) >> 1) // MaxInt
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 512*1024), 512*1024)

	var selected []string
	var selectedBytes int64
	lineIndex := 0
	var totalBytesRead int64
	truncatedByBytes := false
	isFirstLine := true

	for scanner.Scan() {
		line := scanner.Text()
		totalBytesRead += int64(len(line)) + 1 // +1 for newline.

		// Strip BOM from first line (UTF-8 BOM is 3 bytes: EF BB BF, but
		// scanner.Text() returns a string so the BOM appears as rune U+FEFF).
		if isFirstLine {
			isFirstLine = false
			if strings.HasPrefix(line, "\xEF\xBB\xBF") {
				line = line[3:]
			} else if strings.HasPrefix(line, "\uFEFF") {
				line = strings.TrimPrefix(line, "\uFEFF")
			}
		}

		// Strip \r.
		line = strings.TrimSuffix(line, "\r")

		if !truncateOnByteLimit && maxBytes > 0 && totalBytesRead > maxBytes {
			return nil, &FileTooLargeError{SizeInBytes: totalBytesRead, MaxBytes: maxBytes}
		}

		if lineIndex >= offset && lineIndex < endLine && !truncatedByBytes {
			if truncateOnByteLimit && maxBytes > 0 {
				sep := int64(0)
				if len(selected) > 0 {
					sep = 1
				}
				nextBytes := selectedBytes + sep + int64(len(line))
				if nextBytes > maxBytes {
					truncatedByBytes = true
				} else {
					selectedBytes = nextBytes
					selected = append(selected, line)
				}
			} else {
				selected = append(selected, line)
			}
		}
		lineIndex++
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	content := strings.Join(selected, "\n")

	return &ReadFileRangeResult{
		Content:          content,
		LineCount:        len(selected),
		TotalLines:       lineIndex,
		TotalBytes:       totalBytesRead,
		ReadBytes:        int64(len(content)),
		MtimeNs:          mtimeNs,
		TruncatedByBytes: truncatedByBytes,
	}, nil
}

// EditContext is the result of scanning a file for a needle with surrounding context.
type EditContext struct {
	Content    string // Context-window slice containing the match.
	LineOffset int    // 1-based line number of the first line in Content.
	Truncated  bool   // True if MaxScanBytes was reached without finding the needle.
}

// ReadEditContext finds needle in the file and returns surrounding context lines.
// Returns nil if the file does not exist (ENOENT).
// Returns EditContext{Truncated: true} if the needle is not found within MaxScanBytes.
func ReadEditContext(filePath, needle string, contextLines int) (*EditContext, error) {
	if needle == "" {
		return &EditContext{Content: "", LineOffset: 1}, nil
	}

	f, err := os.Open(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	return scanForContext(f, needle, contextLines)
}

func scanForContext(r io.ReadSeeker, needle string, contextLines int) (*EditContext, error) {
	// Read file content up to MaxScanBytes.
	buf := make([]byte, 0, ChunkSize)
	total := 0
	tmp := make([]byte, ChunkSize)

	for total < MaxScanBytes {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			total += n
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}

	content := string(buf)
	// Normalize CRLF.
	if strings.Contains(content, "\r\n") {
		content = strings.ReplaceAll(content, "\r\n", "\n")
	}

	// Try LF needle first, then CRLF.
	matchIdx := strings.Index(content, needle)
	if matchIdx == -1 {
		needleCRLF := strings.ReplaceAll(needle, "\n", "\r\n")
		matchIdx = strings.Index(content, needleCRLF)
		if matchIdx != -1 {
			needle = needleCRLF
		}
	}

	if matchIdx == -1 {
		truncated := total >= MaxScanBytes
		return &EditContext{Content: "", LineOffset: 1, Truncated: truncated}, nil
	}

	// Count lines before match.
	linesBeforeMatch := strings.Count(content[:matchIdx], "\n")

	// Find context window.
	matchEnd := matchIdx + len(needle)

	// Scan backward for contextLines newlines.
	ctxStart := matchIdx
	nlSeen := 0
	for i := matchIdx - 1; i >= 0 && nlSeen <= contextLines; i-- {
		if content[i] == '\n' {
			nlSeen++
			if nlSeen > contextLines {
				ctxStart = i + 1
				break
			}
		}
		ctxStart = i
	}

	// Scan forward for contextLines+1 newlines.
	ctxEnd := matchEnd
	nlSeen = 0
	for i := matchEnd; i < len(content); i++ {
		ctxEnd = i + 1
		if content[i] == '\n' {
			nlSeen++
			if nlSeen >= contextLines+1 {
				break
			}
		}
	}

	lineOffset := linesBeforeMatch - strings.Count(content[ctxStart:matchIdx], "\n") + 1
	if lineOffset < 1 {
		lineOffset = 1
	}

	return &EditContext{
		Content:    content[ctxStart:ctxEnd],
		LineOffset: lineOffset,
		Truncated:  false,
	}, nil
}

// GetFunctionContext reads the surrounding function/class context around a given line number.
// It searches outward from the target line for opening braces/indent blocks.
// contextBefore and contextAfter control how many extra lines to include.
func GetFunctionContext(filePath string, targetLine, contextBefore, contextAfter int) (*LineRange, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)

	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if targetLine < 1 || targetLine > len(lines) {
		return nil, fmt.Errorf("line %d out of range [1, %d]", targetLine, len(lines))
	}

	// Find the enclosing block by looking for brace nesting.
	targetIdx := targetLine - 1
	startIdx := targetIdx
	endIdx := targetIdx

	// Search backward for function/class start.
	braceDepth := 0
	for i := targetIdx; i >= 0; i-- {
		line := lines[i]
		braceDepth += strings.Count(line, "}") - strings.Count(line, "{")
		if braceDepth < 0 || isFuncOrClassDecl(line) {
			startIdx = i
			break
		}
		startIdx = i
	}

	// Search forward for the matching closing brace.
	braceDepth = 0
	for i := startIdx; i < len(lines); i++ {
		line := lines[i]
		braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
		endIdx = i
		if i > startIdx && braceDepth <= 0 {
			break
		}
	}

	// Apply context margins.
	startIdx -= contextBefore
	endIdx += contextAfter
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx >= len(lines) {
		endIdx = len(lines) - 1
	}

	var buf bytes.Buffer
	for i := startIdx; i <= endIdx; i++ {
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(lines[i])
	}

	return &LineRange{
		Start:   startIdx + 1,
		End:     endIdx + 1,
		Content: buf.String(),
	}, nil
}

// isFuncOrClassDecl is a heuristic check for function/class/method declarations.
func isFuncOrClassDecl(line string) bool {
	trimmed := strings.TrimSpace(line)
	for _, prefix := range []string{
		"func ", "function ", "def ", "class ", "type ", "struct ",
		"pub fn ", "fn ", "impl ", "export ", "async ",
		"public ", "private ", "protected ", "static ",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// FormatFileSize formats bytes into a human-readable string.
func FormatFileSize(sizeInBytes int64) string {
	kb := float64(sizeInBytes) / 1024
	if kb < 1 {
		return fmt.Sprintf("%d bytes", sizeInBytes)
	}
	if kb < 1024 {
		return formatFloat(kb) + "KB"
	}
	mb := kb / 1024
	if mb < 1024 {
		return formatFloat(mb) + "MB"
	}
	gb := mb / 1024
	return formatFloat(gb) + "GB"
}

func formatFloat(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	if strings.HasSuffix(s, ".0") {
		return s[:len(s)-2]
	}
	return s
}
