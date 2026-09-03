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

echo "== 3. finalize hook (placeholder until the whisper pipeline lands) =="
cat > "$CFG/jibri/finalize.sh" <<'SH'
#!/bin/bash
# Jibri calls this with the finished recording's directory as $1.
# The whisper pipeline replaces the body of this script.
RECORDING_DIR="$1"
echo "$(date -Is) finalize: $RECORDING_DIR" >> /storage/finalize.log
exit 0
SH
chmod +x "$CFG/jibri/finalize.sh"
sudo -n chown 1000:1000 "$CFG/jibri/finalize.sh"
echo "   $CFG/jibri/finalize.sh"

echo "== 4. bring the stack up with jibri =="
./meet.sh up 2>&1 | tail -6
sleep 25
./meet.sh ps
