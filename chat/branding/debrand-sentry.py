#!/usr/bin/env python3
"""Honco Chat: remove the server-side crash reporter.

Upstream ships a hardcoded Sentry DSN pointing at the vendor's own Sentry
organisation, and wires both a client and an HTTP middleware behind
LogSettings.EnableDiagnostics + EnableSentry. It stays quiet only because the
service environment is 'dev'; a production build reports to the vendor.

This blanks the DSN, defaults EnableSentry off, and removes the middleware.
Idempotent.
"""
import os
import re

ROOT = os.path.expanduser("~/honco-chat/server")
P = os.path.join(ROOT, "server/channels/app/server.go")
C = os.path.join(ROOT, "server/public/model/config.go")

src = open(P).read()
orig = src

# 1. Blank the vendor DSN. sentry.Init with an empty DSN is a documented no-op
#    (the SDK disables itself), so every call site stays valid.
src = re.sub(
    r'var SentryDSN = "https://[^"]*"',
    'var SentryDSN = "" // Honco Chat: vendor crash reporting removed. '
    'An empty DSN makes the Sentry SDK a no-op.',
    src,
)

# 2. Never initialise the client, whatever the config says.
src = src.replace(
    "\tif *s.platform.Config().LogSettings.EnableDiagnostics && "
    "*s.platform.Config().LogSettings.EnableSentry {\n"
    "\t\tswitch model.GetServiceEnvironment() {",
    "\t// Honco Chat: crash reporting to an external service is removed.\n"
    "\tif false {\n"
    "\t\tswitch model.GetServiceEnvironment() {",
)

# 3. Drop the HTTP middleware that wraps every request.
src = src.replace(
    "\t\tif *s.platform.Config().LogSettings.EnableDiagnostics && "
    "*s.platform.Config().LogSettings.EnableSentry {\n"
    "\t\t\tsentryHandler := sentryhttp.New(sentryhttp.Options{\n"
    "\t\t\t\tRepanic: true,\n"
    "\t\t\t})\n"
    "\t\t\thandler = sentryHandler.Handle(handler)\n"
    "\t\t}",
    "\t\t// Honco Chat: the Sentry HTTP middleware is removed.",
)

# 3b. The middleware was the only user of the sentryhttp import; Go refuses to
#     compile an unused import, so it must go with it.
src = src.replace('\tsentryhttp "github.com/getsentry/sentry-go/http"\n', "")

if src != orig:
    open(P, "w").write(src)
    print("patched server.go")
else:
    print("server.go already patched")

# 4. Default the toggle off so the System Console shows it off too.
cfg = open(C).read()
new = re.sub(
    r'(if s\.EnableSentry == nil \{\n\t\ts\.EnableSentry = new)\(true\)',
    r'\1(false)',
    cfg,
)
if new != cfg:
    open(C, "w").write(new)
    print("EnableSentry now defaults to false")
else:
    print("EnableSentry default already false (or shape changed)")

print("verify:")
for i, line in enumerate(open(P), 1):
    if "SentryDSN =" in line or "sentryHandler" in line or "Honco Chat: crash reporting" in line:
        print("  %d: %s" % (i, line.rstrip()[:110]))
