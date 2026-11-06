package whatsappgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/costa/polypod/internal/humanloop"
)

type questionDriver struct {
	sent      chan Outbound
	attempted chan struct{}
	offline   atomic.Bool
	fail      bool
}

func (d *questionDriver) Send(ctx context.Context, msg Outbound) error {
	if d.offline.Load() {
		if d.attempted != nil {
			select {
			case d.attempted <- struct{}{}:
			default:
			}
		}
		return fmt.Errorf("offline: %w", humanloop.ErrRemoteUnavailable)
	}
	select {
	case d.sent <- msg:
		if d.fail {
			return errors.New("ambiguous send")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func questionFixture(t *testing.T) (*humanloop.Store, *Gateway, *QuestionRelay, *questionDriver, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := humanloop.NewStore(filepath.Join(dir, "inbox.json"), 500)
	if err != nil {
		t.Fatal(err)
	}
	driver := &questionDriver{sent: make(chan Outbound, 4)}
	gateway, err := New(store, driver, []string{"chat-a", "chat-b"}, []string{"sender-a", "sender-b"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, "questions.json")
	relay, err := NewQuestionRelay(store, gateway, statePath, "")
	if err != nil {
		t.Fatal(err)
	}
	gateway.SetQuestionRelay(relay)
	return store, gateway, relay, driver, statePath
}

func waitQuestionSend(t *testing.T, ch <-chan Outbound) Outbound {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("question was not sent")
		return Outbound{}
	}
}

func TestQuestionRelayConsumesOnlyCorrelatedReplyAndSurvivesRestart(t *testing.T) {
	store, gateway, relay, driver, statePath := questionFixture(t)
	ctx := WithInboundContext(context.Background(), InboundContext{
		MessageID: "inbound-1", ChatID: "chat-a", SenderID: "sender-a"})
	answers := make(chan string, 1)
	go func() {
		answer, err := relay.Ask(ctx, "q_first", "Qual ambiente?")
		if err != nil {
			answers <- "ERROR: " + err.Error()
			return
		}
		answers <- answer
	}()
	sent := waitQuestionSend(t, driver.sent)
	if sent.ChatID != "chat-a" || !strings.Contains(sent.Text, "q_first") {
		t.Fatal("question routed to wrong destination or missing correlation")
	}
	// Both identities are allowlisted, but neither matches the asking turn.
	for _, event := range []Event{
		{ID: "wrong-sender", ChatID: "chat-a", SenderID: "sender-b", Text: "resposta q_first nao"},
		{ID: "wrong-chat", ChatID: "chat-b", SenderID: "sender-a", Text: "resposta q_first nao"},
	} {
		if err := gateway.Receive(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case answer := <-answers:
		t.Fatalf("wrong identity resolved the question: %q", answer)
	default:
	}
	if err := gateway.Receive(context.Background(), Event{ID: "valid-answer", ChatID: "chat-a",
		SenderID: "sender-a", Text: "homologacao", ReplyTo: sent.ID}); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-answers:
		if answer != "homologacao" {
			t.Fatalf("answer = %q", answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("correct answer did not resolve")
	}
	if err := gateway.Receive(context.Background(), Event{ID: "valid-answer", ChatID: "chat-a",
		SenderID: "sender-a", Text: "homologacao", ReplyTo: sent.ID}); err != nil {
		t.Fatal(err)
	}
	_, messages := store.Snapshot()
	inbound := 0
	for _, msg := range messages {
		if msg.Direction == humanloop.DirectionInbound {
			inbound++
		}
	}
	if inbound != 2 { // only the two unrelated messages reached the agent inbox
		t.Fatalf("inbound count = %d, want 2", inbound)
	}
	if !store.DeliveredTo(messages[0].ID, Channel) {
		// The first item is the question outbox; inspect by direction below if
		// order changes as the store evolves.
		found := false
		for _, msg := range messages {
			if msg.Direction == humanloop.DirectionOutbound && store.DeliveredTo(msg.ID, Channel) {
				found = true
			}
		}
		if !found {
			t.Fatal("question delivery was not checkpointed")
		}
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var saved questionState
	if err := json.Unmarshal(raw, &saved); err != nil || saved.Questions["q_first"].Answer != "homologacao" {
		t.Fatal("answer was not durable before receive returned")
	}
	relay.mu.Lock()
	old := relay.questions["q_first"]
	old.ExpiresAt, old.Closed = time.Now().Add(-time.Minute), true
	relay.questions["q_first"] = old
	if err := relay.saveLocked(); err != nil {
		relay.mu.Unlock()
		t.Fatal(err)
	}
	relay.mu.Unlock()
	restarted, err := NewQuestionRelay(store, gateway, statePath, "")
	if err != nil {
		t.Fatal(err)
	}
	gateway.SetQuestionRelay(restarted)
	again, err := restarted.Ask(ctx, "q_retry", "Qual ambiente?")
	if err != nil || again != "homologacao" {
		t.Fatalf("restarted answer = %q, %v", again, err)
	}
	select {
	case <-driver.sent:
		t.Fatal("restarted question was sent twice")
	default:
	}
}

func TestQuestionRelayCommandAndExpiredOutbox(t *testing.T) {
	store, gateway, relay, driver, _ := questionFixture(t)
	ctx, cancel := context.WithCancel(WithInboundContext(context.Background(), InboundContext{
		MessageID: "inbound-2", ChatID: "chat-a", SenderID: "sender-a"}))
	answerCh := make(chan string, 1)
	go func() {
		answer, _ := relay.Ask(ctx, "q_second", "Qual data?")
		answerCh <- answer
	}()
	_ = waitQuestionSend(t, driver.sent)
	if err := gateway.Receive(context.Background(), Event{ID: "answer-2", ChatID: "chat-a",
		SenderID: "sender-a", Text: "resposta q_second amanha"}); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-answerCh:
		if answer != "amanha" {
			t.Fatalf("answer = %q", answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("command answer did not resolve")
	}
	cancel()
	_, messages := store.Snapshot()
	for _, msg := range messages {
		if msg.Direction == humanloop.DirectionInbound {
			t.Fatal("answer command became a new agent prompt")
		}
	}
	// A question abandoned before send must become terminal before its
	// deferred next-attempt time, so the worker cannot send it much later.
	deferred, err := store.EnqueueQuestionOutboundOnce(Channel, "chat-a", "pergunta antiga", "ask:q_expired", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	relay.mu.Lock()
	relay.questions["q_expired"] = questionRecord{ID: "q_expired", Key: "expired", ChatID: "chat-a",
		SenderID: "sender-a", OutboundID: deferred.ID, ExpiresAt: time.Now().Add(-time.Minute)}
	if err := relay.saveLocked(); err != nil {
		relay.mu.Unlock()
		t.Fatal(err)
	}
	relay.mu.Unlock()
	relay.expire(time.Now())
	if err := gateway.Receive(context.Background(), Event{ID: "late-answer", ChatID: "chat-a",
		SenderID: "sender-a", Text: "resposta q_expired tarde"}); err != nil {
		t.Fatal(err)
	}
	_, messages = store.Snapshot()
	for _, msg := range messages {
		if msg.Direction == humanloop.DirectionInbound {
			t.Fatal("late reply became a new agent task")
		}
		if msg.ID == deferred.ID {
			if msg.Status != humanloop.StatusFailed {
				t.Fatalf("expired outbox status = %s", msg.Status)
			}
			oldUpdated := msg.UpdatedAt
			relay.expire(time.Now().Add(time.Minute))
			_, again := store.Snapshot()
			for _, item := range again {
				if item.ID == deferred.ID && !item.UpdatedAt.Equal(oldUpdated) {
					t.Fatal("expired outbox was retried repeatedly")
				}
			}
			return
		}
	}
	t.Fatal("deferred outbox missing")
}

func TestQuestionRelayReconcilesOutboxCrashWindow(t *testing.T) {
	store, gateway, relay, _, statePath := questionFixture(t)
	q := questionRecord{ID: "q_crash", Key: "crash-key", ChatID: "chat-a", SenderID: "sender-a",
		ExpiresAt: time.Now().Add(-time.Minute)}
	relay.mu.Lock()
	relay.questions[q.ID] = q
	if err := relay.saveLocked(); err != nil {
		relay.mu.Unlock()
		t.Fatal(err)
	}
	relay.mu.Unlock()
	out, err := store.EnqueueQuestionOutboundOnce(Channel, q.ChatID, "pergunta no crash", "ask:"+q.ID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// Simulate crash before OutboundID is persisted in question state.
	recovered, err := NewQuestionRelay(store, gateway, statePath, "")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.questions[q.ID].OutboundID != out.ID {
		t.Fatal("question outbox was not reconciled on restart")
	}
	recovered.expire(time.Now())
	_, messages := store.Snapshot()
	for _, msg := range messages {
		if msg.ID == out.ID {
			if msg.Status != humanloop.StatusFailed {
				t.Fatalf("orphan outbox status = %s", msg.Status)
			}
			return
		}
	}
	t.Fatal("orphan outbox missing")
}

func TestQuestionRelayLocalAnswerWinsAfterRemoteCancel(t *testing.T) {
	store, gateway, relay, driver, _ := questionFixture(t)
	base := WithInboundContext(context.Background(), InboundContext{
		MessageID: "local-answer-turn", ChatID: "chat-a", SenderID: "sender-a"})
	firstCtx, firstCancel := context.WithCancel(base)
	firstDone := make(chan struct{}, 1)
	go func() { _, _ = relay.Ask(firstCtx, "q_local_original", "Qual senha?"); firstDone <- struct{}{} }()
	_ = waitQuestionSend(t, driver.sent)
	firstCancel()
	<-firstDone
	retryCtx, retryCancel := context.WithCancel(base)
	retryDone := make(chan struct{}, 1)
	go func() { _, _ = relay.Ask(retryCtx, "q_local_retry", "Qual senha?"); retryDone <- struct{}{} }()
	select {
	case <-driver.sent:
		t.Fatal("retry sent already-delivered question twice")
	case <-time.After(20 * time.Millisecond):
	}
	retryCancel()
	<-retryDone
	if !relay.Owns("q_local_retry") {
		t.Fatal("question alias disappeared before local resolution callback")
	}
	if err := relay.ResolveQuestion("q_local_retry", "valor privado"); err != nil {
		t.Fatal(err)
	}
	if relay.Owns("q_local_retry") {
		t.Fatal("resolved alias was retained")
	}
	if relay.questions["q_local_original"].Answer != "valor privado" {
		t.Fatal("local answer was not saved to original question")
	}
	relay.mu.Lock()
	relay.aliases["q_stale"] = "q_local_original"
	q := relay.questions["q_local_original"]
	q.ExpiresAt = time.Now().Add(-8 * 24 * time.Hour)
	relay.questions[q.ID] = q
	relay.mu.Unlock()
	relay.expire(time.Now())
	if relay.Owns("q_stale") {
		t.Fatal("old alias survived question retention")
	}
	_ = store
	_ = gateway
}

func TestQuestionRelayRestartBeforeAnswerAndAmbiguousSend(t *testing.T) {
	store, gateway, relay, driver, statePath := questionFixture(t)
	base := WithInboundContext(context.Background(), InboundContext{
		MessageID: "same-inbound", ChatID: "chat-a", SenderID: "sender-a"})
	ctx, cancel := context.WithCancel(base)
	firstDone := make(chan error, 1)
	go func() { _, err := relay.Ask(ctx, "q_original", "Confirma?"); firstDone <- err }()
	first := waitQuestionSend(t, driver.sent)
	cancel()
	select {
	case err := <-firstDone:
		if err == nil {
			t.Fatal("cancelled first turn unexpectedly succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled turn did not stop")
	}
	restarted, err := NewQuestionRelay(store, gateway, statePath, "")
	if err != nil {
		t.Fatal(err)
	}
	gateway.SetQuestionRelay(restarted)
	answerCh := make(chan string, 1)
	go func() {
		answer, err := restarted.Ask(base, "q_after_restart", "Confirma?")
		if err != nil {
			answerCh <- "ERROR: " + err.Error()
			return
		}
		answerCh <- answer
	}()
	select {
	case <-driver.sent:
		t.Fatal("question was resent after restart despite durable delivery")
	case <-time.After(20 * time.Millisecond):
	}
	if err := gateway.Receive(context.Background(), Event{ID: "late-answer", ChatID: "chat-a",
		SenderID: "sender-a", Text: "resposta q_original sim"}); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-answerCh:
		if answer != "sim" {
			t.Fatalf("answer after restart = %q", answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("persisted question did not resume")
	}
	_, messages := store.Snapshot()
	questionOutboxes := 0
	for _, msg := range messages {
		if msg.Direction == humanloop.DirectionOutbound {
			questionOutboxes++
			if outboundID(msg.ID) != first.ID {
				t.Fatal("outbound identity changed across restart")
			}
		}
	}
	if questionOutboxes != 1 {
		t.Fatalf("question outboxes = %d", questionOutboxes)
	}

	// A transport may accept a send and still return an error. Retrying the
	// asking turn must reuse the same durable outbox and WhatsApp message ID.
	store2, gateway2, relay2, driver2, _ := questionFixture(t)
	driver2.fail = true
	base2 := WithInboundContext(context.Background(), InboundContext{
		MessageID: "ambiguous-inbound", ChatID: "chat-a", SenderID: "sender-a"})
	if _, err := relay2.Ask(base2, "q_ambiguous", "Prosseguir?"); err == nil {
		t.Fatal("ambiguous send should report its error")
	}
	accepted := waitQuestionSend(t, driver2.sent)
	driver2.fail = false
	ctx2, cancel2 := context.WithCancel(base2)
	done2 := make(chan error, 1)
	go func() { _, err := relay2.Ask(ctx2, "q_retry_ambiguous", "Prosseguir?"); done2 <- err }()
	retried := waitQuestionSend(t, driver2.sent)
	if retried.ID != accepted.ID {
		t.Fatal("ambiguous send changed WhatsApp message ID")
	}
	cancel2()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("second ask did not stop")
	}
	_, entries := store2.Snapshot()
	outboxes := 0
	for _, entry := range entries {
		if entry.Direction == humanloop.DirectionOutbound {
			outboxes++
		}
	}
	if outboxes != 1 {
		t.Fatalf("ambiguous send created %d outboxes", outboxes)
	}
	_ = gateway2
}

func TestQuestionRelayBackgroundUsesExactOwnerIdentity(t *testing.T) {
	dir := t.TempDir()
	store, err := humanloop.NewStore(filepath.Join(dir, "inbox.json"), 50)
	if err != nil {
		t.Fatal(err)
	}
	driver := &questionDriver{sent: make(chan Outbound, 2)}
	gateway, err := New(store, driver, []string{"owner@lid"}, []string{"owner@lid"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	relay, err := NewQuestionRelay(store, gateway, filepath.Join(dir, "questions.json"), "owner@lid")
	if err != nil {
		t.Fatal(err)
	}
	gateway.SetQuestionRelay(relay)
	answerCh := make(chan string, 1)
	go func() {
		answer, err := relay.Ask(context.Background(), "q_owner", "Qual rotina?")
		if err != nil {
			answerCh <- "ERROR: " + err.Error()
			return
		}
		answerCh <- answer
	}()
	msg := waitQuestionSend(t, driver.sent)
	if msg.ChatID != "owner@lid" {
		t.Fatal("background question did not use owner chat")
	}
	// A telephone JID is not an alias for the allowlisted LID owner.
	if err := gateway.Receive(context.Background(), Event{ID: "wrong-domain", ChatID: "owner@s.whatsapp.net",
		SenderID: "owner@s.whatsapp.net", Text: "resposta q_owner errado"}); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-answerCh:
		t.Fatalf("unmatched owner identity resolved question: %q", answer)
	default:
	}
	if err := gateway.Receive(context.Background(), Event{ID: "owner-answer", ChatID: "owner@lid",
		SenderID: "owner@lid", Text: "resposta q_owner diaria"}); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-answerCh:
		if answer != "diaria" {
			t.Fatalf("owner answer = %q", answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owner answer did not resolve")
	}
}

func TestQuestionRelayWaitsForReconnectBeforeSending(t *testing.T) {
	store, gateway, relay, driver, _ := questionFixture(t)
	relay.retryDelay = 10 * time.Millisecond
	driver.attempted = make(chan struct{}, 2)
	driver.offline.Store(true)
	ctx := WithInboundContext(context.Background(), InboundContext{
		MessageID: "offline-turn", ChatID: "chat-a", SenderID: "sender-a"})
	answerCh := make(chan string, 1)
	go func() {
		answer, err := relay.Ask(ctx, "q_offline", "Pode continuar?")
		if err != nil {
			answerCh <- "ERROR: " + err.Error()
			return
		}
		answerCh <- answer
	}()
	select {
	case <-driver.attempted:
	case <-time.After(2 * time.Second):
		t.Fatal("offline send was not attempted")
	}
	select {
	case <-answerCh:
		t.Fatal("temporary offline state aborted the asking turn")
	default:
	}
	driver.offline.Store(false)
	sent := waitQuestionSend(t, driver.sent)
	if err := gateway.Receive(context.Background(), Event{ID: "answer-after-reconnect", ChatID: "chat-a",
		SenderID: "sender-a", Text: "sim", ReplyTo: sent.ID}); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-answerCh:
		if answer != "sim" {
			t.Fatalf("answer after reconnect = %q", answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("question did not resume after reconnect")
	}
	_, messages := store.Snapshot()
	for _, msg := range messages {
		if msg.Direction == humanloop.DirectionOutbound && msg.Status != humanloop.StatusCompleted {
			t.Fatal("question outbox did not complete")
		}
	}
}

func TestQuestionRelayOfflineUntilDeadlineDefersInboundTurn(t *testing.T) {
	store, _, relay, driver, _ := questionFixture(t)
	relay.timeout = 80 * time.Millisecond
	relay.retryDelay = 5 * time.Millisecond
	driver.offline.Store(true)
	processCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status := NewAskDeliveryStatus(cancel)
	ctx := WithInboundContext(processCtx, InboundContext{
		MessageID: "offline-timeout", ChatID: "chat-a", SenderID: "sender-a", AskStatus: status})
	if _, err := relay.Ask(ctx, "q_offline_timeout", "Pode continuar?"); !errors.Is(err, humanloop.ErrRemoteUnavailable) {
		t.Fatalf("undelivered question must defer inbound turn: %v", err)
	}
	if processCtx.Err() == nil || !relay.ShouldDeferUnavailable(status) {
		t.Fatal("undelivered question did not cancel and mark its agent turn")
	}
	select {
	case <-driver.sent:
		t.Fatal("offline question was sent")
	default:
	}
	if _, claimed, err := store.ClaimNextDue(time.Now().Add(2 * time.Hour)); err != nil || claimed {
		t.Fatalf("question outbox was claimable by generic worker: claimed=%v err=%v", claimed, err)
	}
}

func TestQuestionRelayLocalTimeoutRaceKeepsOfflineMarker(t *testing.T) {
	_, _, relay, driver, _ := questionFixture(t)
	relay.retryDelay = 5 * time.Millisecond
	driver.attempted = make(chan struct{}, 1)
	driver.offline.Store(true)
	processCtx, cancelProcess := context.WithCancel(context.Background())
	defer cancelProcess()
	racedCtx, cancelRace := context.WithCancel(processCtx)
	status := NewAskDeliveryStatus(cancelProcess)
	ctx := WithInboundContext(racedCtx, InboundContext{
		MessageID: "local-timeout-race", ChatID: "chat-a", SenderID: "sender-a", AskStatus: status})
	done := make(chan error, 1)
	go func() { _, err := relay.Ask(ctx, "q_local_timeout", "Pode continuar?"); done <- err }()
	select {
	case <-driver.attempted:
	case <-time.After(time.Second):
		t.Fatal("offline question was not attempted")
	}
	cancelRace() // the TUI timer wins before the relay deadline
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ask cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ask did not stop after local timeout")
	}
	if processCtx.Err() != nil || !relay.ShouldDeferUnavailable(status) {
		t.Fatal("local timeout hid the undelivered question from the inbound processor")
	}
}

func TestQuestionRelayDeliveredTimeoutDoesNotDeferInbound(t *testing.T) {
	_, _, relay, driver, _ := questionFixture(t)
	relay.timeout = 80 * time.Millisecond
	processCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status := NewAskDeliveryStatus(cancel)
	ctx := WithInboundContext(processCtx, InboundContext{
		MessageID: "delivered-timeout", ChatID: "chat-a", SenderID: "sender-a", AskStatus: status})
	if _, err := relay.Ask(ctx, "q_delivered_timeout", "Pode continuar?"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("delivered question should use normal response timeout: %v", err)
	}
	_ = waitQuestionSend(t, driver.sent)
	if processCtx.Err() != nil || relay.ShouldDeferUnavailable(status) {
		t.Fatal("delivered question incorrectly deferred the inbound turn")
	}
}

func TestQuestionRelayLocalAnswerWinsWhileWhatsAppOffline(t *testing.T) {
	_, _, relay, driver, _ := questionFixture(t)
	relay.timeout = time.Second
	relay.retryDelay = 5 * time.Millisecond
	driver.attempted = make(chan struct{}, 1)
	driver.offline.Store(true)
	status := NewAskDeliveryStatus(nil)
	ctx := WithInboundContext(context.Background(), InboundContext{
		MessageID: "local-offline-answer", ChatID: "chat-a", SenderID: "sender-a", AskStatus: status})
	answerCh := make(chan string, 1)
	go func() {
		answer, err := relay.Ask(ctx, "q_local_offline", "Pode continuar?")
		if err != nil {
			answerCh <- "ERROR: " + err.Error()
			return
		}
		answerCh <- answer
	}()
	select {
	case <-driver.attempted:
	case <-time.After(time.Second):
		t.Fatal("offline question was not attempted")
	}
	if err := relay.ResolveQuestion("q_local_offline", "sim"); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-answerCh:
		if answer != "sim" {
			t.Fatalf("local answer = %q", answer)
		}
	case <-time.After(time.Second):
		t.Fatal("local answer did not resolve")
	}
	if relay.ShouldDeferUnavailable(status) {
		t.Fatal("local answer was incorrectly deferred")
	}
}
