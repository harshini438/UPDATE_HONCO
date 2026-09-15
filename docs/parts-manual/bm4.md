
---

# 18. Button → code map

Every path verified against the working tree at commit `126acc9`. Frontend file: `plugins/com.honco.workspace/webapp/dist/main.js`. Backend directory: `plugins/com.honco.workspace/server/`. API prefix: `/plugins/com.honco.workspace/api/v1`.

| Button | Frontend | API | Backend | DB / Service |
|---|---|---|---|---|
| Honco Workspace (App Bar) | `main.js` `Plugin.initialize`, `HoncoPanel` | — | — | — |
| AI Assistant (App Bar) | `main.js` `AIIntent`, `AIPanel` | `GET /channels/{id}/meetings`, `GET /meetings/{id}/ai` | `meetings_api.go`, `ai_api.go` | `honco_meetings`, KV store |
| New task | `main.js` `TasksPanel`, `TaskEditor` | — (Mattermost `GET /api/v4/users?in_team=`) | — | — |
| Save task (create) | `main.js` `TaskEditor` | `POST /tasks` | `tasks_api.go` `handleCreateTask` | `honco_tasks`, `honco_notifications` |
| Edit task | `main.js` `TaskEditor` | `PATCH /tasks/{id}` | `tasks_api.go` `handleUpdateTask` | `honco_tasks` |
| Assignee ▾ | `main.js` `TaskEditor`, `fetchTeamMembers` | `POST /tasks` · `PATCH /tasks/{id}` | `tasks_api.go`, `notify.go` | `honco_tasks`, `honco_notifications` |
| Status ▾ | `main.js` `TaskRow` | `PUT /tasks/{id}/status` | `tasks_api.go` `handleSetTaskStatus` | `honco_tasks`, `honco_notifications` |
| Delete → Yes | `main.js` `TaskRow` | `DELETE /tasks/{id}` | `tasks_api.go` `handleDeleteTask` | `honco_tasks` (soft) |
| Filters / paging | `main.js` `TasksPanel` | `GET /tasks?…` | `tasks_api.go` `handleListTasks`, `task.go` | `honco_tasks` |
| `/meet` | Mattermost command · card by `main.js` `MeetingCard` | `POST /meetings/register` (service secret) | `chat/meetsvc.py` → `recordings_api.go` `handleRegisterMeeting`, `meeting_card.go` | `honco_meetings`, `posts` · **Jitsi** |
| Join Meeting | `main.js` `MeetingCard` (link) | — | `meeting_lifecycle.go`, `muc.go` (afterwards) | `honco_meeting_participants` · **Jitsi** |
| AI Assistant (card) | `main.js` `MeetingCard` → `AIIntent` | `GET /meetings/{id}/ai` | `ai_api.go` | KV store |
| View Summary (card) | `main.js` `MeetingCard` → `MeetingIntent` | `GET /meetings/{id}/summary` | `summary_api.go` | `honco_meeting_summaries` |
| View Recording (card) | `main.js` `MeetingCard` (link) | Mattermost `GET /api/v4/files/{id}` | Mattermost file service | `honco_recordings` → `fileinfo` |
| Choose a meeting ▾ | `main.js` `MeetingPanel` | `GET /channels/{id}/meetings` | `summary_api.go`, `meetings_api.go` | `honco_meetings`, `honco_meeting_summaries` |
| Generate summary / Regenerate / Try again | `main.js` `MeetingPanel` | `POST` + `GET /meetings/{id}/summary` | `summary_api.go`, `summarizer.go`, `meeting_summary.go` | `honco_meeting_summaries` · **Claude summarizer (SSH)** |
| Start / Start new AI session | `main.js` `AIPanel` `startSession` | `POST /meetings/{id}/ai/session` | `ai_api.go`, `ai_adapter.go` | KV store · **external AI service** |
| Stop session | `main.js` `AIPanel` `stopSession` | `DELETE /meetings/{id}/ai/session` | `ai_api.go`, `ai_adapter.go` | KV store · **external AI service** |
| Reconnect | `main.js` `AIPanel` `reconnect` | `GET /meetings/{id}/ai` | `ai_api.go` | KV store |
| Load earlier lines | `main.js` `TranscriptBlock` | `GET /meetings/{id}/ai/transcript?before=` | `ai_api.go` `handleGetAITranscript` | KV store |
| Request Support (send) | `main.js` `SupportPanel` | `POST /support/requests` | `support_api.go`, `support_card.go` | `honco_support_requests`, `honco_support_events` |
| Accept / Decline | `main.js` `SupportPanel` | `POST /support/requests/{id}/accept` · `/reject` | `support_api.go` | both support tables |
| Start / End Session | `main.js` `SupportPanel` | `POST …/start` · `/end` | `support_api.go` | both support tables · **RustDesk (out of band)** |
| Cancel (support) | `main.js` `SupportPanel` | `POST …/cancel` | `support_api.go` | both support tables |
| Search + chips | `main.js` `SearchPanel` | `GET /search?q=&type=&page=` | `search_api.go`, `search.go` | five `honco_*` tables |
| Admin tab / Refresh | `main.js` `AdminPanel` | `GET /admin/overview`, `GET /admin/health` | `admin_api.go`, `admin_store.go`, `files_admin.go` | all `honco_*` + Mattermost tables |
| Profile Photo (Change/Save/Remove) | Mattermost `setting_picture.tsx`, `user_settings_general.tsx` (patched by `chat/branding/profile-photo.py`) | `POST` / `DELETE /api/v4/users/{id}/image` | Mattermost `channels/api4/user.go` | `Users.LastPictureUpdate` + file store |
| Attach file / Send | Mattermost composer | `POST /api/v4/files`, `POST /api/v4/posts` | Mattermost | `fileinfo`, `posts` |

