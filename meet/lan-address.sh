#!/usr/bin/env bash
# Print the address on which this host's published container ports are
# reachable from other machines on the LAN.
#
# This is deliberately not "this machine's IP", because those differ:
#
#   native Linux (ubuntu-3)   the address on the interface holding the
#                             default route -- containers publish there.
#
#   WSL2 + Docker Desktop     containers publish on the *Windows* host, not
#                             inside the WSL VM. WSL's own eth0 address is a
#                             172.x NAT address that no other machine on the
#                             LAN can reach, so asking Linux would give an
#                             answer that looks right and fails in practice.
#                             Ask Windows for its LAN address instead.
#
# Set HONCO_LAN_IP to pin it explicitly (a DHCP reservation, a second NIC, a
# host with several candidate addresses). Nothing downstream hardcodes an
# address; they all call this.
set -uo pipefail

if [ -n "${HONCO_LAN_IP:-}" ]; then
    printf '%s\n' "$HONCO_LAN_IP"
    exit 0
fi

ip_addr=""
if grep -qiE 'microsoft|wsl' /proc/version 2>/dev/null; then
    # Skip loopback, the WSL vEthernet bridge, and link-local autoconfig
    # addresses -- none of those are reachable from a peer.
    ip_addr=$(powershell.exe -NoProfile -Command "
        (Get-NetIPAddress -AddressFamily IPv4 |
          Where-Object { \$_.InterfaceAlias -notmatch 'Loopback|WSL|vEthernet' -and
                         \$_.IPAddress -notlike '169.254.*' -and
                         \$_.IPAddress -notlike '127.*' } |
          Select-Object -First 1 -ExpandProperty IPAddress)" 2>/dev/null | tr -d '\r\n')
else
    # The source address the kernel would use to reach the outside world;
    # no packet is actually sent.
    ip_addr=$(ip route get 1.1.1.1 2>/dev/null | awk '{print $7; exit}')
fi

# The last address this script derived, kept so a transient failure to ask
# the host does not turn every meeting link into localhost. Seen for real:
# Docker Desktop's binfmt registrations displaced WSL's own, powershell.exe
# stopped executing from inside WSL, and the next start fell back to
# localhost until someone noticed. A remembered address can be stale, so
# it is only ever used as a fallback and says so; whenever derivation
# works the cache is refreshed. HONCO_LAN_IP still overrides everything.
CACHE="${HONCO_LAN_CACHE:-${ROOT:-$HOME/honco-chat}/run/lan-address.last}"

case "${ip_addr:-}" in
    "" | 127.* | 169.254.*)
        if [ -s "$CACHE" ] && cached=$(head -1 "$CACHE") && [ -n "$cached" ]; then
            echo "lan-address: could not determine a LAN address (got '${ip_addr:-}');" >&2
            echo "lan-address: using the last known address $cached from $CACHE." >&2
            echo "lan-address: if this host has moved network, set HONCO_LAN_IP." >&2
            printf '%s\n' "$cached"
            exit 0
        fi
        echo "lan-address: could not determine a LAN address (got '${ip_addr:-}')." >&2
        echo "lan-address: set HONCO_LAN_IP to the address peers should use." >&2
        exit 1
        ;;
esac

mkdir -p "$(dirname "$CACHE")" 2>/dev/null && printf '%s\n' "$ip_addr" > "$CACHE" 2>/dev/null || true
printf '%s\n' "$ip_addr"
