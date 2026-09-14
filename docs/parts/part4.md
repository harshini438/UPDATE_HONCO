
---

# 23. Configuration

Live configuration is `~/honco-workspace/server/server/config/config.json` (0600, `.gitignore`d) — read and changed with `mmctl --local config get|set|patch` (never by hand while the server runs). Plugin keys are lowercase in the file and must be *created* with `config patch` (a `config set` on a new key fails). Secrets are written as **[SECRET — NOT DOCUMENTED]**.

| Area | Key | Current value | Notes |
|---|---|---|---|
| Database | `SqlSettings.DriverName` / `DataSource` | `postgres` / [SECRET — NOT DOCUMENTED] (host 127.0.0.1:5433, db `honcochat`) | PostgreSQL 16 in `~/honco-chat/pgdata` |
| Server | `ServiceSettings.ListenAddress` | `:8065` | all interfaces; the WSL host firewall blocks inbound |
| | `ServiceSettings.SiteURL` | `http://localhost:8065` | must become the public hostname when published, or websockets/e-mail links break |
| | `ServiceSettings.ConnectionSecurity` | `""` | TLS terminated outside (tunnel) |
| | `ServiceSettings.EnableMultifactorAuthentication` | `true` | optional per user |
| | `ServiceSettings.SessionLengthWebInHours` | `4320` | 180 days |
| Teams | `TeamSettings.EnableOpenServer` | `false` | no self-signup |
| Files | `FileSettings.DriverName` / `Directory` | `local` / `~/honco-chat/run/data/` | |
| | `FileSettings.MaxFileSize` | `104857600` (100 MB) | also the recording ceiling when `MaxRecordingMB=0` |
| | `FileSettings.EnablePublicLink` | `false` | |
| E-mail | `EmailSettings.SMTPServer` / `SMTPPort` / `ConnectionSecurity` | `smtp.gmail.com` / `587` / `STARTTLS` | credentials [SECRET — NOT DOCUMENTED] |
| | `EmailSettings.SendEmailNotifications` | `true` | |
| | `EmailSettings.RequireEmailVerification` | `false` | |
| | `EmailSettings.PushNotificationServer` | `""` | push not configured; the fork's own push proxy is built but not deployed |
| Plugins | `PluginSettings.EnableUploads` | `true` | needed for `mmctl plugin add` |
| Rate limiting | `RateLimitSettings.Enable` | `false` | Honco's own limiter covers the plugin routes |
| Privacy | `PrivacySettings.ShowEmailAddress` | `true` | members see each other's e-mail |
| **Plugin** `PluginSettings.Plugins.com.honco.workspace` | `jibricallbacksecret` | [SECRET — NOT DOCUMENTED] | must match `/config/honco-callback.secret` in the Jibri container |
| | `meetservicesecret` | [SECRET — NOT DOCUMENTED] | must match `HONCO_SERVICE_SECRET` in `run/meetsvc.env` |
| | `aiserviceurl` | `""` (empty) | AI service not connected |
| | `aiservicetoken` | [SECRET — NOT DOCUMENTED] | bearer token to the AI service |
| | `aicallbacksecret` | [SECRET — NOT DOCUMENTED] (set) | header `X-Honco-AI-Secret` |
| | `summarizercommand` | `""` | local executable alternative to SSH |
| | `summarizerhost` / `summarizerport` / `summarizeruser` / `summarizerkeypath` / `summarizertimeoutseconds` | `192.168.2.150` / `31013` / (default) / [path, not documented] / (default) | the "mother" host — unreachable today |
| | `meetpublicurl` | `https://192.168.1.11:8443` | must be reachable from participants' browsers; equals meetsvc `MEET_BASE` |
| | `prosodyhttpurl` | `http://127.0.0.1:5280` | loopback only |
| | `xmppdomain` | `meet.jitsi` | matches Jitsi `.env` |
| | `supportteamname` / `supportchannelname` | `harshini-sharma` / `honco-support` | the agents are that channel's members |
| | `maxrecordingmb` | unset ⇒ 0 ⇒ Mattermost limit | hard ceiling 2048 |
| meetsvc (`~/honco-chat/run/meetsvc.env`, 0600) | `BOT_TOKEN`, `HONCO_SERVICE_SECRET` | [SECRET — NOT DOCUMENTED] | plus `MEET_BASE`, `PORT` (8077), `SELF_BASE` |
| Jitsi (`~/honco-meet/.env`) | `XMPP_DOMAIN=meet.jitsi`, `PUBLIC_URL=https://localhost:${HTTPS_PORT}`, `ENABLE_RECORDING=1`, `JIBRI_RECORDING_DIR=/storage`, `JIBRI_FINALIZE_RECORDING_SCRIPT_PATH=/config/finalize.sh`, `JIBRI_RECORDING_RESOLUTION=1280x720`, `JIBRI_RECORDING_FRAMERATE=25`; `JIBRI_*_PASSWORD` [SECRET — NOT DOCUMENTED] | | |
| Jibri container | `/config/honco-callback.secret` [SECRET — NOT DOCUMENTED]; optional `/config/honco-chat.host` | | written by `meet/setup-jibri.sh` |
| Cloudflare | `honcochat.sh publish` → `~/honco-chat/bin/cloudflared tunnel --no-autoupdate …` | binary **absent** on this box | quick tunnel; hostname changes on each restart |
| RustDesk | none in Honco Chat | — | relay is a separate deployment on ubuntu-3 |

