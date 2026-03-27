# Polypod

Gateway de IA multi-canal, multi-provider e auto-modificavel. Um unico binario Go que conecta qualquer LLM a CLI, REST API, Telegram, WhatsApp e browser — com 110+ skills, sistema de plugins, MCP, hooks, e muito mais.

## Destaques

- **Multi-canal**: CLI interativa (BubbleTea), REST API (Chi), Telegram Bot, WhatsApp (Green API), Browser UI
- **Multi-provider**: OpenAI, DeepSeek, Ollama (local), Anthropic (Claude), Google (Gemini) — troque com uma linha no YAML
- **110+ skills**: Git, code review, vision, voz, IoT/hardware, seguranca, sandbox Docker, SQL, e mais
- **MCP Client + Server**: Conecta a 5.800+ servidores MCP e tambem expoe suas skills via MCP
- **Auto-modificavel**: A IA pode alterar sua propria persona, adicionar/remover skills, criar agentes
- **Sistema de hooks**: PreToolUse/PostToolUse com allow/deny/ask — controle total do comportamento
- **Plugins instalaveis**: Instale plugins de repositorios git ou caminhos locais
- **Zero dependencia externa**: Funciona sem banco de dados (JSON em disco), sem Docker, sem nada alem da API key

## Inicio Rapido

```bash
# Compilar
go build -o polypod .

# Primeiro uso (setup interativo)
./polypod --setup

# Executar com config
./polypod config.yaml

# Modo headless (para scripts/CI)
./polypod -p "explique o que faz o arquivo main.go"

# Pipe via stdin
cat error.log | ./polypod -p "analise este log de erro"

# Output JSON
./polypod -p "liste os arquivos Go" --output-format json
```

## Instalacao

### Pre-requisitos

- Go 1.24+
- Uma API key (DeepSeek, OpenAI, Anthropic ou Google)

### Compilar

```bash
git clone https://github.com/italosilva18/polypod.git
cd polypod
make build
```

### Cross-compile para Linux (servidor)

```bash
make build-linux
```

## Configuracao

Crie um `config.yaml` (ou execute `./polypod --setup` para gerar interativamente):

```yaml
# Configuracao minima
ai:
  provider: "deepseek"
  base_url: "https://api.deepseek.com/v1"
  api_key: "${DEEPSEEK_API_KEY}"
  model: "deepseek-chat"
  max_tokens: 4096
  temperature: 0.3
  tools: true

cli:
  enabled: true

# Banco de dados (opcional - sem ele, usa JSON em disco)
database:
  enabled: false

# Ou SQLite (recomendado para uso local)
# database:
#   enabled: true
#   driver: sqlite
#   path: data/polypod.db
```

### Variaveis de ambiente

O config suporta `${ENV_VAR}` e `${ENV_VAR:default}`:

```yaml
ai:
  api_key: "${DEEPSEEK_API_KEY}"
telegram:
  token: "${TELEGRAM_BOT_TOKEN}"
```

## Providers de IA

### DeepSeek (padrao)

```yaml
ai:
  provider: "deepseek"
  base_url: "https://api.deepseek.com/v1"
  api_key: "sk-..."
  model: "deepseek-chat"
```

### OpenAI

```yaml
ai:
  provider: "openai"
  base_url: "https://api.openai.com/v1"
  api_key: "sk-..."
  model: "gpt-4o"
```

### Ollama (local, gratis)

```yaml
ai:
  provider: "ollama"
  base_url: "http://localhost:11434/v1"
  model: "llama3.1"
```

### Anthropic (Claude)

```yaml
providers:
  - name: anthropic
    api_key: "sk-ant-..."

ai:
  provider: "anthropic"
  model: "claude-3-5-sonnet-latest"
```

### Google Gemini

```yaml
providers:
  - name: google
    api_key: "AIza..."

ai:
  provider: "google"
  model: "gemini-1.5-pro"
```

## Canais

### CLI (padrao)

Interface terminal rica com BubbleTea, rendering de markdown com Glamour, streaming em tempo real.

```yaml
cli:
  enabled: true
```

### REST API

