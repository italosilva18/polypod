package humanloop

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOutboundMediaPersistsThroughRetryAndPrunesWithHistory(t *testing.T) {
	store, journal := newTestStore(t, 1)
	data := []byte("private media bytes")
	first, err := store.EnqueueOutboundMedia("whatsapp", "owner", "", "", MediaAttachment{
		Kind: "audio", MIME: "audio/ogg; codecs=opus", Filename: "voice.ogg",
	}, data)
	if err != nil {
		t.Fatal(err)
	}
	if first.Media == nil || first.Text != "" || first.Media.Size != int64(len(data)) {
		t.Fatalf("queued media metadata = %+v", first)
	}
	mediaPath := filepath.Join(journal+".media", first.Media.Blob)
	for _, path := range []string{journal + ".media", mediaPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0600)
		if info.IsDir() {
			want = 0700
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
		}
	}
	claimed, ok, err := store.ClaimNextDue(time.Now().Add(time.Second))
	if err != nil || !ok || claimed.ID != first.ID {
		t.Fatalf("claim = %+v, %v, %v", claimed, ok, err)
	}
	reloaded, err := NewStore(journal, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, messages := reloaded.Snapshot()
	if len(messages) != 1 || messages[0].ID != first.ID || messages[0].Status != StatusPending {
		t.Fatalf("recovered = %+v", messages)
	}
	got, err := reloaded.ReadOutboundMedia(messages[0])
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("recovered bytes = %q, %v", got, err)
	}
	if err := reloaded.Complete(first.ID, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	second, err := reloaded.EnqueueOutboundMedia("whatsapp", "owner", "", "", MediaAttachment{
		Kind: "document", MIME: "application/pdf", Filename: "file.pdf",
	}, []byte("%PDF-1.4\n%%EOF"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Complete(second.ID, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mediaPath); !os.IsNotExist(err) {
		t.Fatalf("trimmed media should be removed: %v", err)
	}
	if _, err := reloaded.ReadOutboundMedia(second); err != nil {
		t.Fatalf("retained media missing: %v", err)
	}
}

func TestOutboundMediaRejectsTamperingAndUnsafeReferences(t *testing.T) {
	store, journal := newTestStore(t, 10)
	msg, err := store.EnqueueOutboundMedia("whatsapp", "owner", "caption", "", MediaAttachment{
		Kind: "image", MIME: "image/png", Filename: "image.png",
	}, []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(journal+".media", msg.Media.Blob)
	if err := os.WriteFile(path, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadOutboundMedia(msg); err == nil {
		t.Fatal("modified media accepted")
	}
	unsafe := msg
	clone := *msg.Media
	clone.Blob = "../outside.blob"
	unsafe.Media = &clone
	if _, err := store.ReadOutboundMedia(unsafe); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestOutboundMediaRejectsSymlinkStorageDirectory(t *testing.T) {
	store, journal := newTestStore(t, 10)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(journal), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, journal+".media"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueOutboundMedia("whatsapp", "owner", "", "", MediaAttachment{
		Kind: "audio", MIME: "audio/ogg; codecs=opus", Filename: "voice.ogg",
	}, []byte("voice")); err == nil {
		t.Fatal("symlink media directory accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside path touched: %d files, %v", len(entries), err)
	}
}

func TestOutboundMediaKeepsPublishedJournalAfterDirectorySyncFailure(t *testing.T) {
	store, journal := newTestStore(t, 1)
	first, err := store.EnqueueOutboundMedia("whatsapp", "owner", "", "", MediaAttachment{
		Kind: "audio", MIME: "audio/ogg; codecs=opus", Filename: "first.ogg",
	}, []byte("first payload"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(first.ID, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	store.syncDir = func(*os.File) error { return errors.New("injected directory sync failure") }
	second, err := store.EnqueueOutboundMedia("whatsapp", "owner", "", "", MediaAttachment{
		Kind: "audio", MIME: "audio/ogg; codecs=opus", Filename: "second.ogg",
	}, []byte("second payload"))
	if err != nil {
		t.Fatal("published media must remain queued when only directory sync fails:", err)
	}
	store.syncDir = nil
	if _, err := store.EnqueueOutbound("whatsapp", "owner", "later save", ""); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(journal, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, messages := reloaded.Snapshot()
	found := false
	for _, message := range messages {
		if message.ID == second.ID {
			found = true
			if data, err := reloaded.ReadOutboundMedia(message); err != nil || !bytes.Equal(data, []byte("second payload")) {
				t.Fatalf("published media lost after next save: %q, %v", data, err)
			}
		}
	}
	if !found {
		t.Fatal("published media missing after reload")
	}
	store.syncDir = func(*os.File) error { return errors.New("injected directory sync failure") }
	if err := store.Complete(second.ID, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	firstBlob := filepath.Join(journal+".media", first.Media.Blob)
	if _, err := os.Stat(firstBlob); err != nil {
		t.Fatal("blob referenced by possible pre-sync journal was pruned:", err)
	}
	store.syncDir = nil
	if err := store.RecordHeartbeat(time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(firstBlob); !os.IsNotExist(err) {
		t.Fatalf("old blob should be pruned after successful sync: %v", err)
	}
}
