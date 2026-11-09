package bash

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// PTYStatus describes a live or archived terminal session.
type PTYStatus struct {
	ID          string
	State       string
	ExitCode    int
	OutputBytes int64
	Error       string
}

type ptySession struct {
	mu         sync.Mutex
	writeMu    sync.Mutex
	id         string
	cmd        *exec.Cmd
	terminal   *os.File
	outputPath string
	done       chan struct{}
	readDone   chan struct{}
	state      string
	exitCode   int
	endedAt    time.Time
	outputErr  error
}

// PTYManager keeps terminal sessions alive across tool calls.
type PTYManager struct {
	mu       sync.RWMutex
	sessions map[string]*ptySession
	bashTool *BashTool
	logger   *slog.Logger
}

func NewPTYManager(bt *BashTool, logger *slog.Logger) *PTYManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &PTYManager{
		sessions: make(map[string]*ptySession),
		bashTool: bt,
		logger:   logger,
	}
}

func ptyOutputDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cacheDir, "polypod", "pty-sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("invalid PTY directory %s", dir)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func (m *PTYManager) pruneOutputs(dir string) {
	m.mu.Lock()
	active := make(map[string]bool, len(m.sessions))
	for id, session := range m.sessions {
		session.mu.Lock()
		if session.state == "running" {
			active[id] = true
		} else if !session.endedAt.IsZero() && time.Since(session.endedAt) > outputRetention {
			delete(m.sessions, id)
		}
		session.mu.Unlock()
	}
	m.mu.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		if active[strings.TrimSuffix(entry.Name(), ".log")] {
			continue
		}
		info, err := entry.Info()
		if err == nil && time.Since(info.ModTime()) > outputRetention {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

// Start launches a command with its own pseudo-terminal and returns its ID.
func (m *PTYManager) Start(command, workingDir string, env map[string]string, rows, cols uint16) (string, error) {
	if m == nil || m.bashTool == nil {
		return "", errors.New("PTY manager unavailable")
	}
	if strings.TrimSpace(command) == "" {
		return "", errors.New("comando vazio")
	}
	if err := validateWorkingDir(workingDir); err != nil {
		return "", fmt.Errorf("diretorio de trabalho invalido: %w", err)
	}
	if rows == 0 {
		rows = 24
	}
	if cols == 0 {
		cols = 100
	}
	dir, err := ptyOutputDir()
	if err != nil {
		return "", err
	}
	m.pruneOutputs(dir)
	id, err := newOutputID()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".log")
	output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	cmd := exec.Command(m.bashTool.shell, "-c", command)
	cmd.Dir = workingDir
	cmd.Env = m.bashTool.buildEnv(env)
	if !hasEnvKey(cmd.Env, "TERM") {
		cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	}
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		output.Close()
		os.Remove(path)
		return "", err
	}
	session := &ptySession{
		id: id, cmd: cmd, terminal: terminal,
		outputPath: path, done: make(chan struct{}), readDone: make(chan struct{}), state: "running",
	}
	m.mu.Lock()
	m.sessions[id] = session
	m.mu.Unlock()
	go session.captureOutput(output, m.logger)
	go session.waitForExit(m.logger)
	m.logger.Info("PTY session started", "id", id, "pid", cmd.Process.Pid)
	return id, nil
}

func hasEnvKey(env []string, key string) bool {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
}

func (s *ptySession) captureOutput(output *os.File, logger *slog.Logger) {
	defer close(s.readDone)
	defer output.Close()
	buf := make([]byte, 32*1024)
	for {
		n, readErr := s.terminal.Read(buf)
		if n > 0 {
			written, writeErr := output.Write(buf[:n])
			if writeErr == nil && written != n {
				writeErr = io.ErrShortWrite
			}
			if writeErr != nil {
				s.mu.Lock()
				if s.outputErr == nil {
					s.outputErr = writeErr
					logger.Warn("PTY output could not be stored", "id", s.id, "error", writeErr)
				}
				s.mu.Unlock()
			}
		}
		if readErr != nil {
			return
		}
	}
}

