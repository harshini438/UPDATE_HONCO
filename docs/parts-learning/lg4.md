
---

# 23. How to run the project

Everything runs as a normal user inside WSL (`Ubuntu-22.04`); no `sudo`, nothing destructive.

```bash
wsl.exe -d Ubuntu-22.04                       # 0. a Linux shell on this machine

cd ~/honco-workspace/chat
./honcochat.sh start                          # 1. PostgreSQL (5433) → Honco Chat server (8065) → meetsvc (/meet, 8077)
./honcochat.sh status                         #    postgres: up · honcochat: up (pid) · tunnel: down · meetsvc: up · SiteURL

curl -s http://127.0.0.1:8065/api/v4/system/ping        # 2. {"status":"OK",...}
psql -h 127.0.0.1 -p 5433 -U honco -d honcochat \
     -c "SELECT version FROM honco_schema_migrations ORDER BY version DESC LIMIT 1;"   # 3. → 6 (read-only check)

# 4. Jitsi + Jibri (Docker Desktop must be running first)
cd ~/honco-meet
~/honco-workspace/meet/meet.sh up             #    web / prosody / jicofo / jvb / jibri
~/honco-workspace/meet/meet.sh ps
#    after a Docker Desktop restart Jibri must be recreated:
#    docker compose -f docker-compose.yml -f jibri.yml -f docker-compose.override.yml up -d --force-recreate --no-deps jibri

# 5. Browser
#    http://localhost:8065   (from another device on the LAN: http://<WSL eth0 address>:8065 — see ~/honco-chat/lan-address.sh)
#    Log in with an existing account (admins create accounts; self-signup is off).

# 6. Health, all components at once
~/honco-workspace/chat/healthcheck.sh         #    or Honco Workspace → Admin → System health

# 7. Stop (keeps all data)
./honcochat.sh stop                           #    server + meetsvc ;  ./honcochat.sh stop-all also stops PostgreSQL
```

| Component | Started by | Listens | Log |
|---|---|---|---|
| PostgreSQL | `honcochat.sh start` | 127.0.0.1:5433 | `~/honco-chat/logs/pg.log` |
| Honco Chat server | `honcochat.sh start` | :8065 | `~/honco-chat/logs/mattermost.log`, `honcochat.log` |
| Meeting service (meetsvc.py) | `honcochat.sh start` | 127.0.0.1:8077 | `honcochat.log` |
| Jitsi (web, prosody, jicofo, jvb) | `meet/meet.sh up` | :8443, 127.0.0.1:5280, :10000/udp | `meet.sh logs <svc>` |
| Jibri | `meet/meet.sh up` | 127.0.0.1:2222 (health) | `meet.sh logs jibri`, `/storage/finalize.log` in the container |

After changing plugin code: `cd ~/honco-workspace/plugins/com.honco.workspace && go build ./server/ && go vet ./... && go test ./server/...`, then package and `mmctl --local plugin add --force <tar.gz>` (`OPERATIONS.md` §3). After changing a branding script: run it, then `cd ~/honco-workspace/server/webapp/channels && npm run build` — **run that build alone**; it takes 7–17 minutes and on 14 Sep it overloaded the machine badly enough to poison the server's database connections until a stop/start.

Never run: `DROP DATABASE`, `TRUNCATE`, any "reset database", `git reset --hard`, `git clean`, or delete `pgdata`/`run/data`.

---

# 24. How to debug

The general order is always the same: **browser console → network request → API response → server log → database → source file.**

**"I click Create Task but nothing happens."**
1. Browser console (F12 → Console): a red error from `main.js`? Note the line.
2. Network tab: is there a `POST …/api/v1/tasks`? If not, the click never reached `fetch` — look at `TaskForm` in `main.js`.
3. API response: 400 → read `{"error": …}` (title/status/assignee). 401 → session expired, reload. 403 → team membership or creator/assignee rule. 429 → rate limited, wait 10 s. 500 → server log.
4. Server log: `~/honco-chat/logs/mattermost.log`, search for `honco:` lines around that time.
5. PostgreSQL: `SELECT id, title, status, deleted_at FROM honco_tasks ORDER BY created_at DESC LIMIT 5;` — did the row land?
6. Source: `tasks_api.go handleCreateTask` → `task.go` validation → `store.go CreateTask`.

