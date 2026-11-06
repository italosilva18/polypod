package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

type cdpFixture struct {
	mu              sync.Mutex
	methods         []string
	expressions     []string
	refuse          bool
	failObservation bool
}

func newCDPFixture(t *testing.T, f *cdpFixture) CDP {
	t.Helper()
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]Tab{{ID: "one", Type: "page", URL: "https://example.test", WebSocketURL: "ws" + strings.TrimPrefix(server.URL, "http") + "/page"}})
	})
	mux.Handle("/page", websocket.Handler(func(conn *websocket.Conn) {
		defer conn.Close()
		for {
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
				Params struct {
					Expression string `json:"expression"`
				} `json:"params"`
			}
			if err := websocket.JSON.Receive(conn, &req); err != nil {
				return
			}
			f.mu.Lock()
			f.methods = append(f.methods, req.Method)
			f.expressions = append(f.expressions, req.Params.Expression)
			f.mu.Unlock()
			var result any
			if req.Method == "Page.captureScreenshot" {
				result = map[string]any{"data": "cG5n"}
			} else {
				value := any(map[string]any{"url": "https://example.test/done", "title": "Done", "text": "Saved", "elements": []any{}})
				if strings.HasPrefix(req.Params.Expression, "("+actDOM+")") {
					value = map[string]any{"ok": !f.refuse, "error": "seletor ambíguo"}
				} else if f.failObservation {
					websocket.JSON.Send(conn, map[string]any{"id": req.ID, "error": map[string]any{"message": "navigation in progress"}})
					continue
				}
				result = map[string]any{"result": map[string]any{"value": value}}
			}
			// An unrelated event must not be mistaken for the command result.
			websocket.JSON.Send(conn, map[string]any{"method": "Page.loadEventFired"})
			if err := websocket.JSON.Send(conn, map[string]any{"id": req.ID, "result": result}); err != nil {
				return
			}
		}
	}))
	return CDP{Endpoint: server.URL}
}
func TestCDPObserveCarriesDOMAndImage(t *testing.T) {
	c := newCDPFixture(t, &cdpFixture{})
	obs, err := c.Observe(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	if obs.Screenshot != "cG5n" || obs.Text != "Saved" || obs.TabID != "one" {
		t.Fatalf("observation lost: %+v", obs)
	}
	data, _ := json.Marshal(obs)
	if strings.Contains(string(data), "cG5n") {
		t.Fatal("base64 leaked into textual observation")
	}
}
func TestCDPActionVerifiesAndDoesNotReplay(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		refuse, fail                        bool
		wantErr, wantExecuted, wantVerified bool
		calls                               int
	}{
		{"verified", false, false, false, true, true, 3}, {"rejected", true, false, true, false, false, 1}, {"observation failure", false, true, false, true, false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &cdpFixture{refuse: tc.refuse, failObservation: tc.fail}
			c := newCDPFixture(t, f)
			result, err := c.Act(context.Background(), Action{TabID: "one", Kind: "fill", Selector: "#name", Text: "\";alert(1);//", ExpectedText: "Saved", ExpectedURL: "https://example.test/done"})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v", err)
			}
			if result != nil && (result.Executed != tc.wantExecuted || result.Verified != tc.wantVerified) {
				t.Fatalf("result=%+v", result)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.methods) != tc.calls {
				t.Fatalf("calls=%v", f.methods)
			}
			// Arguments are JSON data, not executable string interpolation.
			if !strings.Contains(f.expressions[0], `"text":"\";alert(1);//"`) {
				t.Fatal("unsafe argument encoding")
			}
		})
	}
}
func TestCDPEndpointValidation(t *testing.T) {
	for _, raw := range []string{"http://example.com:9222", "http://user@localhost:9222", "file:///tmp", "ws://127.0.0.1:9222"} {
		if _, err := (CDP{Endpoint: raw}).Tabs(context.Background()); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestCDPRejectsAmbiguousTab(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]Tab{{ID: "one", Type: "page"}, {ID: "two", Type: "page"}})
	}))
	defer server.Close()
	if _, err := (CDP{Endpoint: server.URL}).Observe(context.Background(), ""); err == nil {
		t.Fatal("selected arbitrary tab")
	}
}
func TestCDPCancellation(t *testing.T) {
	c := newCDPFixture(t, &cdpFixture{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := c.Observe(ctx, "one"); err == nil {
		t.Fatal("cancellation ignored")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation blocked")
	}
}
