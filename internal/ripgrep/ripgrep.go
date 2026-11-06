// Package ripgrep provides utilities for finding, invoking, and parsing output
// from the ripgrep (rg) binary. It mirrors the behaviour of ripgrep.ts.
package ripgrep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// RipgrepMode indicates where the rg binary comes from.
type RipgrepMode string

const (
	ModeSystem   RipgrepMode = "system"
	ModeBuiltin  RipgrepMode = "builtin"
	ModeEmbedded RipgrepMode = "embedded"
)

// RipgrepOptions mirrors the options surface of the TS helper.
type RipgrepOptions struct {
	// CaseSensitive forces case-sensitive matching (default is smart-case).
	CaseSensitive bool
	// IgnoreCase forces case-insensitive matching.
	IgnoreCase bool
	// WholeWord adds word-boundary matching (-w).
	WholeWord bool
	// Regex treats pattern as a regular expression (default ripgrep behaviour).
	// Set to false only to request literal matching (-F).
	Regex bool
	// MaxResults caps the number of results (--max-count / -m).
	MaxResults int
	// FilePattern restricts search to file names matching this glob (-g).
	FilePattern string
	// ExcludePatterns is a list of globs to exclude (-g '!pattern').
	ExcludePatterns []string
	// IncludeHidden includes hidden files and directories (--hidden).
	IncludeHidden bool
	// FollowSymlinks follows symbolic links (-L).
	FollowSymlinks bool
	// JSONOutput requests --json output (required for ParseRipgrepOutput).
	JSONOutput bool
	// NoIgnore disables .gitignore / .ignore file processing (--no-ignore).
	NoIgnore bool
	// SingleThread uses -j 1, useful for EAGAIN retry in constrained environments.
	SingleThread bool
}

// RipgrepMatch is a single match returned by ParseRipgrepOutput.
type RipgrepMatch struct {
	File    string
	Line    int
	Column  int
	Match   string
	Context []string
}

// RipgrepTimeoutError is returned when a search times out with no results.
type RipgrepTimeoutError struct {
	msg            string
	PartialResults []string
}

func (e *RipgrepTimeoutError) Error() string { return e.msg }

// Status is the cached binary status.
type Status struct {
	Mode    RipgrepMode
	Path    string
	Working *bool // nil if not yet tested
}

// ---------------------------------------------------------------------------
// Binary discovery & caching
// ---------------------------------------------------------------------------

var (
	binaryCache     string
	binaryMode      RipgrepMode
	binaryCacheMu   sync.Mutex
	binaryCacheDone bool

	statusMu     sync.Mutex
	cachedStatus *binaryStatus
)

type binaryStatus struct {
	working bool
	tested  time.Time
}

// FindRipgrepBinary locates the rg executable.
// Priority:
//  1. USE_BUILTIN_RIPGREP=false (or unset) → look on PATH
//  2. Bundled vendor binary (OS/arch specific)
//
// The result is cached after the first successful call.
func FindRipgrepBinary() (string, error) {
	binaryCacheMu.Lock()
	defer binaryCacheMu.Unlock()

	if binaryCacheDone {
		if binaryCache == "" {
			return "", fmt.Errorf("ripgrep binary not found")
		}
		return binaryCache, nil
	}
	binaryCacheDone = true

	// Step 1: Check env flag. If USE_BUILTIN_RIPGREP is explicitly "false" or "0",
	// user prefers system rg.
	useBuiltin := os.Getenv("USE_BUILTIN_RIPGREP")
	wantsSystem := useBuiltin == "false" || useBuiltin == "0" || useBuiltin == ""

	if wantsSystem {
		if path, err := exec.LookPath("rg"); err == nil && path != "" {
			binaryCache = "rg" // Use bare name for security (no CWD hijacking).
			binaryMode = ModeSystem
			return binaryCache, nil
		}
	}

	// Step 2: Bundled binary relative to the executable.
	exePath, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exePath)
		candidates := vendorCandidates(exeDir)
		for _, c := range candidates {
			if _, err2 := os.Stat(c); err2 == nil {
				binaryCache = c
				binaryMode = ModeBuiltin
				return binaryCache, nil
			}
		}
	}

	// Step 3: Relative to working directory (development layout).
	cwd, _ := os.Getwd()
	for _, c := range vendorCandidates(cwd) {
		if _, err2 := os.Stat(c); err2 == nil {
			binaryCache = c
			binaryMode = ModeBuiltin
			return binaryCache, nil
		}
	}

	// Step 4: Last resort — try bare "rg" (may not be on PATH, will fail later).
	if _, err := exec.LookPath("rg"); err == nil {
		binaryCache = "rg"
		binaryMode = ModeSystem
		return binaryCache, nil
	}

	return "", fmt.Errorf("ripgrep (rg) binary not found: install ripgrep or set USE_BUILTIN_RIPGREP=false with rg on PATH")
}

