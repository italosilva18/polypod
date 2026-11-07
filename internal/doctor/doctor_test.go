package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/config"
)

func TestCheckCommand_Found(t *testing.T) {
	// "go" should be available since we are running go tests.
	c := checkCommand("Go", "go", "version")
	if c.Status != "ok" {
		t.Errorf("expected status=ok for go, got %q (msg=%q)", c.Status, c.Message)
	}
	if c.Name != "Go" {
		t.Errorf("expected Name=Go, got %q", c.Name)
	}
	if !strings.Contains(strings.ToLower(c.Message), "go") {
		t.Errorf("expected message containing 'go', got %q", c.Message)
	}
}

func TestCheckCommand_NotFound(t *testing.T) {
	c := checkCommand("Fake", "this-binary-definitely-does-not-exist-xyz", "--version")
	if c.Status != "fail" {
		t.Errorf("expected status=fail, got %q", c.Status)
	}
	if !strings.Contains(c.Message, "nao encontrado") {
		t.Errorf("expected 'nao encontrado' in message, got %q", c.Message)
	}
}

func TestCheckCommand_TruncatesLongOutput(t *testing.T) {
	// Use `printf` to print a long first line. Most Linux systems have printf.
	long := strings.Repeat("A", 200)
	c := checkCommand("Long", "printf", "%s", long)
	if c.Status != "ok" {
		t.Skipf("printf not available or failed: %v", c.Message)
	}
	if len(c.Message) > 60 {
		t.Errorf("expected truncation to <=60 chars, got %d: %q", len(c.Message), c.Message)
	}
}

func TestCheckOptionalTool_Missing(t *testing.T) {
	c := checkOptionalTool("Missing", "no-such-tool-abc-xyz-123")
	if c.Status != "warn" {
		t.Errorf("expected status=warn for missing optional tool, got %q", c.Status)
	}
	if !strings.Contains(c.Message, "nao instalado") {
		t.Errorf("expected 'nao instalado' in message, got %q", c.Message)
	}
}

func TestCheckOptionalTool_Present(t *testing.T) {
	c := checkOptionalTool("Go", "go")
	if c.Status != "ok" {
		t.Errorf("expected status=ok when tool is present, got %q", c.Status)
	}
	if c.Message != "disponivel" {
		t.Errorf("expected message='disponivel', got %q", c.Message)
	}
}

func TestCheckAPIKey(t *testing.T) {
	cases := []struct {
		name     string
		key      string
		wantStat string
	}{
		{"empty", "", "fail"},
		{"placeholder_env", "${DEEPSEEK_API_KEY}", "fail"},
		{"placeholder_sk", "sk-your-key-here", "fail"},
		{"valid", "sk-abcdef1234567890", "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := checkAPIKey("AI", tc.key, "https://example.com")
			if c.Status != tc.wantStat {
				t.Errorf("key=%q: want status=%q, got %q (msg=%q)", tc.key, tc.wantStat, c.Status, c.Message)
			}
			if tc.wantStat == "ok" && !strings.Contains(c.Message, "configurada") {
				t.Errorf("expected 'configurada' in ok message, got %q", c.Message)
			}
		})
	}
}

func TestCheckFile(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "exists.db")
	if err := os.WriteFile(existing, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	ok := checkFile("DB", existing)
	if ok.Status != "ok" {
		t.Errorf("expected ok for existing file, got %q", ok.Status)
	}
	if ok.Message != existing {
		t.Errorf("expected message to be path %q, got %q", existing, ok.Message)
	}

	missing := checkFile("DB", filepath.Join(dir, "missing.db"))
	if missing.Status != "warn" {
		t.Errorf("expected warn for missing file, got %q", missing.Status)
	}
}

func TestCheckDir(t *testing.T) {
	dir := t.TempDir()

	// Existing directory -> ok
	ok := checkDir("Data", dir)
	if ok.Status != "ok" {
		t.Errorf("expected ok for existing dir, got %q (msg=%q)", ok.Status, ok.Message)
	}

	// Missing path -> warn
	missing := checkDir("Data", filepath.Join(dir, "does-not-exist"))
	if missing.Status != "warn" {
		t.Errorf("expected warn for missing dir, got %q", missing.Status)
	}

	// Path is a file, not a dir -> fail
	filePath := filepath.Join(dir, "afile")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	notDir := checkDir("Data", filePath)
	if notDir.Status != "fail" {
		t.Errorf("expected fail for file-as-dir, got %q", notDir.Status)
	}
}

