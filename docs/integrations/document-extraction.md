# Extração local de documentos

O agente padrão dispõe de `document_extract`. A ferramenta lê arquivos de texto,
PDF, Office e imagens locais e devolve o texto ao agente. Para PDF, usa
`pdfinfo` e `pdftotext`; se o PDF não tiver texto selecionável suficiente, usa
`pdftoppm` e `tesseract`. Para DOC/DOCX/ODT, planilhas e apresentações, converte
primeiro com LibreOffice em uma pasta temporária privada. Imagens usam
`tesseract` diretamente. Nenhuma chamada é feita a um serviço externo pela
ferramenta de extração.

Exemplo de argumentos:

```json
{"path":"/caminho/relatorio.pdf","start":0,"max_chars":50000}
```

Quando a resposta indicar `Continue com start=N`, repita a chamada com esse
valor para ler o próximo trecho. `ocr=true` força OCR de PDF. A variável
`POLYPOD_OCR_LANG=por+eng` escolhe português e inglês quando ambos os pacotes
de idioma estiverem instalados; sem ela, Tesseract usa o idioma padrão.

Limites operacionais: arquivo de entrada até 25 MiB, texto extraído até 4 MiB,
100 páginas de PDF com texto selecionável, 20 páginas com OCR e 2 minutos por
chamada. A resposta padrão contém 50 mil caracteres Unicode, com máximo de
100 mil por trecho. A ferramenta informa quantas páginas processou quando o
documento ultrapassa o limite. Programas ausentes geram erro claro.

O PolyClaw também oferece uma ação `index` em sua própria base de conhecimento.
O Polypod ainda não possui um índice documental correspondente; esta ferramenta
faz extração e leitura paginada, sem gravar o documento como memória pessoal.
