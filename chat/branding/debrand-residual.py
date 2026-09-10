#!/usr/bin/env python3
"""Honco Chat: two vendor strings the standard de-brand pass and its own
strings-check missed on first rebuild. Idempotent.

  * channels/app/email/email.go -- SendEmailChangeVerifyEmail hardcodes
    "feedback@mattermost.com" as the support address. Every other email in
    the same file correctly reads *es.config().SupportSettings.SupportEmail
    (which debrand-support.py already points at HONCO_SUPPORT_EMAIL). This is
    an upstream inconsistency, not something debrand-support.py's config.go
    substitutions could catch -- it's a Go literal, not a config default.

  * channels/utils/textgeneration.go -- sample markdown text used by mmctl's
    bulk-data / load-test generator contains a literal mattermost.com link.
    Not a phone-home (no network call, it's just sample post content), but it
    fails the `strings` check in DEBRAND.md, so it belongs in the same pass.

Run this after debrand-webapp.py, as the last step.
"""
import os

ROOT = os.path.expanduser("~/honco-chat/server/server")

FIXES = [
    (
        "channels/app/email/email.go",
        'data.Props["SupportEmail"] = "feedback@mattermost.com"',
        'data.Props["SupportEmail"] = *es.config().SupportSettings.SupportEmail',
    ),
    (
        "channels/utils/textgeneration.go",
        "[Link to Mattermost](www.mattermost.com)",
        "[Link to Honco Chat](https://chat.honco.in)",
    ),
]

for rel, old, new in FIXES:
    p = os.path.join(ROOT, rel)
    if not os.path.exists(p):
        print("  SKIP (not found):", rel)
        continue
    s = open(p).read()
    if old in s:
        open(p, "w").write(s.replace(old, new))
        print("  fixed", rel)
    elif new in s:
        print("  already fixed", rel)
    else:
        print("  WARNING: expected string not found in", rel,
              "-- upstream may have changed this line, check manually")
