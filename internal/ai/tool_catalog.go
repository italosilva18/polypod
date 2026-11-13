package ai

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/costa/polypod/internal/conversation"
	providerapi "github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/tool"
	openai "github.com/sashabaranov/go-openai"
)

const (
	inlineToolLimit = 28
	inlineToolBytes = 24 * 1024
	toolSearchName  = "tool_search"
	selectedLimit   = 18
)

// Core coding, search, verification, planning and dialogue tools stay inline on
// every progressive turn. Bulky GUI/browser automation schemas activate
// automatically when the user prompt indicates web/GUI intent or via tool_search.
var inlineToolNames = map[string]bool{
	toolSearchName: true,
	"bash":         true, "bash_output": true, "tool_output": true,
	"read_file": true, "read_file_dir": true,
	"write_file": true, "edit_file": true, "file_write": true, "file_edit": true, "multi_edit": true,
	"glob": true, "grep": true, "glob_search": true, "grep_search": true,
	"code_context_search": true, "code_semantic_search": true, "code_impact": true,
	"run_project_checks": true, "lsp": true,
	"architect_plan": true, "parallel_best_of_n": true,
	"web_search": true, "web_fetch": true,
	"ask_user": true, "ask_user_question": true, "ask_user_structured": true, "ask_parent_question": true,
	"send_user_message": true,
	"task_create":       true, "task_get": true, "task_list": true, "task_output": true,
	"agent_run": true, "memory_add": true, "memory_search": true,
	"history_search": true, "history_recall": true,
	"send_message": true, "send_message_enqueue": true,
}

var guiBrowserToolNames = []string{
	"browser_manage", "browser_observe", "browser_act",
	"computer_screenshot", "computer_click", "computer_type",
	"computer_scroll", "computer_key_press",
}

var gitWorktreeToolNames = []string{
	"git_status", "git_diff", "git_log", "git_commit",
	"worktree_create", "worktree_list", "worktree_remove",
}

type requestQueryHintKey struct{}

func withRequestQueryHint(ctx context.Context, query string) context.Context {
	if strings.TrimSpace(query) == "" {
		return ctx
	}
	return context.WithValue(ctx, requestQueryHintKey{}, query)
}

func requestQueryHintFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	q, _ := ctx.Value(requestQueryHintKey{}).(string)
	return q
}

func intentActivatedTools(query string) []string {
	if strings.TrimSpace(query) == "" {
		return nil
	}
	lower := strings.ToLower(query)
	var extra []string
	for _, kw := range []string{
		"browser", "navegador", "playwright", "chrome", "pagina", "página",
		"screenshot", "print da tela", "clique", "click", "gui", "desktop", "http://", "https://",
	} {
		if strings.Contains(lower, kw) {
			extra = append(extra, guiBrowserToolNames...)
			break
		}
	}
	for _, kw := range []string{"worktree", "commit", "branch", "git ", "pull request", "merge"} {
		if strings.Contains(lower, kw) {
			extra = append(extra, gitWorktreeToolNames...)
			break
		}
	}
	return extra
}

type requestToolSessionKey struct{}

type toolSessionID struct {
	channel string
	user    string
	agent   string
	branch  string
}

func withRequestToolSession(ctx context.Context, channel, user, agent, branch string) context.Context {
	if channel == "" || user == "" {
		return ctx
	}
	ctx = conversation.WithJournalScope(ctx, channel, user, branch, agent)
	return context.WithValue(ctx, requestToolSessionKey{}, toolSessionID{
		channel: channel, user: user, agent: agent, branch: branch,
	})
}

type toolSelection struct {
	names []string
	used  time.Time
}

// toolCatalog is request-local. It expands the provider's standard function
// catalog after a successful tool_search call; no provider-specific deferred
// references are required.
type toolCatalog struct {
	all         []openai.Tool
	byName      map[string]openai.Tool
	active      map[string]bool
	selected    []string
	progressive bool
	onActivate  func(string)
}

func (c *Client) catalogFor(ctx context.Context) *toolCatalog {
	var all []openai.Tool
	if c.skills != nil {
		all = append(all, c.skills.ToolDefinitions(c.skillNamesFor(ctx))...)
	}
	sessionTools := tool.SessionToolsFromContext(ctx)
	sessionToolNames := make(map[string]bool, len(sessionTools))
	for _, st := range sessionTools {
		if st == nil || !st.IsEnabled() {
			continue
		}
		params := st.InputSchema()
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		def := openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        st.Name(),
				Description: st.Description(),
				Parameters:  params,
			},
		}
		sessionToolNames[st.Name()] = true
		replaced := false
		for i := range all {
			if all[i].Function != nil && all[i].Function.Name == st.Name() {
				all[i] = def
				replaced = true
				break
			}
		}
		if !replaced {
			all = append(all, def)
		}
	}
	if len(all) == 0 {
		return &toolCatalog{}
	}
	catalog := &toolCatalog{all: all, byName: make(map[string]openai.Tool, len(all)), active: make(map[string]bool)}
	for _, item := range all {
		if item.Function != nil {
			catalog.byName[item.Function.Name] = item
		}
	}
	if os.Getenv("POLYPOD_TOOL_CATALOG") == "full" || !catalog.has(toolSearchName) || !catalogExceedsBudget(all) {
		return catalog
	}
	catalog.progressive = true
	for name := range inlineToolNames {
		if catalog.has(name) {
			catalog.active[name] = true
		}
	}
	for name := range sessionToolNames {
		catalog.active[name] = true
	}
	for _, name := range intentActivatedTools(requestQueryHintFromContext(ctx)) {
		if catalog.has(name) {
			catalog.active[name] = true
		}
	}
	// Keep selected tools available across turns of the same conversation branch.
	if session, ok := ctx.Value(requestToolSessionKey{}).(toolSessionID); ok {
		for _, name := range c.previouslySelectedTools(session) {
			if catalog.has(name) {
				catalog.activate(name)
			}
		}
		catalog.onActivate = func(name string) { c.rememberSelectedTool(session, name) }
	}
	return catalog
}

