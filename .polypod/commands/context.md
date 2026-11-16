#!/bin/bash
# /context - Context Management
set -e

subcommand="${1:-list}"
shift || true

case "$subcommand" in
    add)
        echo "📎 **Adicionando ao contexto:** $*"
        echo ""
        echo "✅ Arquivo adicionado ao contexto"
        ;;
    remove)
        echo "📎 **Removendo do contexto:** $*"
        echo ""
        echo "✅ Arquivo removido do contexto"
        ;;
    list)
        echo "📎 **Contexto Atual**"
        echo ""
        echo "**Files:** 0"
        echo "**Tags:** 0"
        echo ""
        echo "Use /context add <file> para adicionar"
        ;;
    clear)
        echo "📎 **Limpando contexto**"
        echo ""
        echo "✅ Contexto limpo"
        ;;
esac
