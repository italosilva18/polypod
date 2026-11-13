package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"

	"github.com/costa/polypod/internal/accountauth"
	"github.com/costa/polypod/internal/codesearch"
	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/commitai"
	"github.com/costa/polypod/internal/config"
	pgit "github.com/costa/polypod/internal/git"
	"github.com/costa/polypod/internal/initcmd"
	"github.com/costa/polypod/internal/projectchecks"
	"github.com/costa/polypod/internal/tracking"
	"github.com/costa/polypod/internal/turncheckpoint"
)

// commandResult holds the output of a local slash command.
type commandResult struct {
	output       string
	modelInput   string
	handled      bool
	quit         bool
	clearDisplay bool // limpa m.messages quando true
	voiceSeconds int  // captura por duração fixa; 0 quando a parada é manual
	voiceStart   bool // inicia captura assíncrona de voz no compositor
	voiceStop    bool // encerra a gravação atual e inicia a transcrição
	// replaceMessages, when non-nil, replaces m.messages entirely.
	// Used by /resume to load messages from a persisted session and by
	// /rewind to drop the last N turns from the display.
	replaceMessages []chatEntry
	resumeSessionID string
	historyBefore   int
	recoveryDraft   string
	recoveryTurnID  string
	// openConfigForm asks the host model to display the interactive
	// /config form overlay. Used by the bare "/config" invocation.
	openConfigForm bool
	// openModelPicker, when true, signals the Bubbletea model loop to
	// open the interactive model picker overlay. Used by /model so the
	// command both reports a status and brings up the picker.
	openModelPicker   bool
	openConnectPicker bool
	openEditor        bool
	openDiff          bool
	sideQuestion      string
	semanticQuery     string
	verifyRequest     bool
}

type sessionLoad struct {
	entries        []chatEntry
	start          int
	total          int
	recoveryDraft  string
	recoveryTurnID string
}

// processCommand checks if input is a slash command and executes it locally.
func processCommand(input string, deps commandDeps, messages []chatEntry) commandResult {
	input = strings.TrimSpace(input)

	// ! prefix as alias for /run
	if strings.HasPrefix(input, "!") {
		cmd := strings.TrimSpace(strings.TrimPrefix(input, "!"))
		if cmd == "" {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("!<comando>"), handled: true}
		}
		return handleRun(cmd, deps)
	}

	if !strings.HasPrefix(input, "/") {
		return commandResult{handled: false}
	}

	parts := strings.Fields(input)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/help":
		return commandResult{output: helpText(), handled: true}

	case "/quit", "/exit", "/sair":
		return commandResult{quit: true, handled: true}

	case "/clear":
		return handleClear(deps)

	case "/agents":
		return handleAgents(deps)

	case "/agent":
		if len(parts) >= 3 && strings.ToLower(parts[1]) == "switch" {
			return handleAgentSwitch(deps, parts[2])
		}
		return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/agent switch <nome>"), handled: true}

	case "/skills":
		return handleSkills(deps)

	case "/memory":
		return handleMemory(parts, deps)

	case "/model", "/provider":
		if len(parts) >= 2 && strings.EqualFold(parts[1], "--vision") {
			return handleVisionModelCmd(parts[2:], deps)
		}
		if len(parts) >= 3 && deps.switchProvider != nil {
			name, model := strings.ToLower(parts[1]), strings.Join(parts[2:], " ")
			if err := deps.switchProvider(name, model); err != nil {
				return commandResult{output: "Erro ao trocar provedor: " + err.Error(), handled: true}
			}
			return commandResult{output: fmt.Sprintf("Provedor: %s · modelo: %s", name, model), handled: true}
		}
		if len(parts) == 2 {
			return commandResult{output: "Uso: /provider <provedor> <modelo>  (ou /model --vision [auto | <modelo> | <provedor> <modelo>])", handled: true}
		}
		return handleModel(deps)

	case "/waterfall":
		if deps.latestWaterfall == nil {
			return commandResult{output: "Cascata de execução (/waterfall) indisponível.", handled: true}
		}
		return commandResult{output: deps.latestWaterfall(), handled: true}

	case "/questions":
		if deps.listChildQuestions == nil {
			return commandResult{output: "Fila concorrente de subagentes (/questions) indisponível.", handled: true}
		}
		return commandResult{output: deps.listChildQuestions(), handled: true}

	case "/answer":
		return handleAnswerChildQuestion(input, parts, deps)

	case "/connect":
		return commandResult{handled: true, openConnectPicker: true}

	case "/auth":
		return handleAuthCommand(parts)

	case "/editor":
		return commandResult{handled: true, openEditor: true}

	case "/btw":
		question := strings.TrimSpace(strings.TrimPrefix(input, parts[0]))
		if question == "" {
			return commandResult{output: "Uso: /btw <pergunta>", handled: true}
		}
		return commandResult{handled: true, sideQuestion: question}
	case "/index":
		return handleCodeIndex(parts, deps)
	case "/semantic":
		if len(parts) < 2 {
			return commandResult{output: "Uso: /semantic <descrição do código>", handled: true}
		}
		if deps.semanticSearch == nil {
			return commandResult{output: "Busca semântica indisponível. Configure embeddings no /config e use /index on.", handled: true}
		}
		return commandResult{semanticQuery: strings.TrimSpace(strings.TrimPrefix(input, parts[0])), handled: true}
	case "/verify":
		return commandResult{verifyRequest: true, handled: true}
	case "/queue":
		return commandResult{output: "Use /queue <mensagem> enquanto uma resposta está sendo gerada.", handled: true}

	case "/session":
		return handleSession(deps)

	case "/copy":
		return handleCopy(messages, parts[1:])

	case "/voice":
		if len(parts) == 2 && strings.EqualFold(parts[1], "stop") {
			return commandResult{voiceStop: true, handled: true}
		}
		if len(parts) > 2 {
			return commandResult{output: "uso: /voice [segundos: 1-120 | stop]", handled: true}
		}
		seconds := 0
		if len(parts) == 2 {
			var err error
			seconds, err = strconv.Atoi(parts[1])
			if err != nil || seconds < 1 || seconds > 120 {
				return commandResult{output: "uso: /voice [segundos: 1-120 | stop]", handled: true}
			}
		}
		if deps.captureVoice == nil {
			return commandResult{output: "Entrada por voz indisponível: configure whatsapp.audio_transcription para usar /voice.", handled: true}
		}
		if seconds == 0 {
			return commandResult{output: "Gravando. Use Ctrl+G ou /voice stop para parar e transcrever.", voiceStart: true, handled: true}
		}
		return commandResult{output: fmt.Sprintf("Gravando por %d segundo(s)... Ctrl+G pode parar antes.", seconds), voiceSeconds: seconds, voiceStart: true, handled: true}

	case "/run":
		if len(parts) < 2 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/run <comando>"), handled: true}
		}
		shellCmd := strings.TrimSpace(strings.TrimPrefix(input, parts[0]))
		return handleRun(shellCmd, deps)

	case "/file":
		if len(parts) < 2 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/file <caminho>"), handled: true}
		}
		return handleFile(parts[1], deps)

	case "/search":
		if len(parts) < 2 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/search <pattern>"), handled: true}
		}
		pattern := strings.Join(parts[1:], " ")
		return handleSearch(pattern, deps)

	case "/git":
		sub := ""
		if len(parts) >= 2 {
			sub = strings.ToLower(parts[1])
		}
		return handleGit(sub, deps)

	case "/project":
		return handleProject(deps)

	case "/export":
		path := "chat_export.md"
		if len(parts) >= 2 {
			path = parts[1]
		}
		return handleExport(messages, path)

	case "/context":
		return handleContext(deps, messages)

	case "/cost":
		return handleCost(deps)

	case "/sessions":
		return handleSessions(parts, deps)

	case "/suggest":
		return handleSuggestions(deps)

	case "/init":
		return handleInit(parts, deps)

	case "/commit":
		// Preserve the rest of the original input (with original case) as the message
		var userMsg string
		if len(parts) >= 2 {
			userMsg = strings.TrimSpace(strings.TrimPrefix(input, parts[0]))
		}
		return handleCommit(deps, userMsg)

	case "/diff":
		if len(parts) == 1 {
			return commandResult{handled: true, openDiff: true}
		}
		return handleDiff(parts, deps)

	case "/review":
		return handleReview(parts, deps)

	case "/security-review":
		return handleSecurityReview(parts, deps)

	case "/compact":
		return handleCompact(deps, messages)

	case "/resume":
		arg := ""
		if len(parts) >= 2 {
			arg = parts[1]
		}
		return handleResume(deps, arg)

	case "/rewind":
		return handleRewind(deps, messages, strings.Join(parts[1:], " "))

	case "/permissions":
		return handlePermissions(parts, deps)

	case "/plan":
		sub := ""
		if len(parts) >= 2 {
			sub = strings.ToLower(parts[1])
		}
		return handlePlan(deps, sub)

	case "/doctor":
		return handleDoctor(deps)

	case "/mcp":
		return handleMCP(parts, deps)

	case "/hooks":
		return handleHooks(parts, deps)

	case "/files":
		return handleFiles(deps)

	case "/add-dir":
		return handleAddDir(parts, deps)

	case "/config":
		return handleConfig(parts, input, deps)

	case "/effort":
		return handleEffort(parts, deps)

	case "/architect":
		return handleArchitectCmd(input, parts)

	case "/bestof":
		return handleBestOfCmd(input, parts)

	case "/branch":
		return handleBranch(parts, deps, messages)

	case "/usage":
		return handleUsage(parts, deps)

	case "/init-verifiers":
		return handleInitVerifiers(parts)

	default:
		if strings.HasSuffix(cmd, ":skill") && deps.listSkills != nil {
			name := strings.TrimSuffix(strings.TrimPrefix(cmd, "/"), ":skill")
			for _, available := range deps.listSkills() {
				if name == strings.ToLower(available) {
					request := strings.TrimSpace(strings.TrimPrefix(input, parts[0]))
					return commandResult{modelInput: "Use a skill " + available + " para " + request, handled: false}
				}
			}
		}
		if deps.commandRegistry != nil {
			name := strings.TrimPrefix(cmd, "/")
			if registered, ok := deps.commandRegistry.Get(name); ok {
				ctx := command.CommandContext{WorkingDir: commandWorkingDir(deps)}
				args := parts[1:]
				switch registered.Type {
				case command.CommandPrompt:
					if registered.GetPrompt == nil {
						return commandResult{output: errorStyle.Render("comando sem gerador de prompt: /" + name), handled: true}
					}
					prompt, promptErr := registered.GetPrompt(args, ctx)
					if promptErr != nil {
						return commandResult{output: errorStyle.Render(promptErr.Error()), handled: true}
					}
					return commandResult{modelInput: prompt, handled: false}
				case command.CommandLocal:
					if registered.Execute == nil {
						return commandResult{output: errorStyle.Render("comando sem executor: /" + name), handled: true}
					}
					result, execErr := registered.Execute(args, ctx)
					if execErr != nil {
						return commandResult{output: errorStyle.Render(execErr.Error()), handled: true}
					}
					if result == nil {
						return commandResult{handled: true}
					}
					return commandResult{output: result.Display, handled: true}
				}
			}
		}
		// Unknown slash command falls through to the model as written.
		return commandResult{handled: false}
	}
}

