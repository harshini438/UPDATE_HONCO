#!/usr/bin/env python3
"""Honco Chat: finish removing the mobile crash reporter.

Dropping @sentry/react-native from package.json is not enough. Metro resolves
require() calls statically, so a guarded `require('@sentry/react-native')` in
log.ts still breaks the bundle once the package is gone. The stub must also
export every symbol the upstream module exported, or the app fails to compile.
Idempotent.
"""
import os
import re
import subprocess

M = os.path.expanduser("~/honco-chat/mobile")

STUB = '''// Honco Chat: crash reporting to a third-party service is not used.
// This module keeps the upstream call sites compiling as no-ops. It mirrors the
// full export surface of the module it replaces -- dropping any of these breaks
// the TypeScript build.

export const BREADCRUMB_UNCAUGHT_APP_ERROR = 'uncaught-app-error';
export const BREADCRUMB_UNCAUGHT_NON_ERROR = 'uncaught-non-error';

export function initializeSentry(): void {
    // no-op
}

export function captureException(error: unknown): void {
    if (error) {
        // eslint-disable-next-line no-console
        console.error('captureException', error);
    }
}

export function captureJSException(error: unknown, isFatal: boolean): void {
    if (error) {
        // eslint-disable-next-line no-console
        console.error('captureJSException', isFatal, error);
    }
}

export const addSentryContext = async (serverUrl: string): Promise<void> => {
    // no-op; kept async because callers await it
    return Promise.resolve();
};
'''

p = os.path.join(M, "app/utils/sentry.ts")
cur = open(p).read() if os.path.exists(p) else ""
if cur != STUB:
    open(p, "w").write(STUB)
    print("wrote full no-op stub app/utils/sentry.ts")
else:
    print("stub already complete")

# log.ts: drop the guarded require so Metro has nothing to resolve.
p = os.path.join(M, "app/utils/log.ts")
s = open(p).read()
orig = s
s = re.sub(
    r"const addBreadcrumb = \(logLevel: keyof typeof SentryLevels, \.\.\.args: any\[\]\) => \{.*?\n\};",
    "const addBreadcrumb = (logLevel: keyof typeof SentryLevels, ...args: any[]) => {\n"
    "    // Honco Chat: breadcrumbs went to a third-party crash reporter that is\n"
    "    // no longer bundled. Console output above is the whole log path now.\n"
    "};",
    s,
    flags=re.S,
)
if s != orig:
    open(p, "w").write(s)
    print("removed the @sentry/react-native require from log.ts")
else:
    print("log.ts already clean")

# The upstream tests assert on the reporter; they cannot pass without it.
for t in ("app/utils/sentry.test.ts", "app/utils/log.test.ts"):
    fp = os.path.join(M, t)
    if os.path.exists(fp):
        os.remove(fp)
        print("removed", t)

print("\nverify: any @sentry reference left in shipped code?")
r = subprocess.run(
    ["grep", "-rn", "@sentry/react-native", "app", "index.js"],
    cwd=M, capture_output=True, text=True,
)
print(r.stdout.strip() or "  none")

print("verify: exports of the stub vs what callers import")
r = subprocess.run(
    ["grep", "-rhoE", r"\b(initializeSentry|captureException|captureJSException|addSentryContext|BREADCRUMB_UNCAUGHT_APP_ERROR|BREADCRUMB_UNCAUGHT_NON_ERROR)\b",
     "app"],
    cwd=M, capture_output=True, text=True,
)
used = sorted(set(r.stdout.split()))
exported = set(re.findall(r"export (?:const|function) (\w+)", STUB))
print("  used by callers:", ", ".join(used))
missing = [u for u in used if u not in exported]
print("  missing from stub:", ", ".join(missing) if missing else "none")
