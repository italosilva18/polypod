# Saída de mídia pelo WhatsApp

O gateway nativo aceita texto e agora pode enfileirar voz OGG/Opus, imagem e documento para um chat permitido. A skill `whatsapp_send_media` recebe o caminho de um arquivo local e uma legenda opcional; `Gateway.QueueMedia` aceita bytes produzidos por outra ferramenta, como TTS. A chamada somente enfileira: o processador durável faz o upload e o envio depois.

O arquivo de origem é copiado para um blob privado junto à caixa de entrada, com permissões `0700` no diretório e `0600` no arquivo. A fila guarda tipo, MIME, nome, tamanho e SHA-256; o envio confere novamente bytes e metadados. O blob permanece disponível após reinício e nas tentativas de entrega. Quando uma mensagem sai do histórico retido, seu blob é removido. O caminho original não aparece na mensagem do agente nem precisa continuar existindo.

Formatos aceitos:

| Tipo | Formatos |
| --- | --- |
| Voz | OGG/Opus, enviado como mensagem de voz (`PTT`) sem legenda |
| Imagem | PNG, JPEG, GIF e WebP |
| Documento | PDF, DOCX, XLSX, PPTX, TXT e CSV |

Cada anexo tem limite de 16 MiB. Imagens têm limite de 25 megapixels. O código verifica assinatura ou estrutura básica antes de guardar; anexos desconhecidos são recusados. Imagens e documentos podem levar legenda de até 4.000 caracteres. A skill envia somente ao `owner_chat` configurado e permitido; `Gateway.QueueMedia` exige que o chat esteja na allowlist.

O transporte usa `whatsmeow.Client.Upload` e o tipo de mídia correspondente antes de `SendMessage`. A tentativa reutiliza o ID estável da fila. Isso permite retry após falha de conexão, mas uma confirmação perdida entre a aceitação remota e a gravação local continua sendo uma situação ambígua: antes de repetir uma saída incerta, confira se ela chegou. Testes locais cobrem persistência, integridade, reinício, retry, autorização e montagem dos três tipos de mensagem. **Ainda não houve envio de anexo real para o telefone nesta revisão.**
