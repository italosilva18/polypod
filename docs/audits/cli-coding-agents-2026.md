# Auditoria de CLIs de programação com IA — novembro de 2026

## Escopo e método

Esta auditoria compara a Poly com dez CLIs de programação ativas, usando
documentação primária de cada projeto e a implementação local. A ordem da
tabela é uma ordem de estudo, não um ranking de usuários ou de qualidade. Uma
função anunciada por outro produto é uma hipótese de melhoria para a Poly;
somente uma avaliação com o mesmo modelo, tarefas e orçamento permite concluir
que ela melhora o resultado. O perfil de execução escolhido para a Poly fica
fora deste plano de mudanças.

| Referência | Padrão útil documentado | Estado da Poly e oportunidade |
| --- | --- | --- |
| [Claude Code](https://claude.com/product/claude-code), [plugins](https://support.claude.com/en/articles/13837440-use-plugins-in-claude) | Terminal, IDE e web ligados a plugins de skills, agentes, hooks e MCP. | Pacotes locais da Poly incluem skills, hooks e MCP com validação transacional, rollback de instalação e sincronização em runtime. Oportunidade futura: catálogo remoto interoperável. |
| [Codex CLI](https://github.com/openai/codex), [app-server](https://developers.openai.com/blog/codex-as-a-platform) | CLI, SDK e servidor de sessões com eventos persistentes. | TUI, headless `stream-json`, ACP e canais duráveis publicam e consomem o mesmo diário versionado em `data.dir/session-events` com cursor, recibo idempotente por `turnId` e cancelamento. |
| [Cursor CLI](https://cursor.com/docs/cli/overview), [uso avançado](https://prod.cursor.com/docs/cli/using) | Tarefas remotas, retomada de sessões, worktrees e ACP. | A Poly expõe ACP v1 por stdio com sessão durável, replay, cancelamento, servidores MCP por sessão e blocos `diff` estruturados validados visualmente com extmarks no Neovim 0.12.6 (`Agentic.nvim`). |
| [GitHub Copilot CLI](https://github.com/features/copilot/cli), [fleet](https://docs.github.com/en/copilot/how-tos/copilot-cli/use-copilot-cli/speed-up-task-completion) | Planejamento, subagentes e tarefas paralelas visíveis, além de fluxo GitHub. | `/pr_comments` pagina `gh` e fallback REST API v3 (`Link`) mapeando comentários inline ao diff. `/autofix-pr` retém propostas não verificadas em `.polypod/held-proposals/*.patch` e foi validado ponta a ponta no PR real `github/gh-aw#77334`. |
| [Gemini CLI](https://google-gemini.github.io/gemini-cli/docs/cli/), [cache](https://google-gemini.github.io/gemini-cli/docs/cli/token-caching.html) | Métricas de tokens de cache e uso, com automação headless. | O headless reporta tokens, passos, falhas de ferramenta e custo medido (`US$ 0.0000` em Ollama local, USD tarifado quando tabelado ou `não medido`). Suíte congelada (`coding-suite.v1.json`) executou comparação controlada multi-CLI sob zero chamadas pagas. |
| [OpenCode](https://opencode.ai/docs/agents/), [ACP](https://opencode.ai/docs/cli/acp/) | Agentes configuráveis e protocolo para usar o mesmo agente em editores. | O adaptador ACP v1 aceita `mcpServers` (`stdio`, `http`, `sse`) enviados pelo editor em `session/new`, `session/load` e `session/resume`, isolando ferramentas `mcp__<servidor>__<tool>` por sessão. |
| [Kilo CLI](https://kilo.ai/docs/code-with-ai/platforms/cli-reference), [Agent Manager](https://kilo.ai/docs/automate/agent-manager) | Sessões paralelas em worktrees com revisão visual do diff. | `/work` lista tarefas, progresso, worktrees, arquivos sobrepostos entre branches e atribuição explícita de custo medido em `TaskState.CostUSD` (ou `custo=não medido` quando fora de tabela). |
| [Aider](https://aider.chat/docs/), [mapa do repositório](https://aider.chat/docs/config/options) | Mapa com orçamento de tokens e verificação de lint/testes após edição. | `code_context_search` usa fusão híbrida RRF + seleção diversificada em dois passos dentro do orçamento de tokens; após atingir `1,000` de `recall@5` nas fixtures congeladas v1/v2/v3 com menos tokens que o lexical na v1/v2, `hybrid` virou o ranking padrão. |
| [Qwen Code](https://github.com/QwenLM/qwen-code), [daemon](https://github.com/QwenLM/qwen-code/blob/main/docs/users/qwen-serve.md) | Um agente atende terminal, editor, web, SDK e canais por um serviço de sessões. | A Poly compartilha `internal/session.EventJournal` entre CLI interativa, headless, ACP e canais com retomada por cursor e deduplicação persistente de `turnId`. |
| [Goose](https://github.com/aaif-goose/goose) | CLI, desktop, API e catálogo MCP amplo. | O cliente MCP sincroniza servidores de plugins locais e servidores efêmeros enviados por editores via ACP; `/plugin` instala, atualiza e reverte versões inválidas transacionalmente. |

## Achados no repositório

1. `internal/codesearch/search.go` e `internal/codesearch/hybrid.go`: a busca
   filtra extensões no próprio Git, libera o mutex durante chamadas ao embedder
   e deduplica cálculos simultâneos para trechos idênticos. A fusão híbrida foi
   evoluída para combinar Reciprocal Rank Fusion (RRF) ponderada entre os sinais
   lexical, mapa estrutural e semântico com empacotamento diversificado em duas
   passagens sob orçamento estrito de tokens (`internal/tool/codesearch.go`
   agora usa `hybrid` como padrão; detalhes em
   [qualidade e busca de código](../integrations/code-quality-and-search.md)).
2. `internal/session/` e `internal/headless/headless.go`: o diário de eventos
   versionado foi unificado em `internal/session.EventJournal`
   (`data.dir/session-events`, mantendo leitura retrocompatível de
   `data.dir/acp-events`). TUI (`internal/adapter/cli`), headless
   (`text`/`json`/`stream-json`), ACP (`internal/acp`) e canais em `main.go`
   publicam deltas, chamadas de ferramenta, conclusão, cancelamento e erro na
   mesma sequência monotônica por sessão, com paginação por cursor e recibos
   persistentes de idempotência por `turnId`.
3. `internal/evaluation/`, `docs/evaluation/coding-suite.v1.json`,
   `docs/evaluation/coding-suite-cases.v1.json` e `scripts/eval-coding-suite.py`:
   cinco tarefas reais de Go extraídas de subsistemas da Poly foram congeladas
   com SHA-256 verificável e testes `go test` retidos fora do workspace visível
   ao agente. O runner importa medições headless, julga o patch pelo verificador
   externo mesmo quando o processo sai com erro e registra taxa de resolução,
   latência p50/p95, tokens, falhas de ferramenta e custo medido quando
   aplicável.
4. `internal/commands/pr_comments_native.go`, `internal/tool/builtin.go`,
   `internal/tool/multiedit.go` e `autofix_pr.go`: `/pr_comments` pagina
   comentários inline, revisões, comentários gerais e arquivos via `gh` ou
   fallback HTTP direto na API REST v3 do GitHub (`Link` pagination, repetindo
   sem token em HTTP `401`/`403` para repositórios públicos de terceiros e
   priorizando comentários inline e revisões antes de comentários gerais de bots
   para não truncar revisões de código no teto de `48 KiB`). As ferramentas
   `write_file`, `edit_file` e `multi_edit` passaram a acionar o
   `turnCheckpoint` antes da escrita; quando os verificadores falham ou não
   estão configurados, `/autofix-pr` exporta o diff proposto para
   `.polypod/held-proposals/<timestamp>.patch`, reverte a worktree ao estado
   pré-turno e emite `[STATUS /autofix-pr: PROPOSTA RETIDA — NÃO VERIFICADA]`.
5. `internal/acp/server.go`, `internal/acp/mcp_sessions.go` e
   `internal/acp/file_diff.go`: `polypod acp` expõe
   [ACP v1](https://agentclientprotocol.com/protocol/v1/overview) com sessões
   duráveis, bloqueio entre processos, replay paginado, cancelamento, servidores
   MCP enviados pelo editor (`stdio`, `http`, `sse`) isolados por sessão em
   `session/new`, `session/load` e `session/resume`, e blocos estruturados
   `diff` (`path`, `oldText`, `newText`) em `edit_file`, `write_file`,
   `file_edit`, `file_write`, `multi_edit`, `undo_edit`, `notebook_edit` e
   `apply_patch`. A documentação está em
   [integração ACP](../integrations/ide-acp.md).
6. `internal/tracking/`, `internal/tasks/`, `internal/commands/work_overview.go`
   e `internal/skill/`: `/plugin` valida e aplica pacotes versionados com
   rollback automático de versões inválidas e sincronização MCP em runtime.
   `MeasuredCost` calcula `US$ 0.0000` medido para provedores locais (`ollama`),
   aplica a tabela tarifária quando o modelo possui preço conhecido e mantém
   `CostMeasured=false` (`custo=não medido`) para modelos remotos sem tarifa
   cadastrada, propagando o acumulado real para `TaskState.CostUSD` em `/work`
   junto da detecção de arquivos sobrepostos entre `git worktree`s.
7. `code_context_search`, `code_semantic_search` e `run_project_checks` estão
   registradas no perfil `default` (`main.go`), preservando integralmente o
   perfil de execução irrestrito configurado na Poly.

## Arquitetura alvo

```text
CLI / IDE (ACP) / canais / automação
                 |
        Serviço de sessões
     eventos, retomada, cancelamento
                 |
            Núcleo Poly
  modelo + ferramentas + memória + tarefas
      /          |            \
 contexto     execução      observabilidade
 lexical +    concorrente   traces + custo
 mapa +       e worktrees   + avaliações
 semântico
```

O serviço de sessões mantém IDs duráveis, número de sequência por evento,
reconexão sem repetir ferramentas e cancelamento explícito em
`data.dir/session-events`. TUI, headless, ACP e canais consomem e alimentam essa
mesma estrutura, preservando uma única fonte de verdade para conversa e
checkpoint de arquivos.

O planejador de contexto combina busca lexical, símbolos/repo map, arquivos
modificados e busca semântica opcional sob orçamento explícito de tokens, com
caminho, linhas e motivo da seleção em cada trecho. [RepoBench](https://arxiv.org/abs/2306.03091)
e [Agent Retrieval Bench](https://arxiv.org/abs/2607.24882) orientam a medição
de `recall@k` antes de qualquer promoção de ranking padrão.

## Ordem de implementação e critérios de aceite

| Etapa | Entrega | Critério verificável |
| --- | --- | --- |
| 1 | Suíte de tarefas reais de programação, com versões fixas de Poly, modelo, prompts e ferramentas | Verificador externo passa ou falha cada patch; registrar taxa de resolução, p50/p95 de latência, custo por tarefa resolvida, tokens e falhas de ferramenta. |
| 2 | Contexto com orçamento e recuperação híbrida | Aumentar `recall@5` de arquivos relevantes sem aumentar tokens por tarefa; usar ablação lexical/mapa/semântico. |
| 3 | Protocolo de sessão e eventos versionados | Dois clientes retomam a mesma sessão sem duplicar execução; testes de reconexão, cancelamento e 100 turnos. |
| 4 | ACP e integração de IDE | Editor recebe deltas, chamadas de ferramenta, diff, estado e retomada pelo serviço de sessões. |
| 5 | Fluxo GitHub de PR e issues | Ler comentários/reviews com paginação, mapear ao diff e mostrar evidência de testes antes de propor correções. |
| 6 | Visão de tarefas e worktrees | Trabalho paralelo mostra responsável, progresso, custo e arquivos alterados; conflitos detectados antes da integração. |
| 7 | Distribuição de plugins e MCP | Pacote versionado instala e atualiza skills, hooks e servidores; teste de compatibilidade e rollback da instalação. |

## Avaliação exploratória em 12 de novembro de 2026 (linha de base)

Esta rodada inicial começou apenas com Ollama local e depois incluiu o
[endpoint gratuito de prototipagem da NVIDIA](https://build.nvidia.com/nvidia/nemotron-3.5-super-vl-120b-a12b),
sem nenhuma chamada paga. Foram inventariadas sete CLIs instaladas na máquina:
Claude Code 2.1.301, Codex CLI 0.167.1, Aider 0.86.2, Gemini CLI 0.60.0,
OpenCode 1.4.2, Kilo CLI 7.2.10 e Qwen Code 0.24.0 (Cursor, Goose e GitHub
Copilot CLI não estavam instaladas).

Na linha de base de `scripts/eval-local-context.py` (8 perguntas sobre 16
arquivos reais, `nomic-embed-text` via Ollama local), o híbrido inicial empatava
com o lexical em `6/8` (`0,750`) e na fixture congelada v2 (16 consultas) ficava
abaixo do lexical (`14/16` com `31.866` tokens contra `15/16` com `31.904`
tokens), motivo pelo qual o padrão permanecia `lexical`. No caso sintético único
de `scripts/eval-coding.py`, modelos CPU locais (`qwen2.5-coder:1.5b/3b/7b` em
Intel Core i5-1035G1 sem GPU dedicada) falharam ou atingiram timeout de `300 s`,
enquanto `nvidia/nemotron-3.5-super-vl-120b-a12b` resolveu `1/1` tarefa sintética
em `8.627 ms` (`46.880` tokens, `8` passos) após ajuste exploratório do limite
para `16` passos.

## Fechamento técnico 07/07 em 13 de novembro de 2026

Todas as pendências das etapas 1 a 6 foram implementadas, revalidadas sem
regressão na etapa 7 e medidas sob política estrita de **zero chamadas pagas**
(`nomic-embed-text:latest` em Ollama local `0.40.14` + endpoint gratuito de
prototipagem `nvidia/nemotron-3.5-super-vl-120b-a12b` com `HOME` isolado).

### Etapa 2 — Ablação reproduzível de contexto híbrido (`scripts/eval-local-context.py`)

Com a fusão RRF ponderada + empacotamento diversificado em duas passagens
(`internal/codesearch/hybrid.go`) e orçamento fixo de `2.000` tokens por
consulta (`16.000` na v1, `32.000` na v2 e `48.000` na v3), a medição local com
`nomic-embed-text:latest` produziu:

| Fixture congelada | Lexical (`recall@5` / tokens) | Mapa (`recall@5` / tokens) | Semântico (`recall@5` / tokens) | Híbrido (`recall@5` / tokens) |
| --- | ---: | ---: | ---: | ---: |
| `context-fixture.v1.json` (8 consultas) | 8/8 (`1,000`) / 15.831 | 7/8 (`0,875`) / 13.976 | 8/8 (`1,000`) / 15.929 | **8/8 (`1,000`) / 15.671** |
| `context-fixture.v2.json` (16 consultas) | 16/16 (`1,000`) / 31.645 | 14/16 (`0,875`) / 27.270 | 14/16 (`0,875`) / 31.862 | **16/16 (`1,000`) / 30.810** |
| `context-fixture.v3.json` (24 consultas EN+PT) | 24/24 (`1,000`) / 45.580 | 19/24 (`0,792`) / 40.328 | 21/24 (`0,875`) / 47.743 | **24/24 (`1,000`) / 46.121** |

Frente à linha de base congelada na v2 (`15/16` lexical com `31.904` tokens e
`14/16` híbrido com `31.866` tokens), o novo ranking híbrido subiu para `16/16`
(`1,000`) reduzindo o consumo para `30.810` tokens (`-1.056` tokens frente ao
híbrido anterior e `-835` tokens frente ao lexical atual), mantendo `24/24` na
v3 bilíngue dentro do teto de `48.000` tokens. Com o critério atendido, `hybrid`
foi promovido a padrão em `code_context_search`.

### Etapa 1 — Suíte congelada de Go e comparação controlada multi-CLI (`docs/evaluation/coding-suite-report.v1.json`)

A suíte `docs/evaluation/coding-suite.v1.json` (`manifest_sha256`:
`aae1782f87d8f60c37eee121a77af55af4aae5a99bd182d302af3ba30f65d77d`) e seus casos
`docs/evaluation/coding-suite-cases.v1.json` (`cases_sha256`:
`126fe9d38606ba2b3cf03b6be96f801de235eeea93959d3b770792b417b83f9c`) congelam
cinco problemas reais de Go (`pr-hunk-coordinate-mapping`,
`measured-cost-attribution`, `acp-mcp-tool-kind-and-diff-path`,
`event-journal-cursor-replay` e `hybrid-rrf-diversified-budget`) julgados por
pacotes `go test` retidos fora do workspace do agente. O script
`scripts/eval-coding-suite.py` executou todas as CLIs compatíveis instaladas com
o **mesmo modelo** (`nvidia/nemotron-3.5-super-vl-120b-a12b`), **mesmo orçamento
pré-declarado** (`max_steps=16`, `max_seconds=180`, `max_usd=1,00`), `seed=20260417`
e `HOME` isolado:

| CLI e versão | Tarefas resolvidas | Latência p50 / p95 (ms) | Tokens totais (p50) | Falhas de ferramenta | Custo por tarefa resolvida | `results_sha256` (prefixo) |
| --- | ---: | ---: | ---: | ---: | --- | --- |
| `qwen-code 0.24.0` | **4/5 (`80%`)** | 126.202 / 164.844 | 437.111 (93.494) | 3 | não medido (endpoint gratuito NVIDIA) | `14d1cd633a17` |
| `opencode 1.4.2` | **3/5 (`60%`)** | 34.276 / 44.355 | 157.214 (29.450) | 0 | não medido (endpoint gratuito NVIDIA) | `4b8071f378a5` |
| `aider 0.86.2` | **3/5 (`60%`)** | 37.721 / 120.076 | 23.200 (4.400) | 0 | não medido (endpoint gratuito NVIDIA) | `c317243b3ce4` |
| `polypod v0.6.0` | **2/5 (`40%`)** | 40.775 / 180.101 | 614.497\* (175.218) | 5 | não medido na API NVIDIA; `US$ 0.0000` medido em Ollama local | `7c0903d9d6c3` |

\* *Em `polypod v0.6.0`, a soma de tokens reflete as 3 execuções que emitiram envelope final (`samples=3`); `acp-mcp-tool-kind-and-diff-path` atingiu o teto de `180.101 ms` (`timeout`) e, imediatamente após um turno de `317.669` tokens em `event-journal-cursor-replay`, `hybrid-rrf-diversified-budget` recebeu erro imediato da API (`agent_exit_code:1` em `318 ms`, rate-limit transitório).*

Inventário transparente das demais CLIs locais no mesmo relatório:
- `codex-cli 0.167.1`: `preflight_unavailable` (`in model_providers.nvidia.wire_api` — a versão 0.167 removeu `wire_api = "chat"`, exigindo `/v1/responses`, indisponível no endpoint chat da NVIDIA).
- `claude 2.1.301` e `gemini 0.60.0`: `excluded_zero_paid_policy` (exigem endpoints proprietários Anthropic/Google ou sessão OAuth paga).
- `kilo 7.2.10`: `covered_by_opencode_engine` (compartilha a mesma engine CLI de `opencode run`).

**Diagnóstico técnico revelado pela suíte controlada:** sob o mesmo modelo, as
CLIs dedicadas exclusivamente a código enviam esquemas enxutos de ferramentas
(`23,2k` tokens totais no Aider; `157,2k` no OpenCode), enquanto o perfil
`default` generalista da Poly injeta ~130 definições de ferramentas (IoT, MQTT,
GUI, canais, hardware e rede, ~35k tokens de prompt por iteração), elevando a
latência por passo e acionando timeout/rate-limit no orçamento de `180 s`. Esse
achado orienta diretamente a criação futura de um subperfil enxuto `coding` sem
restringir o perfil `default`.

### Etapas 3, 4, 5, 6 e 7 — Evidências de protocolo, IDE, PR real e worktrees

- **Etapa 3 (`internal/session/`, `internal/acp/`, `internal/headless/`, `internal/adapter/cli/`):**
  testes automatizados com `-race` validam 100 turnos sequenciais, retomada
  concorrente por 2 clientes sobre o mesmo `session-events`, cancelamento em
  andamento e deduplicação persistente de `turnId` sem reexecutar ferramentas.
- **Etapa 4 (`scripts/test-acp-neovim.lua` + `scripts/acp-test-mcp-server.py`):**
  no Neovim `0.12.6` headless com `Agentic.nvim`, a sessão
  `b4f90dc9-0ee8-469a-9d9e-e5ca3001cdf5` injetou um servidor MCP stdio pelo
  editor, invocou `mcp__editor__project_hint`, aplicou `file_write` com bloco
  estruturado `diff` (`1` bloco, `8` extmarks visuais de diff renderizados no
  buffer da UI) e, após reconexão via `session/load`, reconstruiu integralmente
  os `8` chunks e os `8` extmarks visuais (`nvim_exit_code = 0`).
- **Etapa 5 (`scripts/eval-real-pr-autofix.py` no PR público aberto [`github/gh-aw#77334`](https://github.com/github/gh-aw/pull/77334), `head_sha` `975779adacb94c31efd9a073c631b3e7d3fd4c9a`):**
  1. `/pr_comments 77334` paginou a API REST v3 do GitHub sem credenciais após
     fallback automático de PAT restrito (`HTTP 403`) e mapeou os dois
     comentários inline diretamente em
     `Linha pkg/workflow/model_routing_default.go:158 [no diff atual]`.
  2. No ensaio negativo de bloqueio, quando o verificador `go test` falhou
     (`exit status 1`), `/autofix-pr` reteve o patch em
     `.polypod/held-proposals/*.patch`, reverteu os arquivos editados (`git diff`
     vazio) e emitiu `[STATUS /autofix-pr: PROPOSTA RETIDA — NÃO VERIFICADA]`.
  3. Na execução completa com `nvidia/nemotron-3.5-super-vl-120b-a12b`, o
     verificador externo partiu de falha real no `HEAD` do PR
     (`pre_fix_verifier_exit_code = 1` em
     `TestDefaultModelRoutingActivationHonorsModelVariables`), o agente corrigiu
     `pkg/workflow/model_routing_default.go`, `pkg/workflow/model_env_vars_test.go`
     e `pkg/workflow/model_routing_test.go` em `35` passos (`2.126.456` tokens),
     aprovou `run_project_checks` + verificador externo pós-turno
     (`post_fix_verifier_exit_code = 0`, `ok github.com/github/gh-aw/pkg/workflow 0.221s`)
     e emitiu `[STATUS /autofix-pr: CORREÇÃO VERIFICADA]`, sem executar commit
     ou push.
- **Etapa 6 (`internal/tracking/`, `internal/tasks/`, `internal/commands/work_overview.go`):**
  testes `-race` confirmam atribuição acumulada em `TaskState.CostUSD` para
  modelos tarifados e Ollama local (`US$ 0.0000`), preservação explícita de
  `custo=não medido` para endpoints sem tarifa e detecção de conflitos de
  arquivos modificados entre múltiplas worktrees em `/work`.
- **Etapa 7 (`internal/skill/`, `internal/mcp/`):** suíte completa revalidada
  com instalação, atualização, ativação, sincronização MCP e rollback de pacote
  inválido sem regressões.

## Estado verificável em 13 de novembro de 2026 (07/07)

| Etapa | Estado | Evidência verificável no repositório |
| --- | --- | --- |
| 1 | Atendido localmente | Suíte congelada de 5 tarefas reais Go (`docs/evaluation/coding-suite.v1.json`, `cases_sha256=126fe9d38606...`) com verificador `go test` externo retido e relatório multi-CLI reproduzível (`docs/evaluation/coding-suite-report.v1.json`) comparando `qwen-code`, `opencode`, `aider` e `polypod` sob o mesmo modelo, orçamento e zero chamadas pagas. |
| 2 | Atendido localmente | Fusão híbrida RRF + seleção diversificada (`internal/codesearch/hybrid.go`) atingiu `recall@5 = 1,000` nas três fixtures congeladas (`8/8` na v1 com `15.671` tok, `16/16` na v2 com `30.810` tok vs `31.645` do lexical, `24/24` na v3 com `46.121` tok) e foi promovida como padrão em `code_context_search`. |
| 3 | Atendido localmente | `internal/session.EventJournal` (`data.dir/session-events`, retrocompatível com `acp-events`) unifica TUI, headless, ACP e canais com cursor, cancelamento, deduplicação persistente por `turnId` e testes `-race` de 100 turnos e 2 clientes simultâneos. |
| 4 | Atendido localmente | `polypod acp` suporta `mcpServers` por sessão (`stdio`/`http`/`sse`) em `new`/`load`/`resume` e emite blocos `diff` estruturados para todas as ferramentas de escrita; validado ao vivo no Neovim `0.12.6` + `Agentic.nvim` com `mcp__editor__project_hint` e `8` extmarks visuais de diff preservados após replay. |
| 5 | Atendido localmente | `/pr_comments` pagina `gh` e fallback REST API v3 mapeando linhas ao diff; `/autofix-pr` retém propostas não aprovadas em `.polypod/held-proposals/*.patch` revertendo a worktree e comprovou correção verificada ponta a ponta no PR real `github/gh-aw#77334` (`exit 1` -> `exit 0`). |
| 6 | Atendido localmente | `/work` exibe responsável, progresso, worktree, conflitos de arquivos sobrepostos entre branches e custo real acumulado em `TaskState.CostUSD` (`US$ 0.0000` local, USD tabelado ou `custo=não medido` explícito). |
| 7 | Atendido localmente | `/plugin` instala, atualiza, ativa e reverte versões inválidas de skills, hooks e MCP transacionalmente, com sincronização em runtime e zero regressões na suíte completa. |

Para evolução contínua além deste fechamento `07/07`, recomenda-se ampliar o
conjunto privado congelado em `docs/evaluation/` com múltiplas repetições por
seed, adicionar um perfil opcional de ferramentas enxuto para benchmarks de
código puro e acompanhar protocolos externos como
[SWE-bench Verified](https://www.swebench.com/verified.html),
[SWE-bench Pro V2](https://arxiv.org/abs/2610.24206) e
[avaliação de traces](https://developers.openai.com/api/docs/guides/trace-grading).

## Limites desta auditoria

Os critérios técnicos e operacionais das etapas `01/07` a `07/07` estão
atendidos e verificados localmente, sem alterar o perfil de execução da Poly e
sem realizar chamadas pagas. A comparação controlada multi-CLI (`n = 5` tarefas
reais de Go em seed única no endpoint gratuito da NVIDIA) cumpre o protocolo de
mesmo modelo, mesmo orçamento e verificador externo independente, mas não
constitui ranking mundial nem mede CLIs acopladas exclusivamente a APIs
proprietárias pagas (`claude`, `gemini`) ou endpoints `/v1/responses`
(`codex-cli 0.167.1`).