func helpText() string {
	var b strings.Builder

	b.WriteString(cmdTitleStyle.Render("Comandos"))
	b.WriteString("\n\n")

	commands := []struct {
		cmd  string
		desc string
	}{
		{"/help", "lista comandos"},
		{"/clear", "limpa a tela e o histórico da sessão"},
		{"/agents", "lista os agentes disponíveis"},
		{"/agent switch <nome>", "troca de agente ativo"},
		{"/skills", "lista skills do agente atual"},
		{"/memory list", "lista memórias salvas"},
		{"/memory search <q>", "busca memórias"},
		{"/model", "escolhe provedor e modelo (ou /model --vision [auto|<modelo>])"},
		{"/provider", "escolhe provedor e modelo"},
		{"/waterfall", "mostra a cascata visual de execução (tempos de modelo e tools)"},
		{"/questions", "lista perguntas simultâneas pendentes de subagentes"},
		{"/answer <id> <resp>", "responde a uma pergunta de subagente sem perder rascunhos"},
		{"/connect", "conecta uma conta ou chave de API"},
		{"/auth [status|accounts|use|logout]", "gerencia multiplas contas OAuth por provedor"},
		{"/editor", "edita o rascunho no editor externo"},
		{"/btw <pergunta>", "pergunta rápida sem adicionar à conversa"},
		{"/session", "mostra a sessão atual"},
		{"/copy [numero|list|all]", "copia uma resposta, lista respostas ou copia as respostas carregadas"},
		{"/voice [segundos|stop]", "grava até Ctrl+G ou /voice stop; segundos são opcionais"},
		{"Ctrl+G", "inicia ou para a gravação de voz"},
		{"/bg <objetivo>", "cria tarefa durável separada; list/status/retry/cancel para acompanhar"},
		{"/bg detach", "destaca o turno ativo; mensagens novas viram tarefas separadas"},
		{"/run <cmd>  ou  !<cmd>", "executa comando shell inline"},
		{"/file <path>", "mostra o conteúdo de um arquivo"},
		{"/search <pattern>", "busca texto no projeto (grep)"},
		{"/git [status|log|diff|branch]", "operações comuns do Git"},
		{"/project", "mostra a árvore do projeto"},
		{"/export [file]", "exporta a conversa para Markdown"},
		{"/context", "mostra o contexto atual"},
		{"/cost", "mostra o custo detalhado da sessão"},
		{"/sessions [search <q>|live]", "busca sessões salvas ou mostra instâncias ativas"},
		{"/suggest", "sugere próximas ações a partir da conversa"},
		{"/resume [id]", "lista ou carrega uma sessão persistida"},
		{"/recover", "retoma um ponto seguro; /recover discard abandona turno incerto"},
		{"/rewind [n] [--conversation|--files]", "restaura turnos, arquivos e conversa"},
		{"/permissions [sub]", "gerencia regras de permissões"},
		{"/plan [on|off|show|execute]", "gerencia o modo de planejamento"},
		{"/init [--force]", "cria .polypod.md e a estrutura do projeto"},
		{"/commit [msg]", "cria commit com mensagem gerada por IA"},
		{"/diff [--staged|<arquivo>]", "abre o diff navegável; com argumentos mostra o diff em texto"},
		{"/review [target]", "revisa código com IA (--staged, intervalo, arquivo, --pr)"},
		{"/security-review [target]", "revisa a segurança (--staged, --full, arquivo)"},
		{"/compact", "compacta o histórico da conversa com IA"},
		{"/retry", "reenvia a última mensagem que falhou"},
		{"/doctor", "mostra o diagnóstico do sistema"},
		{"/mcp [sub]", "gerencia servidores MCP (list|status|tools|reconnect)"},
		{"/hooks [sub]", "gerencia hooks (list|enable|disable|show)"},
		{"/files", "arquivos no contexto atual"},
		{"/add-dir <path>", "adiciona um diretório de trabalho"},
		{"/config [get|set|save|reload]", "vê ou edita a configuração; sem argumentos abre o formulário"},
		{"/effort [low|med|high|max]", "ajusta o nível de raciocínio da IA"},
		{"/architect <tarefa>", "planeja com Arquiteto (alto raciocínio) e aplica com Editor"},
		{"/bestof <tarefa>", "executa Best-of-N paralelo em worktrees isoladas verificadas"},
		{"/branch [sub] [name]", "gerencia ramificações da conversa (new|switch|delete|tree)"},
		{"/usage [model]", "mostra limites e consumo por provedor"},
		{"/init-verifiers", "detecta e configura comandos de teste/lint do projeto"},
		{"/verify", "executa os verificadores configurados agora"},
		{"/index [on|off|status]", "ativa busca semântica de código neste projeto"},
		{"/semantic <descrição>", "busca código por significado e mostra linhas"},
		{"/queue <mensagem>", "enfileira mensagem durante uma resposta"},
		{"/quit", "sai do programa"},
	}

	maxCmd := 0
	for _, c := range commands {
		if len(c.cmd) > maxCmd {
			maxCmd = len(c.cmd)
		}
	}

	for _, c := range commands {
		padding := strings.Repeat(" ", maxCmd-len(c.cmd)+2)
		b.WriteString("  ")
		b.WriteString(cmdValueStyle.Render(c.cmd))
		b.WriteString(padding)
		b.WriteString(cmdLabelStyle.Render(c.desc))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(cmdLabelStyle.Render("  atalhos: "))
	b.WriteString(welcomeKeyStyle.Render("Tab"))
	b.WriteString(cmdLabelStyle.Render("=completar  "))
	b.WriteString(welcomeKeyStyle.Render("Esc"))
	b.WriteString(cmdLabelStyle.Render("=limpar  "))
	b.WriteString(welcomeKeyStyle.Render("Up/Down"))
	b.WriteString(cmdLabelStyle.Render("=historico  "))
	b.WriteString(welcomeKeyStyle.Render("Ctrl+L"))
	b.WriteString(cmdLabelStyle.Render("=limpar tela"))

	return b.String()
}

func handleClear(deps commandDeps) commandResult {
	if deps.clearSession == nil {
		return commandResult{output: cmdLabelStyle.Render("sessao nao disponivel"), handled: true}
	}
	if err := deps.clearSession(); err != nil {
		return commandResult{output: errorStyle.Render(fmt.Sprintf("erro: %v", err)), handled: true}
	}
	return commandResult{output: notifySuccessStyle.Render("historico limpo"), handled: true, clearDisplay: true}
}

func handleAgents(deps commandDeps) commandResult {
	if deps.listAgents == nil {
		return commandResult{output: cmdLabelStyle.Render("registry de agentes nao disponivel"), handled: true}
	}
	names := deps.listAgents()
	sort.Strings(names)

	active := ""
	if deps.activeAgent != nil {
		active = deps.activeAgent()
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Agentes"))
	b.WriteString("\n\n")

	for _, name := range names {
		b.WriteString("  ")
		if name == active {
			b.WriteString(cmdActiveStyle.Render("● " + name))
			b.WriteString(cmdLabelStyle.Render(" (ativo)"))
		} else {
			b.WriteString(cmdInactiveStyle.Render("○ " + name))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(cmdLabelStyle.Render("  trocar: "))
	b.WriteString(cmdValueStyle.Render("/agent switch <nome>"))

	return commandResult{output: b.String(), handled: true}
}

func handleAgentSwitch(deps commandDeps, name string) commandResult {
	if deps.setAgent == nil {
		return commandResult{output: cmdLabelStyle.Render("servico AI nao disponivel"), handled: true}
	}
	if deps.checkPendingTurn != nil {
		if err := deps.checkPendingTurn(); err != nil {
			return commandResult{output: errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()), handled: true}
		}
	}
	deps.setAgent(name)
	return commandResult{
		output:  notifySuccessStyle.Render("agente trocado: ") + cmdValueStyle.Render(name),
		handled: true,
	}
}

func handleSkills(deps commandDeps) commandResult {
	if deps.listSkills == nil {
		return commandResult{output: cmdLabelStyle.Render("registry de skills nao disponivel"), handled: true}
	}
	list := deps.listSkills()
	sort.Strings(list)

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Skills"))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d)", len(list))))
	b.WriteString("\n\n")

	// Display in 2 columns if wide enough
	cols := 1
	colWidth := 30
	if len(list) > 0 {
		for _, s := range list {
			if len(s)+4 > colWidth {
				colWidth = len(s) + 4
			}
		}
	}

	for _, s := range list {
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render("▸ "))
		b.WriteString(cmdValueStyle.Render(s))
		if cols > 1 {
			padding := colWidth - len(s) - 4
			if padding > 0 {
				b.WriteString(strings.Repeat(" ", padding))
			}
		}
		b.WriteString("\n")
	}

	return commandResult{output: b.String(), handled: true}
}

func handleMemory(parts []string, deps commandDeps) commandResult {
	if deps.listMemories == nil {
		return commandResult{output: cmdLabelStyle.Render("memory store nao disponivel"), handled: true}
	}

	if len(parts) < 2 {
		return commandResult{
			output: cmdLabelStyle.Render("uso: ") +
				cmdValueStyle.Render("/memory list") +
				cmdLabelStyle.Render(" | ") +
				cmdValueStyle.Render("/memory search <query>"),
			handled: true,
		}
	}

	sub := strings.ToLower(parts[1])
	switch sub {
	case "list":
		return handleMemoryList(deps)
	case "search":
		if len(parts) < 3 {
			return commandResult{
				output:  cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/memory search <query>"),
				handled: true,
			}
		}
		query := strings.Join(parts[2:], " ")
		return handleMemorySearch(deps, query)
	default:
		return commandResult{
			output: cmdLabelStyle.Render("uso: ") +
				cmdValueStyle.Render("/memory list") +
				cmdLabelStyle.Render(" | ") +
				cmdValueStyle.Render("/memory search <query>"),
			handled: true,
		}
	}
}

func handleMemoryList(deps commandDeps) commandResult {
	memories, err := deps.listMemories()
	if err != nil {
		return commandResult{output: errorStyle.Render(fmt.Sprintf("erro: %v", err)), handled: true}
	}
	if len(memories) == 0 {
		return commandResult{output: cmdLabelStyle.Render("nenhuma memoria salva"), handled: true}
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Memorias"))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d)", len(memories))))
	b.WriteString("\n\n")

	for _, m := range memories {
		b.WriteString("  ")
		b.WriteString(cmdTopicStyle.Render(m.topic))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(cmdPreviewStyle.Render(truncate(m.content, 72)))
		b.WriteString("\n\n")
	}

	return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}
}

func handleMemorySearch(deps commandDeps, query string) commandResult {
	memories, err := deps.searchMemories(query)
	if err != nil {
		return commandResult{output: errorStyle.Render(fmt.Sprintf("erro: %v", err)), handled: true}
	}
	if len(memories) == 0 {
		return commandResult{
			output:  cmdLabelStyle.Render("nenhuma memoria encontrada para: ") + cmdValueStyle.Render(query),
			handled: true,
		}
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Resultados"))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d)", len(memories))))
	b.WriteString("\n\n")

	for _, m := range memories {
		b.WriteString("  ")
		b.WriteString(cmdTopicStyle.Render(m.topic))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(cmdPreviewStyle.Render(truncate(m.content, 72)))
		b.WriteString("\n\n")
	}

	return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}
}

func handleModel(deps commandDeps) commandResult {
	// /model now opens the interactive model picker overlay so users
	// can browse the full capabilities database and switch models in
	// one place. When no setModel callback is wired we fall back to
	// the old read-only behaviour, reporting the current model name.
	if deps.setModel == nil {
		current := ""
		if deps.currentModel != nil {
			current = deps.currentModel()
		}
		if current == "" && deps.activeAgent != nil {
			current = deps.activeAgent()
		}
		return commandResult{
			output:  cmdLabelStyle.Render("modelo atual: ") + cmdActiveStyle.Render(current),
			handled: true,
		}
	}
	return commandResult{
		handled:         true,
		openModelPicker: true,
	}
}

func handleSession(deps commandDeps) commandResult {
	if deps.sessionInfo == nil {
		return commandResult{output: cmdLabelStyle.Render("sessao nao disponivel"), handled: true}
	}
	id, count, err := deps.sessionInfo()
	if err != nil {
		return commandResult{output: errorStyle.Render(fmt.Sprintf("erro: %v", err)), handled: true}
	}
	active := ""
	if deps.activeAgent != nil {
		active = deps.activeAgent()
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Sessao"))
	b.WriteString("\n\n")

	rows := []struct{ label, value string }{
		{"id", id},
		{"mensagens", fmt.Sprintf("%d", count)},
		{"agente", active},
	}

	maxLabel := 0
	for _, r := range rows {
		if len(r.label) > maxLabel {
			maxLabel = len(r.label)
		}
	}

	for _, r := range rows {
		padding := strings.Repeat(" ", maxLabel-len(r.label))
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render(r.label + padding + "  "))
		b.WriteString(cmdValueStyle.Render(r.value))
		b.WriteString("\n")
	}

	return commandResult{output: b.String(), handled: true}
}

