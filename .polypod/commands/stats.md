#!/bin/bash

# /stats - Usage Statistics for PolyPod
# Usage: /stats [period]

set -e

period="${1:-summary}"

echo "📊 **Estatísticas de Uso**"
echo ""

case "$period" in
    daily|hoje)
        echo "📅 **Hoje**"
        echo ""
        echo "⏳ Implementação em andamento..."
        echo ""
        echo "**Métricas:**"
        echo "  - Sessões: (pendente)"
        echo "  - Tokens: (pendente)"
        echo "  - Tool calls: (pendente)"
        echo "  - Custo: (pendente)"
        ;;
    
    weekly|semana)
        echo "📅 **Esta Semana**"
        echo ""
        echo "⏳ Implementação em andamento..."
        ;;
    
    monthly|mês)
        echo "📅 **Este Mês**"
        echo ""
        echo "⏳ Implementação em andamento..."
        ;;
    
    model|modelo)
        echo "🤖 **Por Modelo**"
        echo ""
        echo "⏳ Implementação em andamento..."
        ;;
    
    cost|custo)
        echo "💰 **Custos**"
        echo ""
        echo "⏳ Implementação em andamento..."
        ;;
    
    *)
        cat << 'EOF'
📊 **Estatísticas de Uso**

**Períodos:**
  daily, hoje        - Estatísticas de hoje
  weekly, semana     - Esta semana
  monthly, mês       - Este mês
  model, modelo      - Por modelo
  cost, custo        - Custos

**Métricas (em implementação):**
  - Total de sessões
  - Tokens usados (input/output)
  - Tool calls
  - Custo por provider
  - Modelos mais usados
  - Tempo médio de sessão

⏳ **Status:** Implementação em andamento...

**Próximos passos:**
  1. Integrar com SQLite (data/polypod.db)
  2. Adicionar tracking de eventos
  3. Criar queries de agregação
  4. Visualização TUI
EOF
        ;;
esac
