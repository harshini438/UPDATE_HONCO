#!/usr/bin/env bash
# Honco Meet -- brand the Jitsi deployment and wire in our own backgrounds.
# Idempotent; re-run after pulling a newer docker-jitsi-meet.
set -euo pipefail

MEET="$HOME/honco-meet"
CFG="$HOME/.jitsi-meet-cfg"
cd "$MEET"

echo "== 1. serve our backgrounds from the web root =="
# A compose override rather than editing docker-compose.yml, so an upstream
# pull does not clobber it. ${CONFIG} is resolved by compose from .env.
cat > docker-compose.override.yml <<'YAML'
services:
  web:
    volumes:
      - ${CONFIG}/web-backgrounds:/usr/share/jitsi-meet/images/honco-backgrounds:ro
YAML
echo "   wrote docker-compose.override.yml"

echo "== 2. branding =="
mkdir -p "$CFG/web"
# These files are appended to Jitsi's own config at container start, so they
# survive image upgrades -- unlike editing files inside the image.
cat > "$CFG/web/custom-interface_config.js" <<'JS'
// Honco Meet -- branding overrides. Appended to interface_config.js.
interfaceConfig.APP_NAME = "Honco Meet";
interfaceConfig.NATIVE_APP_NAME = "Honco Meet";
interfaceConfig.PROVIDER_NAME = "Honco";
interfaceConfig.JITSI_WATERMARK_LINK = "";
interfaceConfig.SHOW_JITSI_WATERMARK = false;
interfaceConfig.SHOW_WATERMARK_FOR_GUESTS = false;
interfaceConfig.SHOW_BRAND_WATERMARK = false;
interfaceConfig.SHOW_POWERED_BY = false;
interfaceConfig.DISPLAY_WELCOME_PAGE_CONTENT = false;
interfaceConfig.DISPLAY_WELCOME_FOOTER = false;
interfaceConfig.MOBILE_APP_PROMO = false;
JS
echo "   wrote custom-interface_config.js"

echo "== 3. background picker =="
python3 - "$CFG" <<'PY'
import os, sys
cfg = sys.argv[1]
d = os.path.join(cfg, "web-backgrounds")
files = sorted(f for f in os.listdir(d) if f.lower().endswith((".jpg", ".jpeg", ".png")))
urls = ",\n".join('        "/images/honco-backgrounds/%s"' % f for f in files)
body = '''// Honco Meet -- config overrides. Appended to config.js.
config.virtualBackgrounds = [
%s
];
config.disableVirtualBackground = false;
config.prejoinConfig = { enabled: true };
// No third-party fetches: gravatar avatars and the like would leak who is in
// a meeting to an outside service.
config.disableThirdPartyRequests = true;
config.analytics = { disabled: true };
config.deploymentInfo = {};
''' % urls
open(os.path.join(cfg, "web", "custom-config.js"), "w").write(body)
print("   custom-config.js lists %d backgrounds" % len(files))
PY

echo "== 4. permissions + restart =="
sudo -n chown -R 1000:1000 "$CFG/web" "$CFG/web-backgrounds"
sudo -n docker compose up -d 2>&1 | tail -3
sleep 20
sudo -n docker compose ps --format "   {{.Service}} {{.State}}" | head -6
