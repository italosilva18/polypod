package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestManagedBrowserOwnsPrivateProfileAndStops(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			w.Write([]byte(`{"Browser":"Chrome/fake"}`))
			return
		}
		if r.URL.Path == "/json/list" {
			json.NewEncoder(w).Encode([]Tab{{ID: "one", Type: "page", URL: "about:blank"}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	port := strings.TrimPrefix(server.URL[strings.LastIndex(server.URL, ":"):], ":")
	if _, err := strconv.Atoi(port); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPOD_FAKE_CDP_PORT", port)
	bin := filepath.Join(t.TempDir(), "fake-chrome")
	code := `#!/bin/sh
for arg in "$@"; do case "$arg" in --user-data-dir=*) profile=${arg#--user-data-dir=};; esac; done
printf '%s\n/devtools/browser/test\n' "$POLYPOD_FAKE_CDP_PORT" > "$profile/DevToolsActivePort"
exec sleep 30
`
	if err := os.WriteFile(bin, []byte(code), 0700); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(t.TempDir(), "private-profile")
	m := NewManagedBrowser(ManagedConfig{Binary: bin, ProfileDir: profile, Headless: true})
	if m.Status().Running {
		t.Fatal("browser started before first request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := m.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tabs, err := c.Tabs(ctx)
	if err != nil || len(tabs) != 1 || !m.Status().Running {
		t.Fatalf("tabs=%v status=%+v error=%v", tabs, m.Status(), err)
	}
	if _, err := m.Start(ctx); err != nil {
		t.Fatalf("idempotent start failed: %v", err)
	}
	info, err := os.Stat(profile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("profile mode=%v", info.Mode())
	}
	if err := m.Stop(ctx); err != nil || m.Status().Running {
		t.Fatalf("stop error=%v status=%+v", err, m.Status())
	}
}

func TestManagedBrowserRejectsSharedProfile(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(profile, 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManagedBrowser(ManagedConfig{Binary: os.Args[0], ProfileDir: profile})
	if _, err := m.Start(context.Background()); err == nil {
		t.Fatal("shared profile accepted")
	}
}

func TestManagedBrowserFallsBackToHeadless(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			w.Write([]byte(`{"Browser":"Chrome/fake"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	port := strings.TrimPrefix(server.URL[strings.LastIndex(server.URL, ":"):], ":")
	t.Setenv("POLYPOD_FAKE_CDP_PORT", port)
	bin := filepath.Join(t.TempDir(), "fake-chrome")
	code := `#!/bin/sh
profile=
headless=false
for arg in "$@"; do
 case "$arg" in --user-data-dir=*) profile=${arg#--user-data-dir=};; --headless=new) headless=true;; esac
done
if [ "$headless" = false ]; then exit 1; fi
printf '%s\n/devtools/browser/test\n' "$POLYPOD_FAKE_CDP_PORT" > "$profile/DevToolsActivePort"
exec sleep 30
`
	if err := os.WriteFile(bin, []byte(code), 0700); err != nil {
		t.Fatal(err)
	}
	m := NewManagedBrowser(ManagedConfig{Binary: bin, ProfileDir: filepath.Join(t.TempDir(), "profile"), FallbackHeadless: true})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if status := m.Status(); !status.Running || !status.Headless {
		t.Fatalf("fallback status=%+v", status)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
