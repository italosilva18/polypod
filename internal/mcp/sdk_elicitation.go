package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// SetElicitationHandler attaches a human-question channel to SDK MCP sessions.
// Register it before Connect so incoming server requests can be answered.
// With no handler, the SDK callback explicitly cancels each request.
func (m *Manager) SetElicitationHandler(handler ElicitationHandler) {
	m.mu.Lock()
	m.elicitationHandler = handler
	m.mu.Unlock()
}

func (m *Manager) handleSDKElicitation(ctx context.Context, incoming *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
	cancelled := &mcpsdk.ElicitResult{Action: string(ElicitationCancel)}
	if incoming == nil || incoming.Params == nil {
		return cancelled, nil
	}
	m.mu.RLock()
	handler := m.elicitationHandler
	m.mu.RUnlock()
	if handler == nil {
		m.logger.Warn("mcp elicitation canceled: no human channel", "server", m.serverForSession(incoming.Session))
		return cancelled, nil
	}
	mode := incoming.Params.Mode
	if mode == "" {
		mode = "form"
		if incoming.Params.URL != "" {
			mode = "url"
		}
	}
	var schema map[string]any
	if incoming.Params.RequestedSchema != nil {
		encoded, err := json.Marshal(incoming.Params.RequestedSchema)
		if err != nil {
			return cancelled, fmt.Errorf("mcp elicitation schema: %w", err)
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			return cancelled, fmt.Errorf("mcp elicitation schema: %w", err)
		}
	}
	request := &ElicitationRequest{
		ServerName:      m.serverForSession(incoming.Session),
		Message:         incoming.Params.Message,
		RequestedSchema: schema,
		Mode:            mode,
		URL:             incoming.Params.URL,
		ElicitationID:   incoming.Params.ElicitationID,
	}
	response, err := m.awaitHumanElicitation(ctx, incoming.Session, request, handler)
	if err != nil {
		if ctx.Err() != nil {
			return cancelled, ctx.Err()
		}
		m.logger.Warn("mcp elicitation canceled after human-channel error", "server", request.ServerName, "error", err)
		return cancelled, nil
	}
	if response == nil {
		return cancelled, nil
	}
	switch response.Action {
	case ElicitationAccept:
		if mode == "form" {
			if err := ValidateElicitationResponse(schema, response.Content); err != nil {
				return cancelled, fmt.Errorf("mcp elicitation response: %w", err)
			}
		}
		return &mcpsdk.ElicitResult{Action: string(ElicitationAccept), Content: response.Content}, nil
	case ElicitationDecline:
		return &mcpsdk.ElicitResult{Action: string(ElicitationDecline)}, nil
	case ElicitationCancel:
		return cancelled, nil
	default:
		return cancelled, fmt.Errorf("mcp elicitation action %q invalid", response.Action)
	}
}

type elicitationAnswer struct {
	result *ElicitationResult
	err    error
}

func (m *Manager) awaitHumanElicitation(ctx context.Context, session *mcpsdk.ClientSession, req *ElicitationRequest, handler ElicitationHandler) (*ElicitationResult, error) {
	// Form elicitation completes with a direct human answer. A URL flow can also
	// complete when the server reports that the out-of-band step finished.
	if req.Mode != "url" || req.ElicitationID == "" || session == nil {
		return handler(ctx, req)
	}
	completed := m.trackElicitation(session, req.ElicitationID)
	if completed == nil {
		return &ElicitationResult{Action: ElicitationCancel}, nil
	}
	defer m.untrackElicitation(session, req.ElicitationID, completed)
	answerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	answerCh := make(chan elicitationAnswer, 1)
	go func() {
		result, err := handler(answerCtx, req)
		answerCh <- elicitationAnswer{result: result, err: err}
	}()
	select {
	case <-completed:
		return &ElicitationResult{Action: ElicitationAccept}, nil
	case answer := <-answerCh:
		return answer.result, answer.err
	case <-ctx.Done():
		return &ElicitationResult{Action: ElicitationCancel}, ctx.Err()
	}
}

func (m *Manager) trackElicitation(session *mcpsdk.ClientSession, id string) chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	if m.elicitationWaiters[session] == nil {
		m.elicitationWaiters[session] = make(map[string]chan struct{})
	}
	if m.elicitationWaiters[session][id] != nil {
		return nil
	}
	completed := make(chan struct{})
	m.elicitationWaiters[session][id] = completed
	return completed
}

