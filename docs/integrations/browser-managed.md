# Navegador gerenciado

`browser_manage` inicia um Chrome/Chromium sob demanda com perfil privado persistente, separado do navegador pessoal. Ele expõe `status`, `attach`, `use_desktop`, `detach`, `start`, `stop`, `tabs`, `new_tab`, `activate_tab`, `close_tab`, `navigate`, `snapshot`, `text` e `evaluate`. Para cliques e preenchimento via CDP, use `browser_observe` e `browser_act` com `tab_id` e seletor único; observe o resultado antes de repetir qualquer ação.

O backend procura `google-chrome`, `chromium`, Brave ou Edge no `PATH`. `POLYPOD_BROWSER_BINARY` escolhe um executável específico e `POLYPOD_BROWSER_PROFILE_DIR` escolhe o diretório privado. `POLYPOD_BROWSER_CDP_URL` conecta, desde a inicialização do Polypod, a um Chrome já aberto com CDP local; nesse caso, o Polypod não o encerra.

Para usar uma conta já conectada em uma janela do usuário via DOM, o navegador **precisa ter sido iniciado com uma porta de depuração CDP local**. Durante a execução, chame `browser_manage` com `{"action":"attach","endpoint":"http://127.0.0.1:9222"}`. Se o navegador foi iniciado com `--remote-debugging-port=0` e um perfil próprio, informe `profile_dir` absoluto no lugar de `endpoint`; o Polypod lê a porta atual e confirma a identidade do navegador pelo arquivo `DevToolsActivePort` desse perfil. `status` confirma `mode: attached`, `connected` e a quantidade de abas. `detach` retira essa conexão e retorna ao endpoint definido no ambiente, se houver, ou ao perfil gerenciado. `stop` nunca encerra um navegador conectado por `attach` ou pela variável de ambiente. O Polypod não copia senhas nem cookies do perfil.

Um Chrome já aberto **sem CDP** não pode receber essa conexão por CDP. Nesse caso, `{"action":"use_desktop"}` faz `browser_observe` devolver a captura e OCR da tela inteira, que pode incluir a janela aberta do navegador, sem iniciar um perfil isolado. Para clicar ou digitar, use as ferramentas `computer_*` de entrada do desktop; coordenadas de tela diferem das coordenadas DOM. `status` informa `mode: desktop`, e `detach` restaura a conexão anterior. A integração por extensão e Native Messaging vista em outros agentes exige uma extensão instalada e um protocolo próprio; ela não está presente neste módulo. `attach` só informa sucesso após obter a lista real de abas da porta local.

Com `DISPLAY` ou Wayland disponível, o navegador abre uma janela visível. O serviço root usa `POLYPOD_DESKTOP_USER` e `POLYPOD_DESKTOP_HOME` para abrir a janela como o usuário do desktop, em um perfil privado desse usuário. Se a abertura visível falhar, tenta em modo headless; `POLYPOD_BROWSER_HEADLESS=1` força esse modo. `browser_manage status` informa o modo atual.

Um login pode ser preenchido pelo agente quando o usuário autorizou a credencial. Para 2FA, peça o código ou aprovação ao usuário; para CAPTCHA, o usuário precisa resolver o desafio na janela visível. O agente observa novamente e continua. O modo headless não oferece esse handoff visual.

Testes automatizados cobrem ciclo de vida, abas, navegação e observação com Chrome isolado. O teste de abertura no serviço root e um login real com handoff ainda precisam ser realizados no desktop do usuário.

Nesta máquina COSMIC, a captura usa `grim` 1.5 ou superior, que entende o
protocolo Wayland `ext-image-copy-capture`; se ele falhar, o Polypod tenta o
capturador nativo. A captura pela sessão do usuário e o socket `ydotool`
podem ser verificados com `python3 scripts/doctor-live-service.py`. Esse comando
herda somente as variáveis gráficas declaradas na unidade systemd; um `sudo`
direto descarta essas variáveis. A verificação não envia eventos de entrada.
