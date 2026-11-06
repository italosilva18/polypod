# PolyClaw: integração MCP opcional

Estado: fallback opcional para integração de ferramentas, sem conexão ao vivo.
A direção atual é fusão seletiva de capacidades no Polypod; o contrato MCP abaixo
fica como referência, não como requisito para essa fusão. O PolyClaw em
`/home/costa/picoclaw-src` já possui WhatsApp e cliente MCP; ainda precisa expor
um **servidor MCP**. Dois clientes MCP não se conectam entre si. Este trabalho
não altera esse repositório nem inicia outra sessão de WhatsApp.

O Polypod continua responsável pelo raciocínio, histórico, permissões e fila
persistente. O PolyClaw fornece canais e ferramentas. A interface abaixo é um
contrato proposto para implementar no servidor; os nomes não são comandos já
existentes no PolyClaw.

## Transporte e descoberta

Preferir um servidor **Streamable HTTP dentro do processo do gateway PolyClaw
já existente**, reutilizando sua sessão WhatsApp. O Polypod conecta ao endpoint
loopback autenticado, por exemplo `http://127.0.0.1:18792/mcp`; não inicia outro
gateway nem abre outra sessão WhatsApp. A porta é ilustrativa, não um serviço
instalado. Usar `transport: http` (alias `streamable-http`). O SDK oficial atende
`initialize`, `tools/list` e `tools/call`; `headers` permite credencial Bearer.
O cliente não segue redirecionamentos para evitar encaminhar credenciais.

O servidor deve bindar somente em loopback por padrão, exigir autenticação em
todas as requisições e validar Origin/Host. Loopback não substitui autenticação.
O contrato não autoriza exposição externa; acesso remoto requer configuração
explícita e TLS. SSE autenticado (`transport: sse`) é alternativa caso o servidor
não implemente Streamable HTTP.

Stdio permanece disponível para ferramentas independentes que não iniciem nem
dupliquem o gateway WhatsApp. Nesse transporte stdout contém apenas JSON-RPC;
logs vão para stderr. O processo herda o ambiente do Polypod, com substituições
explícitas de `env`. Não devolver segredos no resultado de ferramentas.

As ferramentas devem publicar JSON Schema completo, preservando tipos,
`required`, objetos, arrays e restrições. Resultados visuais usam blocos MCP
`image` com `mimeType` e `data` em base64. Textos e `structuredContent` são
preservados; áudio, vídeos e recursos anexados ainda precisam de conversão própria.

## Contrato de ferramentas proposto

| Ferramenta | Entrada | Resultado e comportamento |
| --- | --- | --- |
| `whatsapp_events_poll` | `cursor: string`, `limit: integer`, `wait_ms: integer` | `events: array`, `next_cursor: string`; reentrega até confirmação, sem remover eventos ao ler |
| `whatsapp_events_ack` | `event_ids: string[]` | Confirmação idempotente dos eventos já gravados na inbox local |
| `whatsapp_send` | `chat_id: string`, `text: string`, `idempotency_key: string`, `reply_to?: string` | `message_id`, `status`; repetir a chave não envia outra mensagem |
| `computer_observe` | `display?: string` | Texto e imagem MCP do estado atual, com dimensões e sistema de coordenadas |
| `computer_action` | `action: string`, `x?: integer`, `y?: integer`, `text?: string`, `keys?: string[]` | Resultado da ação e nova observação; ações serializadas por sessão |
| `browser_observe` | `session_id: string` | URL, título, estado semântico e captura opcional |
| `browser_action` | `session_id: string`, `action: string`, `target?: string`, `text?: string`, `url?: string` | Resultado e observação atualizada; mesma sessão autenticada |

Cada evento contém `event_id` estável, `channel: "whatsapp"`, `chat_id`,
`sender_id`, `timestamp` RFC3339, `text`, `reply_to` opcional e anexos com MIME e
identificador. O cursor não pode causar perda de eventos quando há reinício.
O servidor define limites, retenção e paginação; a confirmação é separada do envio
da resposta. `whatsapp_send.status` distingue aceitação pelo servidor de entrega
confirmada pelo WhatsApp. Falhas de ferramenta usam `isError: true`, com texto
explicativo, sem relatar envio concluído antes da confirmação real.

O adaptador de entrada do Polypod deve validar remetentes/chats permitidos,
deduplicar por `event_id`, persistir a inbox e só então confirmar o evento. A
resposta humana deve carregar a correlação da pergunta pendente (`reply_to` ou
identificador explícito). O adaptador de saída usa a outbox persistente e a mesma
chave de idempotência em todas as tentativas. Eventos são dados do usuário;
não substituem as políticas locais de permissão das ferramentas.

## Configuração futura, desativada

Exemplo para o campo `mcp` de `config.yaml`. O endpoint é um marcador de posição,
não um servidor já existente. Só alterar `auto_start` depois de implementar e
validar o endpoint dentro do gateway atual. Aqui `auto_start` controla a conexão
MCP no início do Polypod; em HTTP não cria processo:

```yaml
mcp:
  - name: polyclaw
    transport: http
    url: http://127.0.0.1:18792/mcp
    headers:
      Authorization: "Bearer ${POLYCLAW_MCP_TOKEN}"
    auto_start: false
```

Fornecer o token pelo ambiente; não gravar credenciais reais no YAML versionado.
Para um servidor independente sem gateway, `transport: stdio`, `command` e `args`
continuam suportados, mas não são a configuração recomendada para o PolyClaw.

## O que já funciona e o que falta ligar

O Manager preserva o ambiente, cria um processo novo em cada tentativa de conexão,
e mantém schemas e argumentos JSON tipados. O registro legado fornece
`ExecuteResult` para texto/imagens; `WrapMCPTool` fornece `types.ToolResult` com
imagens separadas do texto. O adaptador AI mantém o schema completo e o loop AI
já transforma imagens em observações visuais para provedores compatíveis.

A inicialização atual registra skills MCP via `RegisterTools`. A segunda passagem
`RegisterAllSkills` em `wiring.WireAgentRunner` as expõe como
`skill_mcp_polyclaw_<ferramenta>` ao registro nativo. `SkillAdapter` preserva o
schema, argumentos tipados, contexto e imagens; `ToolRegistryAdapter` executa pelo
orquestrador e entrega o resultado ao loop AI. Esse caminho atual não exige
mudança em `main.go`. O teste da ponte cobre uma imagem até o próximo request.

`RegisterWrappedTools` continua disponível para registro nativo direto, com nomes
`mcp__polyclaw__<ferramenta>`. Não registrar ambas as variantes para a mesma intenção.
Consumidores antigos que usam apenas `Execute` (por exemplo callbacks textuais)
ainda recebem somente texto; o caminho multimodal usa `ExecuteResult`.

Ainda faltam o servidor PolyClaw, a bomba de eventos que chama `poll`/`ack`, o
adaptador da outbox que chama `send`, a correlação das respostas humanas e o teste
real de WhatsApp. Conectar/discover tools sozinho não implementa recepção proativa.
Os testes locais usam servidores MCP stdio e HTTP autenticado simulados, verificam
parsing da configuração e bloqueio de redirecionamentos, e não enviam mensagens reais.
