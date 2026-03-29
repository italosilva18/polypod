package cli

import "strings"

func helpText() string {
	var b strings.Builder

	b.WriteString(cmdTitleStyle.Render("Comandos"))
	b.WriteString("\n\n")

	commands := []struct {
		cmd  string
		desc string
	}{
		{"/help", "lista comandos"},
		{"/clear", "limpa tela e historico da sessao"},
		{"/agents", "lista agentes disponiveis"},
		{"/agent switch <nome>", "troca de agente ativo"},
		{"/skills", "lista skills do agente atual"},
		{"/memory list", "lista memorias salvas"},
		{"/memory search <q>", "busca memorias"},
		{"/model", "mostra agente/modelo atual"},
		{"/session", "info da sessao"},
		{"/copy", "copia ultima resposta pro clipboard"},
		{"/run <cmd>  ou  !<cmd>", "executa comando shell inline"},
		{"/file <path>", "mostra conteudo de arquivo"},
		{"/search <pattern>", "busca texto no projeto (grep)"},
		{"/git [status|log|diff|branch]", "operacoes git comuns"},
		{"/project", "mostra arvore do projeto"},
		{"/export [file]", "exporta conversa pra markdown"},
		{"/context", "mostra info do contexto atual"},
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
