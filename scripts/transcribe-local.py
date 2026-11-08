#!/usr/bin/env python3
"""Transcribe a validated local PCM WAV with the installed Whisper library.

The Go adapter validates and converts input media before invoking this script.
Passing samples directly to Whisper avoids its second ffmpeg invocation.
Only the transcript JSON is written to stdout.
"""

import argparse
import json
import wave


SAMPLE_RATE = 16_000
MAX_SAMPLES = 5 * 60 * SAMPLE_RATE


def read_pcm16_mono(path: str):
    import numpy as np

    with wave.open(path, "rb") as audio:
        if (
            audio.getnchannels() != 1
            or audio.getsampwidth() != 2
            or audio.getframerate() != SAMPLE_RATE
            or audio.getcomptype() != "NONE"
        ):
            raise ValueError("expected mono PCM16 WAV at 16 kHz")
        frames = audio.getnframes()
        if frames >= MAX_SAMPLES:
            raise ValueError("audio exceeds five minutes")
        raw = audio.readframes(frames)
        if len(raw) != frames * 2:
            raise ValueError("truncated PCM WAV")

    return np.frombuffer(raw, dtype="<i2").astype(np.float32) / 32768.0


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("audio_file")
    parser.add_argument("--model-path", required=True)
    parser.add_argument("--language", default="pt")
    args = parser.parse_args()

    samples = read_pcm16_mono(args.audio_file)
    if len(samples) == 0:
        print(json.dumps({"text": "", "language": args.language}), flush=True)
        return

    import torch
    import whisper

    torch.set_num_threads(4)
    model = whisper.load_model(args.model_path, device="cpu")
    result = model.transcribe(
        samples,
        language=args.language,
        fp16=False,
        verbose=False,
    )
    print(
        json.dumps(
            {"text": str(result.get("text", "")).strip(), "language": result.get("language", "")},
            ensure_ascii=False,
        ),
        flush=True,
    )


if __name__ == "__main__":
    main()
