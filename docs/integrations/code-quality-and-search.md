# Verificação, diffs e busca de código

## Verificadores e formatadores

Execute `/init-verifiers` no diretório do projeto. O comando detecta a stack e
cria `.polypod/verifiers.yaml`. Ele preserva comandos e opções de um arquivo
existente. Use `/init-verifiers --dry-run` para conferir a detecção sem escrever.

Exemplo de configuração:

```yaml
verifiers:
  test: go test ./...
  vet: go vet ./...
  build: go build ./...
auto_run:
  on_save: false
  on_turn: true
  on_commit: true
format_on_write: true
formatters:
  .go: [gofmt, -w, "{file}"]
```

`on_save` executa verificações após cada gravação feita pelas ferramentas de
arquivo da Poly. É desativado por padrão porque pode repetir uma suíte grande
várias vezes no mesmo turno. `on_turn` executa os verificadores quando um turno
alterou arquivos. Se algum falhar, a Poly envia a falha ao modelo e permite
uma tentativa de correção; depois executa os verificadores novamente. O
resultado final mostra qualquer falha ainda presente. `on_commit` verifica
antes de preparar e criar um commit; uma falha bloqueia o commit.
`/verify` executa os verificadores manualmente, mesmo quando as opções
automáticas estão desativadas.
`/autofix-pr` injeta o relatório paginado de `/pr_comments` (obtido via `gh` ou,
como fallback automático, via GitHub REST API v3 com paginação `Link`) e impõe
um gate estrito de verificação ao fim do turno, independentemente de `on_turn`:
somente quando há verificadores configurados em `.polypod/verifiers.yaml` e
todos passam as edições permanecem aplicadas na worktree com o selo
`CORREÇÃO VERIFICADA`. Se os verificadores falharem após a tentativa automática
de reparo, não estiverem configurados, tiverem sido alterados no próprio turno
ou se o turno for interrompido antes da verificação, a Poly salva o diff unificado
proposto em `.polypod/held-proposals/<turnID>.patch`, reverte as edições na
worktree pelo checkpoint do turno e marca o resultado como
`PROPOSTA RETIDA — NÃO VERIFICADA`.

Cada verificador tem limite de dois minutos e a saída exibida é limitada a
4 KiB. A execução ocorre a partir do diretório que contém
`.polypod/verifiers.yaml`. Comandos são executados pelo shell do projeto;
revise esse arquivo antes de ativar verificações automáticas em um repositório
desconhecido.
Se o arquivo de verificadores for alterado durante um turno, a Poly suspende a
execução automática até o próximo turno. Revise a mudança e use `/verify` para
executar os comandos manualmente.

Os formatadores usam uma lista de argumentos, sem expansão pelo shell. A Poly
substitui `{file}` pelo caminho completo do arquivo. O formatador roda após a
gravação feita pela ferramenta e antes da atualização do LSP. Para Go, a
detecção configura `gofmt`. Para outras linguagens, indique um formatador
instalado no projeto e a extensão correspondente.

## Visualizador de diff

`/diff` abre o visualizador. `1` mostra as alterações do trabalho, inclusive
arquivos novos; `2` mostra arquivos preparados; `3` compara o ramo com o ramo
base detectado (`origin/HEAD`, `main` ou `master`);
`4` mostra o último turno com checkpoint de arquivo. Use `n` e `p` para mudar
de arquivo, `j` e `k` para rolar, e `Esc` para fechar. A opção de turno avisa
se o arquivo foi alterado novamente depois do checkpoint. Se o projeto não
tiver um ramo base detectável, o escopo do ramo mostrará uma orientação.

`/diff --staged` e `/diff <arquivo>` continuam produzindo texto no histórico.

## Contexto preciso

Use `@src/arquivo.go#20-45` para incluir somente as linhas 20 a 45 no pedido.
`@src/arquivo.go#20` inclui uma linha. Intervalos precisam existir no arquivo,
estar em ordem e conter no máximo 500 linhas. O conteúdo incluído continua
limitado a 10 KiB por menção.

## Orientação durante uma resposta

Enquanto a Poly responde, digite uma orientação e pressione Enter. Ela é
incluída no próximo ciclo do modelo e aparece na conversa. O ciclo de modelo
já em andamento pode terminar antes que a orientação seja aplicada. Use
`/queue <mensagem>` para iniciar um novo turno depois da resposta atual.

## Busca semântica de código

Configure `embedding` no `config.yaml` com um provedor local ou um endpoint
compatível e reinicie a Poly. Em seguida, execute `/index on` no projeto.
Isso cria `.polypod/code-index.yaml` com limites locais. `/semantic <consulta>`
busca diretamente na CLI; o modelo também pode usar a ferramenta
`code_semantic_search`. `/index off` desativa as duas.
Com o índice ativo, o modelo também dispõe de `code_context_search`, que
seleciona trechos por conteúdo, caminho, símbolos e arquivos alterados dentro
de um orçamento estimado de tokens. O parâmetro `ranking` usa `hybrid` por
padrão (fusão por Reciprocal Rank Fusion entre sinais lexicais/estruturais de
caminho/símbolo e similaridade semântica com seleção diversificada em duas
passagens, degradando automaticamente para lexical+mapa quando `semantic: true`
não estiver habilitado ou não houver embedder) e permite comparar `lexical`,
`map`, `semantic` e `hybrid`; a busca semântica isolada exige um embedder. Cada
trecho traz caminho, linhas e motivo da escolha. Para reproduzir a ablação local
no subconjunto de código fixado, execute
`python3 scripts/eval-local-context.py --embedding-model nomic-embed-text:latest`
(ou informe `--fixture docs/evaluation/context-fixture.v2.json`).
`/index on` também registra a autorização no diretório de configuração pessoal
da Poly, fora do repositório. Apenas colocar `enabled: true` no YAML não ativa
a busca. Se alterar os limites ou extensões nesse arquivo, execute `/index on`
novamente para confirmar a nova configuração.

A busca considera arquivos de código listados pelo Git e respeita `.gitignore`.
Por padrão, lê até 500 arquivos de no máximo 128 KiB e indexa até 1.000
trechos de 80 linhas, com sobreposição de 15 linhas. Os vetores são mantidos
em memória durante a execução e recalculados somente para trechos alterados.
Buscas simultâneas por trechos diferentes podem consultar o provedor em
paralelo; trechos idênticos compartilham o cálculo em andamento. O Git filtra
as extensões antes de devolver caminhos em projetos grandes.
Os resultados mostram caminho, intervalo de linhas e uma prévia abreviada;
abra o arquivo para ler o trecho inteiro.
Se o provedor de embeddings for remoto, os trechos lidos serão enviados a ele;
use o provedor local para manter o código na máquina. A configuração pode
restringir extensões e limites:

```yaml
enabled: true
max_files: 300
max_chunks: 600
extensions: [.go, .ts]
```
