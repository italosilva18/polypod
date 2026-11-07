# Memória episódica automática

O `EpisodeService` consolida pedidos de trabalho do usuário quando há uma
resposta do assistente na mesma sessão. Ele usa o journal persistente de
conversas e mantém um cursor próprio em `<data.dir>/episode-state.json`.
O estado também guarda o último pedido ainda sem resposta de cada sessão,
permitindo associar mensagens separadas por lotes ou reinícios.
Cada rodada processa até 20 lotes de 50 mensagens por padrão; o restante
continua na próxima rodada, sem ocupar o processo por tempo indefinido.

Cada episódio guarda um trecho curto do pedido, os IDs exatos das duas
mensagens como proveniência e o status **"o assistente respondeu; execução e
resultado não foram confirmados"**. O texto da resposta nunca é copiado para
a memória. A fonte é `dream_episode`, com confiança `0.2`, prioridade normal
e um identificador estável por par de mensagens. Reprocessar um lote após
falha reutiliza o identificador e não cria outra memória.

O serviço descarta pedidos triviais, mensagens de teste curtas, links,
blocos de código, possíveis códigos e segredos, além dos canais sintéticos
`scheduler`, `heartbeat`, `inbox`, `task_queue`, `system` e `tool`. O filtro é
conservador: pedidos que mencionam autenticação, credenciais, email ou
telefone também ficam de fora. O arquivo de estado é gravado com permissão
`0600`.

O `DreamService` continua responsável por extrair fatos duráveis, apoiados
explicitamente pelo usuário. Episódios descrevem histórico de conversa, não
fatos pessoais nem resultados verificados. Resultados de ações precisam de
um registro separado com evidência da ferramenta ou do serviço que executou
a ação.

Episódios só são recuperados automaticamente na mesma sessão (`canal:usuário`)
em que o pedido foi feito. Consultas de memória sem esse escopo, inclusive as
skills genéricas de listar e buscar memórias, não mostram episódios. A busca
semântica também filtra os episódios de outras sessões antes de enviar textos
ao embedder; os fatos gerais continuam disponíveis como antes.
