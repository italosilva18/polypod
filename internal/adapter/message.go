package adapter

import "time"

// InMessage is a unified incoming message from any channel.
type InMessage struct {
	ID        string
	Channel   string
	UserID    string
	UserName  string
	Text      string
	Timestamp time.Time
	Metadata  map[string]string
}

// OutMessage is a unified outgoing message to any channel.
type OutMessage struct {
	Channel       string
	UserID        string
	Text          string
	ReplyTo       string
	Metadata      map[string]string
	Usage         *Usage
	EventVersion  int
	EventSequence uint64
}

// Usage records the measured model usage for one completed agent turn.
// CostUSD is nil when the price cannot be established from the provider.
type Usage struct {
	Provider         string   `json:"provider,omitempty"`
	Model            string   `json:"model,omitempty"`
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	TotalTokens      int      `json:"total_tokens"`
	CostUSD          *float64 `json:"cost_usd,omitempty"`
	Steps            int      `json:"steps"`
	ToolFailures     int      `json:"tool_failures"`
}
