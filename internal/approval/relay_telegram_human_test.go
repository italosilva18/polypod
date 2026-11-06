package approval

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func relayTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestParseTextReply(t *testing.T) {
	tests := []struct {
		input  string
		answer string
		id     string
		ok     bool
	}{
		{input: "resposta q_123 Pode continuar", answer: "Pode continuar", id: "q_123", ok: true},
		{input: "R q_9 minha escolha", answer: "minha escolha", id: "q_9", ok: true},
		{input: "sim abc", ok: false},
		{input: "resposta abc", ok: false},
	}
	for _, tt := range tests {
		answer, id, ok := parseTextReply(tt.input)
		if answer != tt.answer || id != tt.id || ok != tt.ok {
			t.Fatalf("parseTextReply(%q) = (%q, %q, %v)", tt.input, answer, id, ok)
		}
	}
}

func TestDispatchTextReplyRestrictsConfiguredChat(t *testing.T) {
	relay := NewTelegramApprovalRelay(TelegramRelayConfig{BotToken: "secret", ChatID: "42", Logger: relayTestLogger()})
	answers := make(chan string, 1)
	relay.textPending["q_1"] = answers

	relay.dispatchUpdates([]tgUpdate{{
		UpdateID: 1,
		Message:  &tgMessage{Chat: &tgChat{ID: 99}, Text: "resposta q_1 invasor"},
	}})
	select {
	case answer := <-answers:
		t.Fatalf("unexpected answer from disallowed chat: %q", answer)
	default:
	}

	relay.dispatchUpdates([]tgUpdate{{
		UpdateID: 2,
		Message:  &tgMessage{Chat: &tgChat{ID: 42}, Text: "resposta q_1 autorizado"},
	}})
	select {
	case answer := <-answers:
		if answer != "autorizado" {
			t.Fatalf("answer = %q", answer)
		}
	default:
		t.Fatal("allowed chat reply was not dispatched")
	}
}

func TestAskQuestionEndToEndWithHTTPTransport(t *testing.T) {
	relay := NewTelegramApprovalRelay(TelegramRelayConfig{
		BotToken: "super-secret-token",
		ChatID:   "42",
		BaseURL:  "https://relay.invalid",
		Logger:   relayTestLogger(),
	})
	var mu sync.Mutex
	requests := make([]string, 0, 2)
	relay.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, req.URL.Path)
		mu.Unlock()
		body := `{"ok":true}`
		if strings.HasSuffix(req.URL.Path, "/getUpdates") {
			body = `{"ok":true,"result":[{"update_id":7,"message":{"message_id":1,"chat":{"id":42},"text":"resposta q_abc azul"}}]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	answer, err := relay.AskQuestion(ctx, "q_abc", "Qual cor?")
	if err != nil {
		t.Fatalf("AskQuestion: %v", err)
	}
	if answer != "azul" {
		t.Fatalf("answer = %q", answer)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || !strings.HasSuffix(requests[0], "/sendMessage") || !strings.HasSuffix(requests[1], "/getUpdates") {
		t.Fatalf("unexpected requests: %v", requests)
	}
}

func TestTransportErrorDoesNotExposeToken(t *testing.T) {
	const token = "token-ultrassecreto"
	relay := NewTelegramApprovalRelay(TelegramRelayConfig{BotToken: token, ChatID: "42", Logger: relayTestLogger()})
	relay.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network error containing " + token)
	})}
	err := relay.SendNotification(context.Background(), "Aviso", "Falhou")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("credential leaked in error: %v", err)
	}
}
