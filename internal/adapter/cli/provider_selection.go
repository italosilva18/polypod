package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/costa/polypod/internal/accountauth"
	"github.com/costa/polypod/internal/auth"
	"github.com/costa/polypod/internal/chatgptauth"
	"github.com/costa/polypod/internal/provider"
)

func (a *Adapter) connectProvider(name, key string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || strings.TrimSpace(key) == "" {
		return fmt.Errorf("provedor e chave de API são obrigatórios")
	}
	if name == "chatgpt" || name == "ollama" {
		return fmt.Errorf("%s não usa chave de API neste fluxo", name)
	}
	return auth.NewKeyStore(a.dataDir).SetKey(name, strings.TrimSpace(key))
}

func (a *Adapter) connectOAuth(ctx context.Context, providerName string) error {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	if providerName == "" || providerName == "chatgpt" {
		_, err := chatgptauth.NewManager(chatgptauth.DefaultCredentialPath()).Login(ctx)
		return err
	}
	_, err := accountauth.NewManager(nil).Login(ctx, providerName, accountauth.LoginOptions{})
	return err
}

func (a *Adapter) connectChatGPT(ctx context.Context) error {
	return a.connectOAuth(ctx, "chatgpt")
}

func (a *Adapter) providerOptions() []ProviderOption {
	if a.cfg == nil {
		return nil
	}
	current := a.cfg.AI.Provider
	if a.aiSvc != nil && a.aiSvc.CurrentProvider() != "" {
		current = a.aiSvc.CurrentProvider()
	}
	names := append([]string{current, "chatgpt"}, provider.SupportedProviders()...)
	for _, p := range a.cfg.Providers {
		names = append(names, p.Name)
	}
	seen := map[string]bool{}
	stored := map[string]bool{}
	for _, name := range auth.NewKeyStore(a.dataDir).ListProviders() {
		stored[name] = true
	}
	acctMgr := accountauth.NewManager(nil)
	hasKey := func(name string) bool {
		if stored[name] {
			return true
		}
		if envName := auth.ProviderEnvVarMap[name]; envName != "" && os.Getenv(envName) != "" {
			return true
		}
		if a.cfg.AI.Provider == name && a.cfg.AI.APIKey != "" {
			return true
		}
		for _, configured := range a.cfg.Providers {
			if strings.EqualFold(configured.Name, name) && configured.APIKey != "" {
				return true
			}
		}
		return false
	}
	var options []ProviderOption
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		option := ProviderOption{Name: name}
		option.BaseURL = a.providerBaseURL(name)
		switch name {
		case "chatgpt":
			option.Model = "gpt-6.1-sol"
			_, err := chatgptauth.NewManager(chatgptauth.DefaultCredentialPath()).Status()
			option.Available = err == nil
			if option.Available {
				accounts, _ := acctMgr.Store().ListAccounts("chatgpt")
				activeID, _ := acctMgr.Store().ActiveAccountID("chatgpt")
				if len(accounts) > 1 {
					option.Detail = fmt.Sprintf("OAuth: %s (%d contas — /auth use chatgpt <conta>)", activeID, len(accounts))
				} else if activeID != "" {
					option.Detail = "OAuth: " + activeID
				}
			} else {
				option.Detail = "Conecte com: polypod auth chatgpt login"
			}
		case "nvidia":
			option.Model = provider.NVIDIADefaultModel
			option.Available = hasKey(name)
			if !option.Available {
				option.Detail = "Configure NVIDIA_API_KEY"
			}
		default:
			oauthConnected := accountauth.IsOAuthProvider(name) && acctMgr.HasActiveAccount(name)
			option.Available = hasKey(name) || oauthConnected || name == "ollama"
			if oauthConnected {
				if defModel := accountauth.DefaultModelFor(name); defModel != "" {
					option.Model = defModel
				}
				accounts, _ := acctMgr.Store().ListAccounts(name)
				activeID, _ := acctMgr.Store().ActiveAccountID(name)
				if len(accounts) > 1 {
					option.Detail = fmt.Sprintf("OAuth: %s (%d contas — /auth use %s <conta>)", activeID, len(accounts), name)
				} else if activeID != "" {
					option.Detail = "OAuth: " + activeID
				}
			} else if !option.Available {
				if accountauth.IsOAuthProvider(name) {
					option.Detail = fmt.Sprintf("OAuth (polypod auth %s login / Ctrl+O) ou chave de API", name)
				} else {
					option.Detail = "Configure a chave de API deste provedor"
				}
			}
		}
		if a.cfg.AI.Provider == name && a.cfg.AI.Model != "" {
			option.Model = a.cfg.AI.Model
		}
		options = append(options, option)
	}
	return options
}

