# Poly em editores via ACP

A Poly atende o [ACP v1](https://agentclientprotocol.com/protocol/v1/overview) por JSON-RPC 2.0, uma mensagem JSON por linha em `stdin`/`stdout`. Execute:

```bash
polypod acp
```

Na instalação local deste computador, o launcher `~/.local/bin/polypod` seleciona o perfil do usuário automaticamente. Em outra instalação, passe o arquivo de configuração: `./polypod acp /caminho/para/config.yaml`. Configure esse comando como agente ACP personalizado no editor. O processo escreve somente mensagens do protocolo em `stdout`; os logs seguem para o arquivo configurado.

## Sessões

- `initialize` negocia ACP v1 e anuncia `loadSession`, `session/list`, `session/resume` e `mcpCapabilities` (`http: true`, `sse: true`, além de `stdio`).
- `session/new`, `session/load` e `session/resume` recebem um `cwd` absoluto existente e `mcpServers` (`stdio`, `http` ou `sse`). Servidores MCP enviados pelo editor são conectados com isolamento estrito por sessão: suas ferramentas (`mcp__<servidor>__<ferramenta>`) ficam visíveis apenas nos turnos daquela sessão ACP e são desconectadas ao substituir a lista ou encerrar o servidor.
- `session/list` filtra pelo `cwd` e pagina até 100 sessões. `session/load` reproduz eventos anteriores, inclusive chamadas de ferramenta e blocos `diff`, a partir do diário unificado de eventos; sessões anteriores à criação do diário reproduzem as mensagens salvas. `session/resume` retoma sem reproduzir o histórico.
- `session/prompt` aceita blocos de texto e envia deltas de resposta e eventos de ferramenta. `session/cancel` e `$/cancel_request` interrompem o turno e retornam `stopReason: cancelled`.
- Chamadas de ferramenta com caminho (`path`, `file_path`, `filePath` ou `notebook_path`) incluem `locations`. Edições feitas por `edit_file`, `write_file`, `file_edit`, `file_write`, `multi_edit`, `undo_edit`, `notebook_edit` e `apply_patch` incluem um bloco ACP `diff` estruturado quando o arquivo de texto tem até 256 KiB; arquivos maiores ou binários continuam com `rawInput` e `rawOutput`.
- O bloqueio de turno por sessão vale entre processos. Uma segunda instância que tentar executar a mesma sessão durante um turno ativo receberá erro; ela pode tentar novamente depois. Isso preserva o histórico e evita ferramentas duplicadas.
- O método de extensão `_polypod/session_events` recebe `sessionId`, `after` e `limit` e devolve eventos versionados com sequência crescente. Ele é anunciado em `agentCapabilities._meta` e permite paginação e retomada por cursor sem executar novamente o turno. O diário fica em `data.dir/session-events` (mantendo leitura compatível de `data.dir/acp-events` para sessões antigas) e é compartilhado com a TUI, o modo headless (`--output-format stream-json`) e os canais duráveis.
- Clientes que precisam repetir uma requisição após queda de conexão podem enviar `_meta: {"github.com/costa/polypod/turnId": "ID-único"}` em `session/prompt` (ou `POLYPOD_TURN_ID` no modo headless). A Poly grava o ID antes de iniciar o modelo ou as ferramentas. Uma repetição com o mesmo prompt devolve o resultado já concluído sem executar as ferramentas outra vez. Se o processo cair durante o turno, o ID permanece como iniciado; consulte `_polypod/session_events` e use um novo ID somente se decidir executar um novo turno. O recurso é opcional e anunciado em `agentCapabilities._meta`.

O adaptador reutiliza o núcleo da Poly e o perfil de execução configurado. As credenciais e servidores MCP globais definidos no perfil continuam disponíveis ao lado dos servidores MCP específicos de cada sessão enviados pelo editor. Imagens, áudio, modos e opções de configuração ACP não são anunciados.

Para conferir a conexão sem usar o modelo:

```bash
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}' | polypod acp
```

O resultado deve conter `"protocolVersion":1` e `"agentCapabilities"`. O editor cria a sessão ao abrir um projeto; não é preciso fornecer o ID manualmente.

## Teste com Neovim

O teste de integração em `scripts/test-acp-neovim.lua` usa o cliente ACP do plugin [Agentic.nvim](https://github.com/carlos-algms/agentic.nvim). Execute o Neovim com o plugin no `runtimepath` e defina `POLYPOD_ACP_BINARY`, `POLYPOD_ACP_CONFIG` e `POLYPOD_ACP_RESULT` (opcionalmente `POLYPOD_ACP_MCP_SERVERS_JSON`, `POLYPOD_ACP_REQUIRE_MCP_TOOL` e `POLYPOD_ACP_REQUIRE_DIFF=1`, usando `scripts/acp-test-mcp-server.py` como servidor MCP de ensaio). O script cria uma sessão, valida ferramentas MCP por sessão, verifica a renderização visual dos blocos `diff` com extmarks no buffer do editor, reconecta via `session/load` e confere a reprodução idempotente dos eventos e diffs. Ele usa o provedor configurado e pode consumir a cota gratuita da API escolhida.