```yaml
rest:
  enabled: true
  api_keys:
    - "sua-api-key"

server:
  host: "0.0.0.0"
  port: 8080
```

Endpoints:
- `POST /api/chat` — Mensagem sincrona
- `POST /api/chat/stream` — Streaming SSE
- `GET /api/health` — Health check
- `GET /api/skills` — Listar skills

### Telegram Bot

```yaml
telegram:
  enabled: true
  token: "${TELEGRAM_BOT_TOKEN}"

auth:
  telegram_allowed_ids: [123456789]  # IDs permitidos (vazio = todos)
```

### WhatsApp (Green API)

```yaml
whatsapp:
  enabled: true
  id_instance: "sua-instancia"
  api_token: "seu-token"

auth:
  whatsapp_allowed_numbers: ["+5511999999999"]
```

### Browser UI

```yaml
webui:
  enabled: true
  host: "127.0.0.1"
  port: 8090
```

Acesse `http://localhost:8090` para uma interface web de chat com streaming.

## Skills (110+)

### Sistema

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

### Git (16 skills)

| Skill | Descricao |
|-------|-----------|
| `git_status` | Status do repositorio |
| `git_diff` | Mostrar diff (staged/unstaged) |
| `git_log` | Historico de commits |
| `git_commit` | Criar commit |
| `git_branch` | Gerenciar branches (list/create/delete/switch) |
| `git_stash` | Gerenciar stash |
| `git_blame` | Autoria de linhas |
| `git_show` | Detalhes de commit |
| `git_pull` | Puxar alteracoes |
| `git_push` | Enviar commits |
| `git_merge` | Merge de branch |
| `git_cherry_pick` | Cherry-pick |
| `git_tag` | Gerenciar tags |
| `git_clone` | Clonar repositorio |
| `git_init` | Inicializar repositorio |
| `git_remote` | Info dos remotos |

### Code Review

| Skill | Descricao |
|-------|-----------|
| `code_review` | Obter diff para revisao |
| `lint_check` | Executar linters (auto-detecta linguagem) |
| `test_run` | Executar testes (auto-detecta framework) |

### Memoria

| Skill | Descricao |
|-------|-----------|
| `save_memory` | Salvar fato persistente |
| `recall_memory` | Buscar memoria por topico |
| `list_memories` | Listar todas as memorias |
| `delete_memory` | Excluir memoria |

### Web

| Skill | Descricao |
|-------|-----------|
| `web_search` | Buscar na internet (DuckDuckGo) |
| `fetch_url` | Ler conteudo de URL |

### Vision

| Skill | Descricao |
|-------|-----------|
| `analyze_image` | Analisar imagem local |
| `screenshot` | Capturar screenshot da tela |
| `image_info` | Metadados de imagem |

### Voz

| Skill | Descricao |
|-------|-----------|
| `voice_record` | Gravar audio do microfone |
| `voice_transcribe` | Transcrever audio (Whisper) |
| `voice_speak` | Texto para fala (TTS) |
| `voice_available` | Verificar ferramentas de voz |

### IoT / Hardware

| Skill | Descricao |
|-------|-----------|
| `list_usb_devices` | Listar dispositivos USB |
| `list_serial_ports` | Listar portas seriais |
| `serial_send` | Enviar dados via serial |
| `serial_exchange` | Enviar e receber via serial (AT commands) |
| `flash_firmware` | Gravar firmware (Arduino, ESP32, AVR) |

### MCP

| Skill | Descricao |
|-------|-----------|
| `mcp_list_servers` | Listar servidores MCP conectados |
| `mcp_connect` | Conectar a servidor MCP |
| `mcp_disconnect` | Desconectar servidor MCP |
| `mcp_call` | Chamar ferramenta MCP diretamente |

### Banco de Dados

| Skill | Descricao |
|-------|-----------|
| `db_query` | Executar SQL (Postgres, SQLite, MySQL) |
| `db_schema` | Mostrar schema do banco |
| `db_tables` | Listar tabelas |

### Seguranca

| Skill | Descricao |
|-------|-----------|
| `security_scan` | Varredura de seguranca no codigo |
| `security_secrets` | Detectar segredos hardcoded |
| `security_deps` | Auditoria de dependencias |

