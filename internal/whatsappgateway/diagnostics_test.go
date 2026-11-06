package whatsappgateway

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestWhatsAppDiagnosticsDoNotPersistIdentifiers(t *testing.T) {
	var out bytes.Buffer
	logger := &waDiagnosticLogger{logger: slog.New(slog.NewTextHandler(&out, nil))}
	logger.Errorf("Error reconnecting after sleep: %s", "123456789@s.whatsapp.net message ABC123")
	logged := out.String()
	if !strings.Contains(logged, "category=reconnect") || strings.Contains(logged, "123456789") || strings.Contains(logged, "ABC123") {
		t.Fatalf("unsafe diagnostic output: %q", logged)
	}
	if got := jidDomain(types.NewJID("private", types.HiddenUserServer)); got != "lid" {
		t.Fatalf("jid category: %q", got)
	}
}
