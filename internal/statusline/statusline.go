package statusline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Data is the JSON payload sent to the status line script.
type Data struct {
	Model         string  `json:"model"`
	Provider      string  `json:"provider"`
	SessionID     string  `json:"session_id"`
	ContextUsed   float64 `json:"context_used_pct"`
	ContextTokens int     `json:"context_tokens"`
	ContextMax    int     `json:"context_max"`
	TotalCost     float64 `json:"total_cost_usd"`
	SessionTokens int     `json:"session_tokens"`
	LinesAdded    int     `json:"lines_added"`
	LinesRemoved  int     `json:"lines_removed"`
	Requests      int     `json:"requests"`
	Mode          string  `json:"mode"`
	Version       string  `json:"version"`
	AgentName     string  `json:"agent_name"`
	ActiveTools   int     `json:"active_tools"`
	MCPServers    int     `json:"mcp_servers"`
}

// Config for the status line.
type Config struct {
	Type    string `yaml:"type"`
	Command string `yaml:"command"`
}

// Render executes the status line script or falls back to builtin.
func Render(cfg Config, data Data) string {
	if cfg.Type == "builtin" || cfg.Command == "" {
		return renderBuiltin(data)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	payload, _ := json.Marshal(data)
	cmd := exec.CommandContext(ctx, "bash", "-c", cfg.Command)
	cmd.Stdin = bytes.NewReader(payload)

	out, err := cmd.Output()
	if err != nil {
		return renderBuiltin(data)
	}
	return strings.TrimRight(string(out), "\n")
}

func renderBuiltin(d Data) string {
	contextColor := "32"
	if d.ContextUsed > 80 {
		contextColor = "31"
	} else if d.ContextUsed > 50 {
		contextColor = "33"
	}

	parts := []string{
		d.Model,
		fmt.Sprintf("\033[%sm%.0f%%\033[0m", contextColor, d.ContextUsed),
		formatTokens(d.SessionTokens),
	}
	if d.TotalCost > 0 {
		parts = append(parts, formatCost(d.TotalCost))
	}
	if d.Mode != "" && d.Mode != "edit" {
		parts = append(parts, "["+d.Mode+"]")
	}
	return strings.Join(parts, " | ")
}

func formatTokens(tokens int) string {
	if tokens >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1000000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fK", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

func formatCost(cost float64) string {
	if cost < 0.01 {
		return fmt.Sprintf("$%.4f", cost)
	}
	return fmt.Sprintf("$%.2f", cost)
}