### Sandbox (Docker)

| Skill | Descricao |
|-------|-----------|
| `sandbox_run` | Executar comando em container isolado |
| `sandbox_script` | Executar script em sandbox (Python/Node/Go/Bash/Ruby) |
| `sandbox_available` | Verificar disponibilidade do Docker |

### Codebase

| Skill | Descricao |
|-------|-----------|
| `repo_map` | Mapa da estrutura do codebase |
| `find_symbol` | Encontrar definicao de simbolo |
| `project_info` | Info do projeto (linguagem, deps) |

### Notificacoes

| Skill | Descricao |
|-------|-----------|
| `notify_send` | Enviar notificacao (Telegram/WhatsApp/Webhook) |
| `notify_broadcast` | Enviar para todos os canais |
| `notify_channels` | Listar canais configurados |

### Agendamento

| Skill | Descricao |
|-------|-----------|
| `scheduler_add` | Agendar tarefa (cron) |
| `scheduler_remove` | Remover tarefa |
| `scheduler_list` | Listar tarefas agendadas |
| `scheduler_run` | Executar tarefa imediatamente |

### Tracking

| Skill | Descricao |
|-------|-----------|
| `usage_summary` | Resumo de uso e custos |
| `usage_export` | Exportar uso para CSV |

### Sessoes

| Skill | Descricao |
|-------|-----------|
| `list_sessions` | Listar sessoes recentes |
| `session_info` | Detalhes de sessao |
| `export_session` | Exportar sessao para markdown |

### Templates

| Skill | Descricao |
|-------|-----------|
| `template_list` | Listar templates de prompt |
| `template_apply` | Aplicar template a um input |
| `template_create` | Criar novo template |

### Plugins

| Skill | Descricao |
|-------|-----------|
| `plugin_list` | Listar plugins instalados |
| `plugin_install` | Instalar plugin (git/local) |
| `plugin_remove` | Remover plugin |
| `plugin_create` | Criar template de plugin |

### Self-Modification

| Skill | Descricao |
|-------|-----------|
| `read_agent_config` | Ler config do agente |
| `update_persona` | Atualizar persona do agente |
| `add_agent_skill` | Adicionar skill ao agente |
| `remove_agent_skill` | Remover skill do agente |
| `create_skill` | Criar skill customizada (script) |
| `list_custom_skills` | Listar skills customizadas |
| `delete_custom_skill` | Excluir skill customizada |

## MCP (Model Context Protocol)

### Como cliente

Conecte a qualquer servidor MCP:

```yaml
mcp:
  - name: filesystem
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/user"]
    auto_start: true

  - name: github
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_TOKEN: "ghp_..."
    auto_start: true
```

Ou conecte em runtime via skill:
```
> Conecte ao servidor MCP de filesystem
[AI usa mcp_connect com command="npx -y @modelcontextprotocol/server-filesystem /tmp"]
```

### Como servidor

Exponha as skills do Polypod para outros clientes MCP:

```bash
./polypod mcp serve
```

Outros tools (Claude Desktop, Cursor) podem consumir as skills do Polypod via MCP.

## Hooks

Controle o comportamento com hooks de lifecycle:

```yaml
hooks:
  - name: block-rm
    event: PreToolUse
    type: shell
    matcher: "run_command"
    command: |
      echo '{"decision":"deny","message":"rm -rf bloqueado"}'
      # Inspeciona stdin (JSON com tool_name e tool_args)
    enabled: true

  - name: log-tools
    event: PostToolUse
    type: http
    url: "https://hooks.example.com/log"
    enabled: true
```

Eventos disponiveis: `SessionStart`, `SessionEnd`, `PreToolUse`, `PostToolUse`, `PreCompact`, `PostCompact`, `UserPrompt`, `AssistantResponse`, `Error`, `Stop`.

## Modos de Operacao

| Modo | Comportamento |
|------|---------------|
| `edit` | Padrao. Edita arquivos e executa comandos |
| `plan` | Somente analisa e planeja (read-only) |
| `ask` | Somente responde perguntas (sem tools) |
| `auto` | Totalmente autonomo (sem pedir permissao) |

