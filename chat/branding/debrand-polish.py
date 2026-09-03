#!/usr/bin/env python3
"""Honco Chat: strip vendor doc/help URLs and the product name from user-visible
server strings.

Two things this must never touch, both learned the hard way:
  * Go template actions -- {{.MattermostUsername}} is an identifier, not prose.
    Renaming inside {{...}} makes the server refuse to boot with
    'function "ChatUsername" not defined'.
  * i18n message ids -- only the "translation" value is prose.
Idempotent.
"""
import json
import os
import re

ROOT = os.path.expanduser("~/honco-chat/server")
BRAND = "Honco Chat"

URL_RE = re.compile(r"https?://[a-z0-9.-]*mattermost[a-z0-9.-]*\.(?:com|io)[^\s\"'\)\],\\]*")
EMAIL_RE = re.compile(r"[a-z0-9._%+-]+@mattermost\.com")
TEMPLATE_RE = re.compile(r"\{\{.*?\}\}", re.S)

changed = []


def rebrand_prose(text):
    """Rewrite prose only, leaving every {{...}} template action untouched."""
    parts = []
    last = 0
    for m in TEMPLATE_RE.finditer(text):
        parts.append(("prose", text[last:m.start()]))
        parts.append(("tmpl", m.group(0)))
        last = m.end()
    parts.append(("prose", text[last:]))

    out = []
    for kind, chunk in parts:
        if kind == "tmpl":
            out.append(chunk)
            continue
        chunk = URL_RE.sub("", chunk)
        chunk = EMAIL_RE.sub("", chunk)
        chunk = chunk.replace("Mattermost", BRAND)
        out.append(chunk)
    return "".join(out)


def walk(node):
    """Rebrand only the 'translation' values of an i18n document."""
    if isinstance(node, list):
        return [walk(x) for x in node]
    if isinstance(node, dict):
        new = {}
        for k, v in node.items():
            if k == "translation" and isinstance(v, str):
                new[k] = rebrand_prose(v)
            elif k == "translation" and isinstance(v, dict):
                # plural forms: {"one": "...", "other": "..."}
                new[k] = {pk: (rebrand_prose(pv) if isinstance(pv, str) else pv)
                          for pk, pv in v.items()}
            else:
                new[k] = walk(v) if isinstance(v, (dict, list)) else v
        return new
    return node


# 1. Go source: drop vendor doc URLs from log/error strings.
go_targets = [
    "server/channels/app/server.go",
    "server/channels/app/bot.go",
    "server/channels/app/platform/license.go",
    "server/channels/app/email/email.go",
    "server/channels/web/handlers.go",
    "server/channels/web/unsupported_browser.go",
]
for rel in go_targets:
    p = os.path.join(ROOT, rel)
    if not os.path.exists(p):
        continue
    src = open(p).read()
    new = URL_RE.sub("", src)
    new = new.replace(" See documentation for details: ", " See documentation for details.")
    new = new.replace("accounts, see ", "accounts.")
    new = new.replace("License key from  required to unlock enterprise features.",
                      "Enterprise licence features are not available in this build.")
    new = new.replace(" Learn more about this setting in Mattermost docs at ", "")
    new = new.replace("Mattermost config", "%s config" % BRAND)
    if new != src:
        open(p, "w").write(new)
        changed.append(rel)

# 2 & 3. Translation catalogues, server and webapp.
for label, d in (("server/i18n", os.path.join(ROOT, "server/i18n")),
                 ("webapp/i18n", os.path.join(ROOT, "webapp/channels/src/i18n"))):
    if not os.path.isdir(d):
        continue
    for name in sorted(os.listdir(d)):
        if not name.endswith(".json"):
            continue
        p = os.path.join(d, name)
        raw = open(p, encoding="utf-8").read()
        try:
            doc = json.loads(raw)
        except ValueError as e:
            print("  SKIP (unparseable):", name, e)
            continue
        if isinstance(doc, dict) and "translation" not in json.dumps(doc)[:200]:
            # webapp catalogues are a flat {id: string} map
            new_doc = {k: (rebrand_prose(v) if isinstance(v, str) else v)
                       for k, v in doc.items()}
        else:
            new_doc = walk(doc)
        if new_doc != doc:
            with open(p, "w", encoding="utf-8") as f:
                json.dump(new_doc, f, ensure_ascii=False, indent=2)
                f.write("\n")
            changed.append("%s/%s" % (label, name))

print("files changed:", len(changed))
for c in changed[:8]:
    print("  ", c)
if len(changed) > 8:
    print("   ... and %d more" % (len(changed) - 8))

# Guard: no template action may contain the new brand name.
bad = 0
for label, d in (("server/i18n", os.path.join(ROOT, "server/i18n")),
                 ("webapp/i18n", os.path.join(ROOT, "webapp/channels/src/i18n"))):
    if not os.path.isdir(d):
        continue
    for name in sorted(os.listdir(d)):
        if not name.endswith(".json"):
            continue
        for m in TEMPLATE_RE.finditer(open(os.path.join(d, name), encoding="utf-8").read()):
            if BRAND in m.group(0):
                print("  BROKEN TEMPLATE in %s/%s: %s" % (label, name, m.group(0)[:60]))
                bad += 1
print("broken template actions:", bad)
