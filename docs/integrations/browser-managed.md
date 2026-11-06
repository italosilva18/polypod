# Navegador gerenciado

`browser_manage` inicia um Chrome/Chromium sob demanda com perfil privado persistente, separado do navegador pessoal. Ele expõe `status`, `start`, `stop`, `tabs`, `new_tab`, `activate_tab`, `close_tab`, `navigate`, `snapshot`, `text` e `evaluate`. Para cliques e preenchimento, use `browser_observe` e `browser_act` com `tab_id` e seletor único; observe o resultado antes de repetir qualquer ação.

O backend procura `google-chrome`, `chromium`, Brave ou Edge no `PATH`. `POLYPOD_BROWSER_BINARY` escolhe um executável específico e `POLYPOD_BROWSER_PROFILE_DIR` escolhe o diretório privado. `POLYPOD_BROWSER_CDP_URL` mantém o modo de conexão com um Chrome já aberto; nesse caso, o Polypod não o encerra.

Com `DISPLAY` ou Wayland disponível, o navegador abre uma janela visível. O serviço root usa `POLYPOD_DESKTOP_USER` e `POLYPOD_DESKTOP_HOME` para abrir a janela como o usuário do desktop, em um perfil privado desse usuário. Se a abertura visível falhar, tenta em modo headless; `POLYPOD_BROWSER_HEADLESS=1` força esse modo. `browser_manage status` informa o modo atual.

Um login pode ser preenchido pelo agente quando o usuário autorizou a credencial. Para 2FA, peça o código ou aprovação ao usuário; para CAPTCHA, o usuário precisa resolver o desafio na janela visível. O agente observa novamente e continua. O modo headless não oferece esse handoff visual.

Testes automatizados cobrem ciclo de vida, abas, navegação e observação com Chrome isolado. O teste de abertura no serviço root e um login real com handoff ainda precisam ser realizados no desktop do usuário.
