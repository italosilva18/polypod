#!/bin/bash
# /pdf - PDF Support
set -e

file="${1:-}"

if [ -z "$file" ]; then
    echo "📄 **PDF Reader**"
    echo ""
    echo "Uso: /pdf <file.pdf>"
else
    echo "📄 **Reading PDF:** $file"
    echo ""
    echo "⏳ Extracting content..."
    echo ""
    echo "**Metadata:**"
    echo "  - Title: placeholder"
    echo "  - Author: placeholder"
fi