func TestRunDiagnostics_SQLiteEnabled(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "pp.db")
	if err := os.WriteFile(dbPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	agentsDir := filepath.Join(tmp, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	templatesDir := filepath.Join(tmp, "templates")
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		AI: config.AIConfig{
			Provider: "deepseek",
			Model:    "deepseek-chat",
			APIKey:   "sk-realkey1234567890",
			BaseURL:  "https://api.deepseek.com",
		},
		Database: config.DatabaseConfig{
			Enabled: true,
			Driver:  "sqlite",
			Path:    dbPath,
		},
		Data: config.DataConfig{
			Dir:       tmp,
			AgentsDir: agentsDir,
		},
		Templates: config.TemplatesConfig{Dir: templatesDir},
		MCP: []config.MCPServerConf{
			{Name: "fake-stdio", Transport: "stdio", Command: "no-such-mcp-binary-xyz"},
			{Name: "fake-sse", Transport: "sse", URL: "http://localhost:9999"},
		},
	}

	checks := RunDiagnostics(cfg)
	if len(checks) == 0 {
		t.Fatal("expected some checks, got 0")
	}

	// Build a map name->check for quick lookup (allow dup names by last-wins)
	byName := make(map[string]Check)
	for _, c := range checks {
		byName[c.Name] = c
	}

	// Config check is deterministic and should always be present as ok
	cfgCheck, ok := byName["Config"]
	if !ok {
		t.Fatal("expected 'Config' check to be present")
	}
	if cfgCheck.Status != "ok" {
		t.Errorf("expected Config status=ok, got %q", cfgCheck.Status)
	}
	if !strings.Contains(cfgCheck.Message, "deepseek") || !strings.Contains(cfgCheck.Message, "deepseek-chat") {
		t.Errorf("expected provider/model in Config message, got %q", cfgCheck.Message)
	}

	// AI Provider should be ok (valid key)
	if ai, ok := byName["AI Provider"]; ok {
		if ai.Status != "ok" {
			t.Errorf("expected AI Provider ok, got %q", ai.Status)
		}
	} else {
		t.Error("expected 'AI Provider' check to be present")
	}

	// SQLite DB should be present and ok (file exists)
	if db, ok := byName["SQLite DB"]; ok {
		if db.Status != "ok" {
			t.Errorf("expected SQLite DB ok, got %q (msg=%q)", db.Status, db.Message)
		}
	} else {
		t.Error("expected 'SQLite DB' check to be present")
	}

	// Data dir / Agents dir / Templates must be ok since we created them
	for _, n := range []string{"Data dir", "Agents dir", "Templates"} {
		c, found := byName[n]
		if !found {
			t.Errorf("expected check %q to be present", n)
			continue
		}
		if c.Status != "ok" {
			t.Errorf("expected %s ok, got %q (msg=%q)", n, c.Status, c.Message)
		}
	}

	// MCP stdio server with bad command should produce a fail entry
	if m, ok := byName["MCP: fake-stdio"]; ok {
		if m.Status != "fail" {
			t.Errorf("expected MCP fake-stdio fail, got %q", m.Status)
		}
	} else {
		t.Error("expected 'MCP: fake-stdio' check to be present")
	}

	// sse MCP should NOT produce a check (only stdio is checked)
	if _, ok := byName["MCP: fake-sse"]; ok {
		t.Error("did not expect 'MCP: fake-sse' check for sse transport")
	}
}

func TestRunDiagnostics_DatabaseDisabled(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		AI: config.AIConfig{
			Provider: "openai",
			Model:    "gpt-4",
			APIKey:   "sk-validkeyhere12345",
		},
		Database: config.DatabaseConfig{Enabled: false},
		Data: config.DataConfig{
			Dir:       tmp,
			AgentsDir: tmp,
		},
		Templates: config.TemplatesConfig{Dir: tmp},
	}

	checks := RunDiagnostics(cfg)
	var found bool
	for _, c := range checks {
		if c.Name == "Banco de dados" {
			found = true
			if c.Status != "ok" {
				t.Errorf("expected ok for disabled DB, got %q", c.Status)
			}
			if !strings.Contains(c.Message, "desativado") {
				t.Errorf("expected 'desativado' in message, got %q", c.Message)
			}
		}
		if c.Name == "SQLite DB" {
			t.Error("did not expect SQLite DB check when DB disabled")
		}
	}
	if !found {
		t.Error("expected 'Banco de dados' check when DB disabled")
	}
}

