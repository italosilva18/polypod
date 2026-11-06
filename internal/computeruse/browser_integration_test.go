package computeruse

import (
	"context"
	"encoding/base64"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/browser"
	"github.com/costa/polypod/internal/types"
)

type browserTestRegistry struct{ defs map[string]types.ToolDef }

func (r *browserTestRegistry) RegisterDef(def types.ToolDef) types.Tool {
	r.defs[def.NameFn()] = def
	return types.BuildTool(def)
}
func TestBrowserRegisteredWithoutDisplay(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux display detection")
	}
	for _, key := range []string{"DISPLAY", "WAYLAND_DISPLAY", "XDG_SESSION_TYPE"} {
		t.Setenv(key, "")
	}
	reg := &browserTestRegistry{defs: map[string]types.ToolDef{}}
	if got := RegisterComputerUseTools(reg); got != 3 {
		t.Fatalf("registered %d", got)
	}
	if _, ok := reg.defs["browser_observe"]; !ok {
		t.Fatal("browser observer missing")
	}
	if _, ok := reg.defs["browser_manage"]; !ok {
		t.Fatal("browser manager missing")
	}
	manager := reg.defs["browser_manage"]
	if !manager.IsDestructiveFn(map[string]any{"action": "evaluate"}) || manager.IsReadOnlyFn(map[string]any{"action": "evaluate"}) {
		t.Fatal("browser evaluate bypasses side-effect classification")
	}
	if !manager.IsReadOnlyFn(map[string]any{"action": "tabs"}) || manager.IsDestructiveFn(map[string]any{"action": "tabs"}) {
		t.Fatal("browser tabs classified as a write")
	}
	if manager.ValidateFn(map[string]any{"action": "navigate", "url": "javascript:alert(1)"}).Valid {
		t.Fatal("unsafe navigation accepted")
	}
	if manager.ValidateFn(map[string]any{"action": "evaluate", "javascript": "1+1"}).Valid {
		t.Fatal("evaluate without exact tab accepted")
	}
	action := reg.defs["browser_act"]
	if !action.IsDestructiveFn(nil) {
		t.Fatal("browser action bypasses destructive tool classification")
	}
	if action.ValidateFn(map[string]any{"tab_id": "one", "action": "fill", "selector": "#name"}).Valid {
		t.Fatal("omitted fill text would clear field")
	}
	if !action.ValidateFn(map[string]any{"tab_id": "one", "action": "fill", "selector": "#name", "text": ""}).Valid {
		t.Fatal("intentional empty value rejected")
	}
}
func TestBrowserFallbackReturnsDesktopImage(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux display detection")
	}
	t.Setenv("DISPLAY", ":99")
	t.Setenv("PATH", t.TempDir())
	cu := NewComputerUseTool()
	cu.captureScreen = func(context.Context) ([]byte, error) { return []byte("desktop"), nil }
	result := browserDesktopFallback(types.ToolContext{Context: context.Background()}, cu, fmt.Errorf("CDP absent"))
	if result.IsError || len(result.Images) != 1 || result.Images[0].Base64 != base64.StdEncoding.EncodeToString([]byte("desktop")) {
		t.Fatal("desktop fallback lost image")
	}
	if !strings.Contains(result.Content, "coordenadas de tela") {
		t.Fatal("viewport and screen coordinates not distinguished")
	}
}
func TestBrowserObservationKeepsImageOutOfText(t *testing.T) {
	result := browserObservationResult(&browser.Observation{Text: "Saved", Screenshot: "secret-base64"})
	if strings.Contains(result.Content, "secret-base64") || len(result.Images) != 1 {
		t.Fatal("image not separated from text")
	}
}
