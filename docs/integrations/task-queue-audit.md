# Fila de tarefas e auditoria operacional

## Tarefas pontuais

`internal/taskqueue.Open(path)` abre uma fila SQLite privada. `SetHandler` liga cada
`Task` ao turno completo do agente; `Start(ctx)` processa uma tarefa por vez,
ordenada por prioridade, horário devido e criação. A ferramenta `task_queue`
oferece `create`, `list`, `get`, `history`, `cancel`, `retry` e `status`.

O claim, o número da tentativa e o início do histórico são gravados na mesma
transação. Resultado e tentativa final também são gravados juntos. Após queda
do processo, uma tentativa `running` vira `interrupted`: a tarefa volta a
`pending` se restarem tentativas ou termina como `failed`. O handler recebe o
mesmo ID da tarefa e o novo número da tentativa para conferir efeitos externos
antes de repetir uma ação.
Cada turno recebe prazo cooperativo padrão de 30 minutos (`SetTaskTimeout`
permite ajustar). Ferramentas que ignorem cancelamento ainda podem bloquear a
única fila; o prazo não desfaz efeitos externos já realizados.

Ao terminar, a fila grava um `Notice` na mesma transação do resultado. O
callback de `SetNotifier` recebe uma chave estável
`<task_id>:terminal:<attempt>`; se a notificação falhar ou o processo parar
entre o envio e a confirmação, só o notice é reenviado. O runtime usa essa
chave em `humanloop.EnqueueOutboundOnce`, que evita recriar a mesma saída.
`status` mostra contagens exatas por estado e notices pendentes. A entrega até
o telefone continua sob responsabilidade do humanloop.

## Auditoria e checkpoints

`internal/audit.Open(dir)` cria diretório `0700`, backups `0600` e um JSONL
sincronizado em disco. `Orchestrator.SetAuditStore` grava um evento de início
antes de executar cada ferramenta e um evento de conclusão/falha depois.
Eventos guardam nome, sessão, estado e duração, sem argumentos, respostas ou
conteúdo de arquivos. A cadeia SHA-256 detecta alteração acidental quando o
log é reaberto ou consultado; ela **não é uma assinatura criptográfica**.

A ferramenta `audit` oferece `recent`, `list`, `checkpoint`, `inspect` e
`restore`. Checkpoints aceitam arquivos regulares de até 64 MiB e verificam
SHA-256 do backup. Para restaurar, o agente deve obter `current_sha256` por
`inspect` e enviar `confirm=true`; uma alteração detectada antes da substituição
impede a restauração. A substituição usa arquivo temporário e rename atômico.
Um escritor externo pode alterar o arquivo entre a verificação do hash e o
rename; esse intervalo não é um compare-and-swap atômico.
O pipeline normal de permissões do Polypod continua aplicável à ferramenta.

## Limites

- Se uma tentativa realizou um efeito externo e caiu antes de confirmar o
  resultado, a fila não consegue provar sozinha se a ação ocorreu. O prompt
  de retomada orienta o agente a verificar antes de repetir.
- A chave estável deduplica a criação da notificação no outbox. Uma aceitação
  remota ambígua ainda exige reconciliação no canal de entrega.
- A fila é de um processo por banco SQLite e não cancela uma tarefa já em
  execução. `cancel` atua em tarefas pendentes ou terminais.
- `confirm=true` é um passo explícito da ferramenta; a exigência de aprovação
  humana depende da política efetiva de permissões do agente.

Os testes em `internal/audit`, `internal/taskqueue`, `internal/tool` e
`internal/orchestrator` cobrem persistência, corrupção, checkpoint, restore,
prioridade, crash recovery e repetição de notificação com detector de corridas.
