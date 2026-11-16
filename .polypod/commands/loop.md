#!/bin/bash

# /loop - Recurring Prompt Scheduler
# Usage: /loop [interval] <prompt>

set -e

input="$*"

if [ -z "$input" ]; then
    cat << 'EOF'
🔄 **Loop - Recurring Prompt Scheduler**

Agenda prompt para execução recorrente.

**Uso:**
  /loop 5m /babysit-prs         # A cada 5 minutos
  /loop 30m check deploy        # A cada 30 minutos
  /loop check every 20m         # A cada 20 minutos
  /loop check deploy            # Default 10m

**Intervalos:**
  - Ns - segundos (min 1m)
  - Nm - minutos (5m, 10m, 30m)
  - Nh - horas (1h, 2h, 6h)
  - Nd - dias (1d, 2d)

**Exemplos:**
  /loop 5m /check-tests
  /loop 1h /status-report
  /loop 1d /daily-summary

⏳ **Status:** Implementação em andamento...

**Próximos passos:**
  - Parse interval + prompt
  - Converter para cron
  - Agendar execução
  - Executar imediatamente
EOF
    exit 0
fi

echo "🔄 **Loop agendado:**"
echo ""
echo "📝 **Input:** $input"
echo ""
echo "⏳ Parseando intervalo e prompt..."
echo ""
echo "**A fazer:**"
echo "  - Parse interval"
echo "  - Converter para cron"
echo "  - Agendar"
echo "  - Executar agora"
echo ""
echo "⏳ Implementação completa em andamento..."
