# Transcrição de áudio do WhatsApp e do CLI

O Polypod recebe mensagens de voz pela mesma caixa de entrada durável usada para texto. O backend `local` executa um worker de reconhecimento no próprio computador. Esta instalação usa `faster-whisper` com modelo multilíngue `small` em CPU INT8; o worker `openai-whisper` anterior continua disponível como alternativa configurável. A conta ChatGPT usada pelo agente não é necessária para transcrever e não fornece automaticamente uma chave para a API de áudio. A [API pública do DeepSeek](https://api-docs.deepseek.com/api/list-models/) também não lista entrada de áudio.

## Backend local

Instale `faster-whisper` em um ambiente Python privado do Polypod, disponibilize `ffmpeg` para a conversão inicial e mantenha um modelo CTranslate2 local. Na instalação desta máquina, o ambiente está em `/home/costa/.local/share/polypod/asr-faster-venv` (`faster-whisper` 1.2.1), o modelo oficial `Systran/faster-whisper-small` na revisão `536b0662742c02347bc0e980a01041f333bce120` está em `/home/costa/.local/share/polypod/asr-faster-models/small`, e o worker está em `scripts/transcribe-faster.py`. O worker anterior em `scripts/transcribe-local.py`, o ambiente `asr-venv` e o checkpoint `models/base.pt` permanecem disponíveis para retorno manual à configuração anterior. Esses dados ficam fora do Git; o Polypod não depende dos arquivos do PolyClaw em tempo de execução.

Exemplo para o `config.yaml` privado:

```yaml
whatsapp:
  audio_transcription:
    enabled: true
    backend: local
    python_binary: /home/costa/.local/share/polypod/asr-faster-venv/bin/python
    script_path: /home/costa/Área de trabalho/CLI/polypod/scripts/transcribe-faster.py
    model_path: /home/costa/.local/share/polypod/asr-faster-models/small
    ffmpeg_binary: /home/costa/.local/bin/ffmpeg
    language: pt
    timeout_seconds: 900
```

O adaptador valida MIME e assinatura do arquivo, limita a entrada a 16 MiB e a duração a menos de cinco minutos, converte para WAV mono PCM16 de 16 kHz, executa um processo de reconhecimento por vez e remove os arquivos temporários. Ambos os workers leem o WAV com `wave` e `numpy` e entregam as amostras diretamente ao modelo; assim, não iniciam um segundo `ffmpeg` nem dependem do `PATH` para decodificar o áudio. O `ffmpeg_binary` configurado continua necessário para a conversão inicial feita pelo Go. Áudio e transcrição não são enviados a um serviço de ASR remoto. O processamento desta instalação usa CPU. Em dois áudios reais preservados, o modelo `small` gerou texto mais coerente em um caso e desempenho semelhante ao `base` no outro; sem uma transcrição de referência, isso não prova fidelidade à fala.

O `doctor` confere os arquivos e executáveis sem abrir o modelo; a prova de fala reconhecida exige uma transcrição real. `model_path` aceita um checkpoint `.pt` não vazio ou diretório CTranslate2 com `model.bin` e `config.json`. Se o modelo ou ambiente for movido, atualize os caminhos privados e reinicie o serviço. Consulte as instruções oficiais de [faster-whisper](https://github.com/SYSTRAN/faster-whisper#installation) e [Whisper](https://github.com/openai/whisper#setup).

## Ditado no CLI

Com `whatsapp.audio_transcription.enabled: true`, o CLI reutiliza o mesmo backend de transcrição. Pressione Ctrl+G ou digite `/voice` para iniciar; pressione Ctrl+G novamente ou use `/voice stop` para encerrar. `/voice N` preserva a captura por duração fixa, de 1 a 120 segundos. O comando grava pelo `arecord` no dispositivo ALSA `default`; `POLYPOD_VOICE_INPUT_DEVICE` escolhe outro dispositivo, como `pulse` ou `pipewire`. A gravação e a transcrição ocorrem em segundo plano. Ao terminar, o texto é inserido no campo de mensagem para revisão e envio com Enter. O arquivo WAV temporário é removido após a transcrição. Uma configuração `backend: local` mantém o áudio na máquina. O texto parcial durante a fala ainda não é exibido.

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

Guarde a chave no ambiente privado do serviço. HTTP é aceito apenas em loopback; endpoint remoto exige HTTPS e chave. Quando `backend` é omitido, a configuração HTTP antiga continua válida. Com `enabled: false`, o Polypod informa ao agente que chegou áudio sem inventar uma transcrição.

O texto transcrito é marcado como conteúdo não confiável no turno do agente. Falha temporária segue as tentativas da caixa de entrada; áudio inválido recebe uma nota e não bloqueia o turno. O recebimento e a resposta a uma mensagem de voz real no WhatsApp ainda precisam ser confirmados após a implantação.
