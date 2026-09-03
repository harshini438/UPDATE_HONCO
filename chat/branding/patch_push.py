#!/usr/bin/env python3
"""Honco Chat: drop the vendor-hosted push service (HPNS) endpoints."""
import os
import sys

p = os.path.expanduser("~/honco-chat/server/server/public/model/push_notification.go")
s = open(p).read()
orig = s

s = s.replace('MHPNSLegacyUS = "https://push.mattermost.com"', 'MHPNSLegacyUS = ""')
s = s.replace('MHPNSLegacyDE = "https://hpns-de.mattermost.com"', 'MHPNSLegacyDE = ""')
s = s.replace('MHPNSGlobal = "https://global.push.mattermost.com"', 'MHPNSGlobal = ""')
s = s.replace('MHPNSUS     = "https://us.push.mattermost.com"', 'MHPNSUS     = ""')
s = s.replace('MHPNSEU     = "https://eu.push.mattermost.com"', 'MHPNSEU     = ""')
s = s.replace('MHPNSAP     = "https://ap.push.mattermost.com"', 'MHPNSAP     = ""')

guard = (
    "func IsMHPNSEndpoint(url string) bool {\n"
    "\t// Honco Chat: the vendor-hosted push service is not used. The constants\n"
    "\t// below are blank, so an empty or custom endpoint must never match.\n"
    "\tif url == \"\" {\n\t\treturn false\n\t}\n"
    "\treturn slices.Contains([]string{"
)
if "Honco Chat: the vendor-hosted push service" not in s:
    s = s.replace(
        "func IsMHPNSEndpoint(url string) bool {\n\treturn slices.Contains([]string{",
        guard,
    )

if s == orig:
    print("no change (already patched?)")
else:
    open(p, "w").write(s)
    print("patched", p)

for line in open(p):
    if "MHPNS" in line and "=" in line and "func" not in line:
        sys.stdout.write("  " + line)
