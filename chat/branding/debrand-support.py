#!/usr/bin/env python3
"""Honco Chat: point the Support / About / Help / download links at Honco's own
URLs instead of the vendor's, and rename the CLI descriptions. Idempotent.

Change BASE (or set HONCO_BASE) when the real intranet host is decided --
everything below derives from it.
"""
import os
import re

ROOT = os.path.expanduser("~/honco-chat/server")
BRAND = "Honco Chat"
BASE = os.environ.get("HONCO_BASE", "https://chat.honco.in").rstrip("/")
SUPPORT_EMAIL = os.environ.get("HONCO_SUPPORT_EMAIL", "it@honco.in")

LINKS = {
    "SupportSettingsDefaultTermsOfServiceLink": BASE + "/help/terms",
    "SupportSettingsDefaultPrivacyPolicyLink":  BASE + "/help/privacy",
    "SupportSettingsDefaultAboutLink":          BASE + "/help/about",
    "SupportSettingsDefaultHelpLink":           BASE + "/help",
    "SupportSettingsDefaultReportAProblemLink": BASE + "/help/report-a-problem",
    "SupportSettingsDefaultSupportEmail":       SUPPORT_EMAIL,
    "NativeappSettingsDefaultAppDownloadLink":        BASE + "/apps",
    "NativeappSettingsDefaultAndroidAppDownloadLink": BASE + "/apps/android",
    "NativeappSettingsDefaultIosAppDownloadLink":     BASE + "/apps/ios",
}

C = os.path.join(ROOT, "server/public/model/config.go")
src = open(C).read()
orig = src

for const, value in LINKS.items():
    # matches both the original vendor URL and a previously-blanked value
    src = re.sub(
        r'(\b%s\s*=\s*)"[^"]*"' % re.escape(const),
        lambda m, v=value: m.group(1) + '"%s"' % v,
        src,
    )

if src != orig:
    open(C, "w").write(src)
    print("support/download links now point at", BASE)
else:
    print("support links already set")

# CLI long descriptions.
for rel in ("server/cmd/mattermost/commands/root.go", "server/cmd/mmctl/commands/root.go"):
    p = os.path.join(ROOT, rel)
    if not os.path.exists(p):
        continue
    s = open(p).read()
    n = re.sub(
        r'`Mattermost offers workplace messaging[^`]*`',
        '`%s -- self-hosted workplace messaging for Honco. Docs: %s/help`' % (BRAND, BASE),
        s,
    )
    if n != s:
        open(p, "w").write(n)
        print("rebranded", rel)

# The CLI's own help text: the cobra command name and every Short/Long string.
# `Use: "mattermost"` is what prints as `Usage: mattermost [command]`.
cli_dir = os.path.join(ROOT, "server/cmd/mattermost/commands")
if os.path.isdir(cli_dir):
    for name in sorted(os.listdir(cli_dir)):
        if not name.endswith(".go") or name.endswith("_test.go"):
            continue
        fp = os.path.join(cli_dir, name)
        body = open(fp).read()
        new = body.replace('Use:   "mattermost"', 'Use:   "honcochat"')
        # Only prose in help strings and log lines -- never an import path.
        new = re.sub(r'("(?:[^"\\]|\\.)*?)\bMattermost\b', lambda m: m.group(1) + BRAND, new)
        if new != body:
            open(fp, "w").write(new)
            print("rebranded cmd/mattermost/commands/" + name)

p = os.path.join(ROOT, "server/cmd/mmctl/commands/sampledata_util.go")
if os.path.exists(p):
    s = open(p).read()
    n = s.replace("sample.mattermost.com", "sample.honco.local")
    if n != s:
        open(p, "w").write(n)
        print("rebranded sampledata emails")

print("verify:")
for line in open(C):
    if re.search(r"(SupportSettingsDefault(TermsOfService|PrivacyPolicy|About|Help|ReportAProblemLink|SupportEmail)|NativeappSettingsDefault\w*DownloadLink)\s*=", line):
        print("  " + line.rstrip())
