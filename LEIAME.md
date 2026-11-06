# Polypod

O Polypod e um agente autonomo de programacao, exclusivo para terminal e
escrito em Go. Ele combina interface Bubble Tea, multiplos provedores de IA,
ferramentas, MCP, sessoes, hooks, subagentes, planejamento e inteligencia de
codigo em um unico binario.

Versao atual: **v0.6.0**

## Estado atual

A linha v0.6 estabiliza a grande migracao para a arquitetura CLI-only:

- 192 pacotes Go e mais de 220 arquivos de teste;
- `go test ./...` e `go vet ./...` aprovados;
- verificacao de permissao em todos os caminhos de ferramentas;
- comandos nativos e autonomos conectados a interface do terminal;
- binario unico, sem depender de runtime Node ou Python.

## Modo irrestrito

Este workspace usa intencionalmente `bypassPermissions`. Nesse modo, o
gerenciador de permissoes, o enforcer do orquestrador e a interface aprovam
todas as ferramentas sem solicitar confirmacao. O sandbox tambem fica
desativado.

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

Assim, o agente pode usar arquivos, processos, rede, Git, gerenciadores de
pacotes e comandos administrativos disponiveis para o usuario do sistema que
executou o Polypod.

## Compilar e validar

Requisito: Go 1.26 ou superior.

```bash
make build
make check
./polypod --version
```

Uso interativo:

```bash
./polypod config.yaml
```

Uso headless:

```bash
./polypod -p "analise este repositorio e corrija os testes"
```

## Capacidades principais

- leitura, edicao, busca e mapeamento de repositorios;
- shell e fluxos Git irrestritos;
- execucao paralela de ferramentas somente leitura;
- provedores OpenAI-compativeis, Anthropic, Google, Ollama e outros;
- MCP, OAuth, reconexao e registro dinamico de ferramentas;
- agentes coder, pesquisador, revisor, arquiteto e DevOps;
- subagentes, tarefas e coordenacao swarm;
- sessoes, memoria, compactacao, checkpoints, undo/redo e arvores;
- hooks, plugins, skills customizadas e hot reload;
- revisao de codigo, seguranca, testes, lint e profiling;
- browser/computer-use, SSH, OpenAPI e agendamentos;
- acompanhamento de tokens, contexto e custo.

## Configuracao

Copie `config.example.yaml` para `config.yaml` e forneca as credenciais pelo
ambiente ou por `.env`.

```bash
cp config.example.yaml config.yaml
export DEEPSEEK_API_KEY="..."
./polypod config.yaml
```

Segredos nao devem ficar nas URLs dos remotos Git nem ser versionados. O
Polypod possui armazenamento seguro para credenciais de runtime.

## Arquitetura

```text
TUI Bubble Tea
  -> servico de IA e streaming
    -> gerenciador de ferramentas
      -> orquestrador
        -> enforcer de permissoes
        -> hooks
        -> registro de ferramentas
        -> memoria e sessoes
```

O wiring central fica em `internal/wiring/`, as permissoes em
`internal/permissions/`, as ferramentas em `internal/tool/` e a interface em
`internal/adapter/cli/`.

## Desenvolvimento

```bash
make fmt
make vet
make test
make check
```

Consulte `AGENTS.md` para as convencoes e `CHANGELOG.md` para o historico.

## Licenca

MIT. Consulte `LICENSE`.