func (m *Manager) untrackElicitation(session *mcpsdk.ClientSession, id string, completed chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.elicitationWaiters[session][id] == completed {
		delete(m.elicitationWaiters[session], id)
		if len(m.elicitationWaiters[session]) == 0 {
			delete(m.elicitationWaiters, session)
		}
	}
}

func (m *Manager) handleElicitationComplete(_ context.Context, notification *mcpsdk.ElicitationCompleteNotificationRequest) {
	if notification == nil || notification.Session == nil || notification.Params == nil {
		return
	}
	id := notification.Params.ElicitationID
	m.mu.Lock()
	if waiting := m.elicitationWaiters[notification.Session]; waiting != nil {
		if completed := waiting[id]; completed != nil {
			delete(waiting, id)
			if len(waiting) == 0 {
				delete(m.elicitationWaiters, notification.Session)
			}
			close(completed)
		}
	}
	m.mu.Unlock()
}

// PromptElicitation converts an MCP request into a human-readable question.
// ask can be backed by Polypod's existing ask_user relay. It never invents an
// answer, and form data is returned only after the user supplies it.
func PromptElicitation(ctx context.Context, req *ElicitationRequest, ask func(context.Context, string) (string, error)) (*ElicitationResult, error) {
	if req == nil || ask == nil {
		return &ElicitationResult{Action: ElicitationCancel}, nil
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		message = "O servidor MCP pediu informacoes adicionais."
	}
	if len(message) > 8000 {
		message = message[:8000] + "…"
	}
	prefix := "Servidor MCP " + req.ServerName + ": " + message
	if req.Mode == "url" {
		question := prefix + "\nURL: " + req.URL + "\nConclua a etapa no navegador e responda 'concluido'. Use 'recusar' ou 'cancelar' para encerrar."
		answer, err := ask(ctx, question)
		if err != nil {
			return &ElicitationResult{Action: ElicitationCancel}, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "concluido", "concluído", "feito", "sim":
			return &ElicitationResult{Action: ElicitationAccept}, nil
		case "recusar", "não", "nao":
			return &ElicitationResult{Action: ElicitationDecline}, nil
		default:
			return &ElicitationResult{Action: ElicitationCancel}, nil
		}
	}
	schemaText := "{}"
	if req.RequestedSchema != nil {
		encoded, err := json.Marshal(req.RequestedSchema)
		if err != nil {
			return &ElicitationResult{Action: ElicitationCancel}, err
		}
		schemaText = string(encoded)
	}
	if len(schemaText) > 8000 {
		schemaText = schemaText[:8000] + "…"
	}
	fieldName := singleStringField(req.RequestedSchema)
	question := prefix + "\nCampos solicitados: " + schemaText
	if fieldName != "" {
		question += "\nResponda com o valor de " + fieldName + " ou com um objeto JSON."
	} else {
		question += "\nResponda com um objeto JSON contendo os campos solicitados."
	}
	question += " Digite 'recusar' ou 'cancelar' para encerrar."
	for attempt := 0; attempt < 3; attempt++ {
		answer, err := ask(ctx, question)
		if err != nil {
			return &ElicitationResult{Action: ElicitationCancel}, err
		}
		trimmed := strings.TrimSpace(answer)
		switch strings.ToLower(trimmed) {
		case "recusar":
			return &ElicitationResult{Action: ElicitationDecline}, nil
		case "cancelar", "":
			return &ElicitationResult{Action: ElicitationCancel}, nil
		}
		var content map[string]any
		if err := json.Unmarshal([]byte(trimmed), &content); err != nil && fieldName != "" {
			content = map[string]any{fieldName: trimmed}
		} else if err != nil {
			question = "A resposta precisa ser um objeto JSON valido. " + question
			continue
		}
		if content == nil {
			question = "A resposta precisa ser um objeto JSON. " + question
			continue
		}
		if err := ValidateElicitationResponse(req.RequestedSchema, content); err != nil {
			question = fmt.Sprintf("Resposta invalida: %v. %s", err, question)
			continue
		}
		return &ElicitationResult{Action: ElicitationAccept, Content: content}, nil
	}
	return &ElicitationResult{Action: ElicitationCancel}, nil
}

func singleStringField(schema map[string]any) string {
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) != 1 {
		return ""
	}
	for name, raw := range properties {
		property, ok := raw.(map[string]any)
		if ok && property["type"] == "string" {
			return name
		}
	}
	return ""
}
