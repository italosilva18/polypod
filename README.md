# Polypod

A CLI de IA mais completa do mundo. Um unico binario Go de 26MB com 130+ skills, 78 packages, 5 providers, 5 canais, MCP, hooks, plugins, modos, e tudo que existe no mercado — mais o que ninguem tem.

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
| Multi-canal (CLI+REST+TG+WA+Web) | **Sim** | Nao | Nao | Nao | Nao |
| IoT/Hardware (USB, serial, firmware) | **Sim** | Nao | Nao | Nao | Nao |
| Auto-modificacao (persona, skills) | **Sim** | Parcial | Nao | Nao | Nao |
| Cross-channel notifications | **Sim** | Nao | Nao | Nao | Nao |
| Scheduler/cron + /loop | **Sim** | Parcial | Nao | Nao | Nao |
| Provider fallback automatico | **Sim** | Nao | Nao | Nao | Nao |
| Smart model routing (auto) | **Sim** | Nao | Nao | Nao | Nao |
| Parallel tool execution | **Sim** | Sim | Nao | Nao | Nao |
| Circuit breaker per-provider | **Sim** | Nao | Nao | Nao | Nao |
| Image generation (/imagine) | **Sim** | Nao | Nao | Nao | Nao |
| SSH remote execution | **Sim** | Nao | Nao | Nao | Nao |
| DB migrations (/migrate) | **Sim** | Nao | Nao | Nao | Nao |
| OpenAPI→tools (/openapi load) | **Sim** | Nao | Nao | Nao | Nao |
| Test generation (/test generate) | **Sim** | Nao | Nao | Nao | Nao |
| Docs generation (/docs) | **Sim** | Nao | Nao | Nao | Nao |
| Profiling integration (/profile) | **Sim** | Nao | Nao | Nao | Nao |
| Conversation tree branching | **Sim** | Parcial | Nao | Nao | Nao |
| Export (HTML/JSON/Notebook) | **Sim** | Nao | Nao | Nao | Nao |
| Dependency graph (/deps) | **Sim** | Nao | Nao | Nao | Nao |
| Clipboard (/copy /paste) | **Sim** | Plugin | Nao | Nao | Nao |
| Token budget management | **Sim** | Nao | Nao | Nao | Nao |
| Spec-driven development | **Sim** | Nao | Nao | Nao | Nao |
| MCP Client + Server | **Sim** | Sim | Nao | Sim | Nao |
| Hooks (PreToolUse/PostToolUse) | **Sim** | Sim | Nao | Nao | Nao |
| Modos (plan/ask/edit/auto) | **Sim** | Sim | Parcial | Sim | Sim |
| Watch mode (// AI: triggers) | **Sim** | Nao | Sim | Nao | Nao |
| Plugin system | **Sim** | Sim | Nao | Sim | Nao |
| Browser UI | **Sim** | Nao | Sim | Nao | Nao |
| Binario unico, zero deps | **Sim** | Nao | Nao | Nao | Nao |
| Open source | **Sim** | Nao | Sim | Sim | Sim |

## Inicio Rapido

```bash
git clone https://github.com/italosilva18/polypod.git
cd polypod && make build

# Setup interativo (detecta stack, gera config)
./polypod --setup

# Inicializar projeto (.polypod.md + commands + settings)
./polypod init

# Executar
./polypod config.yaml

# Modo headless
./polypod -p "explique main.go"
cat error.log | ./polypod -p "analise"
./polypod -p "liste bugs" --output-format json

# Diagnostico
./polypod doctor
```

## Providers de IA

5 providers nativos com fallback automatico:

```yaml
# DeepSeek (padrao, mais barato)
ai:
  provider: "deepseek"
  base_url: "https://api.deepseek.com/v1"
  api_key: "${DEEPSEEK_API_KEY}"
  model: "deepseek-chat"

# Ollama (local, gratis, offline)
# ai:
#   provider: "ollama"
#   base_url: "http://localhost:11434/v1"
#   model: "llama3.1"

# OpenAI / Anthropic / Google — configure em providers:[]
```

**Fallback automatico**: se DeepSeek falhar (429/5xx), tenta Ollama, depois OpenAI — com mapeamento inteligente de modelos.

**Smart routing**: `/effort auto` classifica complexidade do prompt e roteia para modelo barato (simples) ou caro (complexo). ~60% economia.

**Circuit breaker**: 3 falhas → abre circuito → espera 30s → testa recovery. Per-provider.

## 5 Canais

| Canal | Config | Descricao |
|-------|--------|-----------|
| CLI | `cli.enabled: true` | BubbleTea + Glamour, streaming, 6 temas |
| REST API | `rest.enabled: true` | `/api/chat`, `/api/chat/stream` (SSE), `/api/skills` |
| Telegram | `telegram.enabled: true` | Bot com polling |
| WhatsApp | `whatsapp.enabled: true` | Via Green API |
| Browser | `webui.enabled: true` | `localhost:8090` com SSE |

## Comandos da CLI

### Modos e Controle

| Comando | Descricao |
|---------|-----------|
| `/mode plan\|ask\|edit\|auto` | Mudar modo de operacao |
| `/effort low\|medium\|high\|auto` | Controlar profundidade de raciocinio |
| `/compact [foco]` | Compactar contexto com IA (ex: `/compact foco nas decisoes de API`) |
| `/cost` | Tokens e custo da sessao |
| `/context` | Diagnostico do context window |
| `/budget` | Status de limites de tokens/custo |

### Sessao e Historico

| Comando | Descricao |
|---------|-----------|
| `/undo` | Desfazer ultima mudanca |
| `/redo` | Refazer |
| `/rewind` | Voltar a checkpoint anterior |
| `/tree` | Visualizar arvore de conversa (branching) |
| `/fork` | Criar branch na conversa |
| `/history search <query>` | Buscar em sessoes passadas |
| `/export html\|json\|notebook\|markdown` | Exportar sessao |
| `/copy` | Copiar ultima resposta para clipboard |
| `/copy code` | Copiar so blocos de codigo |
| `/paste` | Colar do clipboard como input |

### Projeto e DevOps

| Comando | Descricao |
|---------|-----------|
| `/init` | Scaffold do projeto (.polypod.md + commands + settings) |
| `/doctor` | Diagnostico do ambiente |
| `/commit` | Gerar commit message IA do staged diff (Conventional Commits) |
| `/test generate <file>` | Gerar testes unitarios (Go/Python/JS) |
| `/docs readme\|changelog\|api\|godoc` | Gerar documentacao |
| `/profile go\|python <file>` | Performance profiling |
| `/deps tree\|circular\|graph` | Grafo de dependencias |
| `/migrate diff\|lint\|apply\|list` | Migrations de banco de dados |
| `/openapi load <url>` | Gerar tools a partir de spec OpenAPI |
| `/ssh exec <host> <cmd>` | Executar comando remoto via SSH |
| `/ssh hosts` | Listar hosts de ~/.ssh/config |
| `/imagine <prompt>` | Gerar imagem (DALL-E/LocalAI) |

### Automacao

| Comando | Descricao |
|---------|-----------|
| `/loop 5m <prompt>` | Tarefa recorrente in-session |
| `/loop list\|stop <id>` | Gerenciar loops |
| `/spec <descricao>` | Gerar requirements → design → tasks |
| `@path/to/file` | Incluir arquivo no prompt (autocomplete) |

### Visual

| Comando | Descricao |
|---------|-----------|
| `/theme dark\|light\|monokai\|dracula\|solarized\|nord` | Mudar tema |
| `/color red\|blue\|green\|purple\|...` | Cor da sessao |

## Skills (130+)

### Sistema (10)
`read_file` `read_files` `read_dir` `list_directory` `run_command` `search_files` `create_file` `edit_file` `delete_file` `patch_file`

### Git (16)
`git_status` `git_diff` `git_log` `git_commit` `git_branch` `git_stash` `git_blame` `git_show` `git_pull` `git_push` `git_merge` `git_cherry_pick` `git_tag` `git_clone` `git_init` `git_remote`

### Code Quality (3)
`code_review` `lint_check` `test_run`

### Test Generation (1)
`test_generate` — Go (table-driven), Python (pytest), JS (vitest), Rust (#[test])

### Documentation (4)
`docs_readme` `docs_changelog` `docs_api` `docs_godoc`

### Profiling (4)
`profile_go` `profile_analyze` `profile_flame` `profile_python`

### Memoria (4)
`save_memory` `recall_memory` `list_memories` `delete_memory`

### Web (2)
`web_search` `fetch_url` — com cache local

### Vision (3)
`analyze_image` `screenshot` `image_info`

### Image Generation (1)
`imagine` — DALL-E, LocalAI, Stable Diffusion

### Voz (4)
`voice_record` `voice_transcribe` `voice_speak` `voice_available`

### IoT/Hardware (5)
`list_usb_devices` `list_serial_ports` `serial_send` `serial_exchange` `flash_firmware`

### MCP (4)
`mcp_list_servers` `mcp_connect` `mcp_disconnect` `mcp_call`

### SSH Remote (3)
`ssh_exec` `ssh_hosts` `ssh_copy`

### Banco de Dados (3)
`db_query` `db_schema` `db_tables`

### Migrations (4)
`migrate_diff` `migrate_list` `migrate_apply` `migrate_lint`

### OpenAPI (1)
`openapi_load` — gera tools automaticamente de specs Swagger/OpenAPI

### Dependencies (3)
`deps_tree` `deps_circular` `deps_graph_image`

### Seguranca (3)
`security_scan` `security_secrets` `security_deps`

### Sandbox Docker (3)
`sandbox_run` `sandbox_script` `sandbox_available`

### Codebase (3)
`repo_map` `find_symbol` `project_info`

### Notificacoes (3)
`notify_send` `notify_broadcast` `notify_channels`

### Agendamento (4)
`scheduler_add` `scheduler_remove` `scheduler_list` `scheduler_run`

### Tracking (2)
`usage_summary` `usage_export`

### Sessoes (3)
`list_sessions` `session_info` `export_session`

### Templates (3)
`template_list` `template_apply` `template_create`

### Plugins (4)
`plugin_list` `plugin_install` `plugin_remove` `plugin_create`

### Self-Modification (7)
`read_agent_config` `update_persona` `add_agent_skill` `remove_agent_skill` `create_skill` `list_custom_skills` `delete_custom_skill`

## Features Avancadas

### Parallel Tool Execution

Quando a IA retorna multiplos tool calls, os independentes (leituras) executam em goroutines simultaneas. 4x mais rapido.

### Smart Model Routing

Classifica complexidade do prompt automaticamente:
- **Baixa** (lista, traduza, formate) → modelo barato
- **Media** (explique codigo, review) → modelo balanceado
- **Alta** (arquitetura, debug, refactor) → modelo frontier

### Circuit Breaker

Per-provider, 3 estados: Closed (normal) → Open (3 falhas, rejeita) → Half-Open (testa recovery apos 30s).

### Conversation Tree

```
> /tree

*
|-- [user] Crie uma API REST...
|   |-- [assistant] Vou criar com Gin...
|   |   |-- [user] Use Fiber em vez de Gin
|   |   |   \-- > [assistant] Ok, refatorando para Fiber...
|   |   \-- [user] Adicione autenticacao JWT
|   |       \-- [assistant] Adicionando middleware JWT...
```

Fork em qualquer mensagem, navegacao entre branches.

### Export Formatos

```bash
/export html      # HTML standalone com syntax highlight
/export json      # JSON estruturado com metadados
/export notebook  # Jupyter notebook (.ipynb)
/export markdown  # Markdown limpo
```

### /commit (Diff-Aware)

Analisa `git diff --staged` semanticamente, classifica mudancas, auto-detecta scope, gera Conventional Commit:

```
feat(api): add JWT authentication middleware

- Add auth/jwt.go with token generation and validation
- Add middleware/auth.go with route protection
- Update router.go with protected routes
```

### /test generate

```bash
/test generate internal/auth/jwt.go
# → Gera jwt_test.go com table-driven tests
# → Inclui: sucesso, token expirado, token invalido, claims errados
```

### /docs

```bash
/docs readme      # README a partir da estrutura do projeto
/docs changelog --since v1.0.0  # Changelog categorizado
/docs api internal/router/  # Documentacao de API
/docs godoc internal/auth/  # Comentarios godoc
```

### /profile

```bash
/profile go "go test" --type cpu    # pprof CPU profiling
/profile analyze /tmp/cpu.prof      # Analise de hotspots
/profile flame /tmp/cpu.prof        # Flame graph SVG
/profile python script.py           # cProfile Python
```

### /deps

```bash
/deps tree         # Arvore de dependencias (Go/Node/Python)
/deps circular     # Detectar ciclos
/deps graph deps.svg  # Grafo visual (Graphviz)
```

### /migrate

```bash
/migrate diff add_users_table   # Gerar migration SQL
/migrate lint                   # Validar seguranca (DROP TABLE, etc.)
/migrate apply --dsn postgres://... --dry_run true
/migrate list                   # Listar migrations
```

### /openapi load

```bash
/openapi load https://petstore.swagger.io/v2/swagger.json
# → Parseia 20 endpoints, gera tool definitions
# → Salva em .polypod/tools/openapi_endpoints.json
```

### /ssh

```bash
/ssh hosts          # Lista hosts de ~/.ssh/config
/ssh exec prod-1 "docker ps"
/ssh copy ./deploy.sh prod-1:/opt/app/
```

### /imagine

```bash
/imagine "um gato programando em Go, pixel art"
# → Gera imagem via DALL-E ou LocalAI
# → Salva em polypod-images/<timestamp>.png
```

### Token Budget

```yaml
budget:
  per_session: 100000    # max tokens por sessao
  per_day: 500000        # max tokens por dia
  max_cost_day: 5.00     # max $5/dia
  max_cost_month: 50.00  # max $50/mes
  alert_at_50: true
  alert_at_80: true
  auto_downgrade: true   # muda para modelo barato perto do limite
```

### Clipboard

```bash
/copy        # Copia ultima resposta
/copy code   # Copia so blocos de codigo
/paste       # Cola clipboard como input
```

## MCP

### Cliente — conecte a 5.800+ servers

```yaml
mcp:
  - name: filesystem
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/home"]
    auto_start: true
```

### Servidor — exponha skills do Polypod

```bash
./polypod mcp serve
# Claude Desktop, Cursor, etc. podem consumir as 130+ skills
```

## Hooks

```yaml
hooks:
  - name: block-rm
    event: PreToolUse
    type: shell
    matcher: "run_command"
    command: |
      INPUT=$(cat)
      if echo "$INPUT" | grep -q "rm -rf"; then
        echo '{"decision":"deny","message":"bloqueado"}'
      fi
    enabled: true
```

10 eventos: `SessionStart` `SessionEnd` `PreToolUse` `PostToolUse` `PreCompact` `PostCompact` `UserPrompt` `AssistantResponse` `Error` `Stop`

## Permissoes

```json
{
  "denied_tools": [{"pattern": "delete_file", "decision": "deny"}],
  "ask_tools": [{"pattern": "git_push", "decision": "ask"}],
  "allowed_tools": [{"pattern": "read_*", "decision": "allow"}]
}
```

## Arquitetura (78 packages)

```
polypod/
├── main.go
├── internal/
│   ├── adapter/          # CLI, REST, Telegram, WhatsApp
│   ├── ai/               # Client + tool loop + structured output
│   ├── provider/          # OpenAI, Ollama, Anthropic, Google
│   ├── fallback/          # Provider fallback chain
│   ├── smartroute/        # Smart model routing by complexity
│   ├── circuitbreaker/    # Circuit breaker per provider
│   ├── parallel/          # Parallel tool execution
│   ├── mcp/               # MCP Client (stdio + SSE)
│   ├── mcpserver/         # MCP Server
│   ├── skill/             # Skill registry + builtins
│   ├── agent/             # YAML agent registry
│   ├── router/            # auth → rate → session → AI pipeline
│   ├── config/            # YAML + env vars
│   ├── configmerge/       # config.d/ fragments
│   ├── conversation/      # Sessions + history
│   ├── session/           # Persistence + AI compaction
│   ├── convtree/          # Conversation tree branching
│   ├── memory/            # Persistent memory
│   ├── automemory/        # Auto-extract decisions
│   ├── knowledge/         # RAG (pgvector/SQLite)
│   ├── database/          # Postgres + SQLite
│   ├── auth/              # Authentication
│   ├── ratelimit/         # Rate limiting
│   ├── budget/            # Token budget management
│   ├── hooks/             # Lifecycle hooks
│   ├── permissions/       # Per-tool allow/deny/ask
│   ├── modes/             # plan/ask/edit/auto
│   ├── checkpoint/        # Checkpoints + rewind
│   ├── undoredo/          # Undo/redo with snapshots
│   ├── commands/          # Slash commands + recipes
│   ├── headless/          # -p flag, stdin, JSON output
│   ├── watcher/           # Watch mode (// AI: triggers)
│   ├── architect/         # Dual-model + lint-fix loop
│   ├── diffview/          # Colored diff preview
│   ├── multiread/         # Multi-file read (glob)
│   ├── mentions/          # @file fuzzy autocomplete
│   ├── worktree/          # Git worktrees
│   ├── webui/             # Browser UI (SSE)
│   ├── plugin/            # Plugin system
│   ├── template/          # Prompt templates (8 built-in)
│   ├── project/           # .polypod.md loader
│   ├── codemap/           # Repo-map + symbols
│   ├── git/               # 16 git skills
│   ├── review/            # Code review + lint + test
│   ├── testgen/           # Test generation
│   ├── docgen/            # Documentation generation
│   ├── profiling/         # Performance profiling
│   ├── commitai/          # AI commit messages
│   ├── clipboard/         # System clipboard
│   ├── vision/            # Image analysis
│   ├── imagine/           # Image generation
│   ├── voice/             # Whisper + TTS
│   ├── web/               # Web search + fetch
│   ├── webcache/          # Search result cache
│   ├── iot/               # IoT/Hardware
│   ├── selfmod/           # Self-modification
│   ├── notify/            # Cross-channel notifications
│   ├── scheduler/         # Cron scheduler
│   ├── loop/              # /loop in-session recurring
│   ├── tracking/          # Cost/token tracking
│   ├── security/          # Security scanning
│   ├── sandbox/           # Docker sandbox
│   ├── dbquery/           # Text-to-SQL
│   ├── openapitools/      # OpenAPI → tools
│   ├── sshexec/           # SSH remote execution
│   ├── depsgraph/         # Dependency graph
│   ├── migrate/           # DB migrations
│   ├── export/            # HTML/JSON/Notebook export
│   ├── doctor/            # Environment diagnostics
│   ├── initcmd/           # Project scaffold
│   ├── theme/             # 6 visual themes
│   ├── search/            # Transcript search
│   ├── spec/              # Spec-driven development
│   └── observability/     # Logging
├── agents/                # YAML agent definitions
├── templates/             # 8 prompt templates
├── cmd/ingest/            # Knowledge ingestion CLI
├── scripts/               # Deploy + systemd
└── Makefile
```

## Numeros

| Metrica | Valor |
|---------|-------|
| Arquivos Go | 119 |
| Linhas de codigo | 20.899 |
| Packages | 78 |
| Skills | 130+ |
| Templates | 8 |
| Temas | 6 |
| Providers | 5 |
| Canais | 5 |
| Binario | 26MB |

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

### Docker Compose + Traefik

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

[Install]
WantedBy=multi-user.target
```

## Licenca

MIT

## Autor

**Italo Silva** — [github.com/italosilva18](https://github.com/italosilva18)
