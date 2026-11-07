package evolution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openVerificationTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "evolution.db"), filepath.Join(dir, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func verificationDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func verifiedStartAfterPlan(t *testing.T, store *Store, kind, subjectID string) time.Time {
	t.Helper()
	plan, err := store.verificationPlan(t.Context(), kind, subjectID)
	if err != nil {
		t.Fatal(err)
	}
	return time.UnixMilli(plan.CreatedAt + 1)
}

func TestVerifiedOutcomeRequiresStateTransitionAndSurvivesRetry(t *testing.T) {
	ctx := context.Background()
	store := openVerificationTestStore(t)
	path := filepath.Join(t.TempDir(), "artifact.txt")
	expected := []byte("artifact produced by the queued task")
	plan := VerificationPlanInput{SourceKind: "task", SubjectID: "task-1", PatternKey: "write report",
		Path: path, ExpectedSHA256: verificationDigest(expected)}
	registered, err := store.RegisterVerificationPlan(ctx, plan)
	if err != nil || registered.BaselineSHA256 != missingVerifiedFile {
		t.Fatalf("register plan: %+v, %v", registered, err)
	}
	if _, err := store.RegisterVerificationPlan(ctx, plan); err != nil {
		t.Fatalf("idempotent registration: %v", err)
	}
	changed := plan
	changed.ExpectedSHA256 = verificationDigest([]byte("different"))
	if _, err := store.RegisterVerificationPlan(ctx, changed); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("changed plan was accepted: %v", err)
	}
	if begun, err := store.BeginVerificationRun(ctx, "task", "task-1", "run-1"); err != nil || !begun {
		t.Fatalf("run baseline: begun=%t err=%v", begun, err)
	}
	if err := os.WriteFile(path, expected, 0600); err != nil {
		t.Fatal(err)
	}
	if _, recorded, err := store.VerifyAndRecordAt(ctx, "task", "task-1", "run-1", nil,
		verifiedStartAfterPlan(t, store, "task", "task-1")); err != nil || recorded {
		t.Fatalf("run without final snapshot was credited: recorded=%t err=%v", recorded, err)
	}
	if finished, err := store.FinishVerificationRun(ctx, "task", "task-1", "run-1"); err != nil || !finished {
		t.Fatalf("finish run: finished=%t err=%v", finished, err)
	}
	if err := os.WriteFile(path, []byte("changed before delayed notice"), 0600); err != nil {
		t.Fatal(err)
	}
	startedAt := verifiedStartAfterPlan(t, store, "task", "task-1")
	out, recorded, err := store.VerifyAndRecordAt(ctx, "task", "task-1", "run-1", nil, startedAt)
	if err != nil || !recorded || !out.Success || out.VerifiedBy != "file_sha256_v1" || len(out.ToolNames) != 0 {
		t.Fatalf("verified outcome: %+v recorded=%t err=%v", out, recorded, err)
	}
	if err := os.WriteFile(path, []byte("changed after the run"), 0600); err != nil {
		t.Fatal(err)
	}
	repeated, recorded, err := store.VerifyAndRecordAt(ctx, "task", "task-1", "run-1", nil, startedAt)
	if err != nil || !recorded || repeated.ID != out.ID || !repeated.Success {
		t.Fatalf("retry changed durable outcome: %+v recorded=%t err=%v", repeated, recorded, err)
	}
	if _, recorded, err := store.VerifyAndRecordAt(ctx, "task", "unknown", "run-2", nil, startedAt); err != nil || recorded {
		t.Fatalf("missing plan should not invent evidence: recorded=%t err=%v", recorded, err)
	}
}

