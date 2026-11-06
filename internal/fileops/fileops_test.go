package fileops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------- FileReadCache: basic read / missing file ----------

func TestFileReadCache_ReadExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	c := NewFileReadCache(10)
	content, err := c.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if content != "hello world" {
		t.Errorf("content = %q; want %q", content, "hello world")
	}
	if c.Size() != 1 {
		t.Errorf("cache size = %d; want 1", c.Size())
	}
}

func TestFileReadCache_ReadMissing(t *testing.T) {
	c := NewFileReadCache(10)
	_, err := c.ReadFile(filepath.Join(t.TempDir(), "nonexistent.txt"))
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestFileReadCache_HitMissStats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("abc"), 0o644)

	c := NewFileReadCache(10)
	// First read: miss.
	if _, err := c.ReadFile(path); err != nil {
		t.Fatalf("read1: %v", err)
	}
	// Second read: hit.
	if _, err := c.ReadFile(path); err != nil {
		t.Fatalf("read2: %v", err)
	}
	stats := c.Stats()
	if stats.Hits.Load() != 1 || stats.Misses.Load() != 1 {
		t.Errorf("stats: hits=%d misses=%d; want 1,1", stats.Hits.Load(), stats.Misses.Load())
	}
	if rate := stats.HitRate(); rate != 0.5 {
		t.Errorf("HitRate = %v; want 0.5", rate)
	}
}

func TestFileReadCache_InvalidateOnMtimeChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.txt")
	os.WriteFile(path, []byte("v1"), 0o644)

	c := NewFileReadCache(10)
	v1, _ := c.ReadFile(path)
	if v1 != "v1" {
		t.Fatalf("v1 = %q", v1)
	}

	// Overwrite with a later mtime.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("v2"), 0o644)
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(path, future, future)

	v2, _ := c.ReadFile(path)
	if v2 != "v2" {
		t.Errorf("after mtime change: %q; want v2", v2)
	}
}

func TestFileReadCache_InvalidateAndClear(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "1.txt")
	p2 := filepath.Join(dir, "2.txt")
	os.WriteFile(p1, []byte("a"), 0o644)
	os.WriteFile(p2, []byte("b"), 0o644)

	c := NewFileReadCache(10)
	c.ReadFile(p1)
	c.ReadFile(p2)
	if c.Size() != 2 {
		t.Fatalf("size = %d", c.Size())
	}
	c.Invalidate(p1)
	if c.Size() != 1 {
		t.Errorf("after Invalidate, size = %d; want 1", c.Size())
	}
	c.InvalidateAll()
	if c.Size() != 0 {
		t.Errorf("after InvalidateAll, size = %d; want 0", c.Size())
	}
}

func TestFileReadCache_LRUEviction(t *testing.T) {
	dir := t.TempDir()
	c := NewFileReadCache(2)
	for i, name := range []string{"a", "b", "c"} {
		p := filepath.Join(dir, name+".txt")
		os.WriteFile(p, []byte{byte('0' + i)}, 0o644)
		c.ReadFile(p)
	}
	if c.Size() != 2 {
		t.Errorf("size = %d; want 2 (LRU eviction)", c.Size())
	}
}

// ---------- FileStateCache ----------

func TestFileStateCache_SetGetHasDelete(t *testing.T) {
	c := NewFileStateCache(10, 1024)
	st := FileState{Content: "hi", Timestamp: 1, Offset: -1, Limit: -1}
	c.Set("/foo/bar.txt", st)

	if !c.Has("/foo/bar.txt") {
		t.Error("Has should return true")
	}
	got, ok := c.Get("/foo/bar.txt")
	if !ok {
		t.Fatal("Get returned not ok")
	}
	if got.Content != "hi" {
		t.Errorf("content = %q", got.Content)
	}
	c.Delete("/foo/bar.txt")
	if c.Has("/foo/bar.txt") {
		t.Error("Has should return false after Delete")
	}
}

func TestFileStateCache_NormalizesKey(t *testing.T) {
	c := NewFileStateCache(10, 1024)
	c.Set("/foo//bar.txt", FileState{Content: "x"})
	// filepath.Clean normalizes double slash.
	if !c.Has("/foo/bar.txt") {
		t.Error("key should be normalized")
	}
}

func TestFileStateCache_ClearSizeKeys(t *testing.T) {
	c := NewFileStateCache(10, 1024)
	c.Set("/a", FileState{Content: "a"})
	c.Set("/b", FileState{Content: "bb"})
	if c.Size() != 2 {
		t.Errorf("size = %d; want 2", c.Size())
	}
	keys := c.Keys()
	if len(keys) != 2 {
		t.Errorf("keys len = %d", len(keys))
	}
	c.Clear()
	if c.Size() != 0 {
		t.Errorf("after Clear size = %d", c.Size())
	}
}

