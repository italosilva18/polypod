# Polypod

Polypod is an autonomous, CLI-only AI coding agent written in Go. It ships as
a single binary with a Bubble Tea terminal UI, multi-provider model support,
tool calling, MCP, sessions, hooks, subagents, planning, code intelligence and
an unrestricted execution profile.

Current version: **v0.6.0**

## Status

The v0.6 line is the stabilization release for the large CLI-only rewrite:

- 192 Go packages and more than 220 test files;
- full `go test ./...` and `go vet ./...` validation;
- permission enforcement on every streaming tool path;
- native and autonomous slash commands connected to the terminal UI;
- one binary with no Node or Python runtime requirement.

## Unrestricted mode

This workspace intentionally defaults to `bypassPermissions`. In this mode the
permission manager, orchestrator enforcer and terminal approval handler all
allow tool calls without prompting. The command sandbox is also disabled.

```yaml
permissions:
  mode: bypassPermissions
  defaultMode: bypassPermissions
  allow: []
  deny: []
  ask: []

sandbox:
  enabled: false
```

This gives the agent access to files, processes, network tools, Git, package
managers and system administration commands available to the operating-system
user running Polypod.

## Build and verify

Requirements: Go 1.26 or newer.

```bash
make build
make check
./polypod --version
```

Interactive usage:

```bash
./polypod config.yaml
```

Headless usage:

```bash
./polypod -p "analyze this repository and fix the failing tests"
```

## Core capabilities

- streaming tool execution with parallel read-only operations;
- file read, edit, search and repository mapping;
- unrestricted shell and Git workflows;
- OpenAI-compatible, Anthropic, Google, Ollama and additional providers;
- MCP client, OAuth, reconnect and dynamic tool registration;
- built-in coder, researcher, reviewer, architect and DevOps agents;
- subagents, task tracking and swarm coordination;
- sessions, compaction, memory, checkpoints, undo/redo and conversation trees;
- hooks, plugins, custom skills and hot reloading;
- code review, security analysis, testing, linting and profiling;
- browser/computer-use, SSH, OpenAPI tools and scheduled work;
- token, context-window and cost tracking.

## Configuration

Copy `config.example.yaml` to `config.yaml`, then set credentials through the
environment or `.env`.

```bash
cp config.example.yaml config.yaml
export DEEPSEEK_API_KEY="..."
./polypod config.yaml
```

Secrets should not be embedded in Git remote URLs or committed to the
repository. Runtime credentials can be stored by Polypod's secure-storage
subsystem.

## Architecture

```text
Bubble Tea TUI
  -> AI service and streaming client
    -> streaming tool manager
      -> orchestrator
        -> permission enforcer
        -> hooks
        -> tool registry
        -> memory and session persistence
```

The central wiring is in `internal/wiring/`, permission enforcement in
`internal/permissions/`, tools in `internal/tool/`, and the terminal interface
in `internal/adapter/cli/`.

## Development

```bash
make fmt
make vet
make test
make check
```

See `AGENTS.md` for repository conventions and `CHANGELOG.md` for release
history.

## License

MIT. See `LICENSE`.
