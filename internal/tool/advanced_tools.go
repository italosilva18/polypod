package tool

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/types"
)

// RegisterAdvancedTools registers advanced tool implementations that closely
// mirror the Claude Code TypeScript toolset (FileEditTool, FileWriteTool,
// GrepTool, GlobTool, WebFetchTool, AskUserQuestionTool).
//
// NOTE: file_edit, file_write, and undo_edit are registered by
// RegisterFileEditTools (in fileedit.go) which provides enhanced versions
// with unified diff preview, backup/undo support, and proper validation.
func RegisterAdvancedTools(reg *Registry, workingDir string) {
	// file_edit, file_write, undo_edit → registered via RegisterFileEditTools
	RegisterFileEditTools(reg, workingDir)
	reg.RegisterDef(newAdvancedGrepSearchTool(workingDir))
	reg.RegisterDef(newAdvancedGlobSearchTool(workingDir))
	reg.RegisterDef(newAdvancedWebFetchTool())
	// ask_user is now registered by RegisterAskUserTool (askuser.go)
}

// NOTE: file_edit and file_write have been moved to fileedit.go with enhanced
// implementations including unified diff preview, backup/undo, and proper validation.
// They are registered via RegisterFileEditTools called from RegisterAdvancedTools above.

// ---------------------------------------------------------------------------
// grep_search — search file contents using a regular expression
// ---------------------------------------------------------------------------

// maxGrepMatches limits the total number of matching lines returned.
const maxGrepMatches = 500

func newAdvancedGrepSearchTool(wd string) types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "grep_search" },
		DescriptionFn: func() string {
			return "Search file contents using a regular expression. " +
				"Returns matching lines with file paths and line numbers, sorted by modification time."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "The regular expression pattern to search for",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Directory or file to search in (default: working directory)",
					},
					"include": map[string]any{
						"type":        "string",
						"description": "File glob pattern to filter which files to search (e.g. \"*.go\", \"*.{ts,tsx}\")",
					},
				},
				"required": []string{"pattern"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			p, _ := args["pattern"].(string)
			if p == "" {
				return types.ValidationResult{Valid: false, Message: "pattern is required"}
			}
			// Validate regex compiles
			if _, err := regexp.Compile(p); err != nil {
				return types.ValidationResult{
					Valid:   false,
					Message: fmt.Sprintf("invalid regex: %v", err),
				}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			pattern, _ := args["pattern"].(string)
			searchPath := resolvePath(wd, args["path"])
			if searchPath == "" {
				searchPath = wd
			}
			includeGlob, _ := args["include"].(string)

			re, err := regexp.Compile(pattern)
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Invalid regex: %v", err),
					IsError: true,
				}, nil
			}

			type matchResult struct {
				file    string
				line    int
				text    string
				modTime time.Time
			}

			var matches []matchResult
			truncated := false

			walkErr := filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil // skip inaccessible entries
				}
				// Skip ignored directories
				if info.IsDir() {
					if constants.IsIgnoredDirectory(info.Name()) {
						return filepath.SkipDir
					}
					return nil
				}
				// Skip binary files
				if constants.HasBinaryExtension(path) {
					return nil
				}
				// Apply include filter
				if includeGlob != "" {
					matched, _ := filepath.Match(includeGlob, info.Name())
					if !matched {
						return nil
					}
				}
				// Skip large files (> 1MB)
				if info.Size() > 1024*1024 {
					return nil
				}

				f, fErr := os.Open(path)
				if fErr != nil {
					return nil
				}
				defer f.Close()

				scanner := bufio.NewScanner(f)
				lineNum := 0
				for scanner.Scan() {
					lineNum++
					line := scanner.Text()
					if re.MatchString(line) {
						matches = append(matches, matchResult{
							file:    path,
							line:    lineNum,
							text:    line,
							modTime: info.ModTime(),
						})
						if len(matches) >= maxGrepMatches {
							truncated = true
							return io.EOF // sentinel to stop walk
						}
					}
				}
				return nil
			})

			if walkErr != nil && walkErr != io.EOF {
				return types.ToolResult{
					Content: fmt.Sprintf("Error during search: %v", walkErr),
					IsError: true,
				}, nil
			}

			if len(matches) == 0 {
				return types.ToolResult{Content: "No matches found."}, nil
			}

			// Sort by modification time (most recent first), then by file+line
			sort.Slice(matches, func(i, j int) bool {
				if !matches[i].modTime.Equal(matches[j].modTime) {
					return matches[i].modTime.After(matches[j].modTime)
				}
				if matches[i].file != matches[j].file {
					return matches[i].file < matches[j].file
				}
				return matches[i].line < matches[j].line
			})

			var sb strings.Builder
			for _, m := range matches {
				// Truncate very long lines
				text := m.text
				if len(text) > 500 {
					text = text[:500] + "..."
				}
				sb.WriteString(fmt.Sprintf("%s:%d: %s\n", m.file, m.line, text))
			}
			if truncated {
				sb.WriteString(fmt.Sprintf("\n... [truncated at %d matches]", maxGrepMatches))
			}

			output := sb.String()
			if len(output) > constants.MaxOutputBytes {
				output = output[:constants.MaxOutputBytes] + "\n... [output truncated]"
			}
			return types.ToolResult{Content: output}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// glob_search — find files by glob pattern