---

# 24. Running Honco Chat locally

Everything runs as the normal user on WSL `Ubuntu-22.04` (no `sudo`). Nothing below resets or deletes data.

```bash
# 0. Open a WSL shell
wsl.exe -d Ubuntu-22.04

# 1. Start PostgreSQL, the chat server and the /meet service (in that order — the script does it)
cd ~/honco-workspace/chat
./honcochat.sh start
#   postgres:  already up / started (127.0.0.1:5433)
#   honcochat: up  pid=…  SiteURL=http://localhost:8065
#   meetsvc:   up (pid …)

# 2. Verify the database is reachable (read-only)
psql -h 127.0.0.1 -p 5433 -U honco -d honcochat -c "SELECT version FROM honco_schema_migrations ORDER BY version DESC LIMIT 1;"
#   6

# 3. Verify the server
curl -s http://127.0.0.1:8065/api/v4/system/ping        # {"status":"OK", ...}
./honcochat.sh status                                     # postgres / honcochat / tunnel / meetsvc / SiteURL

# 4. Open the browser
#   http://localhost:8065   (or http://<WSL eth0 IP>:8065 from another device on the LAN — see ~/honco-chat/lan-address.sh)
#   Log in with your Mattermost account. The test accounts used in this document (honcotask-alice/bob/dave/admin) are in
#   ~/.honco-test-credentials (0600) and are not documented here.

# 5. Health of every component
~/honco-workspace/chat/healthcheck.sh                     # one line per component; exit 0 = all healthy
#   or, in the app: Honco Workspace → Admin → System health

# 6. Meetings: start the Jitsi/Jibri stack (Docker Desktop must be running)
cd ~/honco-meet
../honco-workspace/meet/meet.sh up          # or: docker compose -f docker-compose.yml -f jibri.yml -f docker-compose.override.yml up -d
../honco-workspace/meet/meet.sh ps
#   after a Docker Desktop restart, Jibri needs --force-recreate (OPERATIONS.md §8)

# 7. Try the features
#   /meet in any channel → a card appears → Join Meeting → (press Record in Jitsi for a recording)
#   Honco Workspace panel → Tasks → New task ; Support → Request Support ; Search
#   Admin (system admin) → health and usage

# 8. Stop (data is kept)
./honcochat.sh stop          # server + meetsvc ;  ./honcochat.sh stop-all also stops PostgreSQL
```

Rebuilding after code changes: plugin — `cd ~/honco-workspace/plugins/com.honco.workspace && go build ./server/ && go vet ./... && go test ./server/...`, then package and `mmctl --local plugin add --force <tar.gz>` (`OPERATIONS.md` §3 / `deploy-plugin.sh`); web client — run the branding script, then `cd ~/honco-workspace/server/webapp/channels && npm run build` (7–17 min; **run it alone**, see §27). Publishing: `./honcochat.sh publish` needs `cloudflared` installed at `~/honco-chat/bin/cloudflared` (not present today).

---

# 25. Testing

