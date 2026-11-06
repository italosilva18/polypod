---
name: polyclaw_skill_creator
description: Crie ou atualize uma skill SKILL.md do Polypod com instruções curtas e recursos reutilizáveis.
---

# Criar skills no Polypod

Adaptação da skill Skill Creator do PolyClaw. Coloque uma skill local em `.polypod/skills/<nome>/SKILL.md`. Use frontmatter YAML com `name` e `description`; o corpo deve explicar quando usar, passos, requisitos e caminhos de recursos relativos. Prefira exemplos curtos e arquivos `references/` para conteúdo grande.

Verifique se a skill foi carregada pelo `FSSkillLoader` e está registrada como ferramenta no fluxo de permissões. Não coloque senhas no texto da skill; use o cofre de segredos do Polypod. Se depender de um binário externo, declare-o e teste sua presença antes de anunciar a capacidade como pronta. Fonte: `third_party/polyclaw/skills/skill-creator/`; licença MIT em `third_party/polyclaw/LICENSE`.
