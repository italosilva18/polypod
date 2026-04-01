# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
make build              # Build binary
make build-linux        # Cross-compile for Linux amd64
make build-arm          # Cross-compile for Linux arm64
make test               # Run all tests
make check              # fmt + vet + test
make run                # Build and run with config.yaml
make run-headless PROMPT="your prompt"  # Headless mode
make docker             # Build Docker image
make mcp-serve          # Run as MCP server

# Run a single package test
go test ./internal/provider/ -v -run TestProviderFactory

# Run with specific config
./polypod config.local.yaml

# Headless mode
./polypod -p "prompt" config.yaml
./polypod -p "prompt" --output-format json config.yaml

# Subcommands (no config needed)
./polypod doctor
./polypod init
./polypod completion bash
./polypod hook install|uninstall|status
./polypod mcp serve
./polypod --version
```

## Architecture

Polypod is a multi-channel AI gateway in Go. One binary connects any LLM to CLI, REST API, Telegram, WhatsApp, and browser simultaneously.

### Request Flow

```
Channel (CLI/REST/Telegram/WhatsApp/WebUI)
  → adapter.InMessage
    → router.Handler() or router.StreamHandler()
      → auth.Authorizer.IsAllowed()
      → ratelimit.Limiter.Allow()
      → conversation.Manager.GetSession()
      → ai.Service.Answer() or .AnswerStream()
        → ai.Client.CompleteWithTools() [tool-calling loop, max 10 iterations]
          → skill.Registry.Execute() [runs the actual tool]
        → conversation.Manager.AddAssistantMessage()
  → adapter.OutMessage back to channel
```

### Key Interfaces

- **`provider.Provider`** — AI backend abstraction (16 implementations: OpenAI-compat, Ollama, Anthropic SDK, Google). Located in `internal/provider/`.
- **`adapter.Channel`** — Communication channel (CLI, REST, Telegram, WhatsApp). Each has `Start()`, `Send()`, `Name()`. Located in `internal/adapter/`.
- **`skill.Skill`** — A callable tool with `Name`, `Description`, `Parameters` (jsonschema), and `Execute(args map[string]string) (string, error)`. Legacy interface — new tools should use `types.Tool`.
- **`types.Tool`** — Full tool interface with permissions, validation, concurrency safety, read-only/destructive flags. Located in `internal/types/tool.go`.

### Wiring

`main.go` is the only file that imports all packages. The `run()` function (~400 lines) wires everything:

1. Loads .env files → config YAML → config.d/ fragments → validates
2. Initializes database (optional: Postgres, SQLite, or JSON files)
3. Registers all skills from ~30 packages into `skill.Registry`
4. Connects MCP servers, loads plugins, templates, commands
5. Sets up hooks, permissions, modes, tracking, notifications
6. Creates the router pipeline and starts channels

### Dual Skill Systems

There are two coexisting skill systems:

1. **Legacy `skill.Skill`** — Simple struct with `Execute(args map[string]string)`. Most existing skills use this. Parameters use `sashabaranov/go-openai/jsonschema`.
2. **New `types.Tool`** — Full interface with permissions, validation, concurrency checks. Located in `internal/types/tool.go`. Uses `types.BuildTool()` helper.

Both are registered in `skill.Registry` and called via `ai.Client.CompleteWithTools()`.

### Provider Abstraction

`internal/provider/provider.go` defines the `Provider` interface. Implementations:

- `openai_compat.go` — Works with any OpenAI-compatible API (DeepSeek, Groq, Together, etc.)
- `ollama.go` — Native Ollama HTTP API
- `anthropic.go` — Official `anthropic-sdk-go` with prompt caching, extended thinking, vision
- `google.go` — Native Google Gemini API
- `providers.go` — Factory functions for 12+ providers (all delegate to OpenAICompat)

### MCP Integration

Uses official `modelcontextprotocol/go-sdk` v1.4.1. Located in `internal/mcp/`:

- `client.go` — Manages connections to MCP servers, registers their tools as skills
- `registry.go` — Skills for runtime MCP management (connect/disconnect/call)
- `internal/mcpserver/` — Exposes Polypod's skills as an MCP server

### Agent System

YAML-based agents in `agents/` directory. Each agent has a `persona` (system prompt) and a list of allowed `skills`. The `default` agent has full access. The AI service selects which skills to expose based on the active agent.

### Configuration

YAML with `${ENV_VAR:default}` substitution. Loaded in order:
1. `.env` and `.env.local` (via `internal/dotenv/`)
2. `config.yaml` (main config)
3. `config.d/*.yaml` fragments (via `internal/configmerge/`)
4. Validated by `internal/configval/`

### Permissions

`internal/types/permissions.go` defines modes: `ModeDefault`, `ModeBypass`, `ModePlan`, `ModeDontAsk`. The default context uses `ModeBypass` — all operations are allowed without prompting.

## Conventions

- User-facing strings in Portuguese (the primary user communicates in Brazilian Portuguese)
- Code comments and documentation in English
- Skills register via `RegisterSkills(reg *skill.Registry)` pattern
- Config uses YAML with `yaml` struct tags
- All new packages go under `internal/`
- Provider names must match config `providers[].name` field
- Skill names are snake_case, prefixed by domain (e.g., `git_status`, `mcp_connect`)

## Language

The user communicates in Brazilian Portuguese. Respond in Portuguese unless working with code/technical terms.
