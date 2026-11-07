# Transcrição de áudio do WhatsApp

O Polypod recebe mensagens de voz pela mesma caixa de entrada durável usada para texto. O backend `local` usa a biblioteca Python `openai-whisper` e um modelo no próprio computador. A conta ChatGPT usada pelo agente não é necessária para transcrever e não fornece automaticamente uma chave para a API de áudio.

## Backend local

Instale `openai-whisper` em um ambiente Python privado do Polypod, disponibilize `ffmpeg` e baixe um checkpoint Whisper. Na instalação desta máquina, o ambiente está em `/home/costa/.local/share/polypod/asr-venv`, o modelo `base.pt` em `/home/costa/.local/share/polypod/models/base.pt` e o worker em `scripts/transcribe-local.py`. O ambiente e o modelo são dados locais, fora do Git; o Polypod não depende dos arquivos do PolyClaw em tempo de execução.

Exemplo para o `config.yaml` privado:

```yaml
whatsapp:
  audio_transcription:
    enabled: true
    backend: local
    python_binary: /home/costa/.local/share/polypod/asr-venv/bin/python
    script_path: /home/costa/Área de trabalho/CLI/polypod/scripts/transcribe-local.py
    model_path: /home/costa/.local/share/polypod/models/base.pt
    ffmpeg_binary: /home/costa/.local/bin/ffmpeg
    language: pt
    timeout_seconds: 900
```

O adaptador valida MIME e assinatura do arquivo, limita a entrada a 16 MiB e a duração a menos de cinco minutos, converte para WAV mono de 16 kHz, executa um processo Whisper por vez e remove os arquivos temporários. Áudio e transcrição não são enviados a um serviço de ASR remoto. O processamento desta instalação usa CPU; uma gravação real em inglês de 11 segundos da [fixture pública do Whisper](https://github.com/openai/whisper/blob/main/tests/jfk.flac), convertida para OGG/Opus como uma mensagem de voz, foi reconhecida pelo teste de integração em cerca de cinco segundos. Isso não mede latência de mensagens reais do WhatsApp.

O `doctor` confere os arquivos e executáveis sem abrir o modelo; a prova de fala reconhecida exige um teste de transcrição. Se o checkpoint ou ambiente for movido, atualize os caminhos privados e reinicie o serviço. Para instalar novamente sem PolyClaw, consulte as [instruções oficiais do Whisper](https://github.com/openai/whisper#setup).

## Backend HTTP opcional

Um endpoint compatível com `POST /audio/transcriptions` continua disponível:

```yaml
whatsapp:
  audio_transcription:
    enabled: true
    backend: http
    endpoint: https://api.groq.com/openai/v1/audio/transcriptions
    model: whisper-large-v3-turbo
    api_key: ${POLYPOD_ASR_API_KEY}
```

Guarde a chave no ambiente privado do serviço. Endpoint HTTP só é aceito em loopback; endpoint remoto exige HTTPS e chave. Quando `backend` é omitido, a configuração HTTP antiga continua válida. Com `enabled: false`, o Polypod informa ao agente que chegou áudio sem inventar uma transcrição.

O texto transcrito é marcado como conteúdo não confiável no turno do agente. Falha temporária segue as tentativas da caixa de entrada; áudio inválido recebe uma nota e não bloqueia o turno. O recebimento e a resposta a uma mensagem de voz real no WhatsApp ainda precisam ser confirmados após a implantação.
