#!/usr/bin/env bash
# Install the Honco Workspace plugin on ubuntu-3.
#
# ubuntu-3 is where this belongs: it is the documented production host and
# the only box that shares the 192.168.2.0/24 LAN with `mother`, where the
# Claude CLI is authenticated. Meeting Intelligence reaches the summariser
# by SSH forced command, so it can only work from a host on that network.
#
#   ./install-on-ubuntu3.sh [--key /path/to/summariser/ssh/key]
#
# Safe to re-run. It adds a plugin and applies additive migrations; it
# never touches a Mattermost-owned table, never resets a database, and
# never removes existing data.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLUGIN_DIR="$(dirname "$HERE")"
PLUGIN_ID="com.honco.workspace"
MMCTL="${MMCTL:-$HOME/honco-chat/build/mmctl}"
KEY_PATH=""

while [ $# -gt 0 ]; do
    case "$1" in
        --key) KEY_PATH="${2:?--key needs a path}"; shift 2 ;;
        --mmctl) MMCTL="${2:?--mmctl needs a path}"; shift 2 ;;
        -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

say() { printf '  %s\n' "$*"; }

echo "== preflight =="
[ -x "$MMCTL" ] || { echo "mmctl not found at $MMCTL (pass --mmctl)" >&2; exit 1; }
command -v go >/dev/null || { echo "go is required to build the plugin" >&2; exit 1; }
command -v ssh >/dev/null || { echo "ssh is required to reach the summariser" >&2; exit 1; }
say "mmctl: $MMCTL"
say "go:    $(go version | awk '{print $3}')"

# The whole point of installing here rather than on a laptop.
echo
echo "== can this host reach the Claude summariser? =="
MOTHER_HOST="${MOTHER_HOST:-192.168.2.150}"
MOTHER_PORT="${MOTHER_PORT:-31013}"
if timeout 8 bash -c "cat < /dev/null > /dev/tcp/$MOTHER_HOST/$MOTHER_PORT" 2>/dev/null; then
    say "$MOTHER_HOST:$MOTHER_PORT is OPEN"
else
    echo "  $MOTHER_HOST:$MOTHER_PORT is UNREACHABLE." >&2
    echo "  Meeting Intelligence cannot work from this host. Install on a box" >&2
    echo "  on the 192.168.2.0/24 LAN, or fix routing first." >&2
    exit 1
fi

echo
echo "== build =="
cd "$PLUGIN_DIR"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
mkdir -p "$STAGE/$PLUGIN_ID/server/dist" "$STAGE/$PLUGIN_ID/webapp/dist"
GOOS=linux GOARCH=amd64 go build -trimpath \
    -o "$STAGE/$PLUGIN_ID/server/dist/plugin-linux-amd64" ./server/
cp plugin.json "$STAGE/$PLUGIN_ID/plugin.json"
cp webapp/dist/main.js "$STAGE/$PLUGIN_ID/webapp/dist/main.js"
( cd "$STAGE" && tar czf "$PLUGIN_ID.tar.gz" "$PLUGIN_ID" )
say "bundle: $(stat -c%s "$STAGE/$PLUGIN_ID.tar.gz") bytes"

echo
echo "== install =="
# --force replaces the running copy in place. Mattermost removes a hand-placed
# plugin directory on sync, so it must be installed as a bundle, not copied.
"$MMCTL" --local plugin add --force "$STAGE/$PLUGIN_ID.tar.gz"
"$MMCTL" --local plugin enable "$PLUGIN_ID"
sleep 6
"$MMCTL" --local plugin list | grep -i honco || true

if [ -n "$KEY_PATH" ]; then
    echo
    echo "== point the summariser at an explicit SSH identity =="
    [ -r "$KEY_PATH" ] || { echo "  key not readable: $KEY_PATH" >&2; exit 1; }
    case "$KEY_PATH" in /*) ;; *) echo "  key path must be absolute" >&2; exit 1 ;; esac
    # Merge, never replace: overwriting the plugin's settings object would
    # drop the Jibri and meet-service secrets Feature 2 depends on.
    python3 - "$MMCTL" "$PLUGIN_ID" "$KEY_PATH" <<'PY'
import json, subprocess, sys
mmctl, plugin_id, key = sys.argv[1:4]
run = lambda a, i=None: subprocess.run([mmctl, "--local"] + a, input=i,
                                       capture_output=True, text=True, check=True).stdout
cfg = json.loads(run(["config", "show", "--json"]))
settings = dict(cfg.get("PluginSettings", {}).get("Plugins", {}).get(plugin_id, {}))
settings["summarizerkeypath"] = key
run(["config", "patch", "/dev/stdin"],
    json.dumps({"PluginSettings": {"Plugins": {plugin_id: settings}}}))
after = json.loads(run(["config", "show", "--json"]))["PluginSettings"]["Plugins"][plugin_id]
print("  summarizerkeypath set")
print("  jibri secret preserved: %s" % bool(after.get("jibricallbacksecret")))
print("  meet secret preserved:  %s" % bool(after.get("meetservicesecret")))
PY
else
    echo
    say "No --key given: ssh will use the default identity of the user the"
    say "Mattermost server runs as. That user's ~/.ssh must hold the key whose"
    say "public half is in mother's authorized_keys behind the forced command."
fi

echo
echo "Installed. Now prove it end to end:"
echo "  $HERE/verify-meeting-intelligence.sh"
