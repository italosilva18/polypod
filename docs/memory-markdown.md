# Memória Markdown editável

O Polypod consulta notas Markdown do diretório de trabalho antes de responder. Crie
`.polypod/MEMORY.md` para um resumo curto ou arquivos `.md` em
`.polypod/memory/` para assuntos específicos. Subpastas também funcionam.

As notas são lidas novamente a cada turno. Assim, uma edição ou exclusão no
disco aparece na próxima resposta sem reiniciar o serviço. O agente recebe
apenas trechos relevantes, acompanhados do caminho e da linha de origem.

O contexto tem limite de quatro arquivos e 3.000 caracteres Unicode; cada
arquivo tem limite de 64 KiB e a leitura total por turno é limitada a 1 MiB.
Notas Markdown são dados para consulta. Instruções do projeto continuam nos
arquivos de instrução apropriados.

O acesso às notas locais acompanha a autenticação dos documentos locais:
CLI local, rotina do proprietário ou conversa WhatsApp autenticada do
proprietário. Participantes remotos comuns não recebem notas do diretório
de trabalho do serviço.

Fatos salvos no banco de memória continuam separados e pesquisáveis pelo
sistema de memória atual. O Polypod não copia automaticamente fatos privados
para arquivos do projeto.

As ferramentas `recall_memory`, `list_memories` e `review_memories` recebem o
escopo da conversa autenticada. `delete_memory` só esquece fatos visíveis
nesse escopo; a revisão feita pelo agente não mostra fatos automáticos antigos
sem proprietário.

`save_memory` no CLI continua criando uma memória explícita global. Em canais
remotos, a ferramenta grava o tópico dentro da conversa autenticada. Dois chats
podem usar o mesmo nome de tópico sem sobrescrever um ao outro. Apenas o CLI,
as rotinas do proprietário e o chat WhatsApp do proprietário autenticado pelo
gateway podem consultar fatos explícitos globais já existentes. Em chats
remotos, `delete_memory` atua apenas sobre os próprios tópicos; a exclusão
de um fato global é feita pelo CLI local.

## Escopo dos fatos aprendidos

Fatos novos extraídos automaticamente das conversas ficam vinculados à
conversa de origem. No CLI, o vínculo usa a branch ativa; no WhatsApp, usa
o chat. Uma evidência que mistura conversas ou branches diferentes não gera
um fato novo. Resumos sintéticos de compactação também não servem como fala
original nem como evidência de aprendizado. Fatos salvos explicitamente pelo usuário têm precedência sobre
extrações automáticas com o mesmo tópico.

Depois de um período ocioso, a consolidação percorre até quatro janelas
pendentes de 50 mensagens por ciclo. Isso reduz o atraso após uma longa
interrupção sem disparar uma quantidade ilimitada de chamadas ao modelo.
Se uma conversa voltar a ficar ativa, o ciclo encerra antes da próxima janela.

Por enquanto, fatos aprendidos no CLI não atravessam automaticamente para
o WhatsApp, nem no sentido inverso. Fatos explícitos salvos em um chat remoto
também não migram para outro canal sem um vínculo de identidade confiável do
proprietário. Fatos explícitos antigos sem escopo continuam disponíveis ao
proprietário por compatibilidade. Fatos antigos
extraídos automaticamente sem escopo, inclusive os antigos registros de edições de arquivos, permanecem no banco para auditoria offline, mas
não são injetados em novas conversas até uma revisão explícita. O Polypod não
tenta atribuir retroativamente um proprietário a eles.
