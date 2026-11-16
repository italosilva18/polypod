// Package services — Core Services extracted from Claude Code
// Sources: src/services/analytics/, src/services/tokenEstimation.ts, etc.

package services

import (
	"fmt"
	"strings"
	"time"
)

// TokenUsage holds token usage data
type TokenUsage struct {
	InputTokens   int64     `json:"input_tokens"`
	OutputTokens  int64     `json:"output_tokens"`
	TotalTokens   int64     `json:"total_tokens"`
	CostUSD       float64   `json:"cost_usd"`
	Model         string    `json:"model"`
	Timestamp     time.Time `json:"timestamp"`
	SessionID     string    `json:"session_id"`
}

// UsageStats holds usage statistics
type UsageStats struct {
	TotalSessions     int         `json:"total_sessions"`
	TotalTokens       int64       `json:"total_tokens"`
	TotalCost         float64     `json:"total_cost"`
	AvgTokensPerSession int64     `json:"avg_tokens_per_session"`
	MostUsedModel     string      `json:"most_used_model"`
	Period            string      `json:"period"` // daily, weekly, monthly
}

// TokenEstimator estimates token usage
type TokenEstimator struct {
	modelRates map[string]float64 // cost per 1K tokens
}

// NewTokenEstimator creates new token estimator
func NewTokenEstimator() *TokenEstimator {
	return &TokenEstimator{
		modelRates: map[string]float64{
			"gpt-4o":           0.015,
			"gpt-4":            0.03,
			"claude-sonnet-4":  0.003,
			"claude-opus-4":    0.015,
			"gemini-2.5-pro":   0.0025,
		},
	}
}

// EstimateTokens estimates tokens for text
func (e *TokenEstimator) EstimateTokens(text string) int64 {
	// Simple estimation: ~4 chars per token
	chars := len(strings.TrimSpace(text))
	return int64(chars / 4)
}

// EstimateCost estimates cost for tokens
func (e *TokenEstimator) EstimateCost(tokens int64, model string) float64 {
	rate, exists := e.modelRates[model]
	if !exists {
		rate = 0.01 // default rate
	}
	return float64(tokens) * rate / 1000
}

// EstimateMessageCost estimates cost for a message
func (e *TokenEstimator) EstimateMessageCost(text, model string) float64 {
	tokens := e.EstimateTokens(text)
	return e.EstimateCost(tokens, model)
}

// UsageTracker tracks usage
type UsageTracker struct {
	sessions map[string]*SessionUsage
}

// SessionUsage holds session usage data
type SessionUsage struct {
	ID          string
	InputTokens int64
	OutputTokens int64
	StartTime   time.Time
	EndTime     time.Time
	Model       string
}

// NewUsageTracker creates new usage tracker
func NewUsageTracker() *UsageTracker {
	return &UsageTracker{
		sessions: make(map[string]*SessionUsage),
	}
}

// StartSession starts tracking a session
func (t *UsageTracker) StartSession(sessionID, model string) {
	t.sessions[sessionID] = &SessionUsage{
		ID:        sessionID,
		StartTime: time.Now(),
		Model:     model,
	}
}

// AddTokens adds token usage to session
func (t *UsageTracker) AddTokens(sessionID string, input, output int64) {
	session, exists := t.sessions[sessionID]
	if !exists {
		return
	}
	session.InputTokens += input
	session.OutputTokens += output
}

// EndSession ends a session
func (t *UsageTracker) EndSession(sessionID string) {
	session, exists := t.sessions[sessionID]
	if !exists {
		return
	}
	session.EndTime = time.Now()
}

// GetStats returns usage statistics
func (t *UsageTracker) GetStats() *UsageStats {
	stats := &UsageStats{
		TotalSessions: len(t.sessions),
	}
	
	modelCounts := make(map[string]int)
	
	for _, session := range t.sessions {
		stats.TotalTokens += session.InputTokens + session.OutputTokens
		modelCounts[session.Model]++
	}
	
	// Find most used model
	maxCount := 0
	for model, count := range modelCounts {
		if count > maxCount {
			maxCount = count
			stats.MostUsedModel = model
		}
	}
	
	if stats.TotalSessions > 0 {
		stats.AvgTokensPerSession = stats.TotalTokens / int64(stats.TotalSessions)
	}
	
	return stats
}

// Notifier sends notifications
type Notifier struct {
	channels []NotificationChannel
}

// NotificationChannel represents notification channel
type NotificationChannel string

const (
	NotifyTerminal NotificationChannel = "terminal"
	NotifySound    NotificationChannel = "sound"
	NotifyDesktop  NotificationChannel = "desktop"
)

// Notification holds notification data
type Notification struct {
	Title   string
	Message string
	Level   string // info, warning, error, success
}

// NewNotifier creates new notifier
func NewNotifier() *Notifier {
	return &Notifier{
		channels: []NotificationChannel{NotifyTerminal},
	}
}

// Send sends a notification
func (n *Notifier) Send(notif *Notification) {
	// Terminal notification (simplified)
	var icon string
	switch notif.Level {
	case "info":
		icon = "ℹ️"
	case "warning":
		icon = "⚠️"
	case "error":
		icon = "❌"
	case "success":
		icon = "✅"
	default:
		icon = "📢"
	}
	
	fmt.Printf("%s **%s**: %s\n", icon, notif.Title, notif.Message)
}

// NotifyInfo sends info notification
func (n *Notifier) NotifyInfo(title, message string) {
	n.Send(&Notification{Title: title, Message: message, Level: "info"})
}

// NotifyWarning sends warning notification
func (n *Notifier) NotifyWarning(title, message string) {
	n.Send(&Notification{Title: title, Message: message, Level: "warning"})
}

// NotifyError sends error notification
func (n *Notifier) NotifyError(title, message string) {
	n.Send(&Notification{Title: title, Message: message, Level: "error"})
}

// NotifySuccess sends success notification
func (n *Notifier) NotifySuccess(title, message string) {
	n.Send(&Notification{Title: title, Message: message, Level: "success"})
}

// FormatUsageStats formats usage stats for display
func FormatUsageStats(stats *UsageStats) string {
	var sb strings.Builder
	
	sb.WriteString("📊 **Usage Statistics**:\n\n")
	sb.WriteString(fmt.Sprintf("Total Sessions: %d\n", stats.TotalSessions))
	sb.WriteString(fmt.Sprintf("Total Tokens: %s\n", formatNumber(stats.TotalTokens)))
	sb.WriteString(fmt.Sprintf("Avg Tokens/Session: %s\n", formatNumber(stats.AvgTokensPerSession)))
	sb.WriteString(fmt.Sprintf("Most Used Model: %s\n", stats.MostUsedModel))
	sb.WriteString(fmt.Sprintf("Period: %s\n", stats.Period))
	
	return sb.String()
}

func formatNumber(n int64) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}
