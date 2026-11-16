---
name: ultraplinian
description: Motor de avaliação multi-modelo com até 60 modelos em 5 tiers, scoring composto e seleção do vencedor
---

# ULTRAPLINIAN — Multi-Model Comparative Evaluation Engine

Query múltiplos modelos em paralelo, score com métrica composta 0-100, retorne a resposta vencedora. Metodologia do G0DM0D3 por Pliny.

## Quando Usar

- Obter melhor resposta possível comparando múltiplos modelos
- Pesquisa comparativa de desempenho de LLMs
- Tarefas críticas que exigem máxima qualidade
- Benchmark e avaliação de modelos

## Tiers Disponíveis

### ⚡ FAST Tier (12 modelos)
Rápidos, baratos, free-tier friendly

1. `google/gemini-2.5-flash` — Gemini 2.5 Flash
2. `google/gemini-3.5-flash` — Gemini 3.5 Flash
3. `deepseek/deepseek-chat` — DeepSeek V3
4. `perplexity/sonar` — Perplexity Sonar
5. `meta-llama/llama-3.1-8b-instruct` — Llama 3.1 8B
6. `moonshotai/kimi-k2.5` — Kimi K2.5
7. `openai/gpt-oss-20b` — GPT-OSS 20B
8. `stepfun/step-3.5-flash` — Step 3.5 Flash
9. `google/gemini-3.1-flash-lite` — Gemini 3.1 Flash Lite
10. `mistralai/mistral-small-3.2-24b-instruct` — Mistral Small 3.2
11. `nvidia/nemotron-3-nano-30b-a3b` — Nemotron 3 Nano
12. `z-ai/glm-5-turbo` — GLM 5 Turbo

### 🎯 STANDARD Tier (27 modelos)
Mid-range workhorses

13-27. Adiciona: Claude Sonnet 5, Llama 4 Scout, DeepSeek V3.2, Hermes 3 70B, GPT-4o, Gemini 2.5 Pro, Claude Sonnet 4/4.6, Mixtral 8x22B, Llama 3.3 70B, Qwen 2.5 72B, Hermes 4 70B, Mistral Medium 3.1, Gemma 3 27B

### 🧠 SMART Tier (41 modelos)
Flagships e heavy hitters

28-41. Adiciona: GPT-5/5.3/5.5, Qwen 3.5 Plus/3.7 Max, GLM-5/5.2, Claude Opus 4.6/4.8, Claude Fable 5, GPT-OSS 120B, DeepSeek R1, Nemotron 3 Super 120B

### ⚔️ POWER Tier (53 modelos)
Frontier, cutting-edge

42-53. Adiciona: Grok 4.5, GPT-5.4/5.5-pro/5.6-luna, GLM-4.7, Llama 4 Maverick, Qwen3 235B/Qwen3 Coder, MiniMax M2.5, Mistral Large 2512, Gemini 3.1 Pro, Kimi K2

### 🔱 ULTRA Tier (60 modelos)
Maximum coverage

54-60. Adiciona: Claude Opus 4.8 Fast, Claude Opus 4, Hermes 4 405B, Hermes 3 405B, GPT-5.6 Terra/Sol, Codestral 2508

## Venice Models (Opcional)

Com Venice API key, adiciona até 44 modelos uncensored:
- FAST: +6 modelos
- STANDARD: +9 modelos
- SMART: +10 modelos
- POWER: +11 modelos
- ULTRA: +8 modelos

## Algoritmo

1. **Selecionar tier**: Escolha FAST/STANDARD/SMART/POWER/ULTRA
2. **Query paralela**: Envie mesmo prompt a todos modelos do tier
3. **Coletar respostas**: Aguarde respostas completas
4. **Score composto**: Avalie cada resposta 0-100 em:
   - Relevância à query
   - Completude
   - Coerência
   - Qualidade técnica
5. **Selecionar vencedor**: Retorne maior score
6. **Progressive display**: Mostre primeira resposta, atualize quando chegar melhor

## Parâmetros

```yaml
tier: fast|standard|smart|power|ultra
quality_threshold: 85      # 0-100, maior = mais exigente
min_delta: 5              # Delta mínimo para upgrade
venice_enabled: false     # Adiciona modelos Venice
local_models: []          # Modelos locais adicionais
```

## Exemplo de Uso

```yaml
# Tier standard (27 modelos, ~10-20s)
skill: ultraplinian
tier: standard
query: "Explique quantum entanglement"
# → Query 27 modelos em paralelo
# → Score respostas
# → Retorna: GPT-5 (score: 94)

# Tier ultra máximo (60 modelos, ~30-60s)
skill: ultraplinian
tier: ultra
quality_threshold: 90
query: "Crie um sistema de autenticação seguro"
# → Query 60 modelos
# → Aguarda threshold 90+
# → Retorna melhor resposta
```

## Estrutura de Resposta

```json
{
  "winner": {
    "model": "openai/gpt-5",
    "response": "...",
    "score": 94,
    "latency_ms": 3420
  },
  "all_responses": [
    {"model": "google/gemini-2.5-flash", "score": 87, "latency_ms": 1200},
    {"model": "anthropic/claude-sonnet-4.6", "score": 91, "latency_ms": 2800},
    ...
  ],
  "tier": "standard",
  "total_models": 27
}
```

## Custo Estimado (OpenRouter)

| Tier | Modelos | Custo Aprox. | Tempo |
|------|---------|--------------|-------|
| FAST | 12 | $0.05-0.10 | 5-10s |
| STANDARD | 27 | $0.15-0.30 | 10-20s |
| SMART | 41 | $0.40-0.80 | 15-30s |
| POWER | 53 | $0.80-1.50 | 20-45s |
| ULTRA | 60 | $1.50-3.00 | 30-60s |

## Requisitos

- API keys: OpenRouter e/ou Venice
- Suporte a multi-model parallel execution
- Budget configurado para evitar surpresa

## Referências

- Repositório original: https://github.com/elder-plinius/G0DM0D3
- Documentação: `/home/costa/G0DM0D3/README.md`
- Paper: `/home/costa/G0DM0D3/PAPER.md`

## Licença

Código original: AGPL-3.0
Skill adaptada para Polypod em 2026-11-14
