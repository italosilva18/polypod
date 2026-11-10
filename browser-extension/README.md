# Polypod Browser Bridge

Esta extensão Manifest V3 conecta abas do Chrome/Chromium já aberto ao Polypod
sem iniciar o navegador com uma porta CDP. O Chrome mostra sua própria indicação
de depuração ao anexar uma aba; a extensão não altera esse comportamento.

1. Abra `chrome://extensions`, ative o modo de desenvolvedor e escolha
   **Carregar sem compactação**. Selecione esta pasta `browser-extension`.
2. No Polypod, chame `browser_manage` com `{"action":"extension_pair"}`.
3. Clique no ícone da extensão, cole `pair_url` no campo e clique **Conectar**.
4. `browser_manage` com `{"action":"status"}` deve mostrar `mode: extension`
   e `connected: true`. Use `tabs`, `browser_observe` e `browser_act` como no CDP.

A URL de pareamento dá acesso local às abas enquanto o Polypod estiver aberto;
não a compartilhe. A ponte escuta apenas em `127.0.0.1` com porta e token
aleatórios. Após reiniciar o processo Polypod, gere e cole uma nova URL.
`detach` encerra a ponte e devolve o navegador ao modo anterior. O popup também
permite desconectar. A extensão guarda a URL no armazenamento local do navegador
para tentar reconectar enquanto aquele processo Polypod continuar ativo.

O Chrome precisa ser versão 116 ou superior para manter o WebSocket no service
worker. A extensão requer as permissões `tabs` e `debugger`; o navegador pode
impedir a depuração de páginas internas, perfis ou sites sujeitos a políticas.
Não houve ensaio interativo nesta rodada, conforme pedido do usuário.

Referências oficiais: [chrome.debugger](https://developer.chrome.com/docs/extensions/reference/api/debugger),
[WebSockets em service workers](https://developer.chrome.com/docs/extensions/how-to/web-platform/websockets).