---

# 19. Button → database map

| Feature | Button | Table | Operation |
|---|---|---|---|
| Task | Save task (create) | `honco_tasks` | INSERT |
| Task | Edit → Save | `honco_tasks` | UPDATE |
| Task | Status ▾ | `honco_tasks` | UPDATE (status, updated_at) |
| Task | Delete → Yes | `honco_tasks` | **UPDATE — soft delete** (`deleted_at` set; the row is never removed) |
| Task | Filters / paging | `honco_tasks` | SELECT |
| Task | (assignment side effect) | `honco_notifications` | INSERT (claim; a duplicate is refused by a unique index) |
| Meeting | `/meet` | `honco_meetings` | INSERT (+ the card post in Mattermost's `posts`) |
| Meeting | Join / leave (no button — the poller) | `honco_meeting_participants` | INSERT / UPDATE (`left_at`, `present`) |
| Meeting | lifecycle transitions | `honco_meetings` | UPDATE (`started_at`, `ended_at`, `status`, `participant_count`) |
| Recording | Jibri callback (no UI button) | `honco_recordings` | INSERT (`ready` or `failed`) + Mattermost `fileinfo` |
| Recording | View Recording | `fileinfo` | SELECT (Mattermost file read) |
| Meeting Intelligence | Generate summary | `honco_meeting_summaries` | INSERT or UPDATE → `pending` → `ready` / `empty` / `failed` |
| AI Assistant | Start / Stop session | *(no table)* plugin key-value store `ai:session:<meeting_id>` | SET / DELETE |
| AI Assistant | service push `/ai/events` | same KV entry | UPDATE (append) |
| Support | Request Support | `honco_support_requests` + `honco_support_events` | INSERT + INSERT (`created`) |
| Support | Accept / Decline / Start / End / Cancel | `honco_support_requests` (UPDATE) + `honco_support_events` (INSERT) | UPDATE + INSERT |
| Search | any search | 5 tables above | SELECT (ILIKE, scoped) |
| Admin | Admin tab / Refresh | all `honco_*` + Mattermost tables | SELECT (counts only) |
| Profile photo | Save / Remove photo | `Users.LastPictureUpdate` (+ file store) | UPDATE — **no Honco table** |
| Notifications | every notified event | `honco_notifications` | INSERT (claim); rows older than 90 days pruned daily |

Nine Honco tables in total: `honco_tasks`, `honco_meetings`, `honco_meeting_participants`, `honco_recordings`, `honco_meeting_summaries`, `honco_notifications`, `honco_support_requests`, `honco_support_events`, `honco_schema_migrations` (migration version, currently **6**).

---

# 20. Button → external service map

| UI action | External service | Purpose | Reachable today? |
|---|---|---|---|
| Join Meeting | **Jitsi** (Docker, web `:8443`) | the video room itself | Containers up. The address used (`https://192.168.1.11:8443`) is a LAN address — reachable from LAN browsers, **not** from inside the WSL machine (which is why the admin probe says "not reachable from this host"). |
| (no button) participant tracking | **Jitsi Prosody** (`127.0.0.1:5280`) | who is in the room, polled every 10 s | Yes |
| Start recording (in Jitsi) → callback | **Jibri** (Docker) | records the MP4 and delivers it to Honco | Yes — health "idle"; a real recording was delivered and plays (HTTP 200, `video/mp4`) |
| Generate summary | **Claude summarizer** on "mother" (`192.168.2.150`, SSH) | writes the meeting notes | **No** — verified today: status `failed`, "The summarizer is not reachable from this server." |
| Start AI session | **External AI service** (`AIServiceURL`) | live transcript, suggestions, insights, topics, final summary | **Not configured** — the control is not even rendered |
| Start Session (support) | **RustDesk** relay/clients | the actual screen control | Separate host, no API integration — **not verified from here** |
| (no button) notification e-mail | **SMTP** `smtp.gmail.com:587` | e-mail notifications, password reset | Configured; **delivery not exercised in this pass** |
| (no button) public access | **Cloudflare tunnel** | a public hostname for the server | `cloudflared` binary is not installed; tunnel `down` |

---

# 21. Complete data flow examples

Format: **USER ACTION → FRONTEND → API → BACKEND → DATABASE / SERVICE → RESPONSE → UI.**

1. **Send a message** — type + Enter → Mattermost composer → `POST /api/v4/posts` → Mattermost post service → `posts` → 201 → the message appears for everyone (WebSocket `posted`).
2. **Create a task** — New task, fill, Save → `TaskEditor` → `POST /tasks` → `tasks_api.go` (team + assignee checks) → `INSERT honco_tasks` (+ `honco_notifications`) → 201 → the row appears; the assignee gets a DM.
3. **Assign a task** — pick in Assignee ▾, Save → `TaskEditor` → `POST`/`PATCH /tasks` → `tasks_api.go` re-checks team membership → `honco_tasks.assignee_id` → 200 → the row shows the new assignee and their photo; DMs to the new and previous assignee.
4. **Schedule a meeting** — `/meet in 30m Review` → Mattermost command → meetsvc → `POST /meetings/register` → `INSERT honco_meetings` (`scheduled`) → ephemeral reply + card → at the time, meetsvc posts the reminder.
5. **Join a meeting** — click Join Meeting → browser opens `join_url` → (no Honco call) → Jitsi room → within 10 s the poller writes `honco_meeting_participants` and `started_at` → WebSocket `meeting_updated` → the card reads "Meeting is active · N participants".
6. **Participant joins** — Jitsi presence changes → poller (`muc.go` asks Prosody) → `INSERT honco_meeting_participants` → thread reply "joined" → `meeting_updated` → the count and names update.
7. **Participant leaves** — presence disappears → poller → `left_at`, `present=false` → "left" reply → card updates; if the room is now empty the 90-second grace starts.
8. **Record a meeting** — a participant presses Record in Jitsi → Jicofo assigns Jibri → Jibri records to `/storage/<id>/…mp4`.
9. **Finish a recording** — recording stops → Jibri runs `finalize.sh` → `POST /recordings/complete` (secret, multipart) → `recordings_api.go` validates size and room → uploads via Mattermost's file API → `INSERT honco_recordings` + `fileinfo` → the card gains **View Recording**; the bot posts "Recording ready for …".
10. **View a recording** — click View Recording → `GET /api/v4/files/{id}` → Mattermost checks channel membership → the MP4 streams → it plays inline (**verified: HTTP 200 `video/mp4`**).
11. **Generate a meeting summary** — Generate summary → `POST /meetings/{id}/summary` → `summary_api.go` + `summarizer.go` collect the channel conversation (bot posts excluded) → row `pending` → SSH to the summarizer → **today: failure** → row `failed` → the panel shows "The summarizer is not reachable from this server." + Try again; a DM reports the failure.
12. **Start the AI Assistant** — (when a service is configured) Start AI session → `POST /meetings/{id}/ai/session` → `ai_adapter.go` → `POST {AIServiceURL}/v1/sessions` → session in the KV store → status *Connecting*. **Today the control is not rendered** because no service URL is set.
13. **Receive an AI transcript event** — the service → `POST /ai/events` with `X-Honco-AI-Secret` → `ai_api.go` validates secret, meeting and size → appended to the session → WebSocket `ai_event` → the line appears in the panel.
14. **Receive an AI suggestion** — same path with `event: "suggestion"` → rendered as a "Sales suggestion" card with a New tag; older ones collapse behind "Show previous suggestions (N)".
15. **Create a support request** — Request Support → describe → Send → `POST /support/requests` → `INSERT honco_support_requests` (`open`) + `honco_support_events` (`created`) → support card posted, support channel told → WebSocket `support_updated`.
16. **Accept a support request** — (agent) Accept Request → `POST …/accept` → agent membership re-checked → `accepted`, `agent_id` set, event written → DM to the requester → both views update.
17. **Start support** — Start Session → `POST …/start` → `active`, `started_at` → the card shows the RustDesk hint → the screen session then happens **in RustDesk**, outside Honco.
18. **Upload a file** — 📎, choose, send → `POST /api/v4/files` → `fileinfo` + bytes under `run/data/` → the post carries the file id → visible to channel members only.
19. **Search** — type + Enter → `GET /search?q=…&type=all` → `searchScope` limits to your teams and channels → ILIKE over the five tables → grouped results → click a hit to open it (**verified today**).
20. **Change the profile photo** — Change photo → preview → Save photo → `POST /api/v4/users/{me}/image` → stored under your user id, `Users.LastPictureUpdate` bumped → `user_updated` broadcast → every client re-keys the URL → your photo changes everywhere, including Honco rows and cards.
21. **Admin health check** — open the Admin tab (or Refresh) → `GET /admin/overview` + `GET /admin/health` → `manage_system` checked → counts read, six probes run → the cards render, unreachable components in red (**verified today**).

---

# 22. What is Mattermost vs what is Honco?

**MATTERMOST NATIVE** — accounts, login, MFA, sessions, password reset · teams, public/private channels, DMs and group DMs · messages, threads, reactions, mentions, message priority, saved messages · file upload/download and storage · message and file search · desktop/e-mail notification delivery · profile settings and the profile-picture API and storage · the System Console · the plugin framework, REST API and WebSocket.

**HONCO CUSTOM** — the Honco Workspace panel (Tasks · Meetings · AI Assistant · Support · Search · Admin) · meeting cards and support cards as custom post types · the meeting lifecycle poller and its thread replies · the `/meet` command service (`meetsvc.py`) · the recording callback and recording cards · Meeting Intelligence · the AI Assistant UI and integration boundary · the Remote Support workflow and audit trail · Honco global search · the Admin dashboard · the notification ledger and the honco bot's messages · nine PostgreSQL tables · the security layer on the plugin routes (auth check, rate limits, headers) · the de-brand scripts (telemetry, crash reporting, update checker, marketplace, hosted push removed; Honco wordmark) · the "Profile Photo" wording and its success/failure messages · the operations scripts.

**HONCO INTEGRATION (things Honco talks to, but does not own)** — Jitsi (rooms), Jibri (recording), the Claude summarizer on "mother" (notes), the external AI service (live assistance), RustDesk (remote control), SMTP (e-mail), Cloudflare tunnel (public access).

---

# 23. Code navigation

| If I want to change… | Open |
|---|---|
| **Tasks** UI | `plugins/com.honco.workspace/webapp/dist/main.js` → `TasksPanel`, `TaskEditor`, `TaskRow` |
| Tasks rules / validation / page sizes | `plugins/com.honco.workspace/server/task.go` |
| Tasks HTTP handlers | `plugins/com.honco.workspace/server/tasks_api.go` |
| **Meeting lifecycle** (10 s poll, 90 s grace, 30 min fallback) | `server/meeting_lifecycle.go` (+ `server/muc.go` for the Prosody query) |
| **Meeting card** (props, thread replies, WebSocket) | `server/meeting_card.go`; drawing: `main.js` → `MeetingCard` |
| `/meet` command, scheduling, reminders | `chat/meetsvc.py` |
| Meeting registration + recording callback | `server/recordings_api.go` |
| **Jibri** limits and statuses | `server/recording.go`; hook `meet/jibri-finalize.sh`; install `meet/setup-jibri.sh`; stack `meet/meet.sh`, `meet/docker-compose.override.yml` |
| **Notifications** | `server/notify.go` (tasks, summaries, due scanner) · `server/recordings_api.go` (recording posts) · `server/support_card.go` (support) · `server/ai_api.go` (AI DMs) |
| **AI Assistant** UI | `main.js` → `AIPanel`, `AILive`, `AIFinished`, `SuggestionCard`, `TranscriptBlock`, `AIIntent` |
| AI integration (service calls, events, secrets) | `server/ai_adapter.go`, `server/ai_api.go`, `server/ai.go`; contract `AI_INTEGRATION.md` |
| **Meeting Summary** | `server/summarizer.go`, `server/meeting_summary.go`, `server/summary_api.go`; `summarize/honco-summarize.sh` |
| **Remote Support** | `server/support.go`, `server/support_api.go`, `server/support_card.go`; UI `main.js` → `SupportPanel`, `SupportCard` |
| **Search** | `server/search.go`, `server/search_api.go`; UI `main.js` → `SearchPanel` |
| **Admin** | `server/admin_api.go`, `server/admin_store.go`, `server/files_admin.go`; UI `main.js` → `AdminPanel` |
| **Profile photo** wording/behaviour | `chat/branding/profile-photo.py`, then rebuild the web client |
| Avatars inside Honco panels | `main.js` → `UserAvatar`, `pictureUpdateOf` |
| **API routes** (the table of every URL) | `plugins/com.honco.workspace/server/api.go` → `newRouter` |
| Security layer (headers, rate limits, service routes) | `server/hardening.go`; auth wrapper in `server/plugin.go` / `api.go` |
| **Database migrations** | `plugins/com.honco.workspace/server/store.go` → `var migrations` (append only — never edit a shipped entry) |
| Plugin settings/config keys | `plugins/com.honco.workspace/plugin.json` |
| Start / stop / publish | `chat/honcochat.sh` |

---

# 24. Debugging a button

The method is always the same: **browser console → Network request → HTTP status → payload → API handler → server log → PostgreSQL → response → frontend state.**

**Worked example — "I clicked Create Task but nothing happened."**
1. **Browser console** (F12 → Console): a red error from `main.js`? Note the line number.
2. **Network tab**: is there a `POST …/api/v1/tasks`? No request at all means the click never reached `fetch` — look at `TaskEditor`'s submit handler.
3. **HTTP status**: `400` validation · `401` session expired (reload) · `403` not a team member, or not creator/assignee · `429` rate limited (wait 10 s) · `500` server problem.
4. **Request payload**: does it carry `team_id` and a non-empty `title`?
5. **Response body**: `{"error":"…"}` is the exact message the panel shows.
6. **API handler**: `server/tasks_api.go` → `handleCreateTask` — the checks run in the order team → assignee → validation.
7. **Server log**: `~/honco-chat/logs/mattermost.log`, look for `honco:` lines near that timestamp.
8. **PostgreSQL** (read-only): `SELECT id,title,status,deleted_at FROM honco_tasks ORDER BY created_at DESC LIMIT 5;`
9. **Frontend state**: after a success the panel reloads the list — if the row is in the table but not on screen, the list query (filter/page) is hiding it.

| Feature | Same method, feature-specific checks |
|---|---|
| **Meeting** | Did Mattermost report a command error (meetsvc down — `./honcochat.sh status`)? `honcochat.log` for "register … 401" (service secret mismatch). `SELECT room_name,status FROM honco_meetings ORDER BY created_at DESC LIMIT 3;` Card exists but Join fails → that is the browser vs `MeetPublicURL`, not Honco. |
| **Recording** | Was Record actually pressed in Jitsi? `meet.sh exec jibri cat /storage/finalize.log` → "delivered to Honco Chat" / "cannot authenticate" / nothing (no media). `mattermost.log` for "recording callback rejected" or "too large". `SELECT status,error_message FROM honco_recordings ORDER BY created_at DESC LIMIT 3;` |
| **AI** | Admin → AI card: *service configured?* If false, the Start control does not exist — that is expected, not a bug. If true: `ai_adapter.go` errors in the log (secrets are redacted). Pushes rejected → the service must send `X-Honco-AI-Secret` (401) and stay under 300/min (429). `GET /meetings/{id}/ai` shows the stored session. |
| **Support** | Admin → Security: "support channel configured"? Is the agent a **member of that channel**? 409 on Accept means someone else acted first. `SELECT status,agent_id FROM honco_support_requests ORDER BY created_at DESC LIMIT 3;` and `honco_support_events`. |
| **Profile photo** | `GET /api/v4/users/<id>` → did `last_picture_update` change? Image requests must carry `?_=<timestamp>`. Other people update through the `user_updated` event; if their socket is down they update on reload. |
| **Search** | Are you a member of the team/channel that owns the item (scope is by membership, by design)? Is the term a substring of the title/topic/issue? Message text is not searched here. `GET /search?q=…` shows the pages and totals. |

---

# 25. Common button problems

| Button | Problem | Likely cause | Where to check | Expected behaviour |
|---|---|---|---|---|
| Join Meeting | opens a tab that never loads | `MeetPublicURL` (`https://192.168.1.11:8443`) not reachable from that browser; self-signed certificate not accepted; Jitsi down | `meet/meet.sh ps`; open the URL directly | Jitsi prejoin screen |
| Join Meeting | button missing | the meeting is `ended` | the card's status line | the button only exists while the meeting is not ended |
| AI Assistant | says "not set up"/offers no Start | `AIServiceURL` empty (current state) | Admin → AI card | Start appears only when a service URL is configured **and** the meeting is live |
| Generate summary | "The summarizer is not reachable from this server." | the summarizer host is unreachable (current state) | `nc -z 192.168.2.150 31013`; `mattermost.log` "summariser failed" | notes, or this honest failure with **Try again** |
| Generate summary | "No messages were posted…" | there was no human conversation in the meeting's window (bot posts do not count) | the channel during the meeting | `empty` is the correct result, not an error |
| View Summary | nothing happens | **known defect**: only works when the Honco panel is already open | open the panel, then click again | should open the panel — one-line fix in `MeetingIntent.open` |
| View Summary | button missing | no summary, or the summary is `failed`/`empty` (`has_summary` false) | Meetings tab | correct behaviour |
| View Recording | "Recording unavailable" | the file was deleted | Admin → Files ("recordings missing file") | a badge instead of a broken player |
| View Recording | "Recording failed" badge | the recording was refused (usually too large) or the callback failed | `honco_recordings.error_message` | the reason is shown, never silent |
| Task doesn't save | inline error under the form | title empty/too long, bad status, assignee not in the team | the message itself | the message tells you which |
| Task Edit/Delete | 403 | you are not the creator (delete) or creator/assignee (edit) | the row's creator | only those people may change it |
| Profile photo | doesn't update for others | their WebSocket is down | reload their page | it should change without a reload |
| Profile photo | "Unable to update profile photo." | the server refused the file (not a decodable image) | try a real JPG/PNG/BMP | a specific message for type/size, this generic one for server refusal |
| Search | returns nothing | you are not a member of the owning team/channel, or it is a message (not a Honco object) | membership; use the top search box for messages | scoped results, or "No Honco results for …" |
| Support | no Accept button | you are not a member of the support channel | the configured support channel | only agents see the queue controls |
| Support | 409 on Accept | another agent acted first | refresh the list | one agent per request |
| Notification missing | expected a second DM | the ledger prevents duplicates by design | `honco_notifications` for the dedupe key | one message per event |
| Notification missing | no e-mail | you were online, or e-mail notifications are off | ⚙ Settings → Notifications | e-mail only when away/offline with it enabled |
| Admin tab missing | you are not a system administrator | `manage_system` permission | System Console → the user's roles | hidden **and** the API returns 403 |

---

# 26. Feature status

| Feature | Status | Verified behaviour | Blocker |
|---|---|---|---|
| Chat, channels, DMs, threads, files (Mattermost) | **DONE** | unchanged upstream behaviour; exercised by the regression suites | — |
| De-brand / telemetry removal | **DONE** | scripted and idempotent | — |
| Tasks (create, edit, assign, status, delete, filter, page) | **DONE** | every control clicked today; DB verified after each | — |
| `/meet` and meeting cards | **DONE** | three meetings created today; cards rendered; Join link correct | — |
| Meeting lifecycle (90 s empty-room, 30 min fallback) | **DONE** | verified previously with two real participants (ended 108 s after empty; fallback 30.0–30.1 min) | — |
| Jibri recording delivery | **DONE** | a real recording plays: HTTP 200 `video/mp4` | needs a participant to press Record; Jibri must be recreated after a Docker restart |
| Meeting Intelligence (Honco side) | **DONE** | request, window, states, UI, Try again, DM all work; `empty` path verified | — |
| Meeting Intelligence (real notes) | **BLOCKED** | forced run with 4 real messages → `failed`, "not reachable" | summarizer host `192.168.2.150` unreachable |
| AI Assistant (UI + integration) | **DONE** | panel, states, meeting selector, Reconnect, transcript section verified | — |
| AI Assistant (Start/Stop, live session) | **NOT TESTABLE** | the controls are not rendered without a configured service | no AI service exists/configured |
| Remote Support workflow | **DONE** | create → accept → start → end verified with two users; audit trail complete | — |
| Remote Support (actual screen control) | **NOT TESTABLE** | — | RustDesk relay on another host, no API integration |
| Files & attachments | **DONE** | permissions, unavailable handling, admin diagnostics | no retention policy (Team Edition) |
| Notifications (in-app) | **DONE** | DMs, channel posts, thread replies, dedupe ledger | — |
| Notifications (e-mail) | **PARTIAL** | SMTP configured (`smtp_configured: true`) | delivery not exercised in this pass |
| Notifications (push) | **NOT IMPLEMENTED** | — | no push server configured |
| Honco global search | **DONE** | Enter and the category chips verified on the wire | substring matching only; no message text |
| Admin dashboard | **DONE** | both calls, Refresh, and the 403 for a normal user verified | Jitsi probe reports unreachable from WSL |
| Profile photo | **DONE** | verified previously with two users (57/57 and 44/44) | — |
| Security (auth, IDOR, secrets, rate limits, headers) | **DONE** | suites green; admin 403 re-verified today | — |
| Cloudflare publish | **PARTIAL** | script exists | `cloudflared` binary not installed |
| Desktop / mobile apps | **PARTIAL** | backend ready | no client source in this repository; push not configured |
| Transcription worker (`transcribe/`) | **NOT TESTABLE** | — | separate host; not called by the plugin |

---

# 27. Important limitations

1. **Claude summarizer unreachable** — `192.168.2.150:31013` does not answer from this machine; real meeting notes cannot be produced. Verified 15 Sep 2026.
2. **AI service not configured** — `AIServiceURL` is empty, so **Start / Stop / Retry connection are not rendered at all** and no live AI session is possible.
3. **RustDesk session unverified** — Honco manages the workflow only; the relay is on another host and was not exercised from here.
4. **SMTP delivery unverified** — configuration is present; no e-mail was sent or received in this pass.
5. **Push notifications unavailable** — no push server is configured.
6. **Cloudflare publish incomplete** — `~/honco-chat/bin/cloudflared` is missing; the tunnel is `down`; a *quick* tunnel would also change hostname on every restart.
7. **Desktop/mobile source unavailable** — the backend supports them, but no client source is in this repository.
8. **Jitsi reachability from WSL** — the admin health probe targets the LAN address and reports "not reachable from this host"; the containers are up and LAN browsers can join.
9. **View Summary closed-panel defect** — the card button does nothing unless the Honco panel is already open (`MeetingIntent.open` never calls `HoncoOpenWorkspace`). Reproduced again today.
10. **No retention policy** — nothing deletes files or recordings automatically (Mattermost's retention job is an Enterprise feature). Only `honco_notifications` rows older than 90 days are pruned.
11. **Honco search does not search message text** — use Mattermost's own search box for that.
12. **Meeting participants are Jitsi names**, not Mattermost users, so they show initials rather than profile photos and a person joining twice can appear twice.
13. **Recording is manual** — Honco never starts a recording; a participant must press Record in Jitsi.
14. **Single Jibri** — one recording at a time; after a Docker Desktop restart Jibri usually needs recreating.
15. **Web client rebuilds are heavy** — a full rebuild takes 7–17 minutes and can starve this machine; run it alone and restart the server afterwards if database errors appear.

---

# 28. Safe project operations

Nothing here is destructive. Never run `DROP DATABASE`, `TRUNCATE`, "reset the database", `git reset --hard` or `git clean` on this project.

```bash
wsl.exe -d Ubuntu-22.04                       # a Linux shell on this machine

# 1. Start everything (PostgreSQL → server → /meet service)
cd ~/honco-workspace/chat
./honcochat.sh start
./honcochat.sh status                         # postgres / honcochat / tunnel / meetsvc / SiteURL

# 2. Check PostgreSQL (read-only)
psql -h 127.0.0.1 -p 5433 -U honco -d honcochat -c "SELECT version FROM honco_schema_migrations ORDER BY version DESC LIMIT 1;"
psql -h 127.0.0.1 -p 5433 -U honco -d honcochat -c "SELECT count(*) FROM honco_tasks WHERE deleted_at = 0;"

# 3. Check server health
curl -s http://127.0.0.1:8065/api/v4/system/ping          # {"status":"OK",...}
~/honco-workspace/chat/healthcheck.sh                      # one line per component

# 4. Check Jitsi and Jibri
cd ~/honco-meet && ~/honco-workspace/meet/meet.sh ps
~/honco-workspace/meet/meet.sh logs jibri | tail -20
#   after a Docker Desktop restart, recreate Jibri:
#   docker compose -f docker-compose.yml -f jibri.yml -f docker-compose.override.yml up -d --force-recreate --no-deps jibri

# 5. Logs
tail -f ~/honco-chat/logs/mattermost.log                   # server + plugin ("honco:" lines)
tail -f ~/honco-chat/logs/honcochat.log                    # start/stop and meetsvc
tail -f ~/honco-chat/logs/pg.log                           # PostgreSQL

# 6. Rebuild safely
cd ~/honco-workspace/plugins/com.honco.workspace           # the plugin (about a minute)
go build ./server/ && go vet ./... && go test ./server/...
#   then package and install with mmctl (see OPERATIONS.md §3)
cd ~/honco-workspace/server/webapp/channels                # the web client (7-17 min) - RUN THIS ALONE
npm run build

# 7. Back up before anything risky
~/honco-workspace/chat/backup-honcochat.sh                 # pg_dump + config + data

# 8. Stop (keeps all data)
cd ~/honco-workspace/chat && ./honcochat.sh stop           # stop-all also stops PostgreSQL
```

**Tests:** Go unit tests `go test ./server/...` (79); the API and browser suites live outside the repository (`honco-browser/*.js` and the scratchpad `*.sh`) — see §25 of the reference document for the full list.

---

# 29. Quick reference

| I want to… | Go to |
|---|---|
| Change **Tasks** | `main.js` (`TasksPanel`, `TaskEditor`, `TaskRow`) · `server/tasks_api.go` · `server/task.go` · `server/store.go` |
| Change **Meeting** behaviour | `server/meeting_lifecycle.go` (timings) · `server/meeting_card.go` (card) · `chat/meetsvc.py` (`/meet`) |
| Change **Jibri** / recording | `meet/jibri-finalize.sh` · `meet/setup-jibri.sh` · `server/recordings_api.go` · `server/recording.go` |
| Change **AI Assistant** UI | `main.js` (`AIPanel`, `AILive`, `AIFinished`, `TranscriptBlock`) |
| Change **AI integration** | `server/ai_adapter.go` · `server/ai_api.go` · `server/ai.go` · `AI_INTEGRATION.md` |
| Change **Meeting Summary** | `server/summarizer.go` · `server/meeting_summary.go` · `server/summary_api.go` · `summarize/honco-summarize.sh` |
| Change **Support** | `server/support_api.go` · `server/support_card.go` · `server/support.go` · `main.js` (`SupportPanel`) |
| Change **Search** | `server/search.go` · `server/search_api.go` · `main.js` (`SearchPanel`) |
| Change **Profile Photo** | `chat/branding/profile-photo.py` (then rebuild the web client) · `main.js` (`UserAvatar`) |
| Change **Notifications** | `server/notify.go` (+ `support_card.go`, `recordings_api.go`, `ai_api.go`) |
| Change **Admin** | `server/admin_api.go` · `server/admin_store.go` · `server/files_admin.go` · `main.js` (`AdminPanel`) |
| Change the **database** | `server/store.go` → append a new migration; never edit a shipped one |
| Find an **API route** | `server/api.go` → `newRouter` (if it is not there, it does not exist) |
| Find the **frontend** | `plugins/com.honco.workspace/webapp/dist/main.js` (the whole Honco UI, one file) |
| Find the **backend** | `plugins/com.honco.workspace/server/*.go` |

**Ports:** PostgreSQL 5433 · Honco Chat 8065 · meetsvc 8077 · Jitsi web 8443 · Prosody 5280 · JVB 10000/udp · Jibri health 2222.

---

# 30. Beginner glossary

| Term | Plain meaning |
|---|---|
| **Mattermost** | The open-source team-chat server Honco Chat is built from. Provides users, channels, messages, files, search and a plugin system. |
| **Honco Workspace plugin** | Honco's own code loaded by Mattermost (`com.honco.workspace`): a Go part on the server and a JavaScript part in the browser. All Honco features live here. |
| **Frontend** | The code that runs in your browser and draws the screen. Honco's is one file: `webapp/dist/main.js`. |
| **Backend** | The code that runs on the server: checks who you are, applies the rules, reads and writes the database. Honco's is Go, in `server/*.go`. |
| **API** | A way for the frontend to ask the backend to do something — a URL you call with some data. |
| **REST API** | The usual style for those URLs: `GET` reads, `POST` creates, `PATCH` edits, `PUT` replaces, `DELETE` removes; answers come back as JSON. |
| **WebSocket** | A connection that stays open so the *server* can push news to the browser (a card changed, a transcript line arrived) instead of the browser asking repeatedly. |
| **PostgreSQL** | The database program that stores everything in tables. One database here, called `honcochat`. |
| **Database migration** | A numbered, one-way change to the database structure ("create this table", "add this column"). Honco's are in `store.go`; applied ones are recorded in `honco_schema_migrations`. |
| **Jitsi** | Self-hosted video-conferencing software. It provides the meeting room. |
| **Jibri** | Jitsi's recorder: joins a room invisibly, records an MP4, then calls Honco Chat to hand it over. |
| **Prosody** | The chat/presence server inside Jitsi. Honco asks it who is currently in a room. |
| **Jicofo** | Jitsi's "conference focus": decides how participants are connected and assigns Jibri when someone presses Record. |
| **JVB** | Jitsi Videobridge: moves the actual audio and video between participants. |
| **RustDesk** | Open-source remote-desktop software. Honco manages the *request workflow*; the screen control happens in RustDesk. |
| **Callback** | A request an outside system makes *to us* when it has finished something — for example Jibri calling `/recordings/complete`. |
| **Webhook** | The general name for that pattern: "call this URL when something happens". Honco's callbacks are webhooks protected by a shared secret. |
| **Bot** | An automated user. `honco` is the bot that posts cards, thread replies and notification DMs. |
| **Session** | Proof that you are logged in, kept in a cookie. Web sessions here last 180 days. |
| **Authentication** | *Who are you?* — checking your login. Without it every Honco route answers 401. |
| **Authorization** | *Are you allowed?* — team membership, channel membership, creator/assignee, support agent, system administrator. |
| **IDOR** | "Insecure Direct Object Reference": guessing someone else's id in a URL to read their data. Honco re-checks the rule on every object, so guessing an id does not help. |
| **Rate limiting** | A cap on how many requests one user can make (reads 240/min, writes 60/min, AI pushes 300/min). Over the cap the server answers 429 and asks you to retry. |
| **File storage** | Where uploaded files and recordings actually live: `~/honco-chat/run/data/`, indexed by Mattermost's `fileinfo` table. |
| **Service secret** | A long random string two machines share, sent in a request header, so the server knows the caller is really meetsvc, Jibri or the AI service. Kept only in server configuration. |
| **Soft delete** | Marking a row as deleted (`deleted_at`) instead of removing it — what "Delete task" does. |
| **Key-value store** | A small storage area Mattermost gives plugins for simple values. Honco keeps AI session state there instead of creating a table. |

---

*Compiled on 15 September 2026 from the running Honco Chat and the source at commit `126acc9`. Every Honco control in Part 3 was clicked in the live application; results marked BLOCKED or NOT TESTABLE depend on systems outside this machine and were not simulated.*
