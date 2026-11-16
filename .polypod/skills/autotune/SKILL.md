---
name: autotune
description: Otimização adaptativa de parâmetros LLM baseada em detecção de contexto (20 tipos)
---

# AutoTune — Context-Adaptive Parameter Optimization

Classifica queries em 20 contextos e ajusta automaticamente temperature, top_p, top_k, frequency_penalty, presence_penalty e repetition_penalty.

## Quando Usar

- Otimizar respostas de LLMs automaticamente
- Adaptar parâmetros ao tipo de tarefa
- Melhorar qualidade sem ajuste manual
- Multi-tasking com diferentes requisitos

## Contextos (20 tipos)

Cada contexto tem um perfil de parâmetros otimizado:

### Code
**Padrões:** código, função, classe, bug, debug, teste, API
```json
{ "temperature": 0.15, "top_p": 0.8, "top_k": 25, "frequency_penalty": 0.2 }
```

### Creative
**Padrões:** história, poema, criativo, imagine, escreva, arte
```json
{ "temperature": 1.15, "top_p": 0.95, "top_k": 85, "frequency_penalty": 0.5 }
```

### Analytical
**Padrões:** análise, dados, compare, avalie, lógica
```json
{ "temperature": 0.4, "top_p": 0.88, "top_k": 40 }
```

### Conversational
**Padrões:** olá, como vai, conte, converse
```json
{ "temperature": 0.75, "top_p": 0.9, "top_k": 50 }
```

### Chaotic
**Padrões:** aleatório, surpreso, inesperado, caótico
```json
{ "temperature": 1.7, "top_p": 0.99, "top_k": 100 }
```

### Security
**Padrões:** segurança, vulnerabilidade, exploit, pentest
```json
{ "temperature": 0.3, "top_p": 0.85, "top_k": 35 }
```

### Medical
**Padrões:** médico, saúde, sintoma, doença, tratamento
```json
{ "temperature": 0.25, "top_p": 0.82, "top_k": 30 }
```

### Legal
**Padrões:** jurídico, lei, contrato, processo, advogado
```json
{ "temperature": 0.2, "top_p": 0.8, "top_k": 28 }
```

### Financial
**Padrões:** financeiro, investimento, ação, mercado, economia
```json
{ "temperature": 0.3, "top_p": 0.85, "top_k": 35 }
```

### Scientific
**Padrões:** ciência, pesquisa, experimento, hipótese
```json
{ "temperature": 0.35, "top_p": 0.85, "top_k": 35 }
```

### Philosophical
**Padrões:** filosofia, ética, moral, existência, sentido
```json
{ "temperature": 0.9, "top_p": 0.92, "top_k": 65 }
```

### Instructional
**Padrões:** tutorial, como fazer, passo a passo, guia
```json
{ "temperature": 0.3, "top_p": 0.85, "top_k": 30 }
```

### Persuasive
**Padrões:** persuada, argumente, debata, convença
```json
{ "temperature": 0.8, "top_p": 0.9, "top_k": 55 }
```

### Mathematical
**Padrões:** matemática, cálculo, equação, prova, teorema
```json
{ "temperature": 0.1, "top_p": 0.75, "top_k": 20 }
```

### Historical
**Padrões:** história, histórico, passado, evento histórico
```json
{ "temperature": 0.5, "top_p": 0.88, "top_k": 45 }
```

### Political
**Padrões:** política, governo, eleição, policy
```json
{ "temperature": 0.7, "top_p": 0.9, "top_k": 55 }
```

### Subversive
**Padrões:** subversivo, alternativo, contra-cultural
```json
{ "temperature": 1.4, "top_p": 0.97, "top_k": 90 }
```

### Emotional
**Padrões:** emocional, sentimentos, relacionamento, apoio
```json
{ "temperature": 0.85, "top_p": 0.92, "top_k": 60 }
```

### Strategic
**Padrões:** estratégia, planejamento, jogo, negócios
```json
{ "temperature": 0.45, "top_p": 0.88, "top_k": 42 }
```

### Synthesis
**Padrões:** resumo, sintetize, combine, integre
```json
{ "temperature": 0.6, "top_p": 0.9, "top_k": 50 }
```

## Estratégias Alternativas

Quando não usar detecção automática:

| Estratégia | Temperature | top_p | top_k | Uso |
|------------|-------------|-------|-------|-----|
| **precise** | 0.2 | 0.85 | 30 | Factual, determinístico |
| **balanced** | 0.7 | 0.9 | 50 | Propósito geral |
| **creative** | 1.1 | 0.95 | 80 | Ideação, escrita |
| **chaotic** | 1.6 | 0.98 | 100 | Exploração aleatória |

## Algoritmo de Detecção

1. **Pattern matching**: Regex para cada contexto (3x peso na query atual)
2. **Histórico**: Últimas 4 mensagens (1x peso)
3. **Scoring**: Soma pesos por contexto
4. **Confiança**: best_score / total_score
5. **Blend**: Se confiança < 0.6, mistura com balanced
6. **GODMODE boost**: +0.1 temperature, +0.15 presence_penalty

## Parâmetros de Saída

```json
{
  "context": "code",
  "confidence": 0.85,
  "params": {
    "temperature": 0.15,
    "top_p": 0.8,
    "top_k": 25,
    "frequency_penalty": 0.2,
    "presence_penalty": 0.0,
    "repetition_penalty": 1.05
  }
}
```

## Exemplo de Uso

```yaml
# Detecta contexto automaticamente
skill: autotune
query: "Crie uma função Python para ordenar lista"
# → Detecta: code (confiança: 0.92)
# → Aplica: temperature=0.15, top_p=0.8, top_k=25

# Usa estratégia fixa
skill: autotune
strategy: creative
query: "Escreva um poema sobre o mar"
# → Aplica: temperature=1.1, top_p=0.95, top_k=80
```

## Referências

- Repositório original: https://github.com/elder-plinius/G0DM0D3
- Documentação: `/home/costa/G0DM0D3/README.md`

## Licença

Código original: AGPL-3.0
Skill adaptada para Polypod em 2026-11-14
