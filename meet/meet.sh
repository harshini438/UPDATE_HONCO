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
DC=(sudo -n docker compose "${FILES[@]}")

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
