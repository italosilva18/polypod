# Contributing to Polypod

Thanks for your interest in contributing! Here's how to get started.

## Development Setup

```bash
# Clone
git clone https://github.com/italosilva18/polypod.git
cd polypod

# Build
make build

# Run tests
make test

# Run with debug config
cp config.example.yaml config.yaml
# Edit config.yaml with your API key
./polypod config.yaml
```

## Project Structure

```
polypod/
├── main.go              # Entry point, CLI args, wiring
├── internal/            # All packages (92 total)
│   ├── provider/        # AI provider abstraction (16 providers)
│   ├── ai/              # AI client + tool calling loop
│   ├── mcp/             # MCP client (official SDK)
│   ├── skill/           # Skill registry + builtins
│   ├── adapter/         # Channel adapters (CLI, REST, Telegram, WhatsApp)
│   ├── router/          # Central pipeline: auth → rate → session → AI
│   └── ...              # See README for full architecture
├── agents/              # YAML agent definitions
├── templates/           # Prompt templates
├── config.example.yaml  # Reference configuration
└── Makefile
```

## How to Contribute

### Report Bugs

Open an issue with:
- Steps to reproduce
- Expected behavior
- Actual behavior
- Config (redact API keys!)
- `polypod doctor` output

### Add a Skill

1. Create `internal/myskill/skills.go`
2. Implement `RegisterSkills(reg *skill.Registry)`
3. Register in `main.go`
4. Add tests in `internal/myskill/skills_test.go`

### Add a Provider

All OpenAI-compatible providers are one-liners in `internal/provider/providers.go`:

```go
func NewMyProvider(apiKey string) *OpenAICompat {
    return NewOpenAICompat("myprovider", apiKey, "https://api.myprovider.com/v1")
}
```

### Add a Test

We need more tests! Priority packages:
- `internal/provider/` (0% coverage — most critical)
- `internal/ai/` (0% coverage)
- `internal/skill/` (0% coverage)
- `internal/conversation/` (0% coverage)

### Code Style

- `go fmt` and `go vet` must pass
- User-facing strings in the user's language (Portuguese for now)
- English for code comments and documentation
- No unnecessary dependencies

## Pull Request Process

1. Fork the repo
2. Create a feature branch (`git checkout -b feat/my-feature`)
3. Write tests for new code
4. Ensure `make check` passes
5. Commit with conventional commits (`feat:`, `fix:`, `docs:`)
6. Open a PR with description of changes

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
