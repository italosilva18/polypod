# Recuperação do histórico original

O Polypod guarda cada mensagem no journal durável antes de substituir o
histórico ativo por um resumo. As ferramentas `history_search` e
`history_recall` permitem ao agente consultar esse texto original depois da
compactação:

- `history_search({"query":"termo","limit":10})` retorna mensagens recentes
  que contêm o termo, com `message_id`, `sequence` e uma prévia. Uma página
  examina no máximo 2.000 registros; use `before_sequence` com o valor
  `next_before` para continuar em mensagens anteriores.
- `history_recall({"message_id":"...","start":0,"max_chars":6000})` retorna
  o trecho original exato. Use `next_start` para ler partes seguintes.

Marcadores e resumos sintéticos de compactação ficam fora da busca. Um resumo
salvo pode ser lido por `history_recall` quando o prompt cita seu ID; ele
continua sem ser tratado como fala original do usuário nem como evidência para
aprender fatos novos.

## Janela de contexto grande

Antes de chamar o modelo, o runtime limita o histórico e a mensagem atual ao
orçamento da janela. Conversas curtas seguem intactas. Se um turno ou resumo
for grande, o prompt recebe o começo, o fim e o `message_id` para ler o texto
integral em partes com `history_recall`. As mensagens recentes entram primeiro;
um resumo de compactação salvo recebe espaço reservado. Turnos anteriores que
não couberem permanecem no journal e o prompt avisa quantos foram omitidos.

O limite age somente no pedido enviado ao modelo. A sessão e o journal não são
truncados. Se um texto longo não tiver ID salvo ou não houver escopo de diário,
o pedido falha com erro explícito. Isso evita passar uma versão incompleta como
se fosse a mensagem inteira. A compactação persistida continua funcionando
separadamente e pode reduzir o histórico em turnos seguintes. Quando o próprio
pedido de compactação é grande, ele passa por lotes de resumo limitados pelo
contexto configurado. Mensagens individuais enormes levam ao resumo uma prévia
com seu ID, e o resumo final registra esses IDs para recuperar detalhes. Cada
ID é verificado no escopo do agente e branch atual antes de aparecer como
recuperável; uma troca de agente que torne o texto inacessível produz erro
explícito se a janela não puder carregá-lo integralmente.

O canal, usuário, branch da CLI e agente são definidos pelo runtime, não por
argumentos das ferramentas. Um ID de outra conversa aparece como não
encontrado. Mensagens antigas que ainda não trazem metadados de branch/agente
só podem ser recuperadas no escopo não ramificado do agente padrão. Na CLI,
branches com ID próprio não recebem esse histórico legado ambíguo.

Uma branch criada após esta implementação herda o histórico original da
branch pai até o último registro presente no momento do fork. Turnos que o
pai receber depois do fork não ficam visíveis na filha. A regra também vale
para ancestrais em forks sucessivos. Branches antigas, criadas antes do
registro de ancestralidade, continuam sem esse vínculo retroativo.

Um subagente iniciado pelo agente principal no mesmo turno herda esse escopo
como acesso delegado à conversa de origem. Ele não pode pedir um canal,
usuário ou branch diferente pelos argumentos da ferramenta.

O índice SQLite por sessão evita percorrer conversas alheias durante a busca.
O journal original continua sendo a fonte de verdade; os resultados são
limitados para não ocupar todo o contexto do modelo.
