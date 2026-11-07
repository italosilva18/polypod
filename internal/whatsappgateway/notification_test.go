package whatsappgateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/humanloop"
	"github.com/costa/polypod/internal/skill"
)

func TestRegisterNotificationSkillQueuesDurablyAndWakes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.json")
	store, err := humanloop.NewStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	owner := "15551234567@s.whatsapp.net"
	driver := &fakeDriver{}
	wakes := 0
	gateway, err := New(store, driver, []string{owner}, []string{owner}, func() { wakes++ })
	if err != nil {
		t.Fatal(err)
	}
	reg := skill.NewRegistry()
	if err := RegisterNotificationSkill(reg, gateway, owner); err != nil {
		t.Fatal(err)
	}
	result, err := reg.Execute("whatsapp_notify", `{"message":"  status pronto  "}`)
	if err != nil || !strings.Contains(result, "enfileirada com ID") {
		t.Fatalf("skill result = %q, %v", result, err)
	}
	if wakes != 1 || len(driver.sent) != 0 {
		t.Fatalf("wake = %d; direct sends = %d", wakes, len(driver.sent))
	}
	reloaded, err := humanloop.NewStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	_, messages := reloaded.Snapshot()
	if len(messages) != 1 {
		t.Fatalf("persisted messages = %d", len(messages))
	}
	msg := messages[0]
	if msg.Channel != Channel || msg.Direction != humanloop.DirectionOutbound || msg.Status != humanloop.StatusPending || msg.UserID != owner || msg.Text != "status pronto" || msg.ReplyTo != "" || msg.ID == "" {
		t.Fatalf("queued message = %+v", msg)
	}
	registered, ok := reg.Get("whatsapp_notify")
	if !ok {
		t.Fatal("skill missing")
	}
	if _, err := registered.Execute(map[string]string{"message": " \t"}); err == nil {
		t.Fatal("empty notification accepted")
	}
	_, messages = store.Snapshot()
	if len(messages) != 1 || wakes != 1 {
		t.Fatalf("empty notification queued or woke processor: messages = %d, wakes = %d", len(messages), wakes)
	}
}

func TestRegisterNotificationSkillRejectsUnconfiguredDestination(t *testing.T) {
	store, err := humanloop.NewStore(filepath.Join(t.TempDir(), "inbox.json"), 10)
	if err != nil {
		t.Fatal(err)
	}
	owner := "15551234567@s.whatsapp.net"
	gateway, err := New(store, &fakeDriver{}, []string{owner}, []string{owner}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, chat := range []string{"", "other@s.whatsapp.net", " " + owner} {
		reg := skill.NewRegistry()
		if err := RegisterNotificationSkill(reg, gateway, chat); err == nil {
			t.Fatalf("owner chat %q accepted", chat)
		}
		if _, ok := reg.Get("whatsapp_notify"); ok {
			t.Fatalf("skill registered for owner chat %q", chat)
		}
	}
}

func TestNotificationSkillDoesNotWakeWhenPersistenceFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.json")
	store, err := humanloop.NewStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	owner := "15551234567@s.whatsapp.net"
	wakes := 0
	gateway, err := New(store, &fakeDriver{}, []string{owner}, []string{owner}, func() { wakes++ })
	if err != nil {
		t.Fatal(err)
	}
	reg := skill.NewRegistry()
	if err := RegisterNotificationSkill(reg, gateway, owner); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	registered, ok := reg.Get("whatsapp_notify")
	if !ok {
		t.Fatal("skill missing")
	}
	if _, err := registered.Execute(map[string]string{"message": "status"}); err == nil {
		t.Fatal("failed persistence reported success")
	}
	_, messages := store.Snapshot()
	if len(messages) != 0 || wakes != 0 {
		t.Fatalf("failed persistence left messages = %d, wakes = %d", len(messages), wakes)
	}
}

func TestMediaSkillQueuesFileWithoutSending(t *testing.T) {
	store, err := humanloop.NewStore(filepath.Join(t.TempDir(), "inbox.json"), 10)
	if err != nil {
		t.Fatal(err)
	}
	const owner = "15551234567@s.whatsapp.net"
	driver := &fakeDriver{}
	wakes := 0
	gateway, err := New(store, driver, []string{owner}, []string{owner}, func() { wakes++ })
	if err != nil {
		t.Fatal(err)
	}
	reg := skill.NewRegistry()
	if err := RegisterNotificationSkill(reg, gateway, owner); err != nil {
		t.Fatal(err)
	}
	mediaSkill, ok := reg.Get("whatsapp_send_media")
	if !ok {
		t.Fatal("media skill missing")
	}
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\n%%EOF"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := mediaSkill.Execute(map[string]string{"path": path, "caption": "Veja o relatorio"})
	if err != nil || !strings.Contains(result, "enfileirada com ID") {
		t.Fatalf("media skill result = %q, %v", result, err)
	}
	if wakes != 1 || len(driver.sent) != 0 {
		t.Fatalf("wakes=%d sends=%d", wakes, len(driver.sent))
	}
	_, messages := store.Snapshot()
	if len(messages) != 1 || messages[0].Media == nil || messages[0].Media.Kind != "document" {
		t.Fatalf("media outbox = %+v", messages)
	}
}
