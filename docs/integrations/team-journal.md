# Histórico persistente de equipes

Quando `data.dir` está configurado, cada processo Polypod grava um diário JSONL
privado em `team-journals-uid-<uid>/instance-*.jsonl`. O arquivo contém eventos
de criação, membros, mensagens, confirmações da caixa do líder e início/fim de
turnos. Cada registro tem `version: 1`; textos muito extensos são truncados em
256 KiB por campo. Arquivos e locks são privados ao usuário do processo.

`team_status` lista equipes atuais e as registradas anteriormente. Após uma
queda, uma equipe cujo processo não possui mais o lock aparece como
`interrupted`. `team_results` mostra os resultados concluídos, turnos ativos de
outro processo como `running` e turnos sem evento de fim de um processo morto
como `interrupted`. `team_inbox` e `team_ack` permitem ler e
confirmar mensagens pendentes do líder mesmo após o reinício.

As goroutines de agentes não são retomadas automaticamente. A recuperação é
somente de histórico e caixa de entrada; repetir uma ação sem saber se ela foi
concluída poderia duplicar efeitos externos. Processos com UIDs diferentes,
como um serviço root e uma CLI de usuário, têm diários separados para manter
permissões privadas. Uma falha de gravação é registrada no log; o agente em
execução continua, então o diário é evidência persistente de melhor esforço.

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
