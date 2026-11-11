package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/conversation"
)

const testOwnerChat = "123456789@lid"

func ownerMemoryTestContext(channel, user, branch string) context.Context {
	ctx := conversation.WithJournalScope(context.Background(), channel, user, branch, "default")
	ctx = WithGlobalMemoryAccess(ctx)
	ctx = WithOwnerLinkedMemory(ctx, channel, testOwnerChat)
	return scopedMemorySkillContext(ctx)
}

func memoryTopicSet(memories []Memory) map[string]bool {
	topics := make(map[string]bool, len(memories))
	for _, m := range memories {
		topics[m.Topic] = true
	}
	return topics
}

func TestOwnerLinkedMemoryReadsExistingFactsWithoutChangingScope(t *testing.T) {
	store := NewManagedStore(NewFileStore(t.TempDir()))
	ownerWhatsAppCtx := conversation.WithJournalScope(context.Background(), "whatsapp", testOwnerChat, "", "default")
	ownerWhatsAppTopic, _ := scopedUserTopic(ownerWhatsAppCtx, "owner_preference")
	otherWhatsAppTopic, _ := scopedUserTopic(conversation.WithJournalScope(context.Background(), "whatsapp", "other@lid", "", "default"), "other_secret")
	facts := []Memory{
		{Topic: "cli_global", Content: "prefiro Go", Source: "user"},
		{Topic: ownerWhatsAppTopic, Content: "prefiro cafe", SessionID: "whatsapp:" + testOwnerChat, Source: "user"},
		{Topic: "owner_quote", Content: "prefiro cafe forte", SessionID: "whatsapp:" + testOwnerChat, Source: "dream", Tags: []string{"evidence:quote_verified"}},
		{Topic: "cli_branch_a_quote", Content: "moro em Recife", SessionID: "cli-branch:branch-a", Source: "dream", Tags: []string{"evidence:quote_verified"}},
		{Topic: "cli_branch_b_quote", Content: "gosto de inverno", SessionID: "cli-branch:branch-b", Source: "dream", Tags: []string{"evidence:quote_verified"}},
		{Topic: "cli_legacy_quote", Content: "tenho dois gatos", SessionID: "cli:local", Source: "dream", Tags: []string{"evidence:quote_verified"}},
		{Topic: "cli_ungrounded", Content: "sou piloto", SessionID: "cli-branch:branch-a", Source: "dream"},
		{Topic: "cli_episode", Content: "pedi para viajar", SessionID: "cli-branch:branch-a", Source: "dream_episode"},
		{Topic: otherWhatsAppTopic, Content: "segredo de outro chat", SessionID: "whatsapp:other@lid", Source: "user"},
		{Topic: "system_fact", Content: "rotina confirmada", SessionID: "system:owner", Source: "user"},
	}
	for _, fact := range facts {
		if err := store.SaveWithMeta(context.Background(), fact); err != nil {
			t.Fatal(err)
		}
	}

	cli := ownerMemoryTestContext("cli", "local", "branch-a")
	cliMemories, err := store.List(cli)
	if err != nil {
		t.Fatal(err)
	}
	cliTopics := memoryTopicSet(cliMemories)
	for _, topic := range []string{"cli_global", ownerWhatsAppTopic, "owner_quote", "cli_branch_a_quote", "cli_ungrounded", "cli_episode", "system_fact"} {
		if !cliTopics[topic] {
			t.Errorf("CLI owner cannot read %s", topic)
		}
	}
	for _, topic := range []string{"cli_branch_b_quote", "cli_legacy_quote", otherWhatsAppTopic} {
		if cliTopics[topic] {
			t.Errorf("CLI branch received unrelated fact %s", topic)
		}
	}

	ownerWhatsApp := ownerMemoryTestContext("whatsapp", testOwnerChat, "")
	ownerMemories, err := store.List(ownerWhatsApp)
	if err != nil {
		t.Fatal(err)
	}
	ownerTopics := memoryTopicSet(ownerMemories)
	for _, topic := range []string{"cli_global", ownerWhatsAppTopic, "owner_quote", "cli_branch_a_quote", "cli_branch_b_quote", "cli_legacy_quote", "system_fact"} {
		if !ownerTopics[topic] {
			t.Errorf("owner WhatsApp cannot read %s", topic)
		}
	}
	for _, topic := range []string{"cli_ungrounded", "cli_episode", otherWhatsAppTopic} {
		if ownerTopics[topic] {
			t.Errorf("owner WhatsApp received unverified or unrelated fact %s", topic)
		}
	}
	stored, err := store.Store.Get(context.Background(), ownerWhatsAppTopic)
	if err != nil || stored.SessionID != "whatsapp:"+testOwnerChat || stored.Topic != ownerWhatsAppTopic {
		t.Fatalf("cross-channel read changed stored provenance: %v, %v", stored, err)
	}
}

