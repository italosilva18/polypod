package whatsappgateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/costa/polypod/internal/humanloop"
)

type fakeDriver struct {
	sent []Outbound
	fail bool
}

func (f *fakeDriver) Send(ctx context.Context, m Outbound) error {
	f.sent = append(f.sent, m)
	if f.fail {
		return errors.New("offline")
	}
	return ctx.Err()
}

func TestPersistReplayRetryCorrelation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.json")
	store, err := humanloop.NewStore(path, 500)
	if err != nil {
		t.Fatal(err)
	}
	driver := &fakeDriver{}
	g, err := New(store, driver, []string{"chat"}, []string{"sender"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	evt := Event{ID: "event", ChatID: "chat", SenderID: "sender", Text: "answer", ReplyTo: "question"}
	if err = g.Receive(context.Background(), evt); err != nil {
		t.Fatal(err)
	}
	// Crash after persistence and before transport acknowledgement.
	store, err = humanloop.NewStore(path, 500)
	if err != nil {
		t.Fatal(err)
	}
	g, err = New(store, driver, []string{"chat"}, []string{"sender"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Receive(context.Background(), evt); err != nil {
		t.Fatal(err)
	}
	_, msgs := store.Snapshot()
	if len(msgs) != 1 {
		t.Fatalf("replay duplicated: %d", len(msgs))
	}
	decoded, err := Decode(msgs[0])
	if err != nil || decoded.ReplyTo != "question" {
		t.Fatalf("correlation lost: %+v %v", decoded, err)
	}
	if err = store.CompleteInbound(msgs[0].ID, "response", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, msgs = store.Snapshot()
	out := msgs[1]
	driver.fail = true
	if err = g.Deliver(context.Background(), out); err == nil {
		t.Fatal("send should fail")
	}
	driver.fail = false
	if err = g.Deliver(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	if driver.sent[0].ID != driver.sent[1].ID || driver.sent[1].ReplyTo != "event" || driver.sent[1].Participant != "sender" {
		t.Fatalf("retry/correlation: %+v", driver.sent)
	}
}
func TestAuthorizedSelfChatAndOutboundEchoAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.json")
	store, err := humanloop.NewStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	driver := &fakeDriver{}
	const chat = "12345@s.whatsapp.net"
	g, err := New(store, driver, []string{chat}, []string{chat}, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := store.EnqueueOutbound(Channel, chat, "resposta do Polypod", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Deliver(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(out.ID, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	store, err = humanloop.NewStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	wakes := 0
	g, err = New(store, driver, []string{chat}, []string{chat}, func() { wakes++ })
	if err != nil {
		t.Fatal(err)
	}
	echo := Event{ID: driver.sent[0].ID, ChatID: chat, SenderID: chat, Text: out.Text, FromMe: true}
	if reason := g.ignoreReason(echo); reason != "outbound_echo" {
		t.Fatalf("own echo classified as %q", reason)
	}
	if err := g.Receive(context.Background(), echo); err != nil {
		t.Fatal(err)
	}
	user := Event{ID: "phone-message", ChatID: chat, SenderID: chat, Text: "mensagem minha", FromMe: true}
	if err := g.Receive(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	_, messages := store.Snapshot()
	var incoming []Event
	for _, msg := range messages {
		if msg.Direction != humanloop.DirectionInbound {
			continue
		}
		decoded, err := Decode(msg)
		if err != nil {
			t.Fatal(err)
		}
		incoming = append(incoming, decoded)
	}
	if len(incoming) != 1 || incoming[0].ID != user.ID || !incoming[0].FromMe || wakes != 1 {
		t.Fatalf("unexpected inbound events: %+v (wakes %d)", incoming, wakes)
	}
}

func TestRejectUnauthorizedAndCancelled(t *testing.T) {
	s, err := humanloop.NewStore(filepath.Join(t.TempDir(), "inbox.json"), 10)
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDriver{}
	g, err := New(s, d, []string{"chat"}, []string{"sender"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, evt := range []Event{{ID: "1", ChatID: "other", SenderID: "sender", Text: "x", FromMe: true}, {ID: "2", ChatID: "chat", SenderID: "other", Text: "x", FromMe: true}} {
		if err = g.Receive(context.Background(), evt); err != nil {
			t.Fatal(err)
		}
	}
	_, msgs := s.Snapshot()
	if len(msgs) != 0 {
		t.Fatal("unauthorized inbox")
	}
	if err = g.Deliver(context.Background(), humanloop.Message{ID: "x", Channel: Channel, Direction: humanloop.DirectionOutbound, UserID: "other"}); err == nil {
		t.Fatal("unauthorized outbound")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = g.Receive(ctx, Event{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(d.sent) != 0 {
		t.Fatal("sent unauthorized")
	}
}
func TestPreflightProtectsSessionRegardlessOfHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), ".picoclaw", "workspace", "whatsapp")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "store.db"), []byte("not a DB"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := (&Native{SessionDir: dir}).Preflight(); err == nil {
		t.Fatal("live session accepted")
	}
	alias := filepath.Join(t.TempDir(), "session")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if err := (&Native{SessionDir: alias}).Preflight(); err == nil {
		t.Fatal("live symlink accepted")
	}
	if err := (&Native{}).Preflight(); err == nil {
		t.Fatal("empty accepted")
	}
}

func TestNativeRejectsUnpairedWithoutConnecting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "store.db"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := humanloop.NewStore(filepath.Join(t.TempDir(), "inbox.json"), 10)
	if err != nil {
		t.Fatal(err)
	}
	native := &Native{SessionDir: dir}
	g, err := New(s, native, []string{"chat"}, []string{"sender"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = native.Run(context.Background(), g); err == nil {
		t.Fatal("unpaired session accepted")
	}
	if native.client != nil || native.running {
		t.Fatal("failed startup left running state")
	}
}
