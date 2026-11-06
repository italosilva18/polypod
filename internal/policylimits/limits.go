// Package policylimits enforces organization-level usage policies.
// It loads policy definitions from YAML files and checks runtime usage
// (tokens, cost, rate, allowed models, blocked tools) against them.
package policylimits

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Policy defines the organization-level usage constraints.
type Policy struct {
	MaxTokensPerSession  int      `yaml:"max_tokens_per_session" json:"max_tokens_per_session"`
	MaxTokensPerDay      int      `yaml:"max_tokens_per_day" json:"max_tokens_per_day"`
	MaxRequestsPerMinute int      `yaml:"max_requests_per_minute" json:"max_requests_per_minute"`
	MaxCostPerDay        float64  `yaml:"max_cost_per_day" json:"max_cost_per_day"`
	AllowedModels        []string `yaml:"allowed_models" json:"allowed_models"`
	BlockedTools         []string `yaml:"blocked_tools" json:"blocked_tools"`
	RequireApproval      bool     `yaml:"require_approval" json:"require_approval"`
}

// PolicyStore loads and holds the active policy from a YAML file or remote endpoint.
type PolicyStore struct {
	mu     sync.RWMutex
	path   string
	logger *slog.Logger
	policy *Policy
}

// NewPolicyStore creates a PolicyStore that reads from the given YAML file path.
func NewPolicyStore(path string, logger *slog.Logger) *PolicyStore {
	if logger == nil {
		logger = slog.Default()
	}
	return &PolicyStore{
		path:   path,
		logger: logger,
	}
}

// Load reads and parses the YAML policy file. If the file does not exist or
// is empty, a zero-value policy is stored (all limits disabled).
func (ps *PolicyStore) Load() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	data, err := os.ReadFile(ps.path)
	if err != nil {
		if os.IsNotExist(err) {
			ps.logger.Warn("policy file not found, using empty policy", "path", ps.path)
			ps.policy = &Policy{}
			return nil
		}
		return fmt.Errorf("policylimits: read %s: %w", ps.path, err)
	}

	var p Policy
	if err := yaml.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("policylimits: parse %s: %w", ps.path, err)
	}

	ps.policy = &p
	ps.logger.Info("policy loaded",
		"path", ps.path,
		"max_tokens_per_session", p.MaxTokensPerSession,
		"max_tokens_per_day", p.MaxTokensPerDay,
		"max_requests_per_minute", p.MaxRequestsPerMinute,
		"max_cost_per_day", p.MaxCostPerDay,
		"allowed_models", len(p.AllowedModels),
		"blocked_tools", len(p.BlockedTools),
		"require_approval", p.RequireApproval,
	)
	return nil
}

// Get returns the current active policy. If no policy has been loaded yet
// it returns a zero-value (all limits disabled) policy.
func (ps *PolicyStore) Get() *Policy {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if ps.policy == nil {
		return &Policy{}
	}
	// Return a copy so callers cannot mutate the store's state.
	cp := *ps.policy
	cp.AllowedModels = append([]string(nil), ps.policy.AllowedModels...)
	cp.BlockedTools = append([]string(nil), ps.policy.BlockedTools...)
	return &cp
}

// ---------------------------------------------------------------------------
// Enforcer
// ---------------------------------------------------------------------------

// Enforcer checks runtime usage against the active policy.
type Enforcer struct {
	store *PolicyStore

	mu             sync.Mutex
	minuteRequests []time.Time // timestamps of recent requests
}

// NewEnforcer creates an Enforcer backed by the given PolicyStore.
func NewEnforcer(store *PolicyStore) *Enforcer {
	return &Enforcer{store: store}
}

// CheckTokenLimit returns an error if the number of tokens used in the
// current session exceeds the policy's MaxTokensPerSession.
func (e *Enforcer) CheckTokenLimit(used int) error {
	p := e.store.Get()
	if p.MaxTokensPerSession > 0 && used >= p.MaxTokensPerSession {
		return fmt.Errorf("policy violation: token usage (%d) exceeds session limit (%d)",
			used, p.MaxTokensPerSession)
	}
	return nil
}

// CheckCostLimit returns an error if the accumulated cost exceeds the
// policy's MaxCostPerDay.
func (e *Enforcer) CheckCostLimit(cost float64) error {
	p := e.store.Get()
	if p.MaxCostPerDay > 0 && cost >= p.MaxCostPerDay {
		return fmt.Errorf("policy violation: daily cost ($%.4f) exceeds limit ($%.4f)",
			cost, p.MaxCostPerDay)
	}
	return nil
}

// CheckRateLimit records a request and returns an error if the number of
// requests in the current minute exceeds the policy's MaxRequestsPerMinute.
func (e *Enforcer) CheckRateLimit() error {
	p := e.store.Get()
	if p.MaxRequestsPerMinute <= 0 {
		return nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Minute)

	// Prune old entries.
	i := 0
	for i < len(e.minuteRequests) && e.minuteRequests[i].Before(cutoff) {
		i++
	}
	e.minuteRequests = e.minuteRequests[i:]

	// Record the current request.
	e.minuteRequests = append(e.minuteRequests, now)

	if len(e.minuteRequests) > p.MaxRequestsPerMinute {
		return fmt.Errorf("policy violation: requests per minute (%d) exceeds limit (%d)",
			len(e.minuteRequests), p.MaxRequestsPerMinute)
	}
	return nil
}

// IsModelAllowed returns true if the model is permitted by the policy.
// When the AllowedModels list is empty every model is allowed.
func (e *Enforcer) IsModelAllowed(model string) bool {
	p := e.store.Get()
	if len(p.AllowedModels) == 0 {
		return true
	}
	for _, m := range p.AllowedModels {
		if m == model {
			return true
		}
	}
	return false
}

// IsToolBlocked returns true if the tool is on the policy's blocked list.
func (e *Enforcer) IsToolBlocked(tool string) bool {
	p := e.store.Get()
	for _, t := range p.BlockedTools {
		if t == tool {
			return true
		}
	}
	return false
}

// CheckAll runs every policy check and returns a list of human-readable
// violation descriptions. An empty slice means all checks passed.
func (e *Enforcer) CheckAll(model string, tokensUsed int, cost float64) []string {
	var violations []string

	if err := e.CheckTokenLimit(tokensUsed); err != nil {
		violations = append(violations, err.Error())
	}

	if err := e.CheckCostLimit(cost); err != nil {
		violations = append(violations, err.Error())
	}

	if err := e.CheckRateLimit(); err != nil {
		violations = append(violations, err.Error())
	}

	if !e.IsModelAllowed(model) {
		violations = append(violations, fmt.Sprintf(
			"policy violation: model %q is not in the allowed list", model))
	}

	p := e.store.Get()
	if p.RequireApproval {
		violations = append(violations, "policy requires manual approval for this request")
	}

	return violations
}
