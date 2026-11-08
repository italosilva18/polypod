# RTK no comando `bash`

O Polypod pode usar o [RTK](https://www.rtk-ai.app/docs/getting-started/supported-agents/)
para reduzir a saída de comandos antes de enviá-la ao modelo. A integração é
opcional e exige o executável `rtk` instalado separadamente. Ela só se aplica a
comandos simples de leitura reconhecidos pelo RTK; comandos com encadeamento,
redirecionamento ou execução pelo sandbox seguem pelo caminho original. Não
reduz os tokens do pedido do usuário nem da resposta do modelo.

Defina no ambiente do processo:

```bash
POLYPOD_RTK_ENABLED=1
POLYPOD_RTK_BIN=/caminho/absoluto/para/rtk
```

`POLYPOD_RTK_BIN` deve apontar para um executável de confiança. No serviço
`root`, use uma cópia pertencente a `root` fora de diretórios graváveis por
outros usuários. O instalador local prepara `/usr/local/libexec/polypod-rtk` e
configura esse caminho na unidade systemd. No lançador da CLI do usuário, o
RTK instalado em `~/.local/bin/rtk` é usado com os privilégios desse usuário.

O Polypod verifica permissões, classifica e registra o comando original. Depois
da autorização, `rtk rewrite` decide se há uma versão compactada. Os códigos
de saída 0 e 3 indicam uma reescrita; 1 mantém o original. A integração também
mantém o original quando o RTK retorna 2, pois a decisão de permissão cabe ao
Polypod. Se o RTK não estiver disponível, o comando original continua
funcionando. Um comando executado não é repetido se falhar.

Quando o agente precisar de saída exata, ele pode usar `raw_output: true` na
ferramenta `bash`. Também é possível desativar a integração para um processo
com `POLYPOD_RTK_ENABLED=0`. A integração define `RTK_RECALL=0` por padrão para
não guardar uma cópia integral da saída em outro banco; o histórico e a
auditoria próprios do Polypod continuam conforme a configuração local. Nesse
modo, obter depois uma parte omitida exige repetir o comando com `raw_output`.

Para conferir a economia estimada pelo RTK, execute `rtk gain` no terminal.
Essa medição se refere à saída dos comandos filtrados, não à conta inteira do
modelo. Consulte a [explicação oficial da economia](https://www.rtk-ai.app/docs/resources/savings-explained/)
e a [configuração de recall](https://www.rtk-ai.app/docs/getting-started/configuration/).