## Permissoes

Controle granular por ferramenta em `.polypod/settings.json`:

```json
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

Hierarquia: user (`~/.polypod/settings.json`) < projeto (`.polypod/settings.json`).

## Checkpoints e Rewind

O Polypod cria snapshots automaticos antes de cada mudanca em arquivo. Use `/rewind` na CLI para voltar a qualquer ponto.

## Slash Commands de Projeto

Crie comandos customizados em `.polypod/commands/`:

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

Contexto adicional: $ARGUMENTS
```

Use com `/deploy staging` na CLI.

## Recipes (Runbooks)

Automatize workflows multi-step em `.polypod/recipes/`:

```yaml
# .polypod/recipes/release.yaml
name: release
description: Criar release
parameters:
  - name: version
    description: Numero da versao
    required: true
steps:
  - name: test
    command: "go test ./..."
  - name: build
    command: "make build-linux"
  - name: tag
    prompt: "Crie uma tag git para a versao {{version}}"
  - name: changelog
    prompt: "Gere um changelog para a versao {{version}} baseado nos commits recentes"
```

## Watch Mode

Monitore arquivos e use comentarios AI para trigger:

```bash
./polypod --watch
```

No seu editor, adicione comentarios:

```go
// AI: refatore esta funcao para usar context
func oldFunction() { ... }

// AI! corrija o bug de null pointer aqui
func buggyFunc(s *string) { ... }

// AI? o que este regex faz?
var re = regexp.MustCompile(`(?m)^func\s+(\w+)`)
```

## Prompt Templates

8 templates embutidos (estilo Fabric):

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

Crie os seus em `templates/`:

```yaml
name: meu_template
description: "Descricao"
category: coding
system: "Voce e um especialista em..."
user: "Analise o seguinte:\n\n{{input}}"
```

## Plugin System

### Instalar plugin

```bash
# De repositorio git
> instale o plugin https://github.com/user/polypod-plugin-x

# De pasta local
> instale o plugin ./meu-plugin
```

### Criar plugin

```yaml
# plugin.yaml
name: meu-plugin
version: "1.0.0"
description: "Descricao do plugin"
skills:
  - name: minha_skill
    description: "O que faz"
    language: python
    script: |
      import json, os
      args = json.loads(os.environ.get("POLYPOD_ARGS", "{}"))
      print(f"Resultado: {args}")
    parameters:
      - name: input
        description: "Input"
        required: true
```

## Agentes

Defina agentes especializados em `agents/`:

```yaml
# agents/devops.yaml
name: devops
description: Especialista em DevOps
persona: |
  Voce e um SRE/DevOps senior...
skills:
  - read_file
  - run_command
  - git_status
  - security_scan
  - db_query
```

## Instrucoes de Projeto

Crie `.polypod.md` na raiz do projeto:

```markdown
# Polypod - Instrucoes do Projeto

## Sobre
Este e um projeto Go que...

## Regras
- Responda em portugues
- Use Conventional Commits
- Sempre rode testes antes de commitar

## Stack
- Go 1.24, Gin, PostgreSQL
```

## Arquitetura

