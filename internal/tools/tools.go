// Package tools — Tool Wrappers extracted from Claude Code
// Sources: src/tools/BashTool/, src/tools/FileReadTool/, etc.

package tools

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// BashResult holds bash command result
type BashResult struct {
	Command    string
	ExitCode   int
	Stdout     string
	Stderr     string
	Duration   time.Duration
	TimedOut   bool
}

// BashTool executes bash commands
func BashTool(command string, timeout time.Duration) (*BashResult, error) {
	startTime := time.Now()
	
	cmd := exec.Command("bash", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	
	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()
	
	select {
	case err := <-done:
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}
		
		return &BashResult{
			Command:  command,
			ExitCode: exitCode,
			Stdout:   stdout.String(),
			Stderr:   stderr.String(),
			Duration: time.Since(startTime),
		}, nil
		
	case <-time.After(timeout):
		cmd.Process.Kill()
		return &BashResult{
			Command:  command,
			TimedOut: true,
			Duration: timeout,
		}, fmt.Errorf("command timed out after %v", timeout)
	}
}

// ReadFileResult holds file read result
type ReadFileResult struct {
	Path     string
	Content  string
	Encoding string
	LineEnd  string
	Size     int64
}

// ReadFileTool reads file content
func ReadFileTool(path string) (*ReadFileResult, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, err
	}
	
	// Detect encoding and line endings (simplified)
	encoding := "utf-8"
	lineEnd := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		lineEnd = "\r\n"
	}
	
	return &ReadFileResult{
		Path:     absPath,
		Content:  string(data),
		Encoding: encoding,
		LineEnd:  lineEnd,
		Size:     info.Size(),
	}, nil
}

// WriteFileResult holds file write result
type WriteFileResult struct {
	Path      string
	Bytes     int64
	Timestamp time.Time
}

// WriteFileTool writes content to file
func WriteFileTool(path, content string) (*WriteFileResult, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	
	// Create directory if needed
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	
	err = os.WriteFile(absPath, []byte(content), 0644)
	if err != nil {
		return nil, err
	}
	
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, err
	}
	
	return &WriteFileResult{
		Path:      absPath,
		Bytes:     info.Size(),
		Timestamp: info.ModTime(),
	}, nil
}

// EditFileResult holds file edit result
type EditFileResult struct {
	Path       string
	OldContent string
	NewContent string
	Changes    []string
}

// EditFileTool edits file with sed-like replacement
func EditFileTool(path, oldStr, newStr string) (*EditFileResult, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	
	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	
	oldContent := string(content)
	newContent := strings.Replace(oldContent, oldStr, newStr, 1)
	
	if oldContent == newContent {
		return nil, fmt.Errorf("string to replace not found")
	}
	
	err = os.WriteFile(absPath, []byte(newContent), 0644)
	if err != nil {
		return nil, err
	}
	
	changes := []string{fmt.Sprintf("Replaced '%s' with '%s'", oldStr, newStr)}
	
	return &EditFileResult{
		Path:       absPath,
		OldContent: oldContent,
		NewContent: newContent,
		Changes:    changes,
	}, nil
}

// GlobResult holds glob result
type GlobResult struct {
	Pattern string
	Matches []string
	Count   int
}

// GlobTool finds files matching pattern
func GlobTool(pattern string) (*GlobResult, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	
	return &GlobResult{
		Pattern: pattern,
		Matches: matches,
		Count:   len(matches),
	}, nil
}

// GrepResult holds grep result
type GrepResult struct {
	Pattern string
	Path    string
	Matches []GrepMatch
	Count   int
}

// GrepMatch holds a single grep match
type GrepMatch struct {
	LineNum   int
	Line      string
	FilePath  string
}

// GrepTool searches for pattern in files
func GrepTool(pattern, path string) (*GrepResult, error) {
	cmd := exec.Command("grep", "-rn", "--color=never", pattern, path)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	
	err := cmd.Run()
	if err != nil && err.Error() != "exit status 1" {
		return nil, err
	}
	
	var matches []GrepMatch
	lines := strings.Split(stdout.String(), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) >= 3 {
			var lineNum int
			fmt.Sscanf(parts[1], "%d", &lineNum)
			matches = append(matches, GrepMatch{
				LineNum:  lineNum,
				Line:     parts[2],
				FilePath: parts[0],
			})
		}
	}
	
	return &GrepResult{
		Pattern: pattern,
		Path:    path,
		Matches: matches,
		Count:   len(matches),
	}, nil
}

// FormatBashResult formats bash result for display
func FormatBashResult(result *BashResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Command: %s\n", result.Command))
	sb.WriteString(fmt.Sprintf("Exit code: %d\n", result.ExitCode))
	sb.WriteString(fmt.Sprintf("Duration: %v\n", result.Duration))
	
	if result.Stdout != "" {
		sb.WriteString(fmt.Sprintf("Stdout:\n%s\n", result.Stdout))
	}
	if result.Stderr != "" {
		sb.WriteString(fmt.Sprintf("Stderr:\n%s\n", result.Stderr))
	}
	if result.TimedOut {
		sb.WriteString("⚠️  Command timed out\n")
	}
	
	return sb.String()
}

// FormatFileResult formats file result for display
func FormatFileResult(path, content string, size int64) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📄 **%s** (%s)\n\n", filepath.Base(path), formatSize(size)))
	sb.WriteString("```")
	sb.WriteString(content)
	sb.WriteString("\n```")
	return sb.String()
}

func formatSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d bytes", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
}
