package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// MonitorTarget describes a single on-demand monitoring target. Either URL
// (HTTP GET) or FilePath (local file) is expected. Pattern is checked as a
// substring of the fetched/read body.
type MonitorTarget struct {
	ID         string    `json:"id"`
	URL        string    `json:"url,omitempty"`
	FilePath   string    `json:"file_path,omitempty"`
	Pattern    string    `json:"pattern"`
	LastCheck  time.Time `json:"last_check,omitempty"`
	LastStatus string    `json:"last_status,omitempty"`
}

// MonitorStore is a mutex-guarded map of monitor targets.
type MonitorStore struct {
	mu      sync.Mutex
	targets map[string]*MonitorTarget
	// httpClient is overridable for tests. If nil a 15s-timeout client is used.
	httpClient *http.Client
}

// NewMonitorStore creates an empty store with the default HTTP client.
func NewMonitorStore() *MonitorStore {
	return &MonitorStore{
		targets:    make(map[string]*MonitorTarget),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

var globalMonitorStore = NewMonitorStore()

// GetMonitorStore returns the process-wide monitor store.
func GetMonitorStore() *MonitorStore { return globalMonitorStore }

// Add inserts or replaces a target in the store. An auto-generated ID is
// assigned if the caller did not provide one.
func (s *MonitorStore) Add(target MonitorTarget) MonitorTarget {
	s.mu.Lock()
	defer s.mu.Unlock()
	if target.ID == "" {
		target.ID = fmt.Sprintf("mon-%d", time.Now().UnixNano())
	}
	copy := target
	s.targets[target.ID] = &copy
	return copy
}

// Remove deletes a target. Returns true if the target existed.
func (s *MonitorStore) Remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.targets[id]; !ok {
		return false
	}
	delete(s.targets, id)
	return true
}

// List returns a snapshot of all targets in no particular order.
func (s *MonitorStore) List() []MonitorTarget {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]MonitorTarget, 0, len(s.targets))
	for _, t := range s.targets {
		out = append(out, *t)
	}
	return out
}

// Get returns a copy of a target by ID.
func (s *MonitorStore) Get(id string) (MonitorTarget, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.targets[id]
	if !ok {
		return MonitorTarget{}, false
	}
	return *t, true
}

// Check performs a single on-demand check for the given target. It fetches
// the URL (HTTP GET) or reads the file and reports whether Pattern appears
// as a substring of the body.
func (s *MonitorStore) Check(ctx context.Context, id string) (status string, matched bool, err error) {
	s.mu.Lock()
	target, ok := s.targets[id]
	if !ok {
		s.mu.Unlock()
		return "", false, fmt.Errorf("monitor target not found: %s", id)
	}
	// Copy fields we need under the lock.
	url := target.URL
	filePath := target.FilePath
	pattern := target.Pattern
	client := s.httpClient
	s.mu.Unlock()

	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	var body string
	switch {
	case url != "":
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if reqErr != nil {
			status = fmt.Sprintf("request error: %v", reqErr)
			s.recordCheck(id, status)
			return status, false, reqErr
		}
		resp, doErr := client.Do(req)
		if doErr != nil {
			status = fmt.Sprintf("fetch error: %v", doErr)
			s.recordCheck(id, status)
			return status, false, doErr
		}
		defer resp.Body.Close()
		b, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			status = fmt.Sprintf("read error: %v", readErr)
			s.recordCheck(id, status)
			return status, false, readErr
		}
		body = string(b)
		status = fmt.Sprintf("HTTP %d", resp.StatusCode)
	case filePath != "":
		b, readErr := os.ReadFile(filePath)
		if readErr != nil {
			status = fmt.Sprintf("file error: %v", readErr)
			s.recordCheck(id, status)
			return status, false, readErr
		}
		body = string(b)
		status = fmt.Sprintf("file %d bytes", len(b))
	default:
		err = fmt.Errorf("target %s has neither URL nor file path", id)
		s.recordCheck(id, "invalid target")
		return "", false, err
	}

	if pattern != "" && strings.Contains(body, pattern) {
		matched = true
	}
	if matched {
		status += " (match)"
	} else if pattern != "" {
		status += " (no match)"
	}
	s.recordCheck(id, status)
	return status, matched, nil
}

