package ai

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/costa/polypod/internal/conversation"
)

const (
	maxPromptHistoryTurnTokens = 4096
	promptWindowNoticeTokens   = 256
)

type journalReferenceLookup func(context.Context, []string) (map[string]bool, error)

func agentHasHistoryTools(skills []string) bool {
	var search, recall bool
	for _, skill := range skills {
		if skill == "*" {
			return true
		}
		search = search || skill == "history_search"
		recall = recall || skill == "history_recall"
	}
	return search && recall
}

// windowConversationPrompt bounds the transient model request only. The saved
// session and its journal retain the complete messages, including compaction
// summaries. A shortened message names the ID needed to read it exactly with
// history_recall. Without a scoped journal we fail instead of losing context.
func windowConversationPrompt(ctx context.Context, history []conversation.Message, query, currentID string, budget int, canRecall journalReferenceLookup) ([]ChatMessage, error) {
	if budget < 1024 {
		return nil, fmt.Errorf("orçamento de contexto inválido: %d tokens", budget)
	}
	_, scoped := conversation.JournalScopeFromContext(ctx)
	journalAvailable := scoped && canRecall != nil
	var verified map[string]bool
	loadedReferences := false
	checkReference := func(id string) error {
		if !journalAvailable || id == "" {
			return fmt.Errorf("mensagem sem referência recuperável neste agente e nesta conversa")
		}
		if !loadedReferences {
			ids := make([]string, 0, len(history)+1)
			ids = append(ids, currentID)
			for _, message := range history {
				ids = append(ids, message.ID)
			}
			var err error
			verified, err = canRecall(ctx, ids)
			if err != nil {
				return fmt.Errorf("verificando referências do diário: %w", err)
			}
			loadedReferences = true
		}
		if !verified[id] {
			return fmt.Errorf("message_id %q não é acessível neste agente ou branch", id)
		}
		return nil
	}
	preview := func(content, id string, cap int) (string, error) {
		if promptTokens(content) > cap {
			if err := checkReference(id); err != nil {
				return "", err
			}
		}
		return promptMessagePreview(content, id, cap, journalAvailable)
	}
	// One-shot callers may have no journal at all. Keep their complete request
	// when it fits; otherwise report the overflow instead of inventing an ID.
	if !journalAvailable {
		total := promptTokens(query)
		for _, message := range history {
			total += promptTokens(message.Content)
		}
		if total <= budget {
			messages := make([]ChatMessage, 0, len(history)+1)
			for _, message := range history {
				messages = append(messages, ChatMessage{Role: string(message.Role), Content: message.Content})
			}
			return append(messages, ChatMessage{Role: "user", Content: query}), nil
		}
	}
	queryCap := budget - max(256, budget/5)
	visibleQuery, err := preview(query, currentID, queryCap)
	if err != nil {
		return nil, fmt.Errorf("mensagem atual: %w", err)
	}

	// Keep the common case byte-for-byte intact, including the compaction
	// marker and summary. This avoids changing short conversations.
	used := promptTokens(visibleQuery)
	allFit := true
	for _, message := range history {
		cost := promptTokens(message.Content)
		used += cost
		if cost > maxPromptHistoryTurnTokens {
			allFit = false
		}
	}
	if allFit && used <= budget {
		messages := make([]ChatMessage, 0, len(history)+1)
		for _, message := range history {
			messages = append(messages, ChatMessage{Role: string(message.Role), Content: message.Content})
		}
		return append(messages, ChatMessage{Role: "user", Content: visibleQuery}), nil
	}

	remaining := budget - promptTokens(visibleQuery) - promptWindowNoticeTokens
	selected := make([]string, len(history))
	keep := make([]bool, len(history))
	// A saved compaction summary is the bridge to older work. Give it a
	// reserved slot before filling the rest from newest to oldest.
	for i, message := range history {
		if !conversation.IsSyntheticMessage(message) || !strings.Contains(message.Content, "The summary below") {
			continue
		}
		if remaining >= 512 {
			cap := min(maxPromptHistoryTurnTokens, max(512, remaining/3))
			visible, err := preview(message.Content, message.ID, cap)
			if err != nil {
				return nil, fmt.Errorf("resumo da conversa: %w", err)
			}
			selected[i], keep[i] = visible, true
			remaining -= promptTokens(visible)
		}
		break
	}
	for i := len(history) - 1; i >= 0 && remaining >= 256; i-- {
		if keep[i] {
			continue
		}
		message := history[i]
		cap := min(maxPromptHistoryTurnTokens, remaining)
		visible, err := preview(message.Content, message.ID, cap)
		if err != nil {
			return nil, fmt.Errorf("histórico %d: %w", i+1, err)
		}
		cost := promptTokens(visible)
		if cost > remaining {
			break
		}
		selected[i], keep[i] = visible, true
		remaining -= cost
	}

	omitted := 0
	var omittedIDs []string
	for i, message := range history {
		if keep[i] {
			continue
		}
		// The compaction boundary is metadata; the summary itself is saved and
		// recoverable by ID. All other omissions must have a journal reference.
		if message.Content == "[compact_boundary] Conversation compacted" {
			continue
		}
		if err := checkReference(message.ID); err != nil {
			return nil, fmt.Errorf("histórico excede a janela: %w", err)
		}
		omitted++
		if len(omittedIDs) < 8 {
			omittedIDs = append(omittedIDs, message.ID)
		}
	}
	messages := make([]ChatMessage, 0, len(history)+2)
	if omitted > 0 {
		messages = append(messages, ChatMessage{Role: "system", Content: fmt.Sprintf(
			"%d mensagens anteriores foram retiradas apenas desta janela de contexto. Elas permanecem no histórico desta conversa. Use history_search para localizar mensagens ou history_recall com o message_id para ler o texto exato. IDs iniciais: %s.",
			omitted, strings.Join(omittedIDs, ", "))})
	}
	for i, message := range history {
		if keep[i] {
			messages = append(messages, ChatMessage{Role: string(message.Role), Content: selected[i]})
		}
	}
	return append(messages, ChatMessage{Role: "user", Content: visibleQuery}), nil
}