func handleCost(deps commandDeps) commandResult {
	if deps.costReport == nil {
		return commandResult{output: cmdLabelStyle.Render("cost engine nao disponivel"), handled: true}
	}
	report := deps.costReport()
	if report == "" {
		return commandResult{output: cmdLabelStyle.Render("nenhum custo registrado nesta sessao"), handled: true}
	}
	return commandResult{output: report, handled: true}
}

// ── Power commands ──────────────────────────────────────────────────────────

func handleCopy(messages []chatEntry, args []string) commandResult {
	const usage = "uso: /copy [numero|list|all]"
	if len(args) > 1 {
		return commandResult{output: cmdLabelStyle.Render(usage), handled: true}
	}
	if len(args) == 1 && strings.EqualFold(args[0], "all") {
		// This is the resident display history, which can be shorter than the
		// persisted session when older pages have not been loaded with PgUp.
		const maxCopyBytes = 4 << 20
		var combined strings.Builder
		count := 0
		for _, entry := range messages {
			if entry.role != "assistant" || entry.content == "" {
				continue
			}
			separator := ""
			if count > 0 {
				separator = "\n\n---\n\n"
			}
			if combined.Len()+len(separator)+len(entry.content) > maxCopyBytes {
				return commandResult{output: errorStyle.Render("respostas carregadas excedem 4 MiB; copie por numero"), handled: true}
			}
			combined.WriteString(separator)
			combined.WriteString(entry.content)
			count++
		}
		if count == 0 {
			return commandResult{output: cmdLabelStyle.Render("nenhuma resposta para copiar"), handled: true}
		}
		if err := clipboard.WriteAll(combined.String()); err != nil {
			return commandResult{output: errorStyle.Render("erro ao copiar: ") + cmdLabelStyle.Render(err.Error()), handled: true}
		}
		return commandResult{output: notifySuccessStyle.Render(fmt.Sprintf("%d respostas carregadas copiadas", count)), handled: true}
	}
	listing := len(args) == 1 && strings.EqualFold(args[0], "list")
	position := 1
	if len(args) == 1 && !listing {
		parsed, err := strconv.Atoi(args[0])
		if err != nil || parsed < 1 {
			return commandResult{output: cmdLabelStyle.Render(usage), handled: true}
		}
		position = parsed
	}
	count := 0
	var list strings.Builder
	for i := len(messages) - 1; i >= 0; i-- {
		entry := messages[i]
		if entry.role != "assistant" || entry.content == "" {
			continue
		}
		count++
		if listing {
			if count <= 10 {
				list.WriteString(fmt.Sprintf("%d. %s\n", count, copyPreview(entry.content, 80)))
			}
			continue
		}
		if count != position {
			continue
		}
		if err := clipboard.WriteAll(entry.content); err != nil {
			return commandResult{output: errorStyle.Render("erro ao copiar: ") + cmdLabelStyle.Render(err.Error()), handled: true}
		}
		return commandResult{output: notifySuccessStyle.Render(fmt.Sprintf("resposta %d copiada ", position)) +
			cmdLabelStyle.Render("("+copyPreview(entry.content, 60)+")"), handled: true}
	}
	if listing {
		if count == 0 {
			return commandResult{output: cmdLabelStyle.Render("nenhuma resposta carregada"), handled: true}
		}
		if count > 10 {
			list.WriteString(fmt.Sprintf("... %d respostas carregadas no total\n", count))
		}
		list.WriteString("Use /copy <numero> para copiar uma resposta. PgUp carrega respostas mais antigas.")
		return commandResult{output: strings.TrimSpace(list.String()), handled: true}
	}
	if count == 0 {
		return commandResult{output: cmdLabelStyle.Render("nenhuma resposta para copiar"), handled: true}
	}
	return commandResult{output: cmdLabelStyle.Render(fmt.Sprintf("resposta %d indisponível; há %d respostas carregadas", position, count)), handled: true}
}

func copyPreview(content string, maxRunes int) string {
	plain := strings.Join(strings.Fields(ansi.Strip(content)), " ")
	runes := []rune(plain)
	if len(runes) <= maxRunes {
		return plain
	}
	return string(runes[:maxRunes-3]) + "..."
}

func handleRun(command string, deps commandDeps) commandResult {
	var b strings.Builder
	b.WriteString(cmdLabelStyle.Render("$ "))
	b.WriteString(cmdValueStyle.Render(command))
	b.WriteString("\n\n")

	output, err := runShellCmd(command, commandWorkingDir(deps))
	if err != nil {
		b.WriteString(errorStyle.Render("erro: "))
		b.WriteString(cmdLabelStyle.Render(err.Error()))
		if output != "" {
			b.WriteString("\n")
			b.WriteString(cmdPreviewStyle.Render(output))
		}
	} else if output == "" {
		b.WriteString(cmdLabelStyle.Render("(sem output)"))
	} else {
		b.WriteString(cmdPreviewStyle.Render(output))
	}

	return commandResult{output: b.String(), handled: true}
}

