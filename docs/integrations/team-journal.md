# Histórico persistente de equipes

Quando `data.dir` está configurado, cada processo Polypod grava um diário JSONL
privado em `team-journals-uid-<uid>/instance-*.jsonl`. O arquivo contém eventos
de criação, membros, mensagens, confirmações da caixa do líder e início/fim de
turnos. Cada registro tem `version: 1`; prompts e resultados muito extensos são
truncados em 256 KiB. Mensagens acima desse limite são recusadas antes da
entrega para não criar uma fila impossível de recuperar. Arquivos e locks são
privados ao usuário do processo.

`team_status` lista equipes atuais e as registradas anteriormente. Após uma
queda, uma equipe cujo processo não possui mais o lock aparece como
`interrupted`. `team_results` mostra os resultados concluídos, turnos ativos de
outro processo como `running` e turnos sem evento de fim de um processo morto
como `interrupted`. `team_inbox` e `team_ack` permitem ler e
confirmar mensagens pendentes do líder mesmo após o reinício.

`team_resume` sem `apply` só diagnostica. Com `apply=true`, uma equipe criada
depois deste protocolo pode ser restaurada sob um lock exclusivo por nome e
execução. A configuração original, integrantes e caixas são reconstruídos.
Somente mensagens cuja entrega e ordem foram gravadas antes de ficarem
visíveis e que não tenham `turn_started` vinculado voltam à fila. Cada início
de turno é gravado antes da chamada ao modelo; uma falha nesse registro impede
a execução. Todos os turnos iniciados precisam de fim registrado e checkpoint
final `done` íntegro, inclusive os de integrantes já removidos. Turnos incertos,
mensagens de controle pendentes, registros antigos, posse sem lock comprovado e
execuções já retomadas permanecem bloqueados. Uma nova queda depois de uma
retomada exige inspeção manual; o mesmo run não é retomado duas vezes. A
criação grava o marcador de posse antes do evento `created`; se essa gravação
falhar, uma execução anterior também fica bloqueada por precaução.

Processos com UIDs diferentes, como um serviço root e uma CLI de usuário,
têm diários separados para manter permissões privadas. O envio de mensagens
e o início dos turnos do protocolo novo falham fechados quando o diário não
pode ser persistido. Resultados finais e notificações podem ficar incertos se
ocorrer uma falha de gravação depois de uma ação; nessa situação a retomada não
repete o turno.

O formato dos registros permanece na versão 1. Cada `instance-*.jsonl` pode
ter um índice auxiliar `instance-*.jsonl.idx`. O JSONL é a fonte de verdade:
na abertura, o Polypod percorre os registros completos para recusar JSON
inválido, versão desconhecida ou linha acima do limite. Uma linha final sem
quebra, típica de queda, continua ignorada. Consultas seguintes usam os
metadados validados em memória e materializam apenas os registros necessários.
Uma alteração no arquivo invalida ou atualiza esse cache. O índice em disco é
reconstruível e seus metadados são conferidos contra o JSONL antes do uso.

`Teams` devolve a execução mais recente de cada nome, ordenada pelo nome.
`History` lista as execuções criadas, da mais nova para a mais antiga, em
páginas de no máximo 100. Cada página aplica a mesma dobra de estado que
`Teams`; o cursor é opaco.

`Prune` é explícito e não roda na abertura. Com `Apply` verdadeiro, pode
remover execuções antigas que tenham estado final `stopped` gravado, sem
mensagem pendente do líder e sem turno em aberto. A execução mais recente de
cada nome permanece. `KeepRuns` abaixo de 1 ainda conserva essa execução;
valor negativo é recusado. `MaxAge` maior que zero também exige idade.
Arquivos do processo atual, arquivos com lock ativo, arquivos cujo lock não
existe e arquivos com cauda truncada não são reescritos. Uma execução
interrompida ou incerta permanece no disco, mesmo se estiver ociosa.
