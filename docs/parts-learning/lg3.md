
---

# 11. Meeting Intelligence

Four things that are easy to confuse:

| Term | What it is | Where it lives |
|---|---|---|
| **Meeting** | The room, its card, who joined and when. | `honco_meetings`, `honco_meeting_participants` |
| **Recording** | The MP4 Jibri produced. | `honco_recordings` + a Mattermost file |
| **Meeting Intelligence** | Written notes generated from the **channel conversation** during the meeting — summary, key points, decisions, action items, participants. Does **not** listen to audio. | `honco_meeting_summaries`; Meetings tab |
| **AI Assistant** | A *live* assistant during the call (transcript, suggestions, insights, topics) plus a post-call summary — produced by an **external AI service**. | plugin key-value store; AI Assistant tab |

### How Meeting Intelligence works today
```
Meeting (honco_meetings row)
 ↓
Channel conversation: the messages posted in the meeting's channel between the meeting's creation and the end of its last recording (or now),
                      capped at 6 hours / 1000 messages / 200 000 characters; bot posts are left out
 ↓
Summary request: Meetings tab → Generate summary → POST /meetings/{id}/summary → honco_meeting_summaries (status pending)
 ↓
External Claude service: the plugin runs  ssh <SummarizerHost>  with a key locked to one script (summarize/honco-summarize.sh), which runs the Claude CLI;
                         alternatively a local executable named in SummarizerCommand
 ↓
Summary: the reply is parsed into Summary / Key points / Decisions / Action items / Participants → status ready (or "empty" if nobody typed anything, or "failed")
 ↓
Honco UI: the Meetings tab shows the sections; the meeting card gains "View Summary"; the requester gets a DM with a link
```

### The current blocker (still true on 14 Sep 2026)
The summarizer host (`192.168.2.150`, "mother") **cannot be reached from this machine**. Every real generation ends with the message **"The summarizer is not reachable from this server."** and a **Try again** button. The only "ready" summaries in the database were produced by a *test stub* (they contain "STUB-ECHO…") — **no genuine Claude summary exists**. The rest of the feature — the request, the conversation window, the states, the UI, the deep link, the notifications — is implemented and tested.

Honco Chat does **not** do speech-to-text. A separate batch transcription worker (`transcribe/`) exists for another host and is not connected to the plugin.

Code: `server/summarizer.go` (window, SSH/command transport, parsing) · `server/meeting_summary.go` (model, statuses `pending/ready/failed/empty`) · `server/summary_api.go` (routes) · `summarize/honco-summarize.sh` (runs on mother) · `main.js` `MeetingsTab`.

---

# 12. AI Assistant

**VERY IMPORTANT:** Honco Chat does **not** own the AI. There is no speech recognition, no language model and no transcription engine in this codebase. Another service/team owns that. Honco Chat provides the *place where it shows up* and the *secure way for it to plug in*.

| HONCO OWNS THIS | EXTERNAL SERVICE OWNS THIS |
|---|---|
| The UI: the AI Assistant tab, the card button, the App Bar icon, the states (idle / connecting / live / ended / completed / unavailable / failed), transcript with "Load earlier lines", suggestions with history, insights, topics, the completed-summary view, dark theme, phone layout, accessibility | Listening to the call (voice capture) |
| Session handling: Start / Stop / Reconnect, one session per meeting, stored in the plugin key-value store (`ai:session:<meeting_id>`) | Speech-to-text / the live transcript lines |
| The **callback endpoint** `POST /ai/events` protected by a shared secret (`X-Honco-AI-Secret`), with event name aliases and size limits | Sales suggestions, insights (concern / sentiment / opportunity), topics |
| The adapter that calls the service (`POST {AIServiceURL}/v1/sessions`, bearer token kept on the server) | The post-call summary, action items, transcript reference |
| WebSocket delivery of every event to the meeting's channel (`ai_event`) | Any AI model |
| Authorization (channel membership), rate limiting, no secrets in the browser | — |
| DM notifications "AI Meeting Summary Ready" / "AI Meeting Insights Unavailable"; the Admin card | — |

