# Busca semântica local

O Polypod aceita `embedding.backend: local` para consultar memórias e o índice
de documentos com embeddings gerados no CPU. O worker usa
[FastEmbed](https://qdrant.github.io/fastembed/) 0.9.1 e o modelo
[`sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2`](https://qdrant.github.io/fastembed/examples/Supported_Models/):
384 dimensões, português entre cerca de 50 idiomas, pesos ONNX de cerca de
0,22 GB e licença Apache 2.0. Esse modelo não exige prefixos diferentes para
perguntas e documentos. O worker limita o ONNX Runtime a duas threads de CPU.

## Instalação

O ambiente Python e os pesos ficam fora do repositório. Baixe o modelo uma vez
durante a instalação:

```sh
python3 -m venv ~/.local/share/polypod/embedding-venv
~/.local/share/polypod/embedding-venv/bin/python -m pip install fastembed==0.9.1
~/.local/share/polypod/embedding-venv/bin/python -c 'from pathlib import Path; from fastembed import TextEmbedding; cache = str(Path.home() / ".local/share/polypod/embedding-models"); TextEmbedding(model_name="sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2", cache_dir=cache, threads=2, providers=["CPUExecutionProvider"])'
```

Configure `config.yaml`:

```yaml
embedding:
  enabled: true
  backend: local
  python_binary: ~/.local/share/polypod/embedding-venv/bin/python
  cache_dir: ~/.local/share/polypod/embedding-models
  timeout_seconds: 120
```

`python_binary` e `cache_dir` aceitam caminhos absolutos ou `~/`. O
construtor exige o snapshot ativo em `refs/main` antes da partida e identifica
o modelo pela revisão e pelo conteúdo dos pesos, tokenizer e arquivos de
configuração; mudar qualquer um desses componentes separa os vetores antigos
do índice. Snapshots anteriores no cache não impedem a inicialização. No runtime,
`local_files_only=True` e `HF_HUB_OFFLINE=1` impedem downloads. Nenhuma
chave de API nem Ollama é necessária. O texto da memória passa somente por
stdin/stdout privados do processo Python; erros e logs não incluem esse texto.

## Comportamento e limites

- O processo Python permanece carregado entre chamadas e para quando o
  Polypod encerra. Se falhar ou expirar o tempo, a busca lexical continua
  disponível; a próxima chamada tenta iniciar outro worker.
- O cache persistente em `<data.dir>/embedding-cache.db` guarda hashes do texto
  e vetores, separado pela identidade do modelo, sem guardar o texto original.
  Consultas repetidas reutilizam os vetores em vez de recalcular toda a memória.
  O arquivo e seu diretório recebem permissões privadas.
- O wrapper aceita até 100.000 textos por chamada e repassa faltas ao worker
  em lotes de 32; cada texto tem até 128.000 bytes. O modelo trunca cada
  entrada após 128 tokens; documentos longos são divididos em trechos pelo
  índice antes de gerar embeddings.
- As respostas devem ter 384 dimensões, valores finitos e magnitude positiva.
  Respostas incompletas ou inválidas são descartadas sem retornar resultados
  parciais.
- Isso melhora a recuperação por sentido; não cria fatos novos nem substitui
  a consolidação automática da memória.

Para testar a instalação real sem rede:

```sh
POLYPOD_TEST_LOCAL_EMBEDDING_PYTHON=~/.local/share/polypod/embedding-venv/bin/python \
POLYPOD_TEST_LOCAL_EMBEDDING_CACHE=~/.local/share/polypod/embedding-models \
go test ./internal/memory -run '^TestLocalEmbedder' -v
```

Os testes cobrem uma paráfrase em português contra um tema sem relação,
ordenação dos vetores, reutilização do worker, cancelamento e fallback lexical.
Sem as duas variáveis de teste, o teste do modelo real é ignorado.
