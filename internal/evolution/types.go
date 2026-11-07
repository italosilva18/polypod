// Package evolution turns verified operational outcomes into reviewed skill drafts.
package evolution

import "errors"

var (
	ErrNotFound      = errors.New("registro de evolução não encontrado")
	ErrNotEligible   = errors.New("padrão ainda não tem evidência suficiente")
	ErrStateConflict = errors.New("estado do rascunho não permite esta ação")
)

type OutcomeInput struct {
	SourceKind  string   `json:"source_kind"` // task, routine, or manual
	SourceID    string   `json:"source_id"`   // stable ID of the verified run
	PatternKey  string   `json:"pattern_key"` // caller-assigned stable task category
	Success     bool     `json:"success"`
	VerifiedBy  string   `json:"verified_by"` // assertion by the trusted caller, not proof by this package
	EvidenceRef string   `json:"evidence_ref"`
	ToolNames   []string `json:"tool_names"`
}

// VerificationPlanInput describes an independent file-state check for a queued
// task or routine. SubjectID is the stable task ID; each completed run uses its
// own RunID when the verified outcome is recorded.
type VerificationPlanInput struct {
	SourceKind     string `json:"source_kind"`
	SubjectID      string `json:"subject_id"`
	PatternKey     string `json:"pattern_key"`
	Kind           string `json:"kind"` // currently file_sha256
	Path           string `json:"path"`
	ExpectedSHA256 string `json:"expected_sha256"`
}

type VerificationPlan struct {
	VerificationPlanInput
	BaselineSHA256 string `json:"baseline_sha256"`
	CreatedAt      int64  `json:"created_at"`
}

type Outcome struct {
	ID          string   `json:"id"`
	SourceKind  string   `json:"source_kind"`
	SourceID    string   `json:"source_id"`
	PatternKey  string   `json:"pattern_key"`
	Success     bool     `json:"success"`
	VerifiedBy  string   `json:"verified_by"`
	EvidenceRef string   `json:"evidence_ref"`
	ToolNames   []string `json:"tool_names"`
	CreatedAt   int64    `json:"created_at"`
}

type Pattern struct {
	Key          string `json:"key"`
	SuccessCount int    `json:"success_count"`
	TotalCount   int    `json:"total_count"`
	Eligible     bool   `json:"eligible"`
}

type Evidence struct {
	OutcomeID   string `json:"outcome_id"`
	SourceKind  string `json:"source_kind"`
	SourceID    string `json:"source_id"`
	Success     bool   `json:"success"`
	VerifiedBy  string `json:"verified_by"`
	EvidenceRef string `json:"evidence_ref"`
}

type DraftStatus string

const (
	Candidate DraftStatus = "candidate"
	Approved  DraftStatus = "approved"
	Applying  DraftStatus = "applying"
	Applied   DraftStatus = "applied"
	Discarded DraftStatus = "discarded"
)

type Draft struct {
	ID           string      `json:"id"`
	PatternKey   string      `json:"pattern_key"`
	TargetSkill  string      `json:"target_skill"`
	Body         string      `json:"body"`
	BodySHA256   string      `json:"body_sha256"`
	Status       DraftStatus `json:"status"`
	Evidence     []Evidence  `json:"evidence"`
	SuccessCount int         `json:"success_count"`
	TotalCount   int         `json:"total_count"`
	CreatedAt    int64       `json:"created_at"`
	UpdatedAt    int64       `json:"updated_at"`
	ReviewedBy   string      `json:"reviewed_by,omitempty"`
	ReviewedAt   int64       `json:"reviewed_at,omitempty"`
	AppliedAt    int64       `json:"applied_at,omitempty"`
}
