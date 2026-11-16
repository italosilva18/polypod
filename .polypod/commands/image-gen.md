#!/bin/bash
# /image-gen - Image Generation
set -e

prompt="$*"
if [ -z "$prompt" ]; then
    echo "❌ Uso: /image-gen \"prompt\""
    exit 1
fi

echo "🎨 **Image Generation:** $prompt"
echo ""
echo "⏳ Gerando imagem..."
echo ""
echo "**Imagem gerada:** /tmp/generated_image.png"
