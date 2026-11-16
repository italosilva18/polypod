#!/bin/bash

# /skillify - Capture Session as Skill
# Usage: /skillify [description]

set -e

description="$*"

if [ -z "$description" ]; then
    cat << 'EOF'
🛠️  **Skillify - Capture Session Process**

Captura processo repetitivo da sessão como skill reutilizável.

**Uso:**
  /skillify "deploy workflow"      # Captura processo
  /skillify "review process"       # Captura review
  /skillify                        # Mostra status

**Diferença para /learn:**
  - `/skillify`: Captura processo atual
  - `/learn`: Aprende de forma mais genérica

**O que será capturado:**
  - Steps do processo
  - Ferramentas usadas
  - Critérios de sucesso
  - Preferências do usuário
  - Argumentos necessários

⏳ **Status:** Implementação em andamento...

**Entrevista automática:**
  Round 1: Nome e descrição
  Round 2: Steps e argumentos
  Round 3: Detalhes de cada step
  Round 4: Confirmação final
EOF
    exit 0
fi

echo "🛠️  **Skillify:** $description"
echo ""
echo "📊 **Analisando sessão atual...**"
echo ""
echo "**Mensagens do usuário:** (pendente)"
echo "**Memória da sessão:** (pendente)"
echo ""
echo "🎯 **Entrevista (4 rounds):**"
echo ""
echo "  **Round 1:** Nome e descrição"
echo "  **Round 2:** Steps e argumentos"
echo "  **Round 3:** Detalhes dos steps"
echo "  **Round 4:** Confirmação"
echo ""
echo "⏳ Implementação completa em andamento..."
echo ""
echo "**Skill será salva em:**"
echo "  - Repo: .polypod/skills/<nome>/SKILL.md"
echo "  - Pessoal: ~/.polypod/skills/<nome>/SKILL.md"
