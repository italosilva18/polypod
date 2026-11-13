package cli

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/costa/polypod/internal/accountauth"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/session"
)

func TestProviderPickerSelectsProviderThenModel(t *testing.T) {
	picker := newModelPicker("gpt-6.1-sol", ProviderOption{Name: "chatgpt", Model: "gpt-6.1-sol", Available: true}, ProviderOption{Name: "nvidia", Model: "nvidia/test-model", Available: true})
	if !picker.providerStage {
		t.Fatal("seletor não abriu na etapa de provedor")
	}
	picker.Update(tea.KeyMsg{Type: tea.KeyDown})
	picker.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if picker.providerName != "nvidia" || picker.providerStage {
		t.Fatalf("etapa incorreta: %q", picker.providerName)
	}
	if len(picker.matches) != 1 || picker.matches[0].ID != "nvidia/test-model" {
		t.Fatalf("modelos errados: %+v", picker.matches)
	}
	_, _, picked := picker.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if picked != "nvidia\x1fnvidia/test-model" {
		t.Errorf("seleção = %q", picked)
	}
}

func TestSwitchProviderIsSessionScoped(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "synthetic-key")
	dir := t.TempDir()
	path := filepath.Join(dir, "cli.yaml")
	if err := os.WriteFile(path, []byte("ai:\n  provider: chatgpt\n  model: gpt-6.1-sol\n  api_key: \"\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AI: config.AIConfig{Provider: "chatgpt", Model: "gpt-6.1-sol"}}
	svc := ai.NewService(ai.NewClient(cfg.AI, nil), nil, nil, "default", nil, slog.Default())
	a := &Adapter{aiSvc: svc, cfg: cfg, cfgPath: path, dataDir: dir}
	if err := a.switchProvider("nvidia", "nvidia/test-model"); err != nil {
		t.Fatal(err)
	}
	if svc.CurrentProvider() != "nvidia" || svc.CurrentModel() != "nvidia/test-model" {
		t.Fatalf("backend não trocado: %s/%s", svc.CurrentProvider(), svc.CurrentModel())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "synthetic-key") {
		t.Fatal("chave de API gravada no perfil")
	}
	if !strings.Contains(string(data), "provider: chatgpt") {
		t.Fatal("a troca da sessão alterou o provedor padrão")
	}
}

func TestConnectPickerMasksAPIKey(t *testing.T) {
	picker := newModelPicker("", ProviderOption{Name: "nvidia"})
	picker.connectMode = true
	picker.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !picker.keyEntry {
		t.Fatal("entrada de chave não aberta")
	}
	picker.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("segredo-teste")})
	if strings.Contains(picker.View(), "segredo-teste") {
		t.Fatal("a chave apareceu na tela")
	}
	_, _, selected := picker.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if selected != "\x1econnect\x1fnvidia\x1fsegredo-teste" {
		t.Fatalf("chave não entregue ao conector: %q", selected)
	}
}

func TestSessionMetadataRestoresProviderAndModel(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "synthetic-key")
	cfg := &config.Config{AI: config.AIConfig{Provider: "chatgpt", Model: "gpt-6.1-sol"}}
	svc := ai.NewService(ai.NewClient(cfg.AI, nil), nil, nil, "default", nil, slog.Default())
	a := &Adapter{aiSvc: svc, cfg: cfg, dataDir: t.TempDir()}
	if err := a.switchProvider("nvidia", "nvidia/test-model"); err != nil {
		t.Fatal(err)
	}
	metadata := a.currentSessionMetadata(session.SessionMetadata{})
	if metadata.Provider != "nvidia" || metadata.Model != "nvidia/test-model" {
		t.Fatalf("seleção não gravada na sessão: %+v", metadata)
	}
	metadata.AgentName = ""
	if err := svc.SwitchProvider("openai", "gpt-6.1-sol", "synthetic-key", ""); err != nil {
		t.Fatal(err)
	}
	if warnings := a.restoreSessionMetadata(metadata); len(warnings) != 0 {
		t.Fatalf("falha na restauração: %v", warnings)
	}
	if svc.CurrentProvider() != "nvidia" || svc.CurrentModel() != "nvidia/test-model" {
		t.Fatalf("seleção da sessão não restaurada: %s/%s", svc.CurrentProvider(), svc.CurrentModel())
	}
}

func TestConnectPickerCtrlOAndMultiAccountOAuthSwitch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("POLYPOD_OAUTH_STORE_FILE", filepath.Join(dir, "oauth.enc"))
	t.Setenv("POLYPOD_CHATGPT_AUTH_FILE", filepath.Join(dir, "cg.enc"))

	// Ctrl+O in /connect triggers browser OAuth flow for any OAuth-capable provider
	picker := newModelPicker("", ProviderOption{Name: "google"})
	picker.connectMode = true
	picker.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !picker.keyEntry {
		t.Fatal("esperava abrir modal de conexao para google")
	}
	_, _, selected := picker.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if selected != "\x1eoauth\x1fgoogle" {
		t.Fatalf("Ctrl+O retornou %q, esperado \\x1eoauth\\x1fgoogle", selected)
	}

	// Save 2 OAuth accounts on openrouter and verify providerOptions + switchProviderRuntime + /auth use
	store := accountauth.NewStore("")
	if err := store.SaveAccount("openrouter", "pessoal", &accountauth.Credential{
		AccessToken: "or-tok-1",
		BaseURL:     "https://openrouter.ai/api/v1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAccount("openrouter", "empresa", &accountauth.Credential{
		AccessToken: "or-tok-2",
		BaseURL:     "https://openrouter.ai/api/v1",
	}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{AI: config.AIConfig{Provider: "chatgpt", Model: "gpt-6.1-sol"}}
	svc := ai.NewService(ai.NewClient(cfg.AI, nil), nil, nil, "default", nil, slog.Default())
	a := &Adapter{aiSvc: svc, cfg: cfg, dataDir: dir}

	opts := a.providerOptions()
	var foundOR *ProviderOption
	for i := range opts {
		if opts[i].Name == "openrouter" {
			foundOR = &opts[i]
			break
		}
	}
	if foundOR == nil || !foundOR.Available || !strings.Contains(foundOR.Detail, "2 contas") {
		t.Fatalf("openrouter OAuth multi-conta nao refletido nas opcoes: %+v", foundOR)
	}

	if err := a.switchProvider("openrouter", "anthropic/claude-sonnet-4"); err != nil {
		t.Fatalf("switchProvider via OAuth sem API key falhou: %v", err)
	}
	if svc.CurrentProvider() != "openrouter" {
		t.Fatalf("CurrentProvider = %q, esperado openrouter", svc.CurrentProvider())
	}

	res := processCommand("/auth use openrouter pessoal", commandDeps{}, nil)
	if !res.handled || !strings.Contains(res.output, "pessoal") {
		t.Fatalf("/auth use falhou: %+v", res)
	}
	activeID, _ := store.ActiveAccountID("openrouter")
	if activeID != "pessoal" {
		t.Fatalf("ActiveAccountID = %q, esperado pessoal", activeID)
	}
}
