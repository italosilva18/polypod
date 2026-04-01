# Polypod

The only AI gateway that works everywhere — CLI, REST API, Telegram, WhatsApp, and browser. One Go binary, 16 providers, 135+ skills, official Anthropic & MCP SDKs.

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![CI](https://github.com/italosilva18/polypod/actions/workflows/ci.yml/badge.svg)](https://github.com/italosilva18/polypod/actions)

```
   ____       _                       _
  |  _ \ ___ | |_   _ _ __   ___   __| |
  | |_) / _ \| | | | | '_ \ / _ \ / _` |
  |  __/ (_) | | |_| | |_) | (_) | (_| |
  |_|   \___/|_|\__, | .__/ \___/ \__,_|
                |___/|_|  v0.5.0
```

## Why Polypod?

**No other AI tool runs on 5 channels simultaneously.** Claude Code is CLI-only. Aider is CLI-only. Polypod lets you start a task in the terminal, get notified on Telegram when it's done, and review results from your phone via WhatsApp — all with the same conversation context.

| What makes Polypod unique | Others |
|---------------------------|--------|
| 5 channels (CLI + REST + Telegram + WhatsApp + WebUI) | 1 channel (CLI only) |
| 16 AI providers with automatic fallback | 1-5 providers |
| IoT/Hardware (USB, serial, firmware flash) | None |
| Cross-channel notifications | None |
| Self-modifying AI (changes its own persona/skills) | Limited |
| LLM Arena (compare models side-by-side) | None |
| Single 32MB binary, zero dependencies | Python/Node/npm required |

## Quick Start

```bash
# Install
git clone https://github.com/italosilva18/polypod.git
cd polypod && make build

# First run (interactive setup)
./polypod --setup

# Or initialize project config
./polypod init

# Run
./polypod config.yaml

# Headless mode (for scripts/CI)
./polypod -p "explain this code" config.yaml
cat error.log | ./polypod -p "analyze this error"
./polypod -p "find bugs" --output-format json config.yaml

# Check environment
./polypod doctor

# Shell completions
eval "$(./polypod completion bash)"
```

## 16 Providers

| Provider | Type | Example Models |
|----------|------|---------------|
| DeepSeek | OpenAI-compat | deepseek-chat, deepseek-reasoner |
| OpenAI | OpenAI-compat | gpt-4o, gpt-4o-mini |
| Ollama | Native | llama3.1, codellama (local, free) |
| Anthropic | **Official SDK** | claude-3-5-sonnet, claude-3-opus |
| Google | Native | gemini-1.5-pro, gemini-1.5-flash |
| Groq | OpenAI-compat | llama-3.1-70b (ultra fast) |
| Mistral | OpenAI-compat | mistral-large |
| Cohere | OpenAI-compat | command-r-plus |
| OpenRouter | OpenAI-compat | 100+ models |
| Together | OpenAI-compat | llama-3.1, qwen-2.5 |
| Perplexity | OpenAI-compat | sonar-pro (search-enhanced) |
| Fireworks | OpenAI-compat | llama-3.1-405b |
| DeepInfra | OpenAI-compat | any open-source model |
| xAI | OpenAI-compat | grok-2, grok-3 |
| Cerebras | OpenAI-compat | fastest inference |
| SambaNova | OpenAI-compat | llama-3.1 |

Features: **automatic fallback** (provider fails → next in chain), **smart routing** (classify complexity → cheap/expensive model), **circuit breaker** (3 failures → open → recovery), **prompt caching** (90% cost savings on Anthropic).

## 5 Channels

| Channel | Use Case |
|---------|----------|
| **CLI** | Interactive terminal (BubbleTea + Glamour, streaming, 6 themes) |
| **REST API** | Integration with other systems, CI/CD pipelines |
| **Telegram** | Mobile access, team notifications, on-the-go review |
| **WhatsApp** | Client communication, alerts, mobile coding |
| **Browser UI** | Full web interface at localhost:8090 |

## 135+ Skills

**System**: read_file, read_files, read_dir, list_directory, run_command, search_files, create_file, edit_file, delete_file, patch_file

**Git** (16): status, diff, log, commit, branch, stash, blame, show, pull, push, merge, cherry_pick, tag, clone, init, remote

**Code Quality**: code_review, lint_check, test_run, test_generate, docs_readme, docs_changelog, docs_api, docs_godoc

**Profiling**: profile_go, profile_python, profile_analyze, profile_flame

**Database**: db_query, db_schema, db_tables, migrate_diff, migrate_lint, migrate_apply

**Security**: security_scan, security_secrets, security_deps

**DevOps**: ssh_exec, ssh_hosts, ssh_copy, sandbox_run, sandbox_script, deps_tree, deps_circular

**AI**: imagine (DALL-E/LocalAI), openapi_load (Swagger→tools)

**IoT**: list_usb_devices, list_serial_ports, serial_send, serial_exchange, flash_firmware

**MCP**: mcp_list_servers, mcp_connect, mcp_disconnect, mcp_call

**Memory**: save_memory, recall_memory, list_memories, delete_memory

**Web**: web_search, fetch_url

**Vision**: analyze_image, screenshot, image_info

**Voice**: voice_record, voice_transcribe, voice_speak

**Notifications**: notify_send, notify_broadcast (Telegram + WhatsApp + Webhook)

**Scheduling**: scheduler_add, scheduler_remove, scheduler_list, scheduler_run

**Templates** (8 built-in): summarize, explain_code, review_code, commit_message, debug, translate, sql_generate, devops_diagnose

**Plugins**: plugin_list, plugin_install, plugin_remove, plugin_create

**Self-Modification**: update_persona, add_agent_skill, remove_agent_skill, create_skill

## Key Features

### Official SDKs

- **Anthropic Go SDK** (v1.27.1) — prompt caching (90% savings), extended thinking, citations, PDF, batches API
- **MCP Go SDK** (v1.4.1) — co-maintained by Anthropic + Google

### PageRank Repo Map

Builds a ranked map of your codebase using AST parsing + PageRank algorithm. Symbols most relevant to the current conversation are prioritized in the context window.

### Auto-Lint & Auto-Test

After every AI file edit, automatically runs linter and tests. If errors found, feeds them back to the AI for correction. Supports Go, Python, Node.js, Rust.

### Diff Sandbox

All AI changes are staged before applying. Review the cumulative diff, then `/apply` or `/reject`.

### LLM Arena

Send the same prompt to multiple models in parallel, compare responses side-by-side.

### Operation Modes

| Mode | Behavior |
|------|----------|
| `edit` | Default. Edits files and runs commands |
| `plan` | Read-only analysis. No file modifications |
| `ask` | No tools. Only answers questions |
| `auto` | Fully autonomous. No confirmations |

### Hooks System

PreToolUse/PostToolUse lifecycle hooks with allow/deny/ask decisions. Shell and HTTP handlers.

### MCP Client + Server

Connect to any MCP server (5,800+ available). Also expose Polypod's skills as an MCP server for Claude Desktop, Cursor, etc.

## Configuration

```yaml
ai:
  provider: "deepseek"
  api_key: "${DEEPSEEK_API_KEY}"  # auto-loaded from .env
  model: "deepseek-chat"
  tools: true

cli:
  enabled: true

# Add more providers for fallback
providers:
  - name: ollama
    base_url: "http://localhost:11434"
  - name: groq
    api_key: "${GROQ_API_KEY}"
```

See [config.example.yaml](config.example.yaml) for all options.

## Deploy

### Single Binary

```bash
make build          # local
make build-linux    # Linux amd64
make build-arm      # Linux arm64
```

### Docker

```bash
docker build -t polypod .
docker run -v ./config.yaml:/etc/polypod/config.yaml polypod
```

### Docker Compose + Traefik

```yaml
services:
  polypod:
    build: .
    labels:
      - "traefik.http.routers.polypod.rule=Host(`ai.example.com`)"
```

## Stats

| Metric | Value |
|--------|-------|
| Go files | 146 (135 source + 11 test) |
| Lines of code | 24,725 |
| Packages | 92 |
| Test suites | 11 (all passing) |
| Skills | 135+ |
| Providers | 16 |
| Channels | 5 |
| Binary size | 32MB |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

MIT — see [LICENSE](LICENSE).

## Author

**Italo Silva** — [@italosilva18](https://github.com/italosilva18)

---

[Leia em Portugues](LEIAME.md)
