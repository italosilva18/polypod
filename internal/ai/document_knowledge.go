package ai

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/costa/polypod/internal/document"
	"github.com/costa/polypod/internal/whatsappgateway"
)

const (
	autoDocumentHits          = 4
	autoDocumentContextRunes  = 3600
	autoDocumentChunkRunes    = 850
	autoDocumentSearchTimeout = 5 * time.Second
)

// DocumentKnowledgeSearcher retrieves bounded, cited chunks before an AI turn.
// Scope selection is derived from the authenticated turn context, never from
// document text or a model-supplied scope.
type DocumentKnowledgeSearcher struct {
	index     *document.Index
	ownerChat string
}

func NewDocumentKnowledgeSearcher(index *document.Index, ownerChat string) *DocumentKnowledgeSearcher {
	return &DocumentKnowledgeSearcher{index: index, ownerChat: ownerChat}
}

func (s *DocumentKnowledgeSearcher) Search(ctx context.Context, query, channel, userID string) ([]string, error) {
	if s == nil || s.index == nil {
		return nil, nil
	}
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) < 3 {
		return nil, nil
	}
	scopes := s.scopes(ctx, channel, userID)
	if len(scopes) == 0 {
		return nil, nil
	}
	searchCtx, cancel := context.WithTimeout(ctx, autoDocumentSearchTimeout)
	defer cancel()
	// The document index enforces a 1000-byte limit, so truncate at a UTF-8
	// boundary while keeping the leading task description.
	if len(query) > 1000 {
		query = query[:1000]
		for !utf8.ValidString(query) {
			query = query[:len(query)-1]
		}
	}
	available := false
	for _, scope := range scopes {
		status, err := s.index.Status(searchCtx, scope)
		if err != nil {
			return nil, err
		}
		available = available || status.Chunks > 0
	}
	if !available {
		return nil, nil
	}
	hits, err := s.index.Search(searchCtx, query, scopes, autoDocumentHits)
	if err != nil {
		return nil, err
	}
	fragments := make([]string, 0, len(hits))
	remaining := autoDocumentContextRunes
	for _, hit := range hits {
		if remaining < 100 {
			break
		}
		fragment := formatAutoDocumentHit(hit, remaining)
		if fragment == "" {
			continue
		}
		fragments = append(fragments, fragment)
		remaining -= utf8.RuneCountInString(fragment)
	}
	return fragments, nil
}

func (s *DocumentKnowledgeSearcher) scopes(ctx context.Context, channel, userID string) []string {
	// A WhatsApp request needs its validated inbound identity. The caller's
	// Channel and UserID fields alone never grant access to an owner document.
	if source, ok := whatsappgateway.InboundFromContext(ctx); ok {
		if channel != whatsappgateway.Channel || userID != source.ChatID {
			return nil
		}
		if s.ownerChat != "" && source.ChatID == s.ownerChat {
			return []string{source.ChatID, document.LocalScope}
		}
		return []string{source.ChatID}
	}
	if remoteScope, ok := document.RemoteScopeFromContext(ctx); ok {
		if channel == whatsappgateway.Channel || channel == "" || userID == "" ||
			remoteScope != channel+":"+userID {
			return nil
		}
		return []string{remoteScope}
	}
	if channel == whatsappgateway.Channel {
		return nil
	}
	// Only direct local entry points inherit local documents. Queued tasks and
	// routines can be created from a remote chat, but their execution currently
	// carries no authenticated creator scope, so they must not inherit local
	// documents here.
	if (channel == "cli" && userID == "local") ||
		(channel == "system" && userID == "owner") {
		return []string{document.LocalScope}
	}
	return nil
}

func formatAutoDocumentHit(hit document.SearchHit, remaining int) string {
	text := strings.TrimSpace(hit.Text)
	if text == "" || hit.Citation == "" {
		return ""
	}
	scope := "conversa"
	if hit.Scope == document.LocalScope {
		scope = "local"
	}
	header := fmt.Sprintf("Fonte %s (escopo=%s, source_id=%q, trecho=%d):\n",
		hit.Citation, scope, hit.SourceID, hit.Ordinal)
	budget := min(autoDocumentChunkRunes, remaining-utf8.RuneCountInString(header))
	if budget < 40 {
		return ""
	}
	if utf8.RuneCountInString(text) > budget {
		text = string([]rune(text)[:budget-1]) + "…"
	}
	return header + text
}
