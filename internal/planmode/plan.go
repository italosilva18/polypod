// Package planmode implements structured plan mode (v2) with multi-step plans,
// dependency tracking, and parallel execution. Migrated from Claude Code's
// planModeV2.ts and plans.ts.
package planmode

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// PlanStatus represents the overall status of a plan.
type PlanStatus string

const (
	PlanStatusDraft      PlanStatus = "draft"
	PlanStatusApproved   PlanStatus = "approved"
	PlanStatusInProgress PlanStatus = "in_progress"
	PlanStatusCompleted  PlanStatus = "completed"
	PlanStatusFailed     PlanStatus = "failed"
	PlanStatusAborted    PlanStatus = "aborted"
)

// StepStatus represents the status of an individual plan step.
type StepStatus string

const (
	StepStatusPending   StepStatus = "pending"
	StepStatusRunning   StepStatus = "running"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
	StepStatusSkipped   StepStatus = "skipped"
	StepStatusBlocked   StepStatus = "blocked"
)

// PlanStep defines a single step within a plan.
type PlanStep struct {
	// ID uniquely identifies this step within the plan.
	ID string `json:"id"`

	// Description is a human-readable description of the step.
	Description string `json:"description"`

	// ToolCalls lists the tool calls this step will make.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// Dependencies lists step IDs that must complete before this step.
	Dependencies []string `json:"dependencies,omitempty"`

	// Status is the current execution status.
	Status StepStatus `json:"status"`

	// Result holds the outcome after execution.
	Result string `json:"result,omitempty"`

	// Error holds the error message if the step failed.
	Error string `json:"error,omitempty"`

	// StartedAt is when execution began.
	StartedAt time.Time `json:"started_at,omitempty"`

	// CompletedAt is when execution finished.
	CompletedAt time.Time `json:"completed_at,omitempty"`

	// AgentID is the agent assigned to execute this step (for parallel execution).
	AgentID string `json:"agent_id,omitempty"`
}

// ToolCall represents a tool invocation within a plan step.
type ToolCall struct {
	ToolName string         `json:"tool_name"`
	Input    map[string]any `json:"input,omitempty"`
}

// Plan represents a structured execution plan with ordered steps.
type Plan struct {
	mu sync.RWMutex

	// ID uniquely identifies this plan.
	ID string `json:"id"`

	// Title is the plan's display name.
	Title string `json:"title"`

	// Steps are the ordered execution steps.
	Steps []*PlanStep `json:"steps"`

	// Status is the overall plan status.
	Status PlanStatus `json:"status"`

	// Slug is the word-slug used for the plan file name.
	Slug string `json:"slug"`

	// FilePath is the path to the plan file on disk.
	FilePath string `json:"file_path,omitempty"`

	// CreatedAt is when the plan was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the plan was last modified.
	UpdatedAt time.Time `json:"updated_at"`
}

// NewPlan creates a new plan with draft status.
func NewPlan(title string) *Plan {
	return &Plan{
		ID:        generatePlanID(),
		Title:     title,
		Steps:     make([]*PlanStep, 0),
		Status:    PlanStatusDraft,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// AddStep appends a new step to the plan.
func (p *Plan) AddStep(description string, dependencies ...string) *PlanStep {
	p.mu.Lock()
	defer p.mu.Unlock()

	step := &PlanStep{
		ID:           fmt.Sprintf("step_%d", len(p.Steps)+1),
		Description:  description,
		Dependencies: dependencies,
		Status:       StepStatusPending,
	}
	p.Steps = append(p.Steps, step)
	p.UpdatedAt = time.Now()
	return step
}

// GetStep returns a step by ID.
func (p *Plan) GetStep(stepID string) *PlanStep {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, s := range p.Steps {
		if s.ID == stepID {
			return s
		}
	}
	return nil
}

// NextExecutableSteps returns all steps whose dependencies are met
// and that are still pending.
func (p *Plan) NextExecutableSteps() []*PlanStep {
	p.mu.RLock()
	defer p.mu.RUnlock()

	completed := make(map[string]bool)
	for _, s := range p.Steps {
		if s.Status == StepStatusCompleted {
			completed[s.ID] = true
		}
	}

	var ready []*PlanStep
	for _, s := range p.Steps {
		if s.Status != StepStatusPending {
			continue
		}
		allDepsCompleted := true
		for _, dep := range s.Dependencies {
			if !completed[dep] {
				allDepsCompleted = false
				break
			}
		}
		if allDepsCompleted {
			ready = append(ready, s)
		}
	}
	return ready
}

// ValidatePlan checks the plan for structural issues:
// - No duplicate step IDs
// - All dependency references are valid
// - No circular dependencies
// - At least one step exists
func ValidatePlan(p *Plan) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.Steps) == 0 {
		return errors.New("plan has no steps")
	}

	// Check for duplicate IDs.
	ids := make(map[string]bool)
	for _, s := range p.Steps {
		if ids[s.ID] {
			return fmt.Errorf("duplicate step ID: %s", s.ID)
		}
		ids[s.ID] = true
	}

	// Check all dependencies reference existing steps.
	for _, s := range p.Steps {
		for _, dep := range s.Dependencies {
			if !ids[dep] {
				return fmt.Errorf("step %s depends on unknown step %s", s.ID, dep)
			}
			if dep == s.ID {
				return fmt.Errorf("step %s depends on itself", s.ID)
			}
		}
	}

	// Check for circular dependencies using DFS.
	if err := detectCycles(p.Steps); err != nil {
		return err
	}

	return nil
}

