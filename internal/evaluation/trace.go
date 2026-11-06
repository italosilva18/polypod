package evaluation

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type TraceStats struct {
	SHA256              string   `json:"sha256"`
	Entries             int      `json:"entries"`
	ToolCalls           int      `json:"tool_calls"`
	ToolErrors          int      `json:"tool_errors"`
	UsageEntries        int      `json:"usage_entries"`
	InputTokens         int64    `json:"input_tokens"`
	OutputTokens        int64    `json:"output_tokens"`
	CacheReadTokens     int64    `json:"cache_read_tokens"`
	CacheCreationTokens int64    `json:"cache_creation_tokens"`
	SpanMS              *float64 `json:"span_ms"`
}

// ReadTrace reads the session transcript JSONL shape consumed by internal/stats.
// It does not infer task success, recovery or dollar cost from assistant claims.
// All entries, including sidechains, are counted; usage must be final per message.
func ReadTrace(r io.Reader) (TraceStats, error) {
	var stats TraceStats
	hash := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(r, hash))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var first, last time.Time
	timestamps := 0
	for scanner.Scan() {
		var e struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   *struct {
				Content json.RawMessage `json:"content"`
				Usage   *struct {
					Input         int64 `json:"input_tokens"`
					Output        int64 `json:"output_tokens"`
					CacheRead     int64 `json:"cache_read_input_tokens"`
					CacheCreation int64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		stats.Entries++
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return stats, fmt.Errorf("trace linha %d: %w", stats.Entries, err)
		}
		if e.Type == "" {
			return stats, fmt.Errorf("trace linha %d sem type", stats.Entries)
		}
		if e.Timestamp != "" {
			ts, err := time.Parse(time.RFC3339Nano, e.Timestamp)
			if err != nil {
				return stats, fmt.Errorf("trace timestamp: %w", err)
			}
			if timestamps == 0 || ts.Before(first) {
				first = ts
			}
			if timestamps == 0 || ts.After(last) {
				last = ts
			}
			timestamps++
		}
		if e.Message == nil {
			continue
		}
		if u := e.Message.Usage; u != nil {
			if u.Input < 0 || u.Output < 0 || u.CacheRead < 0 || u.CacheCreation < 0 {
				return stats, fmt.Errorf("trace com tokens negativos")
			}
			stats.UsageEntries++
			stats.InputTokens += u.Input
			stats.OutputTokens += u.Output
			stats.CacheReadTokens += u.CacheRead
			stats.CacheCreationTokens += u.CacheCreation
		}
		var blocks []struct {
			Type    string `json:"type"`
			IsError bool   `json:"is_error"`
		}
		if len(e.Message.Content) > 0 && string(e.Message.Content) != "null" {
			if err := json.Unmarshal(e.Message.Content, &blocks); err != nil {
				var plain string
				if err := json.Unmarshal(e.Message.Content, &plain); err != nil {
					return stats, fmt.Errorf("trace content inválido: %w", err)
				}
			}
		}
		for _, b := range blocks {
			if b.Type == "tool_use" {
				stats.ToolCalls++
			}
			if b.Type == "tool_result" && b.IsError {
				stats.ToolErrors++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return stats, fmt.Errorf("ler trace: %w", err)
	}
	if stats.Entries == 0 {
		return stats, fmt.Errorf("trace vazio")
	}
	if timestamps >= 2 {
		ms := float64(last.Sub(first)) / float64(time.Millisecond)
		stats.SpanMS = &ms
	}
	stats.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return stats, nil
}
