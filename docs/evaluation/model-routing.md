# Roteamento de modelos calibrado

O fluxo principal aceita uma política opcional produzida a partir de avaliações reais. Sem política, usa `ai.model`. O roteamento só escolhe IDs explicitamente permitidos em `ai.routing_models` na **mesma conexão do provedor primário**; não muda autenticação, endpoint nem fallback. Uma troca manual com `ai.Service.SetModel` prevalece nas respostas seguintes. Cada turno fixa seu modelo, inclusive durante chamadas sucessivas de ferramentas, para que outras conversas não alterem a execução em curso.

Prepare um manifesto de avaliação com `prompt` em todas as tarefas e resultados no contrato de `internal/evaluation`. Use os mesmos IDs de tarefa, seeds, versão do agente, parâmetros, ambiente, hardware, orçamento e verificador para cada modelo. O gerador rejeita fixtures sintéticas, tarefas ausentes e resultados sem `latency_ms`, `cost_usd` ou `steps`; sucesso exige evidência externa. O texto de `model_settings` deve descrever os parâmetros comuns sem incorporar o ID do modelo, para permitir a comparação. Os resultados do runner local de arquivos não medem custo ou passos por si só, então ainda não habilitam política automática.

```bash
go run ./internal/smartroute/cmd \
  -manifest /caminho/manifesto-real.json \
  -provider chatgpt \
  -default-model MODELO_ATUAL \
  -allowed-models MODELO_CANDIDATO_1,MODELO_CANDIDATO_2 \
  -output /caminho/politica.json \
  /caminho/resultado-modelo-atual-seed1.json \
  /caminho/resultado-candidato-seed1.json \
  /caminho/outros-resultados.json
```

É preciso pelo menos três seeds pareados e dez observações por categoria em cada modelo. O candidato só substitui o padrão quando o limite inferior de Wilson de 95% da sua taxa de sucesso supera o limite superior do padrão. Empate de qualidade entre candidatos elegíveis usa custo médio por tarefa. Categorias sem evidência suficiente ficam no modelo padrão. Uma política gerada de resultados não comprova a autenticidade de quem forneceu o verificador; guarde os artefatos e revise a origem dos julgamentos.

Para ativar no `config.yaml`:

```yaml
ai:
  provider: chatgpt
  model: MODELO_ATUAL
  routing_policy_file: /caminho/politica.json
  routing_models: MODELO_CANDIDATO_1,MODELO_CANDIDATO_2
```

O runtime confere `provider`, `default_model`, versão da política e allowlist antes de responder. As tarefas da fila e do agendador são classificadas como `routine`. Conversas livres do CLI e WhatsApp ficam no modelo padrão porque o transporte não determina a natureza da tarefa. Chamadores internos podem informar `AnswerRequest.RoutingCategory` para `gui`, `memory`, `routine`, `channels` ou `safety` quando dispuserem de uma classificação confiável. O roteador legado por palavras-chave em `internal/smartroute/router.go` não é usado pelo fluxo calibrado.