All suites live outside the repository: API/shell suites in the session scratchpad (`…\scratchpad\*.sh`, run inside WSL), browser suites in `C:\Users\Dell\AppData\Local\Temp\honco-browser\*.js` (Playwright driving the real Chrome against `http://localhost:8065`), Go tests in `plugins/com.honco.workspace/server/*_test.go`. Results below are from runs on **14 Sep 2026** against commit `126acc9` on a freshly restarted server (earlier runs the same day are noted where they differ).

| Feature | Test | Expected | Status |
|---|---|---|---|
| Plugin unit tests | `go test ./server/...` (79 tests, `go vet`) | all pass | **PASS** 79/79 |
| Authentication | `auth.js` (login, wrong password, empty, logout, back button, forgot/reset, signup off), `authtest.sh` (28), `p9-pwchange.sh` (5) | sessions, resets, refusals correct | **PASS** 20/20 · 28 · 5 |
| Normal chat | `qa-chat.js` (messages, DMs, threads, reactions, two users) | 16 checks | **PASS** 15/16 — the one miss is the test's own logout producing 401s in the console, not a defect |
| Tasks | `tasks-ux.js` (23), `p11-tasks.sh` (8), `p7-paging.sh` (8), `tasks-responsive.js` | create/edit/assign/status/delete/filters/paging, no overflow at 1500/1100 | **PASS** |
| Meetings | `card-e2e.sh` (17), `card-render.js` (8/8, two real Jitsi participants), `lifecycle-e2e.js` (16/16), `qa-meet.sh` (all `/meet` forms) | card, join/leave tracking, 90 s end, 30 min fallback | **PASS** |
| Jibri recording | `lan-record-e2e.js` (real Record → real MP4 delivered), `rec-api-test.sh` (16), `recording-unavailable.js` (8) | recording delivered, card, failure/unavailable states | **PASS** (real delivery verified in the audit run; API/states re-run today) |
| Notifications | `notif-e2e.sh` (14), `notif-phase4.sh` (22), `notifications.js` (9), `notif-phase4.js` (11), `ai-notif-e2e.js` (10) | DMs once, dedupe, due/overdue, summary/AI DMs | **PASS** |
| Meeting Intelligence | `meeting-intel.js` (16), `summary_test.go` | UI, empty state, failed state, deep link | **PARTIAL / BLOCKED** — 5/16 without the stub: the UI, empty and failed states pass; "ready" rendering is verified only with the stub summarizer; real Claude generation is **BLOCKED** (host unreachable) |
| AI Assistant | `ai-api.sh` (94), `ai-e2e.js` (56), `ai-entry-e2e.js` (33), `ai-a11y.js` (31), `ai_test.go` | UI, events, aliases, auth, bounds, entry points, a11y | **PASS** for everything Honco owns; real AI E2E **BLOCKED** (no service) |
| Remote Support | `support-e2e.js` (23, two users), `support-api.sh` (43) | full workflow, agent rules, audit trail, 409 on races | **PASS** — RustDesk itself **NOT TESTABLE** from here |
| Files | `files-e2e.js` (32), `p8-security.sh` (56), `retention.sh` (5) | upload, permissions, deletion, unavailable, admin diagnostics | **PASS** |
| Search | `search-e2e.js` (27), `qa-inj.sh` | categories, scope, private channels, injection | **PASS** 27/27 (one earlier run never got past the login page — timing, re-run green) |
| Admin | `admin-e2e.js` (26), `admin-api.sh` (37), `p9-admin-role.sh` (5) | admin-only, all cards, figures | **PASS** — 22/26 in one run because the Jitsi probe now takes 4 s and the test captured early; all six health cards verified rendering afterwards |
| Profile / photo | `profile-photo-e2e.js` (57), `avatar-e2e.js` (44, two users), `av-api.sh` (29) | section, validation, preview, save/replace/remove, everywhere, cache keys, permissions | **PASS** |
| Security | `p8-security.sh` (56), `p7-idor.sh` (30), `p9-cache-auth.sh` (28), `p9-hardening.sh` (15), `qa-secrets.sh`, `qa-inj.sh`, `qa-jibri.sh` | 401/403/404 where expected, headers, limits, no secret leaks | **PASS** (audit run, same code paths) |
| UI / responsive / themes | `ui-verify.js` (153), `tasks-responsive.js`, `ai-a11y.js`, `profile-photo-e2e.js` widths 1440→390 | no overflow, light/dark, focus, labels | **PASS** |
| Push notifications, mobile/desktop apps | — | — | **NOT IMPLEMENTED / NOT TESTABLE** (no push server; no client source in repo — `CLIENTS.md`) |
| Cloudflare publish | `honcochat.sh publish` | public hostname | **NOT TESTABLE** — `cloudflared` binary absent |

