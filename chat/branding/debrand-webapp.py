#!/usr/bin/env python3
"""Honco Chat: de-brand the web client's static shell.

The translated strings are already handled by debrand-polish.py and land in the
built bundle. What that pass cannot reach is root.html, which is a static
template -- the browser tab title and the PWA/app names come from here.

Deliberately NOT touched: anything matching @mattermost/* or mattermost-redux.
Those are package and import paths, not branding; rewriting them breaks the
build. Same reason we never rename the Go module paths.
Idempotent.
"""
import os
import re

W = os.path.expanduser("~/honco-chat/server/webapp")
BRAND = "Honco Chat"

p = os.path.join(W, "channels/src/root.html")
s = open(p).read()
orig = s

s = s.replace("<title>Mattermost</title>", "<title>%s</title>" % BRAND)
s = re.sub(
    r"<meta name='application-name' content='Mattermost'>",
    "<meta name='application-name' content='%s'>" % BRAND,
    s,
)
s = re.sub(
    r'<meta name="apple-mobile-web-app-title" content="Mattermost"\s*/?>',
    '<meta name="apple-mobile-web-app-title" content="%s" />' % BRAND,
    s,
)

if s != orig:
    open(p, "w").write(s)
    print("rebranded channels/src/root.html")
else:
    print("root.html already rebranded")

# The apple-mobile-web-app-title seen in the served page is injected by the
# server template, not root.html -- catch it there too.
T = os.path.expanduser("~/honco-chat/server/server/templates")
if os.path.isdir(T):
    for name in sorted(os.listdir(T)):
        fp = os.path.join(T, name)
        if not os.path.isfile(fp):
            continue
        try:
            body = open(fp, encoding="utf-8").read()
        except (UnicodeDecodeError, OSError):
            continue
        new = re.sub(
            r'(content=["\'])Mattermost(["\'])',
            lambda m: m.group(1) + BRAND + m.group(2),
            body,
        )
        new = new.replace("<title>Mattermost</title>", "<title>%s</title>" % BRAND)
        if new != body:
            open(fp, "w", encoding="utf-8").write(new)
            print("rebranded templates/" + name)

# Every other standalone .html template under channels/src. root.html is not the
# only one -- the no-script fallback lives in its own file and shows up in the
# served page. These are pure markup, no import paths, so a plain replace is safe.
for dirpath, _dirs, files in os.walk(os.path.join(W, "channels/src")):
    if "node_modules" in dirpath:
        continue
    for name in files:
        if not name.endswith(".html"):
            continue
        fp = os.path.join(dirpath, name)
        body = open(fp, encoding="utf-8").read()
        new = body.replace("Mattermost", BRAND)
        if new != body:
            open(fp, "w", encoding="utf-8").write(new)
            print("rebranded", os.path.relpath(fp, W))

# The PWA manifest block in webpack.config.js is what injects the
# <meta name="apple-mobile-web-app-title"> and application-name tags into the
# built root.html, and it writes manifest.json. root.html alone does not cover it.
wp = os.path.join(W, "channels/webpack.config.js")
if os.path.exists(wp):
    body = open(wp).read()
    before = body
    body = body.replace("            name: 'Mattermost',\n            short_name: 'Mattermost',",
                        "            name: '%s',\n            short_name: '%s'," % (BRAND, BRAND))
    body = body.replace(
        "            description: 'Mattermost is an open source, self-hosted Slack-alternative',",
        "            description: 'Honco internal chat, meetings and remote support',")
    if body != before:
        open(wp, "w").write(body)
        print("rebranded channels/webpack.config.js (PWA manifest)")
    else:
        print("webpack.config.js already rebranded")

# Hardcoded vendor URLs in the React source. These are not translations, so
# debrand-polish.py never sees them, and they end up as literals in the shipped
# JS bundle -- `strings dist/*.js` is what surfaces them. Tests and stories are
# skipped: they never ship, and some assert on the exact upstream string.
BASE = os.environ.get("HONCO_BASE", "https://chat.honco.in").rstrip("/")
VENDOR_URL = re.compile(r"https://[a-z0-9.-]*mattermost\.(?:com|io)[^\"'\s)]*")


def replacement(m):
    u = m.group(0)
    if "download" in u:
        return BASE + "/apps"
    return BASE + "/help"


skip = (".test.ts", ".test.tsx", ".stories.tsx", ".stories.ts")
patched = 0
for dirpath, dirs, files in os.walk(os.path.join(W, "channels/src")):
    dirs[:] = [d for d in dirs if d not in ("node_modules", "__snapshots__")]
    for name in files:
        if not name.endswith((".ts", ".tsx")) or name.endswith(skip):
            continue
        fp = os.path.join(dirpath, name)
        body = open(fp, encoding="utf-8").read()
        if "mattermost.com" not in body and "mattermost.io" not in body:
            continue
        new = VENDOR_URL.sub(replacement, body)
        if new != body:
            open(fp, "w", encoding="utf-8").write(new)
            patched += 1
print("rewrote vendor URLs in %d shipped source files" % patched)

print("\nverify: remaining literal 'Mattermost' in the static shell")
for f in [os.path.join(W, "channels/src/root.html")]:
    hits = [ln.strip() for ln in open(f) if "Mattermost" in ln]
    print("  %s: %s" % (os.path.basename(f), hits or "none"))
