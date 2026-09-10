#!/usr/bin/env bash
# Honco Workspace -- health check for whatever this host actually runs.
#
#   healthcheck.sh            human-readable report, one line per component
#   healthcheck.sh --quiet    same checks, only prints failures (for cron)
#
# Exit code is 0 iff every component that's expected to be present on this
# host is healthy. A component is "expected to be present" if its config/
# directory/process exists at all -- this script does not assume it's running
# on ubuntu3 (chat) *and* ubuntu-3 (Jitsi, transcription) at once, since in
# production they're different hosts. Run it from cron for alerting, e.g.:
#
#   */5 * * * * /path/to/healthcheck.sh --quiet || mail -s "Honco health check failed" it@honco.in <<<"see $ROOT/logs/healthcheck.log"
#
# or point CHECK_WEBHOOK at a chat incoming webhook and a failure posts there
# instead (nothing here assumes a specific alerting channel).
set -uo pipefail

ROOT="${ROOT:-$HOME/honco-chat}"
QUIET=false
[ "${1:-}" = "--quiet" ] && QUIET=true

FAILED=0
CHECKED=0

say() { $QUIET || echo "$@"; }
fail() { echo "$@"; FAILED=$((FAILED + 1)); }

check() {
    # check <label> <shell-condition-as-a-string>
    local label="$1" cond="$2"
    CHECKED=$((CHECKED + 1))
    if eval "$cond" >/dev/null 2>&1; then
        say "OK    $label"
    else
        fail "FAIL  $label"
    fi
}

# --- Honco Chat --------------------------------------------------------------
if [ -x "$ROOT/build/honcochat" ] || [ -f "$ROOT/run/honcochat.pid" ]; then
    check "honcochat (chat server, :8065)" \
        "curl -sf --max-time 5 http://127.0.0.1:8065/api/v4/system/ping | grep -q '\"status\":\"OK\"'"
fi

# --- PostgreSQL ----------------------------------------------------------
PGBIN="${PGBIN:-$HOME/toolchain/pg16/bin}"
[ -x "$PGBIN/pg_ctl" ] || PGBIN="/usr/lib/postgresql/16/bin"
if [ -d "$ROOT/pgdata" ]; then
    check "postgres (:5433)" \
        "$PGBIN/pg_ctl -D '$ROOT/pgdata' status"
fi

# --- meetsvc (/meet slash command) ---------------------------------------
if [ -s "$ROOT/run/meetsvc.env" ] || [ -f "$ROOT/run/meetsvc.pid" ]; then
    check "meetsvc (/meet, :8077)" \
        "curl -sf --max-time 5 http://127.0.0.1:8077/health | grep -q '\"status\": *\"ok\"'"
fi

# --- Jitsi / Jibri (only on hosts that run meet/) -------------------------
MEET_DIR="${MEET_DIR:-$HOME/honco-workspace/meet}"
if [ -f "$MEET_DIR/docker-compose.yml" ] && command -v docker >/dev/null 2>&1; then
    check "jitsi web" \
        "docker compose -f '$MEET_DIR/docker-compose.yml' -f '$MEET_DIR/jibri.yml' -f '$MEET_DIR/docker-compose.override.yml' ps web --format '{{.State}}' | grep -q running"
    check "jibri (recording)" \
        "docker compose -f '$MEET_DIR/docker-compose.yml' -f '$MEET_DIR/jibri.yml' -f '$MEET_DIR/docker-compose.override.yml' ps jibri --format '{{.State}}' | grep -q running"
fi

# --- transcription worker (only on hosts running it) ----------------------
if [ -d "$HOME/honco-transcribe" ]; then
    check "transcribe worker process" \
        "pgrep -f 'honco-transcribe/worker.sh' "
    # A stalled worker (crashed but process still around, or wedged on one
    # file) is worse than a stopped one because nothing alerts on it -- so
    # also fail if the newest log line is implausibly old.
    LATEST_LOG=$(ls -t "$HOME/honco-transcribe"/logs/*.log 2>/dev/null | head -1)
    if [ -n "$LATEST_LOG" ]; then
        AGE=$(( $(date +%s) - $(stat -c %Y "$LATEST_LOG") ))
        check "transcribe worker recent activity (<24h since last log write)" \
            "[ $AGE -lt 86400 ]"
    fi
fi

echo
if [ "$FAILED" -eq 0 ]; then
    say "$CHECKED/$CHECKED checks passed"
else
    echo "$FAILED/$CHECKED checks FAILED"
    if [ -n "${CHECK_WEBHOOK:-}" ]; then
        curl -sf --max-time 5 -X POST -H 'Content-Type: application/json' \
            -d "{\"text\":\":rotating_light: Honco health check on $(hostname): $FAILED/$CHECKED checks failed\"}" \
            "$CHECK_WEBHOOK" >/dev/null 2>&1 || true
    fi
fi

mkdir -p "$ROOT/logs" 2>/dev/null
printf '%s %d/%d checks failed\n' "$(date -Is)" "$FAILED" "$CHECKED" >> "$ROOT/logs/healthcheck.log" 2>/dev/null || true

if [ "$FAILED" -eq 0 ]; then exit 0; else exit 1; fi
