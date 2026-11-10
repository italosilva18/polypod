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
- one Go binary for the core agent; optional local Whisper, Piper, document parsing and semantic embeddings use private Python environments.

## Unrestricted mode

This workspace intentionally defaults to `bypassPermissions`. In this mode the
permission manager, orchestrator enforcer and terminal approval handler all
allow tool calls without prompting. The command sandbox is also disabled.
The active `config.yaml` also uses `ai.max_tool_iterations: -1` so the local
runtime does not stop a tool loop after a fixed number of rounds.

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

The local root systemd deployment is documented in [scripts/README.md](scripts/README.md).

## Core capabilities

- streaming tool execution with parallel read-only operations;
- file read, edit, search and repository mapping;
- unrestricted shell and Git workflows;
- OpenAI-compatible, Anthropic, Google, Ollama and additional providers;
- MCP client, OAuth, reconnect and dynamic tool registration;
- built-in coder, researcher, reviewer, architect and DevOps agents;
- subagents, task tracking and swarm coordination;
- sessions, compaction, memory, checkpoints, undo/redo and conversation trees;
- document knowledge index with citations, exact conversation scopes and optional local semantic search;
- durable priority task queue, completion notices and tool audit trail;
- agent-readable persistent secrets through `save_secret`, `get_secret`,
  `list_secrets` and `delete_secret`;
- hooks, plugins, custom skills and hot reloading;
- local PolyClaw skill discovery/import and reviewed evolution from verified outcomes;
- code review, security analysis, testing, linting and profiling;
- browser/computer-use, SSH, OpenAPI tools and scheduled work;
- managed Chrome profile for browser actions, native WhatsApp media intake and
  durable outbound voice, image and document delivery, plus local Whisper audio transcription;
- token, context-window and cost tracking.

The [PolyClaw skill import](docs/integrations/polyclaw-import.md) records the
ported catalog and its limits. See the [document index](docs/integrations/document-index.md)
and [local semantic memory](docs/integrations/semantic-memory.md)
and [WhatsApp audio setup](docs/integrations/audio-transcription.md) for the
new opt-in integrations. [Local voice output](docs/integrations/voice-output.md)
documents the Piper-based OGG/Opus path for WhatsApp voice notes, and
[WhatsApp outbound media](docs/integrations/whatsapp-outbound-media.md) covers file delivery.
The [managed browser](docs/integrations/browser-managed.md) explains the
isolated profile and desktop handoff.
The [remote sessions guide](docs/integrations/remote-sessions.md) shows how to
connect a second terminal to a persistent Polypod session.
The [self-hosted model guide](docs/integrations/self-hosted-model.md) explains
how to connect a private OpenAI-compatible inference server without changing
the existing WhatsApp and scheduler configuration.
The [continuous operations guide](docs/integrations/continuous-operations.md)
explains scheduled routines, inbox recovery and automatic memory.
The [agent runtime guide](docs/integrations/agent-runtime.md) covers background
tasks, interactive terminals, file-triggered work, rewinding and local options.
The [Claude Code behavior audit](docs/integrations/claude-code-behavior-audit.md)
tracks Go rewrites and the remaining gaps without copying reference source.
The [RTK guide](docs/integrations/rtk.md) explains optional compression of
shell command output before it reaches the model.
The [skill catalog](docs/integrations/skill-catalog.md) and
[skill evolution cycle](docs/integrations/skill-evolution.md) cover discovery,
import, evidence, review and application.
The [local plugins guide](docs/integrations/local-plugins.md) covers plugin
installation, updates and rollback from a folder or explicit Git URL.
The [I2C/SPI guide](docs/integrations/hardware-i2c-spi.md) describes device
parameters and the remaining physical verification.
The [USB/serial/firmware guide](docs/integrations/serial-hardware.md) covers
discovery, bounded serial exchanges and supported flash backends.

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

### Open agent secret vault

The default agent may receive credentials in ordinary conversation, save them
with `save_secret`, retrieve their complete plaintext values and use them in
commands, APIs or login flows. The backing `secrets.enc` file is encrypted with
AES-256-GCM and created with mode `0600`; values are intentionally not masked
from the agent. Consequently, credentials included in messages or tool calls
may also be persisted in session history and transmitted to the configured AI
provider.

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
