package ai

import (
	"context"
	"log/slog"
	"testing"

	"github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/turncheckpoint"
)

type steeringProvider struct {
	provider.Provider
	guidance chan string
	requests []provider.ChatRequest
}

func (p *steeringProvider) SupportsVision() bool { return false }
func (p *steeringProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if len(p.requests) == 1 {
		p.guidance <- "Use Go, não Python"
		return &provider.ChatResponse{Content: "rascunho"}, nil
	}
	return &provider.ChatResponse{Content: "solução em Go"}, nil
}

func TestCheckpointedTurnAppliesSteeringAtNextModelBoundary(t *testing.T) {
	manager := turncheckpoint.New(t.TempDir(), nil)
	guidance := make(chan string, 2)
	backend := &steeringProvider{guidance: guidance}
	client := &Client{responseProvider: backend, model: "model", skills: &resumeTestSkills{}, toolsOn: true}
	var observed []string
	ctx := WithTurnCheckpoint(context.Background(), manager, "session", "turn")
	ctx = WithSteering(ctx, guidance)
	ctx = WithSteeringObserver(ctx, func(message string) { observed = append(observed, message) })
	response, err := client.CompleteWithTools(ctx, []ChatMessage{{Role: "user", Content: "programar"}}, slog.Default())
	if err != nil || response.Content != "solução em Go" || len(backend.requests) != 2 {
		t.Fatalf("steered response = %+v, %v; requests=%d", response, err, len(backend.requests))
	}
	seen := false
	for _, message := range backend.requests[1].Messages {
		if message.Role == "user" && message.Content == "Use Go, não Python" {
			seen = true
		}
	}
	if !seen || len(observed) != 1 {
		t.Fatalf("guidance missing from request or transcript: %+v, %+v", backend.requests[1].Messages, observed)
	}
}
