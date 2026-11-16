#!/bin/bash
# /model - Model Switching
set -e

model="${1:-status}"

case "$model" in
    gpt-4o)
        echo "🔄 **Switching to:** GPT-4o"
        ;;
    claude-sonnet)
        echo "🔄 **Switching to:** Claude Sonnet"
        ;;
    gemini-pro)
        echo "🔄 **Switching to:** Gemini Pro"
        ;;
    status)
        echo "📊 **Current Model:** GPT-4o"
        echo ""
        echo "**Quick Switch:**"
        echo "  1. GPT-4o"
        echo "  2. Claude Sonnet"
        echo "  3. Gemini Pro"
        ;;
esac
