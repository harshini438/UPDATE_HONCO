#!/usr/bin/env bash
# Honco Meet -- watch for finished recordings and transcribe them.
#
# Jibri's finalize hook runs INSIDE the jibri container, which has no whisper and
# no GPU access. So the hook does the only thing it usefully can: drop a marker
# file into the shared storage volume. This worker runs on the host, sees the
# marker, and does the real work.
#
# Run under systemd (honco-transcribe.service), not by hand.
set -uo pipefail

STORAGE="${STORAGE:-$HOME/.jitsi-meet-cfg/storage/jibri}"
ROOT="$HOME/honco-transcribe"
INTERVAL="${INTERVAL:-30}"
STATE="$ROOT/state"

mkdir -p "$STATE"

log() { printf '%s %s\n' "$(date -Is)" "$*"; }

log "watching $STORAGE every ${INTERVAL}s"

while true; do
    # Markers are written by the container as <recording-dir-name>.ready
    shopt -s nullglob
    for marker in "$STORAGE"/*.ready; do
        name=$(basename "$marker" .ready)
        dir="$STORAGE/$name"
        done_flag="$STATE/$name.done"

        [ -f "$done_flag" ] && { rm -f "$marker"; continue; }
        [ -d "$dir" ] || { log "marker with no directory: $name"; rm -f "$marker"; continue; }

        # A recording still being flushed to disk would transcribe short. Wait
        # until the mp4 stops growing before touching it.
        vid=$(find "$dir" -maxdepth 1 -name '*.mp4' -print -quit)
        if [ -z "$vid" ]; then
            log "no mp4 yet in $name, will retry"
            continue
        fi
        s1=$(stat -c%s "$vid"); sleep 5; s2=$(stat -c%s "$vid")
        if [ "$s1" != "$s2" ]; then
            log "$name still being written, will retry"
            continue
        fi

        log "processing $name"
        if "$ROOT/transcribe.sh" "$dir" >>"$ROOT/logs/$name.log" 2>&1; then
            touch "$done_flag"
            rm -f "$marker"
            log "transcribed $name"

            # Meeting notes. Never fatal -- the transcript is the thing that
            # matters and is already on disk.
            if [ -x "$ROOT/summarize.sh" ]; then
                if "$ROOT/summarize.sh" "$dir" >>"$ROOT/logs/$name.log" 2>&1; then
                    log "summarised $name"
                else
                    log "summary failed for $name (transcript intact)"
                fi
            fi

            # Optional hand-off into Honco Chat, only if configured.
            if [ -x "$ROOT/post-to-chat.sh" ] && [ -s "$ROOT/chat.env" ]; then
                "$ROOT/post-to-chat.sh" "$dir" >>"$ROOT/logs/$name.log" 2>&1 \
                    || log "post-to-chat failed for $name"
            fi
            log "done $name"
        else
            log "FAILED $name -- see $ROOT/logs/$name.log (leaving marker for retry)"
        fi
    done
    shopt -u nullglob
    sleep "$INTERVAL"
done
