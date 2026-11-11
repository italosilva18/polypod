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

## Metas e tentativas

`goal_review` separa metas prontas, tentativas em andamento (`in_flight`) e resultados cujo efeito precisa ser conferido (`needs_verification`). O heartbeat registra `goal_attempt_start` antes de agir, `goal_attempt_finish` depois da ação e `goal_attempt_review` após verificar a evidência. Uma tentativa com sucesso declarado não conclui a meta sozinha: `goal_update` só aceita a conclusão, quando houver tentativas, se a última foi revisada e aceita e se houver evidência do critério de sucesso. Metas antigas sem tentativas continuam legíveis.

Para metas cujo resultado possa ser medido por estado, chame `goal_verification_plan` **antes da primeira tentativa**. O plano imutável aceita SHA-256 de arquivo, status HTTP, valor JSON em HTTP ou consulta escalar de leitura em SQLite. O registro captura o estado inicial; a tentativa captura outro estado antes da ação. Para um sucesso declarado, `goal_attempt_finish` captura o estado final na mesma chamada que encerra a tentativa. Se essa captura falhar, a tentativa continua em andamento e o agente deve conferir o efeito antes de tentar encerrá-la novamente, ou encerrá-la como falha/interrupção e revisar negativamente. Se a gravação da meta falhar após a captura, a repetição do encerramento preserva a primeira captura. Depois, `goal_attempt_verify` grava a avaliação do estado final já capturado. Um sucesso de versão antiga sem captura final não pode receber crédito de observação tardia: revise a tentativa como rejeitada e inicie outra. O verificador exige uma mudança real para o estado esperado, registra o resultado em `evolution.db` e vincula a prova à tentativa em `goals.json`. Para uma meta com plano, `goal_attempt_review` só aceita `accepted` se essa observação foi positiva, e `goal_update` só conclui a meta se a **última tentativa** tiver essa prova e revisão aceita. A prova confirma o estado observado ao encerrar a tentativa, sem atribuir causalidade exclusiva caso outro processo tenha alterado o alvo nesse instante. Se a verificação falhar ou for inconclusiva, registre uma revisão negativa, ajuste a próxima ação e inicie outra tentativa; a observação anterior não é reescrita.

O registro do plano e a gravação da meta ficam em arquivos diferentes. Se uma gravação de `goals.json` falhar depois que o plano foi salvo, recarregue as metas com `goal_list` e repita `goal_verification_plan` com os mesmos parâmetros. O início de uma tentativa e a conclusão ficam bloqueados enquanto houver esse plano externo pendente. O lock local serializa essas transições entre processos.

Se um processo reiniciar com uma tentativa ainda em andamento, `goal_review` a mantém em `in_flight` e sinaliza `needs_verification` quando ela tiver mais de uma hora. Ele não supõe que o processo original morreu, nem repete a ação. Depois de conferir a atividade e o efeito, registre `goal_attempt_finish` como `reported_success`, `failed` ou `interrupted`. Se houver plano, execute `goal_attempt_verify`; então faça a revisão antes de uma nova tentativa. Uma revisão negativa exige ajustar `next_action` antes de prosseguir. Metas legadas ou sem plano continuam com revisão manual de evidência; esse caminho não oferece a mesma garantia de verificação independente. O plano não verifica efeitos fora dos quatro tipos de estado aceitos.

Gravações de `goals.json` usam lock entre processos e conferem a versão lida antes de substituir o arquivo. Se outra instância alterou as metas, a gravação falha com aviso de estado desatualizado; execute `goal_review` ou `goal_list` para recarregar o plano antes de tentar novamente. Uma operação já iniciada não é repetida automaticamente.

## Memória, mensagens e recuperação

O serviço de memória automática lê conversas recentes, extrai fatos relevantes e mantém um cursor durável em `<data.dir>/dream-state.json`. Ele verifica a ociosidade após 5 minutos e respeita um intervalo mínimo de 30 minutos entre execuções. O mesmo arquivo guarda métricas como `runs`, `successful_runs`, `failed_runs`, `messages_processed`, `memories_added`, `last_error` e `last_successful_run_at`. Para conferir o efeito real, compare essas métricas com as memórias gravadas; uma execução sem memórias novas é possível.

O serviço de episódios guarda um histórico curto de pedidos do usuário que receberam resposta, com baixa confiança e sem afirmar que a ação foi executada. Ele usa um cursor separado em `<data.dir>/episode-state.json` e retoma o processamento após reiniciar; veja [memória episódica](episodic-memory.md). A recuperação automática apresenta origem e confiança das memórias, limita o tamanho do contexto e inclui no máximo um episódio da mesma sessão. Para tarefas e rotinas com um plano SHA-256 registrado antes da execução, o runtime captura o estado do arquivo no começo e no fim do turno; o callback durável usa esses registros para avaliar o resultado, sem depender do relato do agente ou de uma leitura tardia do arquivo. O trace de ferramentas guarda somente nomes e pode ser recuperado após reiniciar; veja [evolução de skills](skill-evolution.md).

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
