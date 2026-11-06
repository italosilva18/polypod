package whatsappgateway

import (
	"context"
	"sync"
)

// AskDeliveryStatus tracks a question that could not yet reach WhatsApp.
// It belongs to one inbound agent turn and never enters prompts or disk state.
type AskDeliveryStatus struct {
	mu          sync.Mutex
	questionID  string
	unavailable bool
	cancel      context.CancelFunc
}

func NewAskDeliveryStatus(cancel context.CancelFunc) *AskDeliveryStatus {
	return &AskDeliveryStatus{cancel: cancel}
}

func (s *AskDeliveryStatus) observeUnavailable(questionID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.questionID, s.unavailable = questionID, true
	s.mu.Unlock()
}

func (s *AskDeliveryStatus) markDelivered() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.unavailable = false
	s.mu.Unlock()
}

func (s *AskDeliveryStatus) cancelUnavailable() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *AskDeliveryStatus) unavailableQuestion() (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.questionID, s.unavailable
}

// InboundContext identifies the WhatsApp turn that is currently using a tool.
// It is carried only in memory; callers must never include it in model prompts.
type InboundContext struct {
	MessageID string
	ChatID    string
	SenderID  string
	AskStatus *AskDeliveryStatus
}

type inboundContextKey struct{}

func WithInboundContext(ctx context.Context, source InboundContext) context.Context {
	return context.WithValue(ctx, inboundContextKey{}, source)
}

func InboundFromContext(ctx context.Context) (InboundContext, bool) {
	if ctx == nil {
		return InboundContext{}, false
	}
	source, ok := ctx.Value(inboundContextKey{}).(InboundContext)
	return source, ok && source.MessageID != "" && source.ChatID != "" && source.SenderID != ""
}
