# Honco Workspace — Upgrade Plan

**Status:** Living document. Phase 1 and Phase 2 implemented; Authentication & Session Management (Phase 11's core, done ahead of schedule as an explicit gate) implemented — see its own section between Phase 2 and Phase 3. Phases 3–10, 12, 13 are scoped but not yet built. Each phase gets its own implementation pass, tested and reported before the next one starts — this file gets updated as that happens, not written once and left stale.

**Re-audited 2026-09-09** against the running system. Three things changed since this was last written: Jibri **is** deployed and has recorded real meetings (the table below said otherwise), RustDesk has now had its inspection pass, and **speech-to-text is out of scope by decision**, which changes what Phase 4 can summarise. See *Audit — 2026-09-09* below for the findings and the four decisions taken. Nothing in the running system was modified to produce that audit.

**Read this first if you're picking this up fresh:** the single biggest finding of this plan is that a large fraction of the originally-requested feature list (Phase 1's entire scope, most of Phase 6, half of Phase 8) **already exists** in the Mattermost fork this project is built on. Mattermost is a 10+ year mature product; chat, search, threads, reactions, file previews, presence, and notifications are not gaps — they're the foundation. The real work is the Honco-specific layer on top: `/meet` integration, transcription, AI summaries, tasks, and the admin/branding polish that makes it feel like one product instead of "Mattermost plus some scripts." This plan is written with that distinction explicit in every phase, so nothing gets rebuilt that already works.

---

## Current Architecture (as verified this session, not assumed)

```
┌─────────────────────────────────────────────────────────────────┐
│  Honco Chat (Mattermost fork)                                    │
│  ├─ Go server   ~/honco-chat/build/honcochat  (:8065)            │
│  ├─ React/TS webapp  server/webapp/channels/src  (362 components)│
│  ├─ PostgreSQL  127.0.0.1:5433 / db "honcochat"                  │
│  └─ config.json (file-based config, no MM_* env overrides live)  │
│         │                                                        │
│         │ /meet slash command (registered per-team via mmctl)    │
│         ▼                                                        │
│  meetsvc.py (:8077) ── bot account "honco-meet" ── Bots table    │
│         │                                                        │
│         │ generates room links                                  │
│         ▼                                                        │
│  Local Jitsi (docker-jitsi-meet, :8443) ── web/prosody/jicofo/jvb│
│         │                                                        │
│         │ Jibri ✅ DEPLOYED — API :2222, 5 recordings on disk    │
│         │   ⚠ finalize.sh currently posts to V2 (:8081), NOT here│
│         ▼                                                        │
│  transcribe/ (Whisper) 🚫 OUT OF SCOPE ── summarize/ (Claude CLI)│
│                              needs a new input: channel messages │
└─────────────────────────────────────────────────────────────────┘
```

**Repo layout convention** (confirmed, matters for every phase below): the git repo (`honco-workspace`) holds only Honco-specific *scripts* (`chat/`, `meet/`, `transcribe/`, `summarize/`). The actual vendor source (Mattermost, Jitsi) and all runtime state live in **sibling directories outside the repo** (`~/honco-chat/`, `~/honco-meet/`), cloned/built by those scripts. Any change to vendor behavior goes through a de-brand/build script, never a direct edit to a vendor file that a re-clone would silently discard.

---

## Existing Features (verified by source/component inspection, not assumed)

