package accountauth

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

// ProviderDisplayLabel returns a friendly label for a supported OAuth provider.
func ProviderDisplayLabel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "chatgpt":
		return "ChatGPT"
	case "openrouter":
		return "OpenRouter"
	case "google":
		return "Google Gemini"
	case "github":
		return "GitHub"
	case "qwen":
		return "Qwen"
	case "anthropic":
		return "Anthropic Claude"
	default:
		return provider
	}
}

// ProviderAuthHint explains how to authenticate each OAuth provider without spoofing.
func ProviderAuthHint(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "chatgpt":
		return "OAuth 2.0 PKCE dinamico oficial (auth.openai.com)"
	case "openrouter":
		return "OAuth 2.0 PKCE no navegador (openrouter.ai/auth)"
	case "google":
		return "OAuth 2.0 PKCE (--client-id / client_secret.json) ou Google Cloud ADC (--adc)"
	case "github":
		return "OAuth Device Flow RFC 8628 (--client-id) ou sessao GitHub CLI (--gh)"
	case "qwen":
		return "OAuth Device Flow / PKCE ou credencial local (~/.qwen/oauth_creds.json)"
	case "anthropic":
		return "OAuth 2.1 Discovery + DCR + PKCE (--issuer) ou token OAuth corporativo"
	default:
		return "OAuth 2.0"
	}
}

// FormatOverview builds a token-free multi-account summary across all 6 OAuth providers.
func FormatOverview(mgr *Manager) string {
	if mgr == nil {
		mgr = NewManager(nil)
	}
	store := mgr.Store()
	var b strings.Builder
	b.WriteString("Contas OAuth no Polypod (multi-provedor / multi-conta):\n\n")

	totalProvidersConnected := 0
	totalAccounts := 0

	for _, prov := range SupportedProviders() {
		accounts, _ := store.ListAccounts(prov)
		label := ProviderDisplayLabel(prov)
		if len(accounts) == 0 {
			fmt.Fprintf(&b, "  ○ %-12s (%s) — nao conectado\n", prov, label)
			fmt.Fprintf(&b, "      fluxo: %s\n", ProviderAuthHint(prov))
			continue
		}
		totalProvidersConnected++
		totalAccounts += len(accounts)
		activeLabel := ""
		for _, acc := range accounts {
			if acc.Active {
				activeLabel = acc.ID
				if acc.Email != "" && acc.Email != acc.ID {
					activeLabel += " <" + acc.Email + ">"
				}
				if !acc.ExpiresAt.IsZero() {
					activeLabel += " (expira " + acc.ExpiresAt.Local().Format(time.RFC3339) + ")"
				}
				break
			}
		}
		fmt.Fprintf(&b, "  ● %-12s (%s) — %d conta(s) · ativa: %s\n", prov, label, len(accounts), activeLabel)
		if len(accounts) > 1 {
			for _, acc := range accounts {
				mark := " "
				if acc.Active {
					mark = "*"
				}
				extra := ""
				if acc.Email != "" && acc.Email != acc.ID {
					extra = " (" + acc.Email + ")"
				}
				fmt.Fprintf(&b, "      %s %s%s\n", mark, acc.ID, extra)
			}
		}
	}

	fmt.Fprintf(&b, "\nResumo: %d/%d provedores OAuth conectados · %d conta(s) salva(s).\n",
		totalProvidersConnected, len(SupportedProviders()), totalAccounts)
	b.WriteString("Comandos:\n")
	b.WriteString("  polypod auth <provedor> login [--account <apelido>]   Conecta uma nova conta\n")
	b.WriteString("  polypod auth <provedor> accounts                      Lista todas as contas salvas\n")
	b.WriteString("  polypod auth <provedor> use <conta>                   Alterna a conta ativa\n")
	b.WriteString("  polypod auth <provedor> models                        Lista modelos disponiveis na conta\n")
	b.WriteString("  polypod auth <provedor> logout [<conta>|--all]        Remove uma ou todas as contas\n")
	return b.String()
}

