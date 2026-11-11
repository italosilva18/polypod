package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/session"
	"github.com/costa/polypod/internal/turncheckpoint"
)

func TestCLICommandLeaseHolderProcess(t *testing.T) {
	if os.Getenv("POLYPOD_CLI_COMMAND_LEASE_HELPER") != "1" {
		return
	}
	release, err := turncheckpoint.New(os.Args[len(os.Args)-1], nil).AcquireBranchLease(context.Background(), "cli:local")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("READY")
	_, _ = io.Copy(io.Discard, os.Stdin)
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestMutatingCLICommandsCannotRaceAnotherProcessTurn(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCLICommandLeaseHolderProcess$", "--", root)
	cmd.Env = append(os.Environ(), "POLYPOD_CLI_COMMAND_LEASE_HELPER=1")
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
		t.Fatalf("helper did not hold CLI turn lease: %q %v %s", ready, err, stderr.String())
	}

	conv := conversation.NewManager(conversation.NewStore(nil, nil, ""), slog.Default())
	sess, err := conv.GetSession(context.Background(), "cli", "local")
	if err != nil {
		t.Fatal(err)
	}
	sess.AddMessage(conversation.RoleUser, "keep this transcript")
	storage := session.NewFileSystemStorage(filepath.Join(t.TempDir(), "sessions"), slog.Default())
	turns := turncheckpoint.New(root, nil)
	a := &Adapter{convMgr: conv, sessStorage: storage, turnCheckpoint: turns,
		activeSessionID: "branch", aiSvc: ai.NewService(nil, nil, nil, "default", nil, slog.Default()), logger: slog.Default()}
	deps := a.buildCommandDeps(context.Background())
	checks := []struct {
		name string
		run  func() error
	}{
		{"clear", deps.clearSession},
		{"compact", func() error { _, _, _, err := deps.compactHistory(); return err }},
		{"rewind", func() error { _, err := deps.truncateMessages(1); return err }},
		{"resume", func() error { _, err := deps.loadSession("other"); return err }},
		{"branch create", func() error {
			controller := newPersistentBranchController(t.TempDir(), storage, conv,
				func() string { return "branch" }, func(string, string) {}, func(*conversation.Session) error { return nil })
			controller.acquireProfileLease = func() (func() error, error) { return turns.AcquireBranchLease(context.Background(), "cli:local") }
			_, err := controller.create("child", nil)
			return err
		}},
	}
	for _, check := range checks {
		if err := check.run(); !errors.Is(err, turncheckpoint.ErrBranchBusy) {
			t.Fatalf("%s mutated while another process held turn lease: %v", check.name, err)
		}
		if len(sess.Messages) != 1 {
			t.Fatalf("%s changed active conversation despite busy lease", check.name)
		}
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("lease helper failed: %v %s", err, stderr.String())
	}
}
