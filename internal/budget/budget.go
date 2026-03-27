package budget

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Limits defines token/cost budgets.
type Limits struct {
	PerSession  int     `yaml:"per_session"`  // max tokens per session (0 = unlimited)
	PerDay      int     `yaml:"per_day"`      // max tokens per day
	PerMonth    int     `yaml:"per_month"`    // max tokens per month
	PerUser     int     `yaml:"per_user"`     // max tokens per user per day
	MaxCostDay  float64 `yaml:"max_cost_day"` // max USD per day
	MaxCostMonth float64 `yaml:"max_cost_month"` // max USD per month
	AlertAt50   bool    `yaml:"alert_at_50"`  // alert at 50% usage
	AlertAt80   bool    `yaml:"alert_at_80"`  // alert at 80% usage
	AutoDowngrade bool  `yaml:"auto_downgrade"` // switch to cheaper model near limit
}

// Usage tracks current consumption.
type Usage struct {
	SessionTokens int
	DayTokens     int
	MonthTokens   int
	DayCost       float64
	MonthCost     float64
	UserTokens    map[string]int
}

// Manager tracks budgets and enforces limits.
type Manager struct {
	mu     sync.Mutex
	limits Limits
	usage  Usage
	logger *slog.Logger
	dayKey string // "2006-01-02"
	monthKey string // "2006-01"
	onAlert func(level int, message string) // callback for alerts (50, 80, 100)
}

// NewManager creates a budget manager.
func NewManager(limits Limits, logger *slog.Logger) *Manager {
	now := time.Now()
	return &Manager{
		limits:   limits,
		usage:    Usage{UserTokens: make(map[string]int)},
		logger:   logger,
		dayKey:   now.Format("2006-01-02"),
		monthKey: now.Format("2006-01"),
	}
}

// SetAlertCallback sets the function called on budget alerts.
func (m *Manager) SetAlertCallback(fn func(level int, message string)) {
	m.onAlert = fn
}

// Record adds token usage and checks limits.
func (m *Manager) Record(userID string, tokens int, cost float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Reset daily/monthly counters if needed
	now := time.Now()
	if now.Format("2006-01-02") != m.dayKey {
		m.usage.DayTokens = 0
		m.usage.DayCost = 0
		m.usage.UserTokens = make(map[string]int)
		m.dayKey = now.Format("2006-01-02")
	}
	if now.Format("2006-01") != m.monthKey {
		m.usage.MonthTokens = 0
		m.usage.MonthCost = 0
		m.monthKey = now.Format("2006-01")
	}

	m.usage.SessionTokens += tokens
	m.usage.DayTokens += tokens
	m.usage.MonthTokens += tokens
	m.usage.DayCost += cost
	m.usage.MonthCost += cost
	m.usage.UserTokens[userID] += tokens

	// Check limits and fire alerts
	m.checkLimits(userID)

	return nil
}

// Check returns whether the next request is allowed.
func (m *Manager) Check(userID string) (allowed bool, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.limits.PerSession > 0 && m.usage.SessionTokens >= m.limits.PerSession {
		return false, fmt.Sprintf("limite de sessao atingido (%d/%d tokens)", m.usage.SessionTokens, m.limits.PerSession)
	}
	if m.limits.PerDay > 0 && m.usage.DayTokens >= m.limits.PerDay {
		return false, fmt.Sprintf("limite diario atingido (%d/%d tokens)", m.usage.DayTokens, m.limits.PerDay)
	}
	if m.limits.PerMonth > 0 && m.usage.MonthTokens >= m.limits.PerMonth {
		return false, fmt.Sprintf("limite mensal atingido (%d/%d tokens)", m.usage.MonthTokens, m.limits.PerMonth)
	}
	if m.limits.PerUser > 0 && m.usage.UserTokens[userID] >= m.limits.PerUser {
		return false, fmt.Sprintf("limite de usuario atingido (%d/%d tokens)", m.usage.UserTokens[userID], m.limits.PerUser)
	}
	if m.limits.MaxCostDay > 0 && m.usage.DayCost >= m.limits.MaxCostDay {
		return false, fmt.Sprintf("limite de custo diario atingido ($%.4f/$%.4f)", m.usage.DayCost, m.limits.MaxCostDay)
	}
	if m.limits.MaxCostMonth > 0 && m.usage.MonthCost >= m.limits.MaxCostMonth {
		return false, fmt.Sprintf("limite de custo mensal atingido ($%.4f/$%.4f)", m.usage.MonthCost, m.limits.MaxCostMonth)
	}

	return true, ""
}

