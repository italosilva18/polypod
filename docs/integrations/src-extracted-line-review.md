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

## Aplicações imediatas concluídas em Go

- `internal/subagent/runner.go`: `agent_run` anuncia os agentes que o registro realmente carregou, inclusive os personalizados, e recusa nomes inexistentes antes de iniciar uma tarefa.
- `internal/skill/plugin_loader.go`: skills `.yaml`/`.yml` passam por YAML verdadeiro; arquivos JSON existentes continuam aceitos. Erros de análise não reproduzem o conteúdo do arquivo.
- `internal/adapter/cli/markdown.go`: renderizações Markdown já concluídas são reutilizadas em redraws do terminal, com limite de entradas e bytes e invalidação por largura.

O executável inteiro compilou com `CGO_ENABLED=0 go build`. Os testes de comportamento continuam adiados conforme pedido do usuário; compilação não comprova melhoria de latência nem todos os fluxos interativos.

## Próximas aplicações por valor

1. **Plano de tarefas durável e coerente.** Hoje `task_create` dispara uma execução; `task_update` aceita até um ID inexistente e grava metadados em um mapa sem ligação à execução. A referência separa plano de processo, inclui dono, dependências e claim. Implementar um store de plano com transições e dependências validadas, ligado às ferramentas. Não reutilizar o status de processos vivos como se fosse status do plano. Evidências: `utils/tasks.ts:541`, `tools/TaskUpdateTool/TaskUpdateTool.ts:129`, `internal/tool/taskupdate.go:34`.
2. **Recuperação de turnos interrompidos.** A referência distingue prompt sem resposta, ferramenta concluída e resultado de ferramenta pendente antes de continuar. O Polypod guarda mensagens e journal de ferramentas, mas não reconstrói automaticamente esse estado ao retomar o CLI. Fazer a detecção no carregamento e oferecer continuação segura com base no journal. Evidências: `utils/conversationRecovery.ts:159`, `internal/session/restore.go:44`, `internal/session/tool_transcript.go:159`.
3. **Rotinas entre processos.** A referência elege um único dono do agendador por projeto, detecta dono morto e assume a função. O Polypod inicia o scheduler em cada processo que usa o mesmo arquivo; a correção precisa também sincronizar alterações feitas por outra instância. Evidências: `utils/cronTasksLock.ts:111`, `utils/cronScheduler.ts:388`, `internal/scheduler/scheduler.go:361`, `main.go:2025`.
4. **Ferramentas durante o stream.** O Polypod já paraleliza leituras depois de fechar o stream. A referência começa ferramentas quando cada chamada completa chega, reduzindo espera em respostas com várias chamadas; a implementação Go deve manter a ordem, propagação de cancelamento e journal. Evidências: `services/tools/StreamingToolExecutor.ts:73`, `internal/ai/client.go:462`.
5. **Sessões remotas e plugins completos.** O transporte remoto Go ainda não está ligado ao agente principal. O carregador de plugins agora aceita YAML, mas não instala, atualiza nem remove pacotes, e descarta metadados de skills por caminho e argumentos. Implementar protocolo remoto e ciclo de plugins próprios; endpoints privados da referência não são reutilizáveis. Evidências: `remote/SessionsWebSocket.ts:15`, `services/plugins/pluginOperations.ts:321`, `skills/loadSkillsDir.ts:853`, `internal/remote/session.go:1`, `internal/skill/plugin_loader.go:35`.
6. **CLI longo e voz contínua.** O cache Markdown melhora redraws repetidos, mas ainda falta virtualização por mensagem. O ditado atual usa duração fixa; gravação contínua e transcrição parcial precisam de backend local ou serviço sob controle do Polypod. Evidências: `hooks/useVirtualScroll.ts:303`, `hooks/useVoice.ts:373`, `internal/adapter/cli/model.go:1397`, `internal/adapter/cli/voice_input.go:16`.

## Partes que não convém transpor literalmente

O prazo automático de sete dias para rotinas recorrentes em `utils/cronTasks.ts:315` conflita com a operação contínua desejada para o Polypod. A busca semântica de sessões em `utils/agenticSessionSearch.ts:146` pode enviar até 100 trechos de transcript por consulta ao modelo; uma versão do Polypod deve limitar custo e preservar busca textual rápida. `utils/tasks.ts:458` grava os dois lados de uma dependência separadamente e não impede ciclos; o novo store deve usar transação e validar o grafo. Integrações CCR, OAuth e voz da referência dependem de serviços privados e requerem alternativas próprias.
