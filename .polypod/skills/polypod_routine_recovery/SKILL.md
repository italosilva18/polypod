---
name: polypod_routine_recovery
description: Investigue rotinas, tarefas e notificações com falha antes de repetir ou encerrar uma ação.
---

# Recuperação operacional

Use quando uma rotina, tarefa pontual, mensagem ou notificação parece parada, falhou ou produziu resultado incerto.

1. Obtenha o estado atual: `heartbeat_status`, `inbox_list`, `scheduler_list` e `task_queue` com `action=status` ou `action=list`. Identifique o ID exato e o último estado; não deduza falha apenas de silêncio no canal.
2. Consulte `scheduler_history` com `id` para uma rotina e `task_queue` com `action=history` e `id` para uma tarefa. Use `audit` com `action=recent` quando precisar conferir quais ferramentas realmente executaram. Distinga execução, resultado, entrega da notificação e efeito final observado.
3. Antes de repetir, procure evidência do efeito já realizado no arquivo, serviço ou conversa de destino. Uma resposta perdida após uma ação concluída pode gerar duplicata. Se o resultado for incerto, investigue mais e peça ao usuário a informação indispensável via `ask_user`.
4. Para falha transitória de tarefa, use `task_queue` com `action=retry` e o mesmo `id` após conferir o histórico. Para rotina, use `scheduler_run` com o `id` existente quando a repetição for adequada. Não crie outro item para o mesmo trabalho sem necessidade.
5. Para item terminal da caixa de entrada, use `inbox_resolve` com `mode=preview` primeiro. Use `mode=apply` e `reason` apenas para registrar um encerramento justificado; essa operação não reexecuta a ação nem comprova sucesso.
6. Informe estado, evidência, ação realizada, resultado e próximo passo. Use `notify_user` quando houver mudança relevante que o usuário precise saber; evite avisos duplicados.

Uma rotina marcada como concluída prova que o fluxo terminou, não que o efeito externo esperado ocorreu. Verifique o estado final antes de declarar sucesso.
