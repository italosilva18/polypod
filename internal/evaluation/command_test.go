package evaluation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCommandRunnerVerifiesCodingTaskWithExternalCommand(t *testing.T) {
	workspace := t.TempDir()
	caseInfo := CommandCase{
		TaskID: "fix-code", WorkingDir: workspace,
		Command:       []string{"/bin/echo", "{prompt}"},
		VerifyCommand: []string{"/bin/sh", "-c", "grep -q 'return 42' feature.go"},
	}
	manifest := Manifest{Tasks: []Task{{ID: "fix-code", Category: "coding"}}}
	runner, err := NewCommandRunner(manifest, CommandPlan{Cases: []CommandCase{caseInfo}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "feature.go")
	if err := os.WriteFile(path, []byte("package p\nfunc Value() int { return 41 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed, err := runner.Verify(context.Background(), manifest.Tasks[0], Execution{})
	if err != nil || failed.Status != "failure" || failed.VerifiedBy != "external_command" {
		t.Fatalf("wrong code was accepted: %+v, %v", failed, err)
	}
	if err := os.WriteFile(path, []byte("package p\nfunc Value() int { return 42 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	passed, err := runner.Verify(context.Background(), manifest.Tasks[0], Execution{})
	if err != nil || passed.Status != "success" || passed.VerifiedBy != "external_command" || passed.Evidence != "exit_code:0" {
		t.Fatalf("verified code was rejected: %+v, %v", passed, err)
	}
}

func TestCommandRunnerRejectsRelativeVerifierExecutable(t *testing.T) {
	workspace := t.TempDir()
	manifest := Manifest{Tasks: []Task{{ID: "fix-code", Category: "coding"}}}
	_, err := NewCommandRunner(manifest, CommandPlan{Cases: []CommandCase{{
		TaskID: "fix-code", WorkingDir: workspace, Command: []string{"/bin/echo", "{prompt}"},
		VerifyCommand: []string{"sh", "-c", "true"},
	}}})
	if err == nil {
		t.Fatal("relative verifier executable was accepted")
	}
}

func TestCommandRunnerImportsMeasuredLocalUsage(t *testing.T) {
	workspace := t.TempDir()
	program := filepath.Join(workspace, "agent.sh")
	content := "#!/bin/sh\nprintf '%s\\n' '{\"content\":\"feito\",\"cost_measured\":true,\"cost_usd\":0,\"steps\":3,\"tool_failures\":1,\"tokens\":{\"total\":123}}'\n"
	if err := os.WriteFile(program, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Tasks: []Task{{ID: "local", Category: "coding"}}}
	runner, err := NewCommandRunner(manifest, CommandPlan{Cases: []CommandCase{{TaskID: "local", WorkingDir: workspace, Command: []string{program, "{prompt}"}}}})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := runner.Execute(context.Background(), Task{ID: "local", Prompt: "corrija"})
	if err != nil {
		t.Fatal(err)
	}
	if execution.CostUSD == nil || *execution.CostUSD != 0 || execution.Steps == nil || *execution.Steps != 3 ||
		execution.Tokens == nil || *execution.Tokens != 123 || execution.ToolFailures == nil || *execution.ToolFailures != 1 {
		t.Fatalf("measured usage lost: %+v", execution)
	}
}

func TestCommandRunnerVerifiesPatchAfterAgentExitError(t *testing.T) {
	workspace := t.TempDir()
	agent := filepath.Join(workspace, "agent.sh")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\nprintf 'package p\\nfunc Value() int { return 42 }\\n' > feature.go\nexit 9\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	task := Task{ID: "fix-code", Category: "coding", Prompt: "fix"}
	runner, err := NewCommandRunner(Manifest{Tasks: []Task{task}}, CommandPlan{Cases: []CommandCase{{
		TaskID: task.ID, WorkingDir: workspace, Command: []string{agent, "{prompt}"},
		VerifyCommand: []string{"/bin/sh", "-c", "grep -q 'return 42' feature.go"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := runner.Execute(context.Background(), task)
	if err != nil || execution.AgentExitCode == nil || *execution.AgentExitCode != 9 {
		t.Fatalf("agent exit lost: %+v, %v", execution, err)
	}
	verified, err := runner.Verify(context.Background(), task, execution)
	if err != nil || verified.Status != "success" || verified.Evidence != "agent_exit_code:9;exit_code:0" {
		t.Fatalf("patch was not judged externally: %+v, %v", verified, err)
	}
}
