// Package acp exposes the existing Poly stream handler through ACP v1 stdio.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/session"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/workdir"
)

type Server struct {
	Stream          adapter.StreamHandler
	Manager         *conversation.Manager
	Store           *conversation.Store
	Workdirs        *workdir.Store
	Version         string
	EventDir        string
	Journal         *session.EventJournal
	fallbackJournal *session.EventJournal
	fallbackDir     string
	mu              sync.Mutex
	eventMu         sync.Mutex
	writeMu         sync.Mutex
	initialized     bool
	active          map[string]context.CancelFunc
	requests        map[string]string
	sessionMCP      map[string]*acpSessionMCP
	output          *json.Encoder
}

func (s *Server) journal() *session.EventJournal {
	if s.Journal != nil {
		return s.Journal
	}
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	if s.EventDir == "" {
		return nil
	}
	if s.fallbackJournal == nil || s.fallbackDir != s.EventDir {
		s.fallbackJournal = session.NewEventJournal(s.EventDir)
		s.fallbackDir = s.EventDir
	}
	return s.fallbackJournal
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func New(stream adapter.StreamHandler, manager *conversation.Manager, store *conversation.Store, workdirs *workdir.Store, version string) *Server {
	return &Server{Stream: stream, Manager: manager, Store: store, Workdirs: workdirs, Version: version,
		active: make(map[string]context.CancelFunc), requests: make(map[string]string), sessionMCP: make(map[string]*acpSessionMCP)}
}

func (s *Server) send(value any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.output.Encode(value)
}

func (s *Server) response(id json.RawMessage, result any) error {
	return s.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *Server) fail(id json.RawMessage, code int, message string) error {
	return s.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcError{Code: code, Message: message}})
}

func (s *Server) update(sessionID string, body map[string]any) error {
	if err := s.appendEvent(sessionID, body); err != nil {
		return err
	}
	return s.sendUpdate(sessionID, body)
}

func (s *Server) sendUpdate(sessionID string, body map[string]any) error {
	return s.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
		"sessionId": sessionID, "update": body,
	}})
}

