# Polypod

A CLI de IA mais completa do mundo. Um unico binario Go de 26MB com 130+ skills, 88 packages (100% conectados, zero dead code), 5 providers, 5 canais, MCP client+server, hooks, plugins, modos, parallel tool execution, smart routing, e absolutamente tudo que existe no mercado — mais o que ninguem tem.

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
| Provider fallback + circuit breaker | **Sim** | Nao | Nao | Nao | Nao |
| Smart model routing (auto) | **Sim** | Nao | Nao | Nao | Nao |
| Parallel tool execution | **Sim** | Sim | Nao | Nao | Nao |
| Image generation (/imagine) | **Sim** | Nao | Nao | Nao | Nao |
| SSH remote execution | **Sim** | Nao | Nao | Nao | Nao |
| DB migrations (/migrate) | **Sim** | Nao | Nao | Nao | Nao |
| OpenAPI→tools (/openapi load) | **Sim** | Nao | Nao | Nao | Nao |
| Test generation (/test generate) | **Sim** | Nao | Nao | Nao | Nao |
| Docs generation (/docs) | **Sim** | Nao | Nao | Nao | Nao |
| Profiling (/profile go/python) | **Sim** | Nao | Nao | Nao | Nao |
| Conversation tree branching | **Sim** | Parcial | Nao | Nao | Nao |
| Export (HTML/JSON/Notebook) | **Sim** | Nao | Nao | Nao | Nao |
| Dependency graph (/deps) | **Sim** | Nao | Nao | Nao | Nao |
| Token budget management | **Sim** | Nao | Nao | Nao | Nao |
| Spec-driven development | **Sim** | Nao | Nao | Nao | Nao |
| Scriptable status line | **Sim** | Sim | Nao | Nao | Nao |
| Config validation + .env loading | **Sim** | Sim | Nao | Nao | Nao |
| Git hook self-install | **Sim** | Nao | Nao | Nao | Nao |
| Auto-update check | **Sim** | Sim | Nao | Sim | Nao |
| Background tasks (Ctrl+B) | **Sim** | Sim | Nao | Nao | Nao |
| PR status in footer | **Sim** | Sim | Nao | Nao | Nao |
| Shell completions (bash/zsh/fish) | **Sim** | Nao | Nao | Sim | Nao |
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

./polypod --setup          # Setup interativo
./polypod init             # Gera .polypod.md + commands + settings
./polypod doctor           # Verifica ambiente (API keys, deps, MCP, plugins)
./polypod config.yaml      # Executar CLI interativa
./polypod --version        # Versao atual

# Headless (para scripts/CI)
./polypod -p "explique main.go"
cat error.log | ./polypod -p "analise"
./polypod -p "bugs" --output-format json

# Shell completions
eval "$(./polypod completion bash)"   # bash
polypod completion zsh > ~/.zsh/_polypod  # zsh
polypod completion fish | source      # fish

# Git hooks (commit messages IA + auto-test antes de push)
./polypod hook install
./polypod hook status

