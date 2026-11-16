// Package verifier — Verification System extracted from Claude Code
// Sources:
// - src/skills/bundled/verify.ts
// - src/skills/bundled/verifyContent.ts

package verifier

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Criterion represents a verification criterion
type Criterion struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Command     string `json:"command,omitempty"`
	Expected    string `json:"expected,omitempty"`
}

// VerificationResult holds the result of a verification
type VerificationResult struct {
	Name       string             `json:"name"`
	Passed     bool               `json:"passed"`
	Criteria   []CriterionResult  `json:"criteria"`
	StartedAt  time.Time          `json:"started_at"`
	CompletedAt time.Time         `json:"completed_at"`
	Message    string             `json:"message,omitempty"`
}

// CriterionResult holds the result of a single criterion
type CriterionResult struct {
	Criterion Criterion `json:"criterion"`
	Passed    bool      `json:"passed"`
	Message   string    `json:"message,omitempty"`
	Error     error     `json:"error,omitempty"`
}

// Verifier verifies code changes
type Verifier struct {
	name     string
	criteria []Criterion
	workDir  string
}

// NewVerifier creates a new verifier
func NewVerifier(name string, criteria []Criterion, workDir string) *Verifier {
	return &Verifier{
		name:     name,
		criteria: criteria,
		workDir:  workDir,
	}
}

// Run runs all verifications
func (v *Verifier) Run() (*VerificationResult, error) {
	result := &VerificationResult{
		Name:       v.name,
		Passed:     true,
		StartedAt:  time.Now(),
		criteria:   make([]CriterionResult, 0, len(v.criteria)),
	}

	for _, criterion := range v.criteria {
		criterionResult := v.verifyCriterion(criterion)
		result.Criteria = append(result.Criteria, criterionResult)
		
		if !criterionResult.Passed {
			result.Passed = false
		}
	}

	result.CompletedAt = time.Now()
	
	if result.Passed {
		result.Message = "All criteria passed"
	} else {
		result.Message = "Some criteria failed"
	}

	return result, nil
}

func (v *Verifier) verifyCriterion(criterion Criterion) CriterionResult {
	result := CriterionResult{
		Criterion: criterion,
		Passed:    false,
	}

	// If command is specified, run it
	if criterion.Command != "" {
		cmd := exec.Command("bash", "-c", criterion.Command)
		cmd.Dir = v.workDir
		output, err := cmd.CombinedOutput()
		
		if err != nil {
			result.Message = fmt.Sprintf("Command failed: %v\nOutput: %s", err, string(output))
			result.Error = err
			return result
		}

		// Check expected output if specified
		if criterion.Expected != "" {
			if strings.Contains(string(output), criterion.Expected) {
				result.Passed = true
				result.Message = "Expected output found"
			} else {
				result.Message = fmt.Sprintf("Expected '%s' not found in output", criterion.Expected)
			}
		} else {
			// Command succeeded, criterion passed
			result.Passed = true
			result.Message = "Command executed successfully"
		}
	} else {
		// No command, just check description
		result.Passed = true
		result.Message = "Criterion checked"
	}

	return result
}

// CheckCriteria checks if all criteria pass
func (v *Verifier) CheckCriteria() bool {
	for _, criterion := range v.criteria {
		result := v.verifyCriterion(criterion)
		if !result.Passed {
			return false
		}
	}
	return true
}

// AddCriterion adds a criterion
func (v *Verifier) AddCriterion(name, description, command, expected string) {
	v.criteria = append(v.criteria, Criterion{
		Name:        name,
		Description: description,
		Command:     command,
		Expected:    expected,
	})
}

// BuildVerifier creates a verifier for build
func BuildVerifier(workDir string) *Verifier {
	v := NewVerifier("Build", []Criterion{
		{
			Name:        "Build Compiles",
			Description: "Code compiles without errors",
			Command:     "go build ./...",
		},
	}, workDir)
	
	return v
}

// TestVerifier creates a verifier for tests
func TestVerifier(workDir string) *Verifier {
	v := NewVerifier("Tests", []Criterion{
		{
			Name:        "Tests Pass",
			Description: "All tests pass",
			Command:     "go test ./...",
		},
	}, workDir)
	
	return v
}

// LintVerifier creates a verifier for linting
func LintVerifier(workDir string) *Verifier {
	v := NewVerifier("Lint", []Criterion{
		{
			Name:        "No Lint Errors",
			Description: "Code passes linter",
			Command:     "golangci-lint run",
		},
	}, workDir)
	
	return v
}

// SecurityVerifier creates a verifier for security checks
func SecurityVerifier(workDir string) *Verifier {
	v := NewVerifier("Security", []Criterion{
		{
			Name:        "No Security Issues",
			Description: "No security vulnerabilities found",
			Command:     "gosec ./...",
		},
	}, workDir)
	
	return v
}

// FullVerifier creates a comprehensive verifier
func FullVerifier(workDir string) *Verifier {
	v := NewVerifier("Full Verification", []Criterion{
		{
			Name:        "Build Compiles",
			Description: "Code compiles without errors",
			Command:     "go build ./...",
		},
		{
			Name:        "Tests Pass",
			Description: "All tests pass",
			Command:     "go test ./...",
		},
		{
			Name:        "No Lint Errors",
			Description: "Code passes linter",
			Command:     "golangci-lint run",
		},
	}, workDir)
	
	return v
}

// FormatResult formats verification result
func FormatResult(result *VerificationResult) string {
	var sb strings.Builder
	
	status := "✅ PASSED"
	if !result.Passed {
		status = "❌ FAILED"
	}
	
	sb.WriteString(fmt.Sprintf("%s: %s\n\n", result.Name, status))
	sb.WriteString(fmt.Sprintf("Started: %s\n", result.StartedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("Completed: %s\n", result.CompletedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("Duration: %s\n\n", result.CompletedAt.Sub(result.StartedAt)))
	
	sb.WriteString("Criteria:\n")
	for i, criterion := range result.Criteria {
		check := "✅"
		if !criterion.Passed {
			check = "❌"
		}
		
		sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, check, criterion.Criterion.Name))
		if criterion.Message != "" {
			sb.WriteString(fmt.Sprintf("   %s\n", criterion.Message))
		}
		if criterion.Error != nil {
			sb.WriteString(fmt.Sprintf("   Error: %v\n", criterion.Error))
		}
	}
	
	if result.Message != "" {
		sb.WriteString(fmt.Sprintf("\n%s\n", result.Message))
	}
	
	return sb.String()
}

// SaveResult saves verification result to file
func SaveResult(result *VerificationResult, filePath string) error {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	
	content := FormatResult(result)
	return os.WriteFile(filePath, []byte(content), 0644)
}

// LoadResult loads verification result from file
func LoadResult(filePath string) (*VerificationResult, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	
	// Simple parsing - in production use proper format
	return &VerificationResult{
		Name:    "Loaded Result",
		Message: string(data),
	}, nil
}
