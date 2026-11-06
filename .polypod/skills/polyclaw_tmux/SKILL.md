---
name: polyclaw_tmux
description: Controle sessões interativas do tmux para executar CLIs, observar saída e retomar processos longos.
---

# tmux

Adaptação da skill tmux do PolyClaw. Use um socket privado próprio para tarefas do agente: `tmux -S /tmp/polypod-task.sock new-session -d -s tarefa`. Envie texto literal com `send-keys -l`, envie `Enter` separadamente e observe com `capture-pane -p -J -S -200`. Liste sessões antes de reutilizar nomes.

O serviço Polypod possui a sessão `polypod-root`; não a substitua ao criar tarefas. Registre o socket e o nome da sessão no resultado para que uma tarefa longa possa ser acompanhada. Encerre somente sessões criadas para a tarefa concluída. Consulte scripts e exemplos em `third_party/polyclaw/skills/tmux/`; licença em `third_party/polyclaw/LICENSE`.
