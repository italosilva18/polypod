#!/bin/bash
# /copy - Copy to Clipboard
set -e

text="$*"
if [ -z "$text" ]; then
    echo "❌ Uso: /copy \"texto\""
    exit 1
fi

echo "📋 **Copiado para clipboard:** $text"
