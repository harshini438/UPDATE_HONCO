
---

# 14. Global search

**Status: DONE.** Verified by `search-e2e.js` (27/27), `qa-inj.sh` (injection), `search_test.go`.

| Topic | Fact |
|---|---|
| Native search | Mattermost's search box (Messages / Files) is untouched. The plugin registers a search component (`registerSearchComponents`) that adds a Honco results button; Mattermost suppresses plugin search *pills* on an unlicensed server, so the entry point is the button and the panel's Search tab. |
| Honco search | `GET /search?q=<term>&type=all|tasks|meetings|recordings|summaries|support&page=N&limit=M` (`search_api.go`, `search.go`). Default 20 per page, maximum 50; larger values are clamped, never refused. |
| Categories | **Tasks** (title, description), **Meetings** (topic, room name), **Recordings** (file name, room), **Summaries** (summary text), **Support** (issue). |
| Scope / authorization | `searchScope(userID)` computes the caller's team ids and channel ids once; every query is `team_id IN (...)` or `channel_id IN (...)` (bounded lists). A user in team A never sees team B's rows; private channels the user is not in are excluded (verified: carol sees nothing from the other team's private channel). |
| Filters | type chips; "All" returns one page per category with per-category totals so the UI can say "No Honco results" without adding up pages. |
| Injection | Terms are parameterised; `%`/`_` are escaped for ILIKE (`EscapeLike`); verified with `'; DROP …` style terms — table intact, literal match. |
| Results | Rows open the owning tab or the channel post; the selection survives a refresh. |
| Limitations | Substring ILIKE, no ranking beyond recency (`RankExpr` favours recent rows), no full-text index; message contents are **not** part of Honco search (use Mattermost search). |

---

# 15. Admin dashboard

**Status: DONE.** Verified by `admin-e2e.js` (26/26; 22/26 only when the Jitsi probe times out and the test captures early), `admin-api.sh` (37), `p9-admin-role.sh`.

- **Who:** users with `manage_system` (system admins). The tab is offered only to admins (read from the webapp store) and **every** `/admin/*` call re-checks the permission server-side (`p9-admin-role.sh`: a normal user gets 403 on all of them).
- **What it is not:** it does not replace the System Console (`/admin_console`), which remains Mattermost's configuration area. The Honco dashboard is read-only.

| Section | Metric | Where the number comes from |
|---|---|---|
| System health (`GET /admin/health`) | Honco Chat, PostgreSQL (latency), Honco Plugin (active), Jitsi (HTTP probe of `MeetPublicURL`, 4 s timeout), Jibri (`127.0.0.1:2222` health), Meeting Service (meetsvc `127.0.0.1:8077`) | live probes in `admin_api.go` |
| Usage | Users, Teams, Channels | Mattermost tables |
| | Active meetings, Meetings (total), Recordings, Tasks, Meeting summaries, Support (open/total) | `honco_*` counts (`admin_store.go`) |
| Files | Stored files/size, attached to a post, meeting recordings + bytes, largest file, possible orphans, post-deleted-file-kept, recordings missing file, soft-deleted, max file / recording bytes, public links | `files_admin.go` over `fileinfo` + `honco_recordings` |
| Notifications | bot configured; count per kind | `honco_notifications` grouped by kind |
| AI Assistant | callback configured, service configured/reachable, sessions live / completed / stored | plugin config + KV scan (`adminAI()`) |
| Security | self-signup, MFA, public links, plugins/uploads, signature requirement, e-mail/push, SMTP, push server, Jibri/meet/summarizer/support configured, SiteURL | Mattermost config (booleans only — **no values**) |
| Recent failures | last recording failures, summary failures, support declines | `honco_recordings` failed, `honco_meeting_summaries` failed, `honco_support_events` rejected |
| Versions | plugin 0.1.0, honco migration 6, Mattermost migration 215, bot configured | plugin + `db_migrations` |

Today's readings: 10 users, 2 teams, 34 channels, 184 meetings (3–5 active), 98 recordings, 144 tasks, 14 summaries, 87 support requests (4 open), 73 stored files / 2.2 MB, Jitsi "not reachable from this host · ~4000 ms" (see §27).

---

# 16. Profile / user account

**Status: DONE.** Verified by `auth.js` (20/20), `p9-pwchange.sh`, `profile-photo-e2e.js` (57/57), `avatar-e2e.js` (44/44, two users), `av-api.sh` (29/29).

| Topic | Fact |
|---|---|
| Authentication | Mattermost e-mail/username + password; `EnableMultifactorAuthentication=true` (optional per user); `EnableOpenServer=false` (no self-signup); sessions 4320 h (180 days) for web; password reset by e-mail (verified in the auth suite). Honco changed nothing here except branding text. |
| Profile information | Account menu → **Profile → Profile Settings**: Full Name, Username, Nickname, Position, Email, Profile Photo. **Security**: password change (old password required, verified), MFA, sessions, personal access tokens. |
| Profile photo | Mattermost's profile picture, stored by the server under the user id in the file store (no table besides `Users.LastPictureUpdate`); `POST/DELETE /api/v4/users/{id}/image`, only for the session's own user (403 otherwise, verified); served at `GET /api/v4/users/{id}/image?_={last_picture_update}` (24 h cacheable, the timestamp is the cache key). Honco relabelled the section **Profile Photo** with **Change photo / Save photo / Remove photo**, a preview, "Profile photo updated." / "Profile photo removed." lines and a plain failure message (`chat/branding/profile-photo.py`). |
| Where the avatar appears | Everywhere Mattermost renders a user (messages, threads, DMs, group DMs, member list, popover, pickers, mention search, header) **and** the Honco panels/cards (task assignee, support requester and agent, meeting "Started by") through one `UserAvatar` that uses the same URL and follows `last_picture_update` from the webapp store — no copies. Meeting *participants* are Jitsi display names and keep initials. |
| Normal user can change | own name, username (if allowed), nickname, position, e-mail (password required), photo, password, MFA, notification and display preferences, custom status. |
| Administrator can change | any user's profile, roles, active state, password reset, MFA reset, team membership — in the System Console or `mmctl`. Admins do **not** get extra powers in the Honco panels except the Admin tab. |

---

# 17. Authorization & security

Plain-language summary of what is actually enforced (verified in `p8-security.sh` 56, `p7-idor.sh` 30, `p9-cache-auth.sh` 28, `p9-hardening.sh` 15, `qa-secrets.sh`, `qa-inj.sh`, `av-api.sh`):

| Layer | Rule | How it is enforced |
|---|---|---|
| Authentication | Every plugin route needs a logged-in Mattermost session; anonymous requests get 401 (all routes verified). Three service routes use a shared secret header instead: `/meetings/register` (`X-Honco-Service-Secret`), `/recordings/complete` (`X-Jibri-Callback-Secret`), `/ai/events` (`X-Honco-AI-Secret`); an unset secret rejects everything. | `plugin.go` (`Mattermost-User-Id` header set by the server), `checkSecret` constant-time compare |
| Team membership | Tasks and search are scoped to teams the caller belongs to; assignees must be team members. | `tasks_api.go`, `search_api.go` via the Mattermost API |
| Channel membership | Meetings, recordings, summaries, AI sessions and support requests are visible only to members of their channel (404, not 403, for outsiders so existence is not leaked). | `meetings_api.go`, `summary_api.go`, `ai_api.go`, `support_api.go` |
| Object-level (IDOR) | 30 checks: a user cannot read/modify another team's tasks, another channel's meetings/recordings/summaries/support requests, or another user's AI session, by guessing ids. | `p7-idor.sh` |
| Admin | `/admin/*` require `manage_system`; the UI hint is not authorization. | `admin_api.go` |
| Support agents | Membership of the configured support channel; re-checked on every accept/reject/start/end. | `support_api.go` |
| Self-only | Profile photo, password, profile edits — Mattermost's `SessionHasPermissionToUser`. | Mattermost |
| Cross-team isolation | Verified: two teams, private channel in team B not visible/searchable from team A. | `search-e2e.js`, `p7-idor.sh` |
| Service secrets | Stored only in Mattermost config (`config.json`, 0600) as `secret: true` settings; never returned by any API, never logged (`redactErr`), never in the JS bundle. Sweep of five secret values across bundle, repo, logs, posts and responses: 0 hits. | `qa-secrets.sh` |
| Callback secrets | Jibri: read from a 0600 file in the container, never in the script. AI: header compare. Meet: env file `run/meetsvc.env` 0600. | `meet/jibri-finalize.sh`, `ai_api.go` |
| Rate limiting | Per user (or per IP for service routes): reads 240/min burst 60; mutations 60/30; service 60/20; AI push 300/60 → 429 with `Retry-After: 10`. Mattermost's own `RateLimitSettings.Enable` is false. | `hardening.go` |
| Security headers | On every plugin response: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store`, `Permissions-Policy: camera=(), microphone=(), geolocation=()`. | `hardening.go` |
| Input bounds | JSON bodies capped (64 KB for tasks; recordings by `MaxRecordingMB`); titles/issues length-checked; statuses whitelisted; ids validated; ILIKE wildcards escaped. | handlers |
| File access | Channel membership; no public links; recordings via the file API; traversal attempts on plugin routes → 301/404. | Mattermost + `hardening.go` |
| Password/session | Mattermost defaults; password change requires the current password; sessions invalidated on password change (verified). | Mattermost |
| Audit trail | Support: `honco_support_events`. Notifications: `honco_notifications` ledger. Mattermost's own audit log for logins/config. Tasks and meetings keep `created_at/updated_at` but no per-field history. | tables |
| What is **not** claimed | No third-party certification (SOC 2 / ISO / HIPAA / GDPR). No end-to-end encryption. TLS is terminated outside Mattermost (`ConnectionSecurity=""`): plain HTTP on the LAN, HTTPS only through the Cloudflare tunnel. Jitsi on the LAN uses a self-signed certificate. | — |

---

# 18. Database

One PostgreSQL 16 database, **`honcochat`** (`127.0.0.1:5433`, user `honco`), 94 tables: 85 Mattermost tables (`users`, `teams`, `channels`, `posts`, `fileinfo`, `preferences`, `sessions`, `db_migrations` = 215 applied, …) and 9 Honco tables created by the plugin's own migration runner (`store.go`; version table `honco_schema_migrations`, currently 6; migrations are append-only, one transaction each, and never touch Mattermost's schema).

| Feature | Table(s) | Purpose |
|---|---|---|
| Tasks | `honco_tasks` | team-scoped tasks (indexes: team+status, assignee, due) |
| Meetings | `honco_meetings` | one row per `/meet` room: room_name (unique), channel, creator, topic, status, post_id (card), scheduled/started/ended_at, participant_count |
| Participants | `honco_meeting_participants` | Jitsi occupants per meeting (occupant_key unique per meeting), joined/left/present |
| Recordings | `honco_recordings` | delivered recordings: meeting, room, channel, status (ready/failed/unavailable), file_id → Mattermost `fileinfo`, size, duration, error |
| Summaries | `honco_meeting_summaries` | one per meeting (unique): status, summary, key_points, decisions, action_items, participants, raw_output, window, message_count |
| Notifications | `honco_notifications` | deduplication ledger (kind, subject, recipient, unique dedupe_key); rows older than 90 days pruned daily |
| Support | `honco_support_requests`, `honco_support_events` | requests with status/agent/timestamps; audit events per request |
| Migrations | `honco_schema_migrations` | applied Honco migration versions |
| AI sessions | *(no table)* — plugin KV store `ai:session:<meeting_id>` (Mattermost `pluginkeyvaluestore`) | session state, transcript lines, suggestions, final outputs |

Relationships (all by 26-char Mattermost-style ids, no foreign keys — the plugin validates through the Mattermost API instead):
```
teams ──< honco_tasks >── users (creator, assignee)
channels ──< honco_meetings ──< honco_meeting_participants
                 │  └──< honco_recordings ──> fileinfo (file_id)
                 └──1 honco_meeting_summaries
posts (custom_honco_meeting / custom_honco_support) ◄── post_id
teams ──< honco_support_requests ──< honco_support_events ;  users (requester, agent, actor)
honco_notifications (kind, subject_id, recipient_id)  — no relation, ledger only
```
Row counts today: tasks 144 (live), meetings 184, recordings 98, summaries 14, support requests 87, notifications 1389. No secrets are stored in Honco tables. Backups: `chat/backup-honcochat.sh` (`pg_dump` + config + data; see `OPERATIONS.md` §6); restore is documented there and must not be confused with reset — nothing in this document recommends dropping or truncating.

---

# 19. Project folder structure

**Git repository (Windows: `C:\Users\Dell\Desktop\Honco_Chat`)** — "everything Honco-specific"; deliberately excludes the Mattermost and Jitsi sources.
```
Honco_Chat/
├── README.md                 start here: layout, where things run, rebuild from scratch, STT note, secrets policy
├── OPERATIONS.md             run-book: build, deploy plugin, start/stop, health, backup/restore, rollback, recovery, secrets, boundaries
├── AI_INTEGRATION.md         the AI Assistant contract (events, endpoints, security, what is not claimed)
├── CLIENTS.md                desktop/mobile client status (PARTIAL: no source in repo)
├── HONCO_UPGRADE_PLAN.md     the phased plan and the Sep-9 audit (historical; statuses there are older than this document)
├── chat/
│   ├── honcochat.sh          start | publish | stop | stop-all | status  (postgres, server, meetsvc, tunnel)
│   ├── healthcheck.sh        one line per component; --quiet for cron
│   ├── backup-honcochat.sh   pg_dump + config + data archive
│   ├── meetsvc.py            the /meet slash-command service
│   ├── DEBRAND.md            what was removed from upstream and why; how to re-apply after a pull
│   └── branding/             16 idempotent scripts: debrand-*.py/.sh, harden-*.py, patch_push.py, profile-photo.py
├── plugins/com.honco.workspace/
│   ├── plugin.json           manifest + settings schema (all plugin config keys)
│   ├── server/*.go           the plugin (37 files incl. tests) — see Section 20
│   └── webapp/dist/main.js   the whole plugin UI (one hand-written bundle, ~3.9 k lines)
├── meet/                     Jitsi/Jibri: meet.sh, docker-compose.override.yml, jibri-finalize.sh, setup-jibri.sh, brand-meet.sh, lan-address.sh
├── summarize/honco-summarize.sh   the Claude summarizer contract (runs on "mother")
├── transcribe/               batch speech-to-text worker (whisper.cpp / vixy) + notes; runs on ubuntu-3; independent of the plugin
├── dns/                      honco.in → Cloudflare migration notes and verifier
├── website/                  the public landing page (Vite static site; not the app)
└── docs/                     this document (+ images/)
```

**Runtime on WSL (`~/honco-chat`, `$ROOT` in the scripts)**
```
~/honco-chat/
├── build/        honcochat (server binary), mmctl, backups of previous binaries
├── server -> ~/honco-workspace/server      the Mattermost fork checkout (config/config.json = live config, holds secrets, 0600)
├── plugins/      plugin source symlink/copy used by deploy
├── run/          config.json?, honcochat.pid, meetsvc.pid, meetsvc.env (secrets, 0600), meet-schedule.json, meet-history.json,
│                 data/ (Mattermost file storage by date; users/ = profile images), plugins/ + client-plugins/ (installed plugin)
├── pgdata/       PostgreSQL data directory (port 5433)
├── logs/         mattermost.log, honcochat.log, pg.log, tunnel.log
├── backups/      archives from backup-honcochat.sh
├── data/         (empty)
└── lan-address.sh, meetsvc.py (deployed copies)
~/honco-workspace/
├── server/       Mattermost fork: server/ (Go), webapp/channels/ (React; dist/ is served as server/client)
├── plugins/      (plugin build staging)
└── chat/         deployed ops scripts (honcochat.sh lives here for `stop`/`start`)
~/honco-meet/     Jitsi docker project: docker-compose.yml, jibri.yml, docker-compose.override.yml, .env, jibri/ (config, finalize.sh, secret file)
```
"What would I look here for?" — `chat/branding/` when upstream is pulled; `plugins/…/server/` for any Honco behaviour; `webapp/dist/main.js` for any Honco UI; `meet/` for Jitsi/Jibri; `~/honco-chat/logs/` when something fails; `~/honco-chat/run/` for the live state files and pids.

---

# 20. Code map — "If I want to change X, where do I go?"

| I want to change… | Go to |
|---|---|
| Task UI (form, list, filters, avatars) | `webapp/dist/main.js` → `TaskForm`, `TaskRow`, `TasksTab` |
| Task API / rules (who may edit, validation) | `server/tasks_api.go`, `server/task.go` |
| Task storage / SQL | `server/store.go` (task functions), migration 1 |
| Task notifications, due reminders | `server/notify.go` |
| `/meet` command syntax, scheduling, reminders | `chat/meetsvc.py` |
| Meeting registration / recording callback / recording listing | `server/recordings_api.go` |
| Meeting read APIs (get, list by channel, active) | `server/meetings_api.go` |
| Meeting lifecycle (poll interval, 90 s grace, 30 min fallback) | `server/meeting_lifecycle.go`, `server/muc.go` |
| Meeting card look / buttons / thread replies | `server/meeting_card.go` (props, posts), `main.js` → `MeetingCard` |
| Jibri delivery limits and file storage | `server/recording.go`, `meet/jibri-finalize.sh`, `meet/setup-jibri.sh` |
| Meeting Intelligence (window, transport, parsing) | `server/summarizer.go`, `server/meeting_summary.go`, `server/summary_api.go`, `summarize/honco-summarize.sh` |
| AI Assistant UI | `main.js` → `AIPanel`, `AILive`, `AIFinished`, `SuggestionCard`, `TranscriptBlock`, `AIIntent` |
| AI integration (service calls, events, secrets) | `server/ai_adapter.go`, `server/ai_api.go`, `server/ai.go`, `AI_INTEGRATION.md` |
| Support workflow / agents | `server/support.go`, `server/support_api.go`, `server/support_card.go`; `main.js` → `SupportTab`, `SupportCard` |
| Search | `server/search.go`, `server/search_api.go`; `main.js` → `SearchTab` |
| Admin dashboard | `server/admin_api.go`, `server/admin_store.go`, `server/files_admin.go`; `main.js` → `AdminTab` |
| Rate limits, security headers, auth wrapper | `server/hardening.go`, `server/plugin.go`, `server/api.go` (route table) |
| Profile Photo section wording/behaviour | `chat/branding/profile-photo.py` (then rebuild the web client) |
| Avatars in Honco panels | `main.js` → `UserAvatar`, `pictureUpdateOf` |
| Branding, telemetry removal, About dialog | `chat/branding/*` + `chat/DEBRAND.md` |
| Plugin configuration keys | `plugins/com.honco.workspace/plugin.json` (settings schema) + `server/recordings_api.go` (config struct) |
| Database migration | `server/store.go` → append to `migrations` (never edit a shipped entry) |
| Start/stop/publish | `chat/honcochat.sh` |
| Jitsi/Jibri stack | `meet/meet.sh`, `meet/docker-compose.override.yml`, `~/honco-meet/.env` |

---

# 21. APIs

All Honco endpoints live under **`/plugins/com.honco.workspace/api/v1`** (`server/api.go`). Unless stated, **Auth = Mattermost session** (cookie or bearer token; the browser also sends `X-Requested-With: XMLHttpRequest`). Errors are JSON `{"error": "<message>"}`; 401 no session, 403 not allowed, 404 not visible/not found, 400 validation, 409 conflict, 413 too large, 429 rate limited, 500 store error.

### Health
| Method | Path | Purpose | Auth | Source |
|---|---|---|---|---|
| GET | `/health` | plugin liveness (`{"status":"ok"}`) | session | `api.go` |

### Tasks (`tasks_api.go`)
| Method | Path | Purpose | Request | Response |
|---|---|---|---|---|
| POST | `/tasks` | create | `{team_id, title, description?, assignee_id?, status?, due_at?}` | 201 task |
| GET | `/tasks` | list | `?team_id&status&assignee_id&creator_id&due_before&page&limit` | `{tasks:[…]}` (the panel pages with `page`+`limit`; "1–20" is computed client-side) |
| GET | `/tasks/{task_id}` | read | — | task |
| PATCH | `/tasks/{task_id}` | edit (pointer fields) | `{title?, description?, assignee_id?, due_at?}` | task |
| PUT | `/tasks/{task_id}/status` | set status | `{status}` | task |
| DELETE | `/tasks/{task_id}` | soft delete (creator) | — | 204 |

### Meetings & recordings (`recordings_api.go`, `meetings_api.go`)
| Method | Path | Purpose | Auth | Request → Response |
|---|---|---|---|---|
| POST | `/meetings/register` | meetsvc registers a room | `X-Honco-Service-Secret` | `{room_name, channel_id, creator_id, topic, scheduled_at?}` → meeting |
| POST | `/recordings/complete` | Jibri delivers a recording | `X-Jibri-Callback-Secret` | multipart `room_name, status, duration, file` → `{recording}` (or failed row) |
| GET | `/channels/{channel_id}/recordings` | recordings of a channel | member | list |
| GET | `/channels/{channel_id}/meetings` | meetings of a channel (+ summary status; handler lives in `summary_api.go`) | member | `{meetings, summary_status}` |
| GET | `/channels/{channel_id}/active-meetings` | active/scheduled meetings | member | list |
| GET | `/meetings/{meeting_id}` | one meeting (card props) | channel member | meeting |

### Summaries (`summary_api.go`)
| Method | Path | Purpose | Response |
|---|---|---|---|
| POST | `/meetings/{meeting_id}/summary` | start (re)generation | 202 `{status: pending}` (or 200 ready if fresh) |
| GET | `/meetings/{meeting_id}/summary` | read state/result | `{status, summary, key_points, decisions, action_items, participants, error_message, …}` |

### AI Assistant (`ai_api.go`)
| Method | Path | Purpose | Auth |
|---|---|---|---|
| POST | `/ai/events` | service pushes `{meeting_id, event, …}` (status/transcript/suggestion/insight/topics/final/error) | `X-Honco-AI-Secret` |
| GET | `/ai/status` | is the assistant configured/reachable | session |
| GET | `/meetings/{meeting_id}/ai` | session state + recent lines | channel member |
| GET | `/meetings/{meeting_id}/ai/transcript?before=<seq>` | older transcript lines | channel member |
| POST | `/meetings/{meeting_id}/ai/session` | start a session (calls the service) | channel member |
| DELETE | `/meetings/{meeting_id}/ai/session` | end a session | channel member |

### Support (`support_api.go`)
| Method | Path | Purpose |
|---|---|---|
| POST | `/support/requests` | create `{team_id, channel_id, issue}` |
| GET | `/support/requests?team_id` | my requests + (agents) the queue; returns `is_agent` |
| GET | `/support/requests/{request_id}` | one request with its events |
| POST | `/support/requests/{request_id}/accept` · `/reject` · `/start` · `/end` · `/cancel` | transitions (body `{reason?}` for reject) |

### Search (`search_api.go`)
| Method | Path | Purpose |
|---|---|---|
| GET | `/search?q=&type=all|tasks|meetings|recordings|summaries|support&page=&limit=` | grouped results `{query, type, pages:[{type, hits, total, page, limit}]}` |

### Admin (`admin_api.go`) — `manage_system`
| Method | Path | Purpose |
|---|---|---|
| GET | `/admin/overview` | usage, files, notifications, AI, security flags, recent failures, versions |
| GET | `/admin/health` | live component probes |

Endpoints that do **not** exist (so nobody looks for them): no task comments, no meeting create via REST (only `/meet`), no recording start/stop API, no user directory endpoint of Honco's own (the UI uses Mattermost's `/api/v4/users`), no RustDesk API.

---

# 22. WebSockets

Honco uses Mattermost's existing WebSocket (`/api/v4/websocket`) — no second socket. The plugin publishes three custom events; the webapp registers handlers for them and a reconnect handler.

| Event | Sent by | When | Payload (plain JSON maps only — a struct once wedged the plugin RPC) | Receivers | UI change |
|---|---|---|---|---|---|
| `custom_com.honco.workspace_meeting_updated` (`WebSocketEventMeeting`) | `meeting_card.go` | any card change: registered, started, joined/left, ended, recording ready/failed, summary ready | `{post_id, meeting_id, props…}` | everyone in the channel | the meeting card re-renders in place; Meetings/AI selectors refresh |
| `custom_com.honco.workspace_support_updated` (`WebSocketEventSupport`) | `support_api.go` | create and every transition | `{request_id, status, …}` | the requester, the agent, the support channel | Support tab rows and the card update without polling |
| `custom_com.honco.workspace_ai_event` (`WebSocketEventAI`) | `ai.go` | every accepted push from the AI service and every session state change | `{meeting_id, event, seq, data…}` (bounded) | everyone in the meeting's channel | the AI panel appends transcript lines, suggestions, insights, topics; status badge changes; final view renders |
| Mattermost `user_updated`, `posted`, etc. | Mattermost | — | — | — | avatars re-key on `last_picture_update`; cards appear as new posts |

**Reconnect:** `registerReconnectHandler` fires `HoncoReconnectBus`; the AI panel re-reads `GET /meetings/{id}/ai` (so lines missed during a drop are recovered from the stored session), the Support and Meetings tabs reload their lists. The AI panel also shows a *Reconnecting* state while Mattermost's socket is down (it watches the webapp store's `websocket` state).

**Why WebSocket rather than polling:** a meeting card is visible to a whole channel; polling every card from every client every few seconds would hammer the server, whereas the plugin already knows the exact moment a card changes and pushes one event to the channel's connected clients. The only polling in the system is server-side (the 10 s Prosody occupancy poll) and the panel's short poll while a summary is `pending`.