func promptTokens(content string) int {
	// Same conservative byte estimate used by compaction, plus message framing.
	return (len(content)+2)/3 + 6
}

func promptMessagePreview(content, messageID string, capTokens int, journalAvailable bool) (string, error) {
	if promptTokens(content) <= capTokens {
		return content, nil
	}
	if !journalAvailable || messageID == "" {
		return "", fmt.Errorf("texto longo sem ID no diário; não é seguro omitir partes")
	}
	if capTokens < 256 {
		return "", fmt.Errorf("janela insuficiente para uma prévia recuperável")
	}
	header := fmt.Sprintf("[Prévia de mensagem longa; texto integral no diário: history_recall({\"message_id\":%q,\"start\":0}). Leia as partes necessárias antes de agir.]\n", messageID)
	separator := "\n[... trecho intermediário omitido apenas do prompt ...]\n"
	remaining := capTokens - promptTokens(header+separator) - 32
	if remaining < 96 {
		return "", fmt.Errorf("ID da mensagem deixa pouco espaço para prévia")
	}
	// Two UTF-8 bytes per remaining estimated token leaves room for provider
	// framing and avoids splitting multi-byte runes at either boundary.
	maxBytes := remaining * 2
	headBytes := maxBytes * 3 / 5
	tailBytes := maxBytes - headBytes
	head := content[:min(headBytes, len(content))]
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	tail := content[max(0, len(content)-tailBytes):]
	for len(tail) > 0 && !utf8.ValidString(tail) {
		tail = tail[1:]
	}
	preview := header + head + separator + tail
	if !utf8.ValidString(preview) || promptTokens(preview) > capTokens {
		return "", fmt.Errorf("prévia não cabe no orçamento de contexto")
	}
	return preview, nil
}
