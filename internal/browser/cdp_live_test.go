package browser

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var cdpLive = flag.Bool("cdp-live", false, "test CDP against an isolated headless Chrome")

func TestCDPLive(t *testing.T) {
	if !*cdpLive {
		t.Skip("use -args -cdp-live for isolated Chrome verification")
	}
	binary, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	page := filepath.Join(dir, "page.html")
	html := `<!doctype html><title>Polypod CDP test</title><input id="name" aria-label="Name"><button id="save" onclick="document.getElementById('status').textContent='Saved '+document.getElementById('name').value">Save</button><p id="status">Waiting</p><button disabled id="disabled">Disabled</button><button class="duplicate">One</button><button class="duplicate">Two</button><button style="display:none" id="hidden">Hidden</button>`
	if err := os.WriteFile(page, []byte(html), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--remote-debugging-port=0", "--remote-allow-origins=http://localhost", "--user-data-dir="+filepath.Join(dir, "profile"), "file://"+page)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Ask Chrome to terminate its children before removing the temporary profile.
		if portData, e := os.ReadFile(filepath.Join(dir, "profile", "DevToolsActivePort")); e == nil {
			cleanupCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
			cleanupClient := CDP{Endpoint: "http://127.0.0.1:" + strings.Split(string(portData), "\n")[0]}
			if session, _, e := cleanupClient.connect(cleanupCtx, ""); e == nil {
				var out any
				session.call(cleanupCtx, "Browser.close", map[string]any{}, &out)
				session.close()
			}
			done()
		}
		cmd.Wait()
		cancel()
	}()
	var port string
	for port == "" {
		data, err := os.ReadFile(filepath.Join(dir, "profile", "DevToolsActivePort"))
		if err == nil {
			port = strings.Split(string(data), "\n")[0]
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("Chrome debugging endpoint unavailable")
		case <-time.After(50 * time.Millisecond):
		}
	}
	c := CDP{Endpoint: "http://127.0.0.1:" + port}
	obs, err := c.Observe(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for len(obs.Elements) < 5 {
		select {
		case <-ctx.Done():
			t.Fatalf("DOM not loaded: title=%q url=%q", obs.Title, obs.URL)
		case <-time.After(50 * time.Millisecond):
		}
		obs, err = c.Observe(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	if obs.Screenshot == "" {
		t.Fatal("missing screenshot")
	}
	result, err := c.Act(ctx, Action{TabID: obs.TabID, Kind: "fill", Selector: "#name", Text: "Polypod"})
	if err != nil || !result.Executed {
		t.Fatalf("fill failed: %v", err)
	}
	result, err = c.Act(ctx, Action{TabID: obs.TabID, Kind: "click", Selector: "#save", ExpectedText: "Saved Polypod"})
	if err != nil || !result.Verified {
		t.Fatalf("click verification failed: %v", err)
	}
	for _, selector := range []string{"#disabled", ".duplicate", "#hidden"} {
		if _, err := c.Act(ctx, Action{TabID: obs.TabID, Kind: "click", Selector: selector}); err == nil {
			t.Fatal(fmt.Sprintf("unsafe selector accepted: %s", selector))
		}
	}
}
