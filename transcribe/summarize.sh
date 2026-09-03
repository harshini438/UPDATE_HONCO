#!/usr/bin/env bash
# Honco Meet -- turn a finished transcript into meeting notes.
#
#   summarize.sh <recording-dir>
#
# The Claude CLI is authenticated on mother, not here, so the transcript is
# piped there over SSH. That key is pinned to the summariser by a forced command
# in mother's authorized_keys -- it cannot get a shell or run anything else.
#
# Failure here is never fatal: the transcript is already on disk and is the
# thing that actually matters. Notes are a convenience on top.
set -uo pipefail

MOTHER_HOST="${MOTHER_HOST:-192.168.2.150}"
MOTHER_PORT="${MOTHER_PORT:-31013}"
MOTHER_USER="${MOTHER_USER:-database}"

DIR="${1:?usage: summarize.sh <recording-dir>}"

TXT=$(find "$DIR" -maxdepth 1 -name '*.txt' ! -name '*.notes.txt' -print -quit)
if [ -z "$TXT" ] || [ ! -s "$TXT" ]; then
    echo "no transcript in $DIR -- nothing to summarise"
    exit 0
fi

NOTES="${TXT%.txt}.notes.md"
if [ -s "$NOTES" ]; then
    echo "notes already exist: $NOTES"
    exit 0
fi

WORDS=$(wc -w < "$TXT")
echo "summarising $TXT (${WORDS} words)"

if out=$(timeout 360 ssh -o BatchMode=yes -o ConnectTimeout=15 \
            -p "$MOTHER_PORT" "$MOTHER_USER@$MOTHER_HOST" < "$TXT" 2>/dev/null) \
   && [ -n "${out// }" ] \
   && ! printf '%s' "$out" | head -1 | grep -q '^(summary failed'; then
    {
        printf '# Meeting notes\n\n'
        printf '_Recording: %s_\n' "$(basename "$DIR")"
        printf '_Generated %s from a machine transcript; check anything that matters._\n\n' "$(date '+%Y-%m-%d %H:%M')"
        printf '%s\n' "$out"
    } > "$NOTES"
    echo "notes: $NOTES"
else
    echo "WARNING: summarisation failed; transcript is intact at $TXT" >&2
    exit 1
fi
