#!/usr/bin/env python3
"""Honco Chat: replace the vendor's Firebase config.

The upstream repo ships `android/app/google-services.json` for **Mattermost's
own Firebase project** (project_number 184930218130), registered for
com.mattermost.rn / com.mattermost.rnbeta. Shipping that would register our
users' FCM tokens against the vendor's project, and the Google Services Gradle
plugin fails the build outright once applicationId no longer matches:

    No matching client found for package name 'com.honco.chat'

This writes a structurally valid PLACEHOLDER for com.honco.chat so the build
completes and the toolchain can be proven end to end.

    PUSH NOTIFICATIONS DO NOT WORK WITH THE PLACEHOLDER.

To make them work, someone with Honco's Google account must create a Firebase
project, register the Android app as com.honco.chat, download the real
google-services.json over this file, and put the matching server key into the
push-proxy. iOS needs an APNs key from Honco's Apple developer account.

Idempotent. Keeps a copy of whatever it replaces, once.
"""
import json
import os
import shutil

M = os.path.expanduser("~/honco-chat/mobile")
P = os.path.join(M, "android/app/google-services.json")
PKG = "com.honco.chat"

MARKER = "PLACEHOLDER-NOT-A-REAL-FIREBASE-PROJECT"

placeholder = {
    "project_info": {
        "project_number": "000000000000",
        "project_id": "honco-chat-placeholder",
        "storage_bucket": "honco-chat-placeholder.appspot.com",
    },
    "client": [
        {
            "client_info": {
                "mobilesdk_app_id": "1:000000000000:android:0000000000000000000000",
                "android_client_info": {"package_name": PKG},
            },
            "oauth_client": [],
            "api_key": [{"current_key": MARKER}],
            "services": {"appinvite_service": {"other_platform_oauth_client": []}},
        }
    ],
    "configuration_version": "1",
}

if os.path.exists(P):
    try:
        cur = json.load(open(P))
    except ValueError:
        cur = {}
    keys = [
        c.get("client_info", {}).get("android_client_info", {}).get("package_name")
        for c in cur.get("client", [])
    ]
    if keys == [PKG]:
        print("  google-services.json already ours (%s)" % PKG)
        raise SystemExit(0)
    backup = P + ".vendor-original"
    if not os.path.exists(backup):
        shutil.copy2(P, backup)
        print("  kept the vendor original at %s" % os.path.basename(backup))
    print("  replacing vendor Firebase project %s (clients: %s)"
          % (cur.get("project_info", {}).get("project_number"), ", ".join(k or "?" for k in keys)))

with open(P, "w") as f:
    json.dump(placeholder, f, indent=2)
    f.write("\n")

print("  wrote placeholder google-services.json for %s" % PKG)
print("  NOTE: push notifications are dead until a real Firebase config replaces this.")
