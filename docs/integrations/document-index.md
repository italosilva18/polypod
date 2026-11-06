# Índice persistente de documentos

`internal/document.Index` guarda trechos extraídos em um arquivo SQLite privado.
O índice é separado das conversas e da memória de fatos. Ele usa FTS5 para
busca textual e aceita um `memory.Embedder` configurado explicitamente para
busca semântica. Falha do provedor de embeddings conserva a busca textual.

## Origem e escopo

- `IndexFile` extrai um arquivo local com os limites de `document.Extract` e o
  registra no escopo `local`. O identificador da fonte é seu caminho canônico.
- `IndexText` recebe texto já extraído, um escopo exato e um identificador
  estável. Para anexos de mensagens, o escopo deve ser a conversa autorizada;
  o identificador da fonte não deve conter telefone nem credencial.
- `Search` exige uma lista não vazia de escopos. O chamador pode incluir
  `local` e a conversa atual; outro chat não entra implicitamente na consulta.
- `DeleteSource` remove uma fonte e todos os trechos dela. Atualizar uma fonte
  substitui os trechos anteriores em uma transação.

Cada resultado traz o hash da revisão, o ordinal do trecho e uma citação
`[doc:…#N]` calculada sem expor o escopo. `ReadChunk` recupera o trecho pelo
escopo, identificador da fonte e ordinal. A citação muda quando a fonte muda.
Trechos têm até 1.200 caracteres Unicode com sobreposição de 120; textos
extraídos têm limite de 4 MiB, e arquivos de entrada têm limite de 25 MiB.

## Embeddings

`memory.NewHTTPEmbedder` usa um endpoint `/embeddings` compatível com o formato
de requisição OpenAI. Precisa de URL e modelo configurados; nenhuma conta ou
chave é descoberta automaticamente. Chaves só são enviadas por HTTPS ou HTTP
local. O identificador do endpoint/modelo fica nos vetores para impedir que
uma troca de modelo misture espaços vetoriais. Para a memória de fatos,
`ManagedStore.SetEmbedder` liga a busca híbrida; sem provedor, a busca lexical
continua disponível.

O índice não rastreia pastas automaticamente. É preciso chamar a ferramenta
de indexação ou integrar um anexo autorizado ao receber a mensagem. Testes
locais verificam persistência, atualização, exclusão, isolamento por chat,
permissões do arquivo, busca semântica e degradação lexical.
