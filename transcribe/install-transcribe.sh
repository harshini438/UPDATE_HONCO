#!/usr/bin/env bash
# Honco Meet -- wire the recording -> transcript pipeline together.
#
# Three pieces:
#   1. finalize.sh   runs inside the jibri container, drops a marker
#   2. worker.sh     runs on the host under systemd, does the real work
#   3. transcribe.sh does one recording
#
# ubuntu-3 has root, so this is a real systemd service rather than a detached
# nohup -- it restarts on failure and survives a reboot.
set -euo pipefail

ROOT="$HOME/honco-transcribe"
CFG="$HOME/.jitsi-meet-cfg"
USER_NAME="$(whoami)"

mkdir -p "$ROOT/logs" "$ROOT/state"
chmod +x "$ROOT/transcribe.sh" "$ROOT/worker.sh"

echo "== 1. finalize hook (inside the jibri container) =="
cat > "$CFG/jibri/finalize.sh" <<'SH'
#!/bin/bash
# Jibri runs this when a recording finishes, with the recording directory as $1.
#
# This container has no whisper and no GPU, so it does the only useful thing it
# can: drop a marker in the shared /storage volume for the host-side worker.
# Keep it fast -- Jibri waits on this before returning to IDLE.
set -u
RECORDING_DIR="${1:-}"
[ -n "$RECORDING_DIR" ] || exit 0

NAME=$(basename "$RECORDING_DIR")
echo "$(date -Is) finalize: $RECORDING_DIR" >> /storage/finalize.log
touch "/storage/${NAME}.ready"
exit 0
SH
chmod +x "$CFG/jibri/finalize.sh"
sudo -n chown 1000:1000 "$CFG/jibri/finalize.sh"
echo "   $CFG/jibri/finalize.sh"

echo "== 2. systemd service =="
sudo -n tee /etc/systemd/system/honco-transcribe.service >/dev/null <<EOF
[Unit]
Description=Honco Meet - transcribe finished Jibri recordings with local whisper
After=network.target docker.service
Wants=docker.service

[Service]
Type=simple
User=${USER_NAME}
Environment=HOME=${HOME}
WorkingDirectory=${ROOT}
ExecStart=${ROOT}/worker.sh
Restart=always
RestartSec=15
# Transcription is a long GPU/CPU job; never let it starve the meeting stack,
# which is the thing users actually notice.
Nice=10
IOSchedulingClass=idle
StandardOutput=append:${ROOT}/logs/worker.log
StandardError=append:${ROOT}/logs/worker.log

[Install]
WantedBy=multi-user.target
EOF

sudo -n systemctl daemon-reload
sudo -n systemctl enable --now honco-transcribe.service
echo "   honco-transcribe.service: $(systemctl is-active honco-transcribe.service)"

echo "== 3. restart jibri so it picks up the new finalize script =="
cd "$HOME/honco-meet" && ./meet.sh restart jibri >/dev/null 2>&1
sleep 10
./meet.sh ps | grep -E "jibri" || true

echo
echo "recordings:  $CFG/storage/jibri"
echo "transcripts: alongside each .mp4, as .txt and .vtt"
echo "worker log:  $ROOT/logs/worker.log"
