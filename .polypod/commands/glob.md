#!/bin/bash

# /glob - Find Files
# Usage: /glob <pattern>

set -e

pattern="${1:-*}"

cat << 'EOF'
🔍 **Glob Tool**

Encontra arquivos por pattern.

**Uso:**
  /glob "*.go"             # Arquivos Go
  /glob "src/**/*.ts"      # TypeScript em src/

**Features:**
  - Glob patterns
  - Recursive matching
  - Count matches

⏳ **Status:** Implementação em andamento...
EOF