func TestFileStateCache_CloneIndependent(t *testing.T) {
	c := NewFileStateCache(10, 1024)
	c.Set("/a", FileState{Content: "a"})
	clone := c.Clone()
	c.Set("/b", FileState{Content: "b"})
	if clone.Size() != 1 {
		t.Errorf("clone should be independent; size = %d", clone.Size())
	}
}

func TestFileStateCache_MergePrefersNewer(t *testing.T) {
	a := NewFileStateCache(10, 1024)
	b := NewFileStateCache(10, 1024)
	a.Set("/f", FileState{Content: "old", Timestamp: 1})
	b.Set("/f", FileState{Content: "new", Timestamp: 2})
	a.Merge(b)
	got, _ := a.Get("/f")
	if got.Content != "new" {
		t.Errorf("merge result = %q; want new", got.Content)
	}
}

// ---------- Detect: IsBinary / generated / type ----------

func TestIsBinaryContent(t *testing.T) {
	if IsBinaryContent([]byte{}) {
		t.Error("empty content should not be binary")
	}
	if IsBinaryContent([]byte("hello world, plain text")) {
		t.Error("plain text should not be binary")
	}
	// ~5% null bytes should count as binary.
	data := make([]byte, 200)
	for i := 0; i < 10; i++ {
		data[i*20] = 0
	}
	if !IsBinaryContent(data) {
		t.Error("content with many nulls should be binary")
	}
}

func TestIsBinaryFile(t *testing.T) {
	dir := t.TempDir()
	textPath := filepath.Join(dir, "text.txt")
	os.WriteFile(textPath, []byte("this is plain ASCII text\n"), 0o644)

	binPath := filepath.Join(dir, "bin.dat")
	binData := make([]byte, 100)
	for i := 0; i < 20; i++ {
		binData[i*5] = 0
	}
	os.WriteFile(binPath, binData, 0o644)

	isBin, err := IsBinaryFile(textPath)
	if err != nil {
		t.Fatalf("text: %v", err)
	}
	if isBin {
		t.Error("text file detected as binary")
	}

	isBin, err = IsBinaryFile(binPath)
	if err != nil {
		t.Fatalf("bin: %v", err)
	}
	if !isBin {
		t.Error("binary file detected as text")
	}

	// Missing file error.
	if _, err := IsBinaryFile(filepath.Join(dir, "nope")); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestIsGeneratedFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"package-lock.json", true},
		{"yarn.lock", true},
		{"app.min.js", true},
		{"bundle.js", false},
		{"src/main.go", false},
		{"/foo/node_modules/pkg/index.js", true},
		{"app.pb.go", true},
		{"normal.go", false},
	}
	for _, tc := range cases {
		if got := IsGeneratedFile(tc.path); got != tc.want {
			t.Errorf("IsGeneratedFile(%q) = %v; want %v", tc.path, got, tc.want)
		}
	}
}

func TestFilterGeneratedFiles(t *testing.T) {
	in := []string{"src/main.go", "yarn.lock", "app.min.js", "README.md"}
	out := FilterGeneratedFiles(in)
	if len(out) != 2 {
		t.Errorf("out = %v; want 2 entries", out)
	}
}

func TestDetectFileType(t *testing.T) {
	dir := t.TempDir()
	textPath := filepath.Join(dir, "a.txt")
	os.WriteFile(textPath, []byte("hello"), 0o644)

	if got := DetectFileType(textPath); got != FileTypeText {
		t.Errorf("text file: got %v", got)
	}

	imgPath := filepath.Join(dir, "img.png")
	os.WriteFile(imgPath, []byte("fake"), 0o644)
	if got := DetectFileType(imgPath); got != FileTypeImage {
		t.Errorf("png: got %v", got)
	}

	lockPath := filepath.Join(dir, "yarn.lock")
	os.WriteFile(lockPath, []byte("x"), 0o644)
	if got := DetectFileType(lockPath); got != FileTypeGenerated {
		t.Errorf("lockfile: got %v", got)
	}
}

func TestIsLargeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.txt")
	os.WriteFile(path, []byte("tiny"), 0o644)

	large, err := IsLargeFile(path, 100)
	if err != nil {
		t.Fatalf("IsLargeFile: %v", err)
	}
	if large {
		t.Error("small file flagged as large")
	}

	large, err = IsLargeFile(path, 2)
	if err != nil {
		t.Fatalf("IsLargeFile: %v", err)
	}
	if !large {
		t.Error("file should be larger than threshold 2")
	}
}

