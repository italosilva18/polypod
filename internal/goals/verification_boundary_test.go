package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/evolution"
)

func TestGoalVerificationUsesStateAtAttemptFinish(t *testing.T) {
	dir := t.TempDir()
	verifier, err := evolution.Open(filepath.Join(dir, "evolution.db"), filepath.Join(dir, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = verifier.Close() })
	store, err := Open(filepath.Join(dir, "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.AttachVerifier(verifier)
	goal, err := store.Create(CreateInput{
		Title: "Gerar artefato", SuccessCriteria: "Arquivo com hash esperado", NextAction: "Escrever arquivo",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "artifact.txt")
	expected := []byte("resultado confirmado")
	digest := sha256.Sum256(expected)
	if _, err := store.RegisterVerification(t.Context(), goal.ID, evolution.VerificationPlanInput{
		Kind: evolution.FileSHA256Check, Path: path, ExpectedSHA256: hex.EncodeToString(digest[:]),
	}); err != nil {
		t.Fatal(err)
	}
	started, err := store.StartAttempt(goal.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	attemptID := started.Attempts[len(started.Attempts)-1].ID
	if err := os.WriteFile(path, expected, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishAttempt(goal.ID, attemptID, AttemptReportedSuccess,
		"Arquivo criado", "arquivo gerado", nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("alterado depois do encerramento"), 0600); err != nil {
		t.Fatal(err)
	}
	verified, err := store.VerifyAttempt(t.Context(), goal.ID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	proof := verified.Attempts[len(verified.Attempts)-1].Verification
	if proof == nil || !proof.Success || proof.OutcomeID == "" {
		t.Fatalf("verificação não preservou o estado do encerramento: %+v", proof)
	}
	if _, err := store.ReviewAttempt(goal.ID, attemptID, "accepted", "hash observado no encerramento", nil); err != nil {
		t.Fatal(err)
	}
	completed, evidence := Completed, "resultado verificado no encerramento"
	if _, err := store.Update(goal.ID, UpdateInput{
		Status: &completed, Evidence: &evidence, Note: "meta concluída com prova",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGoalFinishRetryKeepsFirstCapture(t *testing.T) {
	dir := t.TempDir()
	verifier, err := evolution.Open(filepath.Join(dir, "evolution.db"), filepath.Join(dir, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = verifier.Close() })
	store, err := Open(filepath.Join(dir, "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.AttachVerifier(verifier)
	goal, err := store.Create(CreateInput{Title: "Gerar artefato", SuccessCriteria: "Hash esperado", NextAction: "Escrever"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "artifact.txt")
	expected := []byte("resultado confirmado")
	digest := sha256.Sum256(expected)
	if _, err := store.RegisterVerification(t.Context(), goal.ID, evolution.VerificationPlanInput{
		Kind: evolution.FileSHA256Check, Path: path, ExpectedSHA256: hex.EncodeToString(digest[:]),
	}); err != nil {
		t.Fatal(err)
	}
	started, err := store.StartAttempt(goal.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	attemptID := started.Attempts[len(started.Attempts)-1].ID
	if err := os.WriteFile(path, expected, 0600); err != nil {
		t.Fatal(err)
	}
	tooLong := strings.Repeat("a", 4097)
	if _, err := store.FinishAttempt(goal.ID, attemptID, AttemptReportedSuccess,
		"feito", "arquivo escrito", &tooLong); err == nil {
		t.Fatal("invalid next action unexpectedly accepted")
	}
	// Simulate the interval after the verification DB captured the final
	// state but before the goal store could commit the finished attempt.
	if captured, err := verifier.FinishVerificationRun(t.Context(), "goal", goal.ID, attemptID); err != nil || !captured {
		t.Fatalf("initial capture: captured=%v err=%v", captured, err)
	}
	if err := os.WriteFile(path, []byte("modified after capture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishAttempt(goal.ID, attemptID, AttemptReportedSuccess,
		"feito", "arquivo escrito", nil); err != nil {
		t.Fatal(err)
	}
	verified, err := store.VerifyAttempt(t.Context(), goal.ID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	proof := verified.Attempts[len(verified.Attempts)-1].Verification
	if proof == nil || !proof.Success {
		t.Fatalf("retry replaced first capture: %+v", proof)
	}
}

func TestGoalFailedAttemptCanCloseWithoutFinalCapture(t *testing.T) {
	dir := t.TempDir()
	verifier, err := evolution.Open(filepath.Join(dir, "evolution.db"), filepath.Join(dir, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(dir, "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.AttachVerifier(verifier)
	goal, err := store.Create(CreateInput{Title: "Gerar artefato", SuccessCriteria: "Hash esperado", NextAction: "Escrever"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "artifact.txt")
	digest := sha256.Sum256([]byte("expected"))
	if _, err := store.RegisterVerification(t.Context(), goal.ID, evolution.VerificationPlanInput{
		Kind: evolution.FileSHA256Check, Path: path, ExpectedSHA256: hex.EncodeToString(digest[:]),
	}); err != nil {
		t.Fatal(err)
	}
	started, err := store.StartAttempt(goal.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	attemptID := started.Attempts[len(started.Attempts)-1].ID
	if err := verifier.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishAttempt(goal.ID, attemptID, AttemptFailed, "não foi possível escrever", "erro da ação", nil); err != nil {
		t.Fatalf("failed attempt cannot be closed while verifier is offline: %v", err)
	}
	if _, err := store.ReviewAttempt(goal.ID, attemptID, "rejected", "artefato não foi criado", nil); err != nil {
		t.Fatalf("failed attempt cannot be reviewed: %v", err)
	}
}

func TestGoalLegacyAttemptCannotReceiveLateProof(t *testing.T) {
	dir := t.TempDir()
	verifier, err := evolution.Open(filepath.Join(dir, "evolution.db"), filepath.Join(dir, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = verifier.Close() })
	store, err := Open(filepath.Join(dir, "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.AttachVerifier(verifier)
	goal, err := store.Create(CreateInput{Title: "Gerar artefato", SuccessCriteria: "Hash esperado", NextAction: "Escrever"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "artifact.txt")
	expected := []byte("expected")
	digest := sha256.Sum256(expected)
	if _, err := store.RegisterVerification(t.Context(), goal.ID, evolution.VerificationPlanInput{
		Kind: evolution.FileSHA256Check, Path: path, ExpectedSHA256: hex.EncodeToString(digest[:]),
	}); err != nil {
		t.Fatal(err)
	}
	started, err := store.StartAttempt(goal.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	attemptID := started.Attempts[len(started.Attempts)-1].ID
	// Older binaries closed an attempt without storing its final state.
	store.mu.Lock()
	legacy := clone(store.goals)
	attempt := &legacy[0].Attempts[len(legacy[0].Attempts)-1]
	attempt.Status = AttemptReportedSuccess
	finishedAt := time.Now().UTC()
	attempt.FinishedAt = &finishedAt
	attempt.Result, attempt.Evidence = "relatado", "sem captura objetiva"
	err = store.commit(legacy)
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, expected, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.VerifyAttempt(t.Context(), goal.ID, attemptID); err == nil {
		t.Fatal("late change credited to a legacy attempt without final capture")
	}
	if _, err := store.ReviewAttempt(goal.ID, attemptID, "rejected", "sem prova no encerramento", nil); err != nil {
		t.Fatal(err)
	}
}

func TestGoalVerificationDoesNotCreditLateChange(t *testing.T) {
	dir := t.TempDir()
	verifier, err := evolution.Open(filepath.Join(dir, "evolution.db"), filepath.Join(dir, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = verifier.Close() })
	store, err := Open(filepath.Join(dir, "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.AttachVerifier(verifier)
	goal, err := store.Create(CreateInput{
		Title: "Gerar artefato", SuccessCriteria: "Arquivo com hash esperado", NextAction: "Escrever arquivo",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "artifact.txt")
	expected := []byte("resultado confirmado")
	digest := sha256.Sum256(expected)
	if _, err := store.RegisterVerification(t.Context(), goal.ID, evolution.VerificationPlanInput{
		Kind: evolution.FileSHA256Check, Path: path, ExpectedSHA256: hex.EncodeToString(digest[:]),
	}); err != nil {
		t.Fatal(err)
	}
	started, err := store.StartAttempt(goal.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	attemptID := started.Attempts[len(started.Attempts)-1].ID
	if err := os.WriteFile(path, []byte("resultado errado"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishAttempt(goal.ID, attemptID, AttemptReportedSuccess,
		"Arquivo criado", "arquivo gerado", nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, expected, 0600); err != nil {
		t.Fatal(err)
	}
	verified, err := store.VerifyAttempt(t.Context(), goal.ID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	proof := verified.Attempts[len(verified.Attempts)-1].Verification
	if proof == nil || proof.Success {
		t.Fatalf("mudança posterior recebeu crédito pela tentativa: %+v", proof)
	}
	if _, err := store.ReviewAttempt(goal.ID, attemptID, "accepted", "resultado observado", nil); err == nil {
		t.Fatal("revisão aceitou uma tentativa com verificação negativa")
	}
}
