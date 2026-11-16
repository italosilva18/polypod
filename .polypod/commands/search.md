#!/bin/bash

# /search - Global Code Search (Ctrl+Shift+F)
# Usage: /search <query>

set -e

query="${*:-}"

if [ -z "$query" ]; then
    cat << 'EOF'
🔍 **Global Search**

Busca fuzzy em todo o código com ripgrep.

**Uso:**
  /search <query>           # Busca query
  /search "function main"   # Busca exata
  /search TODO              # Busca simples

**Atalho:** Ctrl+Shift+F (em implementação)

**Features:**
  ✅ Ripgrep streaming
  🚧 Preview de contexto
  🚧 Highlight de matches
  🚧 Navegação TUI

⏳ **Status:** Implementação básica pronta, UI em andamento...

**Exemplo:**
  /search TODO
  /search "console.log"
EOF
    exit 0
fi

echo "🔍 **Buscando:** \`$query\`"
echo ""

# Check if ripgrep is available
if ! command -v rg &> /dev/null; then
    echo "⚠️  ripgrep (rg) não encontrado. Usando grep..."
    
    # Fallback to grep
    results=$(grep -r --include="*.go" --include="*.ts" --include="*.tsx" --include="*.js" --include="*.py" "$query" . 2>/dev/null | head -20 || true)
    
    if [ -z "$results" ]; then
        echo "❌ Nenhum resultado encontrado."
    else
        echo "📄 **Resultados:**"
        echo ""
        echo "$results"
        echo ""
        echo "(usando grep - instale ripgrep para melhor performance)"
    fi
else
    # Use ripgrep
    echo "📄 **Resultados:**"
    echo ""
    
    # Run ripgrep with JSON output for parsing
    results=$(rg --color=never --line-number --heading "$query" . 2>/dev/null | head -50 || true)
    
    if [ -z "$results" ]; then
        echo "❌ Nenhum resultado encontrado."
    else
        echo "$results"
        echo ""
        echo "(mostrando primeiros 50 resultados)"
    fi
fi

echo ""
echo "💡 **Dica:** Instale ripgrep (rg) para busca mais rápida"