// vendorCandidates returns OS/arch specific vendor paths relative to a base dir.
func vendorCandidates(base string) []string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Map Go arch names to the names used in TS vendor layout.
	archMap := map[string]string{
		"amd64": "x64",
		"arm64": "arm64",
		"386":   "ia32",
	}
	arch := goarch
	if mapped, ok := archMap[goarch]; ok {
		arch = mapped
	}

	var paths []string
	switch goos {
	case "windows":
		paths = append(paths, filepath.Join(base, "vendor", "ripgrep", arch+"-win32", "rg.exe"))
	default:
		paths = append(paths, filepath.Join(base, "vendor", "ripgrep", arch+"-"+goos, "rg"))
	}
	return paths
}

// GetMode returns the mode of the cached binary (only valid after FindRipgrepBinary).
func GetMode() RipgrepMode {
	binaryCacheMu.Lock()
	defer binaryCacheMu.Unlock()
	return binaryMode
}

// GetStatus returns current ripgrep availability status.
func GetStatus() Status {
	rgPath, err := FindRipgrepBinary()
	if err != nil {
		rgPath = ""
	}

	statusMu.Lock()
	s := cachedStatus
	statusMu.Unlock()

	st := Status{
		Mode: GetMode(),
		Path: rgPath,
	}
	if s != nil {
		w := s.working
		st.Working = &w
	}
	return st
}

// TestBinary tests whether rg --version works and caches the result.
func TestBinary() (bool, error) {
	statusMu.Lock()
	if cachedStatus != nil {
		w := cachedStatus.working
		statusMu.Unlock()
		return w, nil
	}
	statusMu.Unlock()

	rgPath, err := FindRipgrepBinary()
	if err != nil {
		w := false
		statusMu.Lock()
		cachedStatus = &binaryStatus{working: false, tested: time.Now()}
		statusMu.Unlock()
		return w, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, rgPath, "--version").Output()
	working := err == nil && bytes.HasPrefix(bytes.TrimSpace(out), []byte("ripgrep "))

	statusMu.Lock()
	cachedStatus = &binaryStatus{working: working, tested: time.Now()}
	statusMu.Unlock()

	return working, nil
}

// ---------------------------------------------------------------------------
// Argument building
// ---------------------------------------------------------------------------