| Area | Status | Evidence |
|---|---|---|
| Message composer: file/image upload, emoji, mentions, reply/threads, edit/delete, attachment preview | ✅ **Already exists** | `components/advanced_text_editor`, `file_upload`, `emoji_picker`, `at_mention`, `post_edited_indicator`, `threading`/`rhs_thread` |
| Message search (text, by user, by channel, by date) | ✅ **Already exists** | `components/search*` (8 components: `search`, `search_bar`, `search_results`, `new_search`...) — Mattermost's search bar natively supports `from:`, `in:`, `on:`, `before:`, `after:` modifiers |
| Pinned messages | ✅ **Already exists** | Folded into `channel_info_rhs` (right-hand panel), not a standalone component — confirmed present in Mattermost's post context menu ("Pin to Channel") |
| Reactions | ✅ **Already exists** | `reaction_limit_reached_modal` confirms the reaction system is live (the modal only exists to warn when it's used too much) |
| Presence (online/away/offline/DND) | ✅ **Already exists** | `custom_status`, `status_icon`, `status_icon_new` |
| Notifications (mentions, DMs, thread replies) | ✅ **Already exists** | `notification_box`, `notify_counts`, `channel_notifications_modal`; desktop notifications and email digests are native Mattermost settings |
| `/meet` slash command, instant + scheduled rooms | ✅ **Working** (built this session) | `chat/meetsvc.py`, verified end-to-end via the real command-execution API |
| Meeting reminders posted to channel | ⚠️ **Exists, but doesn't notify** | `meetsvc.py`'s `scheduler()` posts a plain message — no `@channel`/mention, so it doesn't trigger Mattermost's notification/unread system. **Fixed in this phase, see below.** |
| Local Jitsi video meetings | ✅ **Working** (deployed this session, core services only) | `web`/`prosody`/`jicofo`/`jvb` running, `MEET_BASE=https://localhost:8443` |
| Jibri (recording) | ✅ **Deployed and producing recordings** *(corrected 2026-09-09; this row previously read "Not deployed")* | `honco-meet-jibri-1` running; HTTP API on `127.0.0.1:2222`; `/storage` mounted to `~/.jitsi-meet-cfg/storage/jibri`; **5 real recordings** on disk with `metadata.json`; `finalize.log` shows successful completion callbacks. **But every one of them was produced by Honco_Workspace_V2, not by `/meet`** — see *Audit — 2026-09-09* |
| Transcription pipeline (Whisper) | 🚫 **Out of scope by decision (2026-09-09)** | Engine exists and is real (`~/honco-stt/bin/stt-whisper.sh` + `models/ggml-base.en.bin`), but `~/honco-transcribe` — the ROOT `transcribe/worker.sh` requires — does not exist, so that worker has never run here. Explicitly excluded from the current round of work |
| AI summarization (Claude CLI) | ⚠️ **Code exists, untested end-to-end, and now needs a new input** | `summarize/honco-summarize.sh` present, runs on `mother` behind a forced-command SSH key. With STT out of scope there is no transcript to feed it — Phase 4 is re-scoped to summarise **channel conversation** instead. Claude credentials are **not on this host**; `mother` reachability from here is unverified |
| SMTP / Forgot Password | ✅ **Working** (fixed this session) | Gmail SMTP configured, verified via `TestEmail` |
| Bot account / tokens | ✅ **Working** | `honco-meet` bot, restored and re-enabled this session |
| RustDesk remote support | ❌ **Inspected 2026-09-09 — nothing on this host** | No `rustdesk`/`hbbs`/`hbbr`/port reference in any script. `README.md` states the deliberate position that RustDesk stays a separate app. Verified constraint: the **OSS** server exposes no HTTP API (console/REST are Server **Pro**), and the device ID is not an authentication mechanism — so Honco can track the *workflow* but cannot open, query or close a session |
| Task management | ❌ **Does not exist** | No task-related tables, models, or components anywhere in the codebase |
| AI chat assistant (`/summarize`, `/action-items`) | ❌ **Does not exist** | No slash command registered beyond `/meet`; would follow the exact same pattern (bot + slash command + backend service) already proven working |
| Admin dashboard (Honco-specific) | ⚠️ **Partially exists** | Mattermost's own System Console already covers users/channels/roles/permissions/service health in a mature, tested UI. A *separate* Honco-branded dashboard for meetings/transcripts/tasks does not exist |
| Desktop app | ❌ **Does not exist** | Explicitly deferred per your own instructions (Phase 13, "only after the web application is stable") |

---


---

## Audit — 2026-09-09

A full re-inspection of the running system against the 12-feature target list.
Read-only: no code, database, configuration or server state was touched. The
server was up throughout (`/api/v4/system/ping` → 200, ~12h uptime).

### What the fork actually is

The Honco fork carries **no commits**. Its entire delta against
`github.com/mattermost/mattermost.git@240b9bed` is **289 uncommitted
working-tree changes**, and every category was checked:

| Category | Count | What it is |
|---|---|---|
| webapp `.tsx` | 158 | Pure link/string rebrand — e.g. `mattermost.com/community` → `chat.honco.in/help` |
| Go | 18 | Telemetry removal + session hardening (+18 lines across `login.go`, `user.go`) |
| Deletions | ~110 | `server/enterprise/` (now just `placeholder.go`), elasticsearch, compliance export, non-free licences |
| Untracked | 3 | Config backups only |

**There is zero Honco feature code inside the Mattermost tree.** Every Honco
capability lives outside it: `meetsvc.py`, `transcribe/`, `summarize/`,
`meet/`. That is the de-brand model working as designed, and it is the reason
new features belong in a **plugin** rather than in `channels/`.

### Finding 1 — every existing recording came from V2, not from `/meet`

Room naming proves it:

| Source | Scheme | Example |
|---|---|---|
| V2 `meeting_service.go:74` | `"honco-" + hex(16 random bytes)` → 32 hex chars | `honco-21dea015ef20176c1da90cc1bfbecaa6` |
| `meetsvc.py:room_url()` | `<slug>-<6 hex>` | `standup-a3f9c1` |

All five recordings on disk match **V2's** scheme. The Jibri path has never
been exercised from `/meet`, so "complete the recording integration" is not
greenfield work — it is **re-pointing proven infrastructure from V2 to
Mattermost**.

### Finding 2 — the callback contract is fully recovered

From V2's receiving handler, corroborated by `finalize.log`:

```
POST /api/v1/internal/recordings/callback
Header: X-Jibri-Callback-Secret: <shared secret>
multipart/form-data, streamed, in this order:
  room_name, status, error_message, duration_seconds, then file (LAST)
→ 200 {"data":{"message":"recording completion accepted"}}
```

The field ordering is not incidental: the receiver streams rather than
buffering, so metadata must arrive before the file. Any replacement endpoint
must honour the same ordering.

### Finding 3 — the room→channel mapping exists, but not where we can query it

`meetsvc.py` already stores `{id, topic, channel_id, user, url, at, status}`
for both instant (`record_history`) and scheduled meetings, and the room name
is derivable from `url`. But it lives in `~/honco-chat/run/*.json`, **not in
Postgres**, so nothing inside Mattermost can query it. Connecting recordings
to channels needs a registry the plugin can read — cheapest path is ~15 lines
in `meetsvc.py` registering each room with the plugin at creation, which keeps
the working `/meet` flow untouched.

### Finding 4 — plugin auth cannot be spoofed

`channels/app/plugin_requests.go:193` does `r.Header.Del("Mattermost-User-Id")`
on every inbound request and re-sets it only from a validated session (`:283`).
So header present ⇒ authenticated user, header absent ⇒ anonymous. That is the
authorization foundation for every plugin route — and it is exactly why the
Jibri callback, which carries no session, needs its own shared-secret check.

### Verified configuration facts

Read from the live config (named non-secret keys only):

| Setting | Value | Consequence |
|---|---|---|
| `EnableFileAttachments` | `true`, 100 MB, local driver | Files & attachments already complete — nothing to build |
| `EnablePublicLink` | `false` | Correct security default; keep |
| `SendPushNotifications` / `PushNotificationServer` | `false` / `""` | Mobile push unconfigured |
| `EnableEmailBatching` | `false` | Email digests off |
| `EnableIndexing`/`EnableSearching`/`EnableAutocomplete` | `false` | With `enterprise/` gone, search runs on **Postgres full-text** via `searchlayer` → sqlstore. Do **not** reintroduce Elasticsearch — non-free and deliberately removed |
| `PluginSettings.Enable` | `true` | Plugin path is available |
| `PluginSettings.EnableUploads` | `false` | **Blocks installing a plugin via the UI** — needs a bundle drop + restart, or a config change |
| `PluginStates` | 4 plugins listed | None are installed; harmless (absent plugins don't load), but misleading |
| `ServiceSettings.SiteURL` | *empty* | Injected per tunnel start — brittle; emailed links break when the quick tunnel gets a new hostname |

Database: `honcochat` on 5433, **85 tables, 215 migrations**, no task/todo/
checklist tables, no plugin KV rows. `plugins/` and `data/` are both empty.

### Decisions taken (2026-09-09)

1. **Meeting Intelligence input** → summarise the **channel conversation**
   around a meeting, not the audio. Deliverable now, no out-of-scope work.
2. **V2 recording cutover** → **leave `finalize.sh` alone for now.** Build and
   test the plugin's callback endpoint without touching the live hook;
   recording stays unverified end-to-end until the switch is approved.
3. **Server restart** → **not yet.** A window will be nominated. Until then no
   plugin can actually be loaded, so plugin work is build-and-unit-test only.
4. **Task Management scope** → **team-scoped.** A task belongs to a Mattermost
   Team and is assignable to members of that Team.

### Open dependency

Claude credentials are **not on this host** — `summarize/` runs on `mother`
behind a forced-command SSH key. Reachability from this box is **unverified**.
Phases 4 and 5 depend on it and should not be scheduled until it is confirmed.

### Operational note

WSL's exec channel timed out intermittently (`Wsl/Service/0x8007274c`)
throughout this audit; it was completed via UNC file access instead.
`wsl --shutdown` was deliberately **not** run, since it would stop the chat
server, Postgres and the Jitsi stack. This needs attention before
implementation work, which requires running builds and tests.

## Proposed Improvements Per Phase — Honest Scope

For each phase: what's genuinely missing, what the smallest safe implementation looks like, and what NOT to build.

### Phase 1 — Honco Chat (composer/search/pins/presence/notifications)
**Missing:** Nothing in the original list. **Real gap found:** meeting reminders don't notify.
**Do:** Make `meetsvc.py`'s reminder message actually trigger notifications (channel-wide mention). Zero new components, zero webapp rebuild needed.
**Do NOT:** Rebuild composer/search/pins/reactions/presence/notifications — they exist, are mature, and duplicating them would violate your own "avoid duplicate implementations" rule and introduce real regression risk into 362 already-working components.
**Files:** `chat/meetsvc.py` only.

### Phase 2 — Honco Meet UI — ✅ IMPLEMENTED (this session)

**Architectural decision, made explicit before building anything:** a full Mattermost **plugin** (Go + React, its own build pipeline, persistent channel-header button, RHS panel) is what "feels fully native" ultimately wants, but building, compiling, deploying, and *properly verifying* one is realistically multi-day work, not a same-session addition — and shipping one half-tested would violate "do not fake it." Your own instructions explicitly sanction a lighter path when full embedding isn't currently appropriate ("create a polished meeting panel/modal", "implement the smallest safe integration"). Mattermost has an official, zero-plugin mechanism for exactly this: **Interactive Message Actions** (buttons on a bot post) and **Interactive Dialogs** (a real modal form, opened via `trigger_id`). Phase 2 is built on that. The full plugin (persistent header button, dedicated RHS panel, richer participant picker) remains a real, larger follow-on — not done here, and not pretended to be.

**What Phase 2 already had:** all six text-based `/meet` commands (`/meet`, `/meet <name>`, `/meet in Nm|h <name>`, `/meet at HH:MM <name>`, `/meet list`, `/meet cancel <id>`), scheduling persisted to `meet-schedule.json`, the Phase 1 `@channel` reminder notification.

**What was missing:** any clickable UI at all (every interaction was typed text), a schedule *form*, any historical record (fired/cancelled items were simply discarded), any status concept beyond "pending or gone", and a one-click way to copy a meeting link.

**What was implemented, mapped to your 8 numbered requirements:**

| # | Requirement | Implementation |
|---|---|---|
| 1 | Start Meeting button | `/meet schedule` opens a real modal ("Start" is the zero-time-field case) — see limitation below on a persistent header button |
| 2 | Join Meeting, clear info | Unchanged (already worked) — the markdown link in every response is already a native one-click join |
| 3 | Schedule Meeting UI | `/meet schedule` → **Interactive Dialog**: Meeting name, When (text, same syntax as typed `/meet`). Channel is implicit (the invoking channel, matching existing `/meet` behavior exactly) |
| 4 | Reminders preserved | Untouched — Phase 1's `@channel` notification code path is unmodified; regression-tested below |
| 5 | Meeting history | New `/meet history` command, backed by a new `meet-history.json` log (capped at 200 entries) — records every instant/scheduled/cancelled/failed meeting, which previously vanished on completion |
| 6 | Copy Meeting Link | **Copy Link** message-action button on every `/meet` response (instant, scheduled, and dialog-submitted) — replies with the URL in a fenced code block, which Mattermost's own message renderer already puts a copy-icon on, so this reuses an existing UI affordance rather than inventing one |
| 7 | Meeting status | Honestly scoped: `scheduled`, `reminder_sent`, `cancelled`, `failed` are real and shown. `starting`/`in_progress`/`completed` are **not implemented** — meetsvc has no channel back from Jitsi at all (no webhook, no room-occupancy API call), so faking those states would mean displaying data that doesn't exist. Documented as a known limitation, not silently dropped. |
| 8 | UI integration | Chat-native (dialogs/buttons render inside Mattermost's own UI chrome, already Honco-branded from earlier phases of this project) — but not a persistent header button/RHS panel; that needs the full plugin |

**A real bug found and fixed during testing, not left in:** the dialog's separate "Name"/"When" fields don't combine the way `parse_when()`'s regex expects (`"in 4m"` alone doesn't match — it requires trailing text after the time expression, by original design for the single-string slash-command syntax). A bare `when="in 4m"` was silently becoming an *instant* meeting literally named "in 4m" instead of a scheduled one. Fixed by recombining the two dialog fields into the same combined syntax the text command already uses, rather than changing the regex itself (which is still used, unmodified, by the original text path).

**Files changed:** `chat/meetsvc.py` only. No webapp, no Go, no plugin.

**APIs added:** two new local routes on `meetsvc.py` itself (`POST /dialog`, `POST /action`) — both loopback-only, same trust boundary the existing `/` endpoint already has (nothing but this box's own Mattermost can reach 127.0.0.1:8077). `meetsvc.py` now also *calls out* to two existing Mattermost APIs it didn't use before: `POST /api/v4/actions/dialogs/open` and `GET /api/v4/users/{id}` (to resolve a dialog submitter's username) — both using the same `BOT_TOKEN` already in use, no new credential.

**Database changes:** none. `meet-history.json` is a new *file* (same pattern as the pre-existing `meet-schedule.json`), not a database table.

**Tests performed (17/17 passed, real HTTP calls simulating exactly what Mattermost sends, not assumed):**
- Regression: instant `/meet`, `/meet in 2m`, `/meet list`, `/meet cancel`, wrong-token-rejected-with-403 — all still pass
- New: Copy Link button (verified response contains the real URL), Cancel button (verified it actually cancels, and is graceful on a second click)
- New: `/meet schedule` genuinely calls the real `dialogs/open` API (proven by a deliberately fake `trigger_id` being correctly rejected — a stub would have "succeeded")
- New: dialog submission for both instant and scheduled cases — verified the scheduled one landed in `meet-schedule.json` with the **correct topic and a real future timestamp** (catching and fixing the bug above)
- New: `/meet history` shows cancelled and started entries correctly

**Build:** no frontend or Go build was needed or run — nothing outside `meetsvc.py` changed.

**Manual test steps:**
1. In Honco Chat, type `/meet schedule` → a modal should open with Name/When fields
2. Submit it (try leaving When blank for instant, or `in 5m` for scheduled) → a post appears with **Copy Link** (and **Cancel**, if scheduled) buttons
3. Click **Copy Link** → an ephemeral code-block reply with the URL appears (click to select, Mattermost's own copy icon shows on hover)
4. If scheduled, click **Cancel** → confirms cancellation
5. Type `/meet history` → shows what you just did

**Known limitations (honest, not hidden):**
- No persistent "Start Meeting" button in the channel header — needs the full plugin, explicitly deferred
- No participant picker — the existing architecture has no per-attendee concept at all (a meeting is a channel post, not a guest list); adding real participant tracking is new data-model work, not a UI-only addition
- Status is `scheduled`/`reminder_sent`/`cancelled`/`failed` only — no `in_progress`/`completed`, since Jitsi doesn't report room state back to meetsvc
- The dialog-submitted meeting doesn't get a "Cancel" button *until after* it's posted (it's a plain bot post, same as a reminder) — this already works correctly (verified above), noted only because it's a slightly different code path than the text-command response
- `/meet schedule`'s dialog-open call couldn't be end-to-end tested with a *real* `trigger_id` from this session (those are single-use, generated only by a live user typing the command in the browser) — the manual test steps above are the way to confirm this specific piece

**Post-testing bugfix — "Copy Link" didn't copy anything:** manual testing found the Copy Link button's click did not put the URL on the clipboard. Root cause: `EphemeralText` (what the button's handler returned) becomes a real `Post.Message` server-side (confirmed in `channels/app/integration_action.go`), so it *does* render through Mattermost's normal markdown pipeline and *does* get the native `CopyButton` component (`components/copy_button.tsx`, real `navigator.clipboard.writeText()` with an `execCommand` fallback and "Copy code" → "Copied" feedback) — but only on the **second** message the button click revealed, not the button itself. Users reasonably expected the button click to be the copy action, not a reveal-then-copy two-step. There is no custom Honco frontend component involved at all — confirmed by grep, only `meetsvc.py` and this doc mention "Copy Link" anywhere in the codebase. Fixed by putting the URL directly in the main, always-visible message as a fenced code block (`link_block()`), giving Mattermost's existing, already-tested copy affordance in one click instead of two; removed the now-redundant Copy Link button from new messages (kept the handler for backward compatibility with any already-posted message that still has one). 11/11 new tests + full regression passed; verified against the actual stored `Posts.Message` row in Postgres, not just the HTTP response.

### Authentication & Session Management — ✅ IMPLEMENTED (this session)

**Scope note:** this work was requested out of Phase order (it maps conceptually to Phase 11 — Security) because authentication/session correctness was made an explicit gate: *"Do NOT move to another Honco feature until authentication/session management is stable."* It is now stable. Phase 11's later cross-cutting pass should treat this section as already done, not re-scope it.

**Audit-first finding, consistent with the rest of this plan:** the large majority of the requested feature list **already exists**, correctly, in Mattermost's own auth/session architecture — this is a 10+ year mature product and session management is core, not a gap. Confirmed already-working (by source inspection, not assumption):
- Server-side session model with real expiration (`ServiceSettings.SessionLengthWebInDays`/`SessionLengthMobileInDays`/`SessionLengthSSOInDays`, `SessionIdleTimeoutInMinutes`), stored and validated per-request, not a client-trusted token
- Manual logout (`POST /users/logout`) performs a **real server-side session revocation** (`RevokeSessionById`), not just a cookie clear — verified: token is rejected (401) immediately after logout
- Session management API already complete: list sessions (`GET /users/{id}/sessions`), revoke one (`POST /users/{id}/sessions/revoke`), revoke all for a user (`POST /users/{id}/sessions/revoke/all`), revoke all for all users (admin)
- Session management **UI** already complete: Account Settings → Security → **"View and Log Out of Active Sessions"** opens `ActivityLogModal`, which calls `getSessions`/`revokeSession`, lists device/platform/browser per session, with a per-session **Log Out** button — no new UI was built, because one already existed
- CSRF: double-submit cookie pattern (`MMCSRF`), checked against every mutating request
- Cookies: `MMAUTHTOKEN` is `HttpOnly`; `Secure` flag is dynamic on request protocol (only set over HTTPS, so local HTTP dev is unaffected)
- Failed-login lockout: real, server-enforced (`MaximumLoginAttempts`, default/current 10), independent of the separate (currently off) API-wide rate limiter
- Password reset tokens: expire, and are **one-time-use** (deleted from the `Tokens` table on successful use) — verified: reusing a reset token a second time is rejected
- Frontend protected-route pattern: `components/logged_in/logged_in.tsx` shows a loading screen (never app content) until `currentUser` is confirmed, then either renders the app or redirects to `/login?redirect_to=...` — exactly the "no flash of authenticated content" behavior requested, already implemented
- Multi-tab session sync: native `window.addEventListener('storage', ...)` in `components/root/root.tsx`, driven by `BrowserStore.signalLogin()`/`signalLogout()` — a login/logout in one tab is picked up by others instantly, no polling
- Authorization: normal users get **403** on admin-only endpoints (verified against `GET /config`); missing/invalid sessions get **401** (verified against `GET /users/me`)
- MFA/2FA: confirmed genuinely available in this build (`public/model/mfa_secret.go` — not enterprise-gated), with existing frontend UI (`user_settings/security/mfa_section`)

**Two real, narrow gaps found and fixed** (everything else above was already correct — nothing else was changed):

1. **Self-service password change didn't revoke other sessions.** `UpdatePasswordAsUser` (`channels/app/user.go`) changed the password but left every other active session (other browsers/devices) logged in — a real security gap if a password change was prompted by a compromised account. **Fix:** after a successful change, list the user's sessions (`GetSessions`) and revoke every session *except the one that made the request* (`RevokeSession` per session) — deliberately not `RevokeAllSessions` (which would also kill the session making the very request that just proved identity via the current password). Verified: the session that made the change stays logged in; a second, independent session is correctly logged out (401) immediately after.
2. **`SameSite` wasn't explicitly set on session cookies in the normal (non-embedded) case.** Upstream only sets `SameSite` explicitly for the embedded-iframe path (`SameSiteNoneMode`); the ordinary case relied on the browser's unset-defaults-to-Lax behavior, which is implicit rather than guaranteed. **Fix:** explicit `SameSite=Lax` on `MMAUTHTOKEN`/`MMUSERID`/`MMCSRF` whenever the embedded case doesn't apply. Lax still allows normal top-level navigation and same-site fetch/XHR, so local HTTP development and the existing webapp are unaffected. Verified with a real login request shaped exactly like the webapp's own (`X-Requested-With: XMLHttpRequest`, the header Mattermost's login handler actually checks before attaching cookies at all): all three cookies now show `SameSite=Lax`.

**Implementation approach:** both fixes are in a new idempotent, re-appliable Go-source patch script, `chat/branding/harden-session-security.py` — same pattern and same directory as the existing `debrand-*.py` scripts, so it survives (and can be re-run after) a future upstream Mattermost pull, rather than being a one-off hand-edit that a re-clone would silently discard.

**Configuration changed** (`config.json`, backed up first as `config.json.bak-auth-audit-<timestamp>`):
- `EnableMultifactorAuthentication`: `False` → `True` (MFA now available as an **optional** self-service feature under Account Settings → Security; `EnforceMultifactorAuthentication` deliberately left `False` — making it mandatory without a rollout plan would lock out existing users, which is exactly the kind of disruptive, arbitrary change this task's instructions ruled out)

**Configuration deliberately left unchanged, and why:** session timeout values (`SessionLengthWebInDays=180`, `SessionIdleTimeoutInMinutes=43200` i.e. 30 days, `ExtendSessionLengthWithActivity=False`) were precisely traced through their actual enforcement code (`channels/app/session.go`) before deciding not to touch them. They already form a coherent, deliberate-looking policy — a 30-day real idle timeout inside a 180-day absolute cap — and the task's own instructions explicitly rule out "choosing arbitrary timeout values without first checking existing configuration." Changing them without a concrete reason would have violated that instruction, not honored it.

**Database changes:** none. No schema touched.

**Tests performed — 30/30 automated, all real HTTP calls against the live server, never assumed:**
- Password-change session revocation: originating session survives, independent second session correctly revoked (401)
- SameSite=Lax present on all three session cookies, tested with the exact request shape (`X-Requested-With: XMLHttpRequest`) the real webapp sends — this also uncovered and confirmed *why* a plain API call never gets cookies at all (Mattermost's `login()` handler only calls `AttachSessionCookies` when that header is present; a bare token-only API/CLI-style login intentionally gets the `Token:` header instead)
- 401 on no session and on an invalid/garbage token; 403 for a normal user on an admin-only endpoint; 200 for a normal user on their own data
- Session list, single-session revoke (targeted session dies, others unaffected), revoke-all (all sessions including the one that issued the call are correctly killed)
- Logout performs real server-side revocation, not just a client-side cookie clear
- Password reset: send → real one-time token issued → reset succeeds → same token rejected on reuse → old password stops working → new password works

**Build:** Go binary only (`build/honcochat`), rebuilt cleanly (`BUILD_EXIT=0`) after fixing one real Go compile error caught during this work (`} else {` with an interposed comment breaks Go's automatic semicolon insertion — restructured as an independent `if !(cond) { }` block instead). No webapp/frontend changes were needed, so no frontend build was run. Old binary preserved as `build/honcochat.pre-auth-audit`; only the `honcochat` process was restarted (PostgreSQL and `meetsvc.py` were left untouched and confirmed still running under their original PIDs).

**Regression check (post-swap):** system ping, login, `GET /users/me/teams`, and the commands endpoint all still respond correctly; `meetsvc.py` still listening on :8077; de-branding `strings` check on the new binary shows the identical string-hit count as the pre-patch backup (200 in both) — confirming this change introduced no new upstream-branding leaks (the residual hits are pre-existing Go package-import-path strings, out of scope for this task, already accepted as a known limitation of the earlier de-branding pass).

**Manual test steps** (browser-based scenarios that need a real user session and can't be safely scripted headlessly):
1. Log in, refresh the page → still logged in, no flash of a logged-out state
2. Log in, navigate directly to a channel URL while already authenticated → loads directly, no redirect through `/login`
3. Log in in two browser tabs; log out in one → the other tab reflects the logout automatically (via the storage-event sync, not a page reload)
4. Log out → refresh → redirected to `/login`
5. While logged out, navigate directly to a protected URL → redirected to `/login?redirect_to=...` and returned there after logging in
6. Account Settings → Security → **View and Log Out of Active Sessions** → open a second session (e.g. a private/incognito window), confirm it's listed, click **Log Out** on it, confirm that other window is now logged out
7. Change your password (Account Settings → Security) → confirm you stay logged in here, and any other device/browser you were logged into elsewhere is now logged out
8. Use "Forgot password" → follow the emailed reset link → set a new password → confirm the old password no longer works and the new one does
9. As a normal (non-admin) user, try to open the System Console → should be blocked/hidden; as an admin, confirm it's accessible

**Known limitations (honest, not hidden):**
- Global API rate limiting (`RateLimitSettings.Enable`) is still **off** — this is a separate mechanism from the per-account failed-login lockout (which is on and enforced regardless). Enabling the global limiter is a production-tuning decision (thresholds, trusted-proxy IP handling) best made deliberately for a real deployment, not defaulted on here without that context.
- `ExperimentalStrictCSRFEnforcement` left at its default (`False`) — the existing double-submit CSRF cookie check is already active and was not weakened; this flag only tightens an already-functioning protection further, and flipping it is a separate, explicit decision the user can make once the current behavior is confirmed compatible with any external integrations.
- MFA was enabled as **optional**, not enforced — see rationale above. Enforcing it for all users is a legitimate future step once there's a rollout/communication plan for existing accounts.
- Mobile app session/cookie behavior was not tested — no mobile client is available in this environment; the mobile-specific session length (`SessionLengthMobileInDays`) and token-header (non-cookie) auth path were inspected in source but not exercised end-to-end.
- SSO/OAuth/SAML/LDAP session paths were not exercised (none are configured in this environment) — the session-creation code they share with password login was inspected and is unaffected by either fix, but wasn't independently tested end-to-end.

### Phase 3 — Recording UI *(re-scoped 2026-09-09: transcription removed)*
**No longer blocked.** Jibri is deployed and has produced 5 real recordings — the original blocker is gone. **Transcription is out of scope**, so this phase is recording only: capture, store, list, play back.

**The actual remaining work is a re-point, not a build.** `finalize.sh` posts completions to V2 (`:8081`); Mattermost needs an endpoint speaking the same contract (see *Audit — 2026-09-09*, Finding 2) and a room→channel registry it can query (Finding 3).

**Order:** plugin callback endpoint → `meetsvc.py` registers rooms (~15 lines) → record one meeting *from `/meet`* → only then the status/playback UI. Note that no `/meet` room has ever been recorded (Finding 1), so that end-to-end run is the first real proof, not a formality.

**Decision 2026-09-09:** `finalize.sh` is **left alone for now** — the endpoint gets built and unit-tested, and the live hook is not touched until the switch is explicitly approved.

### Phase 4 — AI Meeting Intelligence *(re-scoped 2026-09-09)*
**The input changed.** Jibri produces `.mp4`; with STT out of scope there is no transcript, so this phase now summarises the **channel conversation** around a meeting rather than the audio. That is deliverable today with zero out-of-scope work, and it reuses the summariser unchanged — text in, notes out.

**Exists:** `summarize/honco-summarize.sh` already produces Summary/Decisions/Action-items/Open-questions sections. **Missing:** the structured `Task/Owner/Due date/Status` format requested, and any UI to view it (currently it's a chat post).
**Real path:** small prompt-template change (safe, already the established pattern in that file) + storing the structured result somewhere queryable once Phase 7 (tasks) exists to link into.

**Authorization comes first, always:** the messages fed to the model must be only those the requesting user can already read — resolve channel membership *before* building the prompt, never after. The model is not the authorization layer.

**Blocked on an unverified dependency:** Claude credentials are not on this host; `summarize/` runs on `mother` behind a forced-command SSH key whose reachability from this box has not been confirmed. Confirm that before scheduling this phase.

### Phase 5 — AI Chat Assistant
**Missing:** entirely. **Real path:** exact same architecture as `/meet` — new bot account, new slash command(s) (`/summarize`, `/action-items`), new small Python/Go service that calls the Claude CLI (reusing `summarize/`'s existing invocation pattern) scoped to the requesting user's channel permissions (Mattermost's API already enforces this — the service only needs to fetch posts via a token that respects the same permission model, not invent its own).
**Permissions:** the assistant must call the Mattermost API *as the requesting user's session* (or via a bot that checks membership first) — never as an unrestricted admin — to honor "respect existing user/channel permissions."

### Phase 6 — Files & Documents
**Mostly exists:** upload, preview (image/PDF), download are native Mattermost. **Missing:** a dedicated "recent files across channels" view and file search UI (Mattermost's search does index files, but there's no dedicated files browser). Genuinely small addition once Phase 2's plugin skeleton exists (same RHS-panel pattern).

### Phase 7 — Task Management *(scope fixed 2026-09-09: TEAM-SCOPED)*
**Missing:** entirely — no schema, no model, no UI. Re-confirmed 2026-09-09: no task/todo/checklist table in `honcochat`, no plugin KV rows, no components.

**Scope decision:** a task belongs to a **Mattermost Team** and is assignable to members of that Team. Mattermost has no "workspace" concept — Team → Channel is the real hierarchy, and team-scoped is the closest honest mapping.

**Authorization is the whole risk here.** Every read and write filters on `GetTeamMember(team_id, caller)` — never on task ID alone, or the ID becomes an IDOR. An assignee must be a member of the same team, which is what stops cross-team assignment. Status may be changed by creator or assignee; deletion by the creator. Validate title length, the status enum, and `due_at` bounds on every write.
**Real path:** this is the first phase requiring an actual **new PostgreSQL table** (`HoncoTasks` or similar, in a *separate* schema/migration, not touching Mattermost's own tables — see Database Changes below). A plugin (same one from Phase 2) can own this table via its own KV store or a dedicated table it migrates itself — Mattermost's plugin API supports exactly this pattern, so no core schema fork is needed.

### Phase 8 — Notifications
**Mostly exists** (see Phase 1 table). **Missing:** task-assignment, meeting-starting, transcript-ready, summary-ready as *notification types* — these route through creating a post from the relevant bot (same pattern as `/meet`'s reminders), which native Mattermost notification preferences then handle automatically. Not a new notification *system* — reuse of the existing one, fed by new event sources.

### Phase 9 — Remote Support (RustDesk) *(inspection done 2026-09-09)*
**Inspection complete: there is nothing on this host.** No `rustdesk`/`hbbs`/`hbbr`/port reference in any script; `README.md` states the deliberate position that RustDesk stays a separate app, and gives the reason (it ships to public app stores, where the AGPL calculus differs).

**Two verified constraints that cap what is honestly buildable:**
- The **OSS** RustDesk server (hbbs + hbbr) exposes **no HTTP API** — the web console and REST API are Server **Pro** features. Honco therefore cannot open, query, or terminate a session programmatically. Code that claims otherwise would be fiction.
- RustDesk's own documentation is explicit that the **device ID is not an authentication mechanism** — the password is. So Honco carries the ID and **never accepts, stores, transports or logs a password**.

**Real path:** a plugin that manages the *workflow* around the connection — request → accept → session record → end — plus an audit trail. Connection info is returned exactly once, to the agent who accepted; a later fetch deliberately omits it, so the endpoint cannot become a standing lookup for how to reach a colleague's machine. Visibility is a relationship check (requester, assigned agent, or a support-manager role); everyone else gets **404, not 403**, so the endpoint can't be used to discover that a colleague asked for help.

### Phase 10 — Admin Dashboard
**Reuse first:** System Console already covers users/roles/channels/service-health with permission enforcement already correct — do not duplicate it. **Genuinely new:** a Honco-specific view for meetings/transcripts/summaries/tasks, which is exactly the same plugin-RHS pattern as Phases 2/6/7, gated to `system_admin`/`manage_system` permissions (Mattermost's existing role check, reused not reinvented).

### Phase 11 — Security
**Authentication & session management — ✅ done ahead of schedule**, see the dedicated section between Phase 2 and Phase 3 above (session revocation on password change, explicit `SameSite=Lax`, optional MFA enabled, 30 automated tests). Remaining ongoing discipline applied to every phase above, not a separate deliverable: any new endpoint (meeting API, task API, AI assistant) must check Mattermost session + channel/team permission before acting, same as core Mattermost handlers do. Audit logging for new admin actions uses Mattermost's existing `MakeAuditRecord` pattern (seen directly in the API source this session).

### Phase 12 — UI Polish
Deliberately last among the UI phases — polishing screens before Phases 2/5/7/9/10 exist would mean re-polishing them again once those land. Sequenced after the new surfaces exist, not before.

### Phase 13 — Desktop App (Electron)
Explicitly deferred per your own instruction. Noted here only so it isn't forgotten: Mattermost's own desktop app is itself already Electron-based and open-source, which means "prepare Honco for Electron" may mean **re-branding Mattermost's existing desktop app** (same idempotent de-brand pattern as `chat/branding/`) rather than building one from scratch — worth confirming when this phase actually starts, not now.

---

## Database Changes

**None in Phase 1.** First real schema addition is Phase 7 (tasks), and it will be a **new table in the same PostgreSQL database, added via a proper migration, never a manual `ALTER`**, and never touching any existing Mattermost table. If a Mattermost plugin owns it, the plugin's own migration runner handles this safely (the supported mechanism) rather than a hand-run script.

## API Changes

**None in Phase 1.** Phases 2, 5, 7, 9, 10 each add new endpoints, all additive (new routes on new services or a new plugin), never modifying an existing Mattermost API's behavior.

## Dependencies Required (by phase, not all at once)

- Phase 2/6/7/10: Mattermost plugin SDK (`mattermost-plugin-starter-template` pattern) — Go + a small React bundle, built with the toolchain already on this box
- Phase 5: nothing new — reuses the Claude CLI already authenticated on `mother`/set up in `summarize/`
- Phase 13: Electron — only when that phase actually starts

## Risks

- **Webapp rebuild cost**: every webapp-touching change costs a real 10–15 minute build (measured this session). Batching UI changes within a phase, not rebuilding per-file, is essential.
- **Plugin surface vs. core fork**: it would be *possible* to hand-edit `channels/src` instead of writing a plugin, but that reintroduces exactly the "upstream pull clobbers our changes" problem `chat/branding/` was built to avoid. Plugins are the supported extension point precisely to prevent this — Phases 2/5/6/7/10 should stay plugin-based even though it's more upfront structure.
- **Jibri dependency chain**: Phases 3 and 4's UI are both blocked transitively on one infrastructure step (Jibri) not yet done.

## Testing Strategy

Per phase: (1) the specific new/changed script or component gets a direct functional test (as done for `/meet`, SMTP, and the bot restore this session — real API calls, real log verification, never "should work"), (2) a regression check that the *existing* adjacent feature still works (e.g., after Phase 2's plugin ships, confirm `/meet` still works exactly as today), (3) webapp build (`npm run build`) only when webapp source actually changed.

## Implementation Order

*Revised 2026-09-09. Steps 1–3 are done; the rest is re-ordered around what the audit found — chiefly that Jibri no longer blocks anything, and that Tasks is the only feature with no external dependency at all.*

1. ✅ **Phase 1** — meeting-reminder notification fix. No rebuild needed.
2. ✅ **Phase 2** — Meet UI via Interactive Dialogs/Message Actions (`meetsvc.py` only)
3. ✅ **Phase 9 inspection** — done 2026-09-09: nothing on this host, and the OSS API ceiling is now documented
4. **Plugin scaffold** — `com.honco.workspace`: manifest, bot via `EnsureBot`, health route. One plugin for features 3–10; they share a bot, a schema and a UI surface. *Cannot be loaded until a restart window is agreed — build-and-unit-test only until then.*
5. **Phase 7 — Tasks.** First real feature. Deliberately ahead of recording: no external dependency, no risk to anything running, and it proves the whole plugin pipeline (DB migration → API → authz → RHS UI) end to end.
6. **Phase 3 — Recording.** Callback endpoint + `meetsvc.py` room registry. `finalize.sh` untouched until the cutover is approved.
7. **Phase 8 — Notifications** for tasks and recordings, via the plugin bot's DM. Not a new notification system — new event sources feeding the existing one.
8. **Phase 9 — Remote Support** workflow. Independent of everything above.
9. **Phase 4 — Meeting Intelligence** (channel-conversation summaries). *Gated on confirming `mother` is reachable.*
10. **Phase 5 — AI Assistant.** Highest care: authorization strictly before context assembly.
11. **Phase 10 — Admin Dashboard**, then **Phase 6 — Files browser** and **Global Search over Honco entities** — all three need the data from 5–9 to exist first.
12. **Phase 11, 12** — cross-cutting hardening/polish. Named items from the audit: empty `SiteURL`, `RequirePluginSignature: false`, stale `PluginStates`, unconfigured push.
13. **Phase 13** — Desktop, then Mobile. Last, per your instruction.

**Gating items before step 4 can start:** a restart window, and attention to the intermittent WSL exec failures (`0x8007274c`) — implementation needs working builds and tests, which the audit could not run.