func TestVerifiedOutcomeRecordsStableMismatchAndRejectsPreexistingSuccess(t *testing.T) {
	ctx := context.Background()
	store := openVerificationTestStore(t)
	path := filepath.Join(t.TempDir(), "artifact.txt")
	expected := []byte("expected output")
	plan := VerificationPlanInput{SourceKind: "routine", SubjectID: "routine-1", PatternKey: "publish artifact",
		Kind: FileSHA256Check, Path: path, ExpectedSHA256: verificationDigest(expected)}
	if _, err := store.RegisterVerificationPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if begun, err := store.BeginVerificationRun(ctx, "routine", "routine-1", "run-1"); err != nil || !begun {
		t.Fatalf("first run baseline: begun=%t err=%v", begun, err)
	}
	if err := os.WriteFile(path, []byte("wrong output"), 0600); err != nil {
		t.Fatal(err)
	}
	if finished, err := store.FinishVerificationRun(ctx, "routine", "routine-1", "run-1"); err != nil || !finished {
		t.Fatalf("first finish: finished=%t err=%v", finished, err)
	}
	startedAt := verifiedStartAfterPlan(t, store, "routine", "routine-1")
	out, recorded, err := store.VerifyAndRecordAt(ctx, "routine", "routine-1", "run-1", []string{"write_file"}, startedAt)
	if err != nil || !recorded || out.Success || out.EvidenceRef == "file_sha256:missing" {
		t.Fatalf("stable mismatch not recorded: %+v recorded=%t err=%v", out, recorded, err)
	}
	if begun, err := store.BeginVerificationRun(ctx, "routine", "routine-1", "run-2"); err != nil || !begun {
		t.Fatalf("second run baseline: begun=%t err=%v", begun, err)
	}
	if err := os.WriteFile(path, expected, 0600); err != nil {
		t.Fatal(err)
	}
	if finished, err := store.FinishVerificationRun(ctx, "routine", "routine-1", "run-2"); err != nil || !finished {
		t.Fatalf("second finish: finished=%t err=%v", finished, err)
	}
	second, recorded, err := store.VerifyAndRecordAt(ctx, "routine", "routine-1", "run-2", []string{"write_file"}, startedAt)
	if err != nil || !recorded || !second.Success || second.EvidenceRef != "file_sha256:"+plan.ExpectedSHA256 {
		t.Fatalf("new run did not verify expected state: %+v recorded=%t err=%v", second, recorded, err)
	}
	already := plan
	already.SubjectID = "routine-already-done"
	if _, err := store.RegisterVerificationPlan(ctx, already); err == nil {
		t.Fatal("preexisting matching artifact was accepted as future task evidence")
	}
}

func TestVerifiedOutcomeCannotTurnUntracedSuccessIntoSkill(t *testing.T) {
	ctx := context.Background()
	store := openVerificationTestStore(t)
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		path := filepath.Join(t.TempDir(), id+".txt")
		content := []byte("artifact for " + id)
		if _, err := store.RegisterVerificationPlan(ctx, VerificationPlanInput{
			SourceKind: "task", SubjectID: id, PatternKey: "write report",
			Path: path, ExpectedSHA256: verificationDigest(content),
		}); err != nil {
			t.Fatal(err)
		}
		if begun, err := store.BeginVerificationRun(ctx, "task", id, "run-"+id); err != nil || !begun {
			t.Fatalf("run %s baseline: begun=%t err=%v", id, begun, err)
		}
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
		if finished, err := store.FinishVerificationRun(ctx, "task", id, "run-"+id); err != nil || !finished {
			t.Fatalf("finish %s: finished=%t err=%v", id, finished, err)
		}
		if out, recorded, err := store.VerifyAndRecordAt(ctx, "task", id, "run-"+id, nil,
			verifiedStartAfterPlan(t, store, "task", id)); err != nil || !recorded || !out.Success {
			t.Fatalf("verify %s: %+v recorded=%t err=%v", id, out, recorded, err)
		}
	}
	patterns, err := store.Patterns(ctx)
	if err != nil || len(patterns) != 1 || patterns[0].SuccessCount != 3 {
		t.Fatalf("verified results not counted: %+v err=%v", patterns, err)
	}
	if _, err := store.Propose(ctx, "write report"); !errors.Is(err, ErrNotEligible) {
		t.Fatalf("draft without observed tool sequence was allowed: %v", err)
	}
}

