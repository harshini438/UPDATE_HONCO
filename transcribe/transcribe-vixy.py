#!/usr/bin/env python3
"""Honco Meet -- transcribe with the in-house Vixy STT model.

The alternative backend to whisper.cpp. Vixy is a CTranslate2 (faster-whisper
format) model fine-tuned in-house; unlike stock Whisper it returns Hindi as
romanised Hinglish, which is what these calls are actually written in.

    transcribe-vixy.py <audio.wav> <output-basename>

Writes <basename>.txt and <basename>.vtt so the output is interchangeable with
the whisper.cpp path -- the worker and the summariser do not care which engine
produced them.
"""
import os
import sys
import time

MODEL = os.environ.get("VIXY_MODEL", os.path.expanduser("~/honco-transcribe/vixy-stt-model"))
DEVICE = os.environ.get("VIXY_DEVICE", "auto")
COMPUTE = os.environ.get("VIXY_COMPUTE", "int8")
LANG = os.environ.get("VIXY_LANG", "hi")
THREADS = int(os.environ.get("VIXY_THREADS", "8"))


def ts(seconds):
    """WebVTT timestamp."""
    ms = int(round(seconds * 1000))
    h, ms = divmod(ms, 3600000)
    m, ms = divmod(ms, 60000)
    s, ms = divmod(ms, 1000)
    return "%02d:%02d:%02d.%03d" % (h, m, s, ms)


def main():
    if len(sys.argv) != 3:
        sys.exit("usage: transcribe-vixy.py <audio.wav> <output-basename>")
    audio, base = sys.argv[1], sys.argv[2]

    if not os.path.isdir(MODEL):
        sys.exit("vixy model not found at %s -- set VIXY_MODEL" % MODEL)

    from faster_whisper import WhisperModel

    device = DEVICE
    if device == "auto":
        # CUDA if ctranslate2 was built for it and a GPU is visible; the CPU
        # path is correct either way, just slower.
        try:
            import ctranslate2
            device = "cuda" if ctranslate2.get_cuda_device_count() > 0 else "cpu"
        except Exception:
            device = "cpu"

    t0 = time.time()
    model = WhisperModel(MODEL, device=device, compute_type=COMPUTE,
                         cpu_threads=THREADS)
    segments, info = model.transcribe(
        audio,
        language=LANG,
        beam_size=1,
        vad_filter=True,                    # meeting audio has long silences
        condition_on_previous_text=False,   # stops one bad segment poisoning the rest
        no_repeat_ngram_size=3,
    )

    segs = list(segments)
    elapsed = time.time() - t0
    dur = max(getattr(info, "duration", 0.0), 0.1)

    with open(base + ".txt", "w", encoding="utf-8") as f:
        for s in segs:
            line = s.text.strip()
            if line:
                f.write(line + "\n")

    with open(base + ".vtt", "w", encoding="utf-8") as f:
        f.write("WEBVTT\n\n")
        for s in segs:
            line = s.text.strip()
            if line:
                f.write("%s --> %s\n%s\n\n" % (ts(s.start), ts(s.end), line))

    print("vixy: device=%s audio=%.0fs stt=%.1fs rtf=%.2f segments=%d"
          % (device, dur, elapsed, elapsed / dur, len(segs)))


if __name__ == "__main__":
    main()