// BuildRipgrepArgs converts an RipgrepOptions and pattern into a rg argument slice.
// The search path / target should be appended by the caller.
func BuildRipgrepArgs(pattern string, opts RipgrepOptions) []string {
	var args []string

	// Threading
	if opts.SingleThread {
		args = append(args, "-j", "1")
	}

	// Case sensitivity
	if opts.CaseSensitive {
		args = append(args, "-s")
	} else if opts.IgnoreCase {
		args = append(args, "-i")
	} else {
		args = append(args, "--smart-case")
	}

	// Whole word
	if opts.WholeWord {
		args = append(args, "-w")
	}

	// Literal vs regex
	if !opts.Regex {
		args = append(args, "-F")
	}

	// Max results
	if opts.MaxResults > 0 {
		args = append(args, "--max-count", fmt.Sprintf("%d", opts.MaxResults))
	}

	// File glob filter
	if opts.FilePattern != "" {
		args = append(args, "-g", opts.FilePattern)
	}

	// Exclude patterns
	for _, ex := range opts.ExcludePatterns {
		if !strings.HasPrefix(ex, "!") {
			ex = "!" + ex
		}
		args = append(args, "-g", ex)
	}

	// Hidden files
	if opts.IncludeHidden {
		args = append(args, "--hidden")
	}

	// Follow symlinks
	if opts.FollowSymlinks {
		args = append(args, "-L")
	}

	// JSON output
	if opts.JSONOutput {
		args = append(args, "--json")
	}

	// No ignore files
	if opts.NoIgnore {
		args = append(args, "--no-ignore")
	}

	// Pattern (always last before target)
	args = append(args, "--", pattern)

	return args
}

// ---------------------------------------------------------------------------
// Execution
// ---------------------------------------------------------------------------

const maxBufferSize = 20 * 1024 * 1024 // 20 MB

// wslDetectTimeout is the maximum time we spend checking whether the process
// is running inside WSL.  The check reads /proc/version which is normally
// instantaneous, but we cap it to avoid blocking startup on unusual systems.
const wslDetectTimeout = 500 * time.Millisecond

// isWSL reports whether the current environment is Windows Subsystem for Linux.
// It is cached after the first call so subsequent lookups are free.
var isWSL = sync.OnceValue(func() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), wslDetectTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "cat", "/proc/version").Output()
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(out))
	return strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl")
})

// defaultTimeout returns the search timeout appropriate for the current OS.
// WSL's I/O layer is significantly slower, so we use a longer timeout there.
func defaultTimeout() time.Duration {
	if isWSL() {
		return 60 * time.Second
	}
	return 20 * time.Second
}

// isEAGAIN returns true when stderr indicates an EAGAIN/resource-unavailable error.
func isEAGAIN(stderr string) bool {
	return strings.Contains(stderr, "os error 11") ||
		strings.Contains(stderr, "Resource temporarily unavailable")
}

// ExecRipgrep runs rg with the given args in the given cwd directory.
// It captures up to maxBufferSize bytes of output.
// Exit code 1 (no matches) is treated as success and returns empty output.
// Exit code 0 and 1 are both "not an error".
func ExecRipgrep(ctx context.Context, args []string, cwd string) ([]byte, error) {
	return execRg(ctx, args, cwd, false)
}

func execRg(ctx context.Context, args []string, cwd string, singleThread bool) ([]byte, error) {
	rgPath, err := FindRipgrepBinary()
	if err != nil {
		return nil, err
	}

	fullArgs := args
	if singleThread {
		fullArgs = append([]string{"-j", "1"}, args...)
	}

	timeout := defaultTimeout()
	if v := os.Getenv("CLAUDE_CODE_GLOB_TIMEOUT_SECONDS"); v != "" {
		var secs int
		fmt.Sscanf(v, "%d", &secs)
		if secs > 0 {
			timeout = time.Duration(secs) * time.Second
		}
	}

	ctx2, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx2, rgPath, fullArgs...)
	if cwd != "" {
		cmd.Dir = cwd
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &LimitWriter{buf: &stdout, limit: maxBufferSize}
	cmd.Stderr = &LimitWriter{buf: &stderr, limit: maxBufferSize}

	runErr := cmd.Run()
	if runErr == nil {
		return stdout.Bytes(), nil
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		code := exitErr.ExitCode()
		// Code 1 = no matches found — not an error.
		if code == 1 {
			return stdout.Bytes(), nil
		}
		// EAGAIN retry: resource temporarily unavailable — retry single-threaded.
		if !singleThread && isEAGAIN(stderr.String()) {
			return execRg(ctx, args, cwd, true)
		}
		// Code 2 = ripgrep error; other codes also surfaced.
		return stdout.Bytes(), fmt.Errorf("ripgrep exited with code %d: %s", code, stderr.String())
	}

	// Context / timeout
	if errors.Is(ctx2.Err(), context.DeadlineExceeded) {
		lines := parseLines(stdout.Bytes())
		if len(lines) == 0 {
			return nil, &RipgrepTimeoutError{
				msg:            "ripgrep search timed out after 20 seconds. Try a more specific path or pattern.",
				PartialResults: lines,
			}
		}
		return stdout.Bytes(), nil
	}

	return stdout.Bytes(), runErr
}