```
User → clicks "AI Assistant" on the card → Honco AI Assistant UI (main.js AIPanel) → "Start AI session"
 ↓
Honco integration: POST /meetings/{id}/ai/session → ai_api.go → ai_adapter.go → POST {AIServiceURL}/v1/sessions  (Bearer token, callback URL)
 ↓
External AI service joins/listens to the meeting (its job)
 ↓
AI event: the service POSTs /ai/events {meeting_id, event: transcript|suggestion|insight|topics|status|final|error, …}  with X-Honco-AI-Secret
 ↓
Honco callback: ai_api.go handleAIEvents → secret ok? meeting known? → ai.go appends to the session → publishes WebSocket "ai_event"
 ↓
Browser: AIPanel receives the event → transcript line / suggestion / insight / topic appears live; "final" renders the summary view
```

**Today:** `AIServiceURL` is empty and no such service exists on the network (checked: no ports, no containers, no config; the "mother" host is unreachable; Honco Workspace V2 is a different product with a different contract). Pressing **Start AI session** therefore shows the honest *not configured / unavailable* state. The 24 stored sessions in the Admin card came from the test fixture pusher, not from a real service. Everything Honco owns is tested (94 API checks, 56 browser checks, 33 entry-point checks, 31 accessibility checks); the real AI end-to-end is **BLOCKED** until the service is connected — the contract is written down in `AI_INTEGRATION.md`.

Code: `server/ai.go` (session, events, aliases, KV) · `server/ai_adapter.go` (service calls, URL safety, secret redaction) · `server/ai_api.go` (routes, DMs) · `main.js` `AIPanel`, `AILive`, `AIFinished`, `SuggestionCard`, `TranscriptBlock`, `AIIntent`.

---

# 13. Remote Support

```
Support request  (anyone: Support tab → Request Support → describe → Send)          status: open
 ↓
Agent queue  (members of the support channel see it; the support channel gets a post)
 ↓
Accept  (an agent clicks Accept Request; or Decline)                                  accepted | rejected
 ↓
Start  (the agent clicks Start Session)                                               active  → the card shows the RustDesk hint
 ↓
RustDesk  (the requester opens the RustDesk client and shares their ID; the agent controls the screen THERE)
 ↓
End  (the agent clicks End Session; the requester may Cancel at any time before)     ended | cancelled
```

- **Honco = workflow.** Honco keeps the request, its status, who the agent is, timestamps, an audit trail (`honco_support_events`: created / accepted / rejected / started / ended / cancelled with actor and detail), a card in the channel, DMs to the other party at every step, and live updates by WebSocket.
- **RustDesk = the actual remote control.** Honco stores no RustDesk credential, calls no RustDesk API and cannot see whether the screen session happened. The RustDesk relay is a separate deployment on another host (**not verified in current environment**). If RustDesk is down, the Honco workflow still works but the agent cannot take the screen.
- **Who is an agent?** Whoever is a member of the channel named in the plugin settings (`SupportChannelName` in `SupportTeamName`; today `honco-support` in `harshini-sharma`). Add or remove agents by adding/removing them from that channel. Being a system admin does *not* make you an agent. If the setting is blank, requests can be raised but nobody can accept them.
- **Races:** two agents clicking Accept at once → the second gets 409 "this request was already updated by someone else".

Code: `server/support.go` (model, states) · `server/support_api.go` (routes, agent check) · `server/support_card.go` (card, notifications) · `main.js` `SupportTab`, `SupportCard`, `RustDeskHint` · tables `honco_support_requests`, `honco_support_events`.

---

# 14. Files

