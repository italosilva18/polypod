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
	"github.com/costa/polypod/internal/session"
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
	Prompt        string
	OutputFormat  OutputFormat
	SessionID     string // resume a specific session
	Continue      bool   // continue last session
	SystemPrompt  string
	TurnID        string
	AfterSequence uint64
	EventJournal  *session.EventJournal
	Stdout        io.Writer
}

// Result is the structured output of a headless run.
type Result struct {
	Content       string  `json:"content"`
	SessionID     string  `json:"session_id"`
	EventVersion  int     `json:"event_version,omitempty"`
	LastSequence  uint64  `json:"last_sequence,omitempty"`
	Provider      string  `json:"provider,omitempty"`
	Model         string  `json:"model"`
	CostUSD       float64 `json:"cost_usd"`
	CostMeasured  bool    `json:"cost_measured"`
	Steps         int     `json:"steps"`
	ToolFailures  int     `json:"tool_failures"`
	DurationMS    int64   `json:"duration_ms"`
	Tokens        struct {
		Prompt     int `json:"prompt"`
		Completion int `json:"completion"`
		Total      int `json:"total"`
	} `json:"tokens"`
}

// ReplayEvents writes persisted session events strictly after cursor in the
// versioned stream-json format and returns the highest sequence emitted.
func ReplayEvents(out io.Writer, journal *session.EventJournal, sessionID string, after uint64) (uint64, error) {
	if journal == nil || strings.TrimSpace(sessionID) == "" {
		return after, nil
	}
	if out == nil {
		out = os.Stdout
	}
	encoder := json.NewEncoder(out)
	cursor := after
	for {
		events, err := journal.ReadAfter(sessionID, cursor, 1000)
		if err != nil {
			return cursor, err
		}
		if len(events) == 0 {
			return cursor, nil
		}
		for _, ev := range events {
			payload := map[string]any{
				"version":  ev.Version,
				"sequence": ev.Sequence,
				"type":     "session_event",
				"update":   ev.Update,
			}
			if ev.SessionID != "" {
				payload["session_id"] = ev.SessionID
			}
			if ev.TurnID != "" {
				payload["turn_id"] = ev.TurnID
			}
			if err := encoder.Encode(payload); err != nil {
				return cursor, err
			}
			cursor = ev.Sequence
		}
	}
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

	out := cfg.Stdout
	if out == nil {
		out = os.Stdout
	}
	if cfg.OutputFormat == FormatStreamJSON && cfg.EventJournal != nil && cfg.SessionID != "" && cfg.AfterSequence > 0 && strings.TrimSpace(prompt) == "" {
		_, err := ReplayEvents(out, cfg.EventJournal, cfg.SessionID, cfg.AfterSequence)
		return err
	}

	if prompt == "" {
		return fmt.Errorf("nenhum prompt fornecido (use -p 'prompt' ou pipe via stdin)")
	}

	start := time.Now()

	metadata := map[string]string{}
	if cfg.SessionID != "" {
		metadata["polypod_session_id"] = cfg.SessionID
	}
	if cfg.TurnID != "" {
		metadata["polypod_turn_id"] = cfg.TurnID
	}
	var msgMeta map[string]string
	if len(metadata) > 0 {
		msgMeta = metadata
	}

	msg := adapter.InMessage{
		Channel:   "headless",
		UserID:    "cli",
		Text:      prompt,
		Timestamp: time.Now(),
		Metadata:  msgMeta,
	}

	switch cfg.OutputFormat {
	case FormatJSON:
		resp, err := handler(ctx, msg)
		if err != nil {
			return err
		}
		result := Result{
			Content:      resp.Text,
			SessionID:    cfg.SessionID,
			EventVersion: resp.EventVersion,
			LastSequence: resp.EventSequence,
			DurationMS:   time.Since(start).Milliseconds(),
		}
		if resp.Usage != nil {
			result.Provider = resp.Usage.Provider
			result.Model = resp.Usage.Model
			result.Tokens.Prompt = resp.Usage.PromptTokens
			result.Tokens.Completion = resp.Usage.CompletionTokens
			result.Tokens.Total = resp.Usage.TotalTokens
			result.Steps = resp.Usage.Steps
			result.ToolFailures = resp.Usage.ToolFailures
			if resp.Usage.CostUSD != nil {
				result.CostUSD = *resp.Usage.CostUSD
				result.CostMeasured = true
			}
		}
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return fmt.Errorf("escrevendo resultado JSON: %w", err)
		}

	case FormatStreamJSON:
		var sequence uint64
		if cfg.EventJournal != nil && cfg.SessionID != "" && cfg.AfterSequence > 0 {
			replayed, err := ReplayEvents(out, cfg.EventJournal, cfg.SessionID, cfg.AfterSequence)
			if err != nil {
				return fmt.Errorf("reproduzindo eventos da sessão: %w", err)
			}
			sequence = replayed
		}
		chunks := make(chan adapter.StreamChunk, 100)
		go streamHandler(ctx, msg, chunks)

		var content strings.Builder
		encoder := json.NewEncoder(out)
		emit := func(chunk adapter.StreamChunk, event map[string]any) error {
			version := session.EventProtocolVersion
			if chunk.EventVersion > 0 {
				version = chunk.EventVersion
			}
			if chunk.EventSequence > sequence {
				sequence = chunk.EventSequence
			} else {
				sequence++
			}
			event["version"] = version
			event["sequence"] = sequence
			if cfg.SessionID != "" {
				event["session_id"] = cfg.SessionID
			}
			return encoder.Encode(event)
		}
		for chunk := range chunks {
			if chunk.Error != nil {
				if err := emit(chunk, map[string]any{"type": "error", "error": chunk.Error.Error()}); err != nil {
					return fmt.Errorf("escrevendo evento de erro: %w", err)
				}
				return chunk.Error
			}
			if chunk.ToolEvent != nil {
				if err := emit(chunk, toolStreamEvent(chunk.ToolEvent)); err != nil {
					return fmt.Errorf("escrevendo evento de ferramenta: %w", err)
				}
			}
			if chunk.Delta != "" {
				content.WriteString(chunk.Delta)
				if err := emit(chunk, map[string]any{
					"type":  "delta",
					"delta": chunk.Delta,
				}); err != nil {
					return fmt.Errorf("escrevendo delta: %w", err)
				}
			}
			if chunk.Done {
				event := map[string]any{
					"type":        "done",
					"content":     content.String(),
					"duration_ms": time.Since(start).Milliseconds(),
				}
				if chunk.Usage != nil {
					event["usage"] = chunk.Usage
				}
				if err := emit(chunk, event); err != nil {
					return fmt.Errorf("escrevendo resultado final: %w", err)
				}
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
				fmt.Fprint(out, chunk.Delta)
			}
			if chunk.Done {
				fmt.Fprintln(out)
			}
		}
	}

	return nil
}

func toolStreamEvent(info *adapter.ToolEventInfo) map[string]any {
	status := "progress"
	switch info.Type {
	case adapter.ToolEventStart:
		status = "start"
	case adapter.ToolEventComplete:
		status = "complete"
	case adapter.ToolEventError:
		status = "error"
	}
	event := map[string]any{
		"type":       "tool",
		"tool_name":  info.ToolName,
		"tool_id":    info.ToolID,
		"status":     status,
		"elapsed_ms": info.Elapsed.Milliseconds(),
	}
	if info.Error != "" {
		event["error"] = info.Error
	}
	return event
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