func (s *ptySession) waitForExit(logger *slog.Logger) {
	err := s.cmd.Wait()
	select {
	case <-s.readDone:
	case <-time.After(500 * time.Millisecond):
		s.terminal.Close()
		<-s.readDone
	}
	s.terminal.Close()
	s.mu.Lock()
	s.state = "exited"
	s.endedAt = time.Now()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			s.exitCode = exit.ExitCode()
		} else {
			s.exitCode = -1
			s.outputErr = err
		}
	}
	s.mu.Unlock()
	close(s.done)
	logger.Info("PTY session exited", "id", s.id, "exit_code", s.exitCode)
}

func (m *PTYManager) get(id string) (*ptySession, error) {
	if !validOutputID(id) {
		return nil, errors.New("ID de sessao invalido")
	}
	m.mu.RLock()
	session := m.sessions[id]
	m.mu.RUnlock()
	if session == nil {
		return nil, errors.New("sessao PTY nao encontrada")
	}
	return session, nil
}

// Write sends raw input to a live terminal. Callers may append a newline.
func (m *PTYManager) Write(ctx context.Context, id, input string) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	session, err := m.get(id)
	if err != nil {
		return 0, err
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-session.done:
		return 0, errors.New("sessao PTY encerrada")
	default:
	}
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	total := 0
	for total < len(input) {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		select {
		case <-session.done:
			return total, errors.New("sessao PTY encerrada")
		default:
		}
		n, err := io.WriteString(session.terminal, input[total:])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

// Resize changes the terminal dimensions for full-screen applications.
func (m *PTYManager) Resize(id string, rows, cols uint16) error {
	session, err := m.get(id)
	if err != nil {
		return err
	}
	if rows == 0 || cols == 0 {
		return errors.New("rows e cols devem ser maiores que zero")
	}
	return pty.Setsize(session.terminal, &pty.Winsize{Rows: rows, Cols: cols})
}

// Read returns one page of terminal output and the current process status.
func (m *PTYManager) Read(id string, offset, limit int64) ([]byte, PTYStatus, error) {
	if !validOutputID(id) {
		return nil, PTYStatus{}, errors.New("ID de sessao invalido")
	}
	if offset < 0 {
		return nil, PTYStatus{}, errors.New("offset deve ser maior ou igual a zero")
	}
	if limit <= 0 {
		limit = 8192
	}
	if limit > MaxOutputReadBytes {
		limit = MaxOutputReadBytes
	}
	dir, err := ptyOutputDir()
	if err != nil {
		return nil, PTYStatus{}, err
	}
	path := filepath.Join(dir, id+".log")
	entry, err := os.Lstat(path)
	if err != nil {
		return nil, PTYStatus{}, err
	}
	if !entry.Mode().IsRegular() {
		return nil, PTYStatus{}, errors.New("arquivo de sessao PTY invalido")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, PTYStatus{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, PTYStatus{}, err
	}
	status := PTYStatus{ID: id, State: "archived", OutputBytes: info.Size()}
	m.mu.RLock()
	session := m.sessions[id]
	m.mu.RUnlock()
	if session != nil {
		session.mu.Lock()
		status.State = session.state
		status.ExitCode = session.exitCode
		if session.outputErr != nil {
			status.Error = session.outputErr.Error()
		}
		session.mu.Unlock()
	}
	if offset >= info.Size() {
		return []byte{}, status, nil
	}
	buf := make([]byte, min(limit, info.Size()-offset))
	n, err := file.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, PTYStatus{}, err
	}
	return buf[:n], status, nil
}

// Close sends SIGTERM, then SIGKILL if the process does not exit promptly.
func (m *PTYManager) Close(id string) error {
	session, err := m.get(id)
	if err != nil {
		return err
	}
	select {
	case <-session.done:
		return nil
	default:
	}
	if err := syscall.Kill(-session.cmd.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	select {
	case <-session.done:
		return nil
	case <-time.After(2 * time.Second):
	}
	if err := syscall.Kill(-session.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	select {
	case <-session.done:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("tempo esgotado ao encerrar sessao PTY")
	}
}

// CloseAll stops active terminals when the owning runtime shuts down.
func (m *PTYManager) CloseAll() {
	m.mu.RLock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	for _, id := range ids {
		if err := m.Close(id); err != nil {
			m.logger.Warn("PTY session close failed", "id", id, "error", err)
		}
	}
}
