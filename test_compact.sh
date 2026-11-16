#!/bin/bash

# Teste do comando /compact no PolyPod

echo "=========================================="
echo "Teste: Comando /compact"
echo "=========================================="
echo ""

cd "/home/costa/Área de trabalho/CLI/polypod"

# Teste 1: Verificar se o binário foi compilado
echo "1. Verificando binário..."
if [ -f "./polypod" ]; then
    echo "   ✅ Binário compilado com sucesso"
    ls -lh ./polypod
else
    echo "   ❌ Binário não encontrado"
    exit 1
fi
echo ""

# Teste 2: Verificar versão
echo "2. Versão do PolyPod:"
./polypod --version 2>&1 || echo "   (versão não disponível)"
echo ""

# Teste 3: Verificar doctor
echo "3. Doctor:"
./polypod doctor 2>&1 | head -20
echo ""

# Teste 4: Listar commands (se disponível)
echo "4. Tentando listar commands..."
./polypod --help 2>&1 | grep -E "command|skill|compact" || echo "   (help não mostrou commands)"
echo ""

echo "=========================================="
echo "Próximos passos:"
echo "1. Inicie o PolyPod: ./polypod"
echo "2. Dentro da interface: /compact"
echo "=========================================="
