package arena

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/provider"
)

// Contender represents a model in the arena.
type Contender struct {
	Provider provider.Provider
	Model    string
	Name     string // display name
}

// ArenaResult holds the results from all contenders.
type ArenaResult struct {
	Prompt  string
	Results []ContenderResult
}

// ContenderResult is one model's response.
type ContenderResult struct {
	Name             string
	Model            string
	Content          string
	PromptTokens     int
	CompletionTokens int
	Duration         time.Duration
	Error            error
}

// Arena runs the same prompt against multiple models in parallel.
type Arena struct {
	contenders []Contender
	logger     *slog.Logger
}

// New creates an arena with the given contenders.
func New(contenders []Contender, logger *slog.Logger) *Arena {
	return &Arena{contenders: contenders, logger: logger}
}

// Run sends the prompt to all contenders in parallel and returns results.
func (a *Arena) Run(ctx context.Context, prompt string, maxTokens int, temperature float32) *ArenaResult {
	if maxTokens == 0 {
		maxTokens = 2048
	}
	if temperature == 0 {
		temperature = 0.3
	}

	result := &ArenaResult{Prompt: prompt}
	results := make([]ContenderResult, len(a.contenders))
	var wg sync.WaitGroup

	for i, c := range a.contenders {
		wg.Add(1)
		go func(idx int, contender Contender) {
			defer wg.Done()

			start := time.Now()
			req := provider.ChatRequest{
				Model:       contender.Model,
				Messages:    []provider.Message{{Role: "user", Content: prompt}},
				MaxTokens:   maxTokens,
				Temperature: temperature,
			}

			resp, err := contender.Provider.Chat(ctx, req)
			duration := time.Since(start)

			r := ContenderResult{
				Name:     contender.Name,
				Model:    contender.Model,
				Duration: duration,
			}
			if err != nil {
				r.Error = err
			} else {
				r.Content = resp.Content
				r.PromptTokens = resp.PromptTokens
				r.CompletionTokens = resp.CompletionTokens
			}

			results[idx] = r
		}(i, c)
	}

	wg.Wait()
	result.Results = results
	return result
}

// FormatResults renders arena results for display.
func FormatResults(r *ArenaResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Arena: %d modelo(s)\n\n", len(r.Results))
	fmt.Fprintf(&sb, "**Prompt**: %s\n\n", truncate(r.Prompt, 100))
	sb.WriteString("---\n\n")

	for i, cr := range r.Results {
		fmt.Fprintf(&sb, "### %d. %s (%s)\n\n", i+1, cr.Name, cr.Model)

		if cr.Error != nil {
			fmt.Fprintf(&sb, "**ERRO**: %v\n\n", cr.Error)
			continue
		}

		// Response
		content := cr.Content
		if len(content) > 1000 {
			content = content[:1000] + "\n... [truncado]"
		}
		fmt.Fprintf(&sb, "%s\n\n", content)

		// Stats
		fmt.Fprintf(&sb, "*Tokens*: %d in + %d out | *Tempo*: %s\n\n",
			cr.PromptTokens, cr.CompletionTokens, cr.Duration.Round(time.Millisecond))

		sb.WriteString("---\n\n")
	}

	// Comparison summary
	sb.WriteString("### Resumo\n\n")
	sb.WriteString("| Modelo | Tokens | Tempo | Status |\n")
	sb.WriteString("|--------|--------|-------|--------|\n")
	for _, cr := range r.Results {
		status := "OK"
		if cr.Error != nil {
			status = "ERRO"
		}
		fmt.Fprintf(&sb, "| %s | %d | %s | %s |\n",
			cr.Name, cr.PromptTokens+cr.CompletionTokens,
			cr.Duration.Round(time.Millisecond), status)
	}

	return sb.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