| | Normal attachment | Meeting recording |
|---|---|---|
| Who uploads | a person, with the 📎 button | the Honco plugin, as the `honco` bot, when Jibri calls back |
| How | Mattermost `POST /api/v4/files` | the same Mattermost file API, from Go |
| Where the bytes go | `~/honco-chat/run/data/<date>/…` (Mattermost's local file storage) | the same place |
| Database | Mattermost `fileinfo` row attached to the post | `fileinfo` row **plus** a `honco_recordings` row pointing at it |
| Size limit | `FileSettings.MaxFileSize` = 100 MB | `MaxRecordingMB` (0 ⇒ the same 100 MB); over the limit → "failed", never silently dropped |

```
📎 in the composer → choose a file
 ↓
Browser: Mattermost POST /api/v4/files  (multipart; the login cookie identifies you; limit 100 MB)
 ↓
Mattermost server: is the caller a member of the channel? size ok?  →  fileinfo row  +  bytes under ~/honco-chat/run/data/<date>/
 ↓
The message is sent with the file id attached  →  channel members see a preview / download link
 ↓
Reading later: GET /api/v4/files/{id}  →  channel membership checked again  →  bytes served (no public links)
```

**Permissions.** A file can be opened only by members of the channel whose post carries it. Non-members get 403/404. There are **no public links** (`EnablePublicLink=false`), so a file URL cannot be shared with an outsider.

**Deletion.** Deleting the post soft-deletes the file in Mattermost. The meeting card notices (it asks `GET /api/v4/files/{id}/info`) and shows "Recording unavailable" instead of a broken player. Nothing deletes files automatically: **there is no retention policy** (Mattermost's retention job is an Enterprise feature; this is Team Edition).

**Cache invalidation.** Recording links point at `/api/v4/files/<id>` and the card re-checks existence when drawn. Profile pictures use a different trick — a timestamp in the URL (Part 18).

The Admin tab's Files card (`files_admin.go`) reports stored files and bytes, recordings, orphans and "recordings missing their file" so problems are visible.

---

# 15. Notifications

A **notification** in Honco is a message the `honco` **bot** (an automated Mattermost user) sends to you, or posts in a channel. Examples:

```
Task assigned to you          → DM: "You were assigned: <title>"           (notify.go, kind task_assigned)
Meeting scheduled (/meet in…) → channel post at the time, with the link     (meetsvc.py reminder)
Meeting started / ended       → reply in the meeting card's thread          (meeting_card.go)
Recording ready               → channel post "Recording ready for <topic>"  (recordings_api.go)
Support request accepted      → DM to the requester                         (support_card.go)
AI summary ready              → DM "AI Meeting Summary Ready" + link        (ai_api.go)
Meeting summary ready/failed  → DM with a link to the summary               (notify.go)
Task due soon / overdue       → DM once per due date (scanner every 10 min) (notify.go)
```

**How it reaches the user.**
```
Something happens (e.g. task saved with a new assignee)
 ↓
notify.go claim(kind, subject, recipient, key)  →  INSERT into honco_notifications with a UNIQUE key
        (if the key already exists, the event was already announced → stop; this is why you never get the same DM twice)
 ↓
the plugin creates a direct message from the honco bot (Mattermost plugin API)
 ↓
Mattermost delivers it like any DM: badge in the sidebar, desktop pop-up if enabled, e-mail if you are away/offline and have e-mail notifications on
   (e-mail goes out through the configured SMTP server; push to phones is NOT configured)
```

Ledger rows older than 90 days are pruned daily; that is the only automatic cleanup in the system.

---

# 16. Search

There are **two** searches:

| Mattermost message search (top search box) | Honco global search (panel → Search tab) |
|---|---|
| Searches **messages and files** — Mattermost's own feature, untouched. | Searches Honco's own objects: **Tasks** (title/description), **Meetings** (topic/room), **Recordings** (file name/room), **Summaries** (text), **Support** requests (issue). |
| Results in Mattermost's results panel. | Results grouped by category with counts; click to open the tab/post. |

```
Search tab: type "invoice" + Enter
 ↓
Honco Search API: GET /search?q=invoice&type=all&page=0   (search_api.go)
 ↓
Authorization: searchScope(you) = the list of teams and channels YOU belong to; every query is limited to those ids
               (so a private channel you are not in, or another team's tasks, can never appear)
 ↓
Search categories: one ILIKE query per category (search.go), wildcards escaped, 20 results per page (max 50)
 ↓
Results: pages per category with totals → the panel lists them; "No Honco results for …" when nothing matches
```

Limits to know: it is substring matching (no ranking beyond recency), it does not search message text (use the top box), and the Mattermost search box only shows a Honco *button*, not search pills, because pills are disabled on an unlicensed server.

Code: `server/search.go` (queries, scope), `server/search_api.go` (route, paging) · `main.js` `SearchTab`.

---

# 17. Admin dashboard

**Who can see it?** Only users with the `manage_system` permission (system admins). The tab is hidden for others *and* every `/admin/*` call is re-checked on the server (a normal user gets 403).

**Why does it exist?** Mattermost's System Console shows Mattermost's health; nothing there knows about Honco tables, Jitsi, Jibri, meetsvc or the AI service. This dashboard is the one place to see whether the *Honco* parts are alive and how much they are used. It is read-only.

```
Admin clicks the Admin tab (or Refresh)
 ↓
GET /admin/overview  and  GET /admin/health        (main.js AdminTab)
 ↓
admin_api.go: does the caller have manage_system?  →  no: 403 (the tab is not even offered)
 ↓
overview: counts from honco_* and Mattermost tables (admin_store.go), file figures (files_admin.go), notification counts,
          AI session state, configuration FLAGS (true/false only), recent failures
health:   six probes run now — plugin, PostgreSQL, Jitsi (MeetPublicURL, 4 s timeout), Jibri (:2222), meetsvc (:8077), plugin state
 ↓
The cards render; "not reachable" lines are shown in red with the probe time
```

| Section | What it means | Where the information comes from |
|---|---|---|
| **System health** | Six live probes: Honco Chat (plugin answering), PostgreSQL (a query + latency), Honco Plugin (active), Jitsi (an HTTP request to `MeetPublicURL`, 4 s timeout), Jibri (its health port `127.0.0.1:2222`), Meeting Service (meetsvc on `127.0.0.1:8077`) | `admin_api.go handleAdminHealth`, run at the moment you open the tab |
| **Usage** | Users, Teams, Channels (Mattermost) and Active meetings, Meetings, Recordings, Tasks, Summaries, Support open/total (Honco) | `SELECT count(*)` over the tables (`admin_store.go`) |
| **Honco Workspace** (versions) | plugin version 0.1.0, Honco migration 6, Mattermost migration 215, bot configured | plugin + `db_migrations` |
| **Recent failures** | latest failed recordings, failed summaries, declined support requests | the three tables' failed/rejected rows |
| **Notifications** | bot configured; how many of each kind were ever sent | `honco_notifications` grouped by kind |
| **Files** | stored files/bytes, attached to posts, recordings, largest file, orphans, missing files, limits, public-links flag | `files_admin.go` over `fileinfo` + `honco_recordings` |
| **AI** | callback configured, service configured/reachable, sessions live/completed/stored | plugin config + the key-value store |
| **Security** | yes/no flags: self-signup, MFA, public links, uploads, e-mail/push, SMTP, Jibri/meet/summarizer/support configured | Mattermost config — **flags only, never values or secrets** |

Today's reading includes "Jitsi — not reachable from this host · ~4000 ms": the probe targets the LAN address, which is not routable from inside this WSL machine (the containers are up).

---

# 18. Profile & avatar

The profile photo is **Mattermost's existing profile picture**, relabelled and polished — not a new feature with new storage.

```
Profile Settings (account menu → Profile → Profile Settings → Profile Photo → Edit)
 ↓
Select Photo  ("Change photo" → JPG / PNG / BMP; the browser checks type and size (≤ 100 MB) before uploading)
 ↓
Preview  (the chosen picture is shown; Cancel discards it)
 ↓
Save  ("Save photo")
 ↓
Mattermost Avatar API  POST /api/v4/users/{me}/image   (only your own user; anyone else → 403)
 ↓
Existing user avatar storage  (the server resizes the image and stores it under your user id in the file store;
                               the only database change is Users.LastPictureUpdate = now)
 ↓
user_updated  (Mattermost broadcasts the change on the WebSocket to every connected client)
 ↓
All UI locations update  (each client rebuilds your avatar URL as /api/v4/users/{id}/image?_=<LastPictureUpdate>;
                          the new timestamp means a new URL, so no cached old picture is shown)
```

**Why no new table?** Mattermost already stores one picture per user, already serves it with cache-busting, already checks that only you can change yours, and already broadcasts the change. A second copy per feature would have to be kept in sync and secured again. Instead, the Honco panels render the **same URL** through one small `UserAvatar` component that watches `LastPictureUpdate` in the webapp's store — so the task assignee, the support requester/agent and the meeting "Started by" change at the same instant as the message beside them.

**Where it appears:** messages, threads, DMs and group DMs, the channel member list, the profile popover, the DM picker, mention search, the header menu — and, in Honco: task rows, support rows and cards, meeting cards. Meeting *participants* keep initials (they are Jitsi names, not users).

**Removing:** "Remove photo" → `DELETE …/image` → the generated default avatar (initial on a colour) returns everywhere. Verified with two real users on 14 Sep 2026 (44/44 browser checks, 57/57 for the settings dialog).

Code: the dialog is Mattermost's `user_settings_general.tsx` + `setting_picture.tsx`, patched by `chat/branding/profile-photo.py` (wording, "Profile photo updated."/"removed.", plain failure message); Honco avatars: `main.js` `UserAvatar`, `pictureUpdateOf`.

---

# 19. Database

**Why PostgreSQL?** Mattermost requires a SQL database, and PostgreSQL is the one it supports best (fast, reliable, free). One instance (port 5433, data in `~/honco-chat/pgdata`) holds one database, **`honcochat`**.

**What Mattermost stores** (85 tables): `users`, `teams`, `channels`, `channelmembers`, `posts`, `fileinfo`, `preferences`, `sessions`, `bots`, `pluginkeyvaluestore` (where the AI sessions live), `db_migrations`, …

**What Honco stores** (9 tables, created by the plugin):

| Feature | Table | Purpose |
|---|---|---|
| Tasks | `honco_tasks` | one row per task (team, creator, assignee, title, description, status, due_at, deleted_at) |
| Meetings | `honco_meetings` | one row per `/meet` room (room_name unique, channel, creator, topic, status, post_id of the card, scheduled/started/ended_at, participant_count) |
| Meetings | `honco_meeting_participants` | who was in the room (Jitsi name + occupant key, joined_at, left_at, present) |
| Recording | `honco_recordings` | delivered recordings (status ready/failed/unavailable, file_id → `fileinfo`, size, duration, error) |
| Meeting Intelligence | `honco_meeting_summaries` | one per meeting (status, summary, key_points, decisions, action_items, participants, raw_output, window) |
| Notifications | `honco_notifications` | the "send once" ledger (kind, subject, recipient, unique dedupe_key) |
| Remote Support | `honco_support_requests` | the request (requester, agent, status, issue, timestamps) |
| Remote Support | `honco_support_events` | the audit trail (actor, action, detail) |
| — | `honco_schema_migrations` | which Honco migrations have been applied (currently 6) |

**Migrations.** A **migration** is a numbered, one-way change to the database structure ("create table honco_tasks", "add column status"). The plugin keeps its list in `server/store.go` (`var migrations`), applies the ones not yet recorded in `honco_schema_migrations` when it starts (each in its own transaction), and never touches Mattermost's own `db_migrations`.

**"If I change the Task database structure, what must I consider?"**
1. **Add a new migration; never edit a shipped one.** Version 7 with `ALTER TABLE honco_tasks ADD COLUMN IF NOT EXISTS …`. Editing version 1 would do nothing on existing servers (it already ran) and would make fresh installs differ from old ones.
2. **Make it additive and safe for existing rows** — new columns need a `DEFAULT`; never drop or rename in place.
3. **Update the model and SQL** in `task.go` / `store.go` (the `INSERT`, `UPDATE`, `SELECT` column lists), the JSON the API returns (`tasks_api.go`), the UI (`main.js`), and search if the field should be searchable (`search.go`).
4. **Think about authorization and validation** for the new field (who may set it, what values are allowed).
5. **Run the tests** (`go test ./server/...`, `tasks-ux.js`, `p11-tasks.sh`) and redeploy the plugin — the migration runs on activation. Take a backup first (`chat/backup-honcochat.sh`).

---

# 20. API concept

An **API** is a way for the frontend (or another program) to ask the backend to do something, by calling a URL with a method:

| Method | Meaning | Honco example |
|---|---|---|
| `GET` | read, change nothing | `GET /tasks?team_id=…` — list tasks |
| `POST` | create / do an action | `POST /tasks` — create a task; `POST /support/requests/{id}/accept` — accept |
| `PATCH` | change part of something | `PATCH /tasks/{id}` — edit title/assignee/due date |
| `PUT` | replace one thing | `PUT /tasks/{id}/status` — set the status |
| `DELETE` | remove / end | `DELETE /tasks/{id}` — delete; `DELETE /meetings/{id}/ai/session` — end the AI session |

**One endpoint in detail — `POST /plugins/com.honco.workspace/api/v1/tasks`**
- *Request:* JSON body `{"team_id": "…", "title": "Fix login", "assignee_id": "…", "status": "todo", "due_at": 1789000000000}`, sent with the login cookie and `X-Requested-With: XMLHttpRequest`.
- *Server steps:* Mattermost verifies the cookie and sets `Mattermost-User-Id` → `hardening.go` adds security headers and checks the rate limit (60 mutations/min) → `api.go` routes to `tasks_api.go handleCreateTask` → `requireUser` → team membership → `task.go` validation → `store.go` INSERT → `notify.go`.
- *Response:* `201 Created` with the task JSON; on error `{"error": "title is required"}` with 400, `403`, `429`, or `500`.

**Categorized list** (all under `/plugins/com.honco.workspace/api/v1`; "session" = you must be logged in; membership checks apply as described in Part 22):

| Group | Endpoints | Purpose | Auth | Backend file |
|---|---|---|---|---|
| Health | `GET /health` | is the plugin alive | session | `api.go` |
| Tasks | `POST /tasks`, `GET /tasks`, `GET/PATCH/DELETE /tasks/{id}`, `PUT /tasks/{id}/status` | task CRUD | session + team member (creator/assignee rules) | `tasks_api.go` |
| Meetings | `POST /meetings/register` | meetsvc registers a room | **service secret** `X-Honco-Service-Secret` | `recordings_api.go` |
| Meetings | `GET /meetings/{id}`, `GET /channels/{id}/meetings`, `GET /channels/{id}/active-meetings` | read meetings | session + channel member | `meetings_api.go`, `summary_api.go` (channel list) |
| Recording | `POST /recordings/complete` | Jibri delivers a recording | **callback secret** `X-Jibri-Callback-Secret` | `recordings_api.go` |
| Recording | `GET /channels/{id}/recordings` | list recordings | session + channel member | `recordings_api.go` |
| Summaries | `POST/GET /meetings/{id}/summary` | generate / read notes | session + channel member | `summary_api.go` |
| AI | `POST /ai/events` | the AI service pushes events | **callback secret** `X-Honco-AI-Secret` | `ai_api.go` |
| AI | `GET /ai/status`, `GET /meetings/{id}/ai`, `GET …/ai/transcript`, `POST/DELETE …/ai/session` | status, session state, older lines, start/stop | session + channel member | `ai_api.go` |
| Support | `POST/GET /support/requests`, `GET /support/requests/{id}`, `POST …/{accept,reject,start,end,cancel}` | workflow | session; agent checks for accept/reject/start/end | `support_api.go` |
| Search | `GET /search` | Honco global search | session (scoped to your teams/channels) | `search_api.go` |
| Admin | `GET /admin/overview`, `GET /admin/health` | dashboard | session + `manage_system` | `admin_api.go` |

The route table itself is `server/api.go` (`newRouter`) — if a URL is not there, it does not exist.

---

# 21. WebSockets

**In simple terms:** a normal web request is like a phone call the browser makes — ask, get an answer, hang up. A **WebSocket** is a call that stays open, so the *server* can speak whenever it has news. Mattermost keeps one WebSocket per browser tab (`/api/v4/websocket`); the Honco plugin uses that same connection.

**Why not polling?** Polling means every browser asking "anything new?" every few seconds. With a meeting card visible to a whole channel that would be thousands of useless requests. The plugin already knows the exact moment something changes, so it *pushes* one event to the channel's connected clients.

```
Jitsi participant changes (someone joins or leaves the room)
 ↓
Honco detects it (the 10-second poll of Prosody in meeting_lifecycle.go)
 ↓
WebSocket event "meeting_updated" {post_id, meeting_id, props…}  published to the channel (meeting_card.go)
 ↓
Browser (main.js registerWebSocketEventHandler)
 ↓
Meeting card updates in place: "Meeting is active · 2 participants", names, buttons
```

```
AI event (the external service POSTs /ai/events: a transcript line, a suggestion, an insight, topics, final, error)
 ↓
Honco validates the secret and the meeting, stores the event in the session (ai.go)
 ↓
WebSocket event "ai_event" {meeting_id, event, seq, data}  to the channel
 ↓
AI panel updates: the line/suggestion appears; the status badge changes; "final" shows the summary view
```

A third event, `support_updated`, does the same for support requests. If the connection drops, Mattermost reconnects; the plugin's reconnect handler then re-reads the AI session and the Support/Meetings lists, so nothing missed during the gap is lost (the state is on the server, the socket only announces changes). The AI panel also shows "Reconnecting" while the socket is down.

Payload rule for developers: publish only plain maps/lists (JSON-like data). A Go struct in a WebSocket payload once wedged the plugin's internal connection — there is a unit test guarding this.

---

# 22. Security

In beginner language — *what* is enforced and *why*:

| Topic | What happens | Why it exists |
|---|---|---|
| **Login** | Mattermost checks username/e-mail + password (optionally a second factor, MFA). Self-signup is off; an admin creates accounts. | Only known people get in. |
| **Authentication** ("who are you?") | Every Honco request must carry a valid session. Mattermost sets the `Mattermost-User-Id` header from it; anything without it gets **401**. The browser cannot forge the header. | The plugin never has to guess who is calling. |
| **Authorization** ("may you?") | Each handler checks the specific rule: team member, channel member, creator/assignee, support agent, system admin. | Being logged in is not the same as being allowed. |
| **Team membership** | Tasks and search are limited to the teams you belong to; assignees must be in the team. | Team A cannot see or touch team B's work. |
| **Channel membership** | Meetings, recordings, summaries, AI sessions and support requests are visible only to members of their channel; outsiders get **404** (not 403) so they cannot even learn the item exists. | Private conversations stay private. |
| **Admin permissions** | `/admin/*` require `manage_system`; the UI hiding the tab is only convenience. | Configuration flags and usage figures are for admins. |
| **IDOR protection** | Guessing another object's id does not help: every read/write re-checks the rule above (30 dedicated tests). | Ids are public in URLs; ownership must be checked every time. |
| **Private channels** | Same membership rule, verified across two teams in tests; Honco search never returns a hit from a channel you are not in. | — |
| **Service secrets** | The three machine-to-machine routes (`/meetings/register`, `/recordings/complete`, `/ai/events`) use a shared secret header compared in constant time. An unset secret rejects everything. Secrets live only in the server config (0600), never in the JavaScript, responses or logs. | Only our meetsvc, our Jibri and the real AI service can inject data. |
| **Callback secrets** | Jibri reads its secret from a 0600 file inside its container, never from the script; the AI secret is a header; meetsvc's is in a 0600 env file. | Keeps secrets out of Git and backups. |
| **Rate limiting** | Per user: reads 240/min, mutations 60/min, service routes 60/min, AI pushes 300/min → **429** with `Retry-After`. | A runaway script cannot overload the server. |
| **Security headers** | Every plugin response: `nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store`, `Permissions-Policy`. | Standard browser protections (no clickjacking, no cached private data). |
| **File permissions** | Channel members only; no public links; recordings go through the same file API. | Files inherit the channel's privacy. |
| **Session behaviour** | Web sessions last 180 days; changing your password invalidates sessions; password reset by e-mail. | Mattermost defaults, verified in tests. |
| **Audit trail** | Support: every transition is a row in `honco_support_events`. Notifications: the ledger. Mattermost: its own audit log. Tasks/meetings keep `created_at/updated_at` but no per-field history. | Who did what, when. |
| **What is *not* claimed** | No end-to-end encryption; TLS is terminated *outside* the server (plain HTTP on the LAN, HTTPS only through the Cloudflare tunnel); Jitsi uses a self-signed certificate on the LAN; no SOC 2/ISO/HIPAA/GDPR certification. | Honesty about the boundary. |