func handleFile(path string, deps commandDeps) commandResult {
	// Resolve relative paths
	if !filepath.IsAbs(path) {
		path = filepath.Join(commandWorkingDir(deps), path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}
	if info.IsDir() {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render("caminho e um diretorio, use /project"),
			handled: true,
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return commandResult{
			output:  errorStyle.Render("erro ao ler: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}

	lines := strings.Split(string(data), "\n")
	maxLines := 200
	truncated := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render(filepath.Base(path)))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d linhas)", len(strings.Split(string(data), "\n")))))
	b.WriteString("\n\n")

	for i, line := range lines {
		lineNum := fmt.Sprintf("%4d", i+1)
		b.WriteString(subtleStyle.Render(lineNum))
		b.WriteString(cmdLabelStyle.Render("  "))
		b.WriteString(cmdPreviewStyle.Render(line))
		b.WriteString("\n")
	}

	if truncated {
		b.WriteString("\n")
		b.WriteString(cmdLabelStyle.Render(fmt.Sprintf("  ... truncado em %d linhas", maxLines)))
	}

	return commandResult{output: b.String(), handled: true}
}

func handleSearch(pattern string, deps commandDeps) commandResult {
	output, err := runShellCmd(fmt.Sprintf("grep -rn --include='*' --max-count=30 %q .", pattern), commandWorkingDir(deps))
	if err != nil {
		if output == "" {
			return commandResult{
				output:  cmdLabelStyle.Render("nenhum resultado para: ") + cmdValueStyle.Render(pattern),
				handled: true,
			}
		}
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > 30 {
		lines = lines[:30]
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Busca"))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" \"%s\" (%d resultados)", pattern, len(lines))))
	b.WriteString("\n\n")

	for _, line := range lines {
		// Format: ./file:line:content
		parts := strings.SplitN(line, ":", 3)
		if len(parts) >= 3 {
			b.WriteString("  ")
			b.WriteString(cmdBadgeStyle.Render(parts[0]))
			b.WriteString(subtleStyle.Render(":"))
			b.WriteString(cmdValueStyle.Render(parts[1]))
			b.WriteString(subtleStyle.Render("  "))
			b.WriteString(cmdPreviewStyle.Render(strings.TrimSpace(parts[2])))
			b.WriteString("\n")
		} else {
			b.WriteString("  ")
			b.WriteString(cmdPreviewStyle.Render(line))
			b.WriteString("\n")
		}
	}

	return commandResult{output: b.String(), handled: true}
}

func handleGit(sub string, deps commandDeps) commandResult {
	var shellCmd string

	switch sub {
	case "log":
		shellCmd = "git log --oneline -20"
	case "diff":
		shellCmd = "git diff --stat"
	case "branch":
		shellCmd = "git branch -a"
	case "status", "":
		shellCmd = "git status --short"
	default:
		return commandResult{
			output:  cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/git [status|log|diff|branch]"),
			handled: true,
		}
	}

	output, err := runShellCmd(shellCmd, commandWorkingDir(deps))

	var b strings.Builder
	b.WriteString(cmdLabelStyle.Render("$ "))
	b.WriteString(cmdValueStyle.Render(shellCmd))
	b.WriteString("\n\n")

	if err != nil {
		b.WriteString(errorStyle.Render("erro: "))
		b.WriteString(cmdLabelStyle.Render(err.Error()))
	} else if output == "" {
		b.WriteString(cmdLabelStyle.Render("(limpo)"))
	} else {
		b.WriteString(cmdPreviewStyle.Render(output))
	}

	return commandResult{output: b.String(), handled: true}
}

func handleProject(deps commandDeps) commandResult {
	wd := commandWorkingDir(deps)

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Projeto"))
	b.WriteString(cmdLabelStyle.Render(" " + filepath.Base(wd)))
	b.WriteString("\n\n")
	b.WriteString(cmdValueStyle.Render("  " + filepath.Base(wd) + "/"))
	b.WriteString("\n")
	buildTree(&b, wd, "  ", 1, 3)

	return commandResult{output: b.String(), handled: true}
}

func handleExport(messages []chatEntry, path string) commandResult {
	if len(messages) == 0 {
		return commandResult{output: cmdLabelStyle.Render("nenhuma mensagem pra exportar"), handled: true}
	}

	var b strings.Builder
	b.WriteString("# Chat Export\n\n")
	b.WriteString(fmt.Sprintf("*Exportado em %s*\n\n", time.Now().Format("2006-01-02 15:04:05")))
	b.WriteString("---\n\n")

	for _, entry := range messages {
		switch entry.role {
		case "user":
			b.WriteString("**> Voce:**\n\n")
			b.WriteString(entry.content)
			b.WriteString("\n\n")
		case "assistant":
			b.WriteString("**Assistente:**\n\n")
			b.WriteString(entry.content)
			b.WriteString("\n\n")
		case "system":
			b.WriteString("*Sistema:*\n\n")
			b.WriteString(entry.content)
			b.WriteString("\n\n")
		case "error":
			b.WriteString("*Erro:* ")
			b.WriteString(entry.content)
			b.WriteString("\n\n")
		}
		b.WriteString("---\n\n")
	}

	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return commandResult{
			output:  errorStyle.Render("erro ao salvar: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}

	return commandResult{
		output: notifySuccessStyle.Render("conversa exportada: ") + cmdValueStyle.Render(path) +
			cmdLabelStyle.Render(fmt.Sprintf(" (%d mensagens)", len(messages))),
		handled: true,
	}
}

func handleContext(deps commandDeps, messages []chatEntry) commandResult {
	wd, _ := os.Getwd()

	agent := "n/a"
	if deps.activeAgent != nil {
		if a := deps.activeAgent(); a != "" {
			agent = a
		}
	}

	skillCount := 0
	if deps.listSkills != nil {
		skillCount = len(deps.listSkills())
	}

	memoryCount := 0
	if deps.listMemories != nil {
		if mems, err := deps.listMemories(); err == nil {
			memoryCount = len(mems)
		}
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Contexto"))
	b.WriteString("\n\n")

	rows := []struct{ label, value string }{
		{"mensagens", fmt.Sprintf("%d", len(messages))},
		{"agente", agent},
		{"skills", fmt.Sprintf("%d", skillCount)},
		{"memorias", fmt.Sprintf("%d", memoryCount)},
		{"diretorio", wd},
	}

	maxLabel := 0
	for _, r := range rows {
		if len(r.label) > maxLabel {
			maxLabel = len(r.label)
		}
	}

	for _, r := range rows {
		padding := strings.Repeat(" ", maxLabel-len(r.label))
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render(r.label + padding + "  "))
		b.WriteString(cmdValueStyle.Render(r.value))
		b.WriteString("\n")
	}

	return commandResult{output: b.String(), handled: true}
}

func handleSessions(parts []string, deps commandDeps) commandResult {
	if len(parts) >= 2 && strings.EqualFold(parts[1], "live") {
		if deps.listLiveSessions == nil {
			return commandResult{output: "Registro de sessões ativas indisponível.", handled: true}
		}
		output, err := deps.listLiveSessions()
		if err != nil {
			return commandResult{output: errorStyle.Render(fmt.Sprintf("erro: %v", err)), handled: true}
		}
		return commandResult{output: output, handled: true}
	}
	if deps.listSessions == nil {
		return commandResult{output: cmdLabelStyle.Render("session storage nao disponivel"), handled: true}
	}

	// /sessions search <query>
	if len(parts) >= 3 && strings.ToLower(parts[1]) == "search" {
		query := strings.Join(parts[2:], " ")
		if deps.searchSessions == nil {
			return commandResult{output: cmdLabelStyle.Render("busca de sessoes nao disponivel"), handled: true}
		}
		sessions, err := deps.searchSessions(query)
		if err != nil {
			return commandResult{output: errorStyle.Render(fmt.Sprintf("erro: %v", err)), handled: true}
		}
		if len(sessions) == 0 {
			return commandResult{
				output:  cmdLabelStyle.Render("nenhuma sessao encontrada para: ") + cmdValueStyle.Render(query),
				handled: true,
			}
		}
		return formatSessionsList("Resultados", sessions)
	}

	// /sessions [count]
	count := 10
	if len(parts) >= 2 {
		fmt.Sscanf(parts[1], "%d", &count)
	}

	sessions, err := deps.listSessions(count)
	if err != nil {
		return commandResult{output: errorStyle.Render(fmt.Sprintf("erro: %v", err)), handled: true}
	}
	if len(sessions) == 0 {
		return commandResult{
			output: cmdLabelStyle.Render("nenhuma sessao persistida. use ") +
				cmdValueStyle.Render("-c") +
				cmdLabelStyle.Render(" ou ") +
				cmdValueStyle.Render("-r <id>") +
				cmdLabelStyle.Render(" para restaurar sessoes."),
			handled: true,
		}
	}

	return formatSessionsList("Sessoes", sessions)
}

func formatSessionsList(title string, sessions []sessionEntry) commandResult {
	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render(title))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d)", len(sessions))))
	b.WriteString("\n\n")

	for _, s := range sessions {
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render(s.id))
		b.WriteString("  ")
		b.WriteString(cmdValueStyle.Render(s.title))
		b.WriteString("\n")
		b.WriteString("    ")
		b.WriteString(subtleStyle.Render(fmt.Sprintf("%d msgs  %s", s.messages, s.updatedAt.Format("2006-01-02 15:04"))))
		if s.preview != "" {
			b.WriteString("\n    ")
			b.WriteString(cmdPreviewStyle.Render(truncate(s.preview, 80)))
		}
		b.WriteString("\n\n")
	}

	b.WriteString(cmdLabelStyle.Render("  restaurar: "))
	b.WriteString(cmdValueStyle.Render("polypod -r <id>"))
	b.WriteString(cmdLabelStyle.Render("  continuar ultima: "))
	b.WriteString(cmdValueStyle.Render("polypod -c"))

	return commandResult{output: b.String(), handled: true}
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// memoryEntry is a simplified memory representation for display.
type memoryEntry struct {
	topic   string
	content string
}

// sessionEntry is a simplified session representation for display.
type sessionEntry struct {
	id        string
	title     string
	messages  int
	updatedAt time.Time
	preview   string
}

type rewindTurn struct {
	preview string
}

type rewindOutcome struct {
	messages  []chatEntry
	restored  []string
	conflicts []string
	warnings  []string
	removed   int
}

// commandDeps holds function callbacks for slash commands.
type commandDeps struct {
	workingDir     func() string
	semanticSearch func(context.Context, string) (string, error)
	turnDiff       func() ([]turncheckpoint.FileDiff, error)
	captureVoice   func(context.Context, context.Context, time.Duration, func(string)) (string, error)
	// bindBranches connects /branch to persisted transcripts and the active
	// conversation. The CLI state binds lazily on the first /branch command.
	bindBranches             func(*cliSessionState) error
	commandRegistry          *command.Registry
	activeAgent              func() string
	setAgent                 func(name string)
	checkPendingTurn         func() error
	discardRecovery          func() (string, error)
	listAgents               func() []string
	listSkills               func() []string
	listMemories             func() ([]memoryEntry, error)
	searchMemories           func(query string) ([]memoryEntry, error)
	clearSession             func() error
	sessionInfo              func() (string, int, error) // id, count, err
	activeSessionID          func() string
	activeSessionRevision    func() string
	setActiveSessionRevision func(string)
	// recoveryState rechecks the durable turn after a stream error. This is
	// required before offering /recover or allowing a full-message retry.
	recoveryState    func() (notice, draft, resumeTurnID string)
	listSessions     func(count int) ([]sessionEntry, error)
	searchSessions   func(query string) ([]sessionEntry, error)
	listLiveSessions func() (string, error)
	suggestPrompts   func() ([]string, error)
	costReport       func() string // returns formatted cost report from CostEngine

	// sessionCost returns the current session total cost in USD. Used by
	// the rich status bar. Optional.
	sessionCost func() float64
	// contextTokens returns the current (used, max) context window token
	// counts. Used by the rich status bar. Optional.
	contextTokens func() (used, max int)
	// currentModel returns the model name currently selected for the
	// active agent. Used by the rich status bar. Optional.
	currentModel func() string
	// setModel switches the underlying AI client's model. Optional;
	// when nil, the /model command falls back to a read-only display
	// and the model picker overlay is disabled.
	setModel            func(model string)
	setEffort           func(effort string)
	currentProvider     func() string
	setVisionModel      func(providerName, model string) error
	visionConfig        func() (explicitProvider, explicitModel, effectiveAutoModel string)
	latestWaterfall     func() string
	listChildQuestions  func() string
	answerChildQuestion func(id, answer string) error
	saveChildDraft      func(id, draft string) error
	listProviders   func() []ProviderOption
	switchProvider  func(name, model string) error
	connectProvider func(name, key string) error
	connectChatGPT  func(context.Context) error
	connectOAuth    func(context.Context, string) error
	// generateCommitMsg calls the AI to produce a Conventional-Commits
	// message from a staged diff prompt. Returns the raw message.
	generateCommitMsg func(prompt string) (string, error)
	// generateText runs a one-shot, history-less AI prompt and returns
	// the raw response text. Used by /review and /security-review for
	// non-streaming AI calls outside of the chat conversation.
	generateText     func(prompt string) (string, error)
	generateSideText func(prompt string) (string, error)
	// compactAvailable reports whether the compact service is wired in.
	compactAvailable func() bool
	// compactHistory summarizes and persists the active session, then returns
	// the new display history and estimated token counts.
	compactHistory func() ([]chatEntry, int, int, error)

	// loadSession restores the conversation and returns its newest display page.
	loadSession      func(id string) (sessionLoad, error)
	loadOlderHistory func(id string, before, limit int) ([]chatEntry, int, error)
	// truncateMessages drops the last n messages from the active
	// conversation, persists the change, and returns the number actually
	// removed. Used by /rewind.
	truncateMessages func(n int) (int, error)
	rewindTurns      func(n int, withFiles, withConversation bool) (rewindOutcome, error)
	listRewindTurns  func() ([]rewindTurn, error)

	// permissionsList returns all configured permission rules, each tuple
	// being (pattern, decision, source).
	permissionsList func() [][3]string
	// permissionsAdd adds a new rule with the given decision ("allow",
	// "deny", or "ask") for the given tool pattern, persisting it to
	// .polypod/settings.json.
	permissionsAdd func(decision, pattern string) error
	// permissionsRemove removes all rules matching the given tool pattern.
	// Returns the number of rules removed.
	permissionsRemove func(pattern string) (int, error)
	// permissionsReset clears all in-memory rules and rewrites the
	// .polypod/settings.json file to a clean slate.
	permissionsReset func() error

	// planCurrent returns the current mode name: "edit", "plan", "ask"
	// or "auto".
	planCurrent func() string
	// planSet changes the mode; the first return value is a short
	// description suitable for display.
	planSet func(mode string) (string, error)
	// planSummary returns a human-readable summary of the simulated
	// operations recorded while in plan mode.
	planSummary func() string
	// planExecute switches back to edit mode and returns a summary of
	// what would be executed. The actual execution of simulated actions
	// depends on upstream wiring; by default this just drains the
	// simulator.
	planExecute func() (string, error)

	// ── MCP management (for /mcp) ───────────────────────────────────
	// listMCPServers returns a snapshot of configured/connected MCP
	// servers with their transport and tool counts.
	listMCPServers func() []mcpServerInfo
	// reconnectMCP closes and re-opens the connection to a named server.
	reconnectMCP func(name string) error

	// ── Hooks management (for /hooks) ───────────────────────────────
	// listHooks returns a snapshot of all registered legacy hooks.
	listHooks func() []hookInfo
	// setHookEnabled toggles a hook by name. Returns true if the hook
	// was found and updated, false otherwise.
	setHookEnabled func(name string, enabled bool) bool

	// ── Session-local mutable state (for /files, /add-dir) ───────────
	// sessionState holds runtime state that slash commands can mutate
	// (tracked files, additional working dirs, etc.). Owned by the
	// bubbletea model.
	sessionState *cliSessionState

	// modelBreakdown returns the per-model usage/cost breakdown from
	// the active CostEngine. Used by /usage to render a per-provider
	// summary. Optional.
	modelBreakdown func() map[string]tracking.ModelCost

	// ── Config (for /config) ────────────────────────────────────────
	// configSnapshot returns the live *config.Config used by the
	// running polypod instance. The form is read-only with respect to
	// this pointer until the user explicitly applies/saves.
	configSnapshot func() *config.Config
	// configGet returns the current value of a dotted-path key, plus
	// whether the key was recognised. Optional.
	configGet func(key string) (string, bool)
	// configSet writes a new value to a dotted-path key (in-memory),
	// returning an error if the key/value is not valid.
	configSet func(key, value string) error
	// configSave persists the current in-memory config to disk.
	configSave func() error
	// configReload reloads the config from disk into memory.
	configReload func() error
	// configPath reports the on-disk path used by save/reload.
	configPath func() string
}

func commandWorkingDir(deps commandDeps) string {
	if deps.workingDir != nil {
		if dir := deps.workingDir(); dir != "" {
			return dir
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

// runShellCmd executes a shell command with a 15s timeout and returns its output.
func runShellCmd(command string, dir ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	if len(dir) > 0 {
		cmd.Dir = dir[0]
	}
	out, err := cmd.CombinedOutput()

	output := string(out)
	if len(output) > 8000 {
		output = output[:8000] + "\n... (truncado em 8000 chars)"
	}

	return strings.TrimRight(output, "\n"), err
}

// ── /init, /commit, /diff, /compact ─────────────────────────────────────────

// handleInit bootstraps .polypod.md and the .polypod/ directory using the
// initcmd package. Supports --force to re-run on already-inited projects.
func handleInit(parts []string, deps commandDeps) commandResult {
	wd := commandWorkingDir(deps)

	force := false
	for _, p := range parts[1:] {
		if p == "--force" || p == "-f" {
			force = true
		}
	}

	mdPath := filepath.Join(wd, ".polypod.md")
	polypodDir := filepath.Join(wd, ".polypod")
	_, mdStatErr := os.Stat(mdPath)
	_, dirStatErr := os.Stat(polypodDir)
	mdExists := mdStatErr == nil
	dirExists := dirStatErr == nil

	if (mdExists || dirExists) && !force {
		var b strings.Builder
		b.WriteString(cmdTitleStyle.Render("Projeto ja inicializado"))
		b.WriteString("\n\n")
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render(".polypod.md  "))
		if mdExists {
			b.WriteString(cmdActiveStyle.Render("presente"))
		} else {
			b.WriteString(cmdInactiveStyle.Render("ausente"))
		}
		b.WriteString("\n  ")
		b.WriteString(cmdLabelStyle.Render(".polypod/    "))
		if dirExists {
			b.WriteString(cmdActiveStyle.Render("presente"))
		} else {
			b.WriteString(cmdInactiveStyle.Render("ausente"))
		}
		b.WriteString("\n\n")
		b.WriteString(cmdLabelStyle.Render("  use "))
		b.WriteString(cmdValueStyle.Render("/init --force"))
		b.WriteString(cmdLabelStyle.Render(" para completar arquivos faltantes"))
		return commandResult{output: b.String(), handled: true}
	}

	stack := initcmd.Detect(wd)
	created, err := initcmd.WriteInit(wd, stack)
	if err != nil {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Projeto inicializado"))
	b.WriteString("\n\n")

	// Stack detection summary
	rows := []struct{ label, value string }{}
	if stack.Language != "" {
		rows = append(rows, struct{ label, value string }{"linguagem", stack.Language})
	}
	if stack.Framework != "" {
		rows = append(rows, struct{ label, value string }{"framework", stack.Framework})
	}
	if stack.BuildTool != "" {
		rows = append(rows, struct{ label, value string }{"build", stack.BuildTool})
	}
	if stack.TestCmd != "" {
		rows = append(rows, struct{ label, value string }{"testes", stack.TestCmd})
	}
	if stack.LintCmd != "" {
		rows = append(rows, struct{ label, value string }{"lint", stack.LintCmd})
	}

	maxLabel := 0
	for _, r := range rows {
		if len(r.label) > maxLabel {
			maxLabel = len(r.label)
		}
	}
	for _, r := range rows {
		padding := strings.Repeat(" ", maxLabel-len(r.label))
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render(r.label + padding + "  "))
		b.WriteString(cmdValueStyle.Render(r.value))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if len(created) == 0 {
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render("nenhum arquivo novo criado (tudo ja existia)"))
	} else {
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render(fmt.Sprintf("criados (%d):", len(created))))
		b.WriteString("\n")
		for _, f := range created {
			rel, err := filepath.Rel(wd, f)
			if err != nil {
				rel = f
			}
			b.WriteString("  ")
			b.WriteString(cmdBadgeStyle.Render("▸ "))
			b.WriteString(cmdValueStyle.Render(rel))
			b.WriteString("\n")
		}
	}

	return commandResult{output: b.String(), handled: true}
}

// handleCommit creates a git commit, generating the message via AI when
// no explicit message is provided by the user.
func handleCommit(deps commandDeps, userMsg string) commandResult {
	dir := commandWorkingDir(deps)
	if !pgit.IsInsideRepoAt(dir) {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render("nao esta em um repositorio git"),
			handled: true,
		}
	}
	checkCtx, cancelChecks := context.WithTimeout(context.Background(), 5*time.Minute)
	results, checkErr := projectchecks.Run(checkCtx, dir, "commit")
	cancelChecks()
	if checkErr != nil {
		return commandResult{output: errorStyle.Render("erro nos verificadores: " + checkErr.Error()), handled: true}
	}
	for _, result := range results {
		if result.Err != nil {
			return commandResult{output: errorStyle.Render("commit bloqueado por falha na verificação\n") + projectchecks.FormatResults(results), handled: true}
		}
	}

	// If nothing staged, auto-stage everything so we have something to commit.
	autoStaged := false
	if !pgit.HasStagedChanges(dir) {
		if err := pgit.StageAll(dir); err != nil {
			return commandResult{
				output:  errorStyle.Render("erro ao stageing: ") + cmdLabelStyle.Render(err.Error()),
				handled: true,
			}
		}
		autoStaged = true
		if !pgit.HasStagedChanges(dir) {
			return commandResult{
				output:  cmdLabelStyle.Render("nenhuma mudanca para commitar (working tree limpo)"),
				handled: true,
			}
		}
	}

	diff, changes, err := commitai.GetStagedDiff(dir)
	if err != nil {
		return commandResult{
			output:  errorStyle.Render("erro git diff: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}
	if len(changes) == 0 {
		return commandResult{
			output:  cmdLabelStyle.Render("nenhum arquivo no staging area"),
			handled: true,
		}
	}

	// Determine commit message
	message := strings.TrimSpace(userMsg)
	aiUsed := false
	if message == "" {
		if deps.generateCommitMsg != nil {
			prompt := commitai.GenerateCommitPrompt(diff, changes, false)
			gen, gerr := deps.generateCommitMsg(prompt)
			if gerr == nil && strings.TrimSpace(gen) != "" {
				message = strings.TrimSpace(gen)
				aiUsed = true
			}
		}
		if message == "" {
			// Fallback: heuristic message from file list.
			scope := commitai.DetectScope(changes)
			if scope != "" {
				message = fmt.Sprintf("chore(%s): update %d file(s)", scope, len(changes))
			} else {
				message = fmt.Sprintf("chore: update %d file(s)", len(changes))
			}
		}
	}

	// Strip code fences/quotes that the model sometimes wraps around.
	message = stripCommitMessageFences(message)

	if err := pgit.CreateCommit(message, dir); err != nil {
		return commandResult{
			output:  errorStyle.Render("erro git commit: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Commit criado"))
	b.WriteString("\n\n")
	if autoStaged {
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render("▸ "))
		b.WriteString(cmdLabelStyle.Render("git add -A (auto)"))
		b.WriteString("\n")
	}
	if aiUsed {
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render("▸ "))
		b.WriteString(cmdLabelStyle.Render("mensagem gerada pela AI"))
		b.WriteString("\n")
	}
	b.WriteString("  ")
	b.WriteString(cmdBadgeStyle.Render("▸ "))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf("%d arquivo(s) commitado(s)", len(changes))))
	b.WriteString("\n\n")
	b.WriteString(cmdLabelStyle.Render("  mensagem:"))
	b.WriteString("\n    ")
	b.WriteString(cmdValueStyle.Render(firstLine(message)))
	if rest := tailLines(message); rest != "" {
		b.WriteString("\n    ")
		b.WriteString(cmdPreviewStyle.Render(rest))
	}

	return commandResult{output: b.String(), handled: true}
}

func handleCodeIndex(parts []string, deps commandDeps) commandResult {
	root := commandWorkingDir(deps)
	mode := "status"
	if len(parts) > 1 {
		mode = parts[1]
	}
	switch mode {
	case "on", "ativar":
		if deps.semanticSearch == nil {
			return commandResult{output: "Configure um provedor de embeddings no /config antes de ativar o índice.", handled: true}
		}
		if err := codesearch.SetEnabled(root, true); err != nil {
			return commandResult{output: errorStyle.Render(err.Error()), handled: true}
		}
		return commandResult{output: "Busca semântica ativada neste projeto. Use /semantic <descrição>.", handled: true}
	case "off", "desativar":
		if err := codesearch.SetEnabled(root, false); err != nil {
			return commandResult{output: errorStyle.Render(err.Error()), handled: true}
		}
		return commandResult{output: "Busca semântica desativada neste projeto.", handled: true}
	case "status", "estado":
		cfg, err := codesearch.LoadConfig(root)
		if err != nil {
			return commandResult{output: errorStyle.Render(err.Error()), handled: true}
		}
		if cfg.Enabled {
			return commandResult{output: "Índice semântico ativo. Limites: " + strconv.Itoa(cfg.MaxFiles) + " arquivos, " + strconv.Itoa(cfg.MaxChunks) + " trechos.", handled: true}
		}
		return commandResult{output: "Índice semântico desativado. Use /index on para ativar.", handled: true}
	default:
		return commandResult{output: "Uso: /index [on|off|status]", handled: true}
	}
}

// handleDiff shows uncommitted diffs with ANSI color highlighting.
func handleDiff(parts []string, deps commandDeps) commandResult {
	dir := commandWorkingDir(deps)
	if !pgit.IsInsideRepoAt(dir) {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render("nao esta em um repositorio git"),
			handled: true,
		}
	}

	staged := false
	var targetFile string
	for _, p := range parts[1:] {
		switch p {
		case "--staged", "--cached":
			staged = true
		default:
			if !strings.HasPrefix(p, "-") {
				targetFile = p
			}
		}
	}

	var args []string
	args = append(args, "diff")
	if staged {
		args = append(args, "--cached")
	}
	if targetFile != "" {
		args = append(args, "--", targetFile)
	}

	cmdStr := "git " + strings.Join(args, " ")
	output, err := runGitCapture(dir, args...)
	if err != nil && output == "" {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}

	var b strings.Builder
	b.WriteString(cmdLabelStyle.Render("$ "))
	b.WriteString(cmdValueStyle.Render(cmdStr))
	b.WriteString("\n\n")

	if strings.TrimSpace(output) == "" {
		b.WriteString(cmdLabelStyle.Render("(sem diferencas)"))
		return commandResult{output: b.String(), handled: true}
	}

	// Colorize each line: + green, - red, @@ cyan, file headers bold.
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			b.WriteString(cmdTitleStyle.Render(line))
		case strings.HasPrefix(line, "@@"):
			b.WriteString(cmdBadgeStyle.Render(line))
		case strings.HasPrefix(line, "+"):
			b.WriteString(diffAddStyle.Render(line))
		case strings.HasPrefix(line, "-"):
			b.WriteString(diffDelStyle.Render(line))
		case strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "index "):
			b.WriteString(subtleStyle.Render(line))
		default:
			b.WriteString(cmdPreviewStyle.Render(line))
		}
		b.WriteString("\n")
	}

	return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}
}

