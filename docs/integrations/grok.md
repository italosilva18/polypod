# Grok no Polypod

## Pela conta: Grok Build

O CLI oficial Grok Build suporta login pela conta no navegador, inclusive fluxo
de código para ambientes sem navegador. Instale pelo procedimento oficial em
https://docs.x.ai/build/overview e execute `grok login` (ou
`grok login --device-auth`). A elegibilidade e os limites dependem da conta.
O Polypod não copia tokens, cookies ou senhas; o CLI gerencia a sessão e sua renovação.

A ferramenta `grok_delegate` envia uma tarefa ao CLI oficial com `--prompt-file`.
O arquivo temporário tem modo 0600, pertence ao usuário executor e é removido ao
terminar; o texto da tarefa não aparece nos argumentos visíveis em `ps`.
O diretório é o da sessão do Polypod. Exemplo de argumentos:

```json
{"prompt":"Analise a arquitetura deste projeto e proponha melhorias", "approve_tools":false}
```

`model` é opcional; sem ele, vale o modelo configurado no Grok. A ferramenta é
registrada como mutável, potencialmente destrutiva e não concorrente, passando
pelo mecanismo normal de permissões do Polypod. Grok executa seu próprio agente
e suas próprias ferramentas; as chamadas internas não passam individualmente
pelo orquestrador do Polypod. Autorize a tarefa delegada com esse escopo em mente.

Por padrão `--permission-mode dontAsk` nega ações sem permissão preexistente no Grok.
`approve_tools:true`
adiciona a opção oficial `--always-approve`, permitindo uma tarefa previamente
autorizada sem prompts locais. A entrada padrão fica fechada; login ou aprovações
interativas devem ser realizados no CLI oficial. Não há tentativa de login automático.
Há limite de cinco minutos, cancelamento com a sessão e saída limitada a 64 KiB.
Diagnósticos de erro não são devolvidos ao modelo, para não expor material de login.

Essa integração funciona como delegação, não como substituto do provider principal
ou tradução de tool calls. A ausência do executável produz instrução de instalação.

No serviço Linux executado como root, `POLYPOD_DESKTOP_USER=costa` faz o processo
Grok executar com UID, GID e grupos desse usuário e `HOME=/home/costa`, reutilizando
sua sessão. Defina `POLYPOD_GROK_BIN=/home/costa/.local/bin/grok` no serviço para
selecionar o executável por caminho absoluto, ou disponibilize-o no PATH. O diretório de trabalho
precisa ser acessível ao usuário. O cancelamento Linux encerra o grupo do processo.

## Pela API xAI

O provider `xai` já usa `https://api.x.ai/v1` com Chat Completions, streaming e
ferramentas. O perfil `examples/grok-api.yaml` usa somente uma referência à variável
`XAI_API_KEY`, com `XAI_MODEL` opcional (padrão `grok-4.7`). Crie a chave no console
oficial e disponibilize-a no ambiente local, sem incluí-la em mensagens ou commits.
Execute `./polypod examples/grok-api.yaml` após configurar a variável.

A conta usada no console pode ser a mesma, mas o fluxo documentado da API exige
chave e créditos. Não presuma que a assinatura do produto equivale a créditos de API.
Chat Completions é um endpoint legado ainda documentado; os recursos mais novos
podem exigir Responses, não implementado por esse perfil.

## Fontes oficiais consultadas em 2026-11-06

- Autenticação do CLI: https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/02-authentication.md
- Execução headless e ACP: https://docs.x.ai/build/cli/headless-scripting
- Configuração da API: https://docs.x.ai/developers/quickstart
- Chat Completions e modelo: https://docs.x.ai/developers/model-capabilities/legacy/chat-completions
