package approval

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/humanloop"
)

func TestDurableInboundRestartAndDuplicate(t *testing.T) {
	dir := t.TempDir()
	store, err := humanloop.NewStore(filepath.Join(dir, "inbox.json"), 1)
	if err != nil {
		t.Fatal(err)
	}
	cfg := TelegramRelayConfig{BotToken: "test", ChatID: "42", StatePath: filepath.Join(dir, "relay.json"),
		Inbound: func(id, user, text string) error {
			_, err := store.EnqueueInboundOnce("telegram", user, text, id)
			return err
		}}
	relay := NewTelegramApprovalRelay(cfg)
	updates := []tgUpdate{{UpdateID: 7, Message: &tgMessage{Chat: &tgChat{ID: 42}, Text: "Execute a tarefa"}}}
	if err := relay.dispatchUpdates(updates); err != nil {
		t.Fatal(err)
	}
	relay = NewTelegramApprovalRelay(cfg)
	if err := relay.dispatchUpdates(updates); err != nil {
		t.Fatal(err)
	}
	_, messages := store.Snapshot()
	if len(messages) != 1 || relay.offset != 8 {
		t.Fatalf("messages=%v offset=%d", messages, relay.offset)
	}
	// An untrusted chat must be acknowledged without creating agent work.
	if err := relay.dispatchUpdates([]tgUpdate{{UpdateID: 8, Message: &tgMessage{Chat: &tgChat{ID: 99}, Text: "intruso"}}}); err != nil {
		t.Fatal(err)
	}
	_, messages = store.Snapshot()
	if len(messages) != 1 {
		t.Fatal("untrusted inbound accepted")
	}
}

func TestInboundFailureDoesNotAdvanceCheckpoint(t *testing.T) {
	cfg := TelegramRelayConfig{BotToken: "test", ChatID: "42", StatePath: filepath.Join(t.TempDir(), "relay.json"),
		Inbound: func(string, string, string) error { return errors.New("disk unavailable") }}
	relay := NewTelegramApprovalRelay(cfg)
	if err := relay.dispatchUpdates([]tgUpdate{{UpdateID: 3, Message: &tgMessage{Chat: &tgChat{ID: 42}, Text: "persist me"}}}); err == nil {
		t.Fatal("expected failure")
	}
	if relay.offset != 0 {
		t.Fatal("acknowledged unpersisted message")
	}
	if got := NewTelegramApprovalRelay(cfg).offset; got != 0 {
		t.Fatal(got)
	}
}

func TestLateQuestionAnswerAfterRestart(t *testing.T) {
	var inbound string
	cfg := TelegramRelayConfig{BotToken: "test", ChatID: "42", StatePath: filepath.Join(t.TempDir(), "relay.json"),
		Inbound: func(id, user, text string) error { inbound = text; return nil }}
	relay := NewTelegramApprovalRelay(cfg)
	relay.questions["q_1"] = telegramQuestion{Text: "Qual ambiente?"}
	if err := relay.saveStateLocked(); err != nil {
		t.Fatal(err)
	}
	relay = NewTelegramApprovalRelay(cfg)
	if err := relay.dispatchUpdates([]tgUpdate{{UpdateID: 1, Message: &tgMessage{Chat: &tgChat{ID: 42}, Text: "resposta q_1 homologacao"}}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inbound, "Qual ambiente?") || !strings.Contains(inbound, "homologacao") {
		t.Fatal(inbound)
	}
	relay = NewTelegramApprovalRelay(cfg)
	answer, err := relay.AskQuestion(context.Background(), "q_1", "Qual ambiente?")
	if err != nil || answer != "homologacao" {
		t.Fatalf("answer=%s err=%v", answer, err)
	}
}

func TestGroupRequiresAuthorizedSender(t *testing.T) {
	accepted := 0
	cfg := TelegramRelayConfig{BotToken: "test", ChatID: "-42", Inbound: func(string, string, string) error { accepted++; return nil }}
	relay := NewTelegramApprovalRelay(cfg)
	if err := relay.validateConfig(); err == nil {
		t.Fatal("group without allowlist accepted")
	}
	cfg.AllowedUserIDs = []int64{7}
	relay = NewTelegramApprovalRelay(cfg)
	if err := relay.validateConfig(); err != nil {
		t.Fatal(err)
	}
	for index, sender := range []int64{8, 7} {
		if err := relay.dispatchUpdates([]tgUpdate{{UpdateID: int64(index + 1), Message: &tgMessage{Chat: &tgChat{ID: -42}, From: &tgUser{ID: sender}, Text: "execute"}}}); err != nil {
			t.Fatal(err)
		}
	}
	if accepted != 1 {
		t.Fatal(accepted)
	}
}

func TestLocallyResolvedQuestionIgnoresLateRemoteAnswer(t *testing.T) {
	inbound := 0
	cfg := TelegramRelayConfig{BotToken: "test", ChatID: "42", StatePath: filepath.Join(t.TempDir(), "state.json"), Inbound: func(string, string, string) error { inbound++; return nil }}
	relay := NewTelegramApprovalRelay(cfg)
	if err := relay.ResolveQuestion("q_1", "local"); err != nil {
		t.Fatal(err)
	}
	relay = NewTelegramApprovalRelay(cfg)
	if err := relay.dispatchUpdates([]tgUpdate{{UpdateID: 1, Message: &tgMessage{Chat: &tgChat{ID: 42}, Text: "resposta q_1 remoto"}}}); err != nil {
		t.Fatal(err)
	}
	if inbound != 0 || relay.questions["q_1"].Answer != "local" {
		t.Fatal("resolved question reactivated")
	}
}

func TestRunReceivesWithoutPendingQuestion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	received := make(chan string, 1)
	relay := NewTelegramApprovalRelay(TelegramRelayConfig{BotToken: "test", ChatID: "42", Inbound: func(id, user, text string) error { received <- text; cancel(); return nil }})
	relay.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":[{"update_id":1,"message":{"chat":{"id":42},"text":"nova tarefa"}}]}`)), Header: make(http.Header)}, nil
	})}
	err := relay.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
	select {
	case text := <-received:
		if text != "nova tarefa" {
			t.Fatal(text)
		}
	default:
		t.Fatal("unsolicited message was not received")
	}
}
