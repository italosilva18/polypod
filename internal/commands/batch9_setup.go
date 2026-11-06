// Package commands — batch9_setup.go
//
// Slash commands for guided onboarding and integration installation:
//
//   - /init                — prints the guided onboarding checklist (Portuguese)
//   - /onboarding          — alias for /init
//   - /install-github-app  — prints URL + setup instructions for the GitHub App
//   - /install-slack-app   — prints URL + setup instructions for the Slack App
//   - /install             — meta-command listing available integrations
//
// All commands produce static Portuguese output. No external processes, no
// network calls, no filesystem access. They are registered via
// RegisterBatch9Setup, following the same *command.Command pattern used by
// batch1/batch11.
//
// NOTE: an older subsystem in batch2.go also declares "install-github-app"
// and "install-slack-app", but batch2 uses a different type (SlashCommand)
// and is not wired into command.Registry, so there is no registration
// collision at runtime.
package commands

import (
	"strings"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch9Setup registers the guided onboarding and install slash
// commands into the provided registry.
func RegisterBatch9Setup(reg *command.Registry) {
	reg.Register(b9CmdInit())
	reg.Register(b9CmdInstallGitHubApp())
	reg.Register(b9CmdInstallSlackApp())
	reg.Register(b9CmdInstall())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// b9OK builds a successful command result.
func b9OK(msg string) *command.CommandResult {
	return &command.CommandResult{Display: msg}
}

// ---------------------------------------------------------------------------
// /init (+ alias /onboarding)
// ---------------------------------------------------------------------------

// b9InitChecklist is the Portuguese onboarding checklist rendered by /init
// and its alias /onboarding. Kept as a package-level constant so tests can
// assert specific fragments without duplicating the full text.
const b9InitChecklist = `Bem-vindo ao Polypod! Guia de primeiros passos

Siga este checklist para configurar seu ambiente:

  1. Arquivo de configuracao
     - Copie 'config.example.yaml' para 'config.yaml'
     - Ou rode 'polypod init' para criar um arquivo inicial
     - Ajuste providers, canais e agentes conforme sua necessidade

  2. Chave de API
     - Defina 'ANTHROPIC_API_KEY' (ou a chave do provedor escolhido)
     - Use variaveis de ambiente ou um arquivo '.env' na raiz do projeto
     - Nunca commite chaves no repositorio

  3. Primeiro servidor MCP
     - Configure pelo menos um servidor MCP em 'config.yaml' (secao 'mcp_servers')
     - Exemplo: filesystem, git, github
     - Valide a conexao com '/mcp status' apos iniciar

  4. Primeira execucao
     - Rode 'polypod config.yaml' para iniciar o gateway
     - Teste com 'polypod -p "ola" config.yaml' em modo headless
     - Use '/help' dentro da sessao para listar comandos disponiveis

Dicas adicionais:
  - Documentacao: consulte 'README.md' e 'LEIAME.md'
  - Integracoes: rode '/install' para ver apps disponiveis
  - Problemas: rode 'polypod doctor' para diagnostico

Bom proveito!`

func b9CmdInit() *command.Command {
	return &command.Command{
		Name:        "init",
		Aliases:     []string{"onboarding"},
		Description: "Mostra o checklist guiado de primeiros passos",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return b9OK(b9InitChecklist), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /install-github-app
// ---------------------------------------------------------------------------

// b9GitHubAppURL is the canonical GitHub App install URL. Exposed as a
// constant so tests can assert its presence without duplicating the string.
const b9GitHubAppURL = "https://github.com/apps/polypod"

const b9GitHubAppInstructions = `Instalacao do GitHub App (Polypod)

Acesse a URL abaixo para instalar o app no seu usuario ou organizacao:

  ` + b9GitHubAppURL + `

Passos apos instalar:

  1. Selecione a conta (usuario ou organizacao) onde instalar
  2. Escolha entre todos os repositorios ou apenas os selecionados
  3. Confirme as permissoes solicitadas (read/write em code, issues e PRs)
  4. Copie o 'installation_id' exibido na pagina de confirmacao
  5. Configure o segredo 'POLYPOD_GITHUB_TOKEN' no seu ambiente ou secrets
  6. Ajuste 'config.yaml' com a secao 'integrations.github'
  7. Reinicie o Polypod e valide com '/mcp connect github'

Documentacao: https://docs.github.com/en/apps`

func b9CmdInstallGitHubApp() *command.Command {
	return &command.Command{
		Name:        "install-github-app",
		Description: "Mostra URL e instrucoes para instalar o GitHub App",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return b9OK(b9GitHubAppInstructions), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /install-slack-app
// ---------------------------------------------------------------------------

// b9SlackAppURL is the canonical Slack App install URL.
const b9SlackAppURL = "https://slack.com/apps/polypod"

const b9SlackAppInstructions = `Instalacao do Slack App (Polypod)

Acesse a URL abaixo para adicionar o app ao seu workspace Slack:

  ` + b9SlackAppURL + `

Passos apos instalar:

  1. Clique em 'Add to Slack' e selecione o workspace desejado
  2. Autorize os escopos solicitados (chat:write, channels:read, im:history)
  3. Copie o 'Bot User OAuth Token' (comeca com 'xoxb-')
  4. Defina as variaveis 'POLYPOD_SLACK_BOT_TOKEN' e 'POLYPOD_SLACK_APP_TOKEN'
  5. Ajuste 'config.yaml' adicionando um canal 'slack' em 'channels'
  6. Convide o bot para os canais desejados com '/invite @polypod'
  7. Reinicie o Polypod e envie uma mensagem de teste

Documentacao: https://api.slack.com/start`

func b9CmdInstallSlackApp() *command.Command {
	return &command.Command{
		Name:        "install-slack-app",
		Description: "Mostra URL e instrucoes para instalar o Slack App",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return b9OK(b9SlackAppInstructions), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /install (meta)
// ---------------------------------------------------------------------------

// b9BuildInstallMenu renders the meta-install menu. Kept as a function so
// future integrations can be appended without editing multiple strings.
func b9BuildInstallMenu() string {
	var b strings.Builder
	b.WriteString("Integracoes disponiveis\n\n")
	b.WriteString("Escolha uma integracao e rode o comando correspondente:\n\n")
	b.WriteString("  - GitHub App\n")
	b.WriteString("      Comando: /install-github-app\n")
	b.WriteString("      Descricao: revisao automatica de PRs e CI assistido\n\n")
	b.WriteString("  - Slack App\n")
	b.WriteString("      Comando: /install-slack-app\n")
	b.WriteString("      Descricao: bot do Polypod no seu workspace Slack\n\n")
	b.WriteString("Dicas:\n")
	b.WriteString("  - Rode '/init' (ou '/onboarding') para ver o checklist completo\n")
	b.WriteString("  - Rode '/help' para listar todos os comandos disponiveis\n")
	return b.String()
}

func b9CmdInstall() *command.Command {
	return &command.Command{
		Name:        "install",
		Description: "Lista integracoes disponiveis para instalacao",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return b9OK(b9BuildInstallMenu()), nil
		},
	}
}
