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

func retentionFixture(t *testing.T) (*Native, *Gateway, string) {
	t.Helper()
	sessionDir := t.TempDir()
	if err := os.Chmod(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "store.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	mediaDir := filepath.Join(sessionDir, "media")
	if err := os.Mkdir(mediaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := humanloop.NewStore(filepath.Join(t.TempDir(), "inbox.json"), 10)
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(store, &fakeDriver{}, []string{"chat"}, []string{"sender"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Native{SessionDir: sessionDir}, g, mediaDir
}

func retentionFile(t *testing.T, dir, name string, modified time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCleanupMediaKeepsActiveInboxReferences(t *testing.T) {
	for _, status := range []string{"pending", "processing", "retry"} {
		t.Run(status, func(t *testing.T) {
			native, g, mediaDir := retentionFixture(t)
			now := time.Now()
			old := now.Add(-mediaRetention - time.Hour)
			active := retentionFile(t, mediaDir, "active.bin", old)
			orphan := retentionFile(t, mediaDir, "orphan.bin", old)
			boundary := retentionFile(t, mediaDir, "boundary.bin", now.Add(-mediaRetention))
			recent := retentionFile(t, mediaDir, "recent.bin", now.Add(-time.Hour))
			if err := g.Receive(context.Background(), Event{
				ID: "event", ChatID: "chat", SenderID: "sender", MediaPath: active, MIME: "image/png",
			}); err != nil {
				t.Fatal(err)
			}
			if status != "pending" {
				claimed, ok, err := g.store.ClaimNextDue(now.Add(time.Second))
				if err != nil || !ok {
					t.Fatalf("claim: %v, %t", err, ok)
				}
				if status == "retry" {
					if err := g.store.Retry(claimed.ID, errors.New("temporary"), now.Add(time.Hour), 3); err != nil {
						t.Fatal(err)
					}
				}
			}
			removed, err := native.CleanupMedia(context.Background(), g, now)
			if err != nil || removed != 1 {
				t.Fatalf("cleanup removed %d: %v", removed, err)
			}
			for _, path := range []string{active, boundary, recent} {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("protected file %q: %v", path, err)
				}
			}
			if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("old orphan remains: %v", err)
			}
		})
	}
}

func TestCleanupMediaRemovesCompletedReferenceAndSkipsSymlink(t *testing.T) {
	native, g, mediaDir := retentionFixture(t)
	now := time.Now()
	old := now.Add(-mediaRetention - time.Hour)
	completed := retentionFile(t, mediaDir, "completed.bin", old)
	outside := retentionFile(t, t.TempDir(), "outside.bin", old)
	link := filepath.Join(mediaDir, "link.bin")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := g.Receive(context.Background(), Event{
		ID: "event", ChatID: "chat", SenderID: "sender", MediaPath: completed, MIME: "image/png",
	}); err != nil {
		t.Fatal(err)
	}
	_, messages := g.store.Snapshot()
	if err := g.store.Complete(messages[0].ID, "", now); err != nil {
		t.Fatal(err)
	}
	removed, err := native.CleanupMedia(context.Background(), g, now)
	if err != nil || removed != 1 {
		t.Fatalf("cleanup removed %d: %v", removed, err)
	}
	if _, err := os.Stat(completed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed media remains: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("symlink was removed: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("symlink target was touched: %v", err)
	}
}

func TestCleanupMediaRejectsUnsafePathsBeforeDeleting(t *testing.T) {
	t.Run("media directory symlink", func(t *testing.T) {
		native, g, mediaDir := retentionFixture(t)
		if err := os.Remove(mediaDir); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		old := retentionFile(t, outside, "old.bin", time.Now().Add(-mediaRetention-time.Hour))
		if err := os.Symlink(outside, mediaDir); err != nil {
			t.Fatal(err)
		}
		removed, err := native.CleanupMedia(context.Background(), g, time.Now())
		if removed != 0 || !errors.Is(err, ErrUnsafeMediaPath) {
			t.Fatalf("unsafe directory accepted: removed=%d err=%v", removed, err)
		}
		if _, err := os.Stat(old); err != nil {
			t.Fatalf("outside file touched: %v", err)
		}
	})

	t.Run("active media outside directory", func(t *testing.T) {
		native, g, mediaDir := retentionFixture(t)
		now := time.Now()
		old := retentionFile(t, mediaDir, "old.bin", now.Add(-mediaRetention-time.Hour))
		outside := retentionFile(t, t.TempDir(), "outside.bin", now.Add(-mediaRetention-time.Hour))
		if err := g.Receive(context.Background(), Event{
			ID: "event", ChatID: "chat", SenderID: "sender", MediaPath: outside, MIME: "image/png",
		}); err != nil {
			t.Fatal(err)
		}
		removed, err := native.CleanupMedia(context.Background(), g, now)
		if removed != 0 || !errors.Is(err, ErrUnsafeMediaPath) {
			t.Fatalf("unsafe reference accepted: removed=%d err=%v", removed, err)
		}
		if _, err := os.Stat(old); err != nil {
			t.Fatalf("file deleted before reference validation: %v", err)
		}
	})
}

func TestCleanupMediaRejectsLivePolyClawPath(t *testing.T) {
	native, g, mediaDir := retentionFixture(t)
	live := filepath.Join(t.TempDir(), ".picoclaw", "workspace", "whatsapp")
	if err := os.MkdirAll(live, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "store.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(mediaDir, filepath.Join(live, "media")); err != nil {
		t.Fatal(err)
	}
	old := retentionFile(t, filepath.Join(live, "media"), "old.bin", time.Now().Add(-mediaRetention-time.Hour))
	native.SessionDir = live
	removed, err := native.CleanupMedia(context.Background(), g, time.Now())
	if removed != 0 || err == nil {
		t.Fatalf("live PolyClaw path accepted: removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("live media touched: %v", err)
	}
}