# MCP Server (expoe skills para Claude Desktop, Cursor, etc.)
./polypod mcp serve
```

### O que acontece no startup

1. Carrega `.env` e `.env.local` (antes do config, para `${VAR}` funcionar)
2. Carrega e valida `config.yaml` (erros coloridos com sugestoes)
3. Merge `config.d/*.yaml` (config fragments de equipe)
4. Verifica atualizacao no GitHub (async, nao bloqueia)
5. Conecta ao banco de dados (se habilitado)
6. Registra 130+ skills de todos os 88 packages
7. Conecta MCP servers com `auto_start: true`
8. Carrega plugins, templates, commands de projeto
9. Inicializa permissions, modes, hooks, budget
10. Inicia scheduler (se habilitado)
11. Verifica PR aberto (async)
12. Dispara hook `SessionStart`
13. Abre canais (CLI, REST, Telegram, WhatsApp, WebUI)

## Providers

5 providers nativos com **fallback automatico** + **smart routing** + **circuit breaker**:

```yaml
ai:
  provider: "deepseek"                    # Padrao, mais barato
  base_url: "https://api.deepseek.com/v1"
  api_key: "${DEEPSEEK_API_KEY}"          # Carrega de .env automaticamente
  model: "deepseek-chat"

# Fallback: deepseek → ollama → openai
providers:
  - name: ollama
    base_url: "http://localhost:11434"
  - name: openai
    api_key: "${OPENAI_API_KEY}"
  - name: anthropic
    api_key: "${ANTHROPIC_API_KEY}"
  - name: google
    api_key: "${GOOGLE_API_KEY}"
```

- **Smart routing**: classifica complexidade e roteia para modelo barato ou caro (~60% economia)
- **Circuit breaker**: 3 falhas → abre circuito → espera 30s → testa recovery
- **Retry UX**: countdown visivel "Aguardando 1:25..." durante rate-limit

## 5 Canais

| Canal | Config | Descricao |
|-------|--------|-----------|
| CLI | `cli.enabled: true` | BubbleTea + Glamour, 6 temas, streaming |
| REST API | `rest.enabled: true` | `/api/chat`, `/api/chat/stream` (SSE), `/api/skills` |
| Telegram | `telegram.enabled: true` | Bot polling |
| WhatsApp | `whatsapp.enabled: true` | Green API |
| Browser | `webui.enabled: true` | `localhost:8090` SSE streaming |

## Todos os Comandos

### Modo e Controle

| Comando | Descricao |
|---------|-----------|
| `/mode plan\|ask\|edit\|auto` | Modo de operacao |
| `/effort low\|medium\|high\|auto` | Profundidade de raciocinio |
| `/compact [foco]` | Compactar contexto com IA (`/compact foco nas decisoes de API`) |
| `/cost` | Tokens, custo, duracao da sessao |
| `/stats` | Uso diario dos ultimos 7 dias |
| `/context` | Breakdown do context window com barra visual e sugestoes |
| `/budget` | Status de limites de tokens/custo |
| `/model` | Picker interativo de modelos (inclui Ollama auto-discovery) |

### Sessao e Historico

| Comando | Descricao |
|---------|-----------|
| `/undo` | Desfazer ultima mudanca de arquivo |
| `/redo` | Refazer |
| `/rewind` | Voltar a checkpoint anterior |
| `/tree` | Visualizar arvore de conversa (branching) |
| `/fork` | Criar branch na conversa |
| `/history search <query>` | Buscar em sessoes passadas |
| `/export html\|json\|notebook\|markdown` | Exportar sessao |
| `/copy` | Copiar ultima resposta para clipboard |
| `/copy code` | Copiar so blocos de codigo |
| `/paste` | Colar clipboard como input |
| `/insights` | Analise de padroes e otimizacao da sessao |

### Projeto e DevOps

| Comando | Descricao |
|---------|-----------|
| `/init` | Scaffold (.polypod.md + commands + settings) |
| `/doctor` | Diagnostico completo do ambiente |
| `/commit` | Commit message IA do staged diff (Conventional Commits, auto-scope) |
| `/test generate <file>` | Gerar testes (Go table-driven, Python pytest, JS vitest) |
| `/docs readme\|changelog\|api\|godoc` | Gerar documentacao |
| `/profile go\|python <file>` | Performance profiling + flame graph |
| `/deps tree\|circular\|graph` | Grafo de dependencias |
| `/migrate diff\|lint\|apply\|list` | Migrations de banco de dados |
| `/openapi load <url>` | Gerar tools a partir de spec OpenAPI/Swagger |
| `/ssh exec <host> <cmd>` | Executar remoto via SSH |
| `/ssh hosts` | Listar hosts de ~/.ssh/config |
| `/imagine <prompt>` | Gerar imagem (DALL-E/LocalAI) |
| `/security-review` | Analise de seguranca das mudancas pendentes |

### Automacao

| Comando | Descricao |
|---------|-----------|
| `/loop 5m <prompt>` | Tarefa recorrente in-session |
| `/loop list\|stop <id>` | Gerenciar loops |
| `/spec <descricao>` | Spec-driven: requirements → design → tasks |
| `@path/to/file` | Incluir arquivo no prompt (fuzzy autocomplete) |
| `!<comando>` | Executar shell direto (sem IA) |
| `Ctrl+B` | Mover comando para background |
| `Ctrl+T` | Mostrar/esconder task list |
| `Ctrl+G` | Abrir $EDITOR com prompt atual |

### Visual e Config

| Comando | Descricao |
|---------|-----------|
| `/theme dark\|light\|monokai\|dracula\|solarized\|nord` | Tema |
| `/color red\|blue\|green\|purple\|...` | Cor da sessao |
| `/debug [categorias]` | Inspecionar system prompt e API calls |
| `/release-notes` | Changelog embutido |

### Git Hooks

```bash
polypod hook install     # Instala prepare-commit-msg + pre-push
polypod hook uninstall   # Remove hooks
polypod hook status      # Lista hooks ativos
```

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
`openapi_load`

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

### .env Auto-Loading

Polypod carrega `.env` e `.env.local` automaticamente do diretorio do projeto. Precedencia: env vars existentes > .env.local > .env > config.yaml. Avisa se `.env` contem secrets e nao esta no `.gitignore`.

### Config Validation

Ao carregar, valida todos os campos com mensagens coloridas:
```
  [ERRO] ai.api_key: API key nao configurada
         → defina DEEPSEEK_API_KEY no ambiente ou configure ai.api_key
  [WARN] ai.max_tokens: max_tokens muito baixo (100)
         → use pelo menos 1024, recomendado 4096
  2 erro(s), 1 aviso(s)
```

### Retry UX com Countdown

Quando rate-limited, mostra countdown visivel:
```
[deepseek] Aguardando 1:25... (limite de taxa, tentativa 2/3)
```
Classifica erros por tipo (auth, rate_limit, server, timeout, connection) com estrategia diferente para cada.

### Shell Completions

```bash
# Bash
eval "$(polypod completion bash)"

# Zsh
polypod completion zsh > ~/.zsh/completions/_polypod

# Fish
polypod completion fish | source
```

### /debug — Inspecao do System Prompt

```
/debug            # Mostra system prompt montado, tools, memorias
/debug api        # Loga request/response da API
/debug hooks      # Mostra hook events
```

### Model Picker Interativo

```
/model

## Modelos disponiveis

### deepseek
> deepseek-chat
  deepseek-coder
  deepseek-reasoner

### ollama
  llama3.1:8b (4.7B)
  llama3.1:70b (39.2B)
  codellama:13b (7.4B)
```

### /cost + /stats + /context

```
/cost
- Modelo: deepseek-chat
- Duracao: 23m15s
- Tokens: 45.230 prompt + 12.891 completion = 58.121 total
- Custo: $0.0081 USD
- Media: $0.0004/req, 2906 tokens/req

/context
Uso: 12.450 / 128.000 tokens (9%)
[████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░] 9%

System prompt              2.100 tokens  ( 1.6%)
Tool definitions           4.200 tokens  ( 3.3%)
Memorias                     350 tokens  ( 0.3%)
Historico de mensagens     5.800 tokens  ( 4.5%)
Livre                    115.550 tokens  (90.3%)
```

### Scriptable Status Line

Configure um script que recebe JSON e renderiza a barra:

```yaml
# config.yaml
statusline:
  type: command
  command: "~/.polypod/statusline.sh"
```

O script recebe via stdin:
```json
{
  "model": "deepseek-chat",
  "context_used_pct": 9.7,
  "session_tokens": 58121,
  "total_cost_usd": 0.0081,
  "mode": "edit",
  "requests": 20,
  "mcp_servers": 2
}
```

### Git Hooks Self-Install

```bash
polypod hook install
# Instala:
# - prepare-commit-msg: gera commit message IA do staged diff
# - pre-push: roda testes antes do push (auto-detecta Go/Node/Python)

polypod hook uninstall   # Remove
polypod hook status      # Lista hooks ativos
```

### Auto-Update

Verifica GitHub releases no startup. Se nova versao disponivel:
```
[UPDATE] Nova versao disponivel: v0.5.0 (atual: v0.4.0)
  https://github.com/italosilva18/polypod/releases/tag/v0.5.0
```

### Background Tasks (Ctrl+B)

```
> !npm run build     # Ctrl+B para mover para background
Background task #1 iniciada.

> Ctrl+T             # Mostrar task list
## Background Tasks
✓ #1 [done] npm run build (45s)
⏳ #2 [running] go test ./... (12s)
```

### PR Status no Footer

Detecta PR aberto para a branch atual via `gh` CLI:
- Verde: aprovado
- Amarelo: aguardando review
- Vermelho: changes requested
- Cinza: draft

### /insights — Analise da Sessao

```
/insights

## Insights da sessao
- Eficiencia: 2.906 tokens/req, $0.0004/req
- Ratio read/write: 45 leituras / 12 escritas
- Tools mais usadas: read_file (32), edit_file (12), git_diff (8)
- Dica: sessao longa — considere /compact
```

## Arquitetura (88 packages, 100% conectados)

```
polypod/
├── main.go
├── internal/
│   ├── adapter/          # CLI, REST, Telegram, WhatsApp
│   ├── ai/               # Client + tool loop + structured output
│   ├── provider/          # OpenAI, Ollama, Anthropic, Google
│   ├── fallback/          # Provider fallback chain
│   ├── smartroute/        # Smart model routing
│   ├── circuitbreaker/    # Circuit breaker per provider
│   ├── parallel/          # Parallel tool execution
│   ├── mcp/               # MCP Client (stdio + SSE)
│   ├── mcpserver/         # MCP Server
│   ├── skill/             # Skill registry + builtins
│   ├── agent/             # YAML agent registry
│   ├── router/            # auth → rate → session → AI
│   ├── config/            # YAML + env vars
│   ├── configmerge/       # config.d/ fragments
│   ├── configval/         # Config validation
│   ├── dotenv/            # .env file loading
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
│   ├── retryux/           # Retry countdown + error classification
│   ├── hooks/             # Lifecycle hooks
│   ├── permissions/       # Per-tool allow/deny/ask
│   ├── modes/             # plan/ask/edit/auto
│   ├── checkpoint/        # Checkpoints + rewind
│   ├── undoredo/          # Undo/redo snapshots
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
│   ├── debug/             # Debug mode + system prompt inspection
│   ├── modelpicker/       # Interactive model picker
│   ├── costcmd/           # /cost /stats /context
│   ├── statusline/        # Scriptable status line
│   ├── githook/           # Git hook installer
│   ├── autoupdate/        # Version check + update
│   ├── background/        # Background task manager
│   ├── prstatus/          # PR status footer
│   ├── insights/          # Session analysis
│   ├── completion/        # Shell completions (bash/zsh/fish)
│   └── observability/     # Logging
├── agents/                # YAML agents
├── templates/             # 8 prompt templates
├── cmd/ingest/            # Knowledge ingestion
├── scripts/               # Deploy + systemd
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

## Subcomandos

| Comando | Descricao |
|---------|-----------|
| `polypod config.yaml` | Iniciar CLI interativa |
| `polypod --setup` | Setup interativo (gera config.yaml) |
| `polypod -p "prompt"` | Modo headless (stdout, exit) |
| `polypod -p "prompt" --output-format json` | Headless com output JSON |
| `polypod doctor` | Diagnostico do ambiente |
| `polypod init` | Gerar .polypod.md + commands + settings |
| `polypod completion bash\|zsh\|fish` | Gerar shell completions |
| `polypod hook install\|uninstall\|status` | Gerenciar git hooks |
| `polypod mcp serve` | Expor skills via MCP protocol |
| `polypod --version` | Mostrar versao |

## Numeros

| Metrica | Valor |
|---------|-------|
| Arquivos Go | 132 |
| Linhas de codigo | 22.696 |
| Packages | 88 (100% conectados) |
| Dead code | 0 |
| Skills | 130+ |
| Templates | 8 |
| Temas | 6 |
| Providers | 5 |
| Canais | 5 |
| Binario | 26MB |

## Licenca

MIT

## Autor

**Italo Silva** — [github.com/italosilva18](https://github.com/italosilva18)
