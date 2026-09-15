# Honco Chat — desktop and mobile client dependency

Verified 2026-09-15 by searching this repository, the sibling projects, the
Windows user tree and the WSL host. **No Honco desktop or mobile client source
exists in this environment**, so neither client can be built or tested here.
This document states exactly what is missing and who must supply it.

`CLIENTS.md` (2026-09-11) describes the backend surface a client consumes and
the two de-branding options. This document does not repeat it; it records the
dependency and the prerequisites. Where the two disagree, re-verify — nothing
here is taken on trust from the other.

## What was searched

| Searched for | Where | Result |
|---|---|---|
| Electron, Tauri, Capacitor, React Native, Expo, Flutter | every `package.json` outside `node_modules` in `C:\Users\Dell\Desktop`, `Documents`, `source`, and the WSL home | **none** |
| `pubspec.yaml`, `tauri.conf.json`, `capacitor.config.*`, `*.xcodeproj`, `*.xcworkspace`, `Podfile`, `electron-builder.*`, `metro.config.js`, `react-native.config.js` | same trees, depth 6 | **none found** |
| `android/`, `ios/` directories | same trees | **none** |
| Mattermost desktop or mobile source | `~/honco-chat/server` (the fork) and the whole WSL home | **not present** — the fork contains `server/`, `webapp/`, `api/`, `docs/`, `e2e-tests/`, `tools/` only |
| A mobile checkout for the de-brand scripts | `~/honco-chat/mobile`, `~/mobile`, `~/mattermost-mobile`, `~/honco-chat/desktop` | **all absent** |
| Client repo on the git remotes | `origin`, `update-honco` | see below |

## The one thing that could not be ruled out

`origin` is `https://github.com/honco-repo/honco-workspace.git` and is
**private**: `git ls-remote` fails with *"could not read Username for
https://github.com"*. Public GitHub is reachable from this machine (a control
`ls-remote` against `mattermost/desktop` succeeded), so this is an
authentication limit, not a network one.

**Therefore: I cannot prove a client source does not exist inside that private
repository.** Someone with credentials for it should check before any client
work is commissioned. `update-honco` was reachable and has only `main`.

## What exists today

`chat/branding/` holds the de-brand *recipe* for the upstream Mattermost mobile
app — `debrand-mobile.sh`, `debrand-mobile-sentry.py`, `debrand-firebase.py`,
`patch_push.py`. These are patches, not an application. `debrand-mobile.sh`
operates on `$HOME/honco-chat/mobile`, which does not exist; run today it fails
at `cd`. It writes `assets/override/config.json` with
`DefaultServerUrl=https://chat.honco.in`, app id `com.honco.chat`, and deep-link
schemes `honcochat://` / `honcoauth://`.

There is no equivalent recipe for desktop.

## Required desktop source

- **Upstream:** `mattermost/desktop` (Electron, Apache-2.0).
- **Server URL:** the public HTTPS hostname. It is **not available yet** —
  `SiteURL` is still `http://localhost:8065` and `chat.honco.in` does not
  resolve (see the Cloudflare tunnel dependency).
- **Why it is low-risk:** the desktop app renders the *server's own webapp*, so
  the Honco panel, custom post cards, search and admin dashboard work unchanged.
  No client-side Honco work is required for feature parity with the browser.
- **Decision needed:** ship upstream as-is (zero build work), or fork and
  de-brand. A fork needs code-signing identities for Windows and macOS
  installers — an organisational dependency, not a technical one.

## Required mobile source

- **Upstream:** `mattermost/mattermost-mobile` (React Native, Apache-2.0),
  cloned to `~/honco-chat/mobile` so the existing de-brand script applies.
- **What works without client work:** login, channels, DMs, threads, messages,
  files, profile photos, and Honco bot notifications (they are ordinary DMs).
  Meeting join links arrive as the card's fallback text and open in a browser.
