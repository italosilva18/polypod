package tracking

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// ModelCost tracks cumulative costs for a single model.
type ModelCost struct {
	Model               string  `json:"model"`
	PromptTokens        int     `json:"prompt_tokens"`
	CompletionTokens    int     `json:"completion_tokens"`
	CacheReadTokens     int     `json:"cache_read_tokens"`
	CacheCreationTokens int     `json:"cache_creation_tokens"`
	TotalTokens         int     `json:"total_tokens"`
	InputCostUSD        float64 `json:"input_cost_usd"`
	OutputCostUSD       float64 `json:"output_cost_usd"`
	TotalCostUSD        float64 `json:"total_cost_usd"`
	Requests            int     `json:"requests"`
}

// ToolCost tracks cumulative costs attributed to a tool's re-invocations.
type ToolCost struct {
	ToolName         string  `json:"tool_name"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	TotalCostUSD     float64 `json:"total_cost_usd"`
	Invocations      int     `json:"invocations"`
}

// ModelPricing holds per-million-token pricing for a model.
type ModelPricing struct {
	InputPerMillion  float64
	OutputPerMillion float64
}

// CostEnginePricingTable maps model name prefixes to pricing.
// Models are matched by exact key first, then by prefix.
var CostEnginePricingTable = map[string]ModelPricing{
	// Claude family
	"claude-sonnet-4":   {InputPerMillion: 3.00, OutputPerMillion: 15.00},
	"claude-3-5-sonnet": {InputPerMillion: 3.00, OutputPerMillion: 15.00},
	"claude-3.5-sonnet": {InputPerMillion: 3.00, OutputPerMillion: 15.00},
	"claude-3-5-haiku":  {InputPerMillion: 0.80, OutputPerMillion: 4.00},
	"claude-3.5-haiku":  {InputPerMillion: 0.80, OutputPerMillion: 4.00},
	"claude-3-haiku":    {InputPerMillion: 0.25, OutputPerMillion: 1.25},
	"claude-3-opus":     {InputPerMillion: 15.00, OutputPerMillion: 75.00},

	// OpenAI family
	"gpt-4o-mini":   {InputPerMillion: 0.15, OutputPerMillion: 0.60},
	"gpt-4o":        {InputPerMillion: 2.50, OutputPerMillion: 10.00},
	"gpt-4.1":       {InputPerMillion: 2.00, OutputPerMillion: 8.00},
	"gpt-3.5-turbo": {InputPerMillion: 0.50, OutputPerMillion: 1.50},

	// Google Gemini family
	"gemini-2.5-pro":   {InputPerMillion: 1.25, OutputPerMillion: 10.00},
	"gemini-2.5-flash": {InputPerMillion: 0.15, OutputPerMillion: 0.60},
	"gemini-1.5-pro":   {InputPerMillion: 1.25, OutputPerMillion: 5.00},
	"gemini-1.5-flash": {InputPerMillion: 0.075, OutputPerMillion: 0.30},

	// DeepSeek family
	"deepseek-chat":     {InputPerMillion: 0.14, OutputPerMillion: 0.28},
	"deepseek-coder":    {InputPerMillion: 0.14, OutputPerMillion: 0.28},
	"deepseek-reasoner": {InputPerMillion: 0.55, OutputPerMillion: 2.19},

	// Meta Llama family
	"llama-3.3-70b": {InputPerMillion: 0.59, OutputPerMillion: 0.79},
	"llama3.3":      {InputPerMillion: 0.59, OutputPerMillion: 0.79},
	"llama3.1":      {InputPerMillion: 0, OutputPerMillion: 0},

	// Mistral
	"mistral-large": {InputPerMillion: 2.00, OutputPerMillion: 6.00},

	// Local/free models
	"qwen2.5": {InputPerMillion: 0, OutputPerMillion: 0},
}

// lookupPricing finds pricing for a model, trying exact match first then prefix.
func lookupPricing(model string) (ModelPricing, bool) {
	if p, ok := CostEnginePricingTable[model]; ok {
		return p, true
	}
	// Try prefix match (longer prefixes first for specificity)
	var bestKey string
	for key := range CostEnginePricingTable {
		if strings.HasPrefix(model, key) && len(key) > len(bestKey) {
			bestKey = key
		}
	}
	if bestKey != "" {
		return CostEnginePricingTable[bestKey], true
	}
	return ModelPricing{}, false
}

// calculateModelCost computes USD cost from token counts using the CostEngine pricing table.
func calculateModelCost(model string, promptTokens, completionTokens int) (inputCost, outputCost float64) {
	pricing, ok := lookupPricing(model)
	if !ok {
		return 0, 0
	}
	inputCost = float64(promptTokens) / 1_000_000 * pricing.InputPerMillion
	outputCost = float64(completionTokens) / 1_000_000 * pricing.OutputPerMillion
	return
}

// CostEngine provides comprehensive per-session cost tracking with breakdowns
// by model and tool.
type CostEngine struct {
	mu sync.RWMutex

	// Session-level aggregates
	totalPromptTokens        int
	totalCompletionTokens    int
	totalCacheReadTokens     int
	totalCacheCreationTokens int
	totalInputCostUSD        float64
	totalOutputCostUSD       float64
	totalRequests            int

	// Per-model breakdown
	modelCosts map[string]*ModelCost

	// Per-tool breakdown
	toolCosts map[string]*ToolCost

	logger *slog.Logger
}

// NewCostEngine creates a new CostEngine.
func NewCostEngine(logger *slog.Logger) *CostEngine {
	return &CostEngine{
		modelCosts: make(map[string]*ModelCost),
		toolCosts:  make(map[string]*ToolCost),
		logger:     logger,
	}
}

// RecordCompletion records token usage and cost for one API completion call.
func (ce *CostEngine) RecordCompletion(model string, promptTokens, completionTokens, cacheReadTokens, cacheCreationTokens int) {
	inputCost, outputCost := calculateModelCost(model, promptTokens, completionTokens)

	ce.mu.Lock()
	defer ce.mu.Unlock()

	ce.totalPromptTokens += promptTokens
	ce.totalCompletionTokens += completionTokens
	ce.totalCacheReadTokens += cacheReadTokens
	ce.totalCacheCreationTokens += cacheCreationTokens
	ce.totalInputCostUSD += inputCost
	ce.totalOutputCostUSD += outputCost
	ce.totalRequests++

	mc, ok := ce.modelCosts[model]
	if !ok {
		mc = &ModelCost{Model: model}
		ce.modelCosts[model] = mc
	}
	mc.PromptTokens += promptTokens
	mc.CompletionTokens += completionTokens
	mc.CacheReadTokens += cacheReadTokens
	mc.CacheCreationTokens += cacheCreationTokens
	mc.TotalTokens += promptTokens + completionTokens
	mc.InputCostUSD += inputCost
	mc.OutputCostUSD += outputCost
	mc.TotalCostUSD += inputCost + outputCost
	mc.Requests++

	if ce.logger != nil {
		ce.logger.Debug("cost recorded",
			"model", model,
			"prompt_tokens", promptTokens,
			"completion_tokens", completionTokens,
			"input_cost", inputCost,
			"output_cost", outputCost,
		)
	}
}

// RecordToolUse records token costs attributed to a tool invocation.
func (ce *CostEngine) RecordToolUse(toolName string, promptTokens, completionTokens int) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	tc, ok := ce.toolCosts[toolName]
	if !ok {
		tc = &ToolCost{ToolName: toolName}
		ce.toolCosts[toolName] = tc
	}
	tc.PromptTokens += promptTokens
	tc.CompletionTokens += completionTokens
	tc.TotalTokens += promptTokens + completionTokens
	// Estimate tool cost using overall session average cost-per-token
	totalTokens := ce.totalPromptTokens + ce.totalCompletionTokens
	totalCost := ce.totalInputCostUSD + ce.totalOutputCostUSD
	if totalTokens > 0 {
		avgCostPerToken := totalCost / float64(totalTokens)
		tc.TotalCostUSD += avgCostPerToken * float64(promptTokens+completionTokens)
	}
	tc.Invocations++
}

// SessionCost returns the total USD cost for the current session.
func (ce *CostEngine) SessionCost() float64 {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	return ce.totalInputCostUSD + ce.totalOutputCostUSD
}

// SessionTokens returns prompt, completion, and total token counts for the session.
func (ce *CostEngine) SessionTokens() (prompt, completion, total int) {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	prompt = ce.totalPromptTokens
	completion = ce.totalCompletionTokens
	total = prompt + completion
	return
}

// SessionRequests returns the total number of API requests in the session.
func (ce *CostEngine) SessionRequests() int {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	return ce.totalRequests
}

// ModelBreakdown returns a copy of per-model cost data.
func (ce *CostEngine) ModelBreakdown() map[string]ModelCost {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	result := make(map[string]ModelCost, len(ce.modelCosts))
	for k, v := range ce.modelCosts {
		result[k] = *v
	}
	return result
}

// ToolBreakdown returns a copy of per-tool cost data.
func (ce *CostEngine) ToolBreakdown() map[string]ToolCost {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	result := make(map[string]ToolCost, len(ce.toolCosts))
	for k, v := range ce.toolCosts {
		result[k] = *v
	}
	return result
}

// FormatCostSummary returns a pretty-printed one-line cost summary.
func (ce *CostEngine) FormatCostSummary() string {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	total := ce.totalInputCostUSD + ce.totalOutputCostUSD
	totalTokens := ce.totalPromptTokens + ce.totalCompletionTokens

	return fmt.Sprintf("$%.4f USD | %d tokens (%d prompt + %d completion) | %d requests",
		total, totalTokens, ce.totalPromptTokens, ce.totalCompletionTokens, ce.totalRequests)
}

// FormatDetailedReport returns a detailed multi-section cost report.
func (ce *CostEngine) FormatDetailedReport() string {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	var sb strings.Builder

	totalCost := ce.totalInputCostUSD + ce.totalOutputCostUSD
	totalTokens := ce.totalPromptTokens + ce.totalCompletionTokens

	// ── Session overview ──
	sb.WriteString("## Custo da sessao\n\n")
	fmt.Fprintf(&sb, "- **Tokens**: %d prompt + %d completion = **%d total**\n",
		ce.totalPromptTokens, ce.totalCompletionTokens, totalTokens)
	if ce.totalCacheReadTokens > 0 || ce.totalCacheCreationTokens > 0 {
		fmt.Fprintf(&sb, "- **Cache**: %d read, %d creation\n",
			ce.totalCacheReadTokens, ce.totalCacheCreationTokens)
	}
	fmt.Fprintf(&sb, "- **Custo**: $%.4f input + $%.4f output = **$%.4f USD**\n",
		ce.totalInputCostUSD, ce.totalOutputCostUSD, totalCost)
	fmt.Fprintf(&sb, "- **Requisicoes**: %d\n", ce.totalRequests)

	if ce.totalRequests > 0 {
		fmt.Fprintf(&sb, "- **Media**: $%.4f/req, %d tokens/req\n",
			totalCost/float64(ce.totalRequests),
			totalTokens/ce.totalRequests)
	}

	// ── Per-model breakdown ──
	if len(ce.modelCosts) > 0 {
		sb.WriteString("\n### Por modelo\n\n")
		sb.WriteString("| Modelo | Prompt | Completion | Custo | Reqs |\n")
		sb.WriteString("|--------|--------|------------|-------|------|\n")

		// Sort models by cost descending
		type modelEntry struct {
			name string
			mc   *ModelCost
		}
		var models []modelEntry
		for name, mc := range ce.modelCosts {
			models = append(models, modelEntry{name, mc})
		}
		sort.Slice(models, func(i, j int) bool {
			return models[i].mc.TotalCostUSD > models[j].mc.TotalCostUSD
		})

		for _, m := range models {
			fmt.Fprintf(&sb, "| %s | %d | %d | $%.4f | %d |\n",
				m.name, m.mc.PromptTokens, m.mc.CompletionTokens,
				m.mc.TotalCostUSD, m.mc.Requests)
		}
	}

	// ── Per-tool breakdown ──
	if len(ce.toolCosts) > 0 {
		sb.WriteString("\n### Por ferramenta\n\n")
		sb.WriteString("| Ferramenta | Tokens | Custo est. | Chamadas |\n")
		sb.WriteString("|------------|--------|------------|----------|\n")

		// Sort tools by cost descending
		type toolEntry struct {
			name string
			tc   *ToolCost
		}
		var tools []toolEntry
		for name, tc := range ce.toolCosts {
			tools = append(tools, toolEntry{name, tc})
		}
		sort.Slice(tools, func(i, j int) bool {
			return tools[i].tc.TotalCostUSD > tools[j].tc.TotalCostUSD
		})

		for _, t := range tools {
			fmt.Fprintf(&sb, "| %s | %d | $%.4f | %d |\n",
				t.name, t.tc.TotalTokens, t.tc.TotalCostUSD, t.tc.Invocations)
		}
	}

	return sb.String()
}

// Reset clears all session data for a new session.
func (ce *CostEngine) Reset() {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	ce.totalPromptTokens = 0
	ce.totalCompletionTokens = 0
	ce.totalCacheReadTokens = 0
	ce.totalCacheCreationTokens = 0
	ce.totalInputCostUSD = 0
	ce.totalOutputCostUSD = 0
	ce.totalRequests = 0
	ce.modelCosts = make(map[string]*ModelCost)
	ce.toolCosts = make(map[string]*ToolCost)
}
