#!/bin/bash

# /issue - Create GitHub Issue
# Usage: /issue [title]

set -e

title="$*"

if [ -z "$title" ]; then
    cat << 'EOF'
📋 **Create Issue**

Cria issue no GitHub automaticamente.

**Uso:**
  /issue "Bug: login falha"      # Cria issue
  /issue "Feature: export PDF"   # Feature request

**Template:**
  - Title: fornecida
  - Description: gerada por IA
  - Labels: sugeridas
  - Assignee: você

⏳ **Status:** Implementação em andamento...

**Próximos passos:**
  - Integrar com GitHub API
  - Auto-preencher descrição
  - Sugerir labels
  - Atribuir automaticamente
EOF
    exit 0
fi

echo "📋 **Criando Issue:**"
echo ""
echo "📝 **Título:** $title"
echo ""
echo "⏳ Gerando descrição com IA..."
echo ""
echo "**Labels sugeridas:** (pendente)"
echo "**Assignee:** (pendente)"
echo ""
echo "🔧 Implementação em andamento..."
