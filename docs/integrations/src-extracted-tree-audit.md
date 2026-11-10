# Auditoria da árvore `src_extracted`

**Data:** 9 de novembro de 2026. A referência é a cópia local em `../src_extracted/src` (relativa à pasta `polypod`). Esta análise usa os comportamentos observados como referência para implementação independente em Go; não incorpora código, prompts ou recursos da referência.

A [leitura linha a linha dos fluxos prioritários](src-extracted-line-review.md) registra os arquivos efetivamente cobertos, as correções aplicadas e os próximos trabalhos.

## Cobertura e método

O inventário percorreu todos os arquivos de `src_extracted/src`. Há **3.804 arquivos físicos**, porém `src_extracted/src/src` repete exatamente a árvore externa: **1.902 caminhos correspondentes, sem diferenças de SHA-256**. Portanto, há **1.902 arquivos únicos**. Cada arquivo único foi classificado por área. A revisão funcional rastreou símbolos, imports e pontos de inicialização, com leitura dirigida dos fluxos de maior impacto. **Não foi uma leitura linha a linha dos 1.902 arquivos, nem uma prova de equivalência em execução.** O usuário adiou os testes nesta rodada.

| Área do inventário | Arquivos únicos | Diretórios |
| --- | ---: | --- |
| Núcleo do agente, ferramentas, tarefas, estado, contexto e tipos | 228 | `assistant`, `query`, `tools`, `coordinator`, `tasks`, `state`, `context`, `types` |
| Serviços, rede, memória, plugins, skills e migrações | 212 | `services`, `remote`, `bridge`, `server`, `voice`, `memdir`, `plugins`, `skills`, `migrations`, `upstreamproxy` |
| Interface, comandos, entrada e navegação | 846 | `cli`, `commands`, `components`, `screens`, `ink`, `hooks`, `keybindings`, `vim`, `outputStyles`, `entrypoints` |
| Utilitários, inicialização, constantes e arquivos da raiz | 616 | `utils`, `bootstrap`, `buddy`, `constants`, `moreright`, `native-ts`, `schemas`, arquivos na raiz |
| **Total** | **1.902** | **35 diretórios e arquivos na raiz** |

Uma definição presente na árvore extraída pode estar condicionada a uma *feature flag* ou a um serviço privado. Uma definição no Polypod também pode não estar ligada ao executável principal. Por isso, os itens abaixo distinguem implementação de ligação ao runtime.

## Diferenças confirmadas em 9 de novembro e prioridade original

| Prioridade | Diferença | Evidência na referência | Evidência no Polypod |
| --- | --- | --- | --- |
| Alta | Planejamento e execução de tarefas usam stores distintos. `task_create` inicia imediatamente uma execução, enquanto `task_update` conserva metadados em memória, sem atualizar a tarefa persistida. Falta um plano durável com dono, status e dependências. | `tools/TaskCreateTool/TaskCreateTool.ts:80`, `tools/TaskUpdateTool/TaskUpdateTool.ts:190` | `internal/tool/tasktools.go:159`, `internal/tool/taskupdate.go:12` |
| Alta | Equipes e mensagens entre agentes têm componentes Go, mas as ferramentas de equipe não são registradas no fluxo principal. | `tools/TeamCreateTool/TeamCreateTool.ts:155`, `tools/SendMessageTool/SendMessageTool.ts:149` | `internal/swarm/swarm.go:124`, `internal/wiring/wiring.go:195`, `internal/tool/teamtools.go:240` |
| Alta | Sessões remotas persistentes e entrega ordenada com reconexão não estão ligadas ao Polypod. O módulo Go remoto não tem chamada de produção; `/remote-trigger` é placeholder. Requer protocolo e servidor próprios. | `remote/SessionsWebSocket.ts:17`, `bridge/remoteBridgeCore.ts:260` | `internal/remote/session.go:1`, `internal/commands/batch17_trace.go:42` |
| Alta | O turno minimizado no CLI ainda ocupa o executor principal; mensagens posteriores esperam na fila. | `tasks/LocalMainSessionTask.ts:94`, `hooks/useSessionBackgrounding.ts:27` | `internal/adapter/cli/model.go:127` |
| Média | O CLI pagina mensagens antigas, mas recompõe toda a página carregada; falta virtualização por mensagem para históricos longos. | `hooks/useVirtualScroll.ts:12`, `ink/components/ScrollBox.tsx:73` | `internal/adapter/cli/model.go:1397`, `internal/adapter/cli/history_pagination.go:5` |
| Média | O modelo do Polypod espera o stream terminar antes de iniciar ferramentas. Leituras já podem executar em paralelo depois disso. | `services/tools/StreamingToolExecutor.ts:34` | `internal/ai/client.go:529`, `internal/wiring/wiring.go:318` |
| Média | O ciclo de plugins contempla carregamento de skills, mas não instalação, atualização e reconciliação estruturadas. | `services/plugins/pluginOperations.ts:321`, `utils/plugins/reconciler.ts:1` | `internal/skill/plugin_loader.go:35`, `main.go:1067` |
| Média | Voz no CLI captura por duração fixa e transcreve ao final; faltam pressionar para falar e texto parcial. | `hooks/useVoiceIntegration.tsx:250`, `services/voice.ts:335` | `internal/voice/voice.go:77`, `internal/adapter/cli/commands.go:107` |
| Média | Registro de sessões simultâneas e sugestão contextual estão implementados em pacotes Go, mas sem ligação encontrada ao startup/TUI. | `utils/concurrentSessions.ts:48`, `services/PromptSuggestion/promptSuggestion.ts:121` | `internal/session/concurrent.go:61`, `internal/suggestions/suggestions.go:59` |
| Baixa | A busca de sessões do Polypod é textual; a referência também classifica relevância semanticamente. | `utils/agenticSessionSearch.ts:146` | `internal/session/storage.go:259` |

