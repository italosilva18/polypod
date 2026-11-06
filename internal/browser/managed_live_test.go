package browser

import (
	"context"
	"flag"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

var managedLive = flag.Bool("managed-live", false, "test the managed browser with an isolated Chrome profile")

func TestManagedBrowserLive(t *testing.T) {
	if !*managedLive {
		t.Skip("use -args -managed-live for an isolated Chrome smoke test")
	}
	chrome, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	managed := NewManagedBrowser(ManagedConfig{
		Binary: chrome, ProfileDir: filepath.Join(t.TempDir(), "profile"), Headless: true,
	})
	defer managed.Stop(context.Background())
	client, err := managed.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !managed.Status().Running {
		t.Fatal("Chrome não iniciou")
	}
	tab, err := client.NewTab(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Navigate(ctx, tab.ID, "about:blank"); err != nil {
		t.Fatal(err)
	}
	if err := managed.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if managed.Status().Running {
		t.Fatal("Chrome continuou ativo após Stop")
	}
}
