package ai

import (
	"context"
	"testing"

	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/whatsappgateway"
)

func TestOwnerMemoryContextRequiresTrustedWhatsAppOrigin(t *testing.T) {
	const ownerChat = "123456789@lid"
	store := memory.NewManagedStore(memory.NewFileStore(t.TempDir()))
	if err := store.SaveWithMeta(context.Background(), memory.Memory{
		Topic: "owner_cli_fact", Content: "prefiro Go", SessionID: "cli-branch:main",
		Source: "dream", Tags: []string{"evidence:quote_verified"},
	}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{}
	svc.SetExperienceSearcher(nil, ownerChat)
	validInbound := whatsappgateway.WithInboundContext(context.Background(), whatsappgateway.InboundContext{
		MessageID: "inbound-1", ChatID: ownerChat, SenderID: ownerChat,
	})
	tests := []struct {
		name    string
		ctx     context.Context
		req     AnswerRequest
		allowed bool
	}{
		{name: "local CLI", ctx: context.Background(), req: AnswerRequest{Channel: "cli", UserID: "local", SessionMemoryScope: "main"}, allowed: true},
		{name: "owner WhatsApp", ctx: validInbound, req: AnswerRequest{Channel: "whatsapp", UserID: ownerChat}, allowed: true},
		{name: "missing inbound provenance", ctx: context.Background(), req: AnswerRequest{Channel: "whatsapp", UserID: ownerChat}},
		{name: "wrong sender", ctx: whatsappgateway.WithInboundContext(context.Background(), whatsappgateway.InboundContext{
			MessageID: "inbound-2", ChatID: ownerChat, SenderID: "someone-else@lid",
		}), req: AnswerRequest{Channel: "whatsapp", UserID: ownerChat}},
		{name: "wrong chat", ctx: whatsappgateway.WithInboundContext(context.Background(), whatsappgateway.InboundContext{
			MessageID: "inbound-3", ChatID: "other@lid", SenderID: ownerChat,
		}), req: AnswerRequest{Channel: "whatsapp", UserID: ownerChat}},
		{name: "mismatched request", ctx: validInbound, req: AnswerRequest{Channel: "whatsapp", UserID: "other@lid"}},
		{name: "remote channel", ctx: validInbound, req: AnswerRequest{Channel: "remote", UserID: ownerChat}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := svc.ownerMemoryContext(tc.ctx, tc.req)
			scope := tc.req.Channel + ":" + tc.req.UserID
			if tc.req.Channel == "cli" && tc.req.SessionMemoryScope != "" {
				scope = "cli-branch:" + tc.req.SessionMemoryScope
			}
			ctx = memory.WithSessionScope(ctx, scope)
			memories, err := store.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			got := len(memories) == 1 && memories[0].Topic == "owner_cli_fact"
			if got != tc.allowed {
				t.Fatalf("cross-channel CLI memory visible=%v, want %v: %+v", got, tc.allowed, memories)
			}
		})
	}
}
