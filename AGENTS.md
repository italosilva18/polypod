# AGENTS.md

Guidance for agentic coding agents working in the Polypod repository.

## Project Overview

Polypod is a CLI-only AI coding agent in Go (port of Claude Code patterns). Single binary, Bubbletea TUI, multi-provider LLM support, MCP client, tool orchestration, plan mode, sessions, hooks, permissions. ~750 .go files, ~235k LOC, 192 packages. Module: `github.com/costa/polypod`. Go 1.26.

## Build / Lint / Test Commands

```bash
make build              # Build binary -> ./polypod
make build-linux        # Cross-compile linux/amd64
make build-arm          # Cross-compile linux/arm64
make test               # go test ./...
make fmt                # go fmt ./...
make vet                # go vet ./...
make check              # fmt + vet + test (run before committing)
make run                # Build and run with config.yaml
make run-headless PROMPT="your prompt"  # One-shot headless mode
make clean              # rm -f polypod

# Run a SINGLE package's tests
go test ./internal/provider/ -v

# Run a SINGLE test function (regex match)
go test ./internal/provider/ -v -run TestProviderFactory

# Run with race detector
go test -race ./internal/orchestrator/

# Coverage for one package
go test -cover ./internal/permissions/

# Build a specific package only (faster than full build)
go build ./internal/tool/
```

Always run `make check` (or at least `go vet ./... && go test ./...`) before committing.

## Architecture (CLI-only)

```
User input -> Bubbletea TUI (internal/adapter/cli/)
  -> ai.Service.AnswerStream()
    -> ai.Client.CompleteWithToolsStream() [tool loop, max ~10 iterations]
      -> StreamingToolManager (parallel for read-only, serial for writes)
        -> ToolRegistryAdapter -> orchestrator.ExecuteTool()
          -> permissions.Enforcer.CheckToolCall()
          -> hooks.Manager.Fire(PreToolUse)
          -> tool.Execute()  [actual side effect]
          -> hooks.Manager.Fire(PostToolUse)
          -> automemory.Extract()
      -> ToolEvent callbacks fire UI updates
  -> Bubbletea renders streamed output + tool events + status bar
```

Wiring lives in `internal/wiring/wiring.go` (`WireAll`). `main.go` is the only file that imports all top-level packages.

## Code Style Guidelines

### Imports
- Group in 3 blocks separated by blank lines: stdlib, external, internal (`github.com/costa/polypod/...`).
- Run `go fmt` (or `goimports` if installed) before committing — never hand-format.
- Avoid dot imports. Avoid blank imports unless needed for side effects (e.g. drivers).

### Types and naming
- Package names: lowercase, single word, no underscores (`tool`, `orchestrator`, `permissions`).
- Files under `internal/` only. Public API surfaces stay minimal.
- Exported names use `PascalCase`, unexported use `camelCase`. Acronyms stay uppercase: `URL`, `ID`, `JSON`, `MCP`, `LLM`.
- Tool names are `snake_case`, prefixed by domain: `git_status`, `mcp_connect`, `file_edit`.
- Skill/Agent definitions live in `internal/skill/` and `internal/agent/`. Use `RegisterXxx(reg *Registry)` pattern.
- The `types.Tool` interface uses `*Fn`-suffixed fields (`NameFn`, `ExecuteFn`, etc.) — wrap values in closures.

### Error handling
- Always check errors. Never use `_ = err` to discard.
- Wrap with context: `fmt.Errorf("loading config: %w", err)` (use `%w` for wrapping).
- Sentinel errors live in the package's main file: `var ErrNotFound = errors.New("...")`.
- Tool errors return `types.ToolResult{IsError: true, Content: "..."}` — never panic.
- Use `errors.Is` / `errors.As` for sentinel/typed-error checks, never string comparison.

### Concurrency
- All shared state needs `sync.Mutex` or `sync.RWMutex`. Document field access with comments.
- Pass `context.Context` as the first argument to long-running functions.
- Honor `ctx.Done()` in any loop or blocking call.
- Read-only tools should set `IsReadOnly: true` so they can run in parallel via `StreamingToolManager`.

### Formatting and style
- 1 tab indent, no spaces. Lines wrap at ~120 chars but not enforced.
- Use `strings.Builder` for concatenation in loops, never `s += "..."`.
- Prefer early returns over nested `if err == nil { ... }`.
- Use struct literals with field names: `Foo{Name: "x", Count: 1}` — never positional.
- Avoid `interface{}` / `any` unless necessary (JSON args, generic helpers).

### Logging
- Use `slog.Logger` injected via constructors. Never use `log.Println` or `fmt.Println` for diagnostics.
- Levels: `Debug` for detail, `Info` for state changes, `Warn` for degraded modes, `Error` for failures.
- User-visible messages go through the TUI/router, not the logger.

### Tests
- File pattern: `xxx_test.go` next to the file under test.
- Use table-driven tests with named cases. Test helpers go in `internal/testutil/`:
  - `fakeprovider` — scripted AI provider
  - `fakefs` — in-memory filesystem
  - `fakemcp` — in-process MCP server
  - `golden` — golden-file diffs (use `-update` to regenerate)
  - `fixtures` — embedded sample files
- Use `t.Helper()` in helper functions. Use `t.TempDir()` instead of manual cleanup.

## Conventions

- **User-facing strings in Brazilian Portuguese.** Code, comments, and logs in English.
- **No emojis** in source code unless explicitly requested by the user.
- New packages go under `internal/`. Never create top-level packages.
- Slash commands live in `internal/adapter/cli/commands.go` and follow the `commandResult` pattern.
- Tools register via `Register<Name>Tool(reg *tool.Registry)`. Heavy tools take dependencies through closures, not globals — except for short-lived in-memory state stores marked `globalXxx` (cron jobs, MCP auth, plan mode runtime).
- Config loading: `.env` -> `config.yaml` -> `config.d/*.yaml` -> validate via `internal/configval/`.
- Use `${ENV_VAR:default}` substitution in YAML.
- Permissions: `permissions.Manager.Check(toolName)` always runs in the orchestrator path. Never bypass it from tool code.
- Avoid modifying `main.go`, `internal/adapter/cli/cli.go`, `internal/adapter/cli/model.go`, and `internal/adapter/cli/commands.go` for new features — extend via wiring instead.

## Things to Never Do

- Don't add new top-level slash commands by editing `commands.go` blindly — read it first; many already exist.
- Don't introduce new global mutable state outside `internal/wiring/` except for opt-in stores.
- Don't reintroduce removed adapters (REST, Telegram, WhatsApp, WebUI, IoT, Voice, Vision, Notebook). The user explicitly chose CLI-only.
- Don't add Postgres back. SQLite is the only DB driver.
- Don't run `git push --force`, `git reset --hard`, or destructive git ops without explicit instruction.
- Don't add new dependencies without explaining why in the PR/commit. Prefer stdlib + existing deps.

## Quick Reference

- Main entry: `main.go` (~870 lines, do not bloat)
- Central wiring: `internal/wiring/wiring.go`
- Tool system: `internal/tool/`, `internal/types/tool.go`
- AI client: `internal/ai/client.go`, `internal/ai/service.go`
- Orchestrator: `internal/orchestrator/orchestrator.go`
- Permissions: `internal/permissions/enforcer.go`
- Sessions: `internal/session/persistence.go`, `internal/conversation/`
- TUI: `internal/adapter/cli/model.go` (Bubbletea Update/View)
- Built-in agents: `internal/agent/autonomous.go`, `internal/agent/builtin.go`