```
polypod/
├── main.go                    # Entry point, wiring
├── config.yaml                # Configuracao
├── agents/                    # Agentes YAML
│   ├── default.yaml
│   └── devops.yaml
├── templates/                 # Prompt templates
├── internal/
│   ├── adapter/               # Canais (CLI, REST, Telegram, WhatsApp)
│   │   ├── cli/               #   CLI interativa (BubbleTea)
│   │   ├── rest/              #   REST API (Chi)
│   │   ├── telegram/          #   Telegram Bot
│   │   └── whatsapp/          #   WhatsApp (Green API)
│   ├── ai/                    # Cliente IA + tool calling loop
│   ├── provider/              # Abstracoes de provider (OpenAI, Ollama, Anthropic, Google)
│   ├── mcp/                   # MCP Client (stdio + SSE)
│   ├── mcpserver/             # MCP Server (expoe skills)
│   ├── skill/                 # Registry de skills + builtins
│   ├── agent/                 # Registry de agentes
│   ├── router/                # Pipeline: auth → rate-limit → session → AI
│   ├── config/                # Configuracao YAML + env vars
│   ├── conversation/          # Sessoes e historico
│   ├── session/               # Persistencia + compactacao
│   ├── memory/                # Memoria persistente
│   ├── knowledge/             # RAG (pgvector / SQLite)
│   ├── database/              # Postgres + SQLite
│   ├── auth/                  # Autenticacao
│   ├── ratelimit/             # Rate limiting
│   ├── hooks/                 # Lifecycle hooks
│   ├── permissions/           # Permissoes granulares
│   ├── modes/                 # Modos de operacao
│   ├── checkpoint/            # Checkpoints + rewind
│   ├── commands/              # Slash commands + recipes
│   ├── headless/              # Modo headless (-p)
│   ├── watcher/               # Watch mode (// AI: triggers)
│   ├── architect/             # Architect/Editor + lint-fix
│   ├── diffview/              # Diff preview colorido
│   ├── multiread/             # Leitura multi-arquivo
│   ├── worktree/              # Git worktrees
│   ├── webui/                 # Browser UI
│   ├── plugin/                # Sistema de plugins
│   ├── template/              # Prompt templates
│   ├── project/               # .polypod.md loader
│   ├── codemap/               # Repo-map (simbolos)
│   ├── git/                   # Git skills
│   ├── review/                # Code review + lint + test
│   ├── vision/                # Analise de imagens
│   ├── voice/                 # Voz (Whisper + TTS)
│   ├── web/                   # Web search + fetch
│   ├── iot/                   # IoT/Hardware
│   ├── selfmod/               # Auto-modificacao
│   ├── notify/                # Notificacoes cross-channel
│   ├── scheduler/             # Agendador cron
│   ├── tracking/              # Cost/token tracking
│   ├── security/              # Seguranca scanning
│   ├── sandbox/               # Docker sandbox
│   ├── dbquery/               # Text-to-SQL
│   └── observability/         # Logging
├── cmd/
│   └── ingest/                # CLI de ingestao de knowledge
├── scripts/
│   ├── deploy.sh
│   └── polypod.service        # Systemd unit
└── Makefile
```

## Config Completa (referencia)

```yaml
server:
  host: "0.0.0.0"
  port: 8080

database:
  enabled: true
  driver: sqlite          # "sqlite" ou "postgres"
  path: data/polypod.db   # para sqlite

ai:
  provider: "deepseek"
  base_url: "https://api.deepseek.com/v1"
  api_key: "${DEEPSEEK_API_KEY}"
  model: "deepseek-chat"
  max_tokens: 4096
  temperature: 0.3
  tools: true

providers:
  - name: ollama
    base_url: "http://localhost:11434"
  - name: anthropic
    api_key: "${ANTHROPIC_API_KEY}"
  - name: google
    api_key: "${GOOGLE_API_KEY}"

embedding:
  enabled: false

cli:
  enabled: true

rest:
  enabled: true
  api_keys: ["${API_KEY}"]

telegram:
  enabled: false
  token: "${TELEGRAM_BOT_TOKEN}"

whatsapp:
  enabled: false
  id_instance: "${WA_INSTANCE}"
  api_token: "${WA_TOKEN}"

webui:
  enabled: false
  host: "127.0.0.1"
  port: 8090

mcp:
  - name: filesystem
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/home"]
    auto_start: true

hooks:
  - name: log-all
    event: PostToolUse
    type: shell
    command: "cat >> /tmp/polypod-tools.log"
    enabled: true

sandbox:
  enabled: false
  image: "ubuntu:22.04"
  memory_limit: "256m"
  timeout: 30

scheduler:
  enabled: true
  data_file: "data/scheduler.json"

notify:
  webhook: ""

plugins:
  dir: "data/plugins"

templates:
  dir: "templates"

auth:
  telegram_allowed_ids: []
  whatsapp_allowed_numbers: []

rate:
  requests_per_minute: 30
  burst_size: 10

log:
  level: "info"
  format: "text"

data:
  dir: "data/conversations"
  agents_dir: "agents"
```

## Deploy (Docker)

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

## Deploy (Systemd)

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