// detectCycles checks for circular dependencies in the plan steps.
func detectCycles(steps []*PlanStep) error {
	// Build adjacency list.
	adj := make(map[string][]string)
	for _, s := range steps {
		adj[s.ID] = s.Dependencies
	}

	// States: 0=unvisited, 1=visiting, 2=visited.
	state := make(map[string]int)

	var visit func(id string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("circular dependency detected involving step %s", id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, dep := range adj[id] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}

	for _, s := range steps {
		if state[s.ID] == 0 {
			if err := visit(s.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// ExecutePlan runs the plan steps in dependency order.
// The execute function is called for each ready step. Multiple steps whose
// dependencies are all satisfied may run in sequence within a single call.
// Returns an error if any step fails.
func ExecutePlan(p *Plan, execute func(step *PlanStep) (string, error)) error {
	p.mu.Lock()
	p.Status = PlanStatusInProgress
	p.UpdatedAt = time.Now()
	p.mu.Unlock()

	for {
		ready := p.NextExecutableSteps()
		if len(ready) == 0 {
			break
		}

		for _, step := range ready {
			step.Status = StepStatusRunning
			step.StartedAt = time.Now()

			result, err := execute(step)
			step.CompletedAt = time.Now()

			if err != nil {
				step.Status = StepStatusFailed
				step.Error = err.Error()

				p.mu.Lock()
				p.Status = PlanStatusFailed
				p.UpdatedAt = time.Now()
				p.mu.Unlock()

				return fmt.Errorf("step %s failed: %w", step.ID, err)
			}

			step.Status = StepStatusCompleted
			step.Result = result
		}
	}

	// Check if all steps completed.
	p.mu.Lock()
	defer p.mu.Unlock()

	allCompleted := true
	for _, s := range p.Steps {
		if s.Status != StepStatusCompleted && s.Status != StepStatusSkipped {
			allCompleted = false
			break
		}
	}

	if allCompleted {
		p.Status = PlanStatusCompleted
	}
	p.UpdatedAt = time.Now()
	return nil
}

// PlanModeV2Config holds configuration for plan mode v2.
type PlanModeV2Config struct {
	// AgentCount is the number of implementation agents.
	AgentCount int

	// ExploreAgentCount is the number of exploration agents.
	ExploreAgentCount int

	// InterviewPhaseEnabled enables the interview phase.
	InterviewPhaseEnabled bool

	// PlansDirectory is the directory for plan files.
	PlansDirectory string
}

// DefaultPlanModeV2Config returns defaults based on subscription type.
func DefaultPlanModeV2Config() PlanModeV2Config {
	agentCount := 1
	if envCount := os.Getenv("CLAUDE_CODE_PLAN_V2_AGENT_COUNT"); envCount != "" {
		var n int
		if _, err := fmt.Sscanf(envCount, "%d", &n); err == nil && n > 0 && n <= 10 {
			agentCount = n
		}
	}

	exploreAgentCount := 3
	if envCount := os.Getenv("CLAUDE_CODE_PLAN_V2_EXPLORE_AGENT_COUNT"); envCount != "" {
		var n int
		if _, err := fmt.Sscanf(envCount, "%d", &n); err == nil && n > 0 && n <= 10 {
			exploreAgentCount = n
		}
	}

	return PlanModeV2Config{
		AgentCount:        agentCount,
		ExploreAgentCount: exploreAgentCount,
	}
}

// PlanFileManager manages plan files on disk.
type PlanFileManager struct {
	mu       sync.Mutex
	plansDir string
	slugs    map[string]string // sessionID -> slug
}

// NewPlanFileManager creates a manager for the given plans directory.
func NewPlanFileManager(plansDir string) *PlanFileManager {
	return &PlanFileManager{
		plansDir: plansDir,
		slugs:    make(map[string]string),
	}
}

// GetOrCreateSlug returns the word slug for a session, generating one if needed.
func (m *PlanFileManager) GetOrCreateSlug(sessionID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if slug, ok := m.slugs[sessionID]; ok {
		return slug
	}

	slug := generateWordSlug()
	// Ensure uniqueness by checking for file conflicts.
	for i := 0; i < 10; i++ {
		fp := filepath.Join(m.plansDir, slug+".md")
		if _, err := os.Stat(fp); os.IsNotExist(err) {
			break
		}
		slug = generateWordSlug()
	}

	m.slugs[sessionID] = slug
	return slug
}

// SetSlug explicitly sets the slug for a session (used on resume).
func (m *PlanFileManager) SetSlug(sessionID, slug string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.slugs[sessionID] = slug
}

// ClearSlug removes the slug for a session (called on /clear).
func (m *PlanFileManager) ClearSlug(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.slugs, sessionID)
}

// ClearAllSlugs removes all slug entries.
func (m *PlanFileManager) ClearAllSlugs() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.slugs = make(map[string]string)
}

// GetPlanFilePath returns the file path for a session's plan.
// If agentID is provided, returns the subagent plan path.
func (m *PlanFileManager) GetPlanFilePath(sessionID, agentID string) string {
	slug := m.GetOrCreateSlug(sessionID)
	if agentID == "" {
		return filepath.Join(m.plansDir, slug+".md")
	}
	return filepath.Join(m.plansDir, fmt.Sprintf("%s-agent-%s.md", slug, agentID))
}

// ReadPlan reads the plan content from disk.
func (m *PlanFileManager) ReadPlan(sessionID, agentID string) (string, error) {
	fp := m.GetPlanFilePath(sessionID, agentID)
	data, err := os.ReadFile(fp)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// WritePlan writes plan content to disk.
func (m *PlanFileManager) WritePlan(sessionID, agentID, content string) error {
	if err := os.MkdirAll(m.plansDir, 0o755); err != nil {
		return err
	}
	fp := m.GetPlanFilePath(sessionID, agentID)
	return os.WriteFile(fp, []byte(content), 0o644)
}

// EnsureDir creates the plans directory if it doesn't exist.
func (m *PlanFileManager) EnsureDir() error {
	return os.MkdirAll(m.plansDir, 0o755)
}

func generatePlanID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "plan_" + hex.EncodeToString(b)
}

// generateWordSlug creates a short human-readable slug.
// Uses a small word list for memorable plan file names.
func generateWordSlug() string {
	adjectives := []string{
		"quick", "bright", "calm", "bold", "keen",
		"swift", "warm", "clear", "deep", "fresh",
		"light", "neat", "sharp", "soft", "wide",
		"cool", "fair", "firm", "glad", "pure",
	}
	nouns := []string{
		"plan", "task", "work", "idea", "goal",
		"step", "path", "flow", "code", "test",
		"arch", "beam", "core", "edge", "form",
		"grid", "link", "node", "root", "view",
	}

	b := make([]byte, 2)
	rand.Read(b)
	adj := adjectives[int(b[0])%len(adjectives)]
	noun := nouns[int(b[1])%len(nouns)]

	// Add a short random suffix for uniqueness.
	suffix := make([]byte, 2)
	rand.Read(suffix)

	return strings.Join([]string{adj, noun, hex.EncodeToString(suffix)}, "-")
}
