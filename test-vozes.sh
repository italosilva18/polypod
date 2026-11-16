#!/bin/bash
# Teste de Vozes - espeak vs Google TTS

echo "🎤 === COMPARAÇÃO DE VOZES ==="
echo ""
echo "1. espeak (robótico mas rápido):"
echo "   Tempo: ~0.1s | Qualidade: ⭐⭐"
espeak -v "pt-br+brazil" "Olá, eu sou o PolyPod" 2>&1
echo ""

echo "2. Google TTS (MUITO natural):"
echo "   Tempo: ~2s | Qualidade: ⭐⭐⭐⭐⭐"
python3 -c "from gtts import gTTS; gTTS('Olá, eu sou o PolyPod com voz natural', lang='pt-BR').save('/tmp/compare.mp3')" 2>&1
play /tmp/compare.mp3 2>&1 | head -2
echo ""

echo "3. Veredito:"
echo "   espeak: ⚡ Rápido, offline, robótico"
echo "   Google TTS: 🌐 Requer internet, MUITO natural"
echo ""
echo "✅ ESCOLHA SUA VOZ!"
