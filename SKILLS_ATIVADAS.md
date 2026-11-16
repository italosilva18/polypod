# ✅ Skills G0DM0D3 Ativadas no Agent Default!

## O Que Foi Feito

Editei `agents/default.yaml` para incluir as 4 skills G0DM0D3 na lista de skills ativas do agent default.

```yaml
# G0DM0D3 skills
- godmode_classic
- parseltongue
- autotune
- ultraplinian
```

## 🎯 Agora, Quando Você Iniciar o PolyPod

```bash
cd "/home/costa/Área de trabalho/CLI/polypod"
./polypod
```

**As 4 skills estarão DISPONÍVEIS automaticamente!** ✅

## 📋 Status Atual

| Skill | Instalada | Carregada | Ativa no Default |
|-------|-----------|-----------|------------------|
| **godmode_classic** | ✅ | ✅ | ✅ |
| **parseltongue** | ✅ | ✅ | ✅ |
| **autotune** | ✅ | ✅ | ✅ |
| **ultraplinian** | ✅ | ✅ | ✅ |

## 🚀 Como Usar (Agora)

### Opção 1: Agent Default (Já tem as skills)

```bash
./polypod
# Dentro, pode usar diretamente:
godmode_classic "sua query"
# Ou
autotune "analise este código"
```

### Opção 2: Agents Especializados

```bash
./polypod
# Dentro:
/agent godmode-classic    # Focado em red-teaming
/agent parseltongue       # Focado em obfuscação
```

### Opção 3: Ativar/Desativar Manualmente

```bash
./polypod
# Dentro:
/skill godmode_classic    # Ativa se não estiver
/skill autotune           # Ativa se não estiver
```

## 📖 Lista Completa de Skills no Default

O agent `default` agora tem ~110 skills, incluindo:

- **Ferramentas básicas:** read_file, write_file, bash, glob, grep
- **Memória:** save_memory, recall_memory, list_memories
- **Web:** web_search, web_fetch, tor_fetch
- **Computer control:** computer_screenshot, computer_click, etc.
- **Agents:** agent_run, team_*, plan_*
- **Hardware:** i2c_*, spi_*, serial_*
- **G0DM0D3:** godmode_classic, parseltongue, autotune, ultraplinian ✨

## ⚡ Teste Rápido

```bash
cd "/home/costa/Área de trabalho/CLI/polypod"
./polypod

# Dentro da interface, digite:
/skills

# Você deve ver todas as 4 skills G0DM0D3 listadas!
```

## 🎉 Resumo

**ANTES:** Skills instaladas, mas precisava ativar manualmente com `/skill <nome>`

**AGORA:** Skills já estão na lista do agent default → disponíveis imediatamente!

---

**Pronto para usar!** 🚀

```bash
./polypod
# E comece a usar as skills G0DM0D3 naturalmente!
```
