package commands

import (
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/command"
)

// autonomousCommandSpec describes a slash command implemented by delegating
// the requested outcome to the unrestricted agent. This keeps commands useful
// while allowing the agent to choose the best available tools at runtime.
type autonomousCommandSpec struct {
	Name        string
	Aliases     []string
	Description string
	Instruction string
}

var autonomousCommandSpecs = []autonomousCommandSpec{
	{"oauth-refresh", nil, "Renovar a autenticacao OAuth do provider", "Renove a autenticacao OAuth do provider solicitado, valide o novo token e persista-o no armazenamento seguro configurado."},
	{"context-summary", nil, "Resumir o contexto e estado atual", "Analise a conversa e o workspace atuais e produza um resumo operacional com decisoes, arquivos alterados, pendencias e proximos passos."},
	{"extra-usage", nil, "Analisar indicadores detalhados de uso", "Inspecione os dados locais de uso, tokens e custos e apresente os indicadores detalhados disponiveis."},
	{"break-cache", nil, "Limpar caches relevantes", "Identifique e limpe com seguranca os caches relevantes para o problema descrito, depois valide que foram recriados corretamente."},
	{"mock-limits", nil, "Ativar limites simulados", "Ative limites simulados para o provider ou subsistema solicitado e valide o comportamento de rate limiting."},
	{"reset-limits", nil, "Limpar contadores de limites", "Localize e zere os contadores locais de rate limit, preservando configuracoes permanentes, e confirme o novo estado."},
	{"autofix-pr", nil, "Corrigir automaticamente um pull request", "Use o relatorio paginado de comentarios e revisoes do PR fornecido no contexto (consultando gh/API apenas se o relatorio nao estiver presente), foque nos comentarios marcados como [no diff atual], reproduza a falha pontualmente, aplique correcoes usando exclusivamente as ferramentas nativas de edicao de arquivo (edit_file/write_file/multi_edit, sem editar via bash e sem fazer git commit ou git push automatico) e execute run_project_checks. So declare a correcao verificada quando os verificadores passarem; cite os resultados e deixe a worktree pronta para revisao."},
	{"secret-scan", nil, "Executar varredura real de segredos", "Execute uma varredura de segredos no caminho informado, sem imprimir valores sensiveis. Classifique os achados e corrija exposicoes locais quando seguro."},
	{"vuln-check", nil, "Verificar vulnerabilidades", "Audite dependencias e codigo do caminho informado em busca de vulnerabilidades, valide falsos positivos e aplique correcoes compativeis."},
	{"model-compare", nil, "Comparar modelos lado a lado", "Execute a mesma tarefa nos modelos solicitados, compare qualidade, latencia, custo e uso de ferramentas, e recomende o melhor para este projeto."},
	{"provider-switch", nil, "Trocar o provider ativo", "Troque o provider e o modelo ativos para os valores solicitados, atualize a configuracao persistente e valide uma chamada curta."},
	{"plugin-marketplace", nil, "Pesquisar e instalar plugins", "Pesquise plugins adequados ao objetivo informado, verifique procedencia e compatibilidade e instale a melhor opcao."},
	{"plugin-install", nil, "Instalar um plugin", "Instale e configure o plugin solicitado, resolva dependencias e valide que suas ferramentas ficaram disponiveis."},
	{"plugin-update", nil, "Atualizar plugins", "Atualize os plugins solicitados, preserve configuracoes e valide compatibilidade apos a atualizacao."},
	{"plugin-remove", nil, "Remover um plugin", "Remova o plugin solicitado e seus artefatos exclusivos, preservando dados compartilhados e confirmando que o runtime continua valido."},
	{"reload", nil, "Recarregar configuracao e componentes", "Recarregue a configuracao e os componentes dinamicos do Poly, confirme os valores ativos e relate qualquer item que exija reinicio."},
	{"ide", nil, "Integrar com a IDE", "Detecte a IDE disponivel, configure a integracao solicitada e abra ou conecte o workspace atual."},
	{"chrome", nil, "Integrar com o Chrome", "Configure ou use a integracao de navegador disponivel para realizar a tarefa solicitada no Chrome."},
	{"desktop", nil, "Executar uma tarefa no desktop", "Use as ferramentas de desktop disponiveis para realizar integralmente a tarefa solicitada."},
	{"mobile", nil, "Executar uma tarefa em dispositivo movel", "Detecte dispositivos moveis conectados e realize a tarefa solicitada usando a interface apropriada."},
	{"mcp-refresh", nil, "Atualizar servidores e ferramentas MCP", "Recarregue a configuracao MCP, reconecte os servidores necessarios e atualize a lista de ferramentas."},
	{"mcp-token", nil, "Renovar credencial MCP", "Renove ou configure a credencial do servidor MCP indicado usando armazenamento seguro e valide a conexao sem expor o segredo."},
	{"mcp-debug", nil, "Diagnosticar MCP", "Diagnostique configuracao, transporte, autenticacao e ferramentas MCP; corrija o que estiver ao alcance e valide novamente."},
	{"mcp-reconnect", nil, "Reconectar um servidor MCP", "Desconecte e reconecte o servidor MCP indicado, valide o handshake e liste as ferramentas recuperadas."},
	{"remote-trigger", nil, "Disparar uma automacao remota", "Localize a automacao remota indicada, execute-a, acompanhe o resultado e trate falhas recuperaveis."},
	{"ant-trace", nil, "Capturar e analisar runtime trace", "Capture um runtime trace representativo, analise gargalos de CPU, bloqueios e goroutines e proponha ou aplique correcoes."},
	{"remote-env", nil, "Sincronizar ambiente remoto", "Sincronize as variaveis e configuracoes de ambiente necessarias com o destino remoto, sem revelar segredos, e valide a sessao."},
	{"remote-setup", nil, "Preparar ambiente remoto", "Prepare integralmente o ambiente remoto solicitado, instalando dependencias, configurando acesso e executando verificacoes de saude."},
	{"testgen", nil, "Gerar testes reais", "Gere testes completos para o alvo informado, cobrindo caminhos felizes, erros e casos extremos, e execute-os ate passarem."},
	{"advisor", nil, "Obter aconselhamento tecnico autonomo", "Atue como consultor senior: investigue o problema informado, compare alternativas e execute a melhor solucao quando houver uma acao clara."},
	{"bughunter", nil, "Encontrar e corrigir bugs", "Investigue o alvo informado, reproduza bugs, identifique causas raiz, implemente correcoes e execute testes de regressao."},
}

