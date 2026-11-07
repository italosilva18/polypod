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

## Extração estruturada

Com `POLYPOD_DOCUMENT_PYTHON` apontando para um Python que contém
`openpyxl`, `python-docx` e `python-pptx`, `document_extract` e `IndexFile`
preservam a estrutura de `.xlsx`, `.docx` e `.pptx`. O worker Python está
embutido no binário. Nesta máquina, o ambiente isolado fica em
`/home/costa/.local/share/polypod/document-venv` e a variável está no `.env`
privado. O ambiente e suas bibliotecas não entram no Git. Para reinstalar:

```sh
python3 -m venv /home/costa/.local/share/polypod/document-venv
/home/costa/.local/share/polypod/document-venv/bin/python -m pip install \
  openpyxl==3.1.5 python-docx==1.2.0 python-pptx==1.0.2
```

Planilhas mostram abas, coordenadas, fórmulas e valores armazenados
separadamente; **fórmulas não são executadas**. DOCX conserva a ordem de
parágrafos e tabelas; PPTX conserva slides, tabelas e notas. O worker limita
abas, células, slides, texto e tempo. Pacotes Office com mais de 5.000
entradas ZIP ou 128 MiB expandidos são rejeitados antes de abrir o parser.
Sem o Python configurado, a extração antiga via LibreOffice → PDF permanece
disponível para Office.

PDFs passam a receber marcador de página e OCR por página quando não há texto
selecionável suficiente. `document_extract` aceita `page_start` e `page_end`
para ler trechos posteriores sem processar sempre o início. Cada chamada
processa até 100 páginas, ou 20 quando OCR é forçado. OCR automático também
tem limite de 20 páginas digitalizadas por chamada; o resultado marca páginas
omitidas pelo limite. A ferramenta `image_analyze`, quando registrada com um
provedor de visão, descreve imagens locais; `document_extract` mantém o OCR
local para extrair somente texto delas.

## Embeddings

Nesta instalação, `embedding.backend: local` usa o modelo multilíngue
FastEmbed em CPU. O worker e o cache de vetores persistentes também atendem à
memória de fatos; consulte [busca semântica local](semantic-memory.md) para
configuração, versão do modelo e limites. O runtime não depende de Ollama nem
de uma chave externa para essa busca.

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