// Serve reads one JSON-RPC message per line. Only ACP messages go to stdout;
// callers route diagnostics to stderr before invoking this method.
func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	if s.Stream == nil || s.Manager == nil || s.Store == nil || s.Workdirs == nil {
		return errors.New("ACP sem dependências de sessão")
	}
	s.output = json.NewEncoder(output)
	var cancelReadErr <-chan error
	if closer, ok := input.(io.ReadCloser); ok {
		stopped := make(chan struct{})
		closeErr := make(chan error, 1)
		cancelReadErr = closeErr
		defer close(stopped)
		go func() {
			select {
			case <-ctx.Done():
				closeErr <- closer.Close()
			case <-stopped:
			}
		}()
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	var pending sync.WaitGroup
	turnErrors := make(chan error, 1)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			if sendErr := s.fail(nil, -32700, "JSON inválido"); sendErr != nil {
				return sendErr
			}
			continue
		}
		if req.JSONRPC != "2.0" || req.Method == "" {
			if sendErr := s.fail(req.ID, -32600, "requisição inválida"); sendErr != nil {
				return sendErr
			}
			continue
		}
		if req.ID == nil && req.Method != "session/cancel" && req.Method != "$/cancel_request" {
			continue
		}
		if req.Method == "initialize" {
			var p struct {
				ProtocolVersion int `json:"protocolVersion"`
			}
			if json.Unmarshal(req.Params, &p) != nil || p.ProtocolVersion < 1 {
				if err := s.fail(req.ID, -32602, "versão de protocolo inválida"); err != nil {
					return err
				}
				continue
			}
			s.initialized = true
			result := map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{
				"loadSession":         true,
				"mcpCapabilities":     map[string]any{"http": true, "sse": true},
				"sessionCapabilities": map[string]any{"list": map[string]any{}, "resume": map[string]any{}},
				"_meta": map[string]any{
					"github.com/costa/polypod/sessionEvents": true,
					"github.com/costa/polypod/turnId":        true,
				},
			}, "agentInfo": map[string]string{"name": "polypod", "title": "Poly", "version": s.Version}, "authMethods": []any{}}
			if err := s.response(req.ID, result); err != nil {
				return err
			}
			continue
		}
		if !s.initialized {
			if req.ID != nil {
				if err := s.fail(req.ID, -32000, "inicialize a conexão primeiro"); err != nil {
					return err
				}
			}
			continue
		}
		switch req.Method {
		case "session/new":
			id, err := s.newSession(ctx, req.Params)
			if err != nil {
				if err := s.fail(req.ID, -32602, err.Error()); err != nil {
					return err
				}
				continue
			}
			if err := s.response(req.ID, map[string]any{"sessionId": id}); err != nil {
				return err
			}
		case "session/load", "session/resume":
			id, replay, err := s.existingSession(ctx, req.Params, req.Method == "session/load")
			if err != nil {
				if err := s.fail(req.ID, -32602, err.Error()); err != nil {
					return err
				}
				continue
			}
			if replay != nil {
				events, err := s.readEvents(id, 0, 1000)
				if err != nil {
					return err
				}
				if len(events) > 0 {
					for len(events) > 0 {
						for _, event := range events {
							if err := s.sendUpdate(id, event.Update); err != nil {
								return err
							}
						}
						events, err = s.readEvents(id, events[len(events)-1].Sequence, 1000)
						if err != nil {
							return err
						}
					}
				} else {
					for _, msg := range replay.Messages {
						kind := "agent_message_chunk"
						if msg.Role == conversation.RoleUser {
							kind = "user_message_chunk"
						}
						if err := s.sendUpdate(id, map[string]any{"sessionUpdate": kind, "messageId": msg.ID,
							"content": map[string]string{"type": "text", "text": msg.Content}}); err != nil {
							return err
						}
					}
				}
			}
			if err := s.response(req.ID, map[string]any{}); err != nil {
				return err
			}
		case "session/list":
			result, err := s.listSessions(ctx, req.Params)
			if err != nil {
				if err := s.fail(req.ID, -32602, err.Error()); err != nil {
					return err
				}
				continue
			}
			if err := s.response(req.ID, result); err != nil {
				return err
			}
		case "_polypod/session_events":
			var p struct {
				SessionID string `json:"sessionId"`
				After     uint64 `json:"after"`
				Limit     int    `json:"limit"`
			}
			if json.Unmarshal(req.Params, &p) != nil || !s.exists(p.SessionID) {
				if err := s.fail(req.ID, -32602, "sessão ou cursor inválido"); err != nil {
					return err
				}
				continue
			}
			events, err := s.readEvents(p.SessionID, p.After, p.Limit)
			if err != nil {
				if err := s.fail(req.ID, -32000, err.Error()); err != nil {
					return err
				}
				continue
			}
			if err := s.response(req.ID, map[string]any{"events": events}); err != nil {
				return err
			}
		case "session/prompt":
			id, promptText, turnKey, err := s.promptParams(req.Params)
			if err != nil {
				if err := s.fail(req.ID, -32602, err.Error()); err != nil {
					return err
				}
				continue
			}
			if req.ID == nil {
				continue
			}
			s.mu.Lock()
			if _, busy := s.active[id]; busy {
				s.mu.Unlock()
				if err := s.fail(req.ID, -32000, "sessão já está respondendo"); err != nil {
					return err
				}
				continue
			}
			turnCtx, cancel := context.WithCancel(ctx)
			s.active[id] = cancel
			s.requests[string(req.ID)] = id
			s.mu.Unlock()
			pending.Add(1)
			go func(id, text, key string, requestID json.RawMessage) {
				defer pending.Done()
				if err := s.runPrompt(turnCtx, id, text, key, requestID); err != nil {
					select {
					case turnErrors <- err:
					default:
					}
				}
				s.mu.Lock()
				delete(s.active, id)
				delete(s.requests, string(requestID))
				s.mu.Unlock()
				cancel()
			}(id, promptText, turnKey, append(json.RawMessage(nil), req.ID...))
		case "session/cancel":
			var p struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(req.Params, &p) == nil {
				s.cancelSession(p.SessionID)
			}
			if req.ID != nil {
				if err := s.response(req.ID, map[string]any{}); err != nil {
					return err
				}
			}
		case "$/cancel_request":
			var p struct {
				ID json.RawMessage `json:"id"`
			}
			if json.Unmarshal(req.Params, &p) == nil {
				s.mu.Lock()
				id := s.requests[string(p.ID)]
				s.mu.Unlock()
				s.cancelSession(id)
			}
		default:
			if req.ID != nil {
				if err := s.fail(req.ID, -32601, "método desconhecido"); err != nil {
					return err
				}
			}
		}
	}
	pending.Wait()
	s.closeAllSessionMCP()
	if err := ctx.Err(); err != nil {
		if cancelReadErr != nil {
			select {
			case closeErr := <-cancelReadErr:
				if closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
					return errors.Join(err, closeErr)
				}
			default:
			}
		}
		return err
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	select {
	case err := <-turnErrors:
		return err
	default:
	}
	return nil
}

