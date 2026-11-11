package turncheckpoint

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestBranchLeaseHeldByAnotherProcessBlocksTurn(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBranchLeaseHelperProcess$", "--", root)
	cmd.Env = append(os.Environ(), "POLYPOD_LEASE_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(ready) != "READY" {
		t.Fatalf("helper did not hold lease: ready=%q err=%v stderr=%s", ready, err, stderr.String())
	}
	manager := New(root, nil)
	if release, err := manager.AcquireBranchLease(ctx, "cli:local"); !errors.Is(err, ErrBranchBusy) {
		if release != nil {
			_ = release()
		}
		t.Fatalf("second process acquired active CLI lease: %v", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper exit: %v stderr=%s", err, stderr.String())
	}
	release, err := manager.AcquireBranchLease(ctx, "cli:local")
	if err != nil {
		t.Fatalf("lease remained stuck after process exit: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestBranchLeaseHelperProcess(t *testing.T) {
	if os.Getenv("POLYPOD_LEASE_HELPER") != "1" {
		return
	}
	root := os.Args[len(os.Args)-1]
	release, err := New(root, nil).AcquireBranchLease(context.Background(), "cli:local")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("READY")
	_, _ = io.Copy(io.Discard, os.Stdin)
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestAICheckpointRejectsStaleWriter(t *testing.T) {
	root := t.TempDir()
	first, second := New(root, nil), New(root, nil)
	transcript, _ := json.Marshal([]map[string]string{{"role": "user", "content": "work"}})
	cp := &AICheckpoint{Version: AIVersion, SessionID: "branch", TurnID: "turn", Format: "provider",
		Model: "model", Messages: transcript, Phase: AIPhaseTools}
	if err := first.SaveAI(cp); err != nil {
		t.Fatal(err)
	}
	stale, err := second.LoadAI("branch", "turn")
	if err != nil {
		t.Fatal(err)
	}
	cp.PendingToolCallID = "first-write"
	if err := first.SaveAI(cp); err != nil {
		t.Fatal(err)
	}
	stale.PendingToolCallID = "second-write"
	if err := second.SaveAI(stale); !errors.Is(err, ErrAICheckpointConflict) {
		t.Fatalf("stale writer replaced pending tool boundary: %v", err)
	}
	latest, err := second.LoadAI("branch", "turn")
	if err != nil || latest.PendingToolCallID != "first-write" {
		t.Fatalf("pending tool boundary was overwritten: %+v, %v", latest, err)
	}
}