// LimitWriter is an io.Writer that truncates at a byte limit.
type LimitWriter struct {
	buf   *bytes.Buffer
	limit int
}

func (lw *LimitWriter) Write(p []byte) (int, error) {
	remaining := lw.limit - lw.buf.Len()
	if remaining <= 0 {
		return len(p), nil // discard silently
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	return lw.buf.Write(p)
}

// StreamRipgrepResults returns two channels: one for result lines, one for errors.
// Lines are emitted as they arrive (streaming, not buffered).
// The caller should drain both channels; the error channel receives at most one value.
func StreamRipgrepResults(ctx context.Context, args []string, cwd string) (<-chan string, <-chan error) {
	lines := make(chan string, 256)
	errc := make(chan error, 1)

	go func() {
		defer close(lines)
		defer close(errc)

		rgPath, err := FindRipgrepBinary()
		if err != nil {
			errc <- err
			return
		}

		cmd := exec.CommandContext(ctx, rgPath, args...)
		if cwd != "" {
			cmd.Dir = cwd
		}

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			errc <- fmt.Errorf("stdout pipe: %w", err)
			return
		}
		if err := cmd.Start(); err != nil {
			errc <- fmt.Errorf("start rg: %w", err)
			return
		}

		buf := make([]byte, 32*1024)
		var remainder string
		for {
			n, readErr := stdout.Read(buf)
			if n > 0 {
				data := remainder + string(buf[:n])
				parts := strings.Split(data, "\n")
				remainder = parts[len(parts)-1]
				for _, line := range parts[:len(parts)-1] {
					line = strings.TrimSuffix(line, "\r")
					if ctx.Err() != nil {
						break
					}
					lines <- line
				}
			}
			if readErr != nil {
				break
			}
		}

		if ctx.Err() == nil && remainder != "" {
			lines <- strings.TrimSuffix(remainder, "\r")
		}

		if waitErr := cmd.Wait(); waitErr != nil {
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1 {
				return // no matches — not an error
			}
			if ctx.Err() == nil {
				errc <- waitErr
			}
		}
	}()

	return lines, errc
}

// CountRipgrepMatches runs rg --count and returns the total number of matches.
func CountRipgrepMatches(ctx context.Context, pattern string, paths []string, cwd string) (int, error) {
	args := []string{"--count", "--", pattern}
	args = append(args, paths...)

	out, err := ExecRipgrep(ctx, args, cwd)
	if err != nil {
		return 0, err
	}

	total := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		// Output format: "file:count"
		parts := strings.Split(line, ":")
		if len(parts) < 2 {
			continue
		}
		var n int
		fmt.Sscanf(parts[len(parts)-1], "%d", &n)
		total += n
	}
	return total, nil
}

// ---------------------------------------------------------------------------
// JSON output parsing
// ---------------------------------------------------------------------------

// rgJSONMessage is one JSON line from rg --json output.
type rgJSONMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type rgMatchData struct {
	Path       rgText `json:"path"`
	Lines      rgText `json:"lines"`
	LineNum    uint64 `json:"line_number"`
	Submatches []struct {
		Match rgText `json:"match"`
		Start int    `json:"start"`
		End   int    `json:"end"`
	} `json:"submatches"`
}

