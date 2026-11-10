# Concorrência dos snapshots de sessão

O arquivo `<id>.json` continua sendo o ponto de publicação do snapshot. Cada
`Load` devolve uma `Revision` calculada dos bytes desse arquivo, inclusive no
formato legado. `Save` exige essa revisão para substituir uma sessão existente;
uma sessão nova deve usar revisão vazia. A gravação atualiza a revisão no
objeto após publicar o manifesto. Se outro processo salvou antes, `Save`
devolve `ErrSessionConflict` e não altera o arquivo nem poda páginas.

`Save` e `Delete` usam um lock do diretório compartilhado entre processos. A
verificação da revisão, a publicação do manifesto e a limpeza de páginas
ocorrem sob o mesmo lock. `DeleteAtRevision` protege a limpeza de sessões
antigas contra uma gravação feita depois da leitura. A espera pelo lock é de
até 30 segundos. Em sistemas Unix, o lock usa o inode do diretório; no Windows,
usa `.sessions.lock` dentro dele.

`LoadPageAtRevision` recebe a revisão obtida em `Load`, `LoadHeader` ou outra
página. Se o manifesto mudou, retorna `ErrSessionConflict` em vez de misturar
páginas de versões diferentes. `LoadPage` continua disponível sem fixação para
consultas que aceitam a versão mais recente. O CLI fixa a revisão da sessão
ativa para salvar, retomar, trocar de branch e carregar páginas antigas com
PgUp. Uma revisão diferente pede nova retomada em vez de misturar páginas.

O lock impede sobreposição de gravações, mas não mescla duas conversas que
foram editadas ao mesmo tempo. O processo que receber o conflito deve recarregar
e reconciliar as mensagens ou preservar seu trabalho em outra sessão. No
autosave da CLI, um conflito salva a conversa local sob novo ID, mostra esse ID
no terminal e o registra no índice de sessões. O snapshot de recuperação usa
somente o contexto local conhecido pela CLI, sem copiar metadados da versão
publicada por outro processo. O rollback de uma branch só
remove a revisão da sessão filha que acabou de criar.

Esta garantia cobre o manifesto e as páginas do armazenamento `internal/session`.
O `conversation.Store` compartilhado de `cli:local` e o índice de branches ainda
não têm revisão condicional. Duas CLIs simultâneas podem divergir nesses stores;
o conflito do manifesto impede sobrescrita silenciosa no snapshot, mas não
reconcilia automaticamente os outros estados.
