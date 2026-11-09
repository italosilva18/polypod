# Operação contínua do agente

O perfil padrão deste workspace usa `bypassPermissions` e desativa o sandbox. As ferramentas do agente executam com as permissões da conta do sistema que inicia o Polypod. Credenciais presentes no ambiente também ficam disponíveis aos comandos de terminal. O sistema operacional, as credenciais de serviços externos e o provedor de modelo ainda determinam o que pode ser executado.

O agente principal e o subagente `general-purpose` recebem o catálogo completo de ferramentas ativas. Neste workspace, `ai.max_tool_iterations: -1` remove o limite interno de rodadas de ferramentas; os subagentes também não recebem limite interno de turnos ou de tokens no perfil `bypassPermissions`. Interromper a sessão ainda cancela a execução, e cada provedor mantém seus próprios limites de contexto, cobrança e disponibilidade.

## Tarefas em segundo plano

O agente pode criar tarefas `shell` e `agent` com `task_create`. Tarefas de agente usam o runtime completo e registram progresso, resultado e erros. No CLI:

```text
/task-status
/task-status <id>
/task-logs <id>
/task-restart <id>
```

O estado e os logs são salvos em `task-registry.d` dentro do diretório de dados, com permissão privada. Após uma reinicialização, tarefas que estavam em execução passam a `interrupted`. `/task-restart` inicia outra tentativa com novo ID; não repete ações automaticamente. Tarefas de outra instância ainda ativa não aparecem na janela atual.

## Rotinas e gatilhos

`/schedule` gerencia rotinas persistentes:

```text
/schedule list
/schedule add 0 9 * * * -- Resuma minhas pendências
/schedule history <id>
/schedule run <id>
/schedule enable <id>
/schedule disable <id>
/schedule remove <id>
```

Um comentário `AI!` acrescentado a um arquivo de código observado inicia uma tarefa de agente em segundo plano com o trecho ao redor como contexto. O mesmo comentário não dispara novamente a cada gravação do arquivo; para repetir a instrução, altere o texto do comentário ou use uma tarefa explícita.

## Terminal e resultados longos

O comando `bash` pode encaminhar programas interativos a uma sessão PTY. As ferramentas `bash_pty_start`, `bash_pty_write`, `bash_pty_read`, `bash_pty_resize` e `bash_pty_close` permitem continuar a interação em chamadas seguintes. Saídas extensas de Bash podem ser consultadas por páginas com `bash_output`; os arquivos de saída ficam no cache privado por até 24 horas.

## Conversa e arquivos

O histórico é compactado automaticamente quando o contexto cresce. `/compact` força a compactação. `/rewind` restaura conversa e arquivos gravados pelas ferramentas de edição do agente; `/rewind --conversation` e `/rewind --files` selecionam apenas uma parte. Alterações feitas diretamente por shell ou PTY não entram no snapshot de arquivos. Conflitos com edições posteriores são registrados e a restauração solicitada continua.

## Opções locais

O perfil padrão não bloqueia alterações de arquivo por versões antigas, não serializa ações de desktop entre processos e não remove segredos do ambiente do Bash. Os controles opcionais são `POLYPOD_FILE_WRITE_GUARD=1`, `POLYPOD_DESKTOP_LOCK=1` e `POLYPOD_BASH_STRIP_SECRETS=1`.
