package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/costa/polypod/internal/humanloop"
)

func TestTelegramUnicodeChunkBoundaries(t *testing.T) {
	for _, text := range []string{strings.Repeat("á", 9000), strings.Repeat(string(rune(0x1f680)), 5000), strings.Repeat("abc_`[\\", 2000)} {
		parts := telegramChunks(text)
		if strings.Join(parts, "") != text {
			t.Fatal("split changed text")
		}
		for _, part := range parts {
			if !utf8.ValidString(part) || len(utf16.Encode([]rune(part))) > 4096 {
				t.Fatal("invalid or oversize chunk")
			}
		}
	}
}

func TestLongNotificationResumesAfterRestartWithoutResendingAcknowledgedChunk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.json")
	store, err := humanloop.NewStore(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("á文", 5000)
	msg, err := store.EnqueueOutbound("telegram", "42", body, "")
	if err != nil {
		t.Fatal(err)
	}
	relay := NewTelegramApprovalRelay(TelegramRelayConfig{BotToken: "test", ChatID: "42"})
	var accepted []string
	failSecond := true
	relay.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var payload map[string]string
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["parse_mode"] != "" {
			t.Fatal("notification must be plain text")
		}
		if len(utf16.Encode([]rune(payload["text"]))) > 4096 {
			t.Fatal("oversize payload")
		}
		status := http.StatusOK
		if failSecond && len(accepted) == 1 {
			status = http.StatusServiceUnavailable
			failSecond = false
		} else {
			accepted = append(accepted, payload["text"])
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})}
	deliver := func() error {
		return relay.SendNotificationChunks(context.Background(), "Polypod", body,
			func(index int) bool { return store.DeliveredTo(msg.ID, fmt.Sprintf("telegram:chunk:%d", index)) },
			func(index int) error { return store.MarkDelivered(msg.ID, fmt.Sprintf("telegram:chunk:%d", index)) })
	}
	if err := deliver(); err == nil {
		t.Fatal("expected interrupted delivery")
	}
	store, err = humanloop.NewStore(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := deliver(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(accepted, ""); got != "Polypod\n\n"+body {
		t.Fatal("chunks repeated or omitted after restart")
	}
	count := len(accepted)
	if err := deliver(); err != nil {
		t.Fatal(err)
	}
	if len(accepted) != count {
		t.Fatal("already acknowledged delivery sent again")
	}
}
