# Changelog

All notable changes to Polypod will be documented in this file.

## [0.5.0] - 2026-03-31

### Added
- English README for global audience
- CONTRIBUTING.md with development guide
- LICENSE (MIT)
- CHANGELOG.md
- Renamed `treesitter/` to `repomap/` (honest naming — uses Go AST + regex, not tree-sitter)

### Changed
- Portuguese README moved to LEIAME.md

## [0.4.0] - 2026-03-28

### Added

#### Phase 7: Code Intelligence & Providers
- PageRank repo-map with Go AST parser + reference graph ranking
- Auto-lint/test after AI edits with error feedback loop
- Diff sandbox — staging area for changes (/apply /reject)
- LLM Arena — compare models side-by-side
- 12 new providers: Groq, Mistral, Cohere, OpenRouter, Together, Perplexity, Fireworks, DeepInfra, xAI, Cerebras, SambaNova, Azure (total: 16)

#### Phase 6: SDK Migration
- Migrated Anthropic provider to official SDK (anthropic-sdk-go v1.27.1)
  - Prompt caching (90% cost savings)
  - Extended thinking (enabled/adaptive modes)
  - Citations, PDF support, Batches API
  - Token counting via API
- Migrated MCP client to official SDK (go-sdk v1.4.1)
  - Reduced 1,134 lines custom code to 350 lines
  - Supports MCP spec 2025-11-25

#### Phase 5: UX Polish
- .env auto-loading with security warnings
- Config validation with colored error messages
- Shell completions (bash/zsh/fish)
- Debug mode (/debug system prompt, --debug API calls)
- Interactive model picker with Ollama auto-discovery
- /cost, /stats, /context commands
- Scriptable status line
- Git hook self-installer (prepare-commit-msg, pre-push)
- Auto-update check from GitHub releases
- Background task manager (Ctrl+B)
- PR status in footer
- /insights session analysis

#### Phase 4: Advanced
- Parallel tool execution (goroutines for independent calls)
- Circuit breaker per provider
- Smart model routing by complexity
- AI commit messages from staged diff
- Clipboard integration (/copy /paste)
- Token budget management with alerts
- Test generation (Go/Python/JS/Rust)
- Documentation generation
- Performance profiling (Go pprof + Python cProfile)
- Conversation tree branching
- Export formats (HTML/JSON/Notebook)
- Image generation (/imagine)
- OpenAPI→tools (/openapi load)
- SSH remote execution
- Dependency graph
- DB migrations

#### Phase 3: Maturity
- /doctor environment diagnostics
- /init project scaffolding
- Undo/redo with file snapshots
- Provider fallback chain
- /loop in-session recurring tasks
- 6 visual themes
- @file mentions with fuzzy autocomplete
- Auto-memory (extract decisions from conversations)
- Config fragments (config.d/)
- Web search cache
- Transcript search
- Spec-driven development

#### Phase 2: Platform
- Headless mode (-p flag, stdin piping, JSON output)
- Operation modes (plan/ask/edit/auto)
- Hooks system (PreToolUse/PostToolUse, 10 events)
- Granular permissions (per-tool allow/deny/ask)
- Checkpoints + rewind
- Slash commands (.polypod/commands/*.md)
- Recipes/Runbooks (YAML)
- Watch mode (// AI: triggers)
- Architect/Editor dual-model pattern
- Diff preview (ANSI colored)
- MCP Server mode
- Git worktrees
- Multi-file read
- Browser UI

#### Phase 1: Core
- Multi-provider: OpenAI-compat, Ollama, Anthropic, Google
- MCP Client (stdio + SSE)
- 16 git skills
- File editing with surgical diff
- Code review, linting, testing
- Vision/image analysis
- Voice I/O (Whisper + TTS)
- IoT/Hardware (USB, serial, firmware flash)
- Plugin system (git/local)
- 8 prompt templates
- Session persistence + AI-powered compaction
- .polypod.md project instructions
- Codebase mapper with symbol extraction
- Scheduler/cron
- Cross-channel notifications
- Cost/token tracking
- Security scanning
- Docker sandbox
- Text-to-SQL
- Structured output

#### Infrastructure
- Dockerfile (multi-stage, non-root, healthcheck)
- docker-compose.yml with Traefik labels
- GitHub Actions CI (build + test + vet)
- GitHub Actions Release (5 platforms, checksums)
- 11 test suites, 38 test functions

## [0.3.0] - 2026-03-06

### Added
- Initial release
- CLI (BubbleTea), REST API, Telegram, WhatsApp
- DeepSeek provider
- Basic tool calling
- Memory, RAG, web search
- Self-modification