type rgContextData struct {
	Path    rgText `json:"path"`
	Lines   rgText `json:"lines"`
	LineNum uint64 `json:"line_number"`
}

type rgText struct {
	Text string `json:"text"`
}

// ParseRipgrepOutput parses the JSON output produced by rg --json.
// Multiple matches on the same file/line are merged into a single RipgrepMatch.
func ParseRipgrepOutput(output []byte) []RipgrepMatch {
	var results []RipgrepMatch

	for _, rawLine := range bytes.Split(output, []byte("\n")) {
		rawLine = bytes.TrimSpace(rawLine)
		if len(rawLine) == 0 {
			continue
		}

		var msg rgJSONMessage
		if err := json.Unmarshal(rawLine, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "match":
			var d rgMatchData
			if err := json.Unmarshal(msg.Data, &d); err != nil {
				continue
			}
			col := 0
			matchText := ""
			if len(d.Submatches) > 0 {
				col = d.Submatches[0].Start + 1 // 1-indexed
				matchText = d.Submatches[0].Match.Text
			}
			results = append(results, RipgrepMatch{
				File:   d.Path.Text,
				Line:   int(d.LineNum),
				Column: col,
				Match:  matchText,
			})

		case "context":
			// Attach context lines to the preceding match if any.
			if len(results) == 0 {
				continue
			}
			var d rgContextData
			if err := json.Unmarshal(msg.Data, &d); err != nil {
				continue
			}
			last := &results[len(results)-1]
			last.Context = append(last.Context,
				fmt.Sprintf("%d: %s", d.LineNum, strings.TrimRight(d.Lines.Text, "\r\n")))
		}
	}

	return results
}

// ---------------------------------------------------------------------------
// Plain-line parsing helper
// ---------------------------------------------------------------------------

func parseLines(data []byte) []string {
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return nil
	}
	var out []string
	for _, l := range strings.Split(raw, "\n") {
		l = strings.TrimSuffix(l, "\r")
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Run is the primary high-level entry point mirroring ripGrep() in TS.
// It returns lines from stdout on success (exit 0 or 1).
func Run(ctx context.Context, args []string, target string, cwd string) ([]string, error) {
	fullArgs := append(args, target)
	out, err := ExecRipgrep(ctx, fullArgs, cwd)
	if err != nil {
		return nil, err
	}
	return parseLines(out), nil
}

// CountFilesInDir counts files under dir using rg --files and returns
// the count rounded to the nearest power of 10 (for privacy).
// Returns -1 if counting is not applicable (e.g. home directory).
func CountFilesInDir(ctx context.Context, dir string, ignorePatterns []string) (int, error) {
	home, _ := os.UserHomeDir()
	absDir, _ := filepath.Abs(dir)
	if home != "" {
		absHome, _ := filepath.Abs(home)
		if absDir == absHome {
			return -1, nil // skip home dir
		}
	}

	args := []string{"--files", "--hidden"}
	for _, pat := range ignorePatterns {
		args = append(args, "--glob", "!"+pat)
	}
	args = append(args, dir)

	rgPath, err := FindRipgrepBinary()
	if err != nil {
		return 0, err
	}

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx2, rgPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}

	count := 0
	buf := make([]byte, 32*1024)
	for {
		n, rerr := stdout.Read(buf)
		for i := 0; i < n; i++ {
			if buf[i] == '\n' {
				count++
			}
		}
		if rerr != nil {
			break
		}
	}
	_ = cmd.Wait()

	if count == 0 {
		return 0, nil
	}
	// Round to nearest power of 10.
	mag := 1
	for mag*10 <= count {
		mag *= 10
	}
	rounded := ((count + mag/2) / mag) * mag
	return rounded, nil
}
