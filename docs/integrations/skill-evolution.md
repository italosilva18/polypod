# Evolução revisada de skills

`internal/evolution.Open(dbPath, skillsDir)` mantém resultados e rascunhos em
SQLite. `RecordOutcome` grava um resultado de tarefa, rotina ou confirmação
manual com ID de execução estável, verificador e referência de evidência. O par
`source_kind/source_id` é imutável e idempotente. O pacote **não prova** sozinho
que a tarefa deu certo: o chamador deve conferir o estado externo antes de
registrar `Success=true`. Uma rotina concluída sem essa conferência não basta.

`Patterns` mostra categorias repetidas. `Propose(patternKey)` exige pelo menos
três sucessos, taxa de sucesso mínima de 75% e uma sequência de ferramentas
repetida em pelo menos dois sucessos. O rascunho `SKILL.md` contém apenas nomes
de ferramentas validados e IDs de evidência; texto livre de páginas, mensagens
e respostas do modelo não vira instrução. A evidência completa fica no registro
para consulta. `Review` aprova um corpo revisado ou descarta a proposta;
`Apply` aceita apenas propostas aprovadas e publica a skill em `skillsDir`.

A ferramenta `skill_evolution` expõe `record`, `patterns`, `propose`, `get`,
`list`, `review` e `apply`. `apply` exige `confirm=true`; `review` e `apply` são
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