func catalogExceedsBudget(items []openai.Tool) bool {
	if len(items) > inlineToolLimit {
		return true
	}
	bytes := 0
	for _, item := range items {
		if item.Function == nil {
			continue
		}
		bytes += len(item.Function.Name) + len(item.Function.Description)
		if schema, err := json.Marshal(item.Function.Parameters); err == nil {
			bytes += len(schema)
		}
		if bytes > inlineToolBytes {
			return true
		}
	}
	return false
}

func (c *toolCatalog) has(name string) bool {
	_, ok := c.byName[name]
	return ok
}

func (c *toolCatalog) definitions() []openai.Tool {
	if !c.progressive {
		return c.all
	}
	result := make([]openai.Tool, 0, len(c.active))
	for _, item := range c.all {
		if item.Function != nil && c.active[item.Function.Name] {
			result = append(result, item)
		}
	}
	return result
}

func (c *toolCatalog) providerDefinitions() []providerapi.Tool {
	definitions := c.definitions()
	result := make([]providerapi.Tool, 0, len(definitions))
	for _, item := range definitions {
		if item.Function != nil {
			result = append(result, providerapi.Tool{
				Name: item.Function.Name, Description: item.Function.Description,
				Parameters: responseToolParameters(item.Function.Parameters),
			})
		}
	}
	return result
}

func (c *toolCatalog) observe(results []ToolCallResult) {
	if !c.progressive {
		return
	}
	for _, result := range results {
		if result.Name != toolSearchName || result.IsError {
			continue
		}
		var payload struct {
			Matches []struct {
				Name string `json:"name"`
			} `json:"matches"`
		}
		if json.Unmarshal([]byte(result.Content), &payload) != nil {
			continue
		}
		for _, match := range payload.Matches {
			name := strings.TrimSpace(match.Name)
			if !c.has(name) {
				continue
			}
			if c.activate(name) && c.onActivate != nil {
				c.onActivate(name)
			}
		}
	}
}

func (c *toolCatalog) activate(name string) bool {
	if c.active[name] {
		if inlineToolNames[name] {
			return false
		}
		for index, existing := range c.selected {
			if existing == name {
				c.selected = append(c.selected[:index], c.selected[index+1:]...)
				break
			}
		}
	}
	c.active[name] = true
	if inlineToolNames[name] {
		return true
	}
	c.selected = append(c.selected, name)
	if len(c.selected) > selectedLimit {
		delete(c.active, c.selected[0])
		c.selected = c.selected[1:]
	}
	return true
}

func (c *Client) previouslySelectedTools(session toolSessionID) []string {
	c.toolSelectionMu.Lock()
	defer c.toolSelectionMu.Unlock()
	state, ok := c.toolSelections[session]
	if !ok || time.Since(state.used) > 24*time.Hour {
		delete(c.toolSelections, session)
		return nil
	}
	state.used = time.Now()
	return append([]string(nil), state.names...)
}

func (c *Client) rememberSelectedTool(session toolSessionID, name string) {
	c.toolSelectionMu.Lock()
	defer c.toolSelectionMu.Unlock()
	if c.toolSelections == nil {
		c.toolSelections = make(map[toolSessionID]*toolSelection)
	}
	if len(c.toolSelections) >= 256 && c.toolSelections[session] == nil {
		var oldest toolSessionID
		found := false
		for key, entry := range c.toolSelections {
			if !found || entry.used.Before(c.toolSelections[oldest].used) {
				oldest = key
				found = true
			}
		}
		delete(c.toolSelections, oldest)
	}
	state := c.toolSelections[session]
	if state == nil {
		state = &toolSelection{}
		c.toolSelections[session] = state
	}
	for index, existing := range state.names {
		if existing == name {
			state.names = append(state.names[:index], state.names[index+1:]...)
			break
		}
	}
	state.names = append(state.names, name)
	if len(state.names) > selectedLimit {
		state.names = state.names[len(state.names)-selectedLimit:]
	}
	state.used = time.Now()
}
