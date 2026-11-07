#!/usr/bin/env python3
"""Transcribe a local audio file with the installed openai-whisper library.

The Go adapter validates and converts input media before invoking this script.
Only the transcript JSON is written to stdout.
"""

import argparse
import json


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("audio_file")
    parser.add_argument("--model-path", required=True)
    parser.add_argument("--language", default="pt")
    args = parser.parse_args()

    import torch
    import whisper

    torch.set_num_threads(4)
    model = whisper.load_model(args.model_path, device="cpu")
    result = model.transcribe(
        args.audio_file,
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
