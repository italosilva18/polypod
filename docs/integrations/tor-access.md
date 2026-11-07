# Acesso a sites .onion

O Polypod oferece `tor_fetch` ao agente padrão e ao pesquisador `task`. A ferramenta aceita apenas URLs HTTP(S) cujo domínio termina em `.onion`, envia o nome do destino ao proxy SOCKS5 do Tor e nunca tenta uma conexão direta caso o proxy falhe. Redirecionamentos para fora de `.onion` são recusados.

O serviço Tor deve expor SOCKS5 em `127.0.0.1:9050`. Para outra porta local, configure `POLYPOD_TOR_SOCKS_ADDR` como IP de loopback e porta, por exemplo `127.0.0.1:9150`, antes de iniciar o Polypod. Um endereço de proxy remoto é recusado. A ferramenta ignora as variáveis genéricas `HTTP_PROXY` e `HTTPS_PROXY`.

Exemplo de chamada:

```json
{"url":"http://<endereco-v3>.onion/","start":0,"max_chars":12000}
```

`start` e `max_chars` permitem ler trechos sucessivos de páginas longas. O corpo recebido é limitado a 512 KiB e apenas conteúdo textual é retornado. Cada chamada tem prazo de 90 segundos. A ferramenta não oferece navegação com JavaScript, cookies persistentes, login interativo ou anonimato equivalente ao Tor Browser. Para essas tarefas, use o Tor Browser oficial e o controle do computador; não abra endereços `.onion` em um navegador comum.

Referências: [especificação dos endereços .onion](https://spec.torproject.org/address-spec), [SOCKS5 no Tor](https://spec.torproject.org/socks-extensions.html) e [documentação do proxy no Go](https://pkg.go.dev/golang.org/x/net/proxy).