func TestOwnerLinkedMemorySkillsShareOnlyOwnerFacts(t *testing.T) {
	store := NewManagedStore(NewFileStore(t.TempDir()))
	owner := ownerMemoryTestContext("whatsapp", testOwnerChat, "")
	cli := ownerMemoryTestContext("cli", "local", "branch-a")
	other := scopedMemorySkillContext(conversation.WithJournalScope(context.Background(), "whatsapp", "other@lid", "", "default"))

	result, err := executeScopedMemorySkill(owner, store, "save_memory", map[string]any{"topic": "cor_favorita", "content": "Prefiro azul"})
	if err != nil || result.IsError {
		t.Fatalf("owner save: %+v, %v", result, err)
	}
	result, err = executeScopedMemorySkill(cli, store, "recall_memory", map[string]any{"query": "azul"})
	if err != nil || result.IsError || !strings.Contains(result.Content, "Prefiro azul") || !strings.Contains(result.Content, "[cor_favorita]") {
		t.Fatalf("CLI missed owner WhatsApp fact: %+v, %v", result, err)
	}
	result, err = executeScopedMemorySkill(other, store, "recall_memory", map[string]any{"query": "azul"})
	if err != nil || result.IsError || strings.Contains(result.Content, "Prefiro azul") {
		t.Fatalf("other chat read owner memory: %+v, %v", result, err)
	}
	result, err = executeScopedMemorySkill(cli, store, "save_memory", map[string]any{"topic": "linguagem", "content": "Prefiro Go"})
	if err != nil || result.IsError {
		t.Fatalf("CLI save: %+v, %v", result, err)
	}
	result, err = executeScopedMemorySkill(owner, store, "recall_memory", map[string]any{"query": "Go"})
	if err != nil || result.IsError || !strings.Contains(result.Content, "Prefiro Go") {
		t.Fatalf("owner WhatsApp missed CLI fact: %+v, %v", result, err)
	}
	result, err = executeScopedMemorySkill(other, store, "recall_memory", map[string]any{"query": "Go"})
	if err != nil || result.IsError || strings.Contains(result.Content, "Prefiro Go") {
		t.Fatalf("other chat read CLI fact: %+v, %v", result, err)
	}
	if injected := AutoInject(cli, store, "azul"); !strings.Contains(injected, "Prefiro azul") {
		t.Fatalf("CLI prompt did not receive owner memory: %q", injected)
	}
	if injected := AutoInject(other, store, "azul"); strings.Contains(injected, "Prefiro azul") {
		t.Fatalf("other chat prompt received owner memory: %q", injected)
	}
}

func TestSemanticIndexSeparatesOwnerAndUntrustedViewsOfSameChat(t *testing.T) {
	store := NewFileStore(t.TempDir())
	for _, m := range []Memory{
		{Topic: "owner_fact", Content: "apenas no chat", SessionID: "whatsapp:" + testOwnerChat, Source: "user"},
		{Topic: "cli_fact", Content: "apenas no CLI", SessionID: "cli-branch:main", Source: "dream", Tags: []string{"evidence:quote_verified"}},
		{Topic: "global_fact", Content: "fato geral", Source: "user"},
	} {
		if err := store.SaveWithMeta(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	idx := newSemanticIndex()
	embedder := &cacheTestEmbedder{name: "test-semantic-index"}
	ownerCtx := WithSessionScope(WithOwnerLinkedMemory(WithGlobalMemoryAccess(context.Background()), "whatsapp", testOwnerChat), "whatsapp:"+testOwnerChat)
	ownerSnapshot, err := idx.load(ownerCtx, store, embedder)
	if err != nil {
		t.Fatal(err)
	}
	untrustedCtx := WithSessionScope(context.Background(), "whatsapp:"+testOwnerChat)
	untrustedSnapshot, err := idx.load(untrustedCtx, store, embedder)
	if err != nil {
		t.Fatal(err)
	}
	if !memoryTopicSet(ownerSnapshot.memories)["cli_fact"] || !memoryTopicSet(ownerSnapshot.memories)["global_fact"] {
		t.Fatalf("owner snapshot omitted shared facts: %+v", ownerSnapshot.memories)
	}
	if memoryTopicSet(untrustedSnapshot.memories)["cli_fact"] || memoryTopicSet(untrustedSnapshot.memories)["global_fact"] {
		t.Fatalf("semantic snapshot leaked across authorization contexts: %+v", untrustedSnapshot.memories)
	}
	if len(idx.snapshots) != 2 || embedder.calls != 2 {
		t.Fatalf("visibility scopes shared one cached index: snapshots=%d embeds=%d", len(idx.snapshots), embedder.calls)
	}
}
