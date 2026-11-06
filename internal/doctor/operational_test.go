package doctor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/chatgptauth"
	"github.com/costa/polypod/internal/config"
)

type probeTransport func(*http.Request) (*http.Response, error)

func (f probeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOAuthReportsActualAccessAndSanitizesFailures(t *testing.T) {
	p := operationalProbes{credential: func() (*chatgptauth.Credential, error) { return &chatgptauth.Credential{AccessToken: "secret"}, nil },
		models: func(context.Context) ([]chatgptauth.Model, error) { return []chatgptauth.Model{{ID: "selected"}}, nil }}
	if got := checkOAuth(context.Background(), "selected", false, p); got.Status != "warn" {
		t.Fatal("offline probe claimed verified OAuth")
	}
	if got := checkOAuth(context.Background(), "selected", true, p); got.Status != "ok" {
		t.Fatal(got)
	}
	if got := checkOAuth(context.Background(), "unavailable", true, p); got.Status != "warn" {
		t.Fatal(got)
	}
	p.models = func(context.Context) ([]chatgptauth.Model, error) { return nil, errors.New("Bearer secret") }
	if got := checkOAuth(context.Background(), "selected", true, p); got.Status != "fail" || strings.Contains(got.Message, "secret") {
		t.Fatal(got)
	}
}

func TestTelegramLiveDoesNotConsumeUpdatesOrSendMessages(t *testing.T) {
	paths := []string{}
	p := operationalProbes{telegramURL: "https://telegram.invalid", http: &http.Client{Transport: probeTransport(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})}}
	c := checkTelegramLive(context.Background(), config.RemoteConfig{BotToken: "secret", ChatID: "42"}, p)
	if c.Status != "ok" || len(paths) != 2 || !strings.HasSuffix(paths[0], "/getMe") || !strings.HasSuffix(paths[1], "/getChat") {
		t.Fatal(c, paths)
	}
	p.http.Transport = probeTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("URL /botsecret/getMe") })
	if c := checkTelegramLive(context.Background(), config.RemoteConfig{BotToken: "secret"}, p); strings.Contains(c.Message, "secret") {
		t.Fatal(c)
	}
}

func TestHeartbeatFreshnessAndFailures(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Data.Dir = t.TempDir()
	now := time.Now().UTC()
	cases := []struct{ name, content, want string }{
		{"fresh", `{"state":{"last_heartbeat":"` + now.Format(time.RFC3339) + `"},"messages":[]}`, "ok"},
		{"old", `{"state":{"last_heartbeat":"2000-01-01T00:00:00Z"}}`, "warn"},
		{"failure", `{"state":{"last_heartbeat":"` + now.Format(time.RFC3339) + `","last_error":"private payload"}}`, "warn"},
		{"corrupt", `broken`, "fail"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(cfg.Data.Dir, "humanloop.json"), []byte(tt.content), 0600); err != nil {
				t.Fatal(err)
			}
			got := checkHeartbeat(cfg, now)
			if got.Status != tt.want || strings.Contains(got.Message, "private payload") {
				t.Fatal(got)
			}
		})
	}
	cfg.Scheduler.Enabled, cfg.Scheduler.HeartbeatEnabled = true, true
	cfg.Scheduler.HeartbeatInterval = "-1s"
	if c := checkScheduledHeartbeat(cfg); c.Status != "fail" {
		t.Fatal(c)
	}
	cfg.Scheduler.HeartbeatInterval = "1h"
	if c := checkScheduledHeartbeat(cfg); c.Status != "ok" {
		t.Fatal(c)
	}
}

func TestBootAndDesktopLiveProbes(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", ":99")
	captureCalls, inputCalls := 0, 0
	p := operationalProbes{capture: func(context.Context) ([]byte, error) { captureCalls++; return []byte("image"), nil },
		command: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if name == "systemctl" {
				return []byte("LoadState=loaded\nActiveState=active\nUnitFileState=enabled\n"), nil
			}
			if name == "xdotool" && len(args) == 2 && args[0] == "getmouselocation" {
				inputCalls++
				return []byte("X=0"), nil
			}
			t.Fatalf("unexpected command: %s %v", name, args)
			return nil, nil
		}}
	if c := checkBoot(context.Background(), p); c.Status != "ok" {
		t.Fatal(c)
	}
	checkDesktop(context.Background(), false, p)
	if captureCalls != 0 || inputCalls != 0 {
		t.Fatal("offline doctor touched desktop")
	}
	results := checkDesktop(context.Background(), true, p)
	if captureCalls != 1 || inputCalls != 1 || results[0].Status != "ok" || results[1].Status != "ok" {
		t.Fatal(results)
	}
}

func TestAPIKeyNeverAppearsInReport(t *testing.T) {
	for _, key := range []string{"x", "sk-secret-value"} {
		c := checkAPIKey("API", key, "")
		if strings.Contains(c.Message, key) {
			t.Fatal(c)
		}
	}
}
