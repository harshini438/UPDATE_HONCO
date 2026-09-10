#!/usr/bin/env bash
# Honco Meet -- one place that knows the full compose file list.
#
# Every `docker compose` call must pass the same -f set or compose will happily
# tear down the services it cannot see. Use this instead of calling compose
# directly.
#
#   ./meet.sh up | down | ps | logs <svc> | restart <svc> | exec <svc> <cmd...>
set -euo pipefail

cd "$(dirname "$0")"

FILES=(-f docker-compose.yml -f jibri.yml -f docker-compose.override.yml)
# ubuntu-3 (no sudo *and* no docker group membership for the deploy user)
# needs sudo -n to reach the docker socket at all. A dev box where the user
# is already in the docker group -- confirmed the case here -- has direct
# access, and sudo -n would just fail outright (it cannot prompt). Prefer
# direct access when it works rather than hardcoding either assumption.
if docker info >/dev/null 2>&1; then
    DC=(docker compose "${FILES[@]}")
else
    DC=(sudo -n docker compose "${FILES[@]}")
fi

case "${1:-ps}" in
up)      shift; "${DC[@]}" up -d "$@" ;;
down)    shift; "${DC[@]}" down "$@" ;;
ps)      "${DC[@]}" ps --format "  {{.Service}}  {{.State}}  {{.Ports}}" ;;
logs)    shift; "${DC[@]}" logs --tail="${TAIL:-40}" "$@" ;;
restart) shift; "${DC[@]}" restart "$@" ;;
exec)    shift; svc="$1"; shift; "${DC[@]}" exec -T "$svc" "$@" ;;
config)  "${DC[@]}" config ;;
*)       echo "usage: $0 {up|down|ps|logs <svc>|restart <svc>|exec <svc> <cmd...>|config}" >&2; exit 2 ;;
esac
