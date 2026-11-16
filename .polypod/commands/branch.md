#!/bin/bash

# /branch - Git Branch Management for PolyPod
# Usage: /branch <subcommand> [args]

set -e

subcommand="${1:-help}"
shift || true

case "$subcommand" in
    list|ls|l)
        echo "📑 **Branches locais:**"
        echo ""
        git branch --no-color | while read -r line; do
            if [[ $line == "* "* ]]; then
                name="${line#* }"
                echo "🟢 **$name**"
            else
                echo "   $line"
            fi
        done
        echo ""
        count=$(git branch | wc -l)
        echo "Total: $count branches"
        ;;
    
    create|c|new)
        if [ -z "$1" ]; then
            echo "❌ Uso: /branch create <nome>"
            exit 1
        fi
        name="$1"
        
        # Auto-format branch name
        formatted=$(echo "$name" | tr '[:upper:]' '[:lower:]' | sed 's/ /-/g; s/_/-/g; s/\./-/g' | sed 's/--*/-/g' | sed 's/^-//; s/-$//')
        
        if [[ ! $formatted == claude/* ]]; then
            formatted="claude/$formatted"
        fi
        
        git checkout -b "$formatted"
        echo "✅ Branch **$formatted** criada com sucesso!"
        echo ""
        echo "🔀 Switched to branch"
        ;;
    
    switch|checkout|s|co)
        if [ -z "$1" ]; then
            echo "❌ Uso: /branch switch <nome>"
            exit 1
        fi
        name="$1"
        
        # Check for uncommitted changes
        if ! git diff --quiet; then
            echo "🔴 Você tem mudanças não commitadas!"
            echo "Faça commit ou stash antes de trocar de branch."
            exit 1
        fi
        
        git checkout "$name"
        echo "✅ Switched to branch **$name**"
        ;;
    
    delete|del|d|remove|rm)
        if [ -z "$1" ]; then
            echo "❌ Uso: /branch delete <nome>"
            exit 1
        fi
        name="$1"
        force=""
        
        if [ "$2" == "-f" ] || [ "$2" == "--force" ]; then
            force="-D"
        else
            force="-d"
        fi
        
        current=$(git rev-parse --abbrev-ref HEAD)
        if [ "$current" == "$name" ]; then
            echo "❌ Não é possível deletar a branch atual"
            exit 1
        fi
        
        git branch $force "$name"
        if [ -n "$force" ] && [ "$force" == "-D" ]; then
            echo "✅ Branch **$name** deletada (forçado)"
        else
            echo "✅ Branch **$name** deletada"
        fi
        ;;
    
    current|cur)
        current=$(git rev-parse --abbrev-ref HEAD)
        
        if git diff --quiet; then
            status="🟢 (clean)"
        else
            status="🔴 (modificado)"
        fi
        
        echo "📍 Branch atual: **$current** $status"
        ;;
    
    help|h|?|*)
        cat << 'EOF'
📑 **Gerenciamento de Branches Git**

**Uso:** /branch <subcommand> [args]

**Subcomandos:**
  list (ls, l)       - Lista todas as branches
  create (c, new)    - Cria nova branch
  switch (s, co)     - Troca para branch existente
  delete (d, rm)     - Deleta branch
  current (cur)      - Mostra branch atual
  help (h, ?)        - Mostra esta ajuda

**Exemplos:**
  /branch list
  /branch create feature-x
  /branch create "Implementar feature X"
  /branch switch feature-x
  /branch delete feature-x
  /branch delete feature-x -f

**Dicas:**
- Nomes são auto-formatados com prefixo "claude/"
- Branch switch verifica mudanças não commitadas
- Use -f para forçar deleção de branch não merged
EOF
        ;;
esac
