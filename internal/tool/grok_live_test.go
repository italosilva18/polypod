package tool

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/types"
)

// TestGrokLiveSession is opt-in because it uses the account's Grok allowance.
func TestGrokLiveSession(t *testing.T) {
	if os.Getenv("POLYPOD_GROK_LIVE_TEST") != "1" {
		t.Skip("set POLYPOD_GROK_LIVE_TEST=1 for account smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := executeGrok(types.ToolContext{Context: ctx}, map[string]any{
		"prompt": "Responda apenas OK. Nao use ferramentas.",
	})
	if err != nil || result.IsError || strings.TrimSpace(result.Content) != "OK" {
		t.Fatalf("Grok account delegation failed: is_error=%v error=%v", result.IsError, err)
	}
}
