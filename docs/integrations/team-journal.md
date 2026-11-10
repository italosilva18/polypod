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

O diário cresce com o uso e atualmente é lido por inteiro para consultas de
histórico. A consulta expõe apenas a execução mais recente de cada nome de
equipe, embora registros anteriores permaneçam no JSONL. Limpeza, retenção e
busca paginada ainda podem ser adicionadas sem mudar o formato v1.
