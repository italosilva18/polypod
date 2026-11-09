# Leitura detalhada da referência `src_extracted`

**Data:** 9 de novembro de 2026. Este registro complementa o [inventário da árvore](src-extracted-tree-audit.md). A árvore externa tem 1.902 arquivos únicos e 514.587 linhas físicas; `src_extracted/src/src` é uma cópia idêntica e não é contada novamente.

## Cobertura linha a linha desta rodada

Foram examinados integralmente os fluxos de **27 arquivos**, somando **12.023 linhas examinadas** das 12.025 linhas físicas desses arquivos. As duas linhas restantes são mapas de origem gerados no fim de dois componentes. Isso representa cerca de 2,3% das linhas únicas da árvore. Os outros arquivos continuam inventariados e foram consultados por busca ou leitura dirigida; esta rodada não os declara lidos integralmente.

| Área | Arquivos | Linhas examinadas | Arquivos lidos integralmente |
| --- | ---: | ---: | --- |
| Utilitários de rotinas, fila, sessões e recuperação | 9 | 2.828 | `utils/cronScheduler.ts`, `utils/cronTasks.ts`, `utils/cronTasksLock.ts`, `utils/concurrentSessions.ts`, `utils/messageQueueManager.ts`, `utils/conversationRecovery.ts`, `utils/backgroundHousekeeping.ts`, `utils/queueProcessor.ts`, `utils/mailbox.ts` |
| Tarefas planejadas e equipes | 7 | 2.029 | `tools/TaskCreateTool/TaskCreateTool.ts`, `tools/TaskUpdateTool/TaskUpdateTool.ts`, `tools/TaskGetTool/TaskGetTool.ts`, `tools/TaskListTool/TaskListTool.ts`, `tools/TeamCreateTool/TeamCreateTool.ts`, `tools/TeamDeleteTool/TeamDeleteTool.ts`, `utils/tasks.ts` |
| Streaming, sessão remota, plugins e skills | 6 | 3.639 | `services/tools/StreamingToolExecutor.ts`, `services/tools/toolOrchestration.ts`, `remote/RemoteSessionManager.ts`, `remote/SessionsWebSocket.ts`, `services/plugins/pluginOperations.ts`, `skills/loadSkillsDir.ts` |
| Scroll, segundo plano, voz e seleção de mensagem | 5 | 3.527 | `hooks/useVirtualScroll.ts`, `hooks/useSessionBackgrounding.ts`, `hooks/useVoice.ts`, `components/MessageSelector.tsx`, `hooks/useVoiceIntegration.tsx` |
| **Total** | **27** | **12.023** | |

## Continuação da leitura

Nesta etapa foram lidos integralmente mais dois arquivos da árvore única: `tasks/LocalMainSessionTask.ts` (479 linhas) e `tools/SendMessageTool/SendMessageTool.ts` (917 linhas). A cobertura acumulada passa a **29 arquivos e 13.419 linhas examinadas**, cerca de 2,6% das 514.587 linhas únicas. Os demais arquivos continuam sem declaração de leitura integral.

O primeiro arquivo separa a consulta em segundo plano da conversa principal: mantém controle de cancelamento, transcript próprio, progresso e notificação de conclusão. Essa separação explica por que simplesmente ocultar o turno ativo do CLI do Polypod não liberaria um novo executor. O segundo arquivo entrega mensagens a colegas por caixa de entrada, mantém a resposta associada ao pedido e diferencia entrega local de transporte remoto. A implementação Go deve ligar apenas destinos reais e não tratar envio de mensagem como operação de leitura, embora a referência classifique mensagens de texto dessa forma.

## Aplicações imediatas concluídas em Go

- `internal/subagent/runner.go`: `agent_run` anuncia os agentes que o registro realmente carregou, inclusive os personalizados, e recusa nomes inexistentes antes de iniciar uma tarefa.
- `internal/skill/plugin_loader.go`: skills `.yaml`/`.yml` passam por YAML verdadeiro; arquivos JSON existentes continuam aceitos. Erros de análise não reproduzem o conteúdo do arquivo.
- `internal/adapter/cli/markdown.go`: renderizações Markdown já concluídas são reutilizadas em redraws do terminal, com limite de entradas e bytes e invalidação por largura.

