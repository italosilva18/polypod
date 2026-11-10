#!/usr/bin/env python3
"""Transcribe a validated PCM WAV with a local faster-whisper model.

The Go adapter supplies mono PCM16 at 16 kHz. Audio is loaded as samples so
faster-whisper never needs to invoke ffmpeg. Stdout contains transcript JSON.

--pcm-stream keeps one model loaded for a dictation. Stdin is a sequence of
little-endian uint32 lengths followed by that many PCM16 bytes. A zero length
ends the recording. Stdout is one JSON object per line: ready, partial text
for the speech heard so far, then one final line for the whole buffer. Partial
lines are previews. The final line uses the same beam size as file mode.
"""

import argparse
import json
import sys
import threading
from pathlib import Path
import wave


SAMPLE_RATE = 16_000
MAX_SAMPLES = 5 * 60 * SAMPLE_RATE
MAX_PCM_BYTES = MAX_SAMPLES * 2
# Re-decode the unstable tail often enough to follow speech, and commit audio
# that has scrolled out of that tail so a long dictation does not reprocess
# its whole history on every update.
PARTIAL_HOP_BYTES = SAMPLE_RATE * 2 * 2
PARTIAL_WINDOW_BYTES = SAMPLE_RATE * 2 * 8
MAX_FRAME_BYTES = 256 * 1024
SPEECH_ABS = 800
SPEECH_PERCENT = 2


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


def emit(payload: dict) -> None:
    print(json.dumps(payload, ensure_ascii=False), flush=True)


def load_model(model_path: Path):
    from faster_whisper import WhisperModel

    return WhisperModel(
        str(model_path),
        device="cpu",
        compute_type="int8",
        cpu_threads=4,
        local_files_only=True,
    )


def transcribe_samples(model, samples, language: str, *, partial: bool) -> str:
    kwargs = {"language": language, "beam_size": 1 if partial else 5}
    if partial:
        # Independent windows must not invent continuity from an earlier guess.
        kwargs["condition_on_previous_text"] = False
        kwargs["without_timestamps"] = True
    segments, _info = model.transcribe(samples, **kwargs)
    return "".join(segment.text for segment in segments).strip()


def samples_from_pcm(pcm: bytes):
    import numpy as np

    if len(pcm) < 2:
        return np.zeros(0, dtype=np.float32)
    if len(pcm) % 2:
        pcm = pcm[:-1]
    return np.frombuffer(pcm, dtype="<i2").astype(np.float32) / 32768.0


def has_speech(pcm: bytes) -> bool:
    import numpy as np

    if len(pcm) < SAMPLE_RATE * 2:
        return False
    if len(pcm) % 2:
        pcm = pcm[:-1]
    samples = np.frombuffer(pcm, dtype="<i2").astype(np.int32)
    if len(samples) == 0:
        return False
    loud = int(np.count_nonzero(np.abs(samples) >= SPEECH_ABS))
    return loud * 100 >= len(samples) * SPEECH_PERCENT


def read_exact(stream, n: int) -> bytes:
    buf = bytearray()
    while len(buf) < n:
        piece = stream.read(n - len(buf))
        if not piece:
            raise EOFError
        buf.extend(piece)
    return bytes(buf)


def read_frame(stream) -> bytes:
    header = read_exact(stream, 4)
    size = int.from_bytes(header, "little")
    if size == 0:
        return b""
    if size > MAX_FRAME_BYTES or size % 2:
        raise ValueError("invalid pcm frame")
    return read_exact(stream, size)


def partial_plan(total: int, committed: int, last_live: int, done: bool) -> str:
    """Choose the next resident-worker step for `total` buffered PCM bytes."""
    if done:
        return "final"
    if total < 0 or committed < 0 or last_live < 0 or committed > total:
        return "wait"
    if total - committed > PARTIAL_WINDOW_BYTES:
        return "commit"
    if (
        total - last_live >= PARTIAL_HOP_BYTES
        and total - committed >= PARTIAL_HOP_BYTES
    ):
        return "live"
    return "wait"


def join_partial(parts: list[str], live: str) -> str:
    items = [part for part in parts if part]
    if live:
        items.append(live)
    return " ".join(items).strip()


