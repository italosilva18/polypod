#!/bin/bash

# /review - Code Review
# Usage: /review [path|file]

set -e

path="${1:-.}"

cat << 'EOF'
🔍 **Review - Code Review**

Revisa código em busca de issues e melhorias.

**Uso:**
  /review                     # Revisa diretório atual
  /review ./src               # Revisa src/
  /review ./file.go           # Revisa arquivo

**Tipos de review:**
  - code          - Revisão de código
  - security      - Revisão de segurança
  - performance   - Revisão de performance
  - style         - Revisão de estilo

**O que será verificado:**
  - Code smells
  - Security vulnerabilities
  - Performance issues
  - Style violations
  - Best practices

⏳ **Status:** Implementação em andamento...

**Próximos passos:**
  - Análise estática
  - Verificação de segurança
  - Sugestões de melhoria
  - Auto-fix (opcional)
EOF
