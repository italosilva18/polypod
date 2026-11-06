# Avaliação reproduzível

Este harness offline importa resultados de verificadores e traces de sessão do Polypod; não executa tarefas, não baixa datasets, não chama modelos e não comprova superioridade sobre outros agentes. O manifesto `manifest.v1.json` é um smoke test local de cinco capacidades, **não** uma reprodução dos benchmarks oficiais. A fixture é sintética e todo relatório gerado a partir dela traz esse aviso.

```bash
go run ./internal/evaluation/cmd \
  -manifest docs/evaluation/manifest.v1.json \
  -results internal/evaluation/testdata/results.v1.json \
  -format json > /tmp/polypod-eval.json

go run ./internal/evaluation/cmd \
  -manifest docs/evaluation/manifest.v1.json \
  -results internal/evaluation/testdata/results.v1.json \
  -format markdown > /tmp/polypod-eval.md

go test -race ./internal/evaluation/...
go vet ./internal/evaluation/...
```

O comando fica dentro de `internal/` para seguir a convenção do repositório. Para um executável separado: `go build -o /tmp/polypod-eval ./internal/evaluation/cmd`.

## Contrato e pontuação

Manifesto: `schema_version=1`, `id`, `version`, lista `tasks` com ID único, `category` (`gui`, `memory`, `routine`, `channels`, `safety`), `benchmark`, critério externo verificável e `recovery` indicando falha injetada planejada. Fixe a lista antes de executar; inclua todas as tarefas do recorte e documente exclusões.

Resultados: `schema_version=1`, `manifest_id`, `manifest_version`, `metadata` e uma lista `outcomes` com no máximo um resultado por tarefa. O exemplo completo está em `internal/evaluation/testdata/results.v1.json`. Status aceitos: `success`, `failure`, `timeout`, `error`, `skipped`. Sucesso exige `evidence` e `verified_by`; esses campos registram a origem do julgamento, não autenticam nem executam o verificador. Use artefatos e verificadores externos confiáveis, nunca a declaração do agente. IDs desconhecidos/duplicados, schemas incompatíveis, campos desconhecidos, números negativos e recuperação inconsistente causam erro.

A taxa é sucessos dentro do orçamento / **todas as tarefas do manifesto**. Ausentes, skipped, timeout e error não aumentam o numerador. Cada categoria mantém denominador próprio; categoria sem tarefas fica `null`, não zero. O agregado é micro, ponderado pela quantidade de tarefas; não compare agregados de manifestos diferentes. Segurança exige simultaneamente completar a tarefa legítima e impedir a ação proibida, evitando premiar recusa indiscriminada.

Recuperação = tarefas com falha injetada, recuperadas e bem-sucedidas dentro do orçamento / todas as tarefas marcadas `recovery`. `recovered` deve ser informado pelo verificador, não inferido de um tool retry. Preserve o tipo/instante da falha no artefato de evidência.

`latency_ms`, `cost_usd` e `steps` são opcionais; medição ausente não vira zero. Latência e custo incluem falhas reportadas. Relatamos cobertura, total e percentis por nearest-rank (p50/p95). Informe custo real total da tentativa, inclusive retries/subagentes, com tabela de preços datada no artefato; zero só quando comprovado. Limites por tarefa em `metadata.budget` são obrigatórios, positivos: `max_steps`, `max_seconds`, `max_usd`. Um sucesso com excesso observado deixa de pontuar. **Sem todas as três medições, o harness não pode atestar cumprimento do orçamento**; esses relatórios não devem sustentar comparações de desempenho/custo. Um coletor externo deve impor os limites durante a execução.

Registre commit/tag do agente, identificador exato do modelo, parâmetros de geração/contexto, hardware (CPU/GPU/RAM), OS/imagem de VM, revisão/hash do dataset, versão do verificador, seed e orçamento em metadata. Versões de ferramentas, política de permissões, transporte, prompt/configuração e mecanismo de memória devem constar de `environment`/`model_settings` ou artefato associado. A saída preserva esses dados e cada resultado, e calcula SHA-256 dos bytes de manifesto, resultados e traces. Os hashes identificam entradas; não são atestação independente. Repita com seeds pré-definidas em arquivos separados; o harness não agrega repetições nem produz intervalos de confiança.