// ---------------------------------------------------------------------------

func newAdvancedGlobSearchTool(wd string) types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "glob_search" },
		DescriptionFn: func() string {
			return "Find files matching a glob pattern. " +
				"Returns matching file paths sorted by modification time (most recent first)."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "The glob pattern to match files against (e.g. \"**/*.go\", \"*.md\")",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "The directory to search in (default: working directory)",
					},
				},
				"required": []string{"pattern"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			p, _ := args["pattern"].(string)
			if p == "" {
				return types.ValidationResult{Valid: false, Message: "pattern is required"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			pattern, _ := args["pattern"].(string)
			base := resolvePath(wd, args["path"])
			if base == "" {
				base = wd
			}

			type fileEntry struct {
				path    string
				modTime time.Time
			}

			var entries []fileEntry
			limit := constants.MaxGlobResults

			walkErr := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				// Skip ignored directories
				if info.IsDir() {
					if constants.IsIgnoredDirectory(info.Name()) {
						return filepath.SkipDir
					}
					return nil
				}
				// Match against the file name
				matched, _ := filepath.Match(pattern, info.Name())
				if !matched {
					// Also try matching against the full relative path for
					// patterns like "internal/**/*.go" (filepath.Match doesn't
					// support ** but we handle simple cases).
					rel, relErr := filepath.Rel(base, path)
					if relErr == nil {
						matched, _ = filepath.Match(pattern, rel)
					}
				}
				if matched {
					entries = append(entries, fileEntry{
						path:    path,
						modTime: info.ModTime(),
					})
					if len(entries) > limit*2 {
						// Collect extra for sorting then trim
						return nil
					}
				}
				return nil
			})

			if walkErr != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error during search: %v", walkErr),
					IsError: true,
				}, nil
			}

			if len(entries) == 0 {
				return types.ToolResult{Content: "No files found."}, nil
			}

			// Sort by modification time, most recent first
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].modTime.After(entries[j].modTime)
			})

			// Truncate to limit
			truncated := false
			if len(entries) > limit {
				entries = entries[:limit]
				truncated = true
			}

			var sb strings.Builder
			for _, e := range entries {
				sb.WriteString(e.path)
				sb.WriteByte('\n')
			}
			if truncated {
				sb.WriteString(fmt.Sprintf("... [results limited to %d files]", limit))
			}
			return types.ToolResult{Content: sb.String()}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// web_fetch — fetch content from a URL
// ---------------------------------------------------------------------------

// maxFetchBytes limits the body size read from HTTP responses.
const maxFetchBytes = 512 * 1024 // 512KB

// fetchTimeout is the HTTP client timeout.
const fetchTimeout = 30 * time.Second

func newAdvancedWebFetchTool() types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "web_fetch" },
		DescriptionFn: func() string {
			return "Fetch content from a URL via HTTP GET. Returns the body as text. " +
				"Content is truncated if it exceeds 512KB."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The URL to fetch content from",
					},
					"format": map[string]any{
						"type":        "string",
						"description": "Response format: \"text\" or \"markdown\" (default: \"text\")",
						"enum":        []string{"text", "markdown"},
					},
				},
				"required": []string{"url"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			u, _ := args["url"].(string)
			if u == "" {
				return types.ValidationResult{Valid: false, Message: "url is required"}
			}
			if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				return types.ValidationResult{
					Valid:   false,
					Message: "url must start with http:// or https://",
				}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			rawURL, _ := args["url"].(string)

			// Upgrade http to https
			if strings.HasPrefix(rawURL, "http://") {
				rawURL = "https://" + rawURL[len("http://"):]
			}

			client := &http.Client{
				Timeout: fetchTimeout,
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					if len(via) >= 10 {
						return fmt.Errorf("too many redirects")
					}
					return nil
				},
			}

			req, err := http.NewRequestWithContext(ctx.Context, http.MethodGet, rawURL, nil)
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error creating request: %v", err),
					IsError: true,
				}, nil
			}
			req.Header.Set("User-Agent", "polypod/1.0")
			req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

			resp, err := client.Do(req)
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error fetching URL: %v", err),
					IsError: true,
				}, nil
			}
			defer resp.Body.Close()

			// Read up to maxFetchBytes
			limited := io.LimitReader(resp.Body, int64(maxFetchBytes)+1)
			body, err := io.ReadAll(limited)
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error reading response: %v", err),
					IsError: true,
				}, nil
			}

			truncated := len(body) > maxFetchBytes
			if truncated {
				body = body[:maxFetchBytes]
			}

			content := string(body)

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("HTTP %d %s\n", resp.StatusCode, resp.Status))
			sb.WriteString(fmt.Sprintf("URL: %s\n", rawURL))
			sb.WriteString(fmt.Sprintf("Content-Type: %s\n", resp.Header.Get("Content-Type")))
			sb.WriteString(fmt.Sprintf("Size: %d bytes\n\n", len(body)))
			sb.WriteString(content)
			if truncated {
				sb.WriteString("\n\n... [content truncated at 512KB]")
			}

			return types.ToolResult{Content: sb.String()}, nil
		},
	}
}