How to run: browser suites `node <suite>.js` from the `honco-browser` folder (Chrome at `C:\Program Files\Google\Chrome\Application\chrome.exe`; credentials read from `\\wsl$\Ubuntu-22.04\home\harshi\.honco-test-credentials`); shell suites `wsl.exe -d Ubuntu-22.04 bash /mnt/c/…/scratchpad/<suite>.sh`; several suites need their fixture script first (`ai-fixture.sh`, `card-e2e.sh`, `p7-search-fixture.sh`, `p8-files-fixture.sh`, `p6-reset-fixture.sh`). Fixtures create channels/tasks named `zz…` and remove what they can; the cleanup scripts are `ai-clean.sh`, `p19-final-clean.sh`, `p8-clean-orphans.sh`.

---

# 26. Current feature status

| Feature | Status | What works | Limitation / blocker |
|---|---|---|---|
| Chat (Mattermost core) | **DONE** | everything upstream provides | Team Edition: no Enterprise features (retention, compliance, LDAP/SAML) |
| De-brand / de-phone-home | **DONE** | scripted, idempotent, `strings` check clean | must be re-run after every upstream pull (`DEBRAND.md`) |
| Tasks | **DONE** | create/edit/assign/status/delete, overdue, filters, paging, DMs | no comments, no attachments, no per-field history |
| `/meet` + meeting cards + lifecycle | **DONE** | instant/scheduled rooms, reminders, cards, join/leave tracking, 90 s end, 30 min fallback, WebSocket updates | participants are Jitsi names (no Mattermost identity); `MeetPublicURL` must be reachable by participants; "View Summary" does not open a closed panel (defect) |
| Jibri recording | **DONE** | delivery, size guard, card, failure states, channel notice | recording must be started by a participant; one Jibri; Docker restart needs Jibri recreate |
| Meeting Intelligence | **BLOCKED (external)** | request, window, transport, parsing, states, UI, DMs, deep link (with a stub) | real summarizer host unreachable → every real generation fails; no real Claude summary exists in the DB |
| AI Assistant | **DONE (UI + infra) / BLOCKED (real E2E)** | panel, entry points, controls, events, KV, WebSocket, DMs, admin card, security | no AI service exists/configured; Honco owns no engine |
| Remote Support | **DONE (workflow)** | request → accept/decline → start/end/cancel, agents from a channel, audit, cards, DMs | RustDesk is external: no API, not verified from here |
| Files & recordings | **DONE** | permissions, unavailable handling, admin diagnostics | no retention policy (by design/edition) |
| Notifications | **DONE** | 20+ kinds, ledger dedupe, e-mail via Mattermost | no push (no push server) |
| Search | **DONE** | five categories, scoped, paged, injection-safe | ILIKE only; not message content |
| Admin dashboard | **DONE** | health, usage, files, notifications, AI, security, failures | read-only; Jitsi probe reports unreachable from WSL today |
| Profile photo | **DONE** | Mattermost storage + API, Profile Photo section, everywhere incl. Honco panels, live updates, permissions | uploader's own session uses a client-side timestamp until the next event (upstream behaviour) |
| Security hardening | **DONE** | auth on every route, secrets, rate limits, headers, IDOR, cross-team | no TLS at the server (tunnel terminates it); no certification |
| Backups / ops scripts | **DONE** | start/stop/status/health/backup/restore/rollback | `publish` needs `cloudflared` (absent) |
| Cloudflare tunnel | **PARTIAL** | script and DNS notes exist | binary absent; quick tunnel only; `SiteURL` must follow |
| Desktop / mobile apps | **PARTIAL** | backend ready (`CLIENTS.md`); fork of mobile de-branded | no client source in this repo; push not configured |
| Transcription worker | **SEPARATE** | `transcribe/` scripts for ubuntu-3 | not integrated with the plugin; not on this host |
| Landing page (`website/`) | **DONE** | static site, env-driven "Open Honco Chat" | not deployed anywhere |

