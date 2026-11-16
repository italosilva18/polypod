#!/bin/bash
# Teste Completo do Modo de Voz

echo "🎤 === TESTE DO MODO DE VOZ POLYPOD ==="
echo ""

# Teste 1: TTS com espeak
echo "1. Testando TTS (Text-to-Speech):"
echo ""
espeak "Olá, eu sou o PolyPod" && echo "   ✅ TTS funcionando!" || echo "   ❌ TTS falhou"
echo ""

# Teste 2: Gravar áudio com sox (simulado)
echo "2. Testando gravação (sox):"
echo ""
if command -v rec &> /dev/null; then
    echo "   ✅ sox (rec) disponível"
    echo "   Para gravar: rec teste.wav"
else
    echo "   ⚠️ rec não disponível"
fi
echo ""

# Teste 3: Comandos do PolyPod
echo "3. Comandos /voice disponíveis:"
echo ""
echo "   /voice start        - Inicia modo de voz"
echo "   /voice listen       - Escuta comando"
echo "   /voice speak TEXTO  - Fala texto"
echo "   /voice stop         - Para modo de voz"
echo ""

# Teste 4: Conteúdo do command
echo "4. Conteúdo do /voice command:"
echo ""
cat .polypod/commands/voice.md | grep -A 3 "case"
echo ""

# Teste 5: Instruções de uso
echo "5. PARA USAR NO POLYPOD:"
echo ""
echo "   cd /home/costa/Área de trabalho/CLI/polypod"
echo "   ./polypod"
echo ""
echo "   Dentro do PolyPod:"
echo "   /voice start"
echo "   /voice speak 'Olá mundo'"
echo "   /voice listen"
echo "   /voice stop"
echo ""

echo "=== TESTE COMPLETO ==="
