# Delegacao para subagentes

O agente `default` expoe `agent_run` e pode delegar uma subtarefa independente
com `prompt`, `agent_name` e `timeout` (segundos). O resultado volta como texto
para o agente chamador, que continua responsavel por conferir a evidencia e
responder ao usuario. A ferramenta espera a conclusao do subagente e usa o
mesmo provedor de IA configurado para o Polypod.

| Nome no `agent_run` | Trabalho | Ferramentas principais |
| --- | --- | --- |
| `task` | Pesquisa em codigo, documentos e web | `read_file`, `read_file_dir`, `glob`, `grep`, `web_search`, `web_fetch`, `document_extract` |
| `architect` | Plano tecnico com contratos e dependencias | Ferramentas de leitura e pesquisa |
| `coder` | Implementacao de codigo | Leitura, `write_file`, `edit_file`, `bash` e pesquisa |
| `reviewer` | Verificacao independente | Ferramentas de leitura e extracao de documentos |
| `devops` | Diagnostico e operacao de sistema | Leitura, `bash`, web e ferramentas de hardware |

As definicoes ficam em `agents/*.yaml` e sao carregadas de `data.agents_dir`
durante a inicializacao. Os nomes acima aparecem no enum da ferramenta
`agent_run`; apenas criar outro YAML nao o torna selecionavel por esse enum.
`image_analyze` so aparece para os subagentes que o declaram quando ha um
provedor de visao configurado. Ferramentas nao registradas sao omitidas da
lista oferecida ao modelo.

O `prompt` precisa levar contexto suficiente: objetivo, arquivos ou fontes,
restricoes atuais do usuario, criterio de entrega e formato de resposta.
Exemplo de delegacao:

```json
{
  "agent_name": "reviewer",
  "prompt": "Revise a mudanca em internal/foo.go para o requisito X. Leia tambem os chamadores. O usuario adiou testes; faca apenas inspecao estatica. Relate achados com arquivo e linha.",
  "timeout": 120
}
```

`task`, `architect` e `reviewer` recebem apenas ferramentas de leitura.
`coder` e `devops` podem alterar o sistema por meio das ferramentas de escrita
e do `bash`. A execucao dos subagentes passa pelo orquestrador, que aplica
validacao, permissoes, hooks e auditoria com o nome do papel delegado. As
chamadas de `coder`, `devops` e `default` sao tratadas como operacoes de escrita
para nao disputar recursos com outras alteracoes. `task` e `architect` podem
consultar as skills de pesquisa e documentos, e `coder` e `reviewer` podem
consultar a skill de documentos. `agent_run` nao aparece no conjunto de
ferramentas dos proprios subagentes, para impedir delegacao recursiva. O
executor limita turnos, tokens
e duracao; atingir um limite pode produzir um resultado parcial. Esses papeis
organizam o trabalho e a verificacao, mas nao criam memoria ou aprendizado
automatico por si so.
