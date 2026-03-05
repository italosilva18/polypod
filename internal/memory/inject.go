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

	memories, err := store.Search(ctx, query)
	if err != nil {
		return ""
	}

	if len(memories) == 0 {
		return ""
	}

	// Limit to max memories
	if len(memories) > maxAutoInjectMemories {
		memories = memories[:maxAutoInjectMemories]
	}

	var lines []string
	for _, m := range memories {
		lines = append(lines, fmt.Sprintf("- [%s]: %s", m.Topic, m.Content))
	}

	return "MEMORIAS RELEVANTES:\n" + strings.Join(lines, "\n")
}
