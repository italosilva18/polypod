#!/bin/bash

# /read - Read File
# Usage: /read <path>

set -e

path="${1:-.}"

cat << 'EOF'
📖 **Read File Tool**

Lê conteúdo de arquivos.

**Uso:**
  /read ./file.txt         # Lê arquivo
  /read ./src/main.go      # Lê código

**Features:**
  - Detect encoding
  - Detect line endings
  - File size
  - Syntax highlighting

⏳ **Status:** Implementação em andamento...
EOF
