package memory

import (
	"context"
	"fmt"
	"strings"
)

const maxAutoInjectMemories = 5

// AutoInject searches for memories relevant to the query and formats them for prompt injection.
// Returns empty string if no relevant memories found or store is nil.
func AutoInject(ctx context.Context, store Store, query string) string {
	if store == nil || query == "" {
		return ""
	}

	memories, err := store.SearchRelevant(ctx, query, maxAutoInjectMemories, 0.2)
	if err != nil {
		return ""
	}

	if len(memories) == 0 {
		return ""
	}

	var lines []string
	for _, m := range memories {
		lines = append(lines, fmt.Sprintf("- [%s]: %s", m.Topic, m.Content))
	}

	return "MEMORIAS RELEVANTES:\n" + strings.Join(lines, "\n")
}
