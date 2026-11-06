# Transcrição de áudio do WhatsApp

O Polypod aceita mensagens de voz pelo mesmo inbox durável usado para texto. Para transcrevê-las, habilite explicitamente um endpoint compatível com `POST /audio/transcriptions` no `config.yaml` privado:

```yaml
whatsapp:
  audio_transcription:
    enabled: true
    endpoint: https://api.groq.com/openai/v1/audio/transcriptions
    model: whisper-large-v3-turbo
    api_key: ${POLYPOD_ASR_API_KEY}
```

Guarde `POLYPOD_ASR_API_KEY` no ambiente privado do serviço. A assinatura do ChatGPT ou o login do Grok CLI não fornecem automaticamente uma credencial para essa API. Com `enabled: false` ou sem configuração, o Polypod informa ao agente que chegou áudio, sem inventar uma transcrição. Um endpoint HTTP só é aceito em loopback; endpoint remoto exige HTTPS e chave. O corpo da resposta do provedor não é gravado no erro.

Antes do envio, o anexo deve ter até 16 MiB e formato reconhecido por MIME e assinatura: OGG/Opus, WAV, MP3, FLAC, WebM ou MP4/M4A. O texto transcrito é marcado como conteúdo não confiável no turno do agente. Falha temporária do provedor segue as tentativas do inbox; áudio inválido recebe uma nota e não bloqueia o turno.

Esta integração foi testada com servidor HTTP falso local. A transcrição real depende de credencial, conectividade e um teste de voz no WhatsApp após habilitar a opção.
