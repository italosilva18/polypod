# Operação contínua do Polypod

O agendador, a caixa de entrada durável e a consolidação de memória funcionam enquanto o processo do Polypod estiver ativo. No `config.yaml` atual, o agendador e o heartbeat operacional estão habilitados. O intervalo padrão do heartbeat operacional é **1 hora** (`scheduler.heartbeat_interval`). A fila de entrada e saída tem outro ciclo, de **30 segundos**, e também desperta ao receber trabalho novo.

## Rotinas iniciais

Na primeira inicialização com o agendador habilitado, `EnsureDefaultRoutines` cria as rotinas abaixo. O marcador `<scheduler.data_file>.defaults-seeded` impede que uma edição, desativação ou remoção seja desfeita em inicializações posteriores.

| ID | Horário | Objetivo |
| --- | --- | --- |
| `system_daily_plan` | Todos os dias, 09:00 | Rever metas, pendências e compromissos; definir até três prioridades. |
| `system_daily_review` | Todos os dias, 18:00 | Registrar resultados, falhas e próximos passos. |
| `system_weekly_review` | Segunda-feira, 10:00 | Rever a semana e ajustar prioridades e rotinas. |

Esses horários usam `America/Sao_Paulo`. As três rotinas usam `misfire_policy=run_once`: após uma parada que ultrapasse o horário, cada rotina executa uma vez na retomada, sem reproduzir cada ocorrência perdida. Cada uma permite uma nova tentativa após falha. O `system_heartbeat` é uma rotina separada, com `misfire_policy=skip`; ele não executa uma rodada atrasada quando perde seu horário.

Cada rotina com `prompt` desperta o **agente completo**, com suas ferramentas e uma conversa persistente própria. Um gate serializa os turnos das rotinas e das tarefas pontuais para evitar que dois planos autônomos atuem ao mesmo tempo sobre a mesma meta ou item. O agente pode consultar pendências, avançar ações prontas, usar `ask_user` quando precisar de uma informação essencial e responder `NO_REPLY` quando não houver aviso útil. `NO_REPLY` suprime a notificação de conclusão bem-sucedida.

## Memória, mensagens e recuperação

O serviço de memória automática lê conversas recentes, extrai fatos relevantes e mantém um cursor durável em `<data.dir>/dream-state.json`. Ele verifica a ociosidade após 5 minutos e respeita um intervalo mínimo de 30 minutos entre execuções. O mesmo arquivo guarda métricas como `runs`, `successful_runs`, `failed_runs`, `messages_processed`, `memories_added`, `last_error` e `last_successful_run_at`. Para conferir o efeito real, compare essas métricas com as memórias gravadas; uma execução sem memórias novas é possível.

O serviço de episódios guarda um histórico curto de pedidos do usuário que receberam resposta, com baixa confiança e sem afirmar que a ação foi executada. Ele usa um cursor separado em `<data.dir>/episode-state.json` e retoma o processamento após reiniciar; veja [memória episódica](episodic-memory.md). Para tarefas e rotinas com um plano SHA-256 registrado antes da execução, o callback durável verifica o arquivo e registra o resultado independentemente do relato do agente. O trace de ferramentas guarda somente nomes e pode ser recuperado após reiniciar; veja [evolução de skills](skill-evolution.md).

Mensagens remotas entram na inbox durável. O processador retoma itens pendentes, grava resultado e resposta e entrega a outbox com tentativas posteriores quando há falha. Notificações das rotinas usam a mesma outbox; quando configurado, o WhatsApp entrega ao chat do dono, além dos canais locais ou remotos disponíveis. A falta temporária de conexão remota adia a entrega. O histórico de cada tentativa de rotina fica em `<scheduler.data_file>.runs.jsonl`; na retomada, o agendador reconcilia a execução interrompida com esse histórico e com as notificações pendentes. A retomada pode repetir um efeito externo cujo resultado não foi confirmado: o prompt manda verificar o efeito anterior antes de agir novamente.

Uma automação externa também pode enviar uma instrução ao [webhook autenticado](webhook-ingress.md) e consultar o resultado por ID. Esse canal é opcional, fica desativado até receber um token privado e escuta apenas no loopback.

Itens com falha terminal podem ser encerrados com `inbox_resolve`, após uma prévia e com motivo registrado. O estado `resolved` preserva erro, tentativas e destinos já entregues, sem reexecutar a mensagem nem marcar sucesso. A manutenção offline também oferece essa ação quando o serviço está parado.

## Inspeção e controle

- `scheduler_list`: mostra IDs, estado, próxima execução, fuso e último resultado.
- `scheduler_history` com `id`: mostra tentativas, duração, erros e resultados.
- `scheduler_update` com `id` e campos como `schedule`, `time_zone`, `prompt` ou `misfire_policy`: edita a rotina futura sem perder o histórico.
- `scheduler_toggle` com `id` e `enabled=false`: pausa uma rotina; `enabled=true` a reativa. `scheduler_run` com `id` executa imediatamente.
- `heartbeat_status` e `inbox_list` (opcionalmente `status=failed` ou `status=resolved`): mostram o ciclo da caixa e itens que exigem investigação.
- `inbox_resolve` com `mode=preview` ou `mode=apply` e `reason`: confere ou encerra uma falha terminal sem replay.

Para desabilitar o **heartbeat operacional** de modo persistente, ajuste `scheduler.heartbeat_enabled` no `config.yaml`: com essa opção ativa, a inicialização reativa `system_heartbeat`. Para parar todas as rotinas, ajuste `scheduler.enabled` e reinicie o serviço. As rotinas iniciais podem ser pausadas individualmente com `scheduler_toggle` e permanecem pausadas após reiniciar.

Os testes automatizados verificam agendamento, histórico, recuperação e filas, mas não comprovam sozinhos uma execução no horário real, uma resposta de `ask_user` nem a entrega ao WhatsApp do dono. Para validar em produção, consulte `scheduler_history`, `heartbeat_status`, `inbox_list` e o recebimento no canal configurado após uma rodada real; não interprete processo ativo ou mensagem enfileirada como entrega confirmada.
