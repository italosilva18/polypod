package sessionmem

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode"

	"github.com/costa/polypod/internal/conversation"
)

const maxCapturedPerCompact = 8

var credentialLike = regexp.MustCompile(`(?i)(?:\b(?:sk|ghp|gho|github_pat|xoxb|xoxp)-?[a-z0-9_-]{16,}\b|\bAKIA[A-Z0-9]{16}\b|\b[a-z0-9_-]{24,}\.[a-z0-9_-]{16,}\b)`)

var secretWords = []string{
	"senha", "password", "passphrase", "token", "api key", "api_key",
	"segredo", "secret", "chave privada", "private key", "2fa", "otp",
	"cookie", "bearer", "credential", "credencial",
}

type explicitPattern struct {
	prefix   string
	category string
}

// Restrict automatic capture to statements that explicitly commit the user to
// a preference, decision, or stable project fact. Tool output and assistant
// guesses are never scanned. The global dreaming service handles broader
// long-term inference independently.
var explicitPatterns = []explicitPattern{
	{"decidi ", "decision"}, {"eu decidi ", "decision"}, {"decidimos ", "decision"},
	{"adotamos ", "decision"}, {"escolhi ", "decision"},
	{"prefiro ", "context"}, {"eu prefiro ", "context"}, {"minha preferência é ", "context"},
	{"minha preferencia é ", "context"}, {"minha preferencia e ", "context"},
	{"o projeto usa ", "context"}, {"este projeto usa ", "context"},
	{"nosso projeto usa ", "context"}, {"o polypod usa ", "context"},
	{"aprendi que ", "learning"},
}

func isSensitive(value string) bool {
	if credentialLike.MatchString(value) {
		return true
	}
	lower := strings.ToLower(value)
	for _, word := range secretWords {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func cleanEntry(raw Entry) (Entry, bool) {
	raw.Key = strings.TrimSpace(raw.Key)
	raw.Value = strings.TrimSpace(raw.Value)
	raw.Category = strings.ToLower(strings.TrimSpace(raw.Category))
	if raw.Key == "" || len(raw.Key) > 120 || len(raw.Value) < 12 || len(raw.Value) > maxEntryValue {
		return Entry{}, false
	}
	if isSensitive(raw.Key) || isSensitive(raw.Value) || isSensitive(raw.SourceID) {
		return Entry{}, false
	}
	switch raw.Category {
	case "context", "decision", "learning":
	default:
		return Entry{}, false
	}
	return raw, true
}

func messageKey(msg conversation.Message) string {
	if msg.ID != "" {
		return "id:" + msg.ID
	}
	digest := sha256.Sum256([]byte(string(msg.Role) + "\x00" + msg.Content))
	return "content:" + hex.EncodeToString(digest[:])
}

func removedMessages(before, after []conversation.Message) []conversation.Message {
	retained := make(map[string]int, len(after))
	for _, msg := range after {
		retained[messageKey(msg)]++
	}
	removed := make([]conversation.Message, 0, len(before))
	for i := len(before) - 1; i >= 0; i-- {
		msg := before[i]
		key := messageKey(msg)
		if retained[key] > 0 {
			retained[key]--
			continue
		}
		removed = append(removed, msg)
	}
	for i, j := 0, len(removed)-1; i < j; i, j = i+1, j-1 {
		removed[i], removed[j] = removed[j], removed[i]
	}
	return removed
}

func splitStatements(content string) []string {
	var statements []string
	fenced := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced || strings.HasPrefix(trimmed, ">") || strings.Contains(trimmed, "`") {
			continue
		}
		statements = append(statements, strings.FieldsFunc(trimmed, func(r rune) bool {
			return r == ';' || r == '.' || r == '!' || r == '?'
		})...)
	}
	return statements
}

func explicitEntry(text, sourceID string, turn int) (Entry, bool) {
	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimSpace(strings.TrimLeft(text, "-*>• 0123456789)"))
	if len(text) < 24 || len(text) > 280 || strings.Contains(text, "http://") || strings.Contains(text, "https://") {
		return Entry{}, false
	}
	if isSensitive(text) {
		return Entry{}, false
	}
	lower := strings.ToLower(text)
	category := ""
	for _, pattern := range explicitPatterns {
		if strings.HasPrefix(lower, pattern.prefix) {
			category = pattern.category
			break
		}
	}
	if category == "" {
		return Entry{}, false
	}
	// A sentence with mostly punctuation or digits is not a useful memory.
	letters := 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < 18 {
		return Entry{}, false
	}
	digest := sha256.Sum256([]byte(lower))
	entry := Entry{
		Key: "explicit." + hex.EncodeToString(digest[:12]), Value: text,
		Category: category, SourceID: sourceID, TurnNumber: turn,
	}
	return cleanEntry(entry)
}

// CaptureCompaction stores only explicit user statements from turns actually
// removed by a successful compaction. It does not persist the summary itself.
func (s *FileStore) CaptureCompaction(scope string, before, after []conversation.Message) (int, error) {
	removed := removedMessages(before, after)
	entries := make([]Entry, 0, maxCapturedPerCompact)
	seen := make(map[string]bool)
	for i := len(removed) - 1; i >= 0 && len(entries) < maxCapturedPerCompact; i-- {
		msg := removed[i]
		if msg.Role != conversation.RoleUser {
			continue
		}
		statements := splitStatements(msg.Content)
		for j := len(statements) - 1; j >= 0 && len(entries) < maxCapturedPerCompact; j-- {
			entry, ok := explicitEntry(statements[j], msg.ID, i+1)
			if !ok || seen[entry.Key] {
				continue
			}
			seen[entry.Key] = true
			entries = append(entries, entry)
		}
	}
	if len(entries) == 0 {
		return 0, nil
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	if err := s.Merge(scope, entries); err != nil {
		return 0, err
	}
	return len(entries), nil
}