**"Meeting doesn't start (no card after /meet)."**
1. Did Mattermost show "command failed"? → meetsvc is down: `./honcochat.sh status`.
2. `honcochat.log`: meetsvc lines — "register … 401"? → `HONCO_SERVICE_SECRET` in `run/meetsvc.env` does not match the plugin's `meetservicesecret` (compare without printing them).
3. `mattermost.log`: "registration rejected".
4. DB: `SELECT room_name, status, created_at FROM honco_meetings ORDER BY created_at DESC LIMIT 3;`
5. Source: `meetsvc.py register_room`, `recordings_api.go handleRegisterMeeting`.
Card exists but "Join Meeting" fails → `MeetPublicURL` is not reachable from your browser; Jitsi containers (`meet.sh ps`).

**"Recording unavailable."**
1. Was Record pressed in Jitsi at all? (Honco never starts recordings.)
2. Inside Jibri: `meet.sh exec jibri cat /storage/finalize.log` — "delivered to Honco Chat"? "cannot authenticate"? nothing (zero media captured)?
3. `mattermost.log`: "recording callback rejected" (secret) or "too large".
4. Admin → Files: "recordings missing file"; card badge says *failed* (with the reason) vs *unavailable* (file deleted).
5. DB: `SELECT status, error_message, size_bytes FROM honco_recordings ORDER BY created_at DESC LIMIT 3;`
6. Source: `meet/jibri-finalize.sh`, `recordings_api.go handleRecordingComplete`, `recording.go`.

**"AI Assistant offline."**
1. Admin → AI Assistant card: *service configured?* Today it is **not** (`aiserviceurl` empty) — that is expected, not a bug.
2. If configured: *reachable?* → `ai_adapter.go` errors in `mattermost.log` (secrets are redacted).
3. Pushes not arriving → the service must send `X-Honco-AI-Secret`; 401 in the log means a mismatch; 429 means it pushes too fast.
4. `GET /meetings/{id}/ai` in the Network tab shows the stored session state.
5. Source: `ai_api.go`, `ai_adapter.go`, `ai.go`; contract `AI_INTEGRATION.md`.

**"Support unavailable (nobody can accept)."**
1. Admin → Security: "support channel configured"? Settings `supportteamname` / `supportchannelname`.
2. Is the agent a *member* of that channel? Membership is the only thing that makes an agent.
3. 409 on Accept → someone else already acted; refresh.
4. DB: `SELECT status, agent_id, updated_at FROM honco_support_requests ORDER BY created_at DESC LIMIT 3;` and `honco_support_events`.
5. Source: `support_api.go isSupportAgent`, `support.go`.