// recordCheck updates LastCheck/LastStatus under the lock.
func (s *MonitorStore) recordCheck(id, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.targets[id]; ok {
		t.LastCheck = time.Now()
		t.LastStatus = status
	}
}

// NewMonitorTool creates the monitor tool that exposes the given store.
func NewMonitorTool(store *MonitorStore) types.Tool {
	if store == nil {
		store = GetMonitorStore()
	}
	return types.BuildTool(types.ToolDef{
		NameFn: func() string { return "monitor" },
		DescriptionFn: func() string {
			return "On-demand URL/file monitor with substring pattern matching. " +
				"Actions: add, remove, list, check."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"add", "remove", "list", "check"},
						"description": "Operation to perform.",
					},
					"id": map[string]any{
						"type":        "string",
						"description": "Target ID (required for remove and check; optional for add).",
					},
					"url": map[string]any{
						"type":        "string",
						"description": "URL to fetch via HTTP GET (add).",
					},
					"file": map[string]any{
						"type":        "string",
						"description": "Local file path to read (add).",
					},
					"pattern": map[string]any{
						"type":        "string",
						"description": "Substring to look for in the body (add).",
					},
				},
				"required": []string{"action"},
			}
		},
		IsReadOnlyFn: func(args map[string]any) bool {
			action, _ := args["action"].(string)
			return action == "list" || action == "check"
		},
		IsDestructiveFn: func(args map[string]any) bool {
			action, _ := args["action"].(string)
			return action == "remove"
		},
		IsConcurrencyFn: func(args map[string]any) bool {
			action, _ := args["action"].(string)
			return action == "list" || action == "check"
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			action, _ := args["action"].(string)
			if action == "" {
				return types.ValidationResult{Valid: false, Message: "action is required"}
			}
			switch action {
			case "add":
				url, _ := args["url"].(string)
				file, _ := args["file"].(string)
				if url == "" && file == "" {
					return types.ValidationResult{Valid: false, Message: "add requires url or file"}
				}
			case "remove", "check":
				id, _ := args["id"].(string)
				if id == "" {
					return types.ValidationResult{Valid: false, Message: "id is required for " + action}
				}
			case "list":
				// no extra requirements
			default:
				return types.ValidationResult{Valid: false, Message: "unknown action: " + action}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			action, _ := args["action"].(string)
			switch action {
			case "add":
				id, _ := args["id"].(string)
				url, _ := args["url"].(string)
				file, _ := args["file"].(string)
				pattern, _ := args["pattern"].(string)
				t := store.Add(MonitorTarget{
					ID:       id,
					URL:      url,
					FilePath: file,
					Pattern:  pattern,
				})
				data, _ := json.Marshal(t)
				return types.ToolResult{Content: "Target added:\n" + string(data)}, nil

			case "remove":
				id, _ := args["id"].(string)
				if !store.Remove(id) {
					return types.ToolResult{IsError: true, Content: "target not found: " + id}, nil
				}
				return types.ToolResult{Content: "Target removed: " + id}, nil

			case "list":
				targets := store.List()
				data, _ := json.Marshal(targets)
				return types.ToolResult{Content: fmt.Sprintf("%d targets:\n%s", len(targets), string(data))}, nil

			case "check":
				id, _ := args["id"].(string)
				checkCtx := ctx.Context
				if checkCtx == nil {
					checkCtx = context.Background()
				}
				status, matched, err := store.Check(checkCtx, id)
				if err != nil {
					return types.ToolResult{IsError: true, Content: err.Error()}, nil
				}
				return types.ToolResult{
					Content: fmt.Sprintf("id=%s status=%q matched=%t", id, status, matched),
				}, nil

			default:
				return types.ToolResult{IsError: true, Content: "unknown action: " + action}, nil
			}
		},
	})
}