---

# 27. Known limitations

1. **Summarizer host unreachable** (`192.168.2.150`): Meeting Intelligence cannot generate real notes; the UI reports it honestly.
2. **No AI service**: the AI Assistant has nothing to connect to; `aiserviceurl` is empty.
3. **Jitsi public URL not routable from WSL** (`https://192.168.1.11:8443`): the admin probe shows Jitsi "not reachable from this host" although the containers run and meetings work; the Join button depends on participants reaching that address (phones on the LAN, or the operator changing `MeetPublicURL`).
4. **RustDesk**: workflow only; the relay is on another host; no integration and no verification from here.
5. **`cloudflared` missing** on this box: `honcochat.sh publish` cannot run; even when installed it is a *quick* tunnel (new hostname each restart) — production needs a named tunnel and `SiteURL` set to it.
6. **No TLS at the server**; LAN traffic to `:8065` is plain HTTP; Jitsi uses a self-signed certificate.
7. **Push notifications** not configured (no push server); e-mail only.
8. **Desktop/mobile clients**: no source in this repository (`CLIENTS.md`).
9. **No retention policy** for files/recordings; Team Edition has no data-retention job.
10. **"View Summary" on a meeting card does nothing when the Honco panel is closed** (`MeetingIntent.open` never opens the RHS). Works once the panel is open. Found in the QA audit, not fixed.
11. **Meeting participants are Jitsi display names**, not Mattermost users — no avatars, no per-user history.
12. **Building the web client on this box (8 GB WSL VM) starves PostgreSQL**: on 14 Sep a 17-minute build left the server with poisoned pool connections ("pq: there is already a query being processed on this connection") until it was stopped and started. Build alone, then check the log.
13. **After a Docker Desktop restart Jibri must be recreated** (`OPERATIONS.md` §8).
14. **Test data in the database**: QA fixtures (`zz…` channels/meetings, stub summaries, ~1389 notification rows) are present; cleanup scripts exist but were not all run.
15. **Documentation drift**: `HONCO_UPGRADE_PLAN.md` (Sep 9 audit) predates the lifecycle fix, the AI Assistant, the Profile Photo work and this document; `README.md` "Where things run" describes the production layout (ubuntu3 / ubuntu-3 / mother) while today's verified instance runs everything except the summarizer and RustDesk on one WSL box. Where they disagree, this document reflects the code and the running instance.

---

# 28. Troubleshooting

