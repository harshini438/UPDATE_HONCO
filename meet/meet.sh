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

# The bridge has to advertise an address a peer can actually reach.
#
# Left unset, JVB offers only the address it can see for itself -- a
# docker-internal 172.x -- plus whatever STUN reports for the NAT, which is
# an ephemeral public mapping that is not forwarded. A second machine on the
# LAN then joins the room, sees the participant list, and gets no audio or
# video at all: the failure looks like "it half works" rather than an error.
#
# Derived at startup rather than written into a file, so moving between
# networks does not silently leave a stale address behind. Export it
# yourself, or set HONCO_LAN_IP, to override.
if [ -z "${JVB_ADVERTISE_IPS:-}" ]; then
    if lan_ip=$(./lan-address.sh 2>/dev/null); then
        export JVB_ADVERTISE_IPS="$lan_ip"
    else
        echo "meet.sh: could not derive a LAN address; JVB will not advertise one." >&2
        echo "meet.sh: participants off this host would get no media. Set HONCO_LAN_IP." >&2
    fi
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
