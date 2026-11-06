package tool

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/types"
)

func fakeGrok(t *testing.T, script string) {
	t.Helper()
	t.Setenv("POLYPOD_GROK_BIN", "")
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "grok"), []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestGrokDelegationArgumentsAndPermissions(t *testing.T) {
	fakeGrok(t, "printf '%s\\n' \"$PWD\"\nwhile [ \"$#\" -gt 0 ]; do\nif [ \"$1\" = '--prompt-file' ]; then\nshift\nprintf 'prompt-file:%s\\n' \"$1\"\n/bin/cat \"$1\"\nelse\nprintf '%s\\n' \"$1\"\nfi\nshift\ndone\n")
	reg := NewRegistry()
	RegisterGrokTool(reg)
	tool, ok := reg.Get("grok_delegate")
	if !ok || tool.IsReadOnly(nil) || tool.IsConcurrencySafe(nil) || !tool.IsDestructive(nil) {
		t.Fatal("delegation must be registered as a mutating, serial tool")
	}
	dir := t.TempDir()
	prompt := "explain $(touch should-not-exist); --always-approve"
	for _, approve := range []bool{false, true} {
		args := map[string]any{"prompt": prompt, "model": "grok-4.7", "approve_tools": approve}
		got, err := tool.Execute(types.ToolContext{Context: context.Background(), WorkingDir: dir}, args)
		if err != nil || got.IsError {
			t.Fatalf("execute = %+v, %v", got, err)
		}
		want := dir + "\n--no-auto-update\n--output-format\nplain\n"
		if approve {
			want += "--always-approve\n"
		} else {
			want += "--permission-mode\ndontAsk\n"
		}
		want += "--model\ngrok-4.7\nprompt-file:"
		if !strings.HasPrefix(got.Content, want) || !strings.HasSuffix(got.Content, "\n"+prompt) {
			t.Fatalf("arguments not preserved: %q", got.Content)
		}
		path := strings.SplitN(strings.TrimPrefix(got.Content, want), "\n", 2)[0]
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("prompt file was not cleaned up", err)
		}
	}
}

func TestGrokFailureDoesNotExposeDiagnostics(t *testing.T) {
	fakeGrok(t, "printf 'private-token' >&2\nprintf 'private-token'\nexit 1\n")
	got, err := executeGrok(types.ToolContext{}, map[string]any{"prompt": "test"})
	if err != nil || !got.IsError || strings.Contains(got.Content, "private-token") {
		t.Fatalf("failure = %+v, %v", got, err)
	}
}

func TestGrokCancellation(t *testing.T) {
	fakeGrok(t, "while :; do :; done\n")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	got, err := executeGrok(types.ToolContext{Context: ctx}, map[string]any{"prompt": "test"})
	if err != nil || !got.IsError || time.Since(start) > 2*time.Second {
		t.Fatalf("cancellation = %+v, %v", got, err)
	}
}

func TestGrokOutputBound(t *testing.T) {
	var b grokOutput
	input := strings.Repeat("a", 128*1024)
	n, err := b.Write([]byte(input))
	if err != nil || n != len(input) || b.Len() != 64*1024 || !b.truncated {
		t.Fatalf("bounded output: n=%d len=%d truncated=%v err=%v", n, b.Len(), b.truncated, err)
	}
	if _, err := b.Write([]byte("more")); err != nil || b.Len() != 64*1024 {
		t.Fatal("overflow after first truncation", err)
	}
}

func TestGrokMissingCLI(t *testing.T) {
	t.Setenv("POLYPOD_GROK_BIN", "")
	t.Setenv("PATH", t.TempDir())
	got, err := executeGrok(types.ToolContext{}, map[string]any{"prompt": "test"})
	if err != nil || !got.IsError || !strings.Contains(got.Content, "não instalado") {
		t.Fatalf("missing CLI = %+v, %v", got, err)
	}
}

func TestGrokExplicitExecutable(t *testing.T) {
	fakeGrok(t, "printf 'ok'\n")
	path := filepath.Join(os.Getenv("PATH"), "grok")
	t.Setenv("POLYPOD_GROK_BIN", path)
	t.Setenv("PATH", t.TempDir())
	got, err := executeGrok(types.ToolContext{}, map[string]any{"prompt": "test"})
	if err != nil || got.IsError || got.Content != "ok" {
		t.Fatalf("explicit executable = %+v, %v", got, err)
	}
	t.Setenv("POLYPOD_GROK_BIN", "./grok")
	if _, err := grokExecutable(); err == nil {
		t.Fatal("relative executable must be rejected")
	}
}

func TestGrokDesktopUserExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable path")
	}
	home := t.TempDir()
	path := filepath.Join(home, ".local", "bin", "grok")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPOD_GROK_BIN", "")
	t.Setenv("POLYPOD_DESKTOP_HOME", home)
	t.Setenv("PATH", t.TempDir())
	got, err := grokExecutable()
	if err != nil || got != path {
		t.Fatalf("grokExecutable = %q, %v", got, err)
	}
}