| Symptom | Possible cause | What to check | Safe check | Expected |
|---|---|---|---|---|
| Honco Chat doesn't start | PostgreSQL not up; stale pid file after a WSL restart (pid reused by another process); config error | `~/honco-chat/logs/honcochat.log`, `mattermost.log` | `./honcochat.sh status` then `./honcochat.sh start` (it verifies the pid's command name before trusting a pid file) | `honcochat: up` |
| Database unavailable | Postgres down; wrong port; pool poisoned after heavy load | `pg.log`; `"already a query being processed"` in `mattermost.log` | `psql -h 127.0.0.1 -p 5433 -U honco -d honcochat -c 'select 1'`; if the server logs pool errors, `./honcochat.sh stop` then `start` | `1`; errors stop |
| Port 8065 already in use | a previous server still running | `ss -ltnp \| grep 8065` | `./honcochat.sh status`; stop it with the script, not `kill -9` | one `honcochat` listener |
| Jitsi unavailable | Docker Desktop not running; containers down; `MeetPublicURL` not reachable from where you are | Admin → System health; `meet.sh ps`; `curl -k https://127.0.0.1:8443` | `meet/meet.sh up` | web/prosody/jicofo/jvb `Up` |
| Jibri unavailable / no Record button | Jibri container down or not registered after a Docker restart | `meet.sh logs jibri`; health probe `127.0.0.1:2222` | recreate Jibri (`docker compose … up -d --force-recreate --no-deps jibri` — `OPERATIONS.md` §8) | Jibri "idle" |
| Meeting doesn't appear after `/meet` | meetsvc down; wrong `HONCO_SERVICE_SECRET` (register 401); bot token invalid | `~/honco-chat/logs/honcochat.log` (meetsvc lines); `mattermost.log` for "registration rejected" | `./honcochat.sh status` → meetsvc up; compare the secret in `run/meetsvc.env` with the plugin setting (do not print them) | card posts |
| Recording unavailable / never arrives | nobody pressed Record; zero media captured; callback secret mismatch (401); file over the limit (failed); file deleted | `/storage/finalize.log` inside Jibri; `mattermost.log` "recording callback rejected"; card badge | `meet.sh exec jibri cat /storage/finalize.log` | "delivered to Honco Chat" |
| AI Assistant offline / not configured | `aiserviceurl` empty (today) or service unreachable; callback secret unset | Admin → AI Assistant card | `mmctl --local config get PluginSettings.Plugins.com.honco.workspace.aiserviceurl` | shows the configured URL; otherwise expected |
| Summary unavailable / "not reachable" | summarizer host down or unreachable (today); key path wrong; timeout | Admin → Recent failures (summary); `mattermost.log` | `nc -z -w2 192.168.2.150 22` from WSL | reachable → retry generation |
| Support: nobody can accept | `SupportTeamName`/`SupportChannelName` blank or wrong; agent not in that channel | Admin → Security "support channel configured" | add the agent to the support channel | Accept Request appears |
| Avatar not updating | browser cached an old key (only possible for a URL without `?_=`); client stamp on the uploader's own session | `GET /api/v4/users/<id>` → `last_picture_update` | reload the page | avatars re-key |
| Files unavailable | user not a channel member; post deleted; storage path moved | card "Recording unavailable"; Admin → Files "recordings missing file" | `ls ~/honco-chat/run/data` | files present |
| WebSocket disconnected (cards/panels stale) | server restarted; tunnel hostname changed while `SiteURL` still old | browser console; `SiteURL` vs the URL in use | `mmctl --local config get ServiceSettings.SiteURL`; the plugin's reconnect handler refreshes panels once the socket returns | live updates resume |

Never: `git reset --hard`, dropping/truncating tables, deleting `pgdata` or `run/data`, editing `config.json` while the server runs.

---

# 29. Common user flows

Each flow: **USER ACTION** → *WHAT THE SYSTEM DOES*.

**A. Send a message** — type in the composer, Enter → *Mattermost `POST /api/v4/posts`; stored in `posts`; broadcast `posted` on the WebSocket; mentions notified.*

**B. Create a task** — Honco Workspace → Tasks → New task → Title (+ Description, Assignee, Status, Due) → Save task → *`POST /tasks`; team membership and assignee checked; `INSERT honco_tasks`; DM to the assignee (once); row appears.*

**C. Assign a task** — Edit → choose Assignee → Save task → *`PATCH /tasks/{id}` with `assignee_id`; only creator/assignee may; new assignee DM `task_assigned`, previous DM `task_reassigned`.*

**D. Schedule a meeting** — `/meet in 30m Client review` (or `/meet at 15:00 …`, `/meet schedule`) → *meetsvc stores the reminder in `meet-schedule.json`, registers the room (status scheduled, `scheduled_at`), posts the card; at the time it posts the reminder with the link.*

**E. Join a meeting** — click **Join Meeting** on the card → *new tab to `MeetPublicURL/<room>`; Jitsi prejoin; the plugin's poller sees the occupant within 10 s, stamps `started_at`, replies "started"/"joined" in the thread, updates the card for everyone.*

**F. Record a meeting** — in Jitsi: ⋯ → Start recording → later Stop → *Jibri joins as a hidden participant, records 1280×720; on stop writes the MP4 and runs `finalize.sh` → `POST /recordings/complete` with the secret → stored via the file API → card shows View Recording; channel post "Recording ready for …".*

**G. View recording** — click **View Recording** → *`GET /api/v4/files/<id>`; Mattermost checks channel membership; plays inline.*

**H. Generate meeting summary** — Honco Workspace → Meetings → choose the meeting → Generate summary → *`POST /meetings/{id}/summary`; conversation window collected; summarizer called over SSH (today: fails "not reachable"); on success sections render, card gets View Summary, DM with permalink.*

