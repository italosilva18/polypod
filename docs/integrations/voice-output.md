# Voz enviada pelo WhatsApp

O Polypod usa [Piper](https://github.com/OHF-Voice/piper1-gpl/blob/main/docs/CLI.md) para sintetizar fala em português no computador e `ffmpeg` com `libopus` para criar OGG/Opus. A ferramenta `whatsapp_send_voice` coloca o arquivo na fila persistente do WhatsApp configurado como `owner_chat`; a entrega ocorre pelo transporte nativo. O modelo e o ambiente Python ficam fora do Git.

Nesta máquina, Piper 1.8.0 está em `/home/costa/.local/share/polypod/tts-venv` e a voz [pt_BR-cadu-medium](https://huggingface.co/rhasspy/piper-voices/blob/main/pt/pt_BR/cadu/medium/MODEL_CARD) está em `/home/costa/.local/share/polypod/tts-models`. O SHA-256 do `.onnx` instalado é `765f0809a6ea9035d4a6d0d008dbf8876e68b2dd32029312672fa8f405bdb535`. O código Piper usa GPL-3.0; ele é uma dependência externa instalada no diretório privado, sem código ou pesos copiados para o repositório do Polypod. Confira a licença do modelo no seu `MODEL_CARD` ao trocar de voz.

Para instalar em outra máquina:

```bash
python3 -m venv /home/costa/.local/share/polypod/tts-venv
/home/costa/.local/share/polypod/tts-venv/bin/pip install piper-tts
/home/costa/.local/share/polypod/tts-venv/bin/python -m piper.download_voices \
  --data-dir /home/costa/.local/share/polypod/tts-models pt_BR-cadu-medium
```

Configure no `config.yaml` privado:

```yaml
whatsapp:
  voice_output:
    enabled: true
    python_binary: /home/costa/.local/share/polypod/tts-venv/bin/python
    model_path: /home/costa/.local/share/polypod/tts-models/pt_BR-cadu-medium.onnx
    ffmpeg_binary: /home/costa/.local/bin/ffmpeg
    timeout_seconds: 120
```

O sintetizador aceita até 1200 caracteres por mensagem, executa uma conversão por vez e remove WAV e texto temporários. O `doctor` verifica arquivos e executáveis sem sintetizar nem enviar áudio. Um teste local gerou OGG/Opus decodificável em cerca de dois segundos; o envio e a reprodução no telefone ainda exigem verificação ao vivo.