**"Avatar not updating."**
1. `GET /api/v4/users/<id>` → `last_picture_update` changed? If not, the upload did not happen (check the dialog's message).
2. Network tab: image requests must carry `?_=<timestamp>`; a URL *without* it can be cached for a day — Honco never renders one.
3. Other users update through the `user_updated` WebSocket event; if their socket is down they update on reload.
4. Your own session right after the dialog uses a client-side timestamp (upstream behaviour) — still a new key.
5. Source: `main.js UserAvatar`, `chat/branding/profile-photo.py`.

**"Search not returning results."**
1. Are you a member of the team/channel that owns the item? Scope is by membership — this is by design.
2. Is the term a substring of the title/topic/issue? (Message text is not searched here.)
3. Network tab: `GET /search?q=…` → the JSON pages and totals.
4. DB: `SELECT title FROM honco_tasks WHERE title ILIKE '%term%';`
5. Source: `search_api.go searchScope`, `search.go`.

---

# 25. Code navigation — "I want to change X → open Y"

| I want to change… | Open |
|---|---|
| **Tasks** (UI) | `plugins/com.honco.workspace/webapp/dist/main.js` → `TaskForm`, `TaskRow`, `TasksTab` |
| Tasks (rules, validation, page size) | `plugins/com.honco.workspace/server/task.go` |
| Tasks (HTTP handlers) | `plugins/com.honco.workspace/server/tasks_api.go` |
| Tasks (SQL) | `plugins/com.honco.workspace/server/store.go` |
| **Meeting lifecycle** (10 s poll, 90 s grace, 30 min fallback) | `server/meeting_lifecycle.go`; occupancy query `server/muc.go` |
| **Meeting card** (data, thread replies, WebSocket) | `server/meeting_card.go`; drawing: `main.js` → `MeetingCard` |
| `/meet` command syntax, scheduling, reminders | `chat/meetsvc.py` |
| Meeting registration, recording callback, recordings list | `server/recordings_api.go` |
| Meeting read endpoints | `server/meetings_api.go` |
| **Jibri** (limits, statuses) | `server/recording.go`; hook `meet/jibri-finalize.sh`; install `meet/setup-jibri.sh`; stack `meet/meet.sh`, `meet/docker-compose.override.yml` |
| **Notifications** (texts, recipients, dedupe, due scanner) | `server/notify.go` (tasks, summaries); `server/recordings_api.go` (recording posts); `server/support_card.go` (support); `server/ai_api.go` (AI DMs) |
| **AI** (UI) | `main.js` → `AIPanel`, `AILive`, `AIFinished`, `SuggestionCard`, `TranscriptBlock`, `AIIntent` |
| AI (integration: service calls, events, secrets) | `server/ai_adapter.go`, `server/ai_api.go`, `server/ai.go`; contract `AI_INTEGRATION.md`; keys in `plugin.json` |
| **Meeting Intelligence** | `server/summarizer.go`, `server/meeting_summary.go`, `server/summary_api.go`; `summarize/honco-summarize.sh`; UI `main.js` → `MeetingsTab` |
| **Support** | `server/support.go`, `server/support_api.go`, `server/support_card.go`; UI `main.js` → `SupportTab`, `SupportCard` |
| **Search** | `server/search.go`, `server/search_api.go`; UI `main.js` → `SearchTab` |
| **Admin** | `server/admin_api.go`, `server/admin_store.go`, `server/files_admin.go`; UI `main.js` → `AdminTab` |
| **Profile photo** (dialog wording/behaviour) | `chat/branding/profile-photo.py` → then rebuild the web client |
| Avatars inside Honco panels | `main.js` → `UserAvatar`, `pictureUpdateOf` |
| **Frontend** (all Honco UI, styles `.hw-*`, WebSocket handlers, registrations) | `plugins/com.honco.workspace/webapp/dist/main.js` (single file) |
| **API routes** (the table of every URL) | `plugins/com.honco.workspace/server/api.go` → `newRouter` |
| Security layer (headers, rate limits, service routes) | `server/hardening.go`; auth wrapper in `server/plugin.go` / `api.go` |
| **Database migrations** | `plugins/com.honco.workspace/server/store.go` → `var migrations` (append only) |
| Plugin settings (config keys) | `plugins/com.honco.workspace/plugin.json` (schema) + the config struct in `server/recordings_api.go` |
| Start / stop / publish | `chat/honcochat.sh` |
| De-brand / vendor removal | `chat/branding/*`, `chat/DEBRAND.md` |
| Tests | Go: `server/*_test.go`; browser/API suites: outside the repo (`honco-browser/*.js`, scratchpad `*.sh`) — see the reference document §25 |

All paths verified against the repository at commit `126acc9`.

---

# 26. Feature status

| Feature | Status | Explanation |
|---|---|---|
| Chat (Mattermost core) | **DONE** | Unchanged upstream behaviour; Team Edition. |
| De-brand / telemetry removal | **DONE** | Scripted; re-run after each upstream pull. |
| Tasks | **DONE** | All operations, overdue, filters, paging, notifications; tests green. |
| `/meet`, meeting cards, lifecycle | **DONE** | Verified with two real participants: 90 s empty-room end and 30 min fallback. Caveat: `MeetPublicURL` must be reachable by participants; "View Summary" on a card needs the panel already open (known defect). |
| Jibri recording | **DONE** | Real delivery verified; a participant must press Record; Jibri needs recreating after a Docker restart. |
| Meeting Intelligence | **BLOCKED** | Implemented and tested with a stub; the real summarizer host is unreachable, so no real notes can be produced today. |
| AI Assistant | **DONE** (UI + integration) / **BLOCKED** (real AI) | Everything Honco owns works and is tested; no external AI service exists or is configured. |
| Remote Support | **DONE** | Full workflow with two real users; RustDesk itself is external and **NOT TESTABLE** from here. |
| Files & recordings | **DONE** | Permissions, unavailable handling, admin diagnostics; no retention policy (by edition). |
| Notifications | **DONE** | 20+ kinds with the send-once ledger; e-mail via SMTP; no push (no push server). |
| Search | **DONE** | Five categories, scoped, injection-safe. |
| Admin dashboard | **DONE** | All cards; Jitsi probe reports unreachable from WSL today. |
| Profile photo | **DONE** | Dialog, validation, everywhere, live updates, permissions — two-user tests green. |
| Security hardening | **DONE** | Auth on every route, secrets, rate limits, headers, IDOR — suites green. |
| Backup / ops scripts | **DONE** | `honcochat.sh`, `healthcheck.sh`, `backup-honcochat.sh`. |
| Cloudflare publish | **PARTIAL** | Script exists; `cloudflared` binary is missing; quick tunnel only. |
| Desktop / mobile apps | **PARTIAL** | Backend ready; no client source in this repository; push not configured. |
| Transcription worker (`transcribe/`) | **NOT TESTABLE** | Runs on another host; not connected to the plugin. |
| Landing page (`website/`) | **DONE** | Static site; not deployed. |

---

# 27. What is not our responsibility

| Component | Honco controls | The external service controls | If it is unavailable |
|---|---|---|---|
| **Jitsi** | the link on the card, who-is-in-the-room polling, lifecycle, Docker config/overrides, branding | audio/video quality, the prejoin screen, the toolbar, rooms | Join Meeting fails in the browser; the Admin health card shows Jitsi unavailable; cards/tasks/chat keep working. Today the LAN URL is unreachable *from WSL* only. |
| **Jibri** | the finalize hook, the callback endpoint, size limits, the card and the failure states | when recording starts/stops (a participant's click), the MP4 itself, one recording at a time | No recording arrives; the card never gains "View Recording"; nothing else breaks. |
| **RustDesk** | the request workflow, agents, audit trail, cards, DMs, the hint text | the actual screen sharing/control, the relay, its clients and accounts | The workflow still runs; the agent cannot take the screen. **Not verified in current environment.** |
| **External AI service** | the UI, entry points, session start/stop, the `/ai/events` endpoint and its secret, WebSocket delivery, notifications | voice capture, transcript, suggestions, insights, topics, summary, the model | The AI tab shows *not configured / unavailable*; nothing is faked. **Not connected today.** |
| **Claude summarizer** | the conversation window, the SSH/command transport, parsing, states, UI, DMs | running the Claude CLI on "mother", the wording of the notes | Generation fails with "The summarizer is not reachable from this server." **Unreachable today.** |
| **SMTP** | Mattermost's mail settings (server, port, STARTTLS) | delivering the e-mail | In-app notifications continue; e-mails queue/fail in Mattermost's log. Sending **not exercised** in this pass. |
| **Cloudflare** | the `publish` script, `SiteURL` | the tunnel and the public hostname | LAN access on `:8065` still works; no public address. **Binary missing today.** |

---

# 28. Common questions

**What is Mattermost?** An open-source, self-hosted team-chat server (channels, DMs, files, search, apps). Honco Chat is a customized copy of it.

**What is the Honco plugin?** `com.honco.workspace` — Honco's own code that Mattermost loads: a Go part on the server (tasks, meetings, recordings, AI, support, search, admin) and a JavaScript part in the browser (the Honco Workspace panel and the cards).

**What is PostgreSQL?** The database program that stores all the data in tables. Honco Chat uses one database called `honcochat`.

**What is Jitsi?** Self-hosted video conferencing. It provides the meeting room; Honco provides everything around it.

**What is Jibri?** Jitsi's recorder. It joins the room invisibly, records an MP4, and then calls Honco Chat to deliver it.

**What is a WebSocket?** A connection between browser and server that stays open so the server can push news (a card changed, a transcript line arrived) without the browser asking.

**What is a REST API?** The set of URLs the browser calls to read or change things: `GET` reads, `POST` creates, `PATCH` edits, `DELETE` removes; answers are JSON.

**What is a migration?** A numbered, one-way change to the database structure. Honco's live in `store.go`; the applied ones are recorded in `honco_schema_migrations` (currently 6). Add new ones; never edit old ones.

**What is a bot?** An automated Mattermost user. `honco` is the bot that posts meeting/support cards, thread replies, recording notices and notification DMs.

**What is a callback?** A request that an *external* system makes *to us* when something finished: Jibri calls `/recordings/complete` when a recording is done; the AI service calls `/ai/events` when it has something to show.

**What is a service secret?** A long random string both sides know. meetsvc, Jibri and the AI service each put theirs in a request header; the plugin compares it (in constant time) before trusting the request. They live only in server config files, never in the browser or Git.

**Why do we have multiple services?** Each does one thing well and can be restarted or replaced alone: Mattermost for chat, PostgreSQL for data, meetsvc for the `/meet` command, Jitsi for video, Jibri for recording, the summarizer for notes, RustDesk for remote control. Honco Chat is the hub that ties them together.

**Why does one feature use PostgreSQL while another uses Mattermost storage?** Structured data that must be queried (tasks, meetings, requests) goes in Honco's own tables. Things Mattermost already handles well are reused: recordings are ordinary Mattermost *files* (so channel permissions apply for free), AI sessions sit in Mattermost's plugin key-value store (small, per-meeting, no query needed), and the profile photo is Mattermost's own picture (so every existing screen already shows it).

---

# 29. Complete user journeys

Each journey: **USER ACTION → SYSTEM → RESULT.**

**1. Send message** — type in the composer, Enter → Mattermost saves the post and broadcasts it → the message appears for everyone in the channel; mentions notify.

**2. Create task** — Honco Workspace → Tasks → New task → fill → Save task → `POST /tasks` → rules checked → `honco_tasks` row → DM to the assignee → the row appears at the top.

**3. Schedule meeting** — type `/meet in 30m Client review` → meetsvc stores the reminder, registers the meeting (scheduled), posts the card → at the time the reminder is posted with the link; the card becomes joinable.

**4. Join meeting** — click Join Meeting → new tab, Jitsi prejoin, enter the room → within 10 s the plugin sees you, sets `started_at`, replies "started/joined" in the thread → the card says "Meeting is active · 1 participant".

**5. Record meeting** — in Jitsi: ⋯ → Start recording → Jibri joins invisibly and records → Stop recording → finalize → callback → stored → the card gains "View Recording"; "Recording ready for …" is posted.

**6. View recording** — click View Recording → Mattermost checks you are a channel member → the MP4 plays inline.

**7. Generate summary** — Honco Workspace → Meetings → choose the meeting → Generate summary → the plugin collects the channel conversation and calls the summarizer → **today: "not reachable" + Try again**; when it works: sections shown, View Summary on the card, DM with a link.

**8. Use AI Assistant** — click AI Assistant on the card → the panel opens on the AI tab → Start AI session → the plugin asks the external service → **today: not configured** → when connected: transcript, suggestions, insights and topics stream in live; after the call the summary view and a DM.

**9. Create support request** — Honco Workspace → Support → Request Support → describe → Send → request saved (open), card posted, agents notified → an agent Accepts, Starts (RustDesk hint), Ends → you get a DM at each step.

**10. Upload file** — 📎 → choose → send → Mattermost stores it (≤ 100 MB) → visible to channel members only.

**11. Change profile photo** — avatar → Profile → Profile Photo → Edit → Change photo → preview → Save photo → Mattermost stores it, broadcasts `user_updated` → "Profile photo updated."; your photo changes everywhere for everyone.

**12. Search** — Honco Workspace → Search → term → Enter → results grouped by Tasks / Meetings / Recordings / Summaries / Support, limited to what you may see → click to open.

**13. Admin health check** — (admin) Honco Workspace → Admin → six probes run, counts load → read System health; Refresh re-runs.

---

# 30. One-page project cheat sheet

**HONCO CHAT =** a customized, self-hosted Mattermost (team chat) + the Honco Workspace plugin (tasks · meetings · recording · meeting notes · AI-assistant boundary · remote-support workflow · search · admin) + Jitsi/Jibri for video and recording.

**Main components:** browser (Mattermost web client + `main.js`) → Honco Chat server (`honcochat`, :8065) → Honco Workspace plugin (`com.honco.workspace`) → PostgreSQL (`honcochat`, :5433). Helpers: meetsvc.py (`/meet`, :8077), Jitsi + Jibri (Docker `honco-meet`), SMTP. External: AI service (not connected), Claude summarizer on "mother" (unreachable), RustDesk relay (other host), Cloudflare tunnel (binary missing).

**Main features:** Tasks · `/meet` + meeting cards + lifecycle (started/joined/left/ended, 90 s empty-room end, 30 min fallback) · Jibri recording → View Recording · Meeting Intelligence (notes from the channel conversation) · AI Assistant (UI + integration only) · Remote Support (request → accept → start → end; RustDesk does the control) · Global search · Admin dashboard · Profile Photo (Mattermost's avatar, everywhere).

**Main database tables:** `honco_tasks` · `honco_meetings` · `honco_meeting_participants` · `honco_recordings` · `honco_meeting_summaries` · `honco_notifications` · `honco_support_requests` · `honco_support_events` · `honco_schema_migrations` (+ 85 Mattermost tables; AI sessions in the plugin key-value store).

**Main APIs** (`/plugins/com.honco.workspace/api/v1`): `/tasks` · `/meetings/register` (secret) · `/recordings/complete` (secret) · `/channels/{id}/meetings|recordings|active-meetings` · `/meetings/{id}` · `/meetings/{id}/summary` · `/ai/events` (secret) · `/ai/status` · `/meetings/{id}/ai[/transcript|/session]` · `/support/requests[/{id}/accept|reject|start|end|cancel]` · `/search` · `/admin/overview|health`. Route table: `server/api.go`.

**Main services & ports:** PostgreSQL 5433 · Honco Chat 8065 · meetsvc 8077 · Jitsi web 8443 · Prosody 5280 · JVB 10000/udp · Jibri health 2222.

**Important folders:** `plugins/com.honco.workspace/server/` (Go) · `plugins/com.honco.workspace/webapp/dist/main.js` (UI) · `chat/` (`honcochat.sh`, `meetsvc.py`, `branding/`, `DEBRAND.md`) · `meet/` (Jitsi/Jibri) · `summarize/` · `docs/` · runtime `~/honco-chat/{build,run,logs,pgdata}` · source fork `~/honco-workspace/server`.

**Important files:** `api.go` (routes) · `hardening.go` (security) · `store.go` (migrations) · `tasks_api.go` / `task.go` · `meeting_lifecycle.go` / `muc.go` / `meeting_card.go` · `recordings_api.go` / `recording.go` · `summarizer.go` / `summary_api.go` · `ai.go` / `ai_adapter.go` / `ai_api.go` · `support_api.go` / `support_card.go` · `search.go` · `admin_api.go` · `notify.go` · `plugin.json` · `main.js`.

**How to start:** `wsl.exe -d Ubuntu-22.04` → `cd ~/honco-workspace/chat && ./honcochat.sh start` → `curl http://127.0.0.1:8065/api/v4/system/ping` → (meetings) `cd ~/honco-meet && ~/honco-workspace/meet/meet.sh up` → browser `http://localhost:8065`.

**How to debug:** browser console → Network tab (status code + JSON error) → `~/honco-chat/logs/mattermost.log` (`honco:` lines) → `psql … honcochat` (read-only SELECT) → the file from Part 25. Admin → System health for the services.

**Current blockers:** summarizer host unreachable (Meeting Intelligence) · no AI service (AI Assistant real E2E) · `cloudflared` missing (publish) · Jitsi LAN URL not routable from WSL (health probe only) · RustDesk/transcription on other hosts (not testable here).

> **Most important rule: always inspect the existing implementation before changing it.** Read the handler, the store function, the UI component and the test that covers them — then change the smallest thing, add a migration rather than editing one, keep secrets out of code, and run the tests.

---

*Generated from the codebase at commit `126acc9` on 14 September 2026. Items marked BLOCKED, NOT TESTABLE or "Not verified in current environment" depend on systems outside this machine and were not simulated.*