// handleCompact summarizes and persists the active conversation when wired.
func handleCompact(deps commandDeps, messages []chatEntry) commandResult {
	if deps.compactHistory != nil {
		replacement, before, after, err := deps.compactHistory()
		if err != nil {
			return commandResult{output: errorStyle.Render("Nao foi possivel compactar: " + err.Error()), handled: true}
		}
		return commandResult{
			output:          cmdLabelStyle.Render(fmt.Sprintf("Contexto compactado: ~%d para ~%d tokens.", before, after)),
			replaceMessages: replacement,
			handled:         true,
		}
	}

	// Rough token estimate: ~4 chars per token with a small overhead.
	totalChars := 0
	for _, m := range messages {
		totalChars += len(m.content) + 4
	}
	estTokens := (totalChars * 4) / 12 // /4 for tokens, *4/3 for padding

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Compactacao de contexto"))
	b.WriteString("\n\n")

	rows := []struct{ label, value string }{
		{"mensagens", fmt.Sprintf("%d", len(messages))},
		{"tokens (est.)", fmt.Sprintf("~%d", estTokens)},
	}
	maxLabel := 0
	for _, r := range rows {
		if len(r.label) > maxLabel {
			maxLabel = len(r.label)
		}
	}
	for _, r := range rows {
		padding := strings.Repeat(" ", maxLabel-len(r.label))
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render(r.label + padding + "  "))
		b.WriteString(cmdValueStyle.Render(r.value))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if deps.compactAvailable != nil && deps.compactAvailable() {
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render("▸ "))
		b.WriteString(cmdLabelStyle.Render("auto-compact esta ativo; a AI ira resumir automaticamente quando o contexto encher"))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render("▸ "))
		b.WriteString(cmdLabelStyle.Render("para forcar agora, use "))
		b.WriteString(cmdValueStyle.Render("/clear"))
		b.WriteString(cmdLabelStyle.Render(" (apaga historico) ou aguarde o limite"))
	} else {
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render("▸ "))
		b.WriteString(cmdLabelStyle.Render("servico de compact nao esta conectado neste build"))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render("▸ "))
		b.WriteString(cmdLabelStyle.Render("uma implementacao completa chamaria "))
		b.WriteString(cmdValueStyle.Render("compact.CompactService.Compact()"))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render("▸ "))
		b.WriteString(cmdLabelStyle.Render("use "))
		b.WriteString(cmdValueStyle.Render("/clear"))
		b.WriteString(cmdLabelStyle.Render(" para limpar o historico manualmente"))
	}

	return commandResult{output: b.String(), handled: true}
}

