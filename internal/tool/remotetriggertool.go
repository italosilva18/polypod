package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// ---------------------------------------------------------------------------
// RemoteTriggerStore
// ---------------------------------------------------------------------------

// RemoteTriggerStore holds a mutex-guarded registry of named HTTP endpoints
// that can be invoked by the remote_trigger tool. The store owns a single
// http.Client so that connections are pooled across triggers.
type RemoteTriggerStore struct {
	mu      sync.RWMutex
	targets map[string]string
	client  *http.Client
}

// NewRemoteTriggerStore constructs an empty store with a 30s-timeout client.
func NewRemoteTriggerStore() *RemoteTriggerStore {
	return &RemoteTriggerStore{
		targets: make(map[string]string),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Register adds or replaces a named endpoint. Empty names or URLs are ignored.
func (s *RemoteTriggerStore) Register(name, url string) {
	if name == "" || url == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.targets[name] = url
}

// Lookup returns the URL associated with name and whether it exists.
func (s *RemoteTriggerStore) Lookup(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	url, ok := s.targets[name]
	return url, ok
}

// Names returns all registered trigger names, sorted.
func (s *RemoteTriggerStore) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.targets))
	for k := range s.targets {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Trigger POSTs the JSON-encoded payload to the registered endpoint and
// returns the response body. Non-2xx responses produce an error that still
// includes the body bytes for diagnostics.
func (s *RemoteTriggerStore) Trigger(ctx context.Context, name string, payload any) ([]byte, error) {
	url, ok := s.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("remote trigger %q is not registered", name)
	}

	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		body = bytes.NewReader(data)
	} else {
		body = bytes.NewReader([]byte("{}"))
	}

	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")

	client := s.client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("read response: %w", readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return respBody, fmt.Errorf("remote trigger %q returned HTTP %d: %s",
			name, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return respBody, nil
}

// ---------------------------------------------------------------------------
// remote_trigger tool
// ---------------------------------------------------------------------------

// NewRemoteTriggerTool builds the remote_trigger tool bound to the supplied
// store. The tool looks up a registered endpoint by name and POSTs a JSON
// payload, returning the response body as text to the caller.
func NewRemoteTriggerTool(store *RemoteTriggerStore) types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn: func() string { return "remote_trigger" },
		DescriptionFn: func() string {
			return "Invoke a pre-registered remote HTTP endpoint by name, " +
				"POSTing an optional JSON payload and returning the response body."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Registered trigger name to invoke",
					},
					"payload": map[string]any{
						"type":        "object",
						"description": "Optional JSON payload to POST (any object)",
					},
				},
				"required": []string{"name"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return false },
		IsDestructiveFn: func(map[string]any) bool { return false },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			name, _ := args["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				return types.ValidationResult{Valid: false, Message: "name is required"}
			}
			if store == nil {
				return types.ValidationResult{
					Valid:   false,
					Message: "remote trigger store is not configured",
				}
			}
			if _, ok := store.Lookup(name); !ok {
				available := store.Names()
				msg := fmt.Sprintf("remote trigger %q is not registered", name)
				if len(available) > 0 {
					msg += " (available: " + strings.Join(available, ", ") + ")"
				}
				return types.ValidationResult{Valid: false, Message: msg}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			name, _ := args["name"].(string)
			name = strings.TrimSpace(name)
			payload := args["payload"]

			ctx := tc.Context
			if ctx == nil {
				ctx = context.Background()
			}

			body, err := store.Trigger(ctx, name, payload)
			if err != nil {
				content := err.Error()
				if len(body) > 0 {
					content += "\n" + string(body)
				}
				return types.ToolResult{
					Content: content,
					IsError: true,
				}, nil
			}

			text := string(body)
			if text == "" {
				text = fmt.Sprintf("(empty response from %q)", name)
			}
			return types.ToolResult{Content: text}, nil
		},
	})
}

// RegisterRemoteTriggerTool registers the remote_trigger tool on reg using
// the provided store. If store is nil, a fresh empty store is created so
// that the tool remains callable (though no triggers will be registered).
func RegisterRemoteTriggerTool(reg *Registry, store *RemoteTriggerStore) {
	if store == nil {
		store = NewRemoteTriggerStore()
	}
	reg.Register(NewRemoteTriggerTool(store))
}
