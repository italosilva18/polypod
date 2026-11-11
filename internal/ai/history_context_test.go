package ai

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/conversation"
)

func allowJournalID(_ context.Context, ids []string) (map[string]bool, error) {
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	return allowed, nil
}

func TestWindowConversationPromptKeepsShortConversationExact(t *testing.T) {
	history := []conversation.Message{
		{ID: "one", Role: conversation.RoleUser, Content: "primeira pergunta"},
		{ID: "two", Role: conversation.RoleAssistant, Content: "primeira resposta"},
	}
	messages, err := windowConversationPrompt(context.Background(), history, "nova pergunta", "", 4000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || messages[0].Content != history[0].Content || messages[1].Content != history[1].Content || messages[2].Content != "nova pergunta" {
		t.Fatalf("short history changed: %+v", messages)
	}
}

func TestWindowConversationPromptMinimumBudgetAcceptsShortQuery(t *testing.T) {
	ctx := conversation.WithJournalScope(context.Background(), "cli", "local", "cli:local", "default")
	messages, err := windowConversationPrompt(ctx, nil, "oi", "current", 1024, allowJournalID)
	if err != nil || len(messages) != 1 || messages[0].Content != "oi" {
		t.Fatalf("minimum supported budget rejected short query: %+v, %v", messages, err)
	}
}

func TestWindowConversationPromptBoundsHugeHistoryAndKeepsRecentInstructions(t *testing.T) {
	ctx := conversation.WithJournalScope(context.Background(), "cli", "local", "cli:local", "default")
	history := []conversation.Message{
		{ID: "boundary", Role: conversation.RoleSystem, Content: "[compact_boundary] Conversation compacted", Origin: conversation.OriginCompaction},
		{ID: "summary", Role: conversation.RoleUser, Content: "This session is being continued from a previous conversation that ran out of context. The summary below covers earlier work.\n" + strings.Repeat("resumo antigo ", 7000), Origin: conversation.OriginCompaction},
	}
	for i := 0; i < 30; i++ {
		history = append(history, conversation.Message{ID: "turn-" + string(rune('a'+i)), Role: conversation.RoleAssistant, Content: strings.Repeat("detalhe anterior ", 1000)})
	}
	history = append(history, conversation.Message{ID: "recent", Role: conversation.RoleUser, Content: "Instrução recente: entregar o relatório em português."})
	messages, err := windowConversationPrompt(ctx, history, "Continue o relatório", "current", 4000, allowJournalID)
	if err != nil {
		t.Fatal(err)
	}
	var total int
	var joined strings.Builder
	for _, message := range messages {
		total += promptTokens(message.Content)
		joined.WriteString(message.Content)
	}
	if total > 4000 {
		t.Fatalf("prompt still exceeds budget: %d tokens", total)
	}
	got := joined.String()
	if !strings.Contains(got, "Instrução recente: entregar o relatório em português.") || !strings.Contains(got, `"message_id":"summary"`) {
		t.Fatalf("lost recent turn or summary reference: %s", got)
	}
	if !strings.Contains(got, "history_search") || !strings.Contains(got, "history_recall") {
		t.Fatalf("missing recovery guidance: %s", got)
	}
	if history[1].Content == messages[0].Content {
		t.Fatal("persisted history was modified")
	}
}

func TestWindowConversationPromptRequiresRecoverableReference(t *testing.T) {
	long := strings.Repeat("comando longo ", 2000)
	if _, err := windowConversationPrompt(context.Background(), nil, long, "", 4000, nil); err == nil {
		t.Fatal("unscoped oversized query was silently shortened")
	}
	ctx := conversation.WithJournalScope(context.Background(), "cli", "local", "cli:local", "default")
	if _, err := windowConversationPrompt(ctx, nil, long, "", 4000, allowJournalID); err == nil {
		t.Fatal("oversized query without a saved ID was silently shortened")
	}
	messages, err := windowConversationPrompt(ctx, nil, long, "current-id", 4000, allowJournalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Content, `"message_id":"current-id"`) || promptTokens(messages[0].Content) > 4000 {
		t.Fatalf("query preview is not bounded and recoverable: %+v", messages)
	}
}

func TestPromptMessagePreviewKeepsUnicodeValid(t *testing.T) {
	long := strings.Repeat("ação 🚀 ", 2000)
	preview, err := promptMessagePreview(long, "unicode-id", 300, true)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(preview) || !strings.Contains(preview, "unicode-id") || promptTokens(preview) > 300 {
		t.Fatalf("invalid preview: %q", preview)
	}
	if _, err := promptMessagePreview(long, strings.Repeat("x", 2000), 300, true); err == nil {
		t.Fatal("oversized reference made preview exceed its cap")
	}
}

func TestPromptConversationBudgetAccountsForSystemInstructions(t *testing.T) {
	base, err := promptConversationBudget("deepseek-chat", "short")
	if err != nil || base != historyWindowBudget("deepseek-chat") {
		t.Fatalf("unexpected short-system budget: %d, %v", base, err)
	}
	if _, err := promptConversationBudget("deepseek-chat", strings.Repeat("instruções ", 50_000)); err == nil {
		t.Fatal("oversized system context was sent to the provider")
	}
}

func TestRequestModelUsesRoutedTurn(t *testing.T) {
	service := &Service{client: &Client{model: "deepseek-chat"}}
	if got := service.requestModel(context.Background()); got != "deepseek-chat" {
		t.Fatalf("unexpected default model %q", got)
	}
	ctx := withRequestModel(context.Background(), "gpt-4o")
	if got := service.requestModel(ctx); got != "gpt-4o" {
		t.Fatalf("routed model ignored: %q", got)
	}
}

func TestSpecializedAgentsHaveHistoryToolsAndCustomAgentWithoutThemFailsSafely(t *testing.T) {
	for _, specialized := range agent.AutonomousAgents() {
		if !agentHasHistoryTools(specialized.Skills) {
			t.Fatalf("built-in agent %q cannot recall shortened context", specialized.Name)
		}
	}
	if agentHasHistoryTools([]string{"read_file"}) {
		t.Fatal("custom agent without history tools was marked recoverable")
	}
	ctx := conversation.WithJournalScope(context.Background(), "cli", "local", "cli:local", "custom")
	large := strings.Repeat("instrução extensa ", 2000)
	if _, err := windowConversationPrompt(ctx, nil, large, "saved-id", 4000, nil); err == nil {
		t.Fatal("agent without history tools silently received shortened message")
	}
}

func TestWindowConversationPromptNeverPromisesOtherAgentJournalID(t *testing.T) {
	store := conversation.NewStore(nil, nil, t.TempDir())
	sess := conversation.NewSession("cli:local", "cli", "local")
	oldAgentCtx := conversation.WithJournalScope(context.Background(), "cli", "local", "cli:local", "default")
	oldTurn := conversation.Message{ID: "old-agent-long", Role: conversation.RoleUser, Content: strings.Repeat("instrução anterior ", 2000)}
	sess.Messages = []conversation.Message{oldTurn}
	if err := store.Save(oldAgentCtx, sess); err != nil {
		t.Fatal(err)
	}
	newAgentCtx := conversation.WithJournalScope(context.Background(), "cli", "local", "cli:local", "new-agent")
	newTurn := conversation.Message{ID: "new-agent-recent", Role: conversation.RoleUser, Content: "Instrução recente no novo agente"}
	sess.Messages = append(sess.Messages, newTurn)
	if err := store.Save(newAgentCtx, sess); err != nil {
		t.Fatal(err)
	}
	lookup := func(ctx context.Context, ids []string) (map[string]bool, error) {
		scope, _ := conversation.JournalScopeFromContext(ctx)
		return store.CanReadJournalScopedBatch(ctx, scope, ids)
	}
	if _, err := windowConversationPrompt(newAgentCtx, sess.Messages, "continue", "new-query", 4000, lookup); err == nil {
		t.Fatal("context promised recall of another agent's inaccessible old turn")
	}
	messages, err := windowConversationPrompt(oldAgentCtx, sess.Messages, "continue", "old-query", 4000, lookup)
	if err != nil {
		t.Fatal(err)
	}
	var joined strings.Builder
	for _, message := range messages {
		joined.WriteString(message.Content)
	}
	if !strings.Contains(joined.String(), `"message_id":"old-agent-long"`) {
		t.Fatal("same-agent long turn lost its exact reference")
	}
}
