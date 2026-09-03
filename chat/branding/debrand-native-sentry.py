#!/usr/bin/env python3
"""Honco Chat: remove the crash reporter from the mobile app's NATIVE code.

Dropping @sentry/react-native from package.json and stubbing the TS wrapper is
only two of four layers. The Android and iOS projects link the native SDK
directly, so the build fails with:

    e: MainApplication.kt:23 Unresolved reference 'sentry'
    e: MainApplication.kt:56 Unresolved reference 'RNSentrySDK'

Android is verified by building. **iOS is edited but NOT verified** -- there is
no macOS here, so the Xcode/CocoaPods side needs a build on a Mac before anyone
trusts it.

Idempotent.
"""
import os
import re

M = os.path.expanduser("~/honco-chat/mobile")
changed = []


def edit(rel, fn):
    p = os.path.join(M, rel)
    if not os.path.exists(p):
        return
    src = open(p, encoding="utf-8").read()
    new = fn(src)
    if new != src:
        open(p, "w", encoding="utf-8").write(new)
        changed.append(rel)


# --- Android -----------------------------------------------------------------
def android_main(s):
    s = s.replace("import io.sentry.react.RNSentrySDK\n", "")
    s = s.replace(
        "        // Initialize Sentry early for native crash reporting\n"
        "        RNSentrySDK.init(this)\n",
        "        // Honco Chat: native crash reporting to a third-party service is removed.\n",
    )
    return s


edit("android/app/src/main/java/com/mattermost/rnbeta/MainApplication.kt", android_main)


def android_gradle(s):
    # The whole `if (System.getenv("SENTRY_ENABLED") == "true") { ... }` block,
    # which applies node_modules/@sentry/react-native/sentry.gradle -- a path
    # that no longer exists.
    return re.sub(
        r'\nif \(System\.getenv\("SENTRY_ENABLED"\) == "true"\) \{.*?\n\}\n',
        "\n// Honco Chat: the Sentry gradle integration is removed along with the SDK.\n",
        s,
        flags=re.S,
    )


edit("android/app/build.gradle", android_gradle)

# --- iOS (edited, not verified) ----------------------------------------------
SHIM = '''//  Honco Chat: crash reporting to a third-party service is removed.
//  This file keeps the upstream call sites compiling as no-ops.
//
//  NOT VERIFIED: edited without a macOS build. Compile on a Mac before trusting.

import Foundation

func initSentryAppExt() {
    // no-op
}

func testSentry(msg: String) {
    // no-op
}
'''

p = os.path.join(M, "ios/ErrorReporting/Sentry.swift")
if os.path.exists(p) and "Honco Chat: crash reporting" not in open(p).read():
    open(p, "w").write(SHIM)
    changed.append("ios/ErrorReporting/Sentry.swift")


def strip_swift_sentry(s):
    s = re.sub(r"^\s*import Sentry\s*$\n", "", s, flags=re.M)
    # Direct SDK calls that the shim does not cover.
    s = re.sub(r"^\s*SentrySDK\.[^\n]*\n", "", s, flags=re.M)
    return s


for rel in (
    "ios/Mattermost/AppDelegate.swift",
    "ios/NotificationService/NotificationService.swift",
    "ios/MattermostShare/ShareViewController.swift",
):
    edit(rel, strip_swift_sentry)


def strip_podfile(s):
    return re.sub(r"^.*[Ss]entry.*$\n", "", s, flags=re.M)


edit("ios/Podfile", strip_podfile)

# The AppDelegate reads sentry.options.json at launch and calls RNSentrySDK.start().
# strip_swift_sentry does not catch this because the call is inside an if-let chain.
def appdelegate_block(s):
    return re.sub(
        r"[ \t]*// Initialize Sentry early for native crash reporting[^\n]*\n"
        r"(?:[ \t]*(?:if let|[ \t]*let|RNSentrySDK\.start\(\)|\})[^\n]*\n)+",
        "        // Honco Chat: native crash reporting to a third-party service is removed.\n",
        s,
    )


edit("ios/Mattermost/AppDelegate.swift", appdelegate_block)

# The options file itself: checked in at the repo root and, more importantly, as
# an Android asset that ships inside the APK. Nothing reads it any more.
for rel in ("sentry.options.json", "android/app/src/main/assets/sentry.options.json"):
    fp = os.path.join(M, rel)
    if os.path.exists(fp):
        os.remove(fp)
        changed.append("removed " + rel)

print("files changed: %d" % len(changed))
for c in changed:
    print("  ", c)

print("\nverify (android):")
for rel in ("android/app/src/main/java/com/mattermost/rnbeta/MainApplication.kt",
            "android/app/build.gradle"):
    p = os.path.join(M, rel)
    hits = [l.strip() for l in open(p) if "sentry" in l.lower()]
    print("  %-58s %s" % (os.path.basename(rel), hits or "clean"))
