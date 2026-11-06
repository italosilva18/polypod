package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEventTrailSurvivesRestartAndDetectsTampering(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"file_read", "bash", "audit"} {
		if err := s.Append(context.Background(), Event{Kind: "tool", Tool: tool, Status: "completed"}); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := s.Recent(context.Background(), 2)
	if err != nil || len(recent) != 2 || recent[0].Tool != "bash" || recent[1].Tool != "audit" {
		t.Fatalf("recent=%+v, err=%v", recent, err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Append(context.Background(), Event{Kind: "tool", Tool: "next"}); err != nil {
		t.Fatal(err)
	}
	all, err := reopened.Recent(context.Background(), 20)
	if err != nil || len(all) != 4 || all[3].Sequence != 4 {
		t.Fatalf("reopened trail=%+v, err=%v", all, err)
	}
	path := filepath.Join(dir, "events.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(raw), `"tool":"bash"`, `"tool":"shell"`, 1)
	if changed == string(raw) {
		t.Fatal("test did not alter audit entry")
	}
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); !errors.Is(err, ErrCorruptLog) {
		t.Fatalf("tampered trail should fail closed, got %v", err)
	}
}

func TestCheckpointRestoreRequiresFreshInspectionAndValidBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "document.txt")
	if err := os.WriteFile(path, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "private-audit"))
	if err != nil {
		t.Fatal(err)
	}
	cp, err := s.CreateCheckpoint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o640); err != nil {
		t.Fatal(err)
	}
	inspection, err := s.InspectCheckpoint(context.Background(), cp.ID)
	if err != nil || inspection.CurrentSHA256 == cp.SHA256 {
		t.Fatalf("inspection=%+v, err=%v", inspection, err)
	}
	if _, err := s.RestoreCheckpoint(context.Background(), cp.ID, cp.SHA256); err == nil {
		t.Fatal("stale current hash should not restore")
	}
	if _, err := s.RestoreCheckpoint(context.Background(), cp.ID, inspection.CurrentSHA256); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "original" {
		t.Fatalf("restored=%q, err=%v", raw, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("restored mode=%v, err=%v", info.Mode().Perm(), err)
	}
	if err := os.WriteFile(path, []byte("again"), 0o640); err != nil {
		t.Fatal(err)
	}
	inspection, err = s.InspectCheckpoint(context.Background(), cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "private-audit", "checkpoints", cp.ID+".bin"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreCheckpoint(context.Background(), cp.ID, inspection.CurrentSHA256); err == nil || !strings.Contains(err.Error(), "corrompido") {
		t.Fatalf("corrupt backup must be rejected, got %v", err)
	}
	raw, _ = os.ReadFile(path)
	if string(raw) != "again" {
		t.Fatalf("corrupt backup changed target to %q", raw)
	}
}

func TestCheckpointRejectsSymlinkAndTraversalID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCheckpoint(context.Background(), link); err == nil {
		t.Fatal("symlink checkpoint should be rejected")
	}
	if _, err := s.InspectCheckpoint(context.Background(), "../escape"); err == nil {
		t.Fatal("checkpoint traversal ID should be rejected")
	}
}
