#!/usr/bin/env bash
# Honco Meet -- summarise a meeting transcript.
#
# Reads a transcript on stdin, writes meeting notes on stdout.
#
# This runs on mother because that is where the Claude CLI is authenticated.
# ubuntu-3 reaches it over SSH with a key that is PINNED TO THIS SCRIPT via a
# forced command in authorized_keys -- that key cannot get a shell, forward a
# port, or run anything else. It is not a general login to this box.
set -uo pipefail

# A forced-command SSH session gets a minimal PATH with no ~/.local/bin, so the
# CLI must be addressed absolutely. HOME is set explicitly for the same reason:
# the CLI reads its credentials from there.
export HOME=/home/database
CLAUDE=/home/database/.local/bin/claude

# claude -p's context window is ~200K tokens (~4 chars/token => ~800K chars).
# 600K leaves headroom for the prompt and the model's own output, and covers
# every meeting we've actually seen (a dense 4-hour meeting runs ~150K
# chars). The old 120K limit was sized for a much smaller context window and
# was truncating meetings well under 2 hours long.
MAX_CHARS="${MAX_CHARS:-600000}"
LOG=/home/database/honco-summarize.log

transcript=$(cat)

if [ -z "${transcript// }" ]; then
    echo "(empty transcript -- nothing to summarise)"
    exit 0
fi

# A long meeting can still exceed even that. Truncate rather than fail, and
# say so in the output so nobody mistakes a partial summary for a complete
# one. Keep both ends rather than just the head: decisions and action items
# are disproportionately likely to land in the last few minutes, and a
# head-only truncation was silently dropping every one of them.
truncated=""
if [ "${#transcript}" -gt "$MAX_CHARS" ]; then
    HEAD=$(( MAX_CHARS * 2 / 3 ))
    TAIL=$(( MAX_CHARS - HEAD ))
    transcript="${transcript:0:$HEAD}"$'\n\n[... middle of the meeting omitted for length ...]\n\n'"${transcript: -$TAIL}"
    truncated=$'\n\n_(Transcript was truncated for length; this covers the start and end of the meeting, with the middle omitted.)_'
fi

printf '%s summarise: %d chars\n' "$(date -Is)" "${#transcript}" >> "$LOG"

PROMPT='You are writing meeting notes for an internal engineering and sales team at Honco (products: HomeLead, JustSell, GemSetu).

The transcript below is machine-generated from meeting audio, so expect garbled proper nouns, missing punctuation, and occasional wrong words. Read through those errors rather than quoting them.

Write, in this order and nothing else:

## Summary
Two or three sentences on what the meeting was actually about.

## Decisions
Bullet list. Only things that were actually decided. If none, write "None recorded."

## Action items
Bullet list, each as "Owner — what — when". Use the name as spoken. If an owner or date was not stated, write "unassigned" or "no date" rather than guessing. If none, write "None recorded."

## Open questions
Bullet list of things raised but not resolved. Omit this section entirely if there are none.

Be terse. Do not invent anything that is not in the transcript. Do not add a preamble or a closing remark.'

if ! out=$(printf '%s\n\n---TRANSCRIPT---\n%s\n' "$PROMPT" "$transcript" \
           | timeout 300 "$CLAUDE" -p 2>>"$LOG"); then
    echo "(summary failed -- the transcript is still on disk)"
    printf '%s summarise FAILED\n' "$(date -Is)" >> "$LOG"
    exit 1
fi

printf '%s%s\n' "$out" "$truncated"
printf '%s summarise ok: %d chars out\n' "$(date -Is)" "${#out}" >> "$LOG"
