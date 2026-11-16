// Package goal — Goal System with Independent Verifier
package goal

import (
	"fmt"
	"strings"
	"time"
)

// Goal represents a verifiable goal
type Goal struct {
	ID          string
	Objective   string
	DoneWhen    []string
	Status      string
	Progress    int
	Verifier    *Verifier
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// Verifier independently verifies goal completion
type Verifier struct {
	criteria []string
	results  []VerifyResult
}

// VerifyResult holds verification result
type VerifyResult struct {
	Criterion string
	Passed    bool
	Evidence  string
}

// NewGoal creates a new goal
func NewGoal(objective string, doneWhen []string) *Goal {
	return &Goal{
		ID:        fmt.Sprintf("goal_%d", time.Now().UnixNano()),
		Objective: objective,
		DoneWhen:  doneWhen,
		Status:    "active",
		Verifier:  NewVerifier(doneWhen),
	}
}

// NewVerifier creates a verifier
func NewVerifier(criteria []string) *Verifier {
	return &Verifier{criteria: criteria}
}

// Verify verifies all criteria
func (v *Verifier) Verify() []VerifyResult {
	v.results = make([]VerifyResult, len(v.criteria))
	for i, criterion := range v.criteria {
		// In real implementation, check actual evidence
		v.results[i] = VerifyResult{
			Criterion: criterion,
			Passed:    false,
			Evidence:  "Pending verification",
		}
	}
	return v.results
}

// IsComplete checks if all criteria pass
func (v *Verifier) IsComplete() bool {
	for _, result := range v.results {
		if !result.Passed {
			return false
		}
	}
	return true
}

// UpdateProgress updates goal progress
func (g *Goal) UpdateProgress(progress int) {
	g.Progress = progress
}

// Complete marks goal as complete
func (g *Goal) Complete() {
	now := time.Now()
	g.CompletedAt = &now
	g.Status = "completed"
}

// FormatGoal formats goal for display
func FormatGoal(goal *Goal) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🎯 **Goal:** %s\n\n", goal.Objective))
	sb.WriteString("**Done When**:\n")
	
	results := goal.Verifier.Verify()
	for i, result := range results {
		icon := "❌"
		if result.Passed {
			icon = "✅"
		}
		sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, icon, goal.DoneWhen[i]))
		if result.Evidence != "" {
			sb.WriteString(fmt.Sprintf("   Evidence: %s\n", result.Evidence))
		}
	}
	
	sb.WriteString(fmt.Sprintf("\n**Progress:** %d%%\n", goal.Progress))
	sb.WriteString(fmt.Sprintf("**Status:** %s\n", goal.Status))
	
	return sb.String()
}
