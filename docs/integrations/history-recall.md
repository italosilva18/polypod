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

Marcadores e resumos sintéticos de compactação ficam fora da busca e da
recuperação. O journal pode preservá-los para auditoria interna, mas eles não
são tratados como falas originais do usuário nem como evidência para aprender
fatos novos.

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