// RunCLI executes `polypod auth ...` subcommands using mgr (or default Manager if nil).
func RunCLI(ctx context.Context, mgr *Manager, args []string, stdout, stderr io.Writer) error {
	if mgr == nil {
		mgr = NewManager(nil)
	}
	if len(args) == 0 {
		fmt.Fprint(stdout, FormatOverview(mgr))
		return nil
	}

	first := strings.ToLower(strings.TrimSpace(args[0]))
	switch first {
	case "status", "list", "accounts":
		fmt.Fprint(stdout, FormatOverview(mgr))
		return nil
	case "help", "-h", "--help":
		printAuthUsage(stdout)
		return nil
	}

	provider := first
	if !IsOAuthProvider(provider) {
		printAuthUsage(stderr)
		return fmt.Errorf("provedor OAuth desconhecido %q (suportados: %s)", provider, strings.Join(SupportedProviders(), ", "))
	}

	action := "status"
	rest := args[1:]
	if len(rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(rest[0]))
		rest = rest[1:]
	}

	label := ProviderDisplayLabel(provider)
	store := mgr.Store()

	switch action {
	case "login":
		fs := flag.NewFlagSet("polypod auth "+provider+" login", flag.ContinueOnError)
		fs.SetOutput(stderr)
		var opts LoginOptions
		fs.StringVar(&opts.AccountID, "account", "", "Apelido/ID para salvar multiplas contas no mesmo provedor")
		fs.StringVar(&opts.AccountID, "a", "", "Atalho para --account")
		fs.StringVar(&opts.ClientID, "client-id", "", "OAuth 2.0 Client ID")
		fs.StringVar(&opts.ClientSecret, "client-secret", "", "OAuth 2.0 Client Secret (opcional com PKCE)")
		fs.StringVar(&opts.ClientIDFile, "client-id-file", "", "Arquivo client_secret.json (Google Desktop OAuth)")
		fs.StringVar(&opts.IssuerURL, "issuer", "", "URL do servidor OAuth / OIDC (Discovery + DCR)")
		fs.StringVar(&opts.AuthURL, "auth-url", "", "Endpoint de autorizacao OAuth customizado")
		fs.StringVar(&opts.TokenURL, "token-url", "", "Endpoint de token OAuth customizado")
		fs.StringVar(&opts.DeviceURL, "device-url", "", "Endpoint de Device Code RFC 8628 customizado")
		fs.StringVar(&opts.BaseURL, "base-url", "", "URL base da API para esta conta")
		fs.BoolVar(&opts.UseADC, "adc", false, "Importar Google Application Default Credentials (gcloud auth)")
		fs.BoolVar(&opts.UseGH, "gh", false, "Importar sessao autenticada do GitHub CLI (gh auth token)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		opts.OnNotice = func(msg string) {
			fmt.Fprintln(stdout, msg)
		}
		loginCtx, cancel := context.WithTimeout(ctx, 11*time.Minute)
		defer cancel()
		cred, err := mgr.Login(loginCtx, provider, opts)
		if err != nil {
			return fmt.Errorf("conectando %s: %w", label, err)
		}
		who := firstNonEmpty(cred.Email, cred.DisplayName, cred.AccountID)
		if provider == "chatgpt" {
			fmt.Fprintf(stdout, "ChatGPT conectado ao Polypod: %s\n", who)
		} else {
			fmt.Fprintf(stdout, "%s conectado ao Polypod: %s (conta: %s)\n", label, who, cred.AccountID)
		}
		return nil

	case "status":
		cred, err := store.LoadActive(provider)
		if err != nil {
			return fmt.Errorf("lendo sessao %s: %w", label, err)
		}
		accounts, _ := store.ListAccounts(provider)
		if cred == nil || cred.AccessToken == "" {
			fmt.Fprintf(stdout, "%s: nao conectado\n", label)
			return nil
		}
		who := firstNonEmpty(cred.Email, cred.DisplayName, cred.AccountID)
		suffix := ""
		if len(accounts) > 1 {
			suffix = fmt.Sprintf(" [ativa de %d contas]", len(accounts))
		}
		if !cred.ExpiresAt.IsZero() {
			fmt.Fprintf(stdout, "%s: conectado (%s), expira em %s%s\n",
				label, who, cred.ExpiresAt.Local().Format(time.RFC3339), suffix)
		} else {
			fmt.Fprintf(stdout, "%s: conectado (%s)%s\n", label, who, suffix)
		}
		return nil

	case "accounts", "list":
		accounts, err := store.ListAccounts(provider)
		if err != nil {
			return err
		}
		if len(accounts) == 0 {
			fmt.Fprintf(stdout, "%s: nenhuma conta conectada.\n", label)
			return nil
		}
		fmt.Fprintf(stdout, "Contas conectadas em %s (%d):\n", label, len(accounts))
		for _, acc := range accounts {
			mark := " "
			tag := ""
			if acc.Active {
				mark = "*"
				tag = " [ativa]"
			}
			extra := ""
			if acc.Email != "" && acc.Email != acc.ID {
				extra += " <" + acc.Email + ">"
			}
			if !acc.ExpiresAt.IsZero() {
				extra += " (expira " + acc.ExpiresAt.Local().Format(time.RFC3339) + ")"
			}
			fmt.Fprintf(stdout, "  %s %s%s%s\n", mark, acc.ID, extra, tag)
		}
		return nil

	case "use", "switch":
		if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
			return fmt.Errorf("uso: polypod auth %s use <conta>", provider)
		}
		target := strings.TrimSpace(rest[0])
		if err := store.SetActiveAccount(provider, target); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Conta ativa de %s alterada para: %s\n", label, target)
		return nil

	case "models":
		models, err := mgr.ListModels(ctx, provider)
		if err != nil {
			return fmt.Errorf("listando modelos %s: %w", label, err)
		}
		for _, m := range models {
			fmt.Fprintln(stdout, m)
		}
		return nil

	case "logout":
		if len(rest) > 0 && (rest[0] == "--all" || rest[0] == "-a") {
			if err := store.DeleteAll(provider); err != nil {
				return fmt.Errorf("desconectando %s: %w", label, err)
			}
			fmt.Fprintf(stdout, "Todas as sessoes de %s foram removidas do Polypod.\n", label)
			return nil
		}
		target := ""
		if len(rest) > 0 {
			target = strings.TrimSpace(rest[0])
		} else {
			activeID, _ := store.ActiveAccountID(provider)
			target = activeID
		}
		if target == "" {
			if err := store.DeleteAll(provider); err != nil {
				return fmt.Errorf("desconectando %s: %w", label, err)
			}
			fmt.Fprintf(stdout, "Sessao %s removida do Polypod.\n", label)
			return nil
		}
		if err := store.DeleteAccount(provider, target); err != nil {
			return fmt.Errorf("removendo conta %s de %s: %w", target, label, err)
		}
		remaining, _ := store.ListAccounts(provider)
		if provider == "chatgpt" && len(remaining) == 0 && len(rest) == 0 {
			fmt.Fprintln(stdout, "Sessao ChatGPT removida do Polypod.")
			return nil
		}
		if len(remaining) > 0 {
			nextActive, _ := store.ActiveAccountID(provider)
			fmt.Fprintf(stdout, "Conta %s removida de %s. Nova conta ativa: %s\n", target, label, nextActive)
		} else {
			fmt.Fprintf(stdout, "Sessao %s (%s) removida do Polypod.\n", label, target)
		}
		return nil

	default:
		printProviderAuthUsage(stdout, provider)
		return fmt.Errorf("acao desconhecida %q para %s", action, provider)
	}
}

func printAuthUsage(w io.Writer) {
	fmt.Fprint(w, "Uso:\n"+
		"  polypod auth [status|list]                              Mostra todas as contas OAuth conectadas\n"+
		"  polypod auth <provedor> login [--account <apelido>]     Conecta conta OAuth (multi-conta)\n"+
		"  polypod auth <provedor> status                          Mostra a conta ativa do provedor\n"+
		"  polypod auth <provedor> accounts                        Lista todas as contas do provedor\n"+
		"  polypod auth <provedor> use <conta>                     Define qual conta fica ativa\n"+
		"  polypod auth <provedor> models                          Lista modelos via OAuth\n"+
		"  polypod auth <provedor> logout [<conta>|--all]          Desconecta uma ou todas as contas\n\n"+
		"Provedores com suporte a Auth da conta (OAuth):\n"+
		"  chatgpt, openrouter, google, github, qwen, anthropic\n")
}

func printProviderAuthUsage(w io.Writer, provider string) {
	fmt.Fprintf(w, "uso: polypod auth %s <login|status|accounts|use|models|logout>\n", provider)
}