- **What needs client work:** Tasks, Meeting Intelligence, Remote Support,
  Search and Admin are webapp-only React registered through the plugin
  registry. A native client renders nothing for them until those screens are
  built against the plugin REST routes. The two custom post types
  (`custom_honco_meeting`, `custom_honco_support`) need renderers or they show
  fallback text.

## Mattermost API / WebSocket compatibility required

Any client must speak stock Mattermost 11.11.0 APIs — no Honco-specific
transport exists or should be created:

- REST `/api/v4` with `Authorization: Bearer <session token>`.
- WebSocket `/api/v4/websocket`. **The server validates the `Origin` header
  against `SiteURL`**; a foreign origin is refused with 403. This was verified
  directly: a handshake with a mismatched origin returns 403, a matching one
  returns `101` plus a `hello` frame. A native client sending no browser origin
  is unaffected, but any web-hosted client must match `SiteURL`.
- Honco plugin routes under `/plugins/com.honco.workspace/api/v1` (tasks,
  `channels/{id}/meetings`, `channels/{id}/active-meetings`,
  `channels/{id}/recordings`, `meetings/{id}/summary`, `support/requests`,
  `search`, `admin/overview`). All require a valid Mattermost session and are
  rate limited per user.

## Push notifications — a separate deployment dependency

**Not configured, and must not be claimed.** Verified on the live server:
`SendPushNotifications = false` and `PushNotificationServer` is empty.
`patch_push.py` deliberately blanks the vendor-hosted HPNS endpoints, so the
fork will not fall back to Mattermost's hosted service.

To get push, someone must supply:

1. A **Mattermost Push Proxy** deployment (self-hosted is supported on Team
   Edition), reachable over HTTPS from this server.
2. **APNs** credentials (Apple developer account, push key) and **FCM**
   credentials for the de-branded app ids `com.honco.chat`.
3. `EmailSettings.SendPushNotifications = true` and `PushNotificationServer`
   set to the proxy URL.

Until then, mobile notifications arrive only while the app is open, over the
WebSocket. No fake push path has been added.

## Build prerequisites — none of which are present here

| Needed for | Tool | Status on this machine |
|---|---|---|
| Electron desktop | Node 18+, npm/yarn | Node 24 on Windows; **no `node` or `yarn` on the WSL PATH** |
| Android | JDK 17, Android SDK, Gradle, `adb` | **all absent**; `ANDROID_HOME` unset |
| iOS | Xcode, CocoaPods, macOS | **impossible here** — the host is Windows/WSL2 |
| Flutter / Tauri | `flutter`, `rustc`, `cargo` | **all absent** |

Installer code signing (Windows Authenticode, Apple Developer ID) is a further
organisational dependency for distributable builds.

## Exact owner / dependency needed before implementation

1. **Repository owner** — confirm whether a client source exists in the private
   `honco-repo/honco-workspace` remote. This is the only unresolved question.
2. **Deployment owner** — deliver the public HTTPS hostname
   (`https://chat.honco.in`) and set `SiteURL` to it. Both clients need a
   stable, reachable server URL; a desktop client pointed at `localhost:8065`
   only works on the server machine.
3. **Mobile owner** — an Android/iOS build environment plus Apple and Google
   developer accounts for the `com.honco.chat` ids.
4. **Push owner** — the push proxy and its APNs/FCM credentials.
5. **Product owner** — decide upstream-as-is versus a de-branded fork, since
   the fork adds code-signing and release obligations.

Until 1 and 2 are answered, no client work can be verified, and neither
**Desktop** nor **Mobile** may be marked done.

## What is true today instead

The existing web application is **responsive and usable on a phone**, verified
at iPhone 13 (390×664) and Pixel 5 (393×727) viewports with touch emulation
against the live server: login, channel history, sending a message via the send
button, live delivery over the WebSocket, and the Honco Workspace panel with
its Tasks and Meetings tabs all work, and the panel fits the phone width. At
phone width Enter inserts a newline and the send button submits — the standard
Mattermost mobile-web affordance, not a defect.

**This is responsive web, not a native mobile app, and must not be described as
one.**
