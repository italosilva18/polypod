package tool

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestUserPromptHandlerLocalAnswerWins(t *testing.T) {
	handler := NewUserPromptHandler()
	handler.SetRemoteAsk(func(ctx context.Context, _ string, _ UserQuestion) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
			return "remoto", nil
		}
	})
	go func() {
		<-handler.QuestionCh
		handler.AnswerCh <- "local"
	}()
	answer, err := handler.Ask(context.Background(), UserQuestion{Question: "Escolha"})
	if err != nil || answer != "local" {
		t.Fatalf("Ask = %q, %v", answer, err)
	}
}

func TestUserPromptHandlerRemoteAnswerWins(t *testing.T) {
	handler := NewUserPromptHandler()
	handler.SetRemoteAsk(func(_ context.Context, id string, q UserQuestion) (string, error) {
		if id == "" || q.ID != id {
			t.Errorf("missing correlated ID: id=%q question=%q", id, q.ID)
		}
		return "remoto", nil
	})
	answer, err := handler.Ask(context.Background(), UserQuestion{Question: "Escolha"})
	if err != nil || answer != "remoto" {
		t.Fatalf("Ask = %q, %v", answer, err)
	}
}

func TestUserPromptHandlerRemoteFailureFallsBackToTUI(t *testing.T) {
	handler := NewUserPromptHandler()
	handler.SetRemoteAsk(func(context.Context, string, UserQuestion) (string, error) {
		return "", errors.New("offline")
	})
	go func() {
		question := <-handler.QuestionCh
		if question.Question != "Continuar?" {
			t.Errorf("question = %q", question.Question)
		}
		handler.AnswerCh <- "sim"
	}()
	answer, err := handler.Ask(context.Background(), UserQuestion{Question: "Continuar?"})
	if err != nil || answer != "sim" {
		t.Fatalf("Ask = %q, %v", answer, err)
	}
}

func TestUserPromptHandlerHonorsCancellation(t *testing.T) {
	handler := NewUserPromptHandler()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := handler.Ask(ctx, UserQuestion{Question: "Continuar?"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestNewQuestionIDIsCorrelatedAndDistinct(t *testing.T) {
	first := newQuestionID()
	second := newQuestionID()
	if !strings.HasPrefix(first, "q_") || first == second {
		t.Fatalf("unexpected IDs: %q %q", first, second)
	}
}

func TestAskWaitingForSlotHonorsCancellation(t *testing.T) {
	handler := NewUserPromptHandler()
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	firstDone := make(chan struct{})
	go func() { defer close(firstDone); _, _ = handler.Ask(firstCtx, UserQuestion{Question: "primeira"}) }()
	select {
	case <-handler.QuestionCh:
	case <-time.After(time.Second):
		t.Fatal("first question not delivered")
	}
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	cancelSecond()
	secondDone := make(chan error, 1)
	go func() { _, err := handler.Ask(secondCtx, UserQuestion{Question: "segunda"}); secondDone <- err }()
	select {
	case err := <-secondDone:
		if err == nil {
			t.Fatal("expected cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled ask blocked behind another question")
	}
	cancelFirst()
	<-firstDone
}
