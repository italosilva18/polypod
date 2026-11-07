# Entrada HTTP por webhook

O webhook recebe comandos de uma automação externa e grava cada evento na inbox durável do Polypod. O processamento ocorre depois, pelo agente completo e pelas mesmas ferramentas e permissões usadas nos demais canais. O endpoint responde quando a gravação está concluída; a resposta `202` não significa que a tarefa terminou.

O canal é opcional e vem desativado. Para ativá-lo, gere um token aleatório com pelo menos 32 caracteres, guarde-o fora do repositório como `POLYPOD_WEBHOOK_TOKEN` e configure:

```yaml
webhook:
  enabled: true
  listen: "127.0.0.1:8788"
  token: "${POLYPOD_WEBHOOK_TOKEN:}"
```

O listener aceita apenas endereços de loopback. Para acesso de outra máquina, use um proxy reverso com TLS e controle de acesso, encaminhando para o loopback. O token não aparece nos logs do webhook. A configuração falha ao iniciar quando o canal está habilitado sem token válido ou com endereço não local.

Envie um `POST /v1/inbox` com `Authorization: Bearer <token>` e `Content-Type: application/json`:

```json
{"id":"evento-2026-11-07-001","text":"Revise os arquivos novos e informe pendências."}
```

`id` é obrigatório, tem até 128 caracteres ASCII (`A-Z`, `a-z`, dígitos, `.`, `_`, `-`, `:`) e identifica a operação para deduplicação durável. Reenvios com o mesmo ID e texto retornam o mesmo item da inbox e não criam outro turno; texto diferente com ID reutilizado recebe `409`. Use um ID novo para uma ação nova. `text` é obrigatório, tem até 16 KiB e é entregue como instrução ao agente. O corpo total tem limite de 64 KiB. Há limite de oito requisições simultâneas; o excesso recebe `429`. A resposta de aceitação tem a forma `{"id":"<id interno>","status":"pending"}`; em reenvios, `status` reflete o estado atual do item.

Use o ID interno retornado para consultar `GET /v1/inbox/<id interno>`, com o mesmo cabeçalho `Authorization`. A consulta retorna `status` (`pending`, `processing`, `completed`, `failed` ou `resolved`), `attempts`, `updated_at` e, quando existir, `completed_at`. `response` aparece somente após `completed`; `error` aparece em `failed` ou `resolved`. Itens em andamento incluem `Retry-After: 2`. A consulta não devolve o texto original do evento e só enxerga mensagens de entrada do canal webhook. Itens concluídos antigos podem ser removidos pela política de retenção da inbox; nesse caso a consulta responde `404`.

O webhook não faz entrega ativa ao chamador HTTP. O agente grava a resposta na inbox/outbox, e a outbox marca essa resposta como entregue pelo canal de consulta sem gerar alerta local por evento. Integrações HTTP devem consultar o status. Para notificações ou perguntas interativas, use os canais WhatsApp ou Telegram configurados.

O serviço limita cabeçalhos, tempo de leitura e escrita e encerra a conexão na parada do processo. Uma falha de rede depois da gravação pode deixar o cliente sem confirmação; nesse caso, reenvie **o mesmo ID**. A inbox decide se houve duplicata. Nenhuma execução de agente acontece na thread HTTP.
As respostas HTTP usam `Cache-Control: no-store`, inclusive as que contêm o texto final do agente.
