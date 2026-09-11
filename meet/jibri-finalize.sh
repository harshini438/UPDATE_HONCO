#!/bin/bash
# Honco Chat -- Jibri completion hook.
#
# Jibri runs this inside its container with the finished recording's
# directory as $1 once a FILE recording stops (FileRecordingJibriService;
# it is NOT run when zero media was captured -- that case surfaces as a
# meeting whose card never gains a recording). The directory holds the
# media file and a metadata.json whose meeting_url ends in the room name,
# which is how the recording is matched back to the /meet that created it.
#
# This file is installed by setup-jibri.sh as /config/finalize.sh and is
# the version-controlled source of that hook. It carries NO secret: the
# callback secret is read from /config/honco-callback.secret, a separate
# 0600 file that setup-jibri.sh writes and that never enters git or a
# backup archive readable by anyone but the operator.
set -u
RECORDING_DIR="$1"
RECORDING_ID="$(basename "$RECORDING_DIR")"
LOG="/storage/finalize.log"
log() { echo "$(date -Is) $*" >> "$LOG"; }
log "finalize start: dir=$RECORDING_DIR id=$RECORDING_ID"

# --- the secret, from a file, never from this script -----------------------
SECRET_FILE="/config/honco-callback.secret"
if [ ! -r "$SECRET_FILE" ]; then
    log "finalize: $SECRET_FILE is missing or unreadable -- cannot authenticate to Honco Chat; recording left at $RECORDING_DIR"
    exit 0
fi
HONCO_CALLBACK_SECRET="$(head -1 "$SECRET_FILE" | tr -d '\r\n')"

# --- where Honco Chat is, from inside this container -----------------------
# Nothing here is a fixed address. host.docker.internal is what Docker
# Desktop provides; the default-route gateway is the docker bridge on a
# native Linux host. An operator can pin one with /config/honco-chat.host
# (host:port, one line) and it is tried first.
CANDIDATES=""
if [ -r /config/honco-chat.host ]; then
    CANDIDATES="$(head -1 /config/honco-chat.host | tr -d '\r\n')"
fi
GW="$(ip route 2>/dev/null | awk '/default/ {print $3; exit}')"
CANDIDATES="$CANDIDATES host.docker.internal:8065 ${GW:+$GW:8065}"

# --- what we have --------------------------------------------------------
ROOM_NAME=""
if [ -f "$RECORDING_DIR/metadata.json" ]; then
    ROOM_NAME=$(grep -o '"meeting_url"[[:space:]]*:[[:space:]]*"[^"]*"' "$RECORDING_DIR/metadata.json" | sed -E 's#.*/([^/"]+)".*#\1#')
fi
log "finalize: room=$ROOM_NAME"

MEDIA_FILE=$(find "$RECORDING_DIR" -maxdepth 1 -type f ! -name 'metadata.json' ! -name '*.log' | head -1)
if [ -z "$MEDIA_FILE" ]; then
    log "finalize: no media file in $RECORDING_DIR, reporting failed"
    STATUS="failed"
else
    log "finalize: media file $MEDIA_FILE ($(stat -c%s "$MEDIA_FILE") bytes)"
    STATUS="ready"
fi

# --- deliver to Honco Chat -------------------------------------------------
# Field order matters: the receiver streams the body, so room_name and
# status must arrive before the file part. The secret travels only in the
# header, to the first host that answers; it is never written to this log.
MM_PATH="/plugins/com.honco.workspace/api/v1/recordings/complete"
delivered=0
for HOST in $CANDIDATES; do
    URL="http://$HOST$MM_PATH"
    if [ "$STATUS" = "ready" ]; then
        CODE=$(curl -4 -s -o /tmp/finalize_mm.txt -w '%{http_code}' --max-time 600 \
            -H "X-Jibri-Callback-Secret: $HONCO_CALLBACK_SECRET" \
            -F "room_name=$ROOM_NAME" -F "status=ready" -F "file=@${MEDIA_FILE}" "$URL") || CODE="000"
    else
        CODE=$(curl -4 -s -o /tmp/finalize_mm.txt -w '%{http_code}' --max-time 30 \
            -H "X-Jibri-Callback-Secret: $HONCO_CALLBACK_SECRET" \
            -F "room_name=$ROOM_NAME" -F "status=failed" \
            -F "error_message=Jibri produced no media for this session" "$URL") || CODE="000"
    fi
    log "finalize[honcochat]: $HOST -> status=$CODE body=$(head -c 200 /tmp/finalize_mm.txt 2>/dev/null)"
    case "$CODE" in
        200) delivered=1; break ;;
        000) continue ;;               # unreachable: try the next candidate
        *)   break ;;                  # reached, refused: another host will not change that
    esac
done

if [ "$delivered" = "1" ]; then
    log "finalize done: id=$RECORDING_ID delivered to Honco Chat"
else
    log "finalize: FAILED to deliver -- recording left at $RECORDING_DIR for manual recovery"
fi
exit 0
