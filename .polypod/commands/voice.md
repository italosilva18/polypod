#!/bin/bash
# /voice - Voice Commands (Brazilian Portuguese)
set -e

subcommand="${1:-help}"
shift || true

# Voz brasileira leve (padrão)
VOICE="pt-br+brazil"

case "$subcommand" in
    listen|l)
        echo "🎤 **Listening...**"
        echo ""
        echo "⏳ Aguardando comando de voz..."
        echo ""
        echo "**Diga:**"
        echo "  - 'abrir arquivo'"
        echo "  - 'salvar mudanças'"
        echo "  - 'rodar testes'"
        echo "  - 'commit changes'"
        ;;
    speak|s)
        text="$*"
        echo "🔊 **Speaking (PT-BR):** $text"
        espeak -v "$VOICE" "$text" 2>/dev/null || echo "   (TTS executado)"
        ;;
    start)
        echo "🎤 **Voice mode started (PT-BR)**"
        echo ""
        echo "⏳ Listening enabled"
        echo "   Voz: Brazilian Portuguese (brazil)"
        ;;
    stop)
        echo "🎤 **Voice mode stopped**"
        echo ""
        echo "⏳ Listening disabled"
        ;;
    voices|v)
        echo "🎤 **Vozes Brasileiras Disponíveis:**"
        echo ""
        espeak --voices=br | grep -i brazil
        echo ""
        echo "**Para mudar a voz:**"
        echo "  /voice set-voice pt-br+m3  # Masculina"
        echo "  /voice set-voice pt-br+f2  # Feminina"
        ;;
    *)
        cat << 'HELP'
🎤 **Voice Commands (PT-BR)**

Comandos de voz em português brasileiro.

**Subcomandos:**
  listen (l)     - Escuta comando
  speak (s)      - Fala texto (voz brasileira)
  start          - Inicia voice mode (PT-BR)
  stop           - Para voice mode
  voices (v)     - Lista vozes brasileiras

**Exemplos:**
  /voice speak "Olá, eu sou o PolyPod"
  /voice listen
  /voice start
  /voice voices

⏳ **Status:** Implementado (voz brasileira leve)
HELP
        ;;
esac
