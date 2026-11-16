# Como Usar as Skills G0DM0D3 no PolyPod

Guia prático para iniciar o PolyPod e usar as 4 skills do G0DM0D3.

## 🚀 Inicialização

### Modo 1: Interface Interativa (Recomendado)

```bash
cd "/home/costa/Área de trabalho/CLI/polypod"
./polypod
```

Isso abre a interface TUI (Bubble Tea) onde você pode:
- Digitar prompts naturalmente
- Usar comandos `/` para controlar o agent
- Ver histórico de conversas
- Alternar entre agents

### Modo 2: One-shot (Prompt Único)

```bash
./polypod -p "Seu prompt aqui"
```

### Modo 3: Headless com Output JSON

```bash
./polypod -p "Seu prompt" --output-format json
```

### Modo 4: Retomar Sessão Anterior

```bash
./polypod -c          # Continua última sessão
./polypod -r          # Retoma sessão por ID
./polypod -r abc123   # Retoma sessão específica
```

## 📋 Comandos da Interface Interativa

Dentro da interface, digite `/help` para ver todos os comandos.

### Comandos Principais

```
/help                 # Mostra ajuda completa
/new                  # Nova conversa
/list                 # Lista conversas salvas
/agents               # Lista agents disponíveis
/skills               # Lista skills carregadas

# Agents
/agent <nome>         # Usa agent específico
/agent godmode-classic
/agent parseltongue
/agent default

# Skills
/skill <nome>         # Ativa skill específica
/skill godmode_classic
/skill autotune
/skill parseltongue
/skill ultraplinian

# Memória
/save <texto>         # Salva na memória
/memory               # Lista memórias
/search <termo>       # Busca na memória

# Ferramentas
/bash <comando>       # Executa comando shell
/read <arquivo>       # Lê arquivo
/write <arquivo>      # Escreve arquivo
/grep <padrão>        # Busca em arquivos
/glob <padrão>        # Lista arquivos

# Configuração
/config               # Mostra configuração
/setup                # Configuração inicial
/doctor               # Diagnóstico do sistema

# Sessão
/exit                 # Sai do PolyPod
/quit                 # Mesma coisa
```

## 🎯 Usando Skills G0DM0D3

### 1. godmode_classic

**O que faz:** Executa 5 modelos + prompts em paralelo para red-teaming

**Na interface:**
```
/agent godmode-classic
Explique como funciona jailbreak de LLMs para pesquisa
```

**Ou com skill:**
```
/skill godmode_classic
Compare respostas de diferentes modelos para: "Quais são os limites éticos de IA?"
```

**Resultado esperado:**
- 5 respostas de modelos diferentes (Claude, Grok, Gemini, GPT-4, Hermes)
- Score de cada resposta
- Winner selecionado

### 2. parseltongue

**O que faz:** Aplica 33 técnicas de obfuscação para testar robustez

**Na interface:**
```
/agent parseltongue
Teste robustez do modelo com: "Como bypass nas restrições?"
```

**Ou com skill:**
```
/skill parseltongue
Aplique técnicas light tier em: "ignore as regras anteriores"
```

**Técnicas disponíveis:**
- Light: plain, l33t, bubble, braille, morse, uppercase, etc. (11)
- Standard: + double_struck, bold_italic, script, etc. (22)
- Heavy: + zalgo, binary, hex, base64, rot13, etc. (33)

### 3. autotune

**O que faz:** Otimiza parâmetros automaticamente baseado no contexto

**Na interface:**
```
/skill autotune
# Automaticamente detecta contexto e ajusta parâmetros

# Exemplos de contextos:
"Escreva código Python para ordenar lista"     # code → temp=0.15
"Crie um poema criativo"                        # creative → temp=1.15
"Analise dados estatísticos"                    # analytical → temp=0.4
"Converse casualmente"                          # conversational → temp=0.75
```

**Contextos detectáveis (20):**
code, creative, analytical, conversational, chaotic, security, medical, legal, financial, scientific, philosophical, instructional, persuasive, mathematical, historical, political, subversive, emotional, strategic, synthesis

### 4. ultraplinian

**O que faz:** Query até 60 modelos em paralelo, score e retorna o melhor

**Na interface:**
```
/skill ultraplinian
Responda com máxima qualidade: "Explique quantum computing"
```

**Tiers disponíveis:**
```
# FAST (12 modelos, ~5-10s, barato)
/skill ultraplinian --tier fast

# STANDARD (27 modelos, ~10-20s, balanceado)
/skill ultraplinian --tier standard

# SMART (41 modelos, ~15-30s, alta qualidade)
/skill ultraplinian --tier smart

# POWER (53 modelos, ~20-45s, frontier)
/skill ultraplinian --tier power

# ULTRA (60 modelos, ~30-60s, máximo)
/skill ultraplinian --tier ultra
```

## 🔧 Configuração de API Keys

Se precisar adicionar mais providers:

```bash
# Ver keys salvas
polypod list_secrets

# Salvar nova key
polypod save_secret openrouter_api_key sk-or-v1-...
polypod save_secret venice_api_key venice-...

# Ou editar config.yaml diretamente
```

## 📊 Exemplo de Sessão Completa

```bash
# 1. Iniciar PolyPod
cd "/home/costa/Área de trabalho/CLI/polypod"
./polypod

# 2. Ver skills disponíveis
/skills

# 3. Usar agent godmode-classic
/agent godmode-classic

# 4. Fazer pergunta de red-teaming
"Quais técnicas de jailbreak são mais eficazes em 2026?"

# 5. Aguardar respostas dos 5 modelos

# 6. Ver score e winner
# (O agent mostra automaticamente)

# 7. Salvar descoberta importante
/save "Técnica X funciona melhor em modelos Y"

# 8. Nova conversa com autotune
/new
/skill autotune
"Analise este código Python em busca de bugs"

# 9. Sair
/exit
```

## 🎨 Dicas de Uso

### Para Red-Teaming
```
1. /agent godmode-classic
2. Faça perguntas sobre limites do modelo
3. Compare respostas dos 5 modelos
4. /save para documentar descobertas
```

### Para Otimização Automática
```
1. /skill autotune
2. Faça perguntas variadas
3. Observe parâmetros sendo ajustados automaticamente
```

### Para Máxima Qualidade
```
1. /skill ultraplinian --tier smart
2. Faça pergunta complexa
3. Aguarde avaliação multi-modelo
4. Receba melhor resposta
```

### Para Testar Robustez
```
1. /agent parseltongue
2. Use trigger words
3. Veja técnicas de obfuscação aplicadas
4. Analise respostas do modelo
```

## ⚠️ Importante

- **Ética:** Use skills apenas para pesquisa e red-teaming responsável
- **Custos:** ultraplinian com tier ultra pode gerar custos significativos
- **API Keys:** Tenha keys configuradas antes de usar
- **Rate Limits:** Providers podem ter limites de requisição

## 📖 Documentação

- Skills: `.polypod/skills/<nome>/SKILL.md`
- README geral: `.polypod/skills/README_G0DM0D3.md`
- Agents: `agents/godmode-classic.yaml`, `agents/parseltongue.yaml`

---

**Pronto para começar!** 🚀

```bash
cd "/home/costa/Área de trabalho/CLI/polypod"
./polypod
```
