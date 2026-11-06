package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCDPTabCommandsRequireObservedID(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		switch r.URL.Path {
		case "/json/list":
			json.NewEncoder(w).Encode([]Tab{{ID: "one", Type: "page"}})
		case "/json/new":
			if r.Method != http.MethodPut || r.URL.RawQuery != "about:blank" {
				t.Errorf("unexpected new tab request: %s %s", r.Method, r.URL.RequestURI())
			}
			json.NewEncoder(w).Encode(Tab{ID: "two", Type: "page", URL: "about:blank"})
		case "/json/activate/one", "/json/close/one":
			w.Write([]byte("Target activated"))
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.RequestURI())
		}
	}))
	defer server.Close()
	c := CDP{Endpoint: server.URL}
	if _, err := c.NewTab(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.ActivateTab(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseTab(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	before := len(calls)
	if err := c.CloseTab(context.Background(), "unknown"); err == nil {
		t.Fatal("unknown tab was closed")
	}
	if len(calls) != before+1 || !strings.HasSuffix(calls[len(calls)-1], "/json/list") {
		t.Fatalf("unexpected call after unknown tab: %v", calls[before:])
	}
}

func TestCDPNavigateAndEvaluate(t *testing.T) {
	f := &cdpFixture{}
	c := newCDPFixture(t, f)
	result, err := c.Navigate(context.Background(), "one", "https://example.test/done")
	if err != nil || !result.Executed || !result.Verified || result.Observation == nil {
		t.Fatalf("navigation result=%+v error=%v", result, err)
	}
	value, err := c.Evaluate(context.Background(), "one", "document.title")
	if err != nil || !json.Valid(value) {
		t.Fatalf("evaluation value=%s error=%v", value, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.methods) < 4 || f.methods[0] != "Page.navigate" || f.methods[len(f.methods)-1] != "Runtime.evaluate" {
		t.Fatalf("CDP methods=%v", f.methods)
	}
}

func TestCDPRejectsUnsafeNavigationAndOversizedScript(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "data:text/html,x", "https://", "file:relative", ""} {
		if err := ValidateNavigationURL(raw); err == nil {
			t.Errorf("accepted navigation URL %q", raw)
		}
	}
	c := newCDPFixture(t, &cdpFixture{})
	if _, err := c.Evaluate(context.Background(), "one", strings.Repeat("x", 17<<10)); err == nil {
		t.Fatal("oversized javascript accepted")
	}
}