**I. Start AI Assistant** — card → AI Assistant (or App Bar icon) → Start AI session → *`POST /meetings/{id}/ai/session` → service `/v1/sessions` (today: not configured → honest offline state); when connected, events arrive on `/ai/events` and stream to the panel by WebSocket; final → DM.*

**J. Create support request** — Honco Workspace → Support → Request Support → describe → Send → *`POST /support/requests`; `honco_support_requests` open + event; support card in the channel; support channel notified.*

**K. Accept support request** — (agent) Support → Accept Request → Start Session → … → End Session → *transitions `accept`/`start`/`end`; events logged; requester DM'd at each step; card shows the RustDesk hint while active; the screen session itself happens in RustDesk.*

**L. Search** — Honco Workspace → Search → term → Enter → *`GET /search?type=all`; scope = my teams/channels; grouped results; click opens the item.*

**M. Upload file** — paperclip in the composer → choose file → send → *Mattermost `POST /api/v4/files` (≤ 100 MB) → `fileinfo` + `run/data/`; visible to channel members only; no public links.*

**N. Admin health check** — (admin) Honco Workspace → Admin → *`GET /admin/overview` + `GET /admin/health`; six live probes (Jitsi may take 4 s); counts from the tables; Refresh re-runs both.*

---

# 30. Quick reference — "Where do I go if I want to…"

| I want to… | Go to |
|---|---|
| Create a task | Honco Workspace → **Tasks → New task** |
| Change task rules or fields | `server/task.go`, `server/tasks_api.go`; UI `main.js` `TaskForm`/`TaskRow` |
| Change meeting behaviour (poll, 90 s, 30 min) | `server/meeting_lifecycle.go` (`participantPollInterval`, `emptyRoomGrace`, `neverJoinedTimeout`) |
| Change `/meet` syntax or reminders | `chat/meetsvc.py` (then restart with `honcochat.sh stop`/`start`) |
| Change the meeting card | `server/meeting_card.go` + `main.js` `MeetingCard` |
| Change a notification text or recipient | `server/notify.go` (tasks, summaries), `recordings_api.go` (recordings), `support_card.go` (support), `ai_api.go` (AI) |
| Change the AI Assistant UI | `main.js` `AIPanel` and friends; `.hw-ai-*` styles in the same file |
| Change the AI integration (service URL, events, secret) | `server/ai_adapter.go`, `ai_api.go`, `ai.go`; config keys in `plugin.json`; contract `AI_INTEGRATION.md` |
| Change the support workflow or who is an agent | `server/support.go`/`support_api.go`; agents = members of `SupportChannelName` |
| Change the admin dashboard | `server/admin_api.go`, `admin_store.go`, `files_admin.go`; UI `main.js` `AdminTab` |
| Change search | `server/search.go` (queries/scope), `search_api.go`; UI `SearchTab` |
| Change profile/avatar wording | `chat/branding/profile-photo.py` → rebuild the web client |
| Change avatars in Honco panels | `main.js` `UserAvatar` |
| Change Jibri (limits, hook, resolution) | `server/recording.go` (`MaxRecordingMB`), `meet/jibri-finalize.sh`, `meet/setup-jibri.sh`, `~/honco-meet/.env` |
| Add a database migration | append to `migrations` in `server/store.go`; never edit a shipped entry; plugin redeploy applies it |
| Add a plugin config key | `plugin.json` settings schema + config struct in `recordings_api.go`; create it with `mmctl --local config patch` |
| Add a route | `server/api.go` `newRouter` (+ `hardening.go` if it is a service or push route) |
| Re-apply the de-brand after an upstream pull | `chat/DEBRAND.md` — run the scripts in order, rebuild, `strings` check |
| Start / stop / status | `~/honco-workspace/chat/honcochat.sh {start|stop|stop-all|status|publish}` |
| See health | `healthcheck.sh` or Admin → System health |
| Back up | `chat/backup-honcochat.sh` (`OPERATIONS.md` §6) |
| Find the logs | `~/honco-chat/logs/{mattermost,honcochat,pg,tunnel}.log`; Jibri `/storage/finalize.log` |

---

*End of document. Generated from the codebase at commit `126acc9` on 14 September 2026. Items marked BLOCKED or NOT TESTABLE depend on systems outside this machine and were not simulated.*
