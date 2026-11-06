---
name: polyclaw_github
description: Use o gh CLI para consultar e operar issues, PRs e workflows do GitHub quando essa integração for necessária.
---

# GitHub

Adaptação da skill GitHub do PolyClaw para o Polypod. O executável `gh` deve estar disponível e autenticado. Descubra o repositório com `git remote get-url origin` ou forneça `--repo owner/repo` explicitamente. Para estado de código local, use as ferramentas Git do Polypod.

- Consultar PR: `gh pr view 55 --repo owner/repo --json title,state,headRefName`.
- Verificar CI: `gh pr checks 55 --repo owner/repo`.
- Consultar issue: `gh issue view 12 --repo owner/repo --json title,state,body`.
- API avançada: `gh api repos/owner/repo/pulls/55`.

Leia o resultado antes de escrever ou publicar. Não inclua tokens, credenciais ou conversas em comandos, comentários ou commits. Consulte o catálogo original em `third_party/polyclaw/skills/github/` e a licença MIT em `third_party/polyclaw/LICENSE`.