O `AdvancedQueryEngine` em `internal/query/advanced_engine.go` e os modos `coordinator` não são instanciados no caminho principal; o CLI usa `internal/ai/service.go`. A integração deve mirar esse runtime efetivo, evitando ampliar módulos paralelos inativos.

## Situação após a implementação de 10 de novembro

| Área | Implementação atual | Limite ainda conhecido |
| --- | --- | --- |
| Planos e tarefas | `plan_run`/`plan_retry` reservam uma execução durável por item, publicam na fila SQLite, recuperam a janela de falha entre bancos e sincronizam tentativas e resultados. `/bg` cria uma tarefa independente no CLI. | O turno que já está ativo no chat principal não vira uma tarefa destacada; `/bg` inicia um trabalho novo. Efeitos externos podem se repetir após uma falha entre executar e registrar o resultado. |
| Equipes de agentes | `team_create`, `team_spawn`, `team_send`, `team_inbox`, `team_ack`, `team_status`, `team_results` e `team_stop` usam `sys.Swarm` e o executor de agentes. Resultados chegam à caixa do líder, com leitura e confirmação separadas. | A equipe vive apenas no processo atual; não retoma trabalhos após reinício. |
| Sessões remotas | Servidor próprio em `internal/remote` com SQLite, token, ordem, cursor por participante, reconexão e encaminhamento durável ao `humanloop`; `polypod remote` oferece um cliente de terminal. Perguntas `ask_user` são publicadas na mesma sessão e as respostas ficam persistidas. O serviço root hospeda somente em loopback. | O acesso de outra máquina precisa de um transporte externo autenticado e com TLS, por exemplo túnel SSH; a sessão não expõe uma URL pública diretamente. O token é compartilhado entre participantes. |
| Agendador e fila | Locks e leases coordenam o serviço root e o CLI, com retomada e histórico. | Como em qualquer executor com efeitos externos, exatamente uma execução após falha depende de idempotência da ferramenta usada. |
| Memória automática | O serviço de consolidação já estava ligado ao runtime; agora filtra tarefas automáticas, percorre a janela de mensagens do usuário e coordena processos pelo cursor compartilhado. | A qualidade do fato extraído depende do modelo; a memória não equivale a aprendizado de pesos. |
| Plugins e skills | Gerente local de plugins com listagem, ativação, desativação, recarga e detecção de mudanças em disco. | Instalação e atualização de um catálogo remoto ainda não são implementadas. |
| Streaming e CLI | Chamadas de ferramenta são analisadas e validadas durante o stream, com execução após o fim confirmado; o CLI reutiliza a renderização de mensagens e evita redesenhos desnecessários. | O protocolo Chat Completions não confirma o fim isolado de cada chamada de ferramenta; executar antes do EOF pode aplicar uma ação com argumentos incompletos. O CLI ainda não virtualiza todo o histórico. |
| Voz | Áudio recebido no WhatsApp é identificado pelo conteúdo e a transcrição local serializa acesso ao modelo entre processos. No CLI, Ctrl+G e `/voice stop` encerram a captura manual e colocam a transcrição final no compositor; `/voice N` permanece disponível. | O terminal não fornece soltura de tecla confiável, então Ctrl+G alterna gravar/parar. Texto parcial durante a fala ainda não existe. |
| Descoberta de sessões | `/sessions live` registra instâncias do mesmo perfil; `/suggest` usa contexto da conversa, Git e tarefas em segundo plano; `/sessions search` faz ranking textual com reranking semântico opcional e fallback. | A CLI local tem embeddings desativados no perfil atual e usa ranking textual. Perfis root e usuário têm registros de instância separados. |

Esta tabela descreve o código integrado; **os testes de comportamento foram adiados pelo usuário**, portanto não há prova de equivalência ou de operação completa em produção.

## Capacidades presentes no runtime

- Agendador durável com retomada, tentativas e histórico: `internal/scheduler/scheduler.go:360`.
- Heartbeat, WhatsApp e consolidação automática de memória no startup: `main.go:1820`, `main.go:1879`.
- LSP, descoberta progressiva de ferramentas e cache de resultados grandes: `main.go:893`, `internal/ai/tool_catalog.go:60`, `internal/toolcache/cache.go:77`.
- Tarefas de agente com checkpoints, histórico de sessão paginado e rewind de arquivos: `internal/tasks/agent_checkpoint.go:15`, `internal/session/paging.go:1`, `internal/checkpoint/checkpoint.go:111`.
- MCP com recursos e elicitação: `internal/mcp/client.go:70`.
- Paralelismo de ferramentas de leitura ligado ao cliente de modelo: `internal/wiring/wiring.go:318`, `internal/ai/client.go:547`.

## Ajuste feito durante a auditoria

`internal/ai/streaming_tools.go` agrupava todas as leituras e as executava antes de todas as escritas, mesmo quando o modelo havia pedido uma escrita primeiro. O executor agora trata cada escrita como barreira, executa em paralelo somente leituras consecutivas e devolve resultados na ordem original. O pacote `internal/ai` compilou; os testes de comportamento permanecem adiados conforme solicitado.

## Dependências externas da referência

Os transportes de sessão CCR, parte do OAuth, sincronização em nuvem, telemetria e a transcrição parcial da referência usam endpoints ou credenciais da Anthropic. O Polypod pode reproduzir o comportamento com protocolo e backend próprios, mas essas integrações não são transferíveis pela simples reescrita da lógica. `services/MagicDocs/magicDocs.ts:242` explicita restrição a usuários internos. As migrações da referência tratam principalmente formatos e modelos Claude e não são prioridade para a compatibilidade do Polypod.
