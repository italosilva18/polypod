"""Offline FastEmbed worker. Standard input and output use one JSON line per call."""

import json
import sys


MODEL = "sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2"


def send(payload):
    sys.stdout.write(json.dumps(payload, separators=(",", ":"), allow_nan=False) + "\n")
    sys.stdout.flush()


def main():
    if len(sys.argv) != 2:
        send({"error": "invalid worker configuration"})
        return
    try:
        from fastembed import TextEmbedding

        model = TextEmbedding(
            model_name=MODEL,
            cache_dir=sys.argv[1],
            threads=2,
            providers=["CPUExecutionProvider"],
            local_files_only=True,
        )
    except Exception:
        send({"error": "local model unavailable"})
        return

    send({"ready": True})
    for line in sys.stdin.buffer:
        try:
            request = json.loads(line)
            texts = request["texts"]
            if not isinstance(texts, list) or not texts or len(texts) > 32:
                raise ValueError("invalid batch")
            if not all(isinstance(text, str) and text.strip() for text in texts):
                raise ValueError("invalid text")
            vectors = [vector.tolist() for vector in model.embed(texts, batch_size=16)]
            send({"vectors": vectors})
        except Exception:
            # Never include the request or exception details: both can contain memory text.
            send({"error": "local embedding failed"})


if __name__ == "__main__":
    main()
