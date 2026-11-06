// Package tips implements a tip scheduling system that displays contextual
// tips to users based on usage patterns, cooldowns, and relevance conditions.
//
// Migrated from TypeScript: services/tips/{tipHistory,tipRegistry,tipScheduler}.ts
package tips

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Tip types
// ---------------------------------------------------------------------------

// TipContext carries session information used by relevance checks.
type TipContext struct {
	NumStartups       int
	BashTools         map[string]struct{}
	ReadFilePaths     []string
	Terminal          string
	Platform          string
	IsSSH             bool
	MemoryUsageCount  int
	PromptQueueUseUse int
	PlanModeLastUse   time.Time
	IsInteractive     bool
	CustomOverride    *SpinnerTipsOverride

	// Simplified-API fields (used by Registry.Next).
	TurnCount       int
	ToolsUsed       []string
	SessionDuration time.Duration
	CommandsUsed    []string
}

// SpinnerTipsOverride allows settings to replace or augment built-in tips.
type SpinnerTipsOverride struct {
	Tips           []string
	ExcludeDefault bool
}

// RelevanceFunc determines whether a tip should be offered in the given context.
type RelevanceFunc func(ctx *TipContext) bool

// ContentFunc generates tip display text, optionally using context.
type ContentFunc func(ctx *TipContext) string

// Tip represents a single displayable tip.
type Tip struct {
	ID               string
	Content          ContentFunc
	CooldownSessions int
	IsRelevant       RelevanceFunc

	// Simplified-API fields (used by Registry).
	Text       string                    // static tip text (alternative to Content)
	Category   string                    // tip category for grouping
	Condition  func(ctx TipContext) bool // when to show (alternative to IsRelevant)
	ShownCount int                       // how many times this tip has been shown
	MaxShows   int                       // max times to show (0 = unlimited)
}

// ---------------------------------------------------------------------------
// Tip history – records when tips were last shown (by startup count).
// ---------------------------------------------------------------------------

// TipHistory tracks when each tip was last shown.
type TipHistory struct {
	mu      sync.RWMutex
	records map[string]int // tipID → numStartups at last show
}

// NewTipHistory creates an empty tip history.
func NewTipHistory() *TipHistory {
	return &TipHistory{records: make(map[string]int)}
}

// RecordTipShown marks a tip as shown during the current startup.
func (h *TipHistory) RecordTipShown(tipID string, numStartups int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records[tipID] = numStartups
}

// DismissTip permanently prevents a tip from being shown again by
// recording it far in the future.
func (h *TipHistory) DismissTip(tipID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records[tipID] = math.MaxInt32
}

// SessionsSinceLastShown returns how many startups have passed since
// the tip was last shown. Returns math.MaxInt32 if never shown.
func (h *TipHistory) SessionsSinceLastShown(tipID string, currentStartups int) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	last, ok := h.records[tipID]
	if !ok {
		return math.MaxInt32
	}
	return currentStartups - last
}

// Load replaces the internal history from a persisted map.
func (h *TipHistory) Load(data map[string]int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = make(map[string]int, len(data))
	for k, v := range data {
		h.records[k] = v
	}
}

// Snapshot returns a copy of the history for persistence.
func (h *TipHistory) Snapshot() map[string]int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]int, len(h.records))
	for k, v := range h.records {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------------
// Tip registry – holds all registered tips.
// ---------------------------------------------------------------------------

// TipRegistry manages the set of available tips.
type TipRegistry struct {
	mu   sync.RWMutex
	tips []Tip
}

// NewTipRegistry creates an empty registry.
func NewTipRegistry() *TipRegistry {
	return &TipRegistry{}
}

// Register adds a tip to the registry.
func (r *TipRegistry) Register(tip Tip) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tips = append(r.tips, tip)
}

// RegisterMany adds multiple tips to the registry.
func (r *TipRegistry) RegisterMany(tips []Tip) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tips = append(r.tips, tips...)
}

// All returns a copy of all registered tips.
func (r *TipRegistry) All() []Tip {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tip, len(r.tips))
	copy(out, r.tips)
	return out
}

// ---------------------------------------------------------------------------
// Built-in tips
// ---------------------------------------------------------------------------