## Traces do Polypod

O campo opcional `trace` é um JSONL de sessão no formato lido por `internal/stats` e `internal/attribution`: `type`, `timestamp` RFC3339, `message.content` (string ou lista de blocos), `message.usage` com `input_tokens`, `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`. Caminhos relativos são resolvidos a partir do arquivo de resultados. O leitor conta tool_use, tool_result com is_error, tokens e intervalo entre timestamps. Traces corrompidos ou linhas acima de 16 MiB interrompem o comando; não são descartados silenciosamente.

Inclua apenas registros finais de uso por mensagem: deltas de streaming repetidos somariam tokens duas vezes. Sidechains no mesmo arquivo são incluídas; arquivos separados de subagentes não são descobertos automaticamente. `span_ms` do trace é intervalo observado, **não** latência ponta a ponta. O leitor não deduz sucesso, recuperação ou custo de textos/tokens, e não preenche automaticamente medições ausentes. Para outros agentes, normalize resultados no mesmo contrato e preserve seus traces como evidência externa; o parser de transcript é específico do Polypod.

## Protocolo de comparação

Use a mesma revisão/lista de tarefas, VM inicial, modelo e parâmetros, ferramentas disponíveis, rede, orçamento, número de repetições e política de intervenção para Polypod, OpenClaw, Hermes, PicoClaw, Dodaios, Agent S e Manus. Fixe versões exatas e identidades dos projetos, pois nomes não bastam. Quando modelo/ferramentas internos não puderem ser controlados, publique em uma trilha separada de produto completo. Não misture essa trilha com comparação controlada de runtime. Reinicialize estado entre tarefas, salvo histórico expressamente requerido para memória. Separe falha do agente de falha de infraestrutura sem remover nenhuma do denominador; reporte ambas. Ausência de integração é resultado não executado, não desempenho estimado.

## Benchmarks externos

- [OSWorld V2 / V2.1](https://github.com/xlang-ai/OSWorld-V2/blob/main/README.md): congele commit e manifeste IDs/revisão/hash dos assets V2.1 e imagens exigidas; respeite o processo oficial de acesso aos assets gated. Execute em VM isolada, com observações/ações e limite de passos equivalentes. Um adaptador externo deve executar o runner e o verificador oficial e converter cada julgamento final para `gui`. Preserve score oficial, logs e artefatos no caminho citado por `evidence`; não converta score fracionário em sucesso sem limiar previamente documentado. Nenhum runner/assets OSWorld está incluído aqui.
- [LongMemEval](https://github.com/xiaowu0162/LongMemEval): fixe release, split e versão do avaliador; ingira o histórico definido antes da pergunta, isole memória por caso, preserve perguntas sem resposta e atualização temporal. Importe os julgamentos oficiais como `memory` e registre modelo/versão do juiz. Não permita que a resposta-alvo entre no contexto nem use presença de chamada de memória como prova de acerto. [LongMemEval-V2](https://github.com/xiaowu0162/LongMemEval-V2) é outra versão: mantenha manifesto e resultados separados, sem misturar scores.
- [AgentDojo](https://github.com/ethz-spylab/agentdojo): fixe commit, suíte, tarefas, ataque/defesa, seeds e configuração do modelo. Execute o ambiente de ferramentas simulado conforme protocolo oficial. Preserve separadamente sucesso da tarefa legítima e sucesso do atacante no artefato oficial; normalize como `safety` bem-sucedido somente se a tarefa legítima for concluída **e** o ataque falhar. O score combinado local não substitui as métricas oficiais de utilidade e ataque. Não execute payloads contra serviços ou segredos reais.

Os links são pontos de entrada oficiais; o operador deve fixar revisões efetivamente utilizadas. Este pacote não implementa os adaptadores/ambientes oficiais, nem supõe autorização para baixar recursos restritos. Os testes só verificam contrato, cálculo e leitura; nenhum score de benchmark de agente foi produzido.
