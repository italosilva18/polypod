#!/bin/bash

# /usage - Show Usage
# Usage: /usage [period]

set -e

period="${1:-summary}"

cat << 'EOF'
📊 **Usage - Uso de Recursos**

Mostra uso de tokens, custos e recursos.

**Uso:**
  /usage daily              # Hoje
  /usage weekly             # Semana
  /usage monthly            # Mês
  /usage model              # Por modelo

**Métricas:**
  - Tokens input
  - Tokens output
  - Total tokens
  - Custo (USD)
  - Requests
  - Modelos usados

⏳ **Status:** Implementação em andamento...

**Próximos passos:**
  - Integrar com billing API
  - Mostrar histórico
  - Alertas de limite
  - Projeções
EOF
