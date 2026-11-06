---
name: polyclaw_browser
description: Navegue em páginas e execute ações verificadas no Chrome gerenciado pelo Polypod.
---

# Navegador

Adaptação da skill `agent-browser` do PolyClaw para as ferramentas nativas do Polypod. Use `browser_manage` com `status`, `start`, `tabs`, `new_tab`, `activate_tab`, `close_tab` e `navigate` para controlar abas. O Chrome gerenciado usa perfil privado persistente; um endpoint CDP explícito conecta outro navegador.

Para interagir, observe a aba com `browser_observe` e use o `tab_id` e o seletor único retornados em `browser_act` (`click` ou `fill`). Peça `expected_text` ou `expected_url` quando possível. Observe novamente após ações e não repita um clique ou envio de resultado desconhecido antes de verificar a página. Se o DOM não permitir uma ação, use `computer_screenshot` e as ferramentas de desktop.

Em logins, preencha as credenciais autorizadas. Quando aparecer 2FA, solicite o código ou aprovação ao usuário; CAPTCHA deve ser resolvido pelo usuário na janela visível. Depois observe de novo e continue. A referência original está em `third_party/polyclaw/skills/agent-browser/`, sob licença MIT em `third_party/polyclaw/LICENSE`.
