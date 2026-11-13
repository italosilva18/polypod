package evaluation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Execution is the measured output of one complete agent turn. Response is
// available to a verifier but is not copied into the results artifact, because
// it can contain private user data or secrets.
type Execution struct {
	Response      string
	CostUSD       *float64
	Steps         *int
	Tokens        *int
	ToolFailures  *int
	AgentExitCode *int
	Trace         string
}

// Executor runs a task in an isolated test environment. It must honor ctx:
// cancellation is cooperative and cannot undo external effects already made.
type Executor func(ctx context.Context, task Task) (Execution, error)

// Verification is a judgment from outside the agent. A success needs evidence
// and the identity of the verifier. It may also report recovery for tasks that
// deliberately inject a failure.
type Verification struct {
	Status     string
	Evidence   string
	VerifiedBy string
	Recovered  *bool
}

// Verifier inspects external state after the executor returns. The answer
// text alone is not evidence of a GUI, channel, routine or safety effect.
type Verifier func(ctx context.Context, task Task, execution Execution) (Verification, error)

type RunOptions struct {
	Executor         Executor
	Verifier         Verifier
	ExternalEvidence map[string]Verification
}

// Run executes every task in manifest order, enforcing a per-task deadline
// bounded by Metadata.Budget.MaxSeconds. It returns Results compatible with
// Evaluate. Without a verifier or explicit external evidence, a completed
// execution is marked skipped rather than presumed successful.
func Run(ctx context.Context, manifest Manifest, metadata Metadata, opts RunOptions) (Results, error) {
	results := Results{
		SchemaVersion:   SchemaVersion,
		ManifestID:      manifest.ID,
		ManifestVersion: manifest.Version,
		Metadata:        metadata,
		Outcomes:        make([]Outcome, 0, len(manifest.Tasks)),
	}
	if opts.Executor == nil {
		return results, errors.New("executor de avaliação obrigatório")
	}
	if _, err := Evaluate(manifest, results); err != nil {
		return results, fmt.Errorf("manifesto ou metadados inválidos: %w", err)
	}
	budgetTimeout, err := secondsToDuration(metadata.Budget.MaxSeconds)
	if err != nil {
		return results, fmt.Errorf("orçamento de tempo inválido: %w", err)
	}
	known := make(map[string]bool, len(manifest.Tasks))
	for _, task := range manifest.Tasks {
		known[task.ID] = true
		if strings.TrimSpace(task.Prompt) == "" {
			return results, fmt.Errorf("tarefa %q sem prompt", task.ID)
		}
		if task.TimeoutSeconds < 0 {
			return results, fmt.Errorf("tarefa %q com timeout negativo", task.ID)
		}
		if task.TimeoutSeconds > 0 {
			if _, err := secondsToDuration(float64(task.TimeoutSeconds)); err != nil {
				return results, fmt.Errorf("timeout da tarefa %q inválido: %w", task.ID, err)
			}
		}
	}
	for taskID, verification := range opts.ExternalEvidence {
		if !known[taskID] {
			return results, fmt.Errorf("evidência para tarefa desconhecida: %q", taskID)
		}
		if err := validateVerification(manifestTask(manifest, taskID), verification); err != nil {
			return results, fmt.Errorf("evidência externa de %q: %w", taskID, err)
		}
	}
	for _, task := range manifest.Tasks {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		timeout := budgetTimeout
		if task.TimeoutSeconds > 0 {
			caseTimeout, _ := secondsToDuration(float64(task.TimeoutSeconds))
			if caseTimeout < timeout {
				timeout = caseTimeout
			}
		}
		caseCtx, cancel := context.WithTimeout(ctx, timeout)
		start := time.Now()
		execution, execErr := opts.Executor(caseCtx, task)
		latency := float64(time.Since(start)) / float64(time.Millisecond)
		outcome := Outcome{TaskID: task.ID, LatencyMS: &latency}
		if validExecution(execution) {
			outcome.CostUSD, outcome.Steps, outcome.Trace = execution.CostUSD, execution.Steps, execution.Trace
			outcome.TokenCount, outcome.ToolFailures = execution.Tokens, execution.ToolFailures
		}
		if err := ctx.Err(); err != nil {
			cancel()
			return results, err
		}
		if errors.Is(caseCtx.Err(), context.DeadlineExceeded) || errors.Is(execErr, context.DeadlineExceeded) {
			outcome.Status = "timeout"
		} else if execErr != nil {
			outcome.Status = "error"
		} else if !validExecution(execution) {
			outcome.Status = "error"
		} else {
			verification, exists := opts.ExternalEvidence[task.ID]
			if !exists && opts.Verifier != nil {
				verification, err = opts.Verifier(caseCtx, task, execution)
				if errors.Is(caseCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
					outcome.Status = "timeout"
				} else if err != nil {
					outcome.Status = "error"
				} else {
					exists = true
				}
			}
			if exists && outcome.Status == "" {
				if err := validateVerification(task, verification); err != nil {
					cancel()
					return results, fmt.Errorf("verificação de %q inválida: %w", task.ID, err)
				}
				outcome.Status = verification.Status
				outcome.Evidence = verification.Evidence
				outcome.VerifiedBy = verification.VerifiedBy
				outcome.Recovered = verification.Recovered
			}
			if outcome.Status == "" {
				outcome.Status = "skipped"
			}
		}
		cancel()
		latency = float64(time.Since(start)) / float64(time.Millisecond)
		results.Outcomes = append(results.Outcomes, outcome)
	}
	if _, err := Evaluate(manifest, results); err != nil {
		return results, fmt.Errorf("resultados de avaliação inválidos: %w", err)
	}
	return results, nil
}

func manifestTask(m Manifest, id string) Task {
	for _, task := range m.Tasks {
		if task.ID == id {
			return task
		}
	}
	return Task{}
}

func secondsToDuration(seconds float64) (time.Duration, error) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0.001 || seconds > float64(int64(^uint64(0)>>1))/float64(time.Second) {
		return 0, errors.New("o tempo deve ser finito e representável em milissegundos")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func validExecution(result Execution) bool {
	return validNumber(result.CostUSD) && (result.Steps == nil || *result.Steps >= 0) &&
		(result.Tokens == nil || *result.Tokens >= 0) && (result.ToolFailures == nil || *result.ToolFailures >= 0)
}

func validateVerification(task Task, verification Verification) error {
	switch verification.Status {
	case "success", "failure", "skipped":
	default:
		return errors.New("status deve ser success, failure ou skipped")
	}
	if verification.Status == "success" && (strings.TrimSpace(verification.Evidence) == "" || strings.TrimSpace(verification.VerifiedBy) == "") {
		return errors.New("sucesso exige evidência e verificador externo")
	}
	if verification.Recovered != nil && (!task.Recovery || (*verification.Recovered && verification.Status != "success")) {
		return errors.New("recuperação incompatível com a tarefa ou o status")
	}
	return nil
}
