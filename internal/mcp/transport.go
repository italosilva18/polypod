package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Transport abstracts the communication layer with an MCP server.
type Transport interface {
	Send(req JSONRPCRequest) error
	SendNotification(notif JSONRPCNotification) error
	Receive() (JSONRPCResponse, error)
	Close() error
}

// StdioTransport launches a subprocess and speaks JSON-RPC over stdin/stdout.
type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	mu     sync.Mutex
}

// NewStdioTransport starts a subprocess for MCP communication.
func NewStdioTransport(command string, args []string, env []string) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)
	cmd.Stderr = os.Stderr
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("mcp stdio stdout: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, fmt.Errorf("mcp stdio start %q: %w", command, err)
	}

	return &StdioTransport{
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
	}, nil
}

func (t *StdioTransport) Send(req JSONRPCRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = t.stdin.Write(data)
	return err
}

func (t *StdioTransport) SendNotification(notif JSONRPCNotification) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	data, err := json.Marshal(notif)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = t.stdin.Write(data)
	return err
}

func (t *StdioTransport) Receive() (JSONRPCResponse, error) {
	line, err := t.reader.ReadBytes('\n')
	if err != nil {
		return JSONRPCResponse{}, fmt.Errorf("mcp stdio read: %w", err)
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return t.Receive() // skip blank lines
	}
	var resp JSONRPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return JSONRPCResponse{}, fmt.Errorf("mcp stdio decode: %w", err)
	}
	return resp, nil
}

func (t *StdioTransport) Close() error {
	t.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- t.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.cmd.Process.Kill()
		return fmt.Errorf("mcp stdio: processo encerrado por timeout")
	}
}

// SSETransport connects to an MCP server via HTTP + Server-Sent Events.
type SSETransport struct {
	baseURL    string
	httpClient *http.Client
	sessionURL string
	mu         sync.Mutex
}

// NewSSETransport creates an SSE transport for a remote MCP server.
func NewSSETransport(url string) *SSETransport {
	return &SSETransport{
		baseURL:    url,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

func (t *SSETransport) Send(req JSONRPCRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	target := t.baseURL
	if t.sessionURL != "" {
		target = t.sessionURL
	}

	data, err := json.Marshal(req)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequest("POST", target, bytes.NewReader(data))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("mcp sse send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mcp sse HTTP %d: %s", resp.StatusCode, string(b))
	}

	// Check for session URL in response
	if loc := resp.Header.Get("Location"); loc != "" {
		t.sessionURL = loc
	}

	return nil
}

func (t *SSETransport) SendNotification(notif JSONRPCNotification) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	target := t.baseURL
	if t.sessionURL != "" {
		target = t.sessionURL
	}

	data, err := json.Marshal(notif)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequest("POST", target, bytes.NewReader(data))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("mcp sse notify: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (t *SSETransport) Receive() (JSONRPCResponse, error) {
	// For SSE, the response comes inline from the POST in most implementations.
	// This is a simplified version — full SSE would require a persistent GET connection.
	return JSONRPCResponse{}, fmt.Errorf("mcp sse receive: use Send para request/response sincrono")
}

func (t *SSETransport) Close() error {
	return nil
}