// stripCommitMessageFences removes code fences and wrapping quotes that LLMs
// sometimes add around the commit message.
func stripCommitMessageFences(msg string) string {
	msg = strings.TrimSpace(msg)
	// Strip surrounding code fences
	if strings.HasPrefix(msg, "```") {
		if idx := strings.Index(msg, "\n"); idx >= 0 {
			msg = msg[idx+1:]
		}
		msg = strings.TrimSuffix(msg, "```")
		msg = strings.TrimSpace(msg)
	}
	// Strip surrounding quotes
	if len(msg) >= 2 {
		first, last := msg[0], msg[len(msg)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			msg = msg[1 : len(msg)-1]
		}
	}
	return strings.TrimSpace(msg)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func tailLines(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return ""
}

// buildTree recursively builds a tree representation of a directory.
func buildTree(b *strings.Builder, dir string, prefix string, depth int, maxDepth int) {
	if depth > maxDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Filter ignored directories
	ignored := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"__pycache__": true, "dist": true, "build": true,
		".next": true, ".cache": true, ".idea": true,
		".vscode": true, "target": true, "coverage": true,
	}

	var filtered []os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && ignored[name] {
			continue
		}
		if ignored[name] {
			continue
		}
		filtered = append(filtered, e)
	}

	for i, entry := range filtered {
		isLast := i == len(filtered)-1
		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}

		name := entry.Name()
		if entry.IsDir() {
			b.WriteString(prefix)
			b.WriteString(cmdBadgeStyle.Render(connector))
			b.WriteString(cmdValueStyle.Render(name + "/"))
			b.WriteString("\n")
			buildTree(b, filepath.Join(dir, name), childPrefix, depth+1, maxDepth)
		} else {
			b.WriteString(prefix)
			b.WriteString(subtleStyle.Render(connector))
			b.WriteString(cmdPreviewStyle.Render(name))
			b.WriteString("\n")
		}
	}
}

// fileExists is a small helper used by /init (and friends) to check
// whether a given path is already present on disk.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// shortID returns the first 8 characters of a session ID (or the full ID
// if it's already short), stripping a leading "ses_" if present.
func shortID(id string) string {
	s := strings.TrimPrefix(id, "ses_")
	if len(s) > 8 {
		s = s[:8]
	}
	return s
}

// timeAgo formats the duration between t and now in a compact form
// (e.g. "2h atras", "3d atras", "agora").
func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "agora"
	case d < time.Hour:
		return fmt.Sprintf("%dm atras", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh atras", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd atras", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

func parsePositiveInt(s string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("negative")
	}
	return n, nil
}

// stripMarkdown removes simple markdown emphasis markers for clean display.
func stripMarkdown(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.ReplaceAll(s, "`", "")
	return s
}

// ── /resume ─────────────────────────────────────────────────────────────────

// handleResume lists recent persisted sessions or loads a specific one by
// ID (or 1-based index into the listing) into the current conversation.
func handleResume(deps commandDeps, arg string) commandResult {
	if deps.listSessions == nil {
		return commandResult{
			output:  cmdLabelStyle.Render("armazenamento de sessoes nao disponivel"),
			handled: true,
		}
	}

	sessions, err := deps.listSessions(10)
	if err != nil {
		return commandResult{
			output:  errorStyle.Render(fmt.Sprintf("erro: %v", err)),
			handled: true,
		}
	}
	if len(sessions) == 0 {
		return commandResult{
			output:  cmdLabelStyle.Render("nenhuma sessao persistida encontrada"),
			handled: true,
		}
	}

	// No arg: show the interactive list.
	if arg == "" {
		return formatResumeList(sessions)
	}

	// Arg might be a 1-based index into the list or a (possibly short) ID.
	target := ""
	if n, err := parsePositiveInt(arg); err == nil && n >= 1 && n <= len(sessions) {
		target = sessions[n-1].id
	} else {
		for _, s := range sessions {
			if s.id == arg || strings.HasPrefix(s.id, arg) ||
				strings.HasPrefix(shortID(s.id), arg) {
				target = s.id
				break
			}
		}
	}

	if target == "" {
		return commandResult{
			output: errorStyle.Render("sessao nao encontrada: ") +
				cmdValueStyle.Render(arg),
			handled: true,
		}
	}

	if deps.loadSession == nil {
		return commandResult{
			output:  cmdLabelStyle.Render("carregamento de sessao nao disponivel"),
			handled: true,
		}
	}

	loaded, err := deps.loadSession(target)
	if err != nil {
		return commandResult{
			output:  errorStyle.Render(fmt.Sprintf("erro ao carregar: %v", err)),
			handled: true,
		}
	}
	entries := loaded.entries
	var branchErr error
	if deps.sessionState != nil {
		if deps.sessionState.branchesBound() {
			branchErr = deps.sessionState.refreshPersistentBranches()
		} else if deps.bindBranches != nil {
			branchErr = deps.bindBranches(deps.sessionState)
		}
	}

	var b strings.Builder
	messageCount := len(entries)
	if deps.sessionInfo != nil {
		if _, count, err := deps.sessionInfo(); err == nil {
			messageCount = count
		}
	}
	b.WriteString(notifySuccessStyle.Render("sessao restaurada: "))
	b.WriteString(cmdValueStyle.Render(shortID(target)))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d mensagens)", messageCount)))
	if branchErr != nil {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render("branches nao sincronizadas: "))
		b.WriteString(cmdLabelStyle.Render(branchErr.Error()))
	}

	return commandResult{
		output:          b.String(),
		handled:         true,
		replaceMessages: entries,
		resumeSessionID: target,
		historyBefore:   loaded.start,
		recoveryDraft:   loaded.recoveryDraft,
		recoveryTurnID:  loaded.recoveryTurnID,
	}
}

func formatResumeList(sessions []sessionEntry) commandResult {
	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Sessoes recentes"))
	b.WriteString("\n\n")

	for i, s := range sessions {
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render(fmt.Sprintf("%2d.", i+1)))
		b.WriteString(" ")
		b.WriteString(cmdBadgeStyle.Render(shortID(s.id)))
		b.WriteString("  ")
		b.WriteString(subtleStyle.Render(timeAgo(s.updatedAt)))
		b.WriteString("  ")
		b.WriteString(subtleStyle.Render(fmt.Sprintf("%d msgs", s.messages)))
		b.WriteString("\n      ")
		title := s.title
		if title == "" {
			title = s.preview
		}
		if title == "" {
			title = "(sem titulo)"
		}
		b.WriteString(cmdPreviewStyle.Render(truncate(title, 72)))
		b.WriteString("\n\n")
	}

	b.WriteString(cmdLabelStyle.Render("  use "))
	b.WriteString(cmdValueStyle.Render("/resume <numero|id>"))
	b.WriteString(cmdLabelStyle.Render(" para restaurar uma sessao"))

	return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}
}

// ── /rewind ─────────────────────────────────────────────────────────────────

func handleDurableRewind(deps commandDeps, arg string) commandResult {
	parts := strings.Fields(arg)
	if len(parts) == 0 {
		turns, err := deps.listRewindTurns()
		if err != nil {
			return commandResult{output: errorStyle.Render("Nao foi possivel listar turnos: " + err.Error()), handled: true}
		}
		if len(turns) == 0 {
			return commandResult{output: cmdLabelStyle.Render("nenhum turno disponivel"), handled: true}
		}
		var b strings.Builder
		b.WriteString(cmdTitleStyle.Render("Turnos disponiveis para rewind"))
		b.WriteString("\n\n")
		for i := len(turns) - 1; i >= 0 && len(turns)-i <= 10; i-- {
			b.WriteString(fmt.Sprintf("  -%d. %s\n", len(turns)-i, truncate(strings.ReplaceAll(turns[i].preview, "\n", " "), 75)))
		}
		b.WriteString("\n  /rewind <n> restaura arquivos e conversa\n")
		b.WriteString("  /rewind <n> --conversation restaura so a conversa\n")
		b.WriteString("  /rewind <n> --files restaura so arquivos")
		return commandResult{output: b.String(), handled: true}
	}

	n, err := strconv.Atoi(parts[0])
	if err != nil || n <= 0 {
		return commandResult{output: cmdLabelStyle.Render("uso: /rewind <n> [--conversation|--files]"), handled: true}
	}
	withFiles, withConversation := true, true
	if len(parts) > 2 {
		return commandResult{output: cmdLabelStyle.Render("uso: /rewind <n> [--conversation|--files]"), handled: true}
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "--conversation", "--chat":
			withFiles = false
		case "--files":
			withConversation = false
		case "--both":
		default:
			return commandResult{output: cmdLabelStyle.Render("uso: /rewind <n> [--conversation|--files]"), handled: true}
		}
	}
	outcome, err := deps.rewindTurns(n, withFiles, withConversation)
	if err != nil {
		return commandResult{output: errorStyle.Render("Rewind falhou: " + err.Error()), handled: true}
	}
	var b strings.Builder
	b.WriteString(notifySuccessStyle.Render(fmt.Sprintf("rewind aplicado a %d turno(s)", outcome.removed)))
	if withFiles {
		b.WriteString(fmt.Sprintf("\nArquivos restaurados: %d", len(outcome.restored)))
	}
	for _, conflict := range outcome.conflicts {
		b.WriteString("\nConflito registrado; restauracao aplicada: ")
		b.WriteString(conflict)
	}
	for _, warning := range outcome.warnings {
		b.WriteString("\nAviso: ")
		b.WriteString(warning)
	}
	return commandResult{output: b.String(), replaceMessages: outcome.messages, handled: true}
}

