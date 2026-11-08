#!/usr/bin/env python3
"""Transcribe a validated PCM WAV with a local faster-whisper model.

The Go adapter supplies mono PCM16 at 16 kHz. Audio is loaded as samples so
faster-whisper never needs to invoke ffmpeg. Stdout contains transcript JSON.
"""

import argparse
import json
from pathlib import Path
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

    model_path = Path(args.model_path).expanduser()
    if not model_path.is_dir():
        parser.error("--model-path must be a local CTranslate2 model directory")

    samples = read_pcm16_mono(args.audio_file)
    if len(samples) == 0:
        print(json.dumps({"text": "", "language": args.language}), flush=True)
        return

    from faster_whisper import WhisperModel

    model = WhisperModel(
        str(model_path),
        device="cpu",
        compute_type="int8",
        cpu_threads=4,
        local_files_only=True,
    )
    segments, info = model.transcribe(samples, language=args.language, beam_size=5)
    # faster-whisper is lazy: iterating the segments runs the inference.
    transcript = "".join(segment.text for segment in segments).strip()
    print(
        json.dumps({"text": transcript, "language": info.language}, ensure_ascii=False),
        flush=True,
    )


if __name__ == "__main__":
    main()