// RegisterAutonomousCommands replaces incomplete local placeholders with
// prompt commands that are executed by the fully tooled autonomous agent.
func RegisterAutonomousCommands(reg *command.Registry) {
	for _, spec := range autonomousCommandSpecs {
		spec := spec
		reg.Register(&command.Command{
			Name:        spec.Name,
			Aliases:     spec.Aliases,
			Description: spec.Description,
			Type:        command.CommandPrompt,
			Source:      command.SourceDynamic,
			GetPrompt: func(args []string, ctx command.CommandContext) (string, error) {
				request := strings.TrimSpace(strings.Join(args, " "))
				if request == "" {
					request = "Use o contexto atual para determinar o alvo e conclua a operacao integralmente."
				}
				extraContext := ""
				if spec.Name == "autofix-pr" {
					if prReport, prErr := FetchPRReport(ctx.WorkingDir, args); prErr == nil && strings.TrimSpace(prReport) != "" {
						extraContext = "\n\nContexto paginado do PR (/pr_comments):\n" + prReport
					}
				}
				return fmt.Sprintf("[COMANDO AUTONOMO /%s]\n%s\n\nSolicitacao: %s\nDiretorio de trabalho: %s%s\nExecute as ferramentas necessarias, valide o resultado e reporte objetivamente.", spec.Name, spec.Instruction, request, ctx.WorkingDir, extraContext), nil
			},
		})
	}
}
