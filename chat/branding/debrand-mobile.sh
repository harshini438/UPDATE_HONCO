#!/usr/bin/env bash
# Honco Chat — de-brand pass over the upstream mobile tree.
# Idempotent: safe to re-run after pulling upstream changes.
set -euo pipefail

ROOT="${ROOT:-$HOME/honco-chat}"
M="$ROOT/mobile"

BRAND="Honco Chat"
ANDROID_ID="com.honco.chat"
IOS_ID="com.honco.chat"
DEEPLINK_SCHEME="honcochat"
AUTH_SCHEME="honcoauth"
SITE_URL="${SITE_URL:-https://chat.honco.in}"

cd "$M"

echo "== 1. brand override config (upstream's supported white-label hook) =="
mkdir -p assets/override
cat > assets/override/config.json <<EOF
{
    "AuthUrlScheme": "${AUTH_SCHEME}://",
    "AuthUrlSchemeDev": "${AUTH_SCHEME}dev://",
    "DefaultServerUrl": "${SITE_URL}",
    "DefaultServerName": "${BRAND}",
    "AutoSelectServerUrl": true,

    "WebsiteURL": "${SITE_URL}",
    "ServerNoticeURL": "",
    "MobileNoticeURL": "",

    "RudderApiKey": "",

    "SentryEnabled": false,
    "SentryDsnIos": "",
    "SentryDsnAndroid": "",

    "ShowReview": false,
    "ShowOnboarding": false
}
EOF
echo "   wrote assets/override/config.json"

echo "== 2. android identity =="
# applicationId is what the device and Play Console see. `namespace` is left
# alone on purpose: it is the Java package of the sources under
# android/app/src/main/java/com/mattermost/rnbeta and renaming it without
# moving every source file breaks the Gradle build.
sed -i 's|applicationId "com.mattermost.rnbeta"|applicationId "'"$ANDROID_ID"'"|' android/app/build.gradle
sed -i 's|<string name="app_name">Mattermost Beta</string>|<string name="app_name">'"$BRAND"'</string>|' \
       android/app/src/main/res/values/strings.xml
sed -i 's|Mattermost Server URL|'"$BRAND"' Server URL|; s|Mattermost Server Name|'"$BRAND"' Server Name|; s|your Mattermost instance|your '"$BRAND"' instance|g; s|the Mattermost Server|the '"$BRAND"' server|' \
       android/app/src/main/res/values/strings.xml
sed -i 's|android:scheme="mattermost"|android:scheme="'"$DEEPLINK_SCHEME"'"|; s|android:scheme="mmauthbeta"|android:scheme="'"$AUTH_SCHEME"'dev"|; s|android:scheme="mmauth"|android:scheme="'"$AUTH_SCHEME"'"|' \
       android/app/src/main/AndroidManifest.xml
echo "   applicationId -> $(grep -o 'applicationId "[^"]*"' android/app/build.gradle)"

echo "== 3. ios identity =="
sed -i 's|PRODUCT_BUNDLE_IDENTIFIER = com.mattermost.rnbeta.NotificationService;|PRODUCT_BUNDLE_IDENTIFIER = '"$IOS_ID"'.NotificationService;|g; s|PRODUCT_BUNDLE_IDENTIFIER = com.mattermost.rnbeta.MattermostShare;|PRODUCT_BUNDLE_IDENTIFIER = '"$IOS_ID"'.Share;|g; s|PRODUCT_BUNDLE_IDENTIFIER = com.mattermost.rnbeta;|PRODUCT_BUNDLE_IDENTIFIER = '"$IOS_ID"';|g' \
       ios/Mattermost.xcodeproj/project.pbxproj
for plist in ios/Mattermost/Info.plist ios/MattermostShare/Info.plist; do
  [ -f "$plist" ] || continue
  python3 - "$plist" "$BRAND" "$DEEPLINK_SCHEME" "$AUTH_SCHEME" <<'PY'
import re, sys
path, brand, deeplink, auth = sys.argv[1:5]
s = open(path).read()
s = s.replace("<string>Mattermost Beta</string>", "<string>%s</string>" % brand)
s = s.replace("<string>mattermost</string>", "<string>%s</string>" % deeplink)
s = s.replace("<string>mmauthbeta</string>", "<string>%sdev</string>" % auth)
s = s.replace("<string>mmauth</string>", "<string>%s</string>" % auth)
open(path, "w").write(s)
PY
  echo "   patched $plist"
done

echo "== 4. drop the crash-reporting SDK entirely =="
# @sentry/react-native is the only third-party telemetry SDK in the tree. It is
# disabled by config upstream, but the dependency and its native hooks stay in
# the binary. Replace the wrapper with a no-op and remove the package.
cat > app/utils/sentry.ts <<'EOF'
// Honco Chat: crash reporting to a third-party service is not used.
// This module keeps the upstream call sites compiling as no-ops.

export function initializeSentry(): void {}

export function captureException(): void {}

export function captureJSException(): void {}

export function addSentryContext(): void {}
EOF
rm -f app/utils/sentry.test.ts
python3 - <<'PY'
import json, io
p = "package.json"
d = json.load(open(p))
removed = []
for sect in ("dependencies", "devDependencies"):
    for k in list(d.get(sect, {})):
        if "sentry" in k.lower():
            d[sect].pop(k)
            removed.append(k)
with io.open(p, "w") as f:
    json.dump(d, f, indent=2)
    f.write("\n")
print("   removed from package.json:", removed or "(already gone)")
PY

echo
echo "== VERIFY =="
echo "-- remaining sentry imports in app/ --"
grep -rn "@sentry/react-native" app/ index.js 2>/dev/null | head || echo "   none"
echo "-- app identity --"
grep -o 'applicationId "[^"]*"' android/app/build.gradle
grep -o '<string name="app_name">[^<]*</string>' android/app/src/main/res/values/strings.xml
grep -o 'PRODUCT_BUNDLE_IDENTIFIER = [^;]*;' ios/Mattermost.xcodeproj/project.pbxproj | sort -u
echo "DEBRAND_MOBILE_DONE"
