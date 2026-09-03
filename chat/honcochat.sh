#!/usr/bin/env bash
# Honco Chat — start / stop / status for the ubuntu3 instance, plus the
# Cloudflare quick tunnel that publishes it.
#
# ubuntu3 has no root, so there is no systemd unit and no `loginctl
# enable-linger`. logind here runs KillUserProcesses=false, so a detached
# process survives SSH logout. This script records both PIDs so nothing ever
# becomes an untracked orphan -- always stop things through here.
#
# ubuntu3's firewall also blocks inbound 8065, so the tunnel is not just for
# outside clients: it is how anyone reaches this at all.
set -euo pipefail

ROOT="$HOME/honco-chat"
PGBIN="$HOME/toolchain/pg16/bin"
PGDATA="$ROOT/pgdata"
PGSOCK="$ROOT/pgsock"
PIDFILE="$ROOT/run/honcochat.pid"
TUNPID="$ROOT/run/tunnel.pid"
TUNLOG="$ROOT/logs/tunnel.log"
URLFILE="$ROOT/run/public-url.txt"
SVCPID="$ROOT/run/meetsvc.pid"
SVCLOG="$ROOT/logs/meetsvc.log"
LOG="$ROOT/logs/honcochat.log"
PORT="${HONCO_PORT:-8065}"
HOST_IP="${HONCO_HOST_IP:-192.168.2.155}"

# SiteURL must match the address people actually use, or websockets, CSRF checks
# and every emailed link break. A quick tunnel issues a fresh hostname on each
# start, so it is read back from the log and injected here.
site_url() {
    if [ -s "$URLFILE" ]; then cat "$URLFILE"; else echo "http://${HOST_IP}:${PORT}"; fi
}

export_env() {
    export MM_SQLSETTINGS_DRIVERNAME=postgres
    export MM_SQLSETTINGS_DATASOURCE="postgres://honco@127.0.0.1:5433/honcochat?sslmode=disable&connect_timeout=10"
    export MM_SERVICESETTINGS_LISTENADDRESS=":${PORT}"
    export MM_SERVICESETTINGS_SITEURL="$(site_url)"
    export MM_FILESETTINGS_DIRECTORY="$ROOT/run/data/"
    export MM_LOGSETTINGS_FILELOCATION="$ROOT/logs/"
    export MM_PLUGINSETTINGS_DIRECTORY="$ROOT/run/plugins/"
    export MM_PLUGINSETTINGS_CLIENTDIRECTORY="$ROOT/run/client-plugins/"
    # The instance is publicly reachable through the tunnel. Self-signup stays
    # off so the admin invites people; without this, anyone with the URL could
    # register themselves.
    export MM_TEAMSETTINGS_ENABLEOPENSERVER=false
    export MM_TEAMSETTINGS_ENABLEUSERCREATION=true
    # Bots back the /meet slash command. Off by default in Mattermost.
    export MM_SERVICESETTINGS_ENABLEBOTACCOUNTCREATION=true
    # Slash commands are how /meet reaches the meeting service.
    export MM_SERVICESETTINGS_ENABLECOMMANDS=true
    export MM_SERVICESETTINGS_ENABLEPOSTUSERNAMEOVERRIDE=true
    export MM_SERVICESETTINGS_ENABLEPOSTICONOVERRIDE=true
    # The /meet slash command points at a service on this same host. Mattermost
    # refuses to call loopback URLs unless they are named here.
    export MM_SERVICESETTINGS_ALLOWEDUNTRUSTEDINTERNALCONNECTIONS="127.0.0.1 localhost"
}

