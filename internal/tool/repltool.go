package tool

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// REPLSession holds a persistent REPL process.
type REPLSession struct {
	ID       string
	Language string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Reader
	stderr   *bufio.Reader
	mu       sync.Mutex
	lastUsed time.Time
	closed   bool
}

// REPLManager manages REPL sessions with idle eviction.
type REPLManager struct {
	mu       sync.Mutex
	sessions map[string]*REPLSession
	ttl      time.Duration
	maxCount int
}

// NewREPLManager creates a new REPL session manager.
func NewREPLManager() *REPLManager {
	m := &REPLManager{
		sessions: make(map[string]*REPLSession),
		ttl:      10 * time.Minute,
		maxCount: 10,
	}
	go m.gcLoop()
	return m
}

var globalREPLManager = NewREPLManager()

// GetREPLManager returns the singleton manager.
func GetREPLManager() *REPLManager { return globalREPLManager }

// gcLoop runs periodically to evict idle sessions.
func (m *REPLManager) gcLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		m.gc()
	}
}

func (m *REPLManager) gc() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, sess := range m.sessions {
		if now.Sub(sess.lastUsed) > m.ttl {
			sess.Close()
			delete(m.sessions, id)
		}
	}
}

// GetOrCreate returns an existing session or creates a new one.
func (m *REPLManager) GetOrCreate(id, language string) (*REPLSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id == "" {
		id = fmt.Sprintf("repl-%d", time.Now().UnixNano())
	}

	if sess, ok := m.sessions[id]; ok && !sess.closed {
		return sess, nil
	}

	// Evict oldest if over limit
	if len(m.sessions) >= m.maxCount {
		var oldestID string
		var oldestTime time.Time
		for sid, s := range m.sessions {
			if oldestID == "" || s.lastUsed.Before(oldestTime) {
				oldestID = sid
				oldestTime = s.lastUsed
			}
		}
		if oldestID != "" {
			m.sessions[oldestID].Close()
			delete(m.sessions, oldestID)
		}
	}

	sess, err := newREPLSession(id, language)
	if err != nil {
		return nil, err
	}
	m.sessions[id] = sess
	return sess, nil
}

// Close closes a specific session.
func (m *REPLManager) Close(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[id]
	if !ok {
		return false
	}
	sess.Close()
	delete(m.sessions, id)
	return true
}

// Shutdown closes all sessions.
func (m *REPLManager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sess := range m.sessions {
		sess.Close()
	}
	m.sessions = make(map[string]*REPLSession)
}

// newREPLSession spawns a new REPL subprocess.
func newREPLSession(id, language string) (*REPLSession, error) {
	var cmd *exec.Cmd
	switch language {
	case "python", "python3", "py":
		cmd = exec.Command("python3", "-u", "-i")
	case "node", "nodejs", "js":
		cmd = exec.Command("node", "-i")
	case "ruby", "rb":
		cmd = exec.Command("irb", "--noprompt")
	default:
		return nil, fmt.Errorf("unsupported language: %s", language)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	return &REPLSession{
		ID:       id,
		Language: language,
		cmd:      cmd,
		stdin:    stdin,
		stdout:   bufio.NewReader(stdout),
		stderr:   bufio.NewReader(stderr),
		lastUsed: time.Now(),
	}, nil
}

// Execute sends code to the REPL and reads output with timeout.
func (s *REPLSession) Execute(code string, timeout time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return "", errors.New("session closed")
	}

	s.lastUsed = time.Now()

	// Write code + sentinel marker
	marker := fmt.Sprintf("__POLYPOD_REPL_DONE_%d__", time.Now().UnixNano())
	var fullCode string
	switch s.Language {
	case "python", "python3", "py":
		fullCode = code + "\nprint('" + marker + "')\n"
	case "node", "nodejs", "js":
		fullCode = code + "\nconsole.log('" + marker + "');\n"
	case "ruby", "rb":
		fullCode = code + "\nputs '" + marker + "'\n"
	default:
		fullCode = code + "\n"
	}

	if _, err := s.stdin.Write([]byte(fullCode)); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}

	// Read output until sentinel or timeout
	done := make(chan struct{})
	var outBuf []byte
	var readErr error

	go func() {
		defer close(done)
		for {
			line, err := s.stdout.ReadString('\n')
			if len(line) > 0 {
				if contains(line, marker) {
					return
				}
				outBuf = append(outBuf, []byte(line)...)
			}
			if err != nil {
				readErr = err
				return
			}
			if len(outBuf) > 512*1024 {
				outBuf = append(outBuf, []byte("\n... [truncated]")...)
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		return string(outBuf), errors.New("timeout")
	}

	if readErr != nil && readErr != io.EOF {
		return string(outBuf), readErr
	}
	return string(outBuf), nil
}

// Close terminates the REPL process.
func (s *REPLSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.stdin != nil {
		s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
}

func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// NewREPLTool creates the repl tool definition.
func NewREPLTool() types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "repl" },
		DescriptionFn: func() string {
			return "Execute code in a persistent REPL session (python, node, ruby). State persists across calls within the same session_id."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"language": map[string]any{
						"type":        "string",
						"enum":        []string{"python", "python3", "py", "node", "nodejs", "js", "ruby", "rb"},
						"description": "Language/REPL to use",
					},
					"code": map[string]any{
						"type":        "string",
						"description": "Code to execute",
					},
					"session_id": map[string]any{
						"type":        "string",
						"description": "Session ID to continue (optional; creates new session if omitted)",
					},
					"timeout_ms": map[string]any{
						"type":        "integer",
						"description": "Timeout in milliseconds (default 30000)",
					},
					"close": map[string]any{
						"type":        "boolean",
						"description": "If true, close the session after execution",
					},
				},
				"required": []string{"language", "code"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return false },
		IsDestructiveFn: func(map[string]any) bool { return false },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			language, _ := args["language"].(string)
			code, _ := args["code"].(string)
			sessionID, _ := args["session_id"].(string)

			if language == "" || code == "" {
				return types.ToolResult{IsError: true, Content: "language and code are required"}, nil
			}

			timeout := 30 * time.Second
			if v, ok := args["timeout_ms"].(float64); ok {
				timeout = time.Duration(int(v)) * time.Millisecond
			}
			if v, ok := args["timeout_ms"].(int); ok {
				timeout = time.Duration(v) * time.Millisecond
			}

			mgr := GetREPLManager()
			sess, err := mgr.GetOrCreate(sessionID, language)
			if err != nil {
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("session error: %v", err)}, nil
			}

			output, err := sess.Execute(code, timeout)

			if closeAfter, _ := args["close"].(bool); closeAfter {
				mgr.Close(sess.ID)
			}

			result := fmt.Sprintf("[session=%s lang=%s]\n%s", sess.ID, sess.Language, output)
			if err != nil {
				return types.ToolResult{IsError: true, Content: result + "\nerror: " + err.Error()}, nil
			}
			return types.ToolResult{Content: result}, nil
		},
	}
}

// RegisterREPLTool registers the tool.
func RegisterREPLTool(reg *Registry) {
	reg.RegisterDef(NewREPLTool())
}