func TestRunDiagnostics_PostgresDriver(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		AI: config.AIConfig{
			Provider: "openai",
			Model:    "gpt-4",
			APIKey:   "sk-realkey9876543210",
		},
		Database:  config.DatabaseConfig{Enabled: true, Driver: "postgres"},
		Data:      config.DataConfig{Dir: tmp, AgentsDir: tmp},
		Templates: config.TemplatesConfig{Dir: tmp},
	}

	checks := RunDiagnostics(cfg)
	var foundPG bool
	for _, c := range checks {
		if c.Name == "PostgreSQL" {
			foundPG = true
		}
	}
	if !foundPG {
		t.Error("expected 'PostgreSQL' check for postgres driver")
	}
}

func TestFormatReport(t *testing.T) {
	checks := []Check{
		{Name: "A", Status: "ok", Message: "fine"},
		{Name: "B", Status: "warn", Message: "heads up"},
		{Name: "C", Status: "fail", Message: "broken"},
		{Name: "D", Status: "ok", Message: "also fine"},
	}

	report := FormatReport(checks)
	if !strings.Contains(report, "Polypod Doctor") {
		t.Error("expected header 'Polypod Doctor' in report")
	}
	if !strings.Contains(report, "[OK]") {
		t.Error("expected [OK] marker in report")
	}
	if !strings.Contains(report, "[!!]") {
		t.Error("expected [!!] warn marker in report")
	}
	if !strings.Contains(report, "[XX]") {
		t.Error("expected [XX] fail marker in report")
	}
	if !strings.Contains(report, "2 ok, 1 avisos, 1 falhas") {
		t.Errorf("expected summary counts in report, got:\n%s", report)
	}
	// With failures, the "funcionando corretamente" line should NOT appear.
	if strings.Contains(report, "funcionando corretamente") {
		t.Error("did not expect success line when there are failures")
	}
}

func TestFormatReport_AllOK(t *testing.T) {
	checks := []Check{
		{Name: "A", Status: "ok", Message: "fine"},
		{Name: "B", Status: "ok", Message: "great"},
	}
	report := FormatReport(checks)
	if !strings.Contains(report, "2 ok, 0 avisos, 0 falhas") {
		t.Errorf("expected all-ok summary, got:\n%s", report)
	}
	if !strings.Contains(report, "funcionando corretamente") {
		t.Error("expected 'funcionando corretamente' line when no failures")
	}
}

func TestFormatReport_Empty(t *testing.T) {
	report := FormatReport(nil)
	if !strings.Contains(report, "Polypod Doctor") {
		t.Error("expected header even for empty checks")
	}
	if !strings.Contains(report, "0 ok, 0 avisos, 0 falhas") {
		t.Errorf("expected zero counts, got:\n%s", report)
	}
}

func TestCheckLocalTranscriptionReadOnlyAndSanitized(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "segredo-no-caminho")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "executed")
	python := filepath.Join(dir, "python")
	if err := os.WriteFile(python, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "transcribe.py")
	if err := os.WriteFile(script, []byte("raise RuntimeError('executed')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	model := filepath.Join(dir, "base.pt")
	if err := os.WriteFile(model, []byte("checkpoint"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.AudioTranscriptionConfig{
		Enabled: true, Backend: "local", PythonBinary: python,
		ScriptPath: script, ModelPath: model, FFmpegBinary: python,
	}
	check := checkLocalTranscription(cfg)
	if check.Status != "ok" || !strings.Contains(check.Message, "não executada") {
		t.Fatal(check)
	}
	if strings.Contains(check.Message, dir) {
		t.Fatalf("doctor disclosed configured path: %q", check.Message)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("doctor executed a configured binary: %v", err)
	}

	cfg.ModelPath = filepath.Join(dir, "missing-model.pt")
	check = checkLocalTranscription(cfg)
	if check.Status != "fail" || !strings.Contains(check.Message, "modelo") || strings.Contains(check.Message, dir) {
		t.Fatal(check)
	}

	cfg.Enabled = false
	if check = checkLocalTranscription(cfg); check.Status != "warn" {
		t.Fatal(check)
	}
	cfg.Enabled, cfg.Backend = true, "http"
	if check = checkLocalTranscription(cfg); check.Status != "warn" || !strings.Contains(check.Message, "HTTP") {
		t.Fatal(check)
	}
}