// handleRewind rewinds the conversation by N user+assistant pairs.
// With no argument, shows the last 10 message pairs with numbers.
func handleRewind(deps commandDeps, messages []chatEntry, arg string) commandResult {
	if deps.rewindTurns != nil && deps.listRewindTurns != nil {
		return handleDurableRewind(deps, arg)
	}
	// Count only user and assistant messages for pair accounting.
	var pairs [][2]int // user index, assistant index (−1 if no reply yet)
	lastUser := -1
	for i, m := range messages {
		switch m.role {
		case "user":
			if lastUser >= 0 {
				pairs = append(pairs, [2]int{lastUser, -1})
			}
			lastUser = i
		case "assistant":
			if lastUser >= 0 {
				pairs = append(pairs, [2]int{lastUser, i})
				lastUser = -1
			}
		}
	}
	if lastUser >= 0 {
		pairs = append(pairs, [2]int{lastUser, -1})
	}

	// No arg: preview the last 10 pairs.
	if arg == "" {
		if len(pairs) == 0 {
			return commandResult{
				output:  cmdLabelStyle.Render("nenhuma troca de mensagens ainda"),
				handled: true,
			}
		}

		start := 0
		if len(pairs) > 10 {
			start = len(pairs) - 10
		}

		var b strings.Builder
		b.WriteString(cmdTitleStyle.Render("Ultimos turnos"))
		b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d)", len(pairs)-start)))
		b.WriteString("\n\n")

		for i := start; i < len(pairs); i++ {
			num := len(pairs) - i
			p := pairs[i]
			b.WriteString("  ")
			b.WriteString(cmdLabelStyle.Render(fmt.Sprintf("-%d.", num)))
			b.WriteString(" ")
			userMsg := truncate(strings.ReplaceAll(messages[p[0]].content, "\n", " "), 60)
			b.WriteString(cmdValueStyle.Render("> "))
			b.WriteString(cmdPreviewStyle.Render(userMsg))
			b.WriteString("\n")
			if p[1] >= 0 {
				asst := truncate(strings.ReplaceAll(messages[p[1]].content, "\n", " "), 60)
				b.WriteString("      ")
				b.WriteString(cmdBadgeStyle.Render("← "))
				b.WriteString(cmdPreviewStyle.Render(asst))
				b.WriteString("\n")
			}
		}

		b.WriteString("\n")
		b.WriteString(cmdLabelStyle.Render("  use "))
		b.WriteString(cmdValueStyle.Render("/rewind <n>"))
		b.WriteString(cmdLabelStyle.Render(" para remover os ultimos N pares"))
		return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}
	}

	// Parse N.
	n, err := parsePositiveInt(arg)
	if err != nil || n <= 0 {
		return commandResult{
			output: cmdLabelStyle.Render("uso: ") +
				cmdValueStyle.Render("/rewind <n>"),
			handled: true,
		}
	}

	if n > len(pairs) {
		return commandResult{
			output: errorStyle.Render("nao ha ") +
				cmdValueStyle.Render(fmt.Sprintf("%d", n)) +
				errorStyle.Render(" pares para remover (temos ") +
				cmdValueStyle.Render(fmt.Sprintf("%d", len(pairs))) +
				errorStyle.Render(")"),
			handled: true,
		}
	}

	// Determine the first message index that will be cut away.
	cutFrom := pairs[len(pairs)-n][0]
	removedCount := len(messages) - cutFrom
	kept := append([]chatEntry{}, messages[:cutFrom]...)

	// Persist the truncation in the conversation store if wired.
	if deps.truncateMessages != nil {
		if _, terr := deps.truncateMessages(removedCount); terr != nil {
			return commandResult{
				output: errorStyle.Render("aviso: ") +
					cmdLabelStyle.Render(fmt.Sprintf("falha ao persistir rewind: %v", terr)),
				handled:         true,
				replaceMessages: kept,
			}
		}
	}

	var b strings.Builder
	b.WriteString(notifySuccessStyle.Render("rewind aplicado"))
	b.WriteString("\n\n")
	b.WriteString("  ")
	b.WriteString(cmdLabelStyle.Render("pares removidos:     "))
	b.WriteString(cmdValueStyle.Render(fmt.Sprintf("%d", n)))
	b.WriteString("\n  ")
	b.WriteString(cmdLabelStyle.Render("mensagens cortadas:  "))
	b.WriteString(cmdValueStyle.Render(fmt.Sprintf("%d", removedCount)))
	b.WriteString("\n  ")
	b.WriteString(cmdLabelStyle.Render("restantes:           "))
	b.WriteString(cmdValueStyle.Render(fmt.Sprintf("%d", len(kept))))

	return commandResult{
		output:          b.String(),
		handled:         true,
		replaceMessages: kept,
	}
}

// ── /permissions ────────────────────────────────────────────────────────────

// handlePermissions dispatches the /permissions subcommand.
func handlePermissions(parts []string, deps commandDeps) commandResult {
	if deps.permissionsList == nil {
		return commandResult{
			output:  cmdLabelStyle.Render("sistema de permissoes nao disponivel"),
			handled: true,
		}
	}

	sub := "list"
	if len(parts) >= 2 {
		sub = strings.ToLower(parts[1])
	}

	switch sub {
	case "list", "":
		return handlePermissionsList(deps)

	case "allow", "deny", "ask":
		if len(parts) < 3 {
			return commandResult{
				output: cmdLabelStyle.Render("uso: ") +
					cmdValueStyle.Render("/permissions "+sub+" <tool> [pattern]"),
				handled: true,
			}
		}
		pattern := parts[2]
		if len(parts) > 3 {
			pattern = strings.Join(parts[2:], " ")
		}
		if deps.permissionsAdd == nil {
			return commandResult{
				output:  cmdLabelStyle.Render("permissionsAdd nao disponivel"),
				handled: true,
			}
		}
		if err := deps.permissionsAdd(sub, pattern); err != nil {
			return commandResult{
				output:  errorStyle.Render(fmt.Sprintf("erro: %v", err)),
				handled: true,
			}
		}
		return commandResult{
			output: notifySuccessStyle.Render("regra adicionada: ") +
				cmdValueStyle.Render(sub+" "+pattern),
			handled: true,
		}

	case "remove", "rm", "del":
		if len(parts) < 3 {
			return commandResult{
				output: cmdLabelStyle.Render("uso: ") +
					cmdValueStyle.Render("/permissions remove <tool>"),
				handled: true,
			}
		}
		if deps.permissionsRemove == nil {
			return commandResult{
				output:  cmdLabelStyle.Render("permissionsRemove nao disponivel"),
				handled: true,
			}
		}
		removed, err := deps.permissionsRemove(parts[2])
		if err != nil {
			return commandResult{
				output:  errorStyle.Render(fmt.Sprintf("erro: %v", err)),
				handled: true,
			}
		}
		return commandResult{
			output: notifySuccessStyle.Render(fmt.Sprintf("regras removidas: %d ", removed)) +
				cmdLabelStyle.Render("para ") +
				cmdValueStyle.Render(parts[2]),
			handled: true,
		}

	case "reset":
		if deps.permissionsReset == nil {
			return commandResult{
				output:  cmdLabelStyle.Render("permissionsReset nao disponivel"),
				handled: true,
			}
		}
		if err := deps.permissionsReset(); err != nil {
			return commandResult{
				output:  errorStyle.Render(fmt.Sprintf("erro: %v", err)),
				handled: true,
			}
		}
		return commandResult{
			output:  notifySuccessStyle.Render("permissoes resetadas ao padrao"),
			handled: true,
		}

	default:
		return commandResult{
			output: cmdLabelStyle.Render("uso: ") +
				cmdValueStyle.Render("/permissions [list|allow|deny|ask|remove|reset]"),
			handled: true,
		}
	}
}

func handlePermissionsList(deps commandDeps) commandResult {
	rules := deps.permissionsList()

	allow := map[string]string{}
	deny := map[string]string{}
	ask := map[string]string{}

	for _, r := range rules {
		pattern, decision, source := r[0], r[1], r[2]
		label := pattern
		if source != "" {
			label = pattern + "  [" + source + "]"
		}
		switch decision {
		case "allow":
			allow[pattern] = label
		case "deny":
			deny[pattern] = label
		case "ask":
			ask[pattern] = label
		}
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Permissoes"))
	b.WriteString("\n\n")

	writeSection := func(title string, section map[string]string) {
		b.WriteString("  ")
		b.WriteString(cmdTopicStyle.Render(title + ":"))
		b.WriteString("\n")
		if len(section) == 0 {
			b.WriteString("    ")
			b.WriteString(cmdLabelStyle.Render("(nenhum)"))
			b.WriteString("\n")
			return
		}
		keys := make([]string, 0, len(section))
		for k := range section {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString("    ")
			b.WriteString(cmdBadgeStyle.Render("- "))
			b.WriteString(cmdValueStyle.Render(section[k]))
			b.WriteString("\n")
		}
	}

	writeSection("ALLOW", allow)
	b.WriteString("\n")
	writeSection("ASK", ask)
	b.WriteString("\n")
	writeSection("DENY", deny)

	b.WriteString("\n")
	b.WriteString(cmdLabelStyle.Render("  uso: "))
	b.WriteString(cmdValueStyle.Render("/permissions allow|deny|ask <tool>"))

	return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}
}

// ── /plan ───────────────────────────────────────────────────────────────────

// handlePlan toggles and inspects plan mode.
func handlePlan(deps commandDeps, sub string) commandResult {
	if deps.planCurrent == nil {
		return commandResult{
			output:  cmdLabelStyle.Render("modo de planejamento indisponível"),
			handled: true,
		}
	}

	switch sub {
	case "", "status":
		return handlePlanStatus(deps)

	case "on":
		if deps.planSet == nil {
			return commandResult{
				output:  cmdLabelStyle.Render("planSet nao disponivel"),
				handled: true,
			}
		}
		desc, err := deps.planSet("plan")
		if err != nil {
			return commandResult{
				output:  errorStyle.Render(fmt.Sprintf("erro: %v", err)),
				handled: true,
			}
		}
		return commandResult{
			output: notifySuccessStyle.Render("modo de planejamento: ATIVADO") + "\n  " +
				cmdLabelStyle.Render(stripMarkdown(desc)),
			handled: true,
		}

	case "off":
		if deps.planSet == nil {
			return commandResult{
				output:  cmdLabelStyle.Render("planSet nao disponivel"),
				handled: true,
			}
		}
		desc, err := deps.planSet("edit")
		if err != nil {
			return commandResult{
				output:  errorStyle.Render(fmt.Sprintf("erro: %v", err)),
				handled: true,
			}
		}
		return commandResult{
			output: notifySuccessStyle.Render("modo de planejamento: DESATIVADO") + "\n  " +
				cmdLabelStyle.Render(stripMarkdown(desc)),
			handled: true,
		}

	case "show":
		if deps.planSummary == nil {
			return commandResult{
				output:  cmdLabelStyle.Render("planSummary nao disponivel"),
				handled: true,
			}
		}
		summary := deps.planSummary()
		if strings.TrimSpace(summary) == "" {
			summary = "nenhuma operacao simulada ainda"
		}
		var b strings.Builder
		b.WriteString(cmdTitleStyle.Render("Plano atual"))
		b.WriteString("\n\n")
		for _, line := range strings.Split(summary, "\n") {
			b.WriteString("  ")
			b.WriteString(cmdPreviewStyle.Render(line))
			b.WriteString("\n")
		}
		return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}

	case "execute", "exec", "run":
		if deps.planExecute == nil {
			return commandResult{
				output:  cmdLabelStyle.Render("planExecute nao disponivel"),
				handled: true,
			}
		}
		summary, err := deps.planExecute()
		if err != nil {
			return commandResult{
				output:  errorStyle.Render(fmt.Sprintf("erro: %v", err)),
				handled: true,
			}
		}
		var b strings.Builder
		b.WriteString(notifySuccessStyle.Render("modo de planejamento: executando"))
		b.WriteString("\n\n")
		if strings.TrimSpace(summary) != "" {
			for _, line := range strings.Split(summary, "\n") {
				b.WriteString("  ")
				b.WriteString(cmdPreviewStyle.Render(line))
				b.WriteString("\n")
			}
		} else {
			b.WriteString("  ")
			b.WriteString(cmdLabelStyle.Render("(nada a executar)"))
		}
		return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}

	default:
		return commandResult{
			output: cmdLabelStyle.Render("uso: ") +
				cmdValueStyle.Render("/plan [on|off|show|execute]"),
			handled: true,
		}
	}
}