func (s *Server) cancelSession(id string) {
	s.mu.Lock()
	cancel := s.active[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if j := s.journal(); j != nil {
		j.CancelSession(id)
		j.CancelSession("acp:" + id)
	}
}

type sessionParams struct {
	SessionID  string            `json:"sessionId"`
	CWD        string            `json:"cwd"`
	MCPServers []json.RawMessage `json:"mcpServers"`
}

func decodeSessionParams(raw json.RawMessage) (sessionParams, error) {
	var p sessionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	if !filepath.IsAbs(p.CWD) {
		return p, errors.New("cwd deve ser absoluto")
	}
	info, err := os.Stat(p.CWD)
	if err != nil || !info.IsDir() {
		return p, errors.New("cwd indisponível")
	}
	if _, err := parseACPMCPServers(p.MCPServers, p.CWD); err != nil {
		return p, err
	}
	return p, nil
}

func (s *Server) scoped(id string) context.Context {
	return workdir.WithScope(context.Background(), s.Workdirs, "acp", id, "acp:"+id)
}

func (s *Server) newSession(ctx context.Context, raw json.RawMessage) (string, error) {
	p, err := decodeSessionParams(raw)
	if err != nil {
		return "", err
	}
	id := uuid.NewString()
	if err := workdir.SetBase(s.scoped(id), p.CWD); err != nil {
		return "", err
	}
	if err := s.configureSessionMCP(ctx, id, p.CWD, p.MCPServers); err != nil {
		return "", err
	}
	sess, err := s.Manager.GetSession(ctx, "acp", id)
	if err != nil {
		_ = s.configureSessionMCP(ctx, id, p.CWD, nil)
		return "", err
	}
	if err := s.Store.Save(ctx, sess); err != nil {
		_ = s.configureSessionMCP(ctx, id, p.CWD, nil)
		return "", err
	}
	return id, nil
}

func (s *Server) exists(id string) bool {
	for _, key := range s.Store.ListSessions() {
		if key == "acp:"+id {
			return true
		}
	}
	return false
}

func (s *Server) existingSession(ctx context.Context, raw json.RawMessage, replay bool) (string, *conversation.Session, error) {
	p, err := decodeSessionParams(raw)
	if err != nil {
		return "", nil, err
	}
	if p.SessionID == "" || !s.exists(p.SessionID) {
		return "", nil, errors.New("sessão não encontrada")
	}
	if s.sessionCWD(p.SessionID) != p.CWD {
		return "", nil, errors.New("cwd não corresponde ao da sessão")
	}
	if err := s.configureSessionMCP(ctx, p.SessionID, p.CWD, p.MCPServers); err != nil {
		return "", nil, err
	}
	if !replay {
		return p.SessionID, nil, nil
	}
	sess, err := s.Manager.GetSession(ctx, "acp", p.SessionID)
	return p.SessionID, sess, err
}

func (s *Server) sessionCWD(id string) string {
	entry, ok := workdir.Current(s.scoped(id))
	if !ok || !entry.BasePinned {
		return ""
	}
	return entry.Previous
}

func (s *Server) listSessions(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var p struct {
		CWD    string `json:"cwd"`
		Cursor string `json:"cursor"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
	}
	if p.CWD != "" && !filepath.IsAbs(p.CWD) {
		return nil, errors.New("cwd deve ser absoluto")
	}
	offset := 0
	if p.Cursor != "" {
		n, err := strconv.Atoi(p.Cursor)
		if err != nil || n < 0 {
			return nil, errors.New("cursor inválido")
		}
		offset = n
	}
	keys := s.Store.ListSessions()
	sort.Strings(keys)
	var ids []string
	for _, key := range keys {
		if !strings.HasPrefix(key, "acp:") {
			continue
		}
		id := strings.TrimPrefix(key, "acp:")
		cwd := s.sessionCWD(id)
		if cwd == "" || p.CWD != "" && p.CWD != cwd {
			continue
		}
		ids = append(ids, id)
	}
	if offset > len(ids) {
		return nil, errors.New("cursor fora da lista")
	}
	end := min(offset+100, len(ids))
	page := make([]map[string]any, 0, end-offset)
	for _, id := range ids[offset:end] {
		sess, err := s.Manager.GetSession(ctx, "acp", id)
		if err != nil {
			return nil, err
		}
		entry := map[string]any{"sessionId": id, "cwd": s.sessionCWD(id)}
		if !sess.UpdatedAt.IsZero() {
			entry["updatedAt"] = sess.UpdatedAt.Format(time.RFC3339)
		}
		for _, msg := range sess.Messages {
			if msg.Role == conversation.RoleUser && msg.Content != "" {
				entry["title"] = firstLine(msg.Content)
				break
			}
		}
		page = append(page, entry)
	}
	result := map[string]any{"sessions": page}
	if end < len(ids) {
		result["nextCursor"] = strconv.Itoa(end)
	}
	return result, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	if len([]rune(line)) > 120 {
		return string([]rune(line)[:120])
	}
	return line
}

func (s *Server) promptParams(raw json.RawMessage) (string, string, string, error) {
	var p struct {
		SessionID string                     `json:"sessionId"`
		Meta      map[string]json.RawMessage `json:"_meta"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", "", "", err
	}
	if !s.exists(p.SessionID) {
		return "", "", "", errors.New("sessão não encontrada")
	}
	var turnKey string
	if value, ok := p.Meta["github.com/costa/polypod/turnId"]; ok {
		if json.Unmarshal(value, &turnKey) != nil || strings.TrimSpace(turnKey) == "" || len(turnKey) > 128 {
			return "", "", "", errors.New("turnId inválido")
		}
	}
	var pieces []string
	for _, part := range p.Prompt {
		if part.Type != "text" {
			return "", "", "", errors.New("somente blocos de texto são aceitos")
		}
		pieces = append(pieces, part.Text)
	}
	text := strings.TrimSpace(strings.Join(pieces, "\n"))
	if text == "" {
		return "", "", "", errors.New("prompt vazio")
	}
	return p.SessionID, text, turnKey, nil
}

func (s *Server) runPrompt(ctx context.Context, id, text, turnKey string, requestID json.RawMessage) error {
	if turnKey != "" {
		receipt, fresh, err := s.claimTurn(id, turnKey, text)
		if err != nil {
			return s.fail(requestID, -32000, err.Error())
		}
		if !fresh {
			if receipt.Error != "" {
				return s.fail(requestID, -32000, receipt.Error)
			}
			return s.response(requestID, map[string]string{"stopReason": receipt.StopReason})
		}
	}
	finish := func(reason, message string) error {
		if turnKey != "" {
			if err := s.completeTurn(id, turnKey, text, reason, message); err != nil {
				return s.fail(requestID, -32000, err.Error())
			}
		}
		if message != "" {
			return s.fail(requestID, -32000, message)
		}
		return s.response(requestID, map[string]string{"stopReason": reason})
	}
	if err := s.appendEvent(id, map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]string{"type": "text", "text": text}}); err != nil {
		return finish("", err.Error())
	}
	chunks := make(chan adapter.StreamChunk, 64)
	inMeta := map[string]string{"polypod_acp_managed_events": "1"}
	if turnKey != "" {
		inMeta["polypod_turn_id"] = turnKey
	}
	promptCtx := tool.WithSessionTools(ctx, s.sessionTools(id)...)
	go s.Stream(promptCtx, adapter.InMessage{Channel: "acp", UserID: id, Text: text, Timestamp: time.Now(), Metadata: inMeta}, chunks)
	var turnErr error
	var transportErr error
	anonymousCalls := make(map[string][]string)
	fileSnapshots := make(map[string]acpFileSnapshot)
	for item := range chunks {
		if item.ToolEvent != nil {
			ev := item.ToolEvent
			toolID := ev.ToolID
			if toolID == "" {
				if ev.Type == adapter.ToolEventStart {
					toolID = uuid.NewString()
					anonymousCalls[ev.ToolName] = append(anonymousCalls[ev.ToolName], toolID)
				} else if queued := anonymousCalls[ev.ToolName]; len(queued) > 0 {
					toolID = queued[0]
					if ev.Type == adapter.ToolEventComplete || ev.Type == adapter.ToolEventError {
						anonymousCalls[ev.ToolName] = queued[1:]
					}
				}
			}
			body := map[string]any{"toolCallId": toolID}
			switch ev.Type {
			case adapter.ToolEventStart:
				body["sessionUpdate"] = "tool_call"
				body["title"] = ev.ToolName
				body["name"] = ev.ToolName
				body["kind"] = toolKind(ev.ToolName)
				body["status"] = "in_progress"
				if ev.Args != nil {
					body["rawInput"] = ev.Args
					if path := s.toolFilePath(id, ev.Args); path != "" {
						body["locations"] = []map[string]string{{"path": path}}
						if isACPFileMutationTool(ev.ToolName) {
							fileSnapshots[toolID] = snapshotACPFile(path)
						}
					}
				}
			case adapter.ToolEventComplete:
				body["sessionUpdate"] = "tool_call_update"
				body["status"] = "completed"
				if ev.Result != "" {
					body["rawOutput"] = previewToolOutput(ev.Result)
				}
				if previous, ok := fileSnapshots[toolID]; ok {
					if diff := previous.completedDiff(); diff != nil {
						body["content"] = []any{diff}
					}
					delete(fileSnapshots, toolID)
				}
			case adapter.ToolEventError:
				body["sessionUpdate"] = "tool_call_update"
				body["status"] = "failed"
				if ev.Error != "" {
					body["rawOutput"] = previewToolOutput(ev.Error)
				}
				delete(fileSnapshots, toolID)
			case adapter.ToolEventProgress:
				body["sessionUpdate"] = "tool_call_update"
				body["status"] = "in_progress"
				if ev.Result != "" {
					body["rawOutput"] = previewToolOutput(ev.Result)
				}
			default:
				body = nil
			}
			if toolID == "" {
				body = nil
			}
			if body != nil {
				if err := s.update(id, body); err != nil {
					transportErr = err
				}
			}
		}
		if item.Delta != "" {
			if err := s.update(id, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": item.Delta}}); err != nil {
				transportErr = err
			}
		}
		if item.Error != nil {
			turnErr = item.Error
		}
	}
	if transportErr != nil {
		if turnKey != "" {
			if err := s.completeTurn(id, turnKey, text, "", transportErr.Error()); err != nil {
				return err
			}
		}
		return transportErr
	}
	if ctx.Err() != nil {
		return finish("cancelled", "")
	}
	if turnErr != nil {
		return finish("", turnErr.Error())
	}
	return finish("end_turn", "")
}

func toolKind(name string) string {
	if strings.HasPrefix(name, "mcp__") {
		if parts := strings.SplitN(name, "__", 3); len(parts) == 3 && parts[2] != "" {
			name = parts[2]
		}
	}
	switch {
	case strings.Contains(name, "search"), strings.Contains(name, "grep"):
		return "search"
	case strings.Contains(name, "read"), strings.Contains(name, "list"):
		return "read"
	case strings.Contains(name, "write"), strings.Contains(name, "edit"), strings.Contains(name, "patch"):
		return "edit"
	case strings.Contains(name, "delete"), strings.Contains(name, "remove"):
		return "delete"
	case strings.Contains(name, "exec"), strings.Contains(name, "shell"), strings.Contains(name, "bash"):
		return "execute"
	default:
		return "other"
	}
}

func previewToolOutput(value string) string {
	const maxBytes = 64 << 10
	if len(value) <= maxBytes {
		return value
	}
	return strings.ToValidUTF8(value[:maxBytes], "") + "\n… (saída truncada no ACP)"
}