// BuiltinTips returns the default set of tips, analogous to the
// externalTips array in the TypeScript source.
func BuiltinTips() []Tip {
	return []Tip{
		{
			ID: "new-user-warmup",
			Content: func(_ *TipContext) string {
				return "Start with small features or bug fixes, propose a plan, and verify edits"
			},
			CooldownSessions: 3,
			IsRelevant: func(ctx *TipContext) bool {
				return ctx.NumStartups < 10
			},
		},
		{
			ID: "plan-mode-for-complex-tasks",
			Content: func(_ *TipContext) string {
				return "Use Plan Mode to prepare for a complex request before making changes. Press Shift+Tab twice to enable."
			},
			CooldownSessions: 5,
			IsRelevant: func(ctx *TipContext) bool {
				if ctx.PlanModeLastUse.IsZero() {
					return true
				}
				return time.Since(ctx.PlanModeLastUse) > 7*24*time.Hour
			},
		},
		{
			ID: "git-worktrees",
			Content: func(_ *TipContext) string {
				return "Use git worktrees to run multiple sessions in parallel."
			},
			CooldownSessions: 10,
			IsRelevant: func(ctx *TipContext) bool {
				return ctx.NumStartups > 50
			},
		},
		{
			ID: "memory-command",
			Content: func(_ *TipContext) string {
				return "Use /memory to view and manage agent memory"
			},
			CooldownSessions: 15,
			IsRelevant: func(ctx *TipContext) bool {
				return ctx.MemoryUsageCount <= 0
			},
		},
		{
			ID: "theme-command",
			Content: func(_ *TipContext) string {
				return "Use /theme to change the color theme"
			},
			CooldownSessions: 20,
			IsRelevant:       func(_ *TipContext) bool { return true },
		},
		{
			ID: "prompt-queue",
			Content: func(_ *TipContext) string {
				return "Hit Enter to queue up additional messages while the agent is working."
			},
			CooldownSessions: 5,
			IsRelevant: func(ctx *TipContext) bool {
				return ctx.PromptQueueUseUse <= 3
			},
		},
		{
			ID: "todo-list",
			Content: func(_ *TipContext) string {
				return "Ask the agent to create a todo list when working on complex tasks to track progress"
			},
			CooldownSessions: 20,
			IsRelevant:       func(_ *TipContext) bool { return true },
		},
		{
			ID: "shift-tab",
			Content: func(_ *TipContext) string {
				return "Hit Shift+Tab to cycle between permission modes"
			},
			CooldownSessions: 10,
			IsRelevant:       func(_ *TipContext) bool { return true },
		},
		{
			ID: "continue",
			Content: func(_ *TipContext) string {
				return "Run polypod --continue or polypod --resume to resume a conversation"
			},
			CooldownSessions: 10,
			IsRelevant:       func(_ *TipContext) bool { return true },
		},
		{
			ID: "custom-commands",
			Content: func(_ *TipContext) string {
				return "Create skills by adding .md files to .polypod/skills/ in your project"
			},
			CooldownSessions: 15,
			IsRelevant: func(ctx *TipContext) bool {
				return ctx.NumStartups > 10
			},
		},
		{
			ID: "custom-agents",
			Content: func(_ *TipContext) string {
				return "Use /agents to optimize specific tasks. E.g. Architect, Writer, Reviewer"
			},
			CooldownSessions: 15,
			IsRelevant: func(ctx *TipContext) bool {
				return ctx.NumStartups > 5
			},
		},
		{
			ID: "drag-and-drop-images",
			Content: func(_ *TipContext) string {
				return "Did you know you can drag and drop image files into your terminal?"
			},
			CooldownSessions: 10,
			IsRelevant: func(ctx *TipContext) bool {
				return !ctx.IsSSH
			},
		},
		{
			ID: "double-esc",
			Content: func(_ *TipContext) string {
				return "Double-tap Esc to rewind the conversation to a previous point"
			},
			CooldownSessions: 10,
			IsRelevant:       func(_ *TipContext) bool { return true },
		},
		{
			ID: "permissions",
			Content: func(_ *TipContext) string {
				return "Use /permissions to pre-approve and pre-deny tools"
			},
			CooldownSessions: 10,
			IsRelevant: func(ctx *TipContext) bool {
				return ctx.NumStartups > 10
			},
		},
		{
			ID: "feedback-command",
			Content: func(_ *TipContext) string {
				return "Use /feedback to help us improve!"
			},
			CooldownSessions: 15,
			IsRelevant: func(ctx *TipContext) bool {
				return ctx.NumStartups > 5
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Tip scheduler – selects which tip to show.
// ---------------------------------------------------------------------------

// TipScheduler evaluates tips and picks the best one to display.
type TipScheduler struct {
	Registry *TipRegistry
	History  *TipHistory
	Enabled  bool // false disables all tips
}

// NewTipScheduler creates a scheduler wired to the given registry and history.
func NewTipScheduler(reg *TipRegistry, hist *TipHistory) *TipScheduler {
	return &TipScheduler{
		Registry: reg,
		History:  hist,
		Enabled:  true,
	}
}

// ShouldShowTip evaluates a single tip against the current context.
func (s *TipScheduler) ShouldShowTip(tip *Tip, ctx *TipContext) bool {
	if !s.Enabled {
		return false
	}
	if s.History.SessionsSinceLastShown(tip.ID, ctx.NumStartups) < tip.CooldownSessions {
		return false
	}
	if tip.IsRelevant != nil && !tip.IsRelevant(ctx) {
		return false
	}
	return true
}

// GetRelevantTips returns all tips that pass relevance and cooldown checks.
func (s *TipScheduler) GetRelevantTips(ctx *TipContext) []Tip {
	if !s.Enabled {
		return nil
	}

	// If custom override with excludeDefault, return only custom tips.
	if ctx.CustomOverride != nil && ctx.CustomOverride.ExcludeDefault && len(ctx.CustomOverride.Tips) > 0 {
		return customTipsFromStrings(ctx.CustomOverride.Tips)
	}

	all := s.Registry.All()
	var relevant []Tip
	for i := range all {
		tip := &all[i]
		if s.ShouldShowTip(tip, ctx) {
			relevant = append(relevant, *tip)
		}
	}

	// Append custom tips (always relevant, no cooldown).
	if ctx.CustomOverride != nil {
		relevant = append(relevant, customTipsFromStrings(ctx.CustomOverride.Tips)...)
	}

	return relevant
}

// SelectTip picks the single best tip to display — the one that has gone
// the longest without being shown.
func (s *TipScheduler) SelectTip(ctx *TipContext) *Tip {
	tips := s.GetRelevantTips(ctx)
	if len(tips) == 0 {
		return nil
	}
	if len(tips) == 1 {
		return &tips[0]
	}

	type scored struct {
		tip      Tip
		sessions int
	}
	scored_tips := make([]scored, len(tips))
	for i, t := range tips {
		scored_tips[i] = scored{
			tip:      t,
			sessions: s.History.SessionsSinceLastShown(t.ID, ctx.NumStartups),
		}
	}
	sort.Slice(scored_tips, func(i, j int) bool {
		return scored_tips[i].sessions > scored_tips[j].sessions
	})
	return &scored_tips[0].tip
}

// RecordShown marks the selected tip as shown.
func (s *TipScheduler) RecordShown(tip *Tip, numStartups int) {
	s.History.RecordTipShown(tip.ID, numStartups)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func customTipsFromStrings(texts []string) []Tip {
	tips := make([]Tip, len(texts))
	for i, text := range texts {
		t := text // capture
		tips[i] = Tip{
			ID:               fmt.Sprintf("custom-tip-%d", i),
			Content:          func(_ *TipContext) string { return t },
			CooldownSessions: 0,
			IsRelevant:       func(_ *TipContext) bool { return true },
		}
	}
	return tips
}

// ---------------------------------------------------------------------------
// Simplified Registry API — wraps TipRegistry with dedup and show-tracking.
// ---------------------------------------------------------------------------

// Registry holds all tips with deduplication and show-count tracking.
// It provides a simpler API than the TipRegistry+TipHistory+TipScheduler trio.
type Registry struct {
	mu   sync.Mutex
	tips map[string]*Tip // keyed by ID for dedup
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		tips: make(map[string]*Tip),
	}
}

// Register adds a tip to the registry. If a tip with the same ID already
// exists it is silently ignored (dedup).
func (r *Registry) Register(tip Tip) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tips[tip.ID]; exists {
		return
	}
	t := tip // copy
	r.tips[tip.ID] = &t
}

// Next returns the next applicable tip that hasn't exceeded MaxShows.
// Among eligible tips it picks the one shown the fewest times.
// Returns nil when no tip qualifies.
func (r *Registry) Next(ctx TipContext) *Tip {
	r.mu.Lock()
	defer r.mu.Unlock()

	var best *Tip
	for _, tip := range r.tips {
		// Skip tips that exceeded their show limit.
		if tip.MaxShows > 0 && tip.ShownCount >= tip.MaxShows {
			continue
		}
		// Evaluate condition (Condition field or IsRelevant fallback).
		if tip.Condition != nil && !tip.Condition(ctx) {
			continue
		}
		if tip.Condition == nil && tip.IsRelevant != nil && !tip.IsRelevant(&ctx) {
			continue
		}
		// Prefer least-shown tip.
		if best == nil || tip.ShownCount < best.ShownCount {
			best = tip
		}
	}
	return best
}

// MarkShown increments ShownCount for the given tip ID.
func (r *Registry) MarkShown(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if tip, ok := r.tips[id]; ok {
		tip.ShownCount++
	}
}

// All returns a snapshot of every registered tip.
func (r *Registry) All() []Tip {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Tip, 0, len(r.tips))
	for _, t := range r.tips {
		out = append(out, *t)
	}
	return out
}

// ---------------------------------------------------------------------------
// Default tips (Portuguese) and registration helper.
// ---------------------------------------------------------------------------

// DefaultTips returns built-in tips with Portuguese text covering common
// features and shortcuts.
func DefaultTips() []Tip {
	return []Tip{
		{
			ID:       "tip-compact",
			Text:     "Use /compact para liberar espaço na janela de contexto",
			Category: "comandos",
			MaxShows: 5,
		},
		{
			ID:       "tip-cost",
			Text:     "Use /cost para ver o consumo de tokens da sessão",
			Category: "comandos",
			MaxShows: 3,
		},
		{
			ID:       "tip-help",
			Text:     "Use /help para ver todos os comandos disponíveis",
			Category: "comandos",
			MaxShows: 3,
		},
		{
			ID:       "tip-cancel",
			Text:     "Pressione Ctrl+C para cancelar uma operação em andamento",
			Category: "atalhos",
			MaxShows: 3,
		},
		{
			ID:       "tip-model",
			Text:     "Use /model para trocar o modelo de IA durante a sessão",
			Category: "comandos",
			MaxShows: 3,
		},
		{
			ID:       "tip-memory",
			Text:     "Use /memory para consultar e gerenciar a memória do agente",
			Category: "comandos",
			MaxShows: 3,
		},
		{
			ID:       "tip-theme",
			Text:     "Use /theme para mudar o tema de cores do terminal",
			Category: "personalização",
			MaxShows: 3,
		},
		{
			ID:       "tip-resume",
			Text:     "Use --continue ou --resume para retomar uma conversa anterior",
			Category: "sessão",
			MaxShows: 3,
		},
		{
			ID:       "tip-plan-mode",
			Text:     "Pressione Shift+Tab duas vezes para ativar o Modo Planejamento",
			Category: "atalhos",
			MaxShows: 5,
		},
		{
			ID:       "tip-permissions",
			Text:     "Use /permissions para pré-aprovar ou bloquear ferramentas",
			Category: "comandos",
			MaxShows: 3,
			Condition: func(ctx TipContext) bool {
				return ctx.TurnCount > 5
			},
		},
		{
			ID:       "tip-drag-drop",
			Text:     "Você pode arrastar e soltar imagens diretamente no terminal",
			Category: "dicas",
			MaxShows: 3,
		},
		{
			ID:       "tip-double-esc",
			Text:     "Pressione Esc duas vezes para voltar a um ponto anterior da conversa",
			Category: "atalhos",
			MaxShows: 3,
		},
		{
			ID:       "tip-skills",
			Text:     "Crie skills adicionando arquivos .md em .polypod/skills/ no seu projeto",
			Category: "avançado",
			MaxShows: 3,
			Condition: func(ctx TipContext) bool {
				return ctx.TurnCount > 10
			},
		},
		{
			ID:       "tip-agents",
			Text:     "Use /agents para otimizar tarefas específicas com agentes especializados",
			Category: "avançado",
			MaxShows: 3,
		},
		{
			ID:       "tip-feedback",
			Text:     "Use /feedback para nos ajudar a melhorar!",
			Category: "geral",
			MaxShows: 3,
		},
	}
}

// RegisterDefaults registers all DefaultTips into the given Registry.
func RegisterDefaults(r *Registry) {
	for _, tip := range DefaultTips() {
		r.Register(tip)
	}
}