func handlePlanStatus(deps commandDeps) commandResult {
	current := deps.planCurrent()

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Plan mode"))
	b.WriteString("\n\n")
	b.WriteString("  ")
	b.WriteString(cmdLabelStyle.Render("modo atual:  "))
	if current == "plan" {
		b.WriteString(cmdActiveStyle.Render("● plan"))
	} else {
		b.WriteString(cmdInactiveStyle.Render("○ " + current))
	}
	b.WriteString("\n")

	if deps.planSummary != nil {
		summary := strings.TrimSpace(deps.planSummary())
		if summary != "" {
			b.WriteString("\n  ")
			b.WriteString(cmdTopicStyle.Render("Operacoes simuladas:"))
			b.WriteString("\n")
			for _, line := range strings.Split(summary, "\n") {
				b.WriteString("    ")
				b.WriteString(cmdPreviewStyle.Render(line))
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("\n  ")
	b.WriteString(cmdLabelStyle.Render("uso: "))
	b.WriteString(cmdValueStyle.Render("/plan [on|off|show|execute]"))

	return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}
}

// ── /config ─────────────────────────────────────────────────────────────────

// handleConfig implements the /config command. It supports:
//
//	/config              -> open the interactive form overlay
//	/config get <key>    -> print the current value of a dotted-path key
//	/config set <k> <v>  -> update a value in memory (does not persist)
//	/config save         -> persist current in-memory config to disk
//	/config reload       -> reread the config from disk
func handleConfig(parts []string, rawInput string, deps commandDeps) commandResult {
	if len(parts) == 1 {
		// Bare /config: open the interactive form. The host model
		// instantiates the overlay using the live config snapshot.
		return commandResult{handled: true, openConfigForm: true}
	}

	sub := strings.ToLower(parts[1])
	switch sub {
	case "get":
		if len(parts) < 3 {
			return commandResult{
				output:  cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/config get <chave>"),
				handled: true,
			}
		}
		key := parts[2]
		if deps.configGet == nil {
			return commandResult{
				output:  cmdLabelStyle.Render("acesso a config nao disponivel"),
				handled: true,
			}
		}
		val, ok := deps.configGet(key)
		if !ok {
			return commandResult{
				output:  errorStyle.Render("chave desconhecida: ") + cmdValueStyle.Render(key),
				handled: true,
			}
		}
		var b strings.Builder
		b.WriteString(cmdLabelStyle.Render(key + " = "))
		if val == "" {
			b.WriteString(cmdInactiveStyle.Render("(vazio)"))
		} else {
			b.WriteString(cmdValueStyle.Render(val))
		}
		return commandResult{output: b.String(), handled: true}

	case "set":
		if len(parts) < 4 {
			return commandResult{
				output:  cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/config set <chave> <valor>"),
				handled: true,
			}
		}
		if deps.configSet == nil {
			return commandResult{
				output:  cmdLabelStyle.Render("escrita de config nao disponivel"),
				handled: true,
			}
		}
		key := parts[2]
		// Preserve the rest of the original line as the value to allow
		// values containing spaces (e.g. file paths).
		value := strings.TrimSpace(strings.TrimPrefix(rawInput,
			parts[0]+" "+parts[1]+" "+parts[2]))
		if err := deps.configSet(key, value); err != nil {
			return commandResult{
				output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
				handled: true,
			}
		}
		var b strings.Builder
		b.WriteString(notifySuccessStyle.Render("ok "))
		b.WriteString(cmdLabelStyle.Render(key + " = "))
		b.WriteString(cmdValueStyle.Render(value))
		b.WriteString(cmdLabelStyle.Render("  (use "))
		b.WriteString(cmdValueStyle.Render("/config save"))
		b.WriteString(cmdLabelStyle.Render(" para persistir)"))
		return commandResult{output: b.String(), handled: true}

	case "save":
		if deps.configSave == nil {
			return commandResult{
				output:  cmdLabelStyle.Render("salvar config nao disponivel"),
				handled: true,
			}
		}
		if err := deps.configSave(); err != nil {
			return commandResult{
				output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
				handled: true,
			}
		}
		path := ""
		if deps.configPath != nil {
			path = deps.configPath()
		}
		return commandResult{
			output: notifySuccessStyle.Render("config salva ") +
				cmdLabelStyle.Render(path),
			handled: true,
		}

	case "reload":
		if deps.configReload == nil {
			return commandResult{
				output:  cmdLabelStyle.Render("reload de config nao disponivel"),
				handled: true,
			}
		}
		if err := deps.configReload(); err != nil {
			return commandResult{
				output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
				handled: true,
			}
		}
		return commandResult{
			output:  notifySuccessStyle.Render("config recarregada do disco"),
			handled: true,
		}
	}

	return commandResult{
		output: cmdLabelStyle.Render("uso: ") +
			cmdValueStyle.Render("/config [get|set|save|reload] [...]"),
		handled: true,
	}
}

func handleAuthCommand(parts []string) commandResult {
	mgr := accountauth.NewManager(nil)
	if len(parts) == 1 || strings.EqualFold(parts[1], "status") || strings.EqualFold(parts[1], "list") {
		return commandResult{output: strings.TrimRight(accountauth.FormatOverview(mgr), "\n"), handled: true}
	}

	sub := strings.ToLower(parts[1])
	switch sub {
	case "accounts":
		if len(parts) < 3 {
			return commandResult{output: strings.TrimRight(accountauth.FormatOverview(mgr), "\n"), handled: true}
		}
		var sb strings.Builder
		if err := accountauth.RunCLI(context.Background(), mgr, []string{parts[2], "accounts"}, &sb, &sb); err != nil {
			return commandResult{output: errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()), handled: true}
		}
		return commandResult{output: strings.TrimRight(sb.String(), "\n"), handled: true}

	case "use", "switch":
		if len(parts) < 4 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/auth use <provedor> <conta>"), handled: true}
		}
		var sb strings.Builder
		if err := accountauth.RunCLI(context.Background(), mgr, []string{parts[2], "use", parts[3]}, &sb, &sb); err != nil {
			return commandResult{output: errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()), handled: true}
		}
		return commandResult{output: notifySuccessStyle.Render(strings.TrimSpace(sb.String())), handled: true}

	case "logout":
		if len(parts) < 3 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/auth logout <provedor> [conta|--all]"), handled: true}
		}
		cliArgs := []string{parts[2], "logout"}
		if len(parts) >= 4 {
			cliArgs = append(cliArgs, parts[3])
		}
		var sb strings.Builder
		if err := accountauth.RunCLI(context.Background(), mgr, cliArgs, &sb, &sb); err != nil {
			return commandResult{output: errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()), handled: true}
		}
		return commandResult{output: notifySuccessStyle.Render(strings.TrimSpace(sb.String())), handled: true}

	case "login":
		prov := "<provedor>"
		if len(parts) >= 3 {
			prov = strings.ToLower(parts[2])
		}
		return commandResult{
			output: fmt.Sprintf("Para conectar uma nova conta OAuth, use /connect (Ctrl+O no provedor) ou execute no terminal:\n  polypod auth %s login --account <apelido>", prov),
			handled: true,
		}
	}

	// Support `/auth <provedor> [status|accounts|use|logout ...]` mirroring the CLI syntax!
	if accountauth.IsOAuthProvider(sub) {
		if len(parts) >= 3 && strings.EqualFold(parts[2], "login") {
			return commandResult{
				output: fmt.Sprintf("Use /connect (Ctrl+O) ou execute no terminal:\n  polypod auth %s login --account <apelido>", sub),
				handled: true,
			}
		}
		var sb strings.Builder
		if err := accountauth.RunCLI(context.Background(), mgr, parts[1:], &sb, &sb); err != nil {
			return commandResult{output: errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()), handled: true}
		}
		return commandResult{output: strings.TrimRight(sb.String(), "\n"), handled: true}
	}

	return commandResult{
		output: cmdLabelStyle.Render("uso: ") +
			cmdValueStyle.Render("/auth [status | accounts <provedor> | use <provedor> <conta> | logout <provedor> [conta|--all]]"),
		handled: true,
	}
}

func handleArchitectCmd(input string, parts []string) commandResult {
	if len(parts) < 2 {
		return commandResult{
			output: cmdLabelStyle.Render("uso: ") +
				cmdValueStyle.Render("/architect <descrição da arquitetura, refatoração ou correção complexa>"),
			handled: true,
		}
	}
	task := strings.TrimSpace(strings.TrimPrefix(input, parts[0]))
	prompt := fmt.Sprintf("Execute o fluxo Architect → Editor para a tarefa abaixo:\n1. Inspecione os arquivos e o raio de impacto (`code_impact` quando aplicável).\n2. Chame `architect_plan` com plan_only=true para formular o plano cirúrgico de engenharia.\n3. Aplique as edições exatas e valide compilação/testes antes de concluir.\n\nTAREFA: %s", task)
	return commandResult{modelInput: prompt, handled: false}
}

func handleBestOfCmd(input string, parts []string) commandResult {
	if len(parts) < 2 {
		return commandResult{
			output: cmdLabelStyle.Render("uso: ") +
				cmdValueStyle.Render("/bestof <tarefa difícil para resolver e verificar em worktrees paralelas>"),
			handled: true,
		}
	}
	task := strings.TrimSpace(strings.TrimPrefix(input, parts[0]))
	prompt := fmt.Sprintf("Use a ferramenta `parallel_best_of_n` (candidates=2, apply_winner=true) para resolver e verificar em git worktrees paralelas isoladas a seguinte tarefa, e apresente o resumo comparativo e o resultado verificado:\n\n%s", task)
	return commandResult{modelInput: prompt, handled: false}
}

func handleVisionModelCmd(args []string, deps commandDeps) commandResult {
	if len(args) == 0 {
		if deps.visionConfig == nil {
			return commandResult{output: "Roteador multimodal (/model --vision) indisponível.", handled: true}
		}
		expProv, expModel, autoModel := deps.visionConfig()
		if expModel != "" {
			provLabel := expProv
			if provLabel == "" && deps.currentProvider != nil {
				provLabel = deps.currentProvider()
			}
			return commandResult{
				output: fmt.Sprintf("Modelo de visão explícito: %s/%s  (use `/model --vision auto` para voltar ao modo automático)", provLabel, expModel),
				handled: true,
			}
		}
		if autoModel == "" {
			autoModel = "(modelo ativo já suporta visão ou nenhum fallback multimodal disponível)"
		}
		return commandResult{
			output: fmt.Sprintf("Modo de visão: automático · fallback multimodal: %s\nUso: /model --vision [auto | <modelo> | <provedor> <modelo>]", autoModel),
			handled: true,
		}
	}
	if deps.setVisionModel == nil {
		return commandResult{output: "Configuração de modelo de visão indisponível.", handled: true}
	}
	if len(args) == 1 {
		target := strings.TrimSpace(args[0])
		if strings.EqualFold(target, "auto") || strings.EqualFold(target, "clear") || strings.EqualFold(target, "reset") {
			if err := deps.setVisionModel("", "auto"); err != nil {
				return commandResult{output: "Erro limpando override de visão: " + err.Error(), handled: true}
			}
			return commandResult{output: "Roteamento de visão restaurado para modo automático.", handled: true}
		}
		// Support `<provedor>/<modelo>` or `<modelo>` on active provider
		prov := ""
		mod := target
		if p, m, ok := strings.Cut(target, "/"); ok && accountauth.IsOAuthProvider(strings.ToLower(p)) {
			prov = strings.ToLower(p)
			mod = m
		}
		if err := deps.setVisionModel(prov, mod); err != nil {
			return commandResult{output: "Erro configurando modelo de visão: " + err.Error(), handled: true}
		}
		return commandResult{output: fmt.Sprintf("Modelo multimodal de visão configurado: %s", target), handled: true}
	}
	prov := strings.ToLower(strings.TrimSpace(args[0]))
	mod := strings.TrimSpace(strings.Join(args[1:], " "))
	if err := deps.setVisionModel(prov, mod); err != nil {
		return commandResult{output: "Erro configurando modelo de visão: " + err.Error(), handled: true}
	}
	return commandResult{output: fmt.Sprintf("Modelo multimodal de visão configurado: %s/%s", prov, mod), handled: true}
}

func handleAnswerChildQuestion(input string, parts []string, deps commandDeps) commandResult {
	if len(parts) < 3 {
		return commandResult{
			output: "Uso: /answer <id> <resposta>  (ou /answer draft <id> <rascunho>)",
			handled: true,
		}
	}
	if strings.EqualFold(parts[1], "draft") {
		if len(parts) < 4 || deps.saveChildDraft == nil {
			return commandResult{output: "Uso: /answer draft <id> <rascunho>", handled: true}
		}
		id := parts[2]
		draft := strings.TrimSpace(strings.TrimPrefix(input, parts[0]+" "+parts[1]+" "+parts[2]))
		if err := deps.saveChildDraft(id, draft); err != nil {
			return commandResult{output: "Erro salvando rascunho: " + err.Error(), handled: true}
		}
		return commandResult{output: fmt.Sprintf("Rascunho salvo para a pergunta [%s].", id), handled: true}
	}
	if deps.answerChildQuestion == nil {
		return commandResult{output: "Fila concorrente de subagentes indisponível.", handled: true}
	}
	id := parts[1]
	rest := strings.TrimSpace(strings.TrimPrefix(input, parts[0]))
	ans := strings.TrimSpace(strings.TrimPrefix(rest, id))
	if err := deps.answerChildQuestion(id, ans); err != nil {
		return commandResult{output: "Erro respondendo subagente: " + err.Error(), handled: true}
	}
	return commandResult{output: fmt.Sprintf("Resposta entregue à sub-tarefa [%s].", id), handled: true}
}
