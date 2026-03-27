package headless

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/conversation"
)

// OutputFormat controls headless output.
type OutputFormat string

const (
	FormatText       OutputFormat = "text"
	FormatJSON       OutputFormat = "json"
	FormatStreamJSON OutputFormat = "stream-json"
)

// Config for headless execution.
type Config struct {
	Prompt       string
	OutputFormat OutputFormat
	SessionID    string // resume a specific session
	Continue     bool   // continue last session
	SystemPrompt string
}

// Result is the structured output of a headless run.
type Result struct {
	Content   string  `json:"content"`
	SessionID string  `json:"session_id"`
	Model     string  `json:"model"`
	CostUSD   float64 `json:"cost_usd"`
	DurationMS int64  `json:"duration_ms"`
	Tokens    struct {
		Prompt     int `json:"prompt"`
		Completion int `json:"completion"`
		Total      int `json:"total"`
	} `json:"tokens"`
}

// Run executes a single prompt and returns the result.
func Run(ctx context.Context, cfg Config, handler adapter.MessageHandler, streamHandler adapter.StreamHandler, convMgr *conversation.Manager, aiSvc *ai.Service) error {
	prompt := cfg.Prompt

	// Read from stdin if prompt is empty or "-"
	if prompt == "" || prompt == "-" {
		stdinData, err := readStdin()
		if err != nil {
			return fmt.Errorf("lendo stdin: %w", err)
		}
		if cfg.Prompt != "" && cfg.Prompt != "-" {
			prompt = cfg.Prompt + "\n\n" + stdinData
		} else {
			prompt = stdinData
		}
	}

	if prompt == "" {
		return fmt.Errorf("nenhum prompt fornecido (use -p 'prompt' ou pipe via stdin)")
	}

	start := time.Now()

	msg := adapter.InMessage{
		Channel:   "headless",
		UserID:    "cli",
		Text:      prompt,
		Timestamp: time.Now(),
	}

	switch cfg.OutputFormat {
	case FormatJSON:
		resp, err := handler(ctx, msg)
		if err != nil {
			return err
		}
		result := Result{
			Content:    resp.Text,
			SessionID:  cfg.SessionID,
			DurationMS: time.Since(start).Milliseconds(),
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))

	case FormatStreamJSON:
		chunks := make(chan adapter.StreamChunk, 100)
		go streamHandler(ctx, msg, chunks)

		var content strings.Builder
		for chunk := range chunks {
			if chunk.Error != nil {
				return chunk.Error
			}
			if chunk.Delta != "" {
				content.WriteString(chunk.Delta)
				data, _ := json.Marshal(map[string]any{
					"type":  "delta",
					"delta": chunk.Delta,
				})
				fmt.Println(string(data))
			}
			if chunk.Done {
				data, _ := json.Marshal(map[string]any{
					"type":       "done",
					"content":    content.String(),
					"duration_ms": time.Since(start).Milliseconds(),
				})
				fmt.Println(string(data))
			}
		}

	default: // text
		chunks := make(chan adapter.StreamChunk, 100)
		go streamHandler(ctx, msg, chunks)

		for chunk := range chunks {
			if chunk.Error != nil {
				return chunk.Error
			}
			if chunk.Delta != "" {
				fmt.Print(chunk.Delta)
			}
			if chunk.Done {
				fmt.Println()
			}
		}
	}

	return nil
}

func readStdin() (string, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	// Check if stdin has data (pipe or redirect)
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", nil // interactive terminal, no piped data
	}
	reader := bufio.NewReader(os.Stdin)
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
