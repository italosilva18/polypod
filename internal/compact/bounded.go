package compact

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/costa/polypod/internal/conversation"
)

// summarizeBounded keeps every summarizer call below a conservative fraction
// of the configured context window. It uses successive summaries only when a
// single call would exceed that limit. Exact original messages remain in the
// conversation journal; giant individual turns name their durable IDs.
func (s *CompactService) summarizeBounded(ctx context.Context, systemPrompt string, messages []conversation.Message, userPrompt string) (string, error) {
	// The wrapper prompt and instructions consume context too. Leave a fixed
	// margin for provider formatting around the batches.
	limit := min(24_000, s.EffectiveContextWindow()/4) - (len(systemPrompt)+len(userPrompt))/3 - 512
	if limit < 1024 {
		return "", ErrPromptTooLong
	}
	if EstimateTokens(messages) <= limit {
		return s.summarize(ctx, systemPrompt, messages, userPrompt)
	}
	input, references, err := s.boundedSummaryInput(ctx, messages, limit)
	if err != nil {
		return "", err
	}
	stage := input
	const maxPasses = 8
	for pass := 0; pass < maxPasses; pass++ {
		batches := summaryBatches(stage, limit)
		next := make([]conversation.Message, 0, len(batches))
		for i, batch := range batches {
			prompt := userPrompt
			if len(batches) > 1 || pass > 0 {
				prompt += fmt.Sprintf("\n\nEste é o lote %d de %d. Resuma as decisões, tarefas pendentes e referências de message_id. Preserve IDs citados para consulta posterior com history_recall. Seja conciso.", i+1, len(batches))
			}
			if pass > 0 && len(batches) == 1 {
				prompt += "\n\nUna os resumos parciais em um resumo final coerente; preserve referências de message_id."
			}
			summary, err := s.summarize(ctx, systemPrompt, batch, prompt)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(summary) == "" {
				return "", ErrNoSummaryGenerated
			}
			if EstimateTokens([]conversation.Message{{Content: summary}}) > limit {
				return "", fmt.Errorf("%w: resposta de resumo maior que o lote permitido", ErrPromptTooLong)
			}
			next = append(next, conversation.Message{Role: conversation.RoleUser, Content: summary})
		}
		if len(next) == 1 {
			return appendSummaryReferences(next[0].Content, references), nil
		}
		if EstimateTokens(next) >= EstimateTokens(stage) && pass > 0 {
			return "", fmt.Errorf("%w: resumos parciais não reduziram o contexto", ErrPromptTooLong)
		}
		stage = next
	}
	return "", fmt.Errorf("%w: limite de passes da compactação", ErrPromptTooLong)
}

func (s *CompactService) boundedSummaryInput(ctx context.Context, messages []conversation.Message, limit int) ([]conversation.Message, []string, error) {
	scope, scoped := conversation.JournalScopeFromContext(ctx)
	result := make([]conversation.Message, len(messages))
	var references []string
	for _, message := range messages {
		if EstimateTokens([]conversation.Message{message}) <= limit {
			continue
		}
		if !scoped || s.journal == nil || message.ID == "" {
			return nil, nil, fmt.Errorf("%w: mensagem extensa sem referência no diário", ErrPromptTooLong)
		}
		references = append(references, message.ID)
	}
	var accessible map[string]bool
	if len(references) > 0 {
		var err error
		accessible, err = s.journal.CanReadJournalScopedBatch(ctx, scope, references)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: falha ao verificar referências: %v", ErrPromptTooLong, err)
		}
	}
	for i, message := range messages {
		result[i] = message
		if EstimateTokens([]conversation.Message{message}) <= limit {
			continue
		}
		if !accessible[message.ID] {
			return nil, nil, fmt.Errorf("%w: mensagem extensa fora do escopo do agente ou branch", ErrPromptTooLong)
		}
		result[i].Content = summaryMessagePreview(message.Content, message.ID, limit)
		if EstimateTokens([]conversation.Message{result[i]}) > limit {
			return nil, nil, fmt.Errorf("%w: prévia ainda excede o lote", ErrPromptTooLong)
		}
	}
	return result, references, nil
}

func summaryMessagePreview(content, id string, limit int) string {
	header := fmt.Sprintf("[Mensagem longa; texto integral salvo como message_id=%s, recuperável por history_recall. Este é apenas o começo e o fim:]\n", id)
	separator := "\n[... trecho intermediário omitido desta chamada de resumo ...]\n"
	maxBytes := (limit-128)*2 - len(header) - len(separator)
	if maxBytes < 96 {
		maxBytes = 96
	}
	head := content[:min(len(content), maxBytes*3/5)]
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	tail := content[max(0, len(content)-maxBytes*2/5):]
	for len(tail) > 0 && !utf8.ValidString(tail) {
		tail = tail[1:]
	}
	return header + head + separator + tail
}

func summaryBatches(messages []conversation.Message, limit int) [][]conversation.Message {
	var batches [][]conversation.Message
	var batch []conversation.Message
	batchTokens := 0
	for _, message := range messages {
		cost := EstimateTokens([]conversation.Message{message})
		if len(batch) > 0 && batchTokens+cost > limit {
			batches = append(batches, batch)
			batch = nil
			batchTokens = 0
		}
		batch = append(batch, message)
		batchTokens += cost
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches
}

func appendSummaryReferences(summary string, references []string) string {
	if len(references) == 0 {
		return summary
	}
	return fmt.Sprintf("%s\n\nTexto integral de %d mensagens longas resumidas: use history_recall com estes message_id: %s.",
		summary, len(references), strings.Join(references, ", "))
}
