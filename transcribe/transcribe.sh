#!/usr/bin/env bash
# Honco Meet -- transcribe one finished recording.
#
#   transcribe.sh <recording-dir>
#
# Jibri writes an .mp4 plus a metadata.json per recording. This extracts the
# audio and runs the configured speech-to-text engine. Nothing leaves the LAN.
#
# Engine is chosen by STT_ENGINE in engine.conf:
#   whisper  whisper.cpp + large-v3-turbo on the GPU  (default today)
#   vixy     the in-house Vixy model via faster-whisper
#
# Both write <name>.txt and <name>.vtt, so everything downstream -- the worker,
# the summariser -- is unaware of which one ran.
set -euo pipefail

ROOT="$HOME/honco-transcribe"

# engine.conf is the persistent default, but an explicit STT_ENGINE in the
# environment must win -- otherwise sourcing the file silently clobbers a
# deliberate override and you spend a while wondering why the other engine
# never runs.
_STT_ENGINE_ENV="${STT_ENGINE:-}"
# shellcheck source=/dev/null
[ -f "$ROOT/engine.conf" ] && . "$ROOT/engine.conf"
[ -n "$_STT_ENGINE_ENV" ] && STT_ENGINE="$_STT_ENGINE_ENV"
STT_ENGINE="${STT_ENGINE:-whisper}"

BIN="$ROOT/whisper.cpp/build/bin/whisper-cli"
MODEL="$ROOT/models/${WHISPER_MODEL:-ggml-large-v3-turbo.bin}"
# Indian English and Hindi both appear in these meetings; "auto" lets whisper
# decide per recording rather than forcing one and mangling the other.
LANG="${WHISPER_LANG:-auto}"
THREADS="${WHISPER_THREADS:-8}"

DIR="${1:?usage: transcribe.sh <recording-dir>}"
[ -d "$DIR" ] || { echo "not a directory: $DIR" >&2; exit 1; }

VIDEO=$(find "$DIR" -maxdepth 1 -name '*.mp4' -print -quit)
[ -n "$VIDEO" ] || { echo "no .mp4 in $DIR" >&2; exit 1; }

BASE="${VIDEO%.mp4}"
WAV="$BASE.wav"
OUT="$BASE"

echo "recording: $VIDEO"
echo "engine:    $STT_ENGINE"

# Both engines want 16 kHz mono PCM; anything else is resampled internally at a
# quality cost, so do it properly here.
echo "extracting audio"
ffmpeg -nostdin -loglevel error -y -i "$VIDEO" -vn -ar 16000 -ac 1 -c:a pcm_s16le "$WAV"

DUR=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$WAV" 2>/dev/null || echo 0)
printf 'audio: %.0f seconds\n' "${DUR:-0}"

echo "transcribing"
START=$(date +%s)

case "$STT_ENGINE" in
vixy)
    PY="${VIXY_PYTHON:-$ROOT/vixy-venv/bin/python3}"
    [ -x "$PY" ] || { echo "vixy venv missing at $PY" >&2; exit 1; }
    "$PY" "$ROOT/transcribe-vixy.py" "$WAV" "$OUT" 2>&1 | tail -3
    ;;
whisper)
    # Whisper guesses proper nouns from sound alone and gets company-specific
    # ones wrong every time ("Honco" came back as "Honko"). An initial prompt
    # biases the decoder toward the vocabulary these meetings actually use.
    PROMPT_FILE="$ROOT/vocabulary.txt"
    PROMPT_ARG=()
    if [ -s "$PROMPT_FILE" ]; then
        PROMPT_ARG=(--prompt "$(tr '\n' ' ' < "$PROMPT_FILE")")
    fi
    "$BIN" -m "$MODEL" -f "$WAV" \
        -l "$LANG" -t "$THREADS" \
        "${PROMPT_ARG[@]}" \
        --output-txt --output-vtt --output-file "$OUT" \
        -np 2>&1 | tail -3
    ;;
*)
    echo "unknown STT_ENGINE '$STT_ENGINE' (expected: whisper, vixy)" >&2
    exit 1
    ;;
esac

ELAPSED=$(( $(date +%s) - START ))
echo "transcribed in ${ELAPSED}s"

# The wav is a large intermediate; the mp4 and the transcript are what matter.
rm -f "$WAV"

if [ -s "$OUT.txt" ]; then
    WORDS=$(wc -w < "$OUT.txt")
    echo "transcript: $OUT.txt (${WORDS} words)"
else
    echo "WARNING: transcript is empty -- was there any speech?" >&2
fi