pg_up()  { "$PGBIN/pg_ctl" -D "$PGDATA" status >/dev/null 2>&1; }
running(){ [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; }
tun_up() { [ -f "$TUNPID" ] && kill -0 "$(cat "$TUNPID")" 2>/dev/null; }
svc_up() { [ -f "$SVCPID" ] && kill -0 "$(cat "$SVCPID")" 2>/dev/null; }

start_svc() {
    if svc_up; then echo "meetsvc:   already running (pid $(cat "$SVCPID"))"; return; fi
    if [ ! -s "$ROOT/run/meetsvc.env" ]; then
        echo "meetsvc:   no meetsvc.env -- skipping"
        return
    fi
    # The /meet backend. It binds loopback only: the chat server is on this same
    # host, and nothing else should be able to post as the bot.
    set -a
    . "$ROOT/run/meetsvc.env"
    set +a
    CMD_TOKEN="$(cat "$ROOT/run/.cmd-token" 2>/dev/null || true)"
    export CMD_TOKEN
    export MEET_BASE="${MEET_BASE:-https://192.168.2.156:8443}"
    # Host is UTC; the people using this are not. Process-local only.
    export TZ="${TZ:-Asia/Kolkata}"
    mkdir -p "$ROOT/logs"
    setsid nohup python3 "$ROOT/meetsvc.py" >>"$SVCLOG" 2>&1 < /dev/null &
    echo $! > "$SVCPID"
    for _ in $(seq 1 15); do
        if curl -sf --max-time 2 http://127.0.0.1:8077/health >/dev/null 2>&1; then
            echo "meetsvc:   up (pid $(cat "$SVCPID"))"
            return 0
        fi
        sleep 1
    done
    echo "meetsvc:   did NOT come up -- see $SVCLOG" >&2
}

stop_svc() {
    if svc_up; then
        kill "$(cat "$SVCPID")" 2>/dev/null || true
        echo "meetsvc:   stopped"
    else
        echo "meetsvc:   not running"
    fi
    rm -f "$SVCPID"
}

start_pg() {
    if pg_up; then echo "postgres:  already up"; return; fi
    mkdir -p "$PGSOCK" "$ROOT/logs"
    "$PGBIN/pg_ctl" -D "$PGDATA" -l "$ROOT/logs/pg.log" \
        -o "-p 5433 -k $PGSOCK -c listen_addresses=127.0.0.1" -w start >/dev/null
    echo "postgres:  started on 5433"
}

start_app() {
    if running; then echo "honcochat: already running (pid $(cat "$PIDFILE"))"; return; fi
    export_env
    mkdir -p "$ROOT/run" "$ROOT/logs"
    cd "$ROOT/run"
    setsid nohup "$ROOT/build/honcochat" server >>"$LOG" 2>&1 < /dev/null &
    echo $! > "$PIDFILE"
    for _ in $(seq 1 40); do
        if curl -sf --max-time 3 "http://127.0.0.1:${PORT}/api/v4/system/ping" >/dev/null 2>&1; then
            echo "honcochat: up  pid=$(cat "$PIDFILE")  SiteURL=$(site_url)"
            return 0
        fi
        sleep 2
    done
    echo "honcochat: did NOT come up in 80s -- see $LOG" >&2
    return 1
}

stop_app() {
    if running; then
        kill "$(cat "$PIDFILE")" 2>/dev/null || true
        for _ in $(seq 1 20); do running || break; sleep 1; done
        running && kill -9 "$(cat "$PIDFILE")" 2>/dev/null || true
        echo "honcochat: stopped"
    else
        echo "honcochat: not running"
    fi
    rm -f "$PIDFILE"
}

start_tunnel() {
    if tun_up; then echo "tunnel:    already up -> $(cat "$URLFILE" 2>/dev/null)"; return; fi
    mkdir -p "$ROOT/logs" "$ROOT/run"
    : > "$TUNLOG"
    setsid nohup "$ROOT/bin/cloudflared" tunnel --no-autoupdate \
        --url "http://127.0.0.1:${PORT}" >>"$TUNLOG" 2>&1 < /dev/null &
    echo $! > "$TUNPID"
    for _ in $(seq 1 40); do
        URL=$(grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' "$TUNLOG" | head -1 || true)
        if [ -n "${URL:-}" ]; then
            echo "$URL" > "$URLFILE"
            echo "tunnel:    up -> $URL"
            return 0
        fi
        sleep 2
    done
    echo "tunnel:    no URL after 80s -- see $TUNLOG" >&2
    return 1
}

stop_tunnel() {
    if tun_up; then
        kill "$(cat "$TUNPID")" 2>/dev/null || true
        sleep 1
        tun_up && kill -9 "$(cat "$TUNPID")" 2>/dev/null || true
        echo "tunnel:    stopped"
    else
        echo "tunnel:    not running"
    fi
    rm -f "$TUNPID" "$URLFILE"
}

case "${1:-status}" in
start)
    start_pg; start_app; start_svc
    ;;
publish)
    # Bring up the tunnel first, then restart the app so its SiteURL is the
    # public hostname. Doing it the other way round leaves SiteURL stale and
    # the web client fails its websocket handshake.
    start_pg
    stop_app
    start_tunnel
    start_app
    start_svc
    echo
    echo "public URL: $(cat "$URLFILE" 2>/dev/null)"
    echo "note: a quick tunnel issues a NEW hostname every restart."
    echo "      a permanent address needs a named tunnel on your Cloudflare account."
    ;;
stop)
    stop_app; stop_svc
    ;;
stop-all)
    stop_app; stop_svc; stop_tunnel
    pg_up && "$PGBIN/pg_ctl" -D "$PGDATA" -w stop >/dev/null && echo "postgres:  stopped" || true
    ;;
status)
    pg_up && echo "postgres:  up (5433)" || echo "postgres:  down"
    if running; then echo "honcochat: up (pid $(cat "$PIDFILE"))"; else echo "honcochat: down"; fi
    if tun_up; then echo "tunnel:    up -> $(cat "$URLFILE" 2>/dev/null)"; else echo "tunnel:    down"; fi
    if svc_up; then echo "meetsvc:   up (pid $(cat "$SVCPID"))"; else echo "meetsvc:   down"; fi
    echo "SiteURL:   $(site_url)"
    running && { curl -s --max-time 5 "http://127.0.0.1:${PORT}/api/v4/system/ping" && echo; } || true
    ;;
*)
    echo "usage: $0 {start|publish|stop|stop-all|status}" >&2
    exit 2
    ;;
esac