// ShouldDowngrade returns true if near limit and auto_downgrade is enabled.
func (m *Manager) ShouldDowngrade() bool {
	if !m.limits.AutoDowngrade {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.limits.PerDay > 0 {
		pct := float64(m.usage.DayTokens) / float64(m.limits.PerDay) * 100
		if pct >= 80 {
			return true
		}
	}
	if m.limits.MaxCostDay > 0 {
		pct := m.usage.DayCost / m.limits.MaxCostDay * 100
		if pct >= 80 {
			return true
		}
	}
	return false
}

// Status returns a formatted budget status.
func (m *Manager) Status() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("## Budget\n\n")

	if m.limits.PerSession > 0 {
		pct := float64(m.usage.SessionTokens) / float64(m.limits.PerSession) * 100
		fmt.Fprintf(&sb, "- Sessao: %d/%d tokens (%.0f%%)\n", m.usage.SessionTokens, m.limits.PerSession, pct)
	} else {
		fmt.Fprintf(&sb, "- Sessao: %d tokens (sem limite)\n", m.usage.SessionTokens)
	}

	if m.limits.PerDay > 0 {
		pct := float64(m.usage.DayTokens) / float64(m.limits.PerDay) * 100
		fmt.Fprintf(&sb, "- Dia: %d/%d tokens (%.0f%%)\n", m.usage.DayTokens, m.limits.PerDay, pct)
	}
	if m.limits.MaxCostDay > 0 {
		pct := m.usage.DayCost / m.limits.MaxCostDay * 100
		fmt.Fprintf(&sb, "- Custo dia: $%.4f/$%.4f (%.0f%%)\n", m.usage.DayCost, m.limits.MaxCostDay, pct)
	}
	if m.limits.MaxCostMonth > 0 {
		pct := m.usage.MonthCost / m.limits.MaxCostMonth * 100
		fmt.Fprintf(&sb, "- Custo mes: $%.4f/$%.4f (%.0f%%)\n", m.usage.MonthCost, m.limits.MaxCostMonth, pct)
	}

	return sb.String()
}

func (m *Manager) checkLimits(userID string) {
	if m.onAlert == nil {
		return
	}

	checks := []struct {
		name    string
		current float64
		limit   float64
	}{
		{"sessao", float64(m.usage.SessionTokens), float64(m.limits.PerSession)},
		{"dia", float64(m.usage.DayTokens), float64(m.limits.PerDay)},
		{"mes", float64(m.usage.MonthTokens), float64(m.limits.PerMonth)},
		{"custo_dia", m.usage.DayCost * 10000, m.limits.MaxCostDay * 10000},
	}

	for _, c := range checks {
		if c.limit <= 0 {
			continue
		}
		pct := c.current / c.limit * 100
		if pct >= 100 {
			m.onAlert(100, fmt.Sprintf("Limite de %s atingido (100%%)", c.name))
		} else if pct >= 80 && m.limits.AlertAt80 {
			m.onAlert(80, fmt.Sprintf("Alerta: %s em %.0f%%", c.name, pct))
		} else if pct >= 50 && m.limits.AlertAt50 {
			m.onAlert(50, fmt.Sprintf("Aviso: %s em %.0f%%", c.name, pct))
		}
	}
}