def run_stream(model_path: Path, language: str) -> int:
    model = load_model(model_path)
    lock = threading.Lock()
    pcm = bytearray()
    finished = False
    read_error = ""
    ready = threading.Event()

    def reader() -> None:
        nonlocal finished, read_error
        try:
            while True:
                frame = read_frame(sys.stdin.buffer)
                if frame == b"":
                    break
                with lock:
                    if len(pcm) + len(frame) > MAX_PCM_BYTES:
                        read_error = "audio exceeds five minutes"
                        break
                    pcm.extend(frame)
                ready.set()
        except EOFError:
            # Only an explicit zero-length frame closes a valid stream.
            # A broken writer can leave a useful-looking prefix in `pcm`;
            # treating EOF as final would silently transcribe that prefix.
            with lock:
                read_error = "pcm stream interrupted"
        except ValueError:
            with lock:
                read_error = "invalid pcm frame"
        except Exception:
            with lock:
                read_error = "pcm stream interrupted"
        finally:
            with lock:
                finished = True
            ready.set()

    threading.Thread(target=reader, name="pcm-reader", daemon=True).start()
    emit({"type": "ready"})

    committed = 0
    last_live = 0
    parts: list[str] = []
    emitted = ""
    while True:
        with lock:
            err = read_error
            done = finished
            total = len(pcm)
            action = "error" if err else partial_plan(total, committed, last_live, done)
            # Copy only the bytes this step will decode. The reader shares this
            # lock, so a full-buffer copy on every preview would stall intake.
            if action == "commit":
                audio = bytes(pcm[committed : committed + PARTIAL_WINDOW_BYTES])
            elif action == "live":
                start = committed
                if total - start > PARTIAL_WINDOW_BYTES:
                    start = total - PARTIAL_WINDOW_BYTES
                audio = bytes(pcm[start:total])
            elif action == "final":
                audio = bytes(pcm)
            else:
                audio = b""
        if err:
            emit({"type": "error", "error": err})
            return 1
        if action == "wait":
            ready.wait(timeout=0.2)
            ready.clear()
            continue
        if action == "commit":
            committed += PARTIAL_WINDOW_BYTES
            text = ""
            if has_speech(audio):
                try:
                    text = transcribe_samples(
                        model, samples_from_pcm(audio), language, partial=True
                    )
                except Exception:
                    text = ""
            if text:
                parts.append(text)
            partial = join_partial(parts, "")
            if partial and partial != emitted:
                emitted = partial
                emit({"type": "partial", "text": partial})
            continue
        if action == "live":
            text = ""
            if has_speech(audio):
                try:
                    text = transcribe_samples(
                        model, samples_from_pcm(audio), language, partial=True
                    )
                except Exception:
                    text = ""
            last_live = total
            partial = join_partial(parts, text)
            if partial and partial != emitted:
                emitted = partial
                emit({"type": "partial", "text": partial})
            continue
        if len(audio) > MAX_PCM_BYTES:
            emit({"type": "error", "error": "audio exceeds five minutes"})
            return 1
        try:
            text = (
                transcribe_samples(model, samples_from_pcm(audio), language, partial=False)
                if audio
                else ""
            )
        except Exception:
            emit({"type": "error", "error": "transcription failed"})
            return 1
        emit({"type": "final", "text": text})
        return 0


def run_file(audio_file: str, model_path: Path, language: str) -> None:
    samples = read_pcm16_mono(audio_file)
    if len(samples) == 0:
        emit({"text": "", "language": language})
        return

    model = load_model(model_path)
    segments, info = model.transcribe(samples, language=language, beam_size=5)
    # faster-whisper is lazy: iterating the segments runs the inference.
    transcript = "".join(segment.text for segment in segments).strip()
    emit({"text": transcript, "language": info.language})


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("audio_file", nargs="?")
    parser.add_argument("--pcm-stream", action="store_true")
    parser.add_argument("--model-path", required=True)
    parser.add_argument("--language", default="pt")
    args = parser.parse_args()

    model_path = Path(args.model_path).expanduser()
    if not model_path.is_dir():
        parser.error("--model-path must be a local CTranslate2 model directory")

    if args.pcm_stream:
        if args.audio_file:
            parser.error("--pcm-stream does not take an audio file")
        sys.exit(run_stream(model_path, args.language))

    if not args.audio_file:
        parser.error("audio_file is required")
    run_file(args.audio_file, model_path, args.language)


if __name__ == "__main__":
    main()
