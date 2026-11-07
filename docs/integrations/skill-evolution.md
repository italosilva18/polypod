# Evolução revisada de skills

`internal/evolution.Open(dbPath, skillsDir)` mantém resultados e rascunhos em
SQLite. `RecordOutcome` grava um resultado de tarefa, rotina ou confirmação
manual com ID de execução estável, verificador e referência de evidência. O par
`source_kind/source_id` é imutável e idempotente. `RecordOutcome` recebe uma
afirmação do chamador; quem usá-lo deve conferir o estado externo antes de
registrar `Success=true`. Uma rotina concluída sem essa conferência não basta.
O identificador `file_sha256_v1` é reservado à verificação independente interna.

Para aprendizado automático, `RegisterVerificationPlan(ctx, input)` grava uma
verificação `file_sha256` para uma tarefa ou rotina **antes da execução**. O
plano guarda o caminho absoluto, o SHA-256 esperado e o SHA-256 inicial (ou
`missing`). Se o arquivo já tem o hash esperado, o plano é rejeitado: uma
execução futura não pode receber crédito por um estado preexistente. O plano é
imutável para o mesmo `source_kind/subject_id`. O runtime deve registrar o
plano junto com a criação da tarefa ou rotina, antes de liberar uma execução
imediata. `task_queue create` e `scheduler_add` aceitam os três campos opcionais
`verification_path`, `verification_sha256` e `verification_pattern`; quando
informados juntos, geram o ID, registram o plano e só então publicam o trabalho.
`scheduler_add` só aceita essa verificação em rotinas com `prompt`, cujo agente
passa pelo capturador de estado inicial.
Se a publicação falhar após o registro do plano, o ID aleatório não é
reutilizado e o plano órfão não gera resultado. A ação `skill_evolution plan`
também permite criar um plano explicitamente, mas só um plano anterior ao
início durável da execução pode produzir resultado.

Antes de cada execução, `BeginVerificationRun(ctx, sourceKind, subjectID,
runID)` captura o hash atual e o guarda por `runID` antes de qualquer ferramenta.
Em tentativas repetidas da mesma rotina, conserva o primeiro estado inicial.
Sem essa captura, ou se o arquivo já possuía o hash esperado ao começar, a
execução não recebe crédito por sucesso. Assim, uma rotina diária não aprende
repetidamente por encontrar o relatório de ontem intacto.

Ao término do handler, antes de persistir o aviso, `FinishVerificationRun(ctx,
sourceKind, subjectID, runID)` mede o arquivo e grava o hash final. Falta de
snapshot ou dois finais conflitantes para o mesmo `runID` deixam a execução
inconclusiva. Arquivo ausente ou com outro hash só produz falha se o estado
persistir em duas leituras separadas; arquivo alterado durante a leitura não é
aceito. O verificador não executa shell.

No aviso durável, `VerifyAndRecordAt(ctx, sourceKind, subjectID, runID, nil,
startedAt)` usa exclusivamente o snapshot final gravado, sem reler o arquivo.
`startedAt` vem do histórico persistido da tentativa. Plano criado no mesmo
milissegundo do início ou depois dele não gera resultado, pois não há prova de
precedência. A chamada antiga `VerifyAndRecord` retorna erro por não receber
esse horário. Só o hash exato esperado produz sucesso. O texto do agente, o
status do callback e a entrega de uma mensagem não são provas. O resultado
fica vinculado a `runID`; avisos repetidos devolvem o mesmo registro, mesmo se
o arquivo mudar depois. O verificador atual só aceita arquivo regular de até
1 GiB; outros tipos de resultado exigem verificadores novos e independentes.

O contexto da execução recebe `WithToolTrace(ctx)`. O orchestrator e os
caminhos alternativos da IA chamam `ObserveTool` somente depois de uma
ferramenta registrada terminar sem erro. `ToolTrace.Names()` contém, em ordem,
até 24 nomes de ferramentas, sem argumentos ou resultados. Se uma execução
usar mais de 24 ferramentas, o trace é considerado incompleto e `Names()`
retorna vazio; esse resultado não alimenta um rascunho. O runtime salva o
trace da tentativa terminal com `SaveToolTrace(ctx, sourceKind, runID, names)`
**antes** do callback de verificação. `VerifyAndRecordAt` recupera esse trace do
SQLite após reinício ou repetição do callback. Se não houver trace, o resultado
conta nas métricas, mas não pode fornecer uma sequência de ferramentas para
gerar rascunho. Para rotinas que reutilizam o mesmo `runID` nas tentativas, só
o trace da tentativa terminal deve ser salvo; o registro é imutável.

`Patterns` mostra categorias repetidas. `Propose(patternKey)` exige pelo menos
três sucessos, taxa de sucesso mínima de 75% e uma sequência de ferramentas
repetida em pelo menos dois sucessos. O rascunho `SKILL.md` contém apenas nomes
de ferramentas validados e IDs de evidência; texto livre de páginas, mensagens
e respostas do modelo não vira instrução. A evidência completa fica no registro
para consulta. `Review` aprova um corpo revisado ou descarta a proposta;
`Apply` aceita apenas propostas aprovadas e publica a skill em `skillsDir`.

A ferramenta `skill_evolution` expõe `plan`, `patterns`, `propose`, `get`,
`list`, `review` e `apply`. Ela não expõe `record`: o agente não pode certificar
o próprio sucesso. `RecordOutcome` permanece uma API para chamadores confiáveis.
`apply` exige `confirm=true`; `review` e `apply` são
classificados como operações destrutivas no pipeline de permissões. O runtime
pode registrar resultados verificados diretamente pela API, sem depender da
declaração do agente. `RegisterEvolutionTool(reg, store, onApply)` chama
`onApply(skillName)` após a aplicação, inclusive em uma repetição idempotente.
O watcher de `.polypod/skills` carrega o novo arquivo em até cinco segundos.
`AppliedSkillNames(ctx)` permite repor a lista de skills do agente ao reiniciar.
Na recuperação, cada arquivo aplicado é conferido contra o hash aprovado;
arquivo ausente ou alterado é omitido da lista ativa e gera aviso no log.

O estado `applying` é gravado antes da publicação. O arquivo é criado sem
sobrescrever uma skill existente. Se o processo cair antes de criar o arquivo,
`Open` retorna o rascunho a `approved`; se cair após publicar o conteúdo
aprovado, `Open` marca `applied`. Um arquivo existente com outro conteúdo
bloqueia a aplicação. O rascunho gerado é deliberadamente conservador:
aprende a sequência de ferramentas e referências de sucesso, mas uma pessoa
ou verificador confiável precisa revisar instruções mais específicas.