O executável inteiro compilou com `CGO_ENABLED=0 go build`. Os testes de comportamento continuam adiados conforme pedido do usuário; compilação não comprova melhoria de latência nem todos os fluxos interativos.

## Estado das aplicações por valor

1. **Plano de tarefas durável e coerente — implementado.** `plan_create`, `plan_update`, `plan_get`, `plan_list` e `plan_claim` usam SQLite com dono, estado, dependências, detecção de ciclos e claim atômico. `task_create` continua sendo o executor de tarefas background; `task_update` agora altera a descrição de uma tarefa real e rejeita ID inexistente. O plano não inicia um agente sozinho. Evidências: `utils/tasks.ts:541`, `tools/TaskUpdateTool/TaskUpdateTool.ts:129`, `internal/taskplan/store.go`, `internal/tool/taskplan.go`.
2. **Recuperação de turnos interrompidos — implementada no CLI.** A retomada examina a última entrada e o journal; se houver resposta pendente ou chamada sem término registrado, mostra aviso e oferece `/recover`, que preenche apenas um rascunho editável. Sessões legadas sem ID de turno não permitem correlacionar as chamadas. Evidências: `utils/conversationRecovery.ts:159`, `internal/session/recovery.go`, `internal/adapter/cli/recovery.go`.
3. **Rotinas entre processos — pendente.** O salvamento agora usa um arquivo temporário exclusivo por processo, evitando colisão direta entre gravações. Ainda falta eleger o dono do agendador e sincronizar mutações: múltiplas instâncias podem perder atualizações ou executar a mesma rotina. Evidências: `utils/cronTasksLock.ts:111`, `utils/cronScheduler.ts:388`, `internal/scheduler/scheduler.go:361`, `main.go:2025`.
4. **Ferramentas durante o stream — implementadas para buscas locais na Responses API.** Chamadas concluídas de busca podem começar antes da resposta final, preservando ordem e sem duplicar a execução; o fallback de provedor permanece ativo. `read_file` fica fora por registrar versões internas, e chamadas sem texto prévio podem ficar bufferizadas até o fim. Evidências: `services/tools/StreamingToolExecutor.ts:73`, `internal/ai/early_response_tools.go`, `internal/provider/failover.go`.
5. **Sessões remotas e plugins completos.** O transporte remoto Go ainda não está ligado ao agente principal. O carregador de plugins agora aceita YAML, mas não instala, atualiza nem remove pacotes, e descarta metadados de skills por caminho e argumentos. Implementar protocolo remoto e ciclo de plugins próprios; endpoints privados da referência não são reutilizáveis. Evidências: `remote/SessionsWebSocket.ts:15`, `services/plugins/pluginOperations.ts:321`, `skills/loadSkillsDir.ts:853`, `internal/remote/session.go:1`, `internal/skill/plugin_loader.go:35`.
6. **CLI longo e voz contínua.** O cache Markdown melhora redraws repetidos, mas ainda falta virtualização por mensagem. O ditado atual usa duração fixa; gravação contínua e transcrição parcial precisam de backend local ou serviço sob controle do Polypod. Evidências: `hooks/useVirtualScroll.ts:303`, `hooks/useVoice.ts:373`, `internal/adapter/cli/model.go:1397`, `internal/adapter/cli/voice_input.go:16`.

## Partes que não convém transpor literalmente

O prazo automático de sete dias para rotinas recorrentes em `utils/cronTasks.ts:315` conflita com a operação contínua desejada para o Polypod. A busca semântica de sessões em `utils/agenticSessionSearch.ts:146` pode enviar até 100 trechos de transcript por consulta ao modelo; uma versão do Polypod deve limitar custo e preservar busca textual rápida. `utils/tasks.ts:458` grava os dois lados de uma dependência separadamente e não impede ciclos; o novo store deve usar transação e validar o grafo. Integrações CCR, OAuth e voz da referência dependem de serviços privados e requerem alternativas próprias.
