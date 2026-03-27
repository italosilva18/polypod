package tracking

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// UsageRecord captures a single AI request.
type UsageRecord struct {
	Timestamp        time.Time `json:"timestamp"`
	SessionID        string    `json:"session_id"`
	Channel          string    `json:"channel"`
	UserID           string    `json:"user_id"`
	Model            string    `json:"model"`
	Provider         string    `json:"provider"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	InputCost        float64   `json:"input_cost"`
	OutputCost       float64   `json:"output_cost"`
	TotalCost        float64   `json:"total_cost"`
	ToolCalls        int       `json:"tool_calls"`
	DurationMS       float64   `json:"duration_ms"`
}

// PricingTable maps model → [input_per_1M, output_per_1M] in USD.
var PricingTable = map[string][2]float64{
	"deepseek-chat":           {0.14, 0.28},
	"deepseek-coder":          {0.14, 0.28},
	"gpt-4o":                  {2.50, 10.00},
	"gpt-4o-mini":             {0.15, 0.60},
	"gpt-3.5-turbo":           {0.50, 1.50},
	"claude-3-5-sonnet-latest": {3.00, 15.00},
	"claude-3-haiku-20240307":  {0.25, 1.25},
	"claude-3-opus-latest":     {15.00, 75.00},
	"gemini-1.5-pro":          {1.25, 5.00},
	"gemini-1.5-flash":        {0.075, 0.30},
	"llama3.1":                {0, 0},
	"mistral-large-latest":    {2.00, 6.00},
	"qwen2.5":                 {0, 0},
}

// CalculateCost computes USD cost from token counts and model name.
func CalculateCost(model string, promptTokens, completionTokens int) (inputCost, outputCost, totalCost float64) {
	prices, ok := PricingTable[model]
	if !ok {
		// Try prefix match
		for m, p := range PricingTable {
			if strings.HasPrefix(model, m) {
				prices = p
				ok = true
				break
			}
		}
	}
	if !ok {
		return 0, 0, 0
	}
	inputCost = float64(promptTokens) / 1_000_000 * prices[0]
	outputCost = float64(completionTokens) / 1_000_000 * prices[1]
	totalCost = inputCost + outputCost
	return
}

// Tracker records and queries usage.
type Tracker struct {
	mu       sync.RWMutex
	records  []UsageRecord
	dataFile string
	logger   *slog.Logger
}

// NewTracker creates a tracker.
func NewTracker(dataFile string, logger *slog.Logger) *Tracker {
	t := &Tracker{dataFile: dataFile, logger: logger}
	t.Load()
	return t
}

// Record adds a usage record.
func (t *Tracker) Record(rec UsageRecord) {
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now()
	}
	rec.InputCost, rec.OutputCost, rec.TotalCost = CalculateCost(rec.Model, rec.PromptTokens, rec.CompletionTokens)

	t.mu.Lock()
	t.records = append(t.records, rec)
	t.mu.Unlock()

	t.Save()
}

// Load reads records from file.
func (t *Tracker) Load() error {
	data, err := os.ReadFile(t.dataFile)
	if err != nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return json.Unmarshal(data, &t.records)
}

// Save persists records.
func (t *Tracker) Save() error {
	t.mu.RLock()
	data, err := json.Marshal(t.records)
	t.mu.RUnlock()
	if err != nil {
		return err
	}
	return os.WriteFile(t.dataFile, data, 0644)
}

// Summary for a time period.
type Summary struct {
	Period        string             `json:"period"`
	TotalRequests int                `json:"total_requests"`
	TotalTokens   int                `json:"total_tokens"`
	TotalCost     float64            `json:"total_cost"`
	ByModel       map[string]int     `json:"by_model"`
	ByChannel     map[string]int     `json:"by_channel"`
	ByUser        map[string]float64 `json:"by_user"`
}

// GetSummary returns usage stats for the given period.
func (t *Tracker) GetSummary(period string) Summary {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var from time.Time
	now := time.Now()
	switch period {
	case "today":
		from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "week":
		from = now.AddDate(0, 0, -7)
	case "month":
		from = now.AddDate(0, -1, 0)
	default:
		from = time.Time{} // all time
	}

	s := Summary{
		Period:    period,
		ByModel:   make(map[string]int),
		ByChannel: make(map[string]int),
		ByUser:    make(map[string]float64),
	}

	for _, r := range t.records {
		if r.Timestamp.Before(from) {
			continue
		}
		s.TotalRequests++
		s.TotalTokens += r.TotalTokens
		s.TotalCost += r.TotalCost
		s.ByModel[r.Model]++
		s.ByChannel[r.Channel]++
		s.ByUser[r.UserID] += r.TotalCost
	}
	return s
}

// RegisterSkills registers tracking skills.
func RegisterSkills(reg *skill.Registry, tracker *Tracker) {
	reg.Register(&skill.Skill{
		Name:        "usage_summary",
		Description: "Mostrar resumo de uso e custos de IA",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"period": {Type: jsonschema.String, Description: "Periodo: today, week, month, all (default: today)"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			period := args["period"]
			if period == "" {
				period = "today"
			}
			s := tracker.GetSummary(period)
			var sb strings.Builder
			fmt.Fprintf(&sb, "## Uso de IA (%s)\n\n", s.Period)
			fmt.Fprintf(&sb, "- Requisicoes: %d\n", s.TotalRequests)
			fmt.Fprintf(&sb, "- Tokens: %d\n", s.TotalTokens)
			fmt.Fprintf(&sb, "- Custo: $%.4f USD\n\n", s.TotalCost)

			if len(s.ByModel) > 0 {
				sb.WriteString("### Por modelo\n")
				for m, c := range s.ByModel {
					fmt.Fprintf(&sb, "- %s: %d requisicoes\n", m, c)
				}
			}
			if len(s.ByUser) > 0 {
				sb.WriteString("\n### Por usuario\n")
				type userCost struct {
					user string
					cost float64
				}
				var users []userCost
				for u, c := range s.ByUser {
					users = append(users, userCost{u, c})
				}
				sort.Slice(users, func(i, j int) bool { return users[i].cost > users[j].cost })
				for _, u := range users {
					fmt.Fprintf(&sb, "- %s: $%.4f\n", u.user, u.cost)
				}
			}
			return sb.String(), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "usage_export",
		Description: "Exportar dados de uso para CSV",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path": {Type: jsonschema.String, Description: "Caminho do CSV (default: usage.csv)"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			if path == "" {
				path = "usage.csv"
			}

			tracker.mu.RLock()
			records := make([]UsageRecord, len(tracker.records))
			copy(records, tracker.records)
			tracker.mu.RUnlock()

			var sb strings.Builder
			sb.WriteString("timestamp,channel,user_id,model,prompt_tokens,completion_tokens,total_tokens,total_cost\n")
			for _, r := range records {
				fmt.Fprintf(&sb, "%s,%s,%s,%s,%d,%d,%d,%.6f\n",
					r.Timestamp.Format(time.RFC3339), r.Channel, r.UserID, r.Model,
					r.PromptTokens, r.CompletionTokens, r.TotalTokens, r.TotalCost)
			}

			if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
				return "", err
			}
			return fmt.Sprintf("Exportado %d registros para %s", len(records), path), nil
		},
	})
}
