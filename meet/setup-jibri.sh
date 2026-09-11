#!/usr/bin/env bash
# Honco Meet -- turn on recording (Jibri).
#
# Recordings land in ~/.jitsi-meet-cfg/storage/jibri. When one finishes, Jibri
# runs JIBRI_FINALIZE_RECORDING_SCRIPT_PATH with the recording directory as $1 --
# that is the hook the whisper transcription pipeline hangs off.
set -euo pipefail

MEET="$HOME/honco-meet"
CFG="$HOME/.jitsi-meet-cfg"
cd "$MEET"

echo "== 1. .env knobs =="
python3 - <<'PY'
import re

WANT = {
    "ENABLE_RECORDING": "1",
    "ENABLE_SERVICE_RECORDING": "1",
    # Jibri writes here; the path is inside the container, mapped to
    # $CONFIG/storage/jibri on the host.
    "JIBRI_RECORDING_DIR": "/storage",
    # Runs once per finished recording, with the recording dir as $1.
    "JIBRI_FINALIZE_RECORDING_SCRIPT_PATH": "/config/finalize.sh",
    "JIBRI_RECORDING_RESOLUTION": "1280x720",
    "JIBRI_RECORDING_FRAMERATE": "25",
    # A local recording never leaves the LAN, so no need to burn CPU on a
    # smaller file; favour speed so the box stays free for other work.
    "JIBRI_RECORDING_VIDEO_ENCODE_PRESET_RECORDING": "veryfast",
    "JIBRI_STRIP_DOMAIN_JID": "conference",
}

s = open(".env").read()
for k, v in WANT.items():
    if re.search(r"^#?%s=" % re.escape(k), s, re.M):
        s = re.sub(r"^#?%s=.*$" % re.escape(k), "%s=%s" % (k, v), s, flags=re.M)
    else:
        s += "\n%s=%s" % (k, v)
if not s.endswith("\n"):
    s += "\n"
open(".env", "w").write(s)
for k in WANT:
    print("   %s=%s" % (k, WANT[k]))
PY

echo "== 2. storage =="
mkdir -p "$CFG/storage/jibri" "$CFG/jibri"
sudo -n chown -R 1000:1000 "$CFG/storage/jibri" "$CFG/jibri"
echo "   $CFG/storage/jibri"

echo "== 3. finalize hook =="
# The hook is version-controlled (jibri-finalize.sh, beside this script)
# and carries no secret. The callback secret goes in its own 0600 file,
# read at run time, so the hook can be committed, reviewed and backed up
# while the secret is none of those things.
#
# The secret comes from ~/.honco-jibri-secret (JIBRI_SECRET=...), the same
# file the plugin's JibriCallbackSecret setting is filled from. If it does
# not exist yet, one is generated here; set the plugin setting to match.
HERE="$(cd "$(dirname "$0")" && pwd)"
sed 's/\r$//' "$HERE/jibri-finalize.sh" > "$CFG/jibri/finalize.sh"
chmod 755 "$CFG/jibri/finalize.sh"
if [ ! -s "$HOME/.honco-jibri-secret" ]; then
    (umask 077; printf 'JIBRI_SECRET=%s\n' "$(openssl rand -hex 32)" > "$HOME/.honco-jibri-secret")
    echo "   generated a new callback secret in ~/.honco-jibri-secret -- set the plugin's"
    echo "   'Jibri callback secret' to the same value (mmctl --local config set ...)."
fi
# shellcheck disable=SC1090
. "$HOME/.honco-jibri-secret"
(umask 077; printf '%s\n' "$JIBRI_SECRET" > "$CFG/jibri/honco-callback.secret")
sudo -n chown 1000:1000 "$CFG/jibri/finalize.sh" "$CFG/jibri/honco-callback.secret" 2>/dev/null || true
chmod 600 "$CFG/jibri/honco-callback.secret"
# Where the hook delivers to. Not a fixed address: honcochat.sh rewrites
# this on every start from the distro's own eth0, the one address a Docker
# Desktop container can reach a WSL distro on. Owned by the operator so
# that rewrite needs no root.
touch "$CFG/jibri/honco-chat.host"; chmod 644 "$CFG/jibri/honco-chat.host"
echo "   $CFG/jibri/finalize.sh (from jibri-finalize.sh)"
echo "   $CFG/jibri/honco-callback.secret (0600)"
echo "   $CFG/jibri/honco-chat.host (filled in by honcochat.sh start)"

echo "== 4. bring the stack up with jibri =="
./meet.sh up 2>&1 | tail -6
sleep 25
./meet.sh ps
