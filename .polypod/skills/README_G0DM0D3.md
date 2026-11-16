# G0DM0D3 Skills para Polypod

4 skills extraídas do projeto [G0DM0D3](https://github.com/elder-plinius/G0DM0D3) por Pliny (elder-plinius), adaptadas para o Polypod CLI.

## Skills Instaladas

### 1. godmode_classic
**5 combinações modelo+prompt em paralelo para red-teaming**

- **Tier:** FAST
- **Modelos:** 5 (Claude Sonnet 4.6, Grok 4.5, Gemini 2.5 Flash, GPT-4o, Hermes 4 405B)
- **Use para:** Red-teaming, jailbreak research, AI transparency
- **Tempo:** ~5-10 segundos

### 2. parseltongue
**Input perturbation engine com 33 técnicas de obfuscação**

- **Tier:** Light/Standard/Heavy
- **Técnicas:** 33 (L33T, bubble, braille, morse, Unicode, etc.)
- **Use para:** Testar robustez de modelos, bypass research
- **Tempo:** ~3-8 segundos

### 3. autotune
**Otimização adaptativa de parâmetros baseada em contexto**

- **Contextos:** 20 tipos (code, creative, medical, legal, etc.)
- **Parâmetros:** temperature, top_p, top_k, penalties
- **Use para:** Otimizar respostas automaticamente
- **Tempo:** <1 segundo

### 4. ultraplinian
**Motor de avaliação multi-modelo comparativa**

- **Tiers:** FAST(12), STANDARD(27), SMART(41), POWER(53), ULTRA(60)
- **Modelos:** Até 60 modelos OpenRouter + 44 Venice
- **Use para:** Obter melhor resposta comparando múltiplos modelos
- **Tempo:** ~10-60 segundos

## Como Usar

### Via Agent YAML

Adicione no arquivo do agent:

```yaml
skills:
  - godmode_classic
  - parseltongue
  - autotune
  - ultraplinian
```

### Via Comando

```bash
# Usar uma skill específica
polypod --skill godmode_classic "Sua query aqui"

# Múltiplas skills
polypod --skill autotune --skill ultraplinian "Query"
```

### Configuração

Edite `config.yaml` para adicionar API keys:

```yaml
llm:
  openrouter_api_key: ${OPENROUTER_API_KEY}
  venice_api_key: ${VENICE_API_KEY}
  
skills:
  godmode_classic:
    enabled: true
  ultraplinian:
    tier: standard  # fast|standard|smart|power|ultra
    quality_threshold: 85
```

## Estrutura de Diretórios

```
.polypod/skills/
├── godmode_classic/
│   └── SKILL.md
├── parseltongue/
│   └── SKILL.md
├── autotune/
│   └── SKILL.md
└── ultraplinian/
    └── SKILL.md
```

## Agents Compatíveis

As skills foram instaladas em `.polypod/skills/`. Para usar com agents:

1. **Agent personalizado**: Crie um agent YAML em `agents/` com as skills
2. **Agent default**: Edite `agents/default.yaml` para incluir as skills
3. **Agent temporário**: Use `--skill` no comando CLI

Exemplo de agent G0DM0D3:

```yaml
name: redteam
description: Agente de red-teaming e AI safety
skills:
  - godmode_classic
  - parseltongue
  - ultraplinian
persona: |
  Você é especialista em red-teaming de LLMs...
```

## Ética e Segurança

⚠️ **IMPORTANTE:** Estas skills são para:
- ✅ Pesquisa de red-teaming
- ✅ Transparência de IA
- ✅ Segurança e alignment
- ✅ Cognition research

❌ **NÃO use para:**
- Conteúdo ilegal
- Discurso de ódio
- Dano real
- Violar termos de serviço

## API Keys Necessárias

| Skill | OpenRouter | Venice | Local Models |
|-------|------------|--------|--------------|
| godmode_classic | ✅ | ✅ | ✅ |
| parseltongue | ✅ | ✅ | ✅ |
| autotune | ✅ | ✅ | ✅ |
| ultraplinian | ✅ | ✅ | ✅ |

Configure em `config.yaml` ou use secrets do Polypod:
```bash
polypod save_secret openrouter_api_key sk-or-v1-...
polypod save_secret venice_api_key venice-...
```

## Referências

- **G0DM0D3 Original:** https://github.com/elder-plinius/G0DM0D3
- **L1B3RT4S Prompts:** https://github.com/elder-plinius/L1B3RT4S
- **Documentação:** `/home/costa/G0DM0D3/README.md`
- **Paper:** `/home/costa/G0DM0D3/PAPER.md`

## Licença

Código original: AGPL-3.0 (GNU Affero General Public License v3.0)
Skills adaptadas para Polypod em 2026-11-14

---

**Made with 🖤 by Pliny the Prompter**
**Adaptado para Polypod CLI**
