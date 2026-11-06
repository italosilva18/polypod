package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/document"
	"github.com/costa/polypod/internal/types"
	"github.com/costa/polypod/internal/whatsappgateway"
)

func TestDocumentKnowledgeSeparatesRemoteChatsFromLocalFiles(t *testing.T) {
	ctx := context.Background()
	idx, err := document.OpenIndex(filepath.Join(t.TempDir(), "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	localFile := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(localFile, []byte("localonly acácia"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := idx.IndexFile(ctx, localFile, document.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := idx.IndexText(ctx, "owner@lid", "owner-doc", "owneronly acácia", ".txt"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := idx.IndexText(ctx, "other@lid", "other-doc", "otheronly acácia", ".txt"); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	RegisterDocumentKnowledgeTool(reg, idx, "owner@lid")
	search := func(source whatsappgateway.InboundContext, query string) string {
		t.Helper()
		toolCtx := ctx
		if source.ChatID != "" {
			toolCtx = whatsappgateway.WithInboundContext(ctx, source)
		}
		result, err := reg.ExecuteRaw("document_knowledge", map[string]any{"action": "search", "query": query},
			types.ToolContext{Context: toolCtx, WorkingDir: t.TempDir()})
		if err != nil || result.IsError {
			t.Fatalf("search failed: %+v, %v", result, err)
		}
		return result.Content
	}
	other := search(whatsappgateway.InboundContext{MessageID: "in-other", ChatID: "other@lid", SenderID: "other@lid"}, "acácia")
	if !strings.Contains(other, "other-doc") || strings.Contains(other, "owner-doc") || strings.Contains(other, localFile) || strings.Contains(other, "owner@lid") {
		t.Fatalf("cross-chat result leaked: %s", other)
	}
	owner := search(whatsappgateway.InboundContext{MessageID: "in-owner", ChatID: "owner@lid", SenderID: "owner@lid"}, "acácia")
	if !strings.Contains(owner, "owner-doc") || !strings.Contains(owner, localFile) || strings.Contains(owner, "other-doc") {
		t.Fatalf("owner scopes incorrect: %s", owner)
	}
	local := search(whatsappgateway.InboundContext{}, "acácia")
	if !strings.Contains(local, localFile) || strings.Contains(local, "owner-doc") || strings.Contains(local, "other-doc") {
		t.Fatalf("local scope incorrect: %s", local)
	}
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(other), &parsed); err != nil || len(parsed) != 1 || parsed[0]["scope"] != "chat" {
		t.Fatalf("unexpected search output: %s, %v", other, err)
	}
	telegramCtx := WithRemoteDocumentScope(ctx, "telegram", "user-1")
	result, err := reg.ExecuteRaw("document_knowledge", map[string]any{"action": "search", "query": "acácia"},
		types.ToolContext{Context: telegramCtx, WorkingDir: t.TempDir()})
	if err != nil || result.IsError || result.Content != "[]" {
		t.Fatalf("telegram saw local documents: %+v, %v", result, err)
	}
}
