#!/bin/bash
# Teste do Modo de Voz PolyPod

echo "🎤 === TESTE DO MODO DE VOZ POLYPOD ==="
echo ""

# Teste 1: Verificar package Go
echo "1. Package voice:"
if [ -f "internal/voice/voice.go" ]; then
    echo "   ✅ internal/voice/voice.go existe"
else
    echo "   ❌ internal/voice/voice.go não existe"
fi

# Teste 2: Verificar command
echo ""
echo "2. Command voice:"
if [ -f ".polypod/commands/voice.md" ]; then
    echo "   ✅ .polypod/commands/voice.md existe"
else
    echo "   ❌ .polypod/commands/voice.md não existe"
fi

# Teste 3: Conteúdo do command
echo ""
echo "3. Conteúdo do command /voice:"
cat .polypod/commands/voice.md | head -30

# Teste 4: Simulação de uso
echo ""
echo "4. Para usar no PolyPod:"
echo "   ./polypod"
echo "   /voice start      # Inicia modo de voz"
echo "   /voice listen     # Escuta comando"
echo "   /voice speak 'Olá' # Fala texto"
echo "   /voice stop       # Para modo de voz"
echo ""

# Teste 5: Dependências do sistema
echo "5. Dependências do sistema:"
if command -v espeak &> /dev/null; then
    echo "   ✅ espeak instalado (TTS)"
else
    echo "   ⚠️ espeak não instalado (instale: sudo apt install espeak)"
fi

if command -v sox &> /dev/null; then
    echo "   ✅ sox instalado (gravar áudio)"
else
    echo "   ⚠️ sox não instalado (instale: sudo apt install sox)"
fi

echo ""
echo "=== TESTE COMPLETO ==="
