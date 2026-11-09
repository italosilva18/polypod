# Operação contínua do agente

O perfil padrão deste workspace usa `bypassPermissions` e desativa o sandbox. As ferramentas do agente executam com as permissões da conta do sistema que inicia o Polypod. Credenciais presentes no ambiente também ficam disponíveis aos comandos de terminal. O sistema operacional, as credenciais de serviços externos e o provedor de modelo ainda determinam o que pode ser executado.

O agente principal e o subagente `general-purpose` têm acesso às ferramentas ativas. Quando o catálogo é grande, o provedor recebe primeiro as ferramentas comuns e `tool_search`; uma busca adiciona as ferramentas encontradas à conversa e mantém até 16 seleções recentes para o mesmo canal, usuário e agente. `POLYPOD_TOOL_CATALOG=full` envia todos os schemas de uma vez. Neste workspace, `ai.max_tool_iterations: -1` remove o limite interno de rodadas de ferramentas; os subagentes também não recebem limite interno de turnos ou de tokens no perfil `bypassPermissions`. Interromper a sessão ainda cancela a execução, e cada provedor mantém seus próprios limites de contexto, cobrança e disponibilidade.

## Tarefas em segundo plano

O agente pode criar tarefas `shell` e `agent` com `task_create`. Tarefas de agente usam o runtime completo e registram progresso, resultado e erros. No CLI:

```text
/task-status
/task-status <id>
/task-logs <id>
/task-restart <id>
```

O estado e os logs são salvos em `task-registry.d` dentro do diretório de dados, com permissão privada. Após uma reinicialização, tarefas que estavam em execução passam a `interrupted`. `/task-restart` cria uma tentativa com novo ID e retoma subagentes a partir do último checkpoint íntegro. Se uma ferramenta estava em andamento no momento da queda, a retomada é recusada até o efeito externo ser verificado, para não repetir a ação. Tarefas antigas sem checkpoint só podem repetir o prompt quando a falha comprova que nenhuma ferramenta foi executada. Tarefas de outra instância ainda ativa não aparecem na janela atual.

O CLI mostra um aviso quando uma tarefa termina, falha ou é interrompida. É possível digitar e enfileirar a próxima mensagem enquanto a resposta atual chega; a fila aparece na barra de status e pertence apenas à janela atual. Depois de corrigir a causa de uma falha de resposta, `/retry` reenvia a última entrada que falhou.

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

O comando `bash` pode encaminhar programas interativos a uma sessão PTY. As ferramentas `bash_pty_start`, `bash_pty_write`, `bash_pty_read`, `bash_pty_resize` e `bash_pty_close` permitem continuar a interação em chamadas seguintes. Saídas extensas de Bash podem ser consultadas por páginas com `bash_output`. Resultados genéricos de ferramentas acima de 50 KiB ficam em cache privado por até 24 horas e são lidos por páginas com `tool_output`. Cada resultado retido tem limite de 32 MiB; o cache total tem limite de 256 MiB.

## Conversa e arquivos

O histórico é compactado automaticamente quando o contexto cresce. `/compact` força a compactação. `/rewind` restaura conversa e arquivos gravados pelas ferramentas de edição do agente; `/rewind --conversation` e `/rewind --files` selecionam apenas uma parte. Alterações feitas diretamente por shell ou PTY não entram no snapshot de arquivos. Conflitos com edições posteriores são registrados e a restauração solicitada continua.

Na compactação automática e em `/compact`, a memória da sessão guarda até oito afirmações explícitas do usuário que saíram do contexto, com filtros para credenciais e limite de 64 entradas por sessão. A próxima resposta recupera até quatro entradas relevantes, limitadas a 1.000 caracteres. Cada branch começa com uma cópia da memória do ponto de criação e depois evolui separadamente. O registro privado fica em `session-memory.d` no diretório de dados; duas instâncias escrevendo no mesmo ID ao mesmo tempo ainda não têm coordenação entre processos.

`/branch create <nome>` copia o ponto atual da conversa para uma sessão independente. `/branch switch <nome>` salva a branch ativa e carrega o histórico selecionado para a próxima resposta do modelo. `/branch` mostra as branches e `/branch delete <nome>` remove o nome do índice sem apagar o transcript, que continua disponível em `/resume`. Ao retomar uma sessão, o CLI restaura o agente e modo válidos. Se ela veio de outra pasta, a diferença é mostrada na tela; abra o Polypod naquela pasta para usar seus arquivos.

## Opções locais

O perfil padrão não bloqueia alterações de arquivo por versões antigas, não serializa ações de desktop entre processos e não remove segredos do ambiente do Bash. Os controles opcionais são `POLYPOD_FILE_WRITE_GUARD=1`, `POLYPOD_DESKTOP_LOCK=1` e `POLYPOD_BASH_STRIP_SECRETS=1`.
