#!/bin/bash

# /sticker - Add Sticker
# Usage: /sticker [sticker_name]

set -e

sticker="${1:-}"

if [ -z "$sticker" ]; then
    cat << 'EOF'
🎉 **Stickers**

Adiciona stickers divertidos no TUI.

**Stickers disponíveis:**
  - celebrate               # 🎉 Celebração
  - thumbsup                # 👍 Joinha
  - rocket                  # 🚀 Foguete
  - fire                    # 🔥 Fogo
  - heart                   # ❤️  Coração
  - star                    # ⭐ Estrela
  - lightbulb               # 💡 Ideia
  - check                   # ✅ Check

**Uso:**
  /sticker celebrate        # Adiciona sticker
  /sticker rocket           # Adiciona foguete

⏳ **Status:** Implementação em andamento...
EOF
    exit 0
fi

echo ""
echo "🎉 **Sticker:** $sticker"
echo ""
echo "⏳ Renderizando sticker... (implementação em andamento)"