func TestVerifiedOutcomeRejectsSymlinkWithoutRecording(t *testing.T) {
	ctx := context.Background()
	store := openVerificationTestStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.txt")
	if _, err := store.RegisterVerificationPlan(ctx, VerificationPlanInput{
		SourceKind: "task", SubjectID: "task-1", PatternKey: "write report",
		Path: path, ExpectedSHA256: verificationDigest([]byte("expected")),
	}); err != nil {
		t.Fatal(err)
	}
	if begun, err := store.BeginVerificationRun(ctx, "task", "task-1", "run-1"); err != nil || !begun {
		t.Fatalf("run baseline: begun=%t err=%v", begun, err)
	}
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("expected"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if finished, err := store.FinishVerificationRun(ctx, "task", "task-1", "run-1"); err == nil || finished {
		t.Fatalf("symlink accepted as final snapshot: finished=%t err=%v", finished, err)
	}
	if _, recorded, err := store.VerifyAndRecordAt(ctx, "task", "task-1", "run-1", nil,
		verifiedStartAfterPlan(t, store, "task", "task-1")); err != nil || recorded {
		t.Fatalf("symlink accepted as independent proof: recorded=%t err=%v", recorded, err)
	}
	if outcomes, err := store.Outcomes(ctx, "write report"); err != nil || len(outcomes) != 0 {
		t.Fatalf("inconclusive proof was recorded: %+v err=%v", outcomes, err)
	}
}

func TestObservedTracePersistsAcrossRestartAndFeedsVerifiedOutcome(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "evolution.db")
	skillsPath := filepath.Join(dir, "skills")
	path := filepath.Join(dir, "artifact.txt")
	store, err := Open(dbPath, skillsPath)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("verified output")
	if _, err := store.RegisterVerificationPlan(ctx, VerificationPlanInput{
		SourceKind: "task", SubjectID: "task-1", PatternKey: "write report",
		Path: path, ExpectedSHA256: verificationDigest(content),
	}); err != nil {
		t.Fatal(err)
	}
	if begun, err := store.BeginVerificationRun(ctx, "task", "task-1", "task-1:terminal:1"); err != nil || !begun {
		t.Fatalf("run baseline: begun=%t err=%v", begun, err)
	}
	traceCtx, trace := WithToolTrace(ctx)
	ObserveTool(traceCtx, "read_file")
	ObserveTool(traceCtx, "write_file")
	ObserveTool(traceCtx, "name-with-hyphen")
	ObserveTool(ctx, "ignored_without_trace")
	names := trace.Names()
	if len(names) != 2 || names[0] != "read_file" || names[1] != "write_file" {
		t.Fatalf("unexpected trace snapshot: %v", names)
	}
	if err := store.SaveToolTrace(ctx, "task", "task-1:terminal:1", names); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveToolTrace(ctx, "task", "task-1:terminal:1", names); err != nil {
		t.Fatalf("trace replay should be idempotent: %v", err)
	}
	if err := store.SaveToolTrace(ctx, "task", "task-1:terminal:1", []string{"write_file"}); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("changed trace was accepted: %v", err)
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	if finished, err := store.FinishVerificationRun(ctx, "task", "task-1", "task-1:terminal:1"); err != nil || !finished {
		t.Fatalf("finish before restart: finished=%t err=%v", finished, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dbPath, skillsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := os.WriteFile(path, []byte("changed after finished run"), 0600); err != nil {
		t.Fatal(err)
	}
	out, recorded, err := store.VerifyAndRecordAt(ctx, "task", "task-1", "task-1:terminal:1", nil,
		verifiedStartAfterPlan(t, store, "task", "task-1"))
	if err != nil || !recorded || !out.Success || len(out.ToolNames) != 2 || out.ToolNames[1] != "write_file" {
		t.Fatalf("stored trace not used after restart: %+v recorded=%t err=%v", out, recorded, err)
	}
}

func TestToolTraceLimitsAndCopiesNames(t *testing.T) {
	ctx, trace := WithToolTrace(context.Background())
	for range maxObservedTools {
		ObserveTool(ctx, "read_file")
	}
	names := trace.Names()
	if len(names) != maxObservedTools {
		t.Fatalf("trace has %d entries, want %d", len(names), maxObservedTools)
	}
	names[0] = "changed"
	if trace.Names()[0] != "read_file" {
		t.Fatal("trace snapshot mutates internal state")
	}
	ObserveTool(ctx, "write_file")
	if names := trace.Names(); len(names) != 0 {
		t.Fatalf("truncated trace was exposed as complete: %v", names)
	}
}

func TestAutomaticVerifierIdentityCannotBeAssertedThroughRecordOutcome(t *testing.T) {
	store := openVerificationTestStore(t)
	_, err := store.RecordOutcome(context.Background(), OutcomeInput{
		SourceKind: "task", SourceID: "run-spoofed", PatternKey: "write report",
		Success: true, VerifiedBy: "file_sha256_v1", EvidenceRef: "file_sha256:" + verificationDigest([]byte("claimed")),
		ToolNames: []string{"write_file"},
	})
	if err == nil {
		t.Fatal("external outcome claimed the internal file verifier")
	}
}

func TestLatePlanCannotCreditAnAlreadyStartedRun(t *testing.T) {
	ctx := context.Background()
	store := openVerificationTestStore(t)
	startedAt := time.Now().Add(-time.Minute)
	path := filepath.Join(t.TempDir(), "artifact.txt")
	content := []byte("already generated")
	if _, err := store.RegisterVerificationPlan(ctx, VerificationPlanInput{
		SourceKind: "task", SubjectID: "task-1", PatternKey: "write report",
		Path: path, ExpectedSHA256: verificationDigest(content),
	}); err != nil {
		t.Fatal(err)
	}
	if begun, err := store.BeginVerificationRun(ctx, "task", "task-1", "run-1"); err != nil || !begun {
		t.Fatalf("run baseline: begun=%t err=%v", begun, err)
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	if finished, err := store.FinishVerificationRun(ctx, "task", "task-1", "run-1"); err != nil || !finished {
		t.Fatalf("late plan finish: finished=%t err=%v", finished, err)
	}
	if _, recorded, err := store.VerifyAndRecordAt(ctx, "task", "task-1", "run-1", nil, startedAt); err != nil || recorded {
		t.Fatalf("late plan credited run: recorded=%t err=%v", recorded, err)
	}
	if _, recorded, err := store.VerifyAndRecordAt(ctx, "task", "task-1", "run-1", nil, time.Time{}); err != nil || recorded {
		t.Fatalf("unknown start credited run: recorded=%t err=%v", recorded, err)
	}
	if _, _, err := store.VerifyAndRecord(ctx, "task", "task-1", "run-1", nil); !errors.Is(err, ErrVerificationStartUnknown) {
		t.Fatalf("legacy path accepted unbounded verification: %v", err)
	}
	if outcomes, err := store.Outcomes(ctx, "write report"); err != nil || len(outcomes) != 0 {
		t.Fatalf("late plan produced outcome: %+v err=%v", outcomes, err)
	}
}

func TestRecurringRunDoesNotCreditArtifactAlreadyPresentAtStart(t *testing.T) {
	ctx := context.Background()
	store := openVerificationTestStore(t)
	path := filepath.Join(t.TempDir(), "artifact.txt")
	content := []byte("daily report")
	if _, err := store.RegisterVerificationPlan(ctx, VerificationPlanInput{
		SourceKind: "routine", SubjectID: "routine-1", PatternKey: "daily report",
		Path: path, ExpectedSHA256: verificationDigest(content),
	}); err != nil {
		t.Fatal(err)
	}
	startedAt := verifiedStartAfterPlan(t, store, "routine", "routine-1")
	if begun, err := store.BeginVerificationRun(ctx, "routine", "routine-1", "run-1"); err != nil || !begun {
		t.Fatalf("first baseline: begun=%t err=%v", begun, err)
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	if finished, err := store.FinishVerificationRun(ctx, "routine", "routine-1", "run-1"); err != nil || !finished {
		t.Fatalf("first finish: finished=%t err=%v", finished, err)
	}
	if out, recorded, err := store.VerifyAndRecordAt(ctx, "routine", "routine-1", "run-1", nil, startedAt); err != nil || !recorded || !out.Success {
		t.Fatalf("first run not credited: %+v recorded=%t err=%v", out, recorded, err)
	}
	if begun, err := store.BeginVerificationRun(ctx, "routine", "routine-1", "run-2"); err != nil || !begun {
		t.Fatalf("second baseline: begun=%t err=%v", begun, err)
	}
	if finished, err := store.FinishVerificationRun(ctx, "routine", "routine-1", "run-2"); err != nil || !finished {
		t.Fatalf("second finish: finished=%t err=%v", finished, err)
	}
	if out, recorded, err := store.VerifyAndRecordAt(ctx, "routine", "routine-1", "run-2", nil, startedAt); err != nil || recorded {
		t.Fatalf("preexisting artifact credited twice: %+v recorded=%t err=%v", out, recorded, err)
	}
}

func TestChangedFinalSnapshotMakesRepeatedFinishInconclusive(t *testing.T) {
	ctx := context.Background()
	store := openVerificationTestStore(t)
	path := filepath.Join(t.TempDir(), "artifact.txt")
	expected := []byte("expected")
	if _, err := store.RegisterVerificationPlan(ctx, VerificationPlanInput{
		SourceKind: "task", SubjectID: "task-1", PatternKey: "write report",
		Path: path, ExpectedSHA256: verificationDigest(expected),
	}); err != nil {
		t.Fatal(err)
	}
	if begun, err := store.BeginVerificationRun(ctx, "task", "task-1", "run-1"); err != nil || !begun {
		t.Fatalf("begin: begun=%t err=%v", begun, err)
	}
	if err := os.WriteFile(path, expected, 0600); err != nil {
		t.Fatal(err)
	}
	if finished, err := store.FinishVerificationRun(ctx, "task", "task-1", "run-1"); err != nil || !finished {
		t.Fatalf("first finish: finished=%t err=%v", finished, err)
	}
	if err := os.WriteFile(path, []byte("different"), 0600); err != nil {
		t.Fatal(err)
	}
	if finished, err := store.FinishVerificationRun(ctx, "task", "task-1", "run-1"); err != nil || finished {
		t.Fatalf("conflicting finish was accepted: finished=%t err=%v", finished, err)
	}
	if _, recorded, err := store.VerifyAndRecordAt(ctx, "task", "task-1", "run-1", nil,
		verifiedStartAfterPlan(t, store, "task", "task-1")); err != nil || recorded {
		t.Fatalf("conflicting final snapshot credited: recorded=%t err=%v", recorded, err)
	}
}
