package memory

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/database"
)

func TestSharedOwnerMemoryChildProcess(t *testing.T) {
	if os.Getenv("POLYPOD_TEST_OWNER_MEMORY_CHILD") != "1" {
		return
	}
	shared, err := NewSharedFileStore(os.Getenv("POLYPOD_TEST_OWNER_MEMORY_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	if err := shared.SaveWithMeta(context.Background(), Memory{
		Topic: os.Getenv("POLYPOD_TEST_OWNER_MEMORY_TOPIC"), Content: "child fact",
		Source: "user", SessionID: "cli:local", Confidence: 1,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerOverlayBackfillCrossProcessAndForget(t *testing.T) {
	const owner = "owner@example.test"
	base := t.TempDir()
	sharedDir := filepath.Join(base, "owner")
	cliPrivate := NewFileStore(filepath.Join(base, "cli"))
	waPrivate := NewFileStore(filepath.Join(base, "whatsapp"))
	ctx := context.Background()
	for _, m := range []Memory{
		{Topic: "legacy_cli_preference", Content: "prefers short answers", Source: "user"},
		{Topic: "legacy_cli_episode", Content: "private transcript", Source: "dream_episode", SessionID: "cli:local"},
	} {
		if err := cliPrivate.SaveWithMeta(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []Memory{
		{Topic: "owner_quote", Content: "prefers Go", Source: "dream", SessionID: "whatsapp:" + owner, Tags: []string{"evidence:quote_verified"}},
		{Topic: "other_chat_fact", Content: "other person's private detail", Source: "user", SessionID: "whatsapp:other@example.test"},
		{Topic: "unverified_owner_guess", Content: "inferred without quote", Source: "dream", SessionID: "whatsapp:" + owner},
	} {
		if err := waPrivate.SaveWithMeta(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	cliOverlay, err := NewOwnerOverlayStore(cliPrivate, sharedDir, owner)
	if err != nil {
		t.Fatal(err)
	}
	waOverlay, err := NewOwnerOverlayStore(waPrivate, sharedDir, owner)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := cliOverlay.BackfillOwnerFacts(ctx, filepath.Join(base, "cli-backup"), true); err != nil || count != 1 {
		t.Fatalf("CLI backfill count=%d err=%v", count, err)
	}
	if count, err := waOverlay.BackfillOwnerFacts(ctx, filepath.Join(base, "wa-backup"), false); err != nil || count != 1 {
		t.Fatalf("WhatsApp backfill count=%d err=%v", count, err)
	}
	for _, dir := range []string{filepath.Join(base, "cli-backup"), filepath.Join(base, "wa-backup")} {
		files, err := filepath.Glob(filepath.Join(dir, "owner-memory-pre-import-*.json"))
		if err != nil || len(files) != 1 {
			t.Fatalf("backup count=%d err=%v", len(files), err)
		}
		info, err := os.Stat(files[0])
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("backup mode=%v err=%v", info.Mode(), err)
		}
	}
	cliMemory := NewManagedStore(cliOverlay)
	waMemory := NewManagedStore(waOverlay)
	cliCtx := WithSessionScope(WithOwnerLinkedMemory(WithGlobalMemoryAccess(ctx), "cli", owner), "cli:local")
	waCtx := WithSessionScope(WithOwnerLinkedMemory(WithGlobalMemoryAccess(ctx), "whatsapp", owner), "whatsapp:"+owner)
	otherCtx := WithSessionScope(ctx, "whatsapp:other@example.test")
	if _, err := waMemory.Get(waCtx, "legacy_cli_preference"); err != nil {
		t.Fatalf("owner WhatsApp missed CLI fact: %v", err)
	}
	if _, err := cliMemory.Get(cliCtx, "owner_quote"); err != nil {
		t.Fatalf("CLI missed owner WhatsApp fact: %v", err)
	}
	if _, err := waMemory.Get(waCtx, "other_chat_fact"); err == nil {
		t.Fatal("owner WhatsApp saw another chat's private fact")
	}
	if _, err := cliMemory.Get(cliCtx, "unverified_owner_guess"); err == nil {
		t.Fatal("CLI saw unverified owner guess from another channel")
	}
	if _, err := waMemory.Get(otherCtx, "legacy_cli_preference"); err == nil {
		t.Fatal("other chat saw owner CLI fact")
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const childTopic = "separate_process_fact"
	cmd := exec.Command(exe, "-test.run=^TestSharedOwnerMemoryChildProcess$")
	cmd.Env = append(os.Environ(),
		"POLYPOD_TEST_OWNER_MEMORY_CHILD=1",
		"POLYPOD_TEST_OWNER_MEMORY_DIR="+sharedDir,
		"POLYPOD_TEST_OWNER_MEMORY_TOPIC="+childTopic)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child write failed: %v: %s", err, output)
	}
	if _, err := waMemory.Get(waCtx, childTopic); err != nil {
		t.Fatalf("parent did not reload child write: %v", err)
	}
	if err := waMemory.Delete(waCtx, "legacy_cli_preference"); err != nil {
		t.Fatal(err)
	}
	// The old CLI database still contains the fact, but the shared tombstone
	// must hide it both now and after a fresh process runs backfill again.
	if _, err := cliMemory.Get(cliCtx, "legacy_cli_preference"); err == nil {
		t.Fatal("deleted owner fact remained visible in CLI")
	}
	restarted, err := NewOwnerOverlayStore(cliPrivate, sharedDir, owner)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := restarted.BackfillOwnerFacts(ctx, filepath.Join(base, "cli-backup"), true); err != nil || count != 0 {
		t.Fatalf("restart imported forgotten fact count=%d err=%v", count, err)
	}
	if _, err := NewManagedStore(restarted).Get(cliCtx, "legacy_cli_preference"); err == nil {
		t.Fatal("backfill resurrected forgotten fact")
	}
	sharedContents, err := os.ReadFile(filepath.Join(sharedDir, "owner-memories.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sharedContents), "other person's private detail") || strings.Contains(string(sharedContents), "private transcript") {
		t.Fatal("shared owner file contains another chat or episode")
	}
	for _, name := range []string{"owner-memories.json", "owner-memories.json.lock"} {
		info, err := os.Stat(filepath.Join(sharedDir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("shared file %s mode=%v err=%v", name, info.Mode(), err)
		}
	}
}

func TestOwnerOverlayExplicitWritesAndOtherChatStaySeparate(t *testing.T) {
	const owner = "owner@example.test"
	base := t.TempDir()
	private := NewFileStore(filepath.Join(base, "private"))
	overlay, err := NewOwnerOverlayStore(private, filepath.Join(base, "shared"), owner)
	if err != nil {
		t.Fatal(err)
	}
	managed := NewManagedStore(overlay)
	cliCtx := WithOwnerLinkedMemory(WithGlobalMemoryAccess(context.Background()), "cli", owner)
	otherCtx := conversation.WithJournalScope(context.Background(), "whatsapp", "other@example.test", "", "default")
	otherCtx = scopedMemorySkillContext(otherCtx)
	if err := managed.Save(cliCtx, "owner_explicit", "owner fact"); err != nil {
		t.Fatal(err)
	}
	otherTopic, otherScope := scopedUserTopic(otherCtx, "other_explicit")
	if err := managed.SaveWithMeta(otherCtx, Memory{Topic: otherTopic, Content: "other fact", SessionID: otherScope, Source: "user"}); err != nil {
		t.Fatal(err)
	}
	shared, err := overlay.shared.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(shared) != 1 || shared[0].Topic != "owner_explicit" {
		t.Fatalf("shared owner store has wrong topics: %d", len(shared))
	}
	privateList, err := private.List(context.Background())
	if err != nil || len(privateList) != 1 || privateList[0].Topic != otherTopic {
		t.Fatalf("private store has wrong topics: count=%d err=%v", len(privateList), err)
	}
}

func TestOwnerMemoryDirMustBeAbsolute(t *testing.T) {
	if _, err := NewSharedFileStore("relative/owner-memory"); err == nil {
		t.Fatal("relative shared owner memory directory was accepted")
	}
}

func TestSharedMemoryKeyWithNUL(t *testing.T) {
	a := Memory{Topic: "a\x00b", SessionID: "c", Source: "user"}
	b := Memory{Topic: "a", SessionID: "b\x00c", Source: "user"}
	if sharedMemoryKey(a) == sharedMemoryKey(b) {
		t.Fatal("different topic/scope pairs collided")
	}
	store, err := NewSharedFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []Memory{a, b} {
		if err := store.SaveWithMeta(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	all, err := store.List(context.Background())
	if err != nil || len(all) != 2 {
		t.Fatalf("NUL keys merged: count=%d err=%v", len(all), err)
	}
}

func TestOwnerMemoryBackupRejectsCurrentDirectory(t *testing.T) {
	if err := backupOwnerMemories(".", []Memory{{Topic: "synthetic"}}); err == nil {
		t.Fatal("backup in current directory was accepted")
	}
}

func TestOwnerOverlayRevisionForgetSurvivesBackfill(t *testing.T) {
	const owner = "owner@example.test"
	base := t.TempDir()
	private := NewFileStore(filepath.Join(base, "private"))
	ctx := context.Background()
	const topic = "revision_example"
	if err := private.SaveWithMeta(ctx, Memory{Topic: topic, Content: "old owner fact", Source: "user",
		SessionID: "whatsapp:" + owner, Tags: []string{supersededTag, "memory:parent=owner_fact"}}); err != nil {
		t.Fatal(err)
	}
	overlay, err := NewOwnerOverlayStore(private, filepath.Join(base, "shared"), owner)
	if err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(base, "backup")
	if count, err := overlay.BackfillOwnerFacts(ctx, backupDir, false); err != nil || count != 1 {
		t.Fatalf("first import count=%d err=%v", count, err)
	}
	if err := overlay.Delete(ctx, topic); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewOwnerOverlayStore(private, filepath.Join(base, "shared"), owner)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := restarted.BackfillOwnerFacts(ctx, backupDir, false); err != nil || count != 0 {
		t.Fatalf("revision reimport count=%d err=%v", count, err)
	}
	got, err := restarted.Get(ctx, topic)
	if err != nil || got.Content != "" || !hasTag(*got, forgottenTag) {
		t.Fatalf("revision marker lost: err=%v", err)
	}
}

func TestOwnerOverlaySeparateSQLiteDatabases(t *testing.T) {
	const owner = "owner@example.test"
	ctx := context.Background()
	base := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cliDB, err := database.NewSQLite(filepath.Join(base, "cli", "polypod.db"), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer cliDB.DB.Close()
	waDB, err := database.NewSQLite(filepath.Join(base, "service", "polypod.db"), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer waDB.DB.Close()
	for _, db := range []*database.SQLiteDB{cliDB, waDB} {
		if err := db.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	cliPrivate := NewSQLiteStore(cliDB.DB)
	waPrivate := NewSQLiteStore(waDB.DB)
	if err := cliPrivate.SaveWithMeta(ctx, Memory{Topic: "cli_old", Content: "local fact", Source: "user"}); err != nil {
		t.Fatal(err)
	}
	if err := waPrivate.SaveWithMeta(ctx, Memory{Topic: "wa_old", Content: "owner chat fact", Source: "user", SessionID: "whatsapp:" + owner}); err != nil {
		t.Fatal(err)
	}
	cliOverlay, err := NewOwnerOverlayStore(cliPrivate, filepath.Join(base, "shared"), owner)
	if err != nil {
		t.Fatal(err)
	}
	waOverlay, err := NewOwnerOverlayStore(waPrivate, filepath.Join(base, "shared"), owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cliOverlay.BackfillOwnerFacts(ctx, filepath.Join(base, "cli-backup"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := waOverlay.BackfillOwnerFacts(ctx, filepath.Join(base, "service-backup"), false); err != nil {
		t.Fatal(err)
	}
	cliCtx := WithSessionScope(WithOwnerLinkedMemory(WithGlobalMemoryAccess(ctx), "cli", owner), "cli:local")
	waCtx := WithSessionScope(WithOwnerLinkedMemory(WithGlobalMemoryAccess(ctx), "whatsapp", owner), "whatsapp:"+owner)
	if _, err := NewManagedStore(cliOverlay).Get(cliCtx, "wa_old"); err != nil {
		t.Fatalf("CLI cannot read service SQLite fact: %v", err)
	}
	if _, err := NewManagedStore(waOverlay).Get(waCtx, "cli_old"); err != nil {
		t.Fatalf("service cannot read CLI SQLite fact: %v", err)
	}
}

func TestOwnerOverlaySameTopicInOwnerAndOtherChat(t *testing.T) {
	const owner = "owner@example.test"
	base := t.TempDir()
	private := NewFileStore(filepath.Join(base, "private"))
	overlay, err := NewOwnerOverlayStore(private, filepath.Join(base, "shared"), owner)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const topic = "same_topic"
	if err := private.SaveWithMeta(ctx, Memory{Topic: topic, Content: "other private fact", Source: "user", SessionID: "whatsapp:other@example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := overlay.shared.SaveWithMeta(ctx, Memory{Topic: topic, Content: "owner fact", Source: "user", SessionID: "whatsapp:" + owner}); err != nil {
		t.Fatal(err)
	}
	managed := NewManagedStore(overlay)
	ownerCtx := WithSessionScope(WithOwnerLinkedMemory(WithGlobalMemoryAccess(ctx), "whatsapp", owner), "whatsapp:"+owner)
	otherCtx := WithSessionScope(ctx, "whatsapp:other@example.test")
	for _, tc := range []struct {
		ctx     context.Context
		content string
	}{{ownerCtx, "owner fact"}, {otherCtx, "other private fact"}} {
		m, err := managed.Get(tc.ctx, topic)
		if err != nil || m.Content != tc.content {
			t.Fatalf("wrong fact selected for scope: err=%v", err)
		}
		list, err := managed.List(tc.ctx)
		if err != nil || len(list) != 1 || list[0].Content != tc.content {
			t.Fatalf("wrong list for scope: count=%d err=%v", len(list), err)
		}
	}
	if err := managed.SaveWithMeta(otherCtx, Memory{Topic: topic, Content: "other fact updated", Source: "user", SessionID: "whatsapp:other@example.test"}); err != nil {
		t.Fatal(err)
	}
	if m, err := managed.Get(ownerCtx, topic); err != nil || m.Content != "owner fact" {
		t.Fatalf("other chat update changed owner fact: %v", err)
	}
	if err := managed.Delete(ownerCtx, topic); err != nil {
		t.Fatal(err)
	}
	if _, err := managed.Get(ownerCtx, topic); err == nil {
		t.Fatal("owner fact remained active after delete")
	}
	m, err := managed.Get(otherCtx, topic)
	if err != nil || m.Content != "other fact updated" {
		t.Fatalf("owner deletion hid another chat's fact: %v", err)
	}
}

func TestOwnerOverlaySameTopicInCLIAndOwnerWhatsApp(t *testing.T) {
	const owner = "owner@example.test"
	base := t.TempDir()
	overlay, err := NewOwnerOverlayStore(NewFileStore(filepath.Join(base, "private")), filepath.Join(base, "shared"), owner)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cliCtx := WithSessionScope(WithOwnerLinkedMemory(WithGlobalMemoryAccess(ctx), "cli", owner), "cli:local")
	waCtx := WithSessionScope(WithOwnerLinkedMemory(WithGlobalMemoryAccess(ctx), "whatsapp", owner), "whatsapp:"+owner)
	const topic = "same_owner_topic"
	managed := NewManagedStore(overlay)
	if err := managed.SaveWithMeta(cliCtx, Memory{Topic: topic, Content: "CLI value", Source: "user", SessionID: "cli:local"}); err != nil {
		t.Fatal(err)
	}
	if err := managed.SaveWithMeta(waCtx, Memory{Topic: topic, Content: "WhatsApp value", Source: "user", SessionID: "whatsapp:" + owner}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ctx     context.Context
		content string
	}{{cliCtx, "CLI value"}, {waCtx, "WhatsApp value"}} {
		got, err := managed.Get(tc.ctx, topic)
		if err != nil || got.Content != tc.content {
			t.Fatalf("same owner topic selected wrong scope: err=%v", err)
		}
	}
	if err := managed.Delete(waCtx, topic); err != nil {
		t.Fatal(err)
	}
	if _, err := managed.Get(waCtx, topic); err == nil {
		t.Fatal("WhatsApp fact remained active")
	}
	got, err := managed.Get(cliCtx, topic)
	if err != nil || got.Content != "CLI value" {
		t.Fatalf("WhatsApp deletion changed CLI fact: %v", err)
	}
}