func (a *Adapter) providerKey(name string) (string, error) {
	cred, err := auth.NewMultiProviderAuth(auth.NewKeyStore(a.dataDir)).GetCredentials(name)
	if err == nil && cred != nil && cred.Token() != "" {
		return cred.Token(), nil
	}
	if a.cfg != nil {
		if a.cfg.AI.Provider == name && a.cfg.AI.APIKey != "" {
			return a.cfg.AI.APIKey, nil
		}
		for _, p := range a.cfg.Providers {
			if strings.EqualFold(p.Name, name) && p.APIKey != "" {
				return p.APIKey, nil
			}
		}
	}
	return "", err
}

func (a *Adapter) providerBaseURL(name string) string {
	if a.cfg != nil {
		if a.cfg.AI.Provider == name && a.cfg.AI.BaseURL != "" {
			return a.cfg.AI.BaseURL
		}
		for _, p := range a.cfg.Providers {
			if strings.EqualFold(p.Name, name) && p.BaseURL != "" {
				return p.BaseURL
			}
		}
	}
	if accountauth.IsOAuthProvider(name) {
		if u := accountauth.NewManager(nil).ActiveBaseURL(name); u != "" {
			return u
		}
	}
	if name == "nvidia" {
		return provider.NVIDIAHostedBaseURL
	}
	if name == "chatgpt" {
		return "https://api.openai.com/v1"
	}
	return ""
}

func (a *Adapter) switchProvider(name, model string) error {
	if err := a.switchProviderRuntime(name, model); err != nil {
		return err
	}
	if a.convMgr == nil || a.sessStorage == nil || a.activeSessionID == "" {
		return nil
	}
	if err := a.withCLIProfileLease(context.Background(), func() error {
		sess, err := a.convMgr.GetSession(context.Background(), "cli", "local")
		if err != nil {
			return err
		}
		return a.saveActiveSession(sess)
	}); err != nil {
		return fmt.Errorf("modelo ativo, mas não foi possível salvar a escolha na sessão: %w", err)
	}
	return nil
}

func (a *Adapter) switchProviderRuntime(name, model string) error {
	if a.aiSvc == nil || a.cfg == nil {
		return fmt.Errorf("seleção de provedor indisponível")
	}
	var configured bool
	for _, option := range a.providerOptions() {
		if option.Name == name {
			configured = true
			break
		}
	}
	if !configured {
		return fmt.Errorf("provedor %q não configurado", name)
	}
	acctMgr := accountauth.NewManager(nil)
	var key string
	if name == "chatgpt" {
		if _, err := chatgptauth.NewManager(chatgptauth.DefaultCredentialPath()).Status(); err != nil {
			return fmt.Errorf("ChatGPT desconectado; use polypod auth chatgpt login: %w", err)
		}
	} else if name != "ollama" {
		var err error
		key, err = a.providerKey(name)
		if (err != nil || key == "") && accountauth.IsOAuthProvider(name) && acctMgr.HasActiveAccount(name) {
			key = ""
		} else if err != nil || key == "" {
			return fmt.Errorf("chave de API ou sessao OAuth de %s não encontrada", name)
		}
	}
	baseURL := a.providerBaseURL(name)
	if err := a.aiSvc.SwitchProvider(name, model, key, baseURL); err != nil {
		return err
	}
	return nil
}

func (a *Adapter) setVisionModel(providerName, model string) error {
	if a.aiSvc == nil {
		return fmt.Errorf("serviço de IA indisponível")
	}
	model = strings.TrimSpace(model)
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	if model == "" || strings.EqualFold(model, "auto") {
		return a.aiSvc.SetVisionModel("", "auto", "", "")
	}
	var key, baseURL string
	if providerName != "" && a.cfg != nil {
		key, _ = a.providerKey(providerName)
		baseURL = a.providerBaseURL(providerName)
	}
	return a.aiSvc.SetVisionModel(providerName, model, key, baseURL)
}
