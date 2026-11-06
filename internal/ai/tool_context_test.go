package ai

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
)

type cancellationTestProvider struct {
	provider.Provider
	calls int
}

func (p *cancellationTestProvider) Chat(ctx context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "wait", Name: "wait_for_cancel", Arguments: "{}"}}}, nil
	}
	return nil, ctx.Err()
}

func (p *cancellationTestProvider) SupportsVision() bool { return false }

func TestNonStreamingToolReceivesRequestCancellation(t *testing.T) {
	started := make(chan struct{})
	reg := tool.NewRegistry()
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "wait_for_cancel" },
		DescriptionFn: func() string { return "Wait for request cancellation" },
		InputSchemaFn: func() map[string]any { return map[string]any{"type": "object"} },
		ExecuteFn: func(tc types.ToolContext, _ map[string]any) (types.ToolResult, error) {
			close(started)
			<-tc.Context.Done()
			return types.ToolResult{Content: tc.Context.Err().Error(), IsError: true}, nil
		},
	})
	adapter := NewToolRegistryAdapterWithContext(reg, func(ctx context.Context, name string, args map[string]any) (types.ToolResult, error) {
		return reg.Execute(name, args, types.ToolContext{Context: ctx, WorkingDir: "."})
	})
	client := &Client{responseProvider: &cancellationTestProvider{}, skills: adapter, toolsOn: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := client.CompleteWithTools(ctx, []ChatMessage{{Role: "user", Content: "wait"}}, silentLogger())
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("tool was not started")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("completion error = %v, want context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tool ignored request cancellation")
	}
}