func TestShouldSkipFile(t *testing.T) {
	dir := t.TempDir()
	txtPath := filepath.Join(dir, "x.txt")
	os.WriteFile(txtPath, []byte("hello"), 0o644)
	if skip, _ := ShouldSkipFile(txtPath, 0); skip {
		t.Error("text file should not be skipped")
	}

	lockPath := filepath.Join(dir, "yarn.lock")
	os.WriteFile(lockPath, []byte("x"), 0o644)
	if skip, reason := ShouldSkipFile(lockPath, 0); !skip || reason == "" {
		t.Errorf("lockfile should be skipped: skip=%v reason=%q", skip, reason)
	}

	bigPath := filepath.Join(dir, "big.txt")
	os.WriteFile(bigPath, []byte(strings.Repeat("a", 500)), 0o644)
	if skip, _ := ShouldSkipFile(bigPath, 10); !skip {
		t.Error("oversized file should be skipped")
	}
}

// ---------- FormatFileSize ----------

func TestFormatFileSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{500, "500 bytes"},
		{2048, "2KB"},
		{1024 * 1024 * 3, "3MB"},
		{1024 * 1024 * 1024 * 2, "2GB"},
	}
	for _, tc := range cases {
		if got := FormatFileSize(tc.in); got != tc.want {
			t.Errorf("FormatFileSize(%d) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// ---------- ReadFileInRange: read, line count, range ----------

func TestReadFileInRange_FullFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644)

	res, err := ReadFileInRange(path, 0, 0, 0, false)
	if err != nil {
		t.Fatalf("ReadFileInRange: %v", err)
	}
	if res.TotalLines < 3 {
		t.Errorf("TotalLines = %d; want >=3", res.TotalLines)
	}
	if !strings.Contains(res.Content, "line1") || !strings.Contains(res.Content, "line3") {
		t.Errorf("content = %q", res.Content)
	}
}

func TestReadFileInRange_Subset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.txt")
	os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o644)

	res, err := ReadFileInRange(path, 1, 2, 0, false)
	if err != nil {
		t.Fatalf("ReadFileInRange: %v", err)
	}
	if res.LineCount != 2 {
		t.Errorf("LineCount = %d; want 2", res.LineCount)
	}
	if !strings.Contains(res.Content, "b") || !strings.Contains(res.Content, "c") {
		t.Errorf("content = %q; expected b,c", res.Content)
	}
}

func TestReadFileInRange_MissingFile(t *testing.T) {
	_, err := ReadFileInRange(filepath.Join(t.TempDir(), "none"), 0, 0, 0, false)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadFileInRange_Directory(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadFileInRange(dir, 0, 0, 0, false)
	if err == nil {
		t.Error("expected error when reading a directory")
	}
}

func TestReadFileInRange_TooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	os.WriteFile(path, []byte(strings.Repeat("x", 500)), 0o644)
	_, err := ReadFileInRange(path, 0, 0, 100, false)
	if err == nil {
		t.Error("expected FileTooLargeError")
	}
	if _, ok := err.(*FileTooLargeError); !ok {
		t.Errorf("got err type %T", err)
	}
}

// ---------- ReadEditContext ----------

func TestReadEditContext_FoundAndMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	os.WriteFile(path, []byte("line1\nline2\ntarget\nline4\nline5\n"), 0o644)

	ctx, err := ReadEditContext(path, "target", 1)
	if err != nil {
		t.Fatalf("ReadEditContext: %v", err)
	}
	if ctx == nil {
		t.Fatal("ctx is nil")
	}
	if !strings.Contains(ctx.Content, "target") {
		t.Errorf("ctx.Content = %q; want target", ctx.Content)
	}

	// Missing needle: Truncated=false (file is small), Content empty.
	ctx2, err := ReadEditContext(path, "not_here_xyz", 1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ctx2.Content != "" {
		t.Errorf("missing needle content = %q; want empty", ctx2.Content)
	}
}

func TestReadEditContext_NonExistentFile(t *testing.T) {
	ctx, err := ReadEditContext(filepath.Join(t.TempDir(), "none.txt"), "x", 1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ctx != nil {
		t.Errorf("ctx = %+v; want nil for missing file", ctx)
	}
}

func TestReadEditContext_EmptyNeedle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("content"), 0o644)
	ctx, err := ReadEditContext(path, "", 1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ctx == nil || ctx.Content != "" {
		t.Errorf("empty needle: ctx = %+v", ctx)
	}
}

// ---------- GetFunctionContext ----------

func TestGetFunctionContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	src := "package x\n\nfunc Foo() {\n\tx := 1\n\ty := 2\n\t_ = x + y\n}\n"
	os.WriteFile(path, []byte(src), 0o644)

	rng, err := GetFunctionContext(path, 4, 0, 0)
	if err != nil {
		t.Fatalf("GetFunctionContext: %v", err)
	}
	if !strings.Contains(rng.Content, "func Foo()") {
		t.Errorf("ctx = %q; want func Foo() included", rng.Content)
	}
	if rng.Start < 1 || rng.End < rng.Start {
		t.Errorf("range = %d-%d", rng.Start, rng.End)
	}
}

func TestGetFunctionContext_OutOfRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("one\ntwo\n"), 0o644)
	if _, err := GetFunctionContext(path, 999, 0, 0); err == nil {
		t.Error("expected error for out-of-range line")
	}
}

// ---------- FileHistory: backup, record, snapshot, revert ----------

func TestFileHistory_RecordAndRevert(t *testing.T) {
	workingDir := t.TempDir()
	backupDir := filepath.Join(t.TempDir(), "backups")

	filePath := filepath.Join(workingDir, "doc.txt")
	if err := os.WriteFile(filePath, []byte("original"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	fh := NewFileHistory(backupDir, workingDir)
	if !fh.IsEnabled() {
		t.Error("history should be enabled by default")
	}

	if err := fh.RecordChange(filePath, "msg1", "edit"); err != nil {
		t.Fatalf("RecordChange: %v", err)
	}

	// Now modify the file.
	if err := os.WriteFile(filePath, []byte("modified"), 0o644); err != nil {
		t.Fatalf("modify: %v", err)
	}

	// Revert.
	changed, err := fh.Revert("msg1")
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if len(changed) != 1 {
		t.Errorf("changed files = %v; want 1", changed)
	}

	got, _ := os.ReadFile(filePath)
	if string(got) != "original" {
		t.Errorf("after revert, content = %q; want original", got)
	}
}

func TestFileHistory_SnapshotAndCanRestore(t *testing.T) {
	workingDir := t.TempDir()
	backupDir := filepath.Join(t.TempDir(), "backups")

	filePath := filepath.Join(workingDir, "a.txt")
	os.WriteFile(filePath, []byte("v1"), 0o644)

	fh := NewFileHistory(backupDir, workingDir)
	if err := fh.RecordChange(filePath, "m1", "tool"); err != nil {
		t.Fatalf("RecordChange: %v", err)
	}

	if !fh.CanRestore("m1") {
		t.Error("CanRestore(m1) = false; want true")
	}
	if fh.CanRestore("nope") {
		t.Error("CanRestore(nope) = true; want false")
	}

	if err := fh.MakeSnapshot("m2"); err != nil {
		t.Fatalf("MakeSnapshot: %v", err)
	}
	if fh.SnapshotCount() < 1 {
		t.Errorf("SnapshotCount = %d", fh.SnapshotCount())
	}
	if fh.TrackedFileCount() != 1 {
		t.Errorf("TrackedFileCount = %d; want 1", fh.TrackedFileCount())
	}

	hist := fh.GetHistory(filePath)
	if len(hist) == 0 {
		t.Error("GetHistory returned empty")
	}
}

func TestFileHistory_DisabledIsNoop(t *testing.T) {
	workingDir := t.TempDir()
	backupDir := filepath.Join(t.TempDir(), "backups")

	filePath := filepath.Join(workingDir, "a.txt")
	os.WriteFile(filePath, []byte("x"), 0o644)

	fh := NewFileHistory(backupDir, workingDir)
	fh.SetEnabled(false)
	if fh.IsEnabled() {
		t.Error("should be disabled")
	}
	if err := fh.RecordChange(filePath, "m1", "tool"); err != nil {
		t.Errorf("RecordChange disabled: %v", err)
	}
	if fh.SnapshotCount() != 0 {
		t.Errorf("SnapshotCount = %d; want 0 when disabled", fh.SnapshotCount())
	}
}

func TestFileHistory_RevertUnknownMessage(t *testing.T) {
	fh := NewFileHistory(t.TempDir(), t.TempDir())
	if _, err := fh.Revert("no-such-msg"); err == nil {
		t.Error("expected error reverting unknown message")
	}
}

func TestFileHistory_RecordNewFileThenRevertDeletes(t *testing.T) {
	workingDir := t.TempDir()
	backupDir := filepath.Join(t.TempDir(), "backups")

	filePath := filepath.Join(workingDir, "new.txt")
	// File does NOT exist yet.

	fh := NewFileHistory(backupDir, workingDir)
	if err := fh.RecordChange(filePath, "m1", "create"); err != nil {
		t.Fatalf("RecordChange: %v", err)
	}

	// Create file after record.
	os.WriteFile(filePath, []byte("created"), 0o644)

	// Revert should delete it (backup says FileExisted=false).
	if _, err := fh.Revert("m1"); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("file should be removed after revert; stat err = %v", err)
	}
}
