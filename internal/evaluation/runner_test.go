package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runnableFixture(t *testing.T) (Manifest, Metadata) {
	t.Helper()
	m, r := fixture(t)
	m.Tasks = m.Tasks[:2]
	for i := range m.Tasks {
		m.Tasks[i].Prompt = "Execute o teste " + m.Tasks[i].ID
	}
	return m, r.Metadata
}

func TestRunUsesExternalVerifierAndKeepsResponsesPrivate(t *testing.T) {
	m, md := runnableFixture(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "effect.txt")
	calls := 0
	results, err := Run(context.Background(), m, md, RunOptions{
		Executor: func(ctx context.Context, task Task) (Execution, error) {
			calls++
			if task.ID == m.Tasks[0].ID {
				if err := os.WriteFile(path, []byte("feito"), 0600); err != nil {
					return Execution{}, err
				}
			}
			cost := 0.003
			steps := 2
			return Execution{Response: "segredo-do-teste", CostUSD: &cost, Steps: &steps}, nil
		},
		Verifier: func(ctx context.Context, task Task, execution Execution) (Verification, error) {
			if execution.Response != "segredo-do-teste" {
				t.Fatal("verifier did not receive response")
			}
			if task.ID == m.Tasks[0].ID {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "feito" {
					return Verification{Status: "failure"}, nil
				}
				return Verification{Status: "success", Evidence: path, VerifiedBy: "filesystem-checker"}, nil
			}
			return Verification{Status: "failure", Evidence: "arquivo ausente", VerifiedBy: "filesystem-checker"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(results.Outcomes) != 2 || results.Outcomes[0].Status != "success" || results.Outcomes[1].Status != "failure" {
		t.Fatalf("unexpected run: calls=%d outcomes=%+v", calls, results.Outcomes)
	}
	if results.Outcomes[0].CostUSD == nil || *results.Outcomes[0].CostUSD != 0.003 {
		t.Fatalf("missing cost: %+v", results.Outcomes[0])
	}
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "segredo-do-teste") {
		t.Fatal("response copied into results artifact")
	}
	report, err := Evaluate(m, results)
	if err != nil || report.Overall.Success != 1 || report.Overall.Reported != 2 {
		t.Fatalf("incompatible report: %+v, %v", report.Overall, err)
	}
}

func TestRunWithoutVerificationDoesNotInferSuccessFromAnswer(t *testing.T) {
	m, md := runnableFixture(t)
	m.Tasks = m.Tasks[:1]
	exec := func(context.Context, Task) (Execution, error) {
		return Execution{Response: "Concluído com sucesso"}, nil
	}
	unverified, err := Run(context.Background(), m, md, RunOptions{Executor: exec})
	if err != nil || unverified.Outcomes[0].Status != "skipped" {
		t.Fatalf("self-report scored: %+v, %v", unverified.Outcomes, err)
	}
	verified, err := Run(context.Background(), m, md, RunOptions{Executor: exec,
		ExternalEvidence: map[string]Verification{m.Tasks[0].ID: {
			Status: "success", Evidence: "sha256:external-artifact", VerifiedBy: "external-checker",
		}},
	})
	if err != nil || verified.Outcomes[0].Status != "success" {
		t.Fatalf("external evidence ignored: %+v, %v", verified.Outcomes, err)
	}
}

func TestRunEnforcesTimeoutAndContinues(t *testing.T) {
	m, md := runnableFixture(t)
	md.Budget.MaxSeconds = 0.02
	calls := 0
	results, err := Run(context.Background(), m, md, RunOptions{
		Executor: func(ctx context.Context, task Task) (Execution, error) {
			calls++
			if calls == 1 {
				<-ctx.Done()
				return Execution{}, ctx.Err()
			}
			return Execution{Response: "ok"}, nil
		},
		Verifier: func(context.Context, Task, Execution) (Verification, error) {
			return Verification{Status: "failure"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || results.Outcomes[0].Status != "timeout" || results.Outcomes[1].Status != "failure" {
		t.Fatalf("timeout/continuation: %+v", results.Outcomes)
	}
	if results.Outcomes[0].LatencyMS == nil || *results.Outcomes[0].LatencyMS < 15 {
		t.Fatalf("timeout not measured: %+v", results.Outcomes[0])
	}
}

func TestRunRejectsInvalidPlanBeforeEffects(t *testing.T) {
	m, md := runnableFixture(t)
	calls := 0
	exec := func(context.Context, Task) (Execution, error) { calls++; return Execution{}, nil }
	cases := []struct {
		name   string
		change func(*Manifest, *Metadata, *RunOptions)
	}{
		{"missing prompt", func(m *Manifest, _ *Metadata, _ *RunOptions) { m.Tasks[1].Prompt = " " }},
		{"negative timeout", func(m *Manifest, _ *Metadata, _ *RunOptions) { m.Tasks[1].TimeoutSeconds = -1 }},
		{"unknown evidence", func(_ *Manifest, _ *Metadata, o *RunOptions) {
			o.ExternalEvidence = map[string]Verification{"alien": {Status: "failure"}}
		}},
		{"false success", func(m *Manifest, _ *Metadata, o *RunOptions) {
			o.ExternalEvidence = map[string]Verification{m.Tasks[0].ID: {Status: "success"}}
		}},
		{"invalid budget", func(_ *Manifest, md *Metadata, _ *RunOptions) { md.Budget.MaxSeconds = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			copyManifest := m
			copyManifest.Tasks = append([]Task(nil), m.Tasks...)
			copyMetadata := md
			opts := RunOptions{Executor: exec}
			tc.change(&copyManifest, &copyMetadata, &opts)
			if _, err := Run(context.Background(), copyManifest, copyMetadata, opts); err == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
	if calls != 0 {
		t.Fatalf("invalid plans ran executor %d times", calls)
	}
}

func TestRunVerifierDeadlineAndExecutionError(t *testing.T) {
	m, md := runnableFixture(t)
	md.Budget.MaxSeconds = 0.02
	results, err := Run(context.Background(), m, md, RunOptions{
		Executor: func(context.Context, Task) (Execution, error) {
			cost := 0.01
			return Execution{CostUSD: &cost}, nil
		},
		Verifier: func(ctx context.Context, task Task, execution Execution) (Verification, error) {
			if task.ID == m.Tasks[0].ID {
				<-ctx.Done()
				return Verification{}, ctx.Err()
			}
			return Verification{}, errors.New("verificador indisponível")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if results.Outcomes[0].Status != "timeout" || results.Outcomes[1].Status != "error" {
		t.Fatalf("verifier status: %+v", results.Outcomes)
	}
	for _, outcome := range results.Outcomes {
		if outcome.CostUSD == nil || *outcome.CostUSD != 0.01 {
			t.Fatalf("cost of failed run missing: %+v", outcome)
		}
	}
}

func TestRunCancellationReturnsPartialResults(t *testing.T) {
	m, md := runnableFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	results, err := Run(ctx, m, md, RunOptions{Executor: func(context.Context, Task) (Execution, error) {
		calls++
		cancel()
		return Execution{}, nil
	}})
	if !errors.Is(err, context.Canceled) || calls != 1 || len(results.Outcomes) != 0 {
		t.Fatalf("cancellation: calls=%d outcomes=%+v err=%v", calls, results.Outcomes, err)
	}
}

func TestRunRespectsTaskTimeoutSmallerThanBudget(t *testing.T) {
	m, md := runnableFixture(t)
	m.Tasks = m.Tasks[:1]
	m.Tasks[0].TimeoutSeconds = 1
	md.Budget.MaxSeconds = 2
	start := time.Now()
	results, err := Run(context.Background(), m, md, RunOptions{Executor: func(ctx context.Context, task Task) (Execution, error) {
		<-ctx.Done()
		return Execution{}, ctx.Err()
	}})
	if err != nil || results.Outcomes[0].Status != "timeout" || time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("task timeout not applied: %+v, %v", results.Outcomes, err)
	}
}
