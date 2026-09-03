#!/usr/bin/env bash
# Honco Chat — de-brand pass over the upstream server tree.
# Idempotent: safe to re-run after pulling upstream changes.
set -euo pipefail

ROOT="${ROOT:-$HOME/honco-chat}"
S="$ROOT/server"

BRAND="Honco Chat"
PUSH_URL="${PUSH_URL:-http://127.0.0.1:8066}"   # our own push proxy, set later

cd "$S"
echo "== 1. remove source-available (non-free) code =="
# Everything under server/enterprise is gated behind the `enterprise` /
# `sourceavailable` build tags and carries the Mattermost Source Available
# Licence, not AGPL. We never build with those tags, so delete it outright and
# keep only the empty package placeholder so the import path still resolves.
rm -rf server/enterprise/elasticsearch \
       server/enterprise/message_export \
       server/enterprise/metrics \
       server/enterprise/external_imports.go \
       server/enterprise/local_imports.go \
       server/enterprise/LICENSE \
       server/enterprise/README.md \
       LICENSE.enterprise \
       docs/develop/integrate/plugins/source-available-license
echo "   server/enterprise now: $(ls server/enterprise)"

echo "== 2. kill the phone-home: security update check =="
# Upstream POSTs server id, build, DB type, OS, user/team/active-user counts to
# securityupdatecheck.mattermost.com every 24h. Replaced with a no-op.
cat > server/channels/app/security_update_check.go <<'EOF'
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

// Honco Chat: the upstream security-update check reported this server's id,
// build, database type, OS and user/team/active-user counts to an external
// vendor endpoint once every 24 hours. It is a no-op here.
func (s *Server) DoSecurityUpdateCheck() {}
EOF

echo "== 3. kill the phone-home: in-product auto-upgrade =="
# upgrader_linux.go downloads a tarball from releases.mattermost.com and
# replaces the running binary. upgrader.go already carries a "not supported"
# stub for every other OS; drop its !linux tag so it covers linux too.
rm -f server/platform/services/upgrader/upgrader_linux.go \
      server/platform/services/upgrader/upgrader_linux_test.go \
      server/platform/services/upgrader/pubkey.gpg
sed -i 's|^//go:build !linux$|// Honco Chat: in-product binary auto-upgrade removed on every platform.|' \
      server/platform/services/upgrader/upgrader.go
sed -i '/^\/\/ +build !linux$/d' server/platform/services/upgrader/upgrader.go

echo "== 4. blank the remaining vendor endpoints in config defaults =="
C=server/public/model/config.go
sed -i \
  -e 's|GenericNotificationServer    = "https://push-test.mattermost.com"|GenericNotificationServer    = "'"$PUSH_URL"'"|' \
  -e 's|AnnouncementSettingsDefaultNoticesJsonURL               = "https://notices.mattermost.com/"|AnnouncementSettingsDefaultNoticesJsonURL               = ""|' \
  -e 's|PluginSettingsDefaultMarketplaceURL     = "https://api.integrations.mattermost.com"|PluginSettingsDefaultMarketplaceURL     = ""|' \
  -e 's|PluginSettingsOldMarketplaceURL         = "https://marketplace.integrations.mattermost.com"|PluginSettingsOldMarketplaceURL         = ""|' \
  -e 's|CloudSettingsDefaultCwsURL        = "https://customers.mattermost.com"|CloudSettingsDefaultCwsURL        = ""|' \
  -e 's|CloudSettingsDefaultCwsAPIURL     = "https://portal.internal.prod.cloud.mattermost.com"|CloudSettingsDefaultCwsAPIURL     = ""|' \
  -e 's|CloudSettingsDefaultCwsURLTest    = "https://portal.test.cloud.mattermost.com"|CloudSettingsDefaultCwsURLTest    = ""|' \
  -e 's|CloudSettingsDefaultCwsAPIURLTest = "https://api.internal.test.cloud.mattermost.com"|CloudSettingsDefaultCwsAPIURLTest = ""|' \
  -e 's|TeamSettingsDefaultSiteName              = "Mattermost"|TeamSettingsDefaultSiteName              = "'"$BRAND"'"|' \
  "$C"

echo "== 5. default the vendor-facing toggles OFF =="
# EnableSecurityFixAlert defaulted true; the remote plugin marketplace defaulted on.
python3 - "$C" <<'PY'
import re, sys
p = sys.argv[1]
src = open(p).read()
for name in ("EnableSecurityFixAlert", "EnableRemoteMarketplace"):
    src = re.sub(
        r'(if s\.%s == nil \{\n\t\ts\.%s = new)\(true\)' % (name, name),
        r'\1(false)', src)
open(p, "w").write(src)
PY

echo "== 6. licence renewal + in-product sales links =="
sed -i 's|LicenseRenewalLink               = "https://mattermost.com/renew/"|LicenseRenewalLink               = ""|' \
      server/public/model/license.go
sed -i \
  -e 's|data.Props\["AppMarketPlaceLink"\] = "https://integrations.mattermost.com/"|data.Props["AppMarketPlaceLink"] = ""|' \
  -e 's|data.Props\["ButtonURL"\] = "https://mattermost.com/contact-sales/"|data.Props["ButtonURL"] = ""|' \
      server/channels/app/email/email.go

echo "== 7. user-visible product name in translations =="
# Display strings only. Import paths (github.com/mattermost/...) are NEVER
# touched -- renaming those breaks every build.
for f in server/i18n/en.json webapp/channels/src/i18n/en.json; do
  [ -f "$f" ] && sed -i 's/Mattermost/'"$BRAND"'/g' "$f" && echo "   rebranded $f"
done

echo
echo "== VERIFY: runtime calls to vendor hosts still in non-test Go code =="
grep -rInE '"https?://[a-z0-9.-]*mattermost[a-z0-9.-]*\.(com|io)' --include=*.go . \
  | grep -vE '/vendor/|_test\.go|storetest|searchtest' \
  | grep -viE 'docs\.|api\.mattermost\.com/#|example\.com|/pl/|about\.mattermost' \
  || echo "   none -- clean"
echo "DEBRAND_SERVER_DONE"
