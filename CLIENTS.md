# Honco Chat — Desktop and Mobile clients

Status on 2026-09-11: **no Honco desktop or mobile application source exists** in
this repository or on the reference host. What exists is the upstream de-brand
recipe for the Mattermost mobile app (`chat/branding/debrand-mobile.sh`,
`debrand-mobile-sentry.py`) and nothing for desktop. This document is the plan
and the verified backend facts a client team needs; it does not claim a client.

## What the backend already provides (verified)

Every Honco feature is served by two things a native client can consume without
change:

| Surface | Transport | Verified by |
|---|---|---|
| Authentication, sessions, password reset | Mattermost REST `/api/v4` (bearer token or cookie) | Auth 28/28 API, 20/20 browser |
| Messages, channels, files | Mattermost REST + WebSocket `/api/v4/websocket` (token auth; foreign `Origin` refused) | Files 56/56, chat suites |
| Tasks | `GET/POST/PATCH/PUT/DELETE /plugins/com.honco.workspace/api/v1/tasks…` | 43/43 + 20/20 security |
| Meetings, recordings, summaries | `…/api/v1/channels/{id}/meetings`, `…/meetings/{id}`, `…/meetings/{id}/summary`, files via `/api/v4/files/{id}` | 17/17, 16/16, 23/23 |
| Remote support | `…/api/v1/support/requests…` | 43/43 |
| Search | `…/api/v1/search?q=&type=&page=&limit=` | 27/27 + 30/30 + 8/8 |
| Admin | `…/api/v1/admin/overview`, `…/admin/health` (system admin only) | 37/37 |
| Live updates | plugin WebSocket events `custom_com.honco.workspace_meeting_updated`, `…_support_updated`, scoped to the channel | card suites |

All plugin routes require a Mattermost session (`Mattermost-User-Id` is set by the
server only after validating it), enforce CSRF for cookie sessions, return JSON
with `nosniff`/`no-store`, and are rate limited per user. A native client should
use a **bearer token** (`Authorization: Bearer <session token>`), which is what
every API test in this project does; no CSRF token is then needed.

What a native client will **not** get from the server:

- The Honco right-hand panel (Tasks / Meeting Intelligence / Support / Search /
  Admin) is webapp-only React registered through the plugin registry. A native
  client renders nothing for it unless it implements those screens against the
  routes above.
- Meeting and support **cards** are custom post types (`custom_honco_meeting`,
  `custom_honco_support`). A client that does not register a renderer sees the
  card's fallback text (`Message`), which the plugin writes for exactly this case
  — "Meeting: <topic> — Join: <url>", etc. Join links are plain URLs and work.
- Recording playback is a normal file download (`/api/v4/files/{id}`), which both
  upstream clients already handle.

## Desktop — plan (PARTIAL: no source)

The Mattermost desktop app (Electron, `mattermost/desktop`) connects to any
Mattermost server by URL and renders the **server's webapp**, including plugins.
That means the Honco panel, cards, search tab and admin dashboard work in it
unchanged — it is the same webapp bundle. Two choices:

1. **Use upstream desktop as-is**, configured with the server URL
   (`https://chat.honco.in`). Zero build work; branding is Mattermost's in the
   window chrome only. Everything Honco works today.
2. **Fork and de-brand** (`mattermost/desktop`, Apache-2.0): rename, icon,
   default server, disable auto-update/telemetry — the same pattern as
   `debrand-mobile.sh`. Build needs Node 18+, electron-builder, and code-signing
   certificates for Windows/macOS installers.

Backend requirements: none beyond what exists. `SiteURL` must be the public
hostname (websockets), and TLS must terminate in front of `:8065` (§10 of
OPERATIONS.md). Recommended: option 1 now, option 2 when a signing identity exists.

## Mobile — plan (PARTIAL: no source, push not configured)

The intended path (README.md) is a de-branded fork of `mattermost/mattermost-mobile`
(React Native, Apache-2.0): clone to `~/honco-chat/mobile`, run
`debrand-mobile.sh` (sets `DefaultServerUrl`, app ids `com.honco.chat`, deep-link
schemes `honcochat://` / `honcoauth://`) and `debrand-mobile-sentry.py`. Building
needs Android SDK / Xcode, which the reference host does not have.

What works on day one with an upstream or de-branded mobile client: login, chat,
channels, files, recordings (as files), meeting **join** (fallback text carries the
URL; it opens in the browser or the Jitsi app), notifications delivered as DMs from
the honco bot (they are ordinary posts). What needs client work: Tasks, Meeting
Intelligence, Support and Search screens against the plugin API; custom post
renderers for the two card types.

**Push notifications are not configured and must not be claimed.**
`EmailSettings.PushNotificationServer` is unset. Mattermost mobile push requires the
Mattermost Push Proxy (or the hosted HPNS) with APNs/FCM credentials for the
de-branded app ids; Team Edition can run a self-hosted push proxy. Until that
exists, mobile notifications arrive only while the app is open (WebSocket).

## Verification standard

Neither client is DONE until a real device or desktop build has logged in,
posted, joined a meeting and opened a recording against this server. A plan is
not that, and this document does not claim it.
