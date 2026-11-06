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
	{"pr_comments", []string{"pr-comments"}, "Ler e tratar comentarios de pull request", "Localize o pull request indicado, leia todos os comentarios e reviews, agrupe as solicitacoes e aplique as correcoes pertinentes."},
	{"autofix-pr", nil, "Corrigir automaticamente um pull request", "Analise o pull request indicado, reproduza falhas, aplique correcoes, execute validacoes e deixe a branch pronta para revisao."},
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
	{"task-status", nil, "Consultar uma tarefa", "Localize a tarefa indicada e apresente estado, progresso, bloqueios, processos e artefatos relacionados."},
	{"task-logs", nil, "Ler logs de uma tarefa", "Localize a tarefa indicada, colete seus logs relevantes e diagnostique erros ou falta de progresso."},
	{"task-restart", nil, "Reiniciar uma tarefa", "Reinicie a tarefa indicada preservando seu contexto, acompanhe a retomada e valide que voltou a progredir."},
	{"schedule", nil, "Criar ou gerenciar um agendamento", "Crie, altere, liste ou remova o agendamento solicitado usando o scheduler disponivel e valide a expressao e o proximo disparo."},
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
				return fmt.Sprintf("[COMANDO AUTONOMO /%s]\n%s\n\nSolicitacao: %s\nDiretorio de trabalho: %s\nExecute as ferramentas necessarias, valide o resultado e reporte objetivamente.", spec.Name, spec.Instruction, request, ctx.WorkingDir), nil
			},
		})
	}
}
