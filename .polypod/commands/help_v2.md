#!/bin/bash

# /help - Help System V2
# Usage: /help [category]

set -e

category="${1:-}"

case "$category" in
    commands|cmds|c)
        cat << 'EOF'
📚 **Comandos Disponíveis**

**Sessão:**
  /compact              - Resume conversa
  /export               - Exporta sessão
  /context              - Visualiza contexto
  /rewind               - Checkpoints

**Git:**
  /branch               - Gerencia branches
  /branch create        - Cria branch
  /branch switch        - Troca branch
  /branch list          - Lista branches

**Busca:**
  /search               - Busca código
  /history              - Busca histórico

**Tarefas:**
  /task                 - Gerencia tarefas
  /task list            - Lista tarefas
  /task add             - Adiciona tarefa

**Analytics:**
  /stats                - Estatísticas
  /stats daily          - Hoje
  /stats weekly         - Semana
  /stats cost           - Custos

**UI:**
  /theme                - Temas
  /help                 - Esta ajuda
EOF
        ;;
    
    git|g)
        cat << 'EOF'
📚 **Git Commands**

/branch list            - Lista branches
/branch create <nome>   - Cria branch
/branch switch <nome>   - Troca branch
/branch delete <nome>   - Deleta branch
/branch current         - Branch atual
/branch help            - Ajuda git

**Exemplos:**
  /branch create feature-x
  /branch switch claude/implementar-foo
  /branch delete feature-x -f
EOF
        ;;
    
    session|s)
        cat << 'EOF'
📚 **Session Commands**

/compact                - Resume conversa
/export markdown        - Exporta como MD
/export json            - Exporta como JSON
/context show           - Mostra contexto
/rewind save            - Salva checkpoint
EOF
        ;;
    
    *)
        cat << 'EOF'
📚 **Help System**

**Categorias:**
  commands (cmds, c)    - Todos os comandos
  git (g)               - Git commands
  session (s)           - Session commands
  task (t)              - Task commands
  stats                 - Analytics

**Atalhos:**
  /help                 - Esta ajuda
  /help commands        - Lista comandos
  /help git             - Git help
  /help <comando>       - Ajuda específica

**Dicas:**
  - Use / para autocompletar
  - Ctrl+K busca comandos
  - /help <cmd> mostra detalhes
EOF
        ;;
esac
