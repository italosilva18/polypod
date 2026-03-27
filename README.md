# Polypod

Gateway de IA multi-canal, multi-provider e auto-modificavel. Um unico binario Go de 26MB que conecta qualquer LLM a CLI, REST API, Telegram, WhatsApp e browser — com 110+ skills, 62 packages, MCP, hooks, modos, plugins, e tudo que voce precisa.

```
   ____       _                       _
  |  _ \ ___ | |_   _ _ __   ___   __| |
  | |_) / _ \| | | | | '_ \ / _ \ / _` |
  |  __/ (_) | | |_| | |_) | (_) | (_| |
  |_|   \___/|_|\__, | .__/ \___/ \__,_|
                |___/|_|  v0.4.0
```

## Por que Polypod?

| Feature | Polypod | Claude Code | Aider | Gemini CLI | Codex CLI |
|---------|---------|-------------|-------|------------|-----------|
| Multi-canal (CLI+REST+Telegram+WhatsApp+Web) | **Sim** | Nao | Nao | Nao | Nao |
| IoT/Hardware (USB, serial, firmware) | **Sim** | Nao | Nao | Nao | Nao |
| Auto-modificacao (persona, skills, agentes) | **Sim** | Parcial | Nao | Nao | Nao |
| Cross-channel notifications | **Sim** | Nao | Nao | Nao | Nao |
| Scheduler/cron integrado | **Sim** | Nao | Nao | Nao | Nao |
| Multi-provider (5 nativos + fallback) | **Sim** | Nao | Sim | Nao | Nao |
| MCP Client + Server | **Sim** | Sim | Nao | Sim | Nao |
| Hooks (PreToolUse/PostToolUse) | **Sim** | Sim | Nao | Nao | Nao |
| Modos (plan/ask/edit/auto) | **Sim** | Sim | Parcial | Sim | Sim |
| Watch mode (// AI: triggers) | **Sim** | Nao | Sim | Nao | Nao |
| Plugin system | **Sim** | Sim | Nao | Sim | Nao |
| Browser UI | **Sim** | Nao | Sim | Nao | Nao |
| Spec-driven development | **Sim** | Nao | Nao | Nao | Nao |
| Provider fallback automatico | **Sim** | Nao | Nao | Nao | Nao |
| Binario unico, zero deps | **Sim** | Nao | Nao | Nao | Nao |
| Open source | **Sim** | Nao | Sim | Sim | Sim |

## Inicio Rapido

```bash
# Compilar
git clone https://github.com/italosilva18/polypod.git
cd polypod && make build

# Primeiro uso (setup interativo — detecta stack, gera config)
./polypod --setup

# Ou inicializar projeto (gera .polypod.md + commands + settings)
./polypod init

# Executar
./polypod config.yaml

# Modo headless (para scripts/CI)
./polypod -p "explique o que faz main.go"

# Pipe via stdin
cat error.log | ./polypod -p "analise este log"

# Output JSON
./polypod -p "liste bugs" --output-format json

# Diagnostico de ambiente
./polypod doctor
```

## Instalacao

```bash
# Pre-requisitos: Go 1.24+, uma API key
go install github.com/italosilva18/polypod@latest

# Ou compilar do fonte
git clone https://github.com/italosilva18/polypod.git
cd polypod
make build          # binario local
make build-linux    # cross-compile Linux amd64
make build-arm      # cross-compile Linux arm64
```

## Providers de IA

### DeepSeek (padrao — mais barato)

```yaml
ai:
  provider: "deepseek"
  base_url: "https://api.deepseek.com/v1"
  api_key: "${DEEPSEEK_API_KEY}"
  model: "deepseek-chat"
```

### Ollama (local, gratis, offline)

```yaml
ai:
  provider: "ollama"
  base_url: "http://localhost:11434/v1"
  model: "llama3.1"
```

### OpenAI

```yaml
ai:
  provider: "openai"
  base_url: "https://api.openai.com/v1"
  api_key: "${OPENAI_API_KEY}"
  model: "gpt-4o"
```

### Anthropic (Claude)

```yaml
providers:
  - name: anthropic
    api_key: "${ANTHROPIC_API_KEY}"
ai:
  provider: "anthropic"
  model: "claude-3-5-sonnet-latest"
```

### Google Gemini

```yaml
providers:
  - name: google
    api_key: "${GOOGLE_API_KEY}"
ai:
  provider: "google"
  model: "gemini-1.5-pro"
```

### Provider Fallback Automatico

Se o provider primario falhar (rate limit, timeout), Polypod automaticamente tenta o proximo na chain, com mapeamento inteligente de modelos entre providers:

```yaml
# deepseek falha → tenta ollama → tenta openai
providers:
  - name: ollama
    base_url: "http://localhost:11434"
  - name: openai
    api_key: "${OPENAI_API_KEY}"
```

## Canais

| Canal | Config | Descricao |
|-------|--------|-----------|
| **CLI** | `cli.enabled: true` | Interface terminal rica (BubbleTea + Glamour), streaming, markdown |
| **REST API** | `rest.enabled: true` | Endpoints `/api/chat`, `/api/chat/stream` (SSE), `/api/health`, `/api/skills` |
| **Telegram** | `telegram.enabled: true` | Bot com polling, controle por ID |
| **WhatsApp** | `whatsapp.enabled: true` | Via Green API, controle por numero |
| **Browser UI** | `webui.enabled: true` | Chat web em `localhost:8090` com streaming SSE |

## Comandos da CLI

| Comando | Descricao |
|---------|-----------|
| `/mode plan\|ask\|edit\|auto` | Mudar modo de operacao |
| `/effort low\|medium\|high` | Controlar profundidade de raciocinio |
| `/compact [foco]` | Compactar contexto com foco customizado (ex: `/compact foco nas decisoes de API`) |
| `/cost` | Mostrar tokens e custo da sessao |
| `/context` | Diagnostico de uso do context window |
| `/doctor` | Verificar saude do ambiente |
| `/init` | Inicializar projeto (.polypod.md + commands + settings) |
| `/undo` | Desfazer ultima mudanca de arquivo |
| `/redo` | Refazer mudanca desfeita |
| `/rewind` | Voltar a um checkpoint anterior |
| `/theme dark\|light\|monokai\|dracula\|solarized\|nord` | Mudar tema visual |
| `/color red\|blue\|green\|...` | Mudar cor da sessao |
| `/loop 5m <prompt>` | Tarefa recorrente in-session |
| `/loop list` | Listar loops ativos |
| `/loop stop <id>` | Parar um loop |
| `/spec <descricao>` | Gerar spec (requirements → design → tasks) |
| `/history search <query>` | Buscar em sessoes passadas |
| `@path/to/file.go` | Incluir arquivo no prompt (com autocomplete) |
| `@dir/` | Incluir listagem de diretorio |

## Modos de Operacao

| Modo | Comportamento | Quando usar |
|------|---------------|-------------|
| `edit` | Padrao. Edita arquivos e executa comandos | Desenvolvimento normal |
| `plan` | Read-only. Analisa e planeja sem modificar nada | Arquitetura, planejamento |
| `ask` | Sem tools. Apenas responde perguntas | Perguntas rapidas, aprendizado |
| `auto` | Totalmente autonomo. Sem confirmacao | Tarefas bem definidas, scripts |

## Skills (110+)

### Sistema (10)

| Skill | Descricao |
|-------|-----------|
| `read_file` | Ler conteudo de arquivo |
| `read_files` | Ler multiplos arquivos (glob/lista) |
| `read_dir` | Ler todos os arquivos de um diretorio |
| `list_directory` | Listar arquivos e pastas |
| `run_command` | Executar comando shell |
| `search_files` | Buscar arquivos por glob pattern |
| `create_file` | Criar novo arquivo |
| `edit_file` | Editar arquivo (substituicao cirurgica) |
| `delete_file` | Excluir arquivo |
| `patch_file` | Aplicar unified diff |

### Git (16)
`git_status` `git_diff` `git_log` `git_commit` `git_branch` `git_stash` `git_blame` `git_show` `git_pull` `git_push` `git_merge` `git_cherry_pick` `git_tag` `git_clone` `git_init` `git_remote`

### Code Review (3)
`code_review` `lint_check` `test_run` — auto-detecta Go, Node, Python, Rust

### Memoria (4)
`save_memory` `recall_memory` `list_memories` `delete_memory`

### Web (2)
`web_search` (DuckDuckGo + cache) `fetch_url`

### Vision (3)
`analyze_image` `screenshot` `image_info`

### Voz (4)
`voice_record` `voice_transcribe` (Whisper) `voice_speak` (TTS) `voice_available`

### IoT / Hardware (5)
`list_usb_devices` `list_serial_ports` `serial_send` `serial_exchange` `flash_firmware` (Arduino, ESP32, AVR)

### MCP (4)
`mcp_list_servers` `mcp_connect` `mcp_disconnect` `mcp_call`

### Banco de Dados (3)
`db_query` `db_schema` `db_tables` — Postgres, SQLite, MySQL via linguagem natural

### Seguranca (3)
`security_scan` `security_secrets` `security_deps`

### Sandbox Docker (3)
`sandbox_run` `sandbox_script` (Python/Node/Go/Bash/Ruby) `sandbox_available`

### Codebase (3)
`repo_map` `find_symbol` `project_info`

### Notificacoes (3)
`notify_send` `notify_broadcast` `notify_channels` — Telegram, WhatsApp, Webhook

### Agendamento (4)
`scheduler_add` `scheduler_remove` `scheduler_list` `scheduler_run`

### Tracking (2)
`usage_summary` `usage_export` (CSV)

### Sessoes (3)
`list_sessions` `session_info` `export_session`

### Templates (3)
`template_list` `template_apply` `template_create`

### Plugins (4)
`plugin_list` `plugin_install` `plugin_remove` `plugin_create`

### Self-Modification (7)
`read_agent_config` `update_persona` `add_agent_skill` `remove_agent_skill` `create_skill` `list_custom_skills` `delete_custom_skill`

## MCP (Model Context Protocol)

### Como cliente — conecte a qualquer servidor MCP

```yaml
mcp:
  - name: filesystem
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/home"]
    auto_start: true

  - name: github
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_TOKEN: "ghp_..."
    auto_start: true
```

### Como servidor — exponha skills do Polypod via MCP

```bash
./polypod mcp serve
# Outros tools (Claude Desktop, Cursor) podem consumir as skills do Polypod
```

## Hooks (Lifecycle Events)

```yaml
hooks:
  # Bloquear rm -rf
  - name: block-rm
    event: PreToolUse
    type: shell
    matcher: "run_command"
    command: |
      INPUT=$(cat)
      if echo "$INPUT" | grep -q "rm -rf"; then
        echo '{"decision":"deny","message":"rm -rf bloqueado por seguranca"}'
      else
        echo '{"decision":"allow"}'
      fi
    enabled: true

  # Log de todas as tools
  - name: log-tools
    event: PostToolUse
    type: http
    url: "https://hooks.example.com/log"
    enabled: true

  # Auto-lint apos editar arquivo
  - name: auto-lint
    event: PostToolUse
    type: shell
    matcher: "edit_file"
    command: "go vet ./... 2>&1"
    enabled: true
```

Eventos: `SessionStart` `SessionEnd` `PreToolUse` `PostToolUse` `PreCompact` `PostCompact` `UserPrompt` `AssistantResponse` `Error` `Stop`

## Permissoes Granulares

```json
// .polypod/settings.json
{
  "denied_tools": [
    {"pattern": "run_command", "decision": "deny"},
    {"pattern": "delete_file", "decision": "deny"}
  ],
  "ask_tools": [
    {"pattern": "git_push", "decision": "ask"},
    {"pattern": "git_commit", "decision": "ask"}
  ],
  "allowed_tools": [
    {"pattern": "read_*", "decision": "allow"},
    {"pattern": "git_status", "decision": "allow"}
  ]
}
```

Hierarquia: user `~/.polypod/settings.json` < projeto `.polypod/settings.json`

## Checkpoints, Undo, Rewind

- **Auto-checkpoint**: snapshot antes de cada mudanca de arquivo
- **`/undo`**: desfaz ultima mudanca + remove do historico
- **`/redo`**: refaz mudanca desfeita
- **`/rewind`**: volta a qualquer checkpoint anterior
- **Named checkpoints**: `/checkpoint "antes do refactor"`
- **Fork**: criar copia do checkpoint para tentar abordagem alternativa

## Slash Commands de Projeto

```markdown
<!-- .polypod/commands/deploy.md -->
---
name: deploy
description: Deploy para producao
---

Execute o deploy seguindo estes passos:
1. Rode os testes: `go test ./...`
2. Build: `make build-linux`
3. Copie para o servidor
4. Reinicie o servico

Contexto: $ARGUMENTS
```

Use: `/deploy staging`

## Recipes (Runbooks)

```yaml
# .polypod/recipes/release.yaml
name: release
description: Criar release
parameters:
  - name: version
    required: true
steps:
  - name: test
    command: "go test ./..."
  - name: build
    command: "make build-linux"
  - name: tag
    prompt: "Crie tag git v{{version}}"
  - name: changelog
    prompt: "Gere changelog para v{{version}}"
```

## Watch Mode

```bash
./polypod --watch
```

No seu editor, use comentarios para acionar a IA:

```go
// AI: refatore para usar context
func oldFunc() { ... }

// AI! corrija o null pointer
func buggy(s *string) { ... }

// AI? o que este regex faz?
var re = regexp.MustCompile(`...`)
```

## Spec-Driven Development

```
> /spec sistema de autenticacao com JWT e refresh tokens

# Polypod gera automaticamente:
# 1. requirements.md — requisitos funcionais e nao-funcionais
# 2. design.md — arquitetura, interfaces, fluxo de dados
# 3. tasks.md — tarefas ordenadas para implementacao
# Tudo salvo em .polypod/specs/<nome>/
```

## /doctor — Diagnostico

```
> /doctor

## Polypod Doctor

  [OK] Go                      go version go1.24.2 linux/amd64
  [OK] Git                     git version 2.43.0
  [OK] AI Provider             configurada (sk-abc12...)
  [OK] Data dir                data/conversations
  [OK] Agents dir              agents
  [OK] Templates               templates
  [!!] Docker                  nao encontrado (sandbox indisponivel)
  [!!] Whisper (STT)           nao instalado (opcional)
  [OK] Config                  provider=deepseek model=deepseek-chat

Resultado: 7 ok, 2 avisos, 0 falhas
```

## /init — Inicializacao de Projeto

```
> /init

Detectado: Go (Gin), Makefile, Git, Docker
Gerando .polypod.md...
Gerando .polypod/commands/test.md...
Gerando .polypod/settings.json...

3 arquivos criados. Edite .polypod.md para personalizar.
```

## Temas

6 temas built-in: `dark` (padrao), `light`, `monokai`, `dracula`, `solarized`, `nord`

```
> /theme dracula
Tema alterado para dracula.

> /color purple
Cor da sessao: purple.
```

## @File Mentions

Inclua arquivos diretamente no prompt com `@`:

```
> Revise @internal/router/router.go e sugira melhorias

> Compare @go.mod com @go.sum e verifique consistencia

> O que faz @internal/ai/
```

Tab-completion fuzzy para caminhos de arquivo.

## /loop — Tarefas Recorrentes

```
> /loop 5m verifique se o servidor esta respondendo em localhost:8080

Loop #1 criado: a cada 5m

> /loop list
## Loops ativos
- #1 a cada 5m0s | execucoes: 3 | ultima: 14:30:15
  `verifique se o servidor esta respondendo em localhost:8080`

> /loop stop 1
Loop #1 cancelado.
```

## Auto-Memory

Polypod extrai automaticamente decisoes e padroes das conversas e os salva para sessoes futuras:

- Decisoes tecnicas ("optei por usar Redis para cache")
- Padroes do projeto ("sempre usar context.Context como primeiro parametro")
- Preferencias do usuario ("prefiro respostas concisas")

Memorias automaticas sao injetadas como contexto quando relevantes.

## Provider Fallback

Quando o provider primario retorna rate limit (429) ou erro de servidor (5xx), Polypod automaticamente tenta o proximo provider na chain, com mapeamento inteligente de modelos:

```
deepseek-chat → (fallback) → llama3.1 (Ollama) → (fallback) → gpt-4o-mini (OpenAI)
```

## Config Fragments (config.d/)

Distribua configuracoes em equipe sem editar o config principal:

```
config.d/
  00-base.yaml          # defaults da equipe
  10-security.yaml      # politicas de seguranca
  20-team-hooks.yaml    # hooks compartilhados
```

Fragmentos sao mergeados em ordem alfabetica.

## Web Search com Cache

Resultados de busca sao cacheados localmente com TTL configuravel, reduzindo chamadas externas e acelerando respostas repetidas.

## Prompt Templates (8 built-in)

| Template | Categoria | Descricao |
|----------|-----------|-----------|
| `summarize` | analysis | Resumir texto |
| `explain_code` | coding | Explicar codigo |
| `review_code` | coding | Revisar codigo |
| `commit_message` | coding | Gerar commit message |
| `debug` | coding | Analisar erro |
| `translate` | writing | Traduzir para PT-BR |
| `sql_generate` | analysis | Gerar SQL |
| `devops_diagnose` | devops | Diagnosticar infra |

Crie os seus em `templates/meu_template.yaml`.

## Plugin System

```bash
# Instalar de git
> instale o plugin https://github.com/user/polypod-plugin-x

# Criar plugin
> crie um plugin chamado meu-plugin
# Gera template em plugins/meu-plugin/plugin.yaml
```

## Agentes YAML

```yaml
# agents/devops.yaml
name: devops
description: Especialista em DevOps
persona: |
  Voce e um SRE/DevOps senior. Investigue antes de responder.
skills:
  - read_file
  - run_command
  - git_status
  - security_scan
  - db_query
```

## Arquitetura (62 packages)

```
polypod/
├── main.go                      # Entry point, wiring
├── config.yaml                  # Configuracao
├── agents/                      # Agentes YAML
├── templates/                   # Prompt templates
├── internal/
│   ├── adapter/                 # Canais
│   │   ├── cli/                 #   CLI (BubbleTea + Glamour)
│   │   ├── rest/                #   REST API (Chi)
│   │   ├── telegram/            #   Telegram Bot
│   │   └── whatsapp/            #   WhatsApp (Green API)
│   ├── ai/                      # Cliente IA + tool loop + structured output
│   ├── provider/                # Providers (OpenAI, Ollama, Anthropic, Google)
│   ├── fallback/                # Provider fallback chain
│   ├── mcp/                     # MCP Client (stdio + SSE)
│   ├── mcpserver/               # MCP Server (expoe skills)
│   ├── skill/                   # Skill registry + builtins
│   ├── agent/                   # Agent registry
│   ├── router/                  # Pipeline: auth → rate → session → AI
│   ├── config/                  # Config YAML + env vars
│   ├── configmerge/             # Config fragments (config.d/)
│   ├── conversation/            # Sessoes e historico
│   ├── session/                 # Persistencia + compactacao AI
│   ├── memory/                  # Memoria persistente
│   ├── automemory/              # Memoria automatica (extrai decisoes)
│   ├── knowledge/               # RAG (pgvector / SQLite)
│   ├── database/                # Postgres + SQLite
│   ├── auth/                    # Autenticacao
│   ├── ratelimit/               # Rate limiting
│   ├── hooks/                   # Lifecycle hooks (Pre/PostToolUse)
│   ├── permissions/             # Permissoes granulares por tool
│   ├── modes/                   # Modos (plan/ask/edit/auto)
│   ├── checkpoint/              # Checkpoints + rewind
│   ├── undoredo/                # Undo/redo com snapshots
│   ├── commands/                # Slash commands + recipes
│   ├── headless/                # Modo headless (-p)
│   ├── watcher/                 # Watch mode (// AI: triggers)
│   ├── architect/               # Architect/Editor + lint-fix loop
│   ├── diffview/                # Diff preview colorido
│   ├── multiread/               # Leitura multi-arquivo
│   ├── mentions/                # @file mentions + fuzzy autocomplete
│   ├── worktree/                # Git worktrees isolados
│   ├── webui/                   # Browser UI (SSE streaming)
│   ├── plugin/                  # Sistema de plugins
│   ├── template/                # Prompt templates
│   ├── project/                 # .polypod.md loader
│   ├── codemap/                 # Repo-map (simbolos)
│   ├── git/                     # Git skills (16)
│   ├── review/                  # Code review + lint + test
│   ├── vision/                  # Analise de imagens
│   ├── voice/                   # Voz (Whisper + TTS)
│   ├── web/                     # Web search + fetch
│   ├── webcache/                # Cache de resultados web
│   ├── iot/                     # IoT/Hardware
│   ├── selfmod/                 # Auto-modificacao
│   ├── notify/                  # Notificacoes cross-channel
│   ├── scheduler/               # Agendador cron
│   ├── loop/                    # /loop (tarefas recorrentes in-session)
│   ├── tracking/                # Cost/token tracking
│   ├── security/                # Seguranca scanning
│   ├── sandbox/                 # Docker sandbox
│   ├── dbquery/                 # Text-to-SQL
│   ├── doctor/                  # Diagnostico de ambiente
│   ├── initcmd/                 # /init (scaffold de projeto)
│   ├── theme/                   # Temas visuais (6 built-in)
│   ├── search/                  # Busca em historico de sessoes
│   ├── spec/                    # Spec-driven development
│   └── observability/           # Logging
├── cmd/ingest/                  # CLI de ingestao de knowledge
├── scripts/
│   ├── deploy.sh
│   └── polypod.service
└── Makefile
```

## Deploy

### Docker

```dockerfile
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o polypod .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates git bash
COPY --from=builder /app/polypod /usr/local/bin/
COPY --from=builder /app/agents /etc/polypod/agents
COPY --from=builder /app/templates /etc/polypod/templates
ENTRYPOINT ["polypod"]
CMD ["config.yaml"]
```

### Docker Compose (com Traefik)

```yaml
services:
  polypod:
    build: .
    volumes:
      - ./config.yaml:/etc/polypod/config.yaml
      - polypod-data:/data
    networks:
      - traefik-proxy
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.polypod.rule=Host(`ai.example.com`)"
      - "traefik.http.routers.polypod.entrypoints=websecure"
      - "traefik.http.routers.polypod.tls.certresolver=letsencrypt"
      - "traefik.http.services.polypod.loadbalancer.server.port=8080"
    environment:
      - DEEPSEEK_API_KEY=${DEEPSEEK_API_KEY}

volumes:
  polypod-data:

networks:
  traefik-proxy:
    external: true
```

### Systemd

```ini
[Unit]
Description=Polypod AI Gateway
After=network.target

[Service]
Type=simple
User=polypod
ExecStart=/usr/local/bin/polypod /etc/polypod/config.yaml
Restart=always
RestartSec=5
Environment=DEEPSEEK_API_KEY=sk-...

[Install]
WantedBy=multi-user.target
```

## Numeros

| Metrica | Valor |
|---------|-------|
| Arquivos Go | 103 |
| Linhas de codigo | 18.103 |
| Packages | 62 |
| Skills | 110+ |
| Templates | 8 |
| Temas | 6 |
| Providers nativos | 5 |
| Canais | 5 |
| Binario | 26MB |

## Licenca

MIT

## Autor

**Italo Silva** — [github.com/italosilva18](https://github.com/italosilva18)
