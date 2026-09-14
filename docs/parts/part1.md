# HONCO CHAT

## Complete System Documentation & User Guide

**Architecture • Features • UI • APIs • Data Flow • Operations • Testing**

| | |
|---|---|
| Documentation version | 1.0 |
| Documentation date | 14 September 2026 |
| Git commit documented | `126acc9` (branch `main`, not pushed) |
| Repository | `C:\Users\Dell\Desktop\Honco_Chat` (Windows checkout); runtime on WSL `Ubuntu-22.04` at `~/honco-chat` (runtime) and `~/honco-workspace` (source checkout) |
| Mattermost base | 11.11.0, Team Edition, unlicensed |
| Plugin | `com.honco.workspace` 0.1.0 |
| Documentation status | **Verified against the running code and instance** — every status in this document was checked on 14 Sep 2026 by inspecting source, routes, migrations, configuration and by running the test suites listed in Section 25. Where something could not be verified it is marked as such. |

> **How this document was produced.** The code is the source of truth. Facts were taken from the repository (`plugins/com.honco.workspace/server/*.go`, `webapp/dist/main.js`, `chat/*.sh`, `chat/meetsvc.py`, `meet/*`), from the running instance (`mmctl --local config get`, the plugin's own `/admin/overview` and `/admin/health` endpoints, `psql` read-only queries) and from the test suites. Where the existing Markdown documents (`README.md`, `OPERATIONS.md`, `AI_INTEGRATION.md`, `CLIENTS.md`, `chat/DEBRAND.md`) disagree with the code, the discrepancy is called out rather than silently resolved.

---

## Table of contents

1. What is Honco Chat?
2. Complete system architecture
3. Request / data flow
4. Complete UI map
5. Button-by-button user guide
6. Task management
7. Meetings
8. Jibri recording
9. Meeting Intelligence / summary
10. AI Assistant
11. Remote Support
12. Files & attachments
13. Notifications
14. Global search
15. Admin dashboard
16. Profile / user account
17. Authorization & security
18. Database
19. Project folder structure
20. Code map
21. APIs
22. WebSockets
23. Configuration
24. Running Honco Chat locally
25. Testing
26. Current feature status
27. Known limitations
28. Troubleshooting
29. Common user flows
30. Quick reference

---

# 1. What is Honco Chat?

Honco Chat is Honco's own, self-hosted team collaboration workspace. It is a **customised fork of Mattermost** (an open-source team chat server) extended by one Honco-written plugin, **Honco Workspace** (`com.honco.workspace`), plus a small set of services around it for video meetings, meeting recording, meeting notes, an AI-assistant boundary and an internal remote-support workflow.

**The problem it solves.** A team's conversation, its meetings, the follow-up work, the recordings and the notes normally live in four or five different tools. Honco Chat keeps them in one place, on Honco's own servers, so that:

- a meeting is started from the channel where the discussion is happening, and its card, recording and notes stay in that channel;
- the follow-up becomes a task beside the conversation instead of in a separate tracker;
- an internal "please help me with my screen" request is raised, picked up and closed inside the same workspace, with a record of who did what;
- everything is searchable together.

**What a team can do inside it (today, verified):**

| Area | What works |
|---|---|
| Chat | Everything Mattermost provides: teams, public/private channels, direct and group messages, threads, reactions, mentions, file attachments, message search, notifications, profile settings, System Console. |
| Tasks | Team-scoped tasks with assignee, status, due date, overdue view, filters and paging, with DM notifications. |
| Meetings | `/meet` creates a Jitsi room (now or scheduled), posts a **meeting card** in the channel, tracks who joined/left, ends the meeting when the room empties, and keeps the recording and notes with the card. |
| Recording | Jibri records the Jitsi room; the finished MP4 is delivered to Honco Chat and attached to the meeting card ("View Recording"). |
| Meeting Intelligence | Generates meeting notes (summary, key points, decisions, action items, participants) from the **channel conversation** during the meeting, through a Claude CLI on a separate host. *Currently blocked: that host is unreachable from this box.* |
| AI Assistant | The full UI and secure integration boundary for a live-meeting assistant (transcript, suggestions, insights, topics, post-call summary). *The AI engine itself belongs to another team's service, which is not connected.* |
| Remote Support | Request → agent queue → accept/reject → start/end/cancel, with a card in the channel and an audit trail. The actual remote control is done in RustDesk, outside Honco Chat. |
| Search | One search across tasks, meetings, recordings, summaries and support requests, scoped to what the user may see. |
| Administration | A Honco dashboard (health, usage, files, notifications, AI, security posture) for system admins, alongside Mattermost's System Console. |
| Profile photo | Mattermost's profile picture, relabelled "Profile Photo", shown everywhere including the Honco panels and cards. |

**What makes it different from a plain Mattermost installation:**

1. **De-branded and de-phone-homed.** The vendor's telemetry, crash reporting, update checker, marketplace and hosted push endpoints were removed by scripted, idempotent patches (`chat/branding/*`); the product is "Honco Chat" with its own wordmark.
2. **The Honco Workspace plugin** adds a right-hand panel (Tasks · Meetings · AI Assistant · Support · Search · Admin), two custom post types (meeting card, support card), a second App Bar icon for the AI Assistant, a channel-header button, WebSocket events and nine PostgreSQL tables of its own.
3. **`/meet`** is served by a small Python service (`meetsvc.py`) that creates Jitsi rooms, schedules reminders and registers each meeting with the plugin.
4. **Jitsi + Jibri** run in Docker and call back into Honco Chat when a recording finishes.
5. **Operations scripts** (`honcochat.sh`, `healthcheck.sh`, `backup-honcochat.sh`, `meet/meet.sh`) start, stop, check and back up the whole stack without `sudo`.

**Clearly separated:**

| Existing Mattermost capability (unchanged) | Honco-specific |
|---|---|
| Authentication, sessions, MFA, password policy | Honco Workspace plugin (all of Sections 6–15) |
| Teams, channels, DMs, threads, reactions, mentions | `/meet` service and meeting lifecycle |
| Message search, file attachments, file storage | Jibri recording delivery and recording cards |
| Profile settings, profile picture API and storage | "Profile Photo" wording and success/failure lines; avatars in Honco panels |
| Notifications (desktop, email, mentions) | Honco DM notifications from the `honco` bot with a deduplication ledger |
| System Console | Honco Administration dashboard (read-only) |
| Plugin framework, WebSocket, REST API | De-brand scripts, ops scripts, backups |

> **Note on "Honco Workspace V2".** A separate project (`C:\Users\Dell\Desktop\Honco_Workspace_V2`, Docker containers `honco-workspace-backend/frontend/postgres` on this machine) exists. It is **not** Honco Chat, shares no code or database with it, and is mentioned in this document only where a reader might confuse the two (Section 10 and 27).

---

# 2. Complete system architecture

```
                         ┌──────────────────────────────┐
                         │  Browser / Desktop / Mobile   │
                         │  (Mattermost web client with  │
                         │   the Honco plugin bundle)    │
                         └──────────────┬───────────────┘
                                        │ HTTPS/WS  (LAN: http://<host>:8065 ; public: Cloudflare tunnel)
                                        ▼
   ┌───────────────────────────────────────────────────────────────────────┐
   │  Honco Chat server  (Mattermost 11.11.0 fork, Go)     :8065           │
   │  ┌───────────────────────────────────────────────────────────────┐    │
   │  │  Honco Workspace plugin  com.honco.workspace                   │    │
   │  │  /plugins/com.honco.workspace/api/v1/...                       │    │
   │  │  tasks · meetings · recordings · summaries · AI · support ·    │    │
   │  │  search · admin · notifications · WebSocket events            │    │
   │  └───────────────────────────────────────────────────────────────┘    │
   └───────┬───────────────┬──────────────┬──────────────┬────────────────┘
           │               │              │              │
           ▼               ▼              ▼              ▼
   ┌──────────────┐  ┌───────────┐  ┌──────────┐  ┌─────────────────┐
   │ PostgreSQL 16│  │ meetsvc.py│  │ Local    │  │ SMTP (Gmail,    │
   │ honcochat    │  │ /meet     │  │ file     │  │ STARTTLS :587)  │
   │ 127.0.0.1:   │  │ 127.0.0.1:│  │ storage  │  │ e-mail          │
   │ 5433         │  │ 8077      │  │ run/data │  │ notifications   │
   └──────────────┘  └─────┬─────┘  └──────────┘  └─────────────────┘
                           │ registers meeting (shared secret)
                           ▼
   ┌───────────────────────────────────────────────────────────────┐
   │  Honco Meet  (Docker project honco-meet)                       │
   │  web :8443 · prosody 127.0.0.1:5280 · jicofo · jvb :10000/udp  │
   │  jibri (recorder)  ── finalize hook ──► POST /recordings/complete
   └───────────────────────────────────────────────────────────────┘

   External / other hosts (reached from the plugin, all optional):
     • Summarizer ("mother", 192.168.2.150:31013, SSH forced command → Claude CLI)   [unreachable today]
     • AI service (AIServiceURL, bearer token; pushes events with a callback secret)  [not configured]
     • RustDesk relay (hbbs/hbbr on ubuntu-3)                                          [no API; not on this host]
     • Cloudflare quick tunnel (cloudflared)                                            [binary absent on this box]
```

### 2.1 Component-by-component

| Component | What it does | Why it exists | How Honco talks to it | Local / external | Reachable now? | Known limitations |
|---|---|---|---|---|---|---|
| **Honco Chat server** (`~/honco-chat/build/honcochat`, Mattermost fork) | Chat, users, teams, channels, files, sessions, plugin host. Listens on `:8065`. | The product. | Browser ↔ REST `/api/v4` + WebSocket `/api/v4/websocket`. | Local (WSL) | Yes — `GET /api/v4/system/ping` → `OK` | `SiteURL` is `http://localhost:8065`; a public hostname needs the tunnel and `SiteURL` changed together. |
| **Web client** (`~/honco-workspace/server/webapp/channels/dist`, symlinked as `server/client`) | The React UI. Contains the de-brand and the Profile Photo patches. | Rebuilt from source after every branding script. | Served by the server. | Local | Yes | Rebuild takes 7–17 min and starves the box (see §27). |
| **Honco Workspace plugin** (`plugins/com.honco.workspace`, Go + one JS bundle) | Every Honco feature: Sections 6–15. | Keeps Honco code out of the upstream fork so upstream pulls and de-brand scripts never collide with it. | Runs inside the server; REST under `/plugins/com.honco.workspace/api/v1`; plugin API for posts, users, files, KV, WebSocket. | Local | Yes — enabled, version 0.1.0 | — |
| **PostgreSQL 16** (`~/honco-chat/pgdata`, db `honcochat`, port 5433) | All data: 85 Mattermost tables + 9 Honco tables (94 total). | — | `database/sql` via the plugin's own store (`store.go`) and Mattermost's store. | Local | Yes — health "Healthy · 2–6 ms" | Single instance, no replica. |
| **meetsvc.py** (`~/honco-chat/run/meetsvc.py`, 127.0.0.1:8077) | The `/meet` slash command: instant/scheduled rooms, reminders, list/cancel/history, the schedule dialog. Registers each room with the plugin. | Mattermost slash commands need an HTTP target; scheduling is kept on disk so a restart loses nothing. | Mattermost → meetsvc (slash command POST); meetsvc → plugin `POST /meetings/register` with `X-Honco-Service-Secret`; meetsvc → Mattermost REST with a bot token. | Local | Yes — health "Meeting Service Healthy" | Localhost only by design. |
| **Jitsi** (Docker: web `:8443`, prosody, jicofo, jvb) | The video room. | Self-hosted meetings. | Browser joins `MeetPublicURL/<room>`; plugin asks Prosody (`mod_muc_size`, `127.0.0.1:5280`) who is in the room every 10 s. | Local (Docker Desktop on this machine) | Containers up 8 h; **the admin probe reports "not reachable from this host"** because `MeetPublicURL` is the host LAN address `https://192.168.1.11:8443`, which is not routable from inside WSL right now. Meetings still worked in today's tests. | Self-signed certificate on the LAN; `MeetPublicURL` must be reachable by *participants'* browsers. |
| **Jibri** (Docker) | Records the room to MP4 (1280×720, 25 fps) and runs `finalize.sh`. | Recording. | `finalize.sh` → `POST /recordings/complete` with `X-Jibri-Callback-Secret` (multipart). | Local (Docker) | Yes — health "idle" | One Jibri = one recording at a time; recording only starts when a participant presses Record in Jitsi. |
| **Summarizer** (`summarize/honco-summarize.sh` on host "mother") | Turns a conversation into meeting notes with the Claude CLI. | Claude is authenticated on that host only. | Plugin → `ssh` with a key pinned to a forced command (`SummarizerHost/Port/User/KeyPath`), or a local executable (`SummarizerCommand`). | External host | **No** — `192.168.2.150:22` does not answer from this box; every real generation today fails with "The summarizer is not reachable from this server." | Blocked (§27). |
| **AI service** (another team) | Voice capture, live transcript, suggestions, insights, topics, post-call summary. | Not Honco's engine. | Plugin → `POST {AIServiceURL}/v1/sessions` (bearer token); service → `POST /ai/events` with `X-Honco-AI-Secret`. | External | **Not configured** (`AIServiceURL` empty; overview: `service_configured:false`). | Blocked (§10, §27). |
| **RustDesk** (hbbs/hbbr on ubuntu-3) | Remote control. | Support agents actually take the screen there. | **No integration**: Honco stores no RustDesk credential and calls no RustDesk API; the support card tells the requester to open the RustDesk client. | External host | Not on this host; not tested from here. | Workflow only. |
| **SMTP** (Gmail, `smtp.gmail.com:587`, STARTTLS) | Mattermost e-mail: notifications, password reset, invites. | — | Mattermost's own mailer. | External | Configured (`smtp_configured:true`); sending not exercised in this documentation pass. | Credentials live only in `config.json`. |
| **Cloudflare tunnel** | Publishes `:8065` under a public hostname. | The WSL host's firewall blocks inbound 8065. | `honcochat.sh publish` runs `~/honco-chat/bin/cloudflared tunnel …`. | External | **Binary not present** (`~/honco-chat/bin/cloudflared` missing); tunnel status `down`. | A *quick* tunnel changes hostname on every restart; production needs a named tunnel and `SiteURL` set to it. |
| **Transcription worker** (`transcribe/`, ubuntu-3, GPU) | Batch speech-to-text + notes from recordings (whisper.cpp or the in-house "vixy" model). | Post-call transcripts. | Separate worker; **not called by the plugin**. | External host | Not on this host. | Independent of the AI Assistant and of Meeting Intelligence. |

---

# 3. Request / data flow

### 3.1 The general shape

```
User clicks a control in the Honco panel or on a card
  │
  ▼
Plugin webapp handler (main.js)  ── fetch(..., {credentials:'same-origin', 'X-Requested-With':'XMLHttpRequest'})
  │
  ▼
Mattermost routes /plugins/com.honco.workspace/api/v1/...  →  plugin.ServeHTTP  (plugin.go)
  │   • securityHeaders            (hardening.go)  nosniff / DENY / no-referrer / no-store / Permissions-Policy
  │   • rate limiter               reads 240/min burst 60 · mutations 60/30 · service 60/20 · AI push 300/60
  │   • authentication             Mattermost-User-Id header must be present (set by the server from the session),
  │                                 or a shared-secret header on the three service routes
  ▼
Handler (tasks_api.go, meetings_api.go, ...)
  │   • authorization: team membership / channel membership / creator-or-assignee / support-agent / system admin
  │   • validation: sizes, statuses, ids
  ▼
Store (store.go + feature store files)  →  PostgreSQL  honco_*  tables
Mattermost plugin API                   →  posts, DMs, files, users, KV store
  │
  ▼
JSON response  +  (for meetings/support/AI) a WebSocket event to the channel
  │
  ▼
UI re-renders; other open clients update from the WebSocket event
```

### 3.2 Real examples

**Create task**
```
User → Tasks tab → New task → fills Title / Description / Assignee / Status / Due date → Save task
  → POST /plugins/com.honco.workspace/api/v1/tasks           (tasks_api.go: handleCreateTask)
  → must be a member of the team; assignee must be a member of the same team
  → INSERT honco_tasks                                        (task.go / store.go)
  → if assignee ≠ creator: DM from the honco bot "task_assigned" (notify.go, ledger honco_notifications)
  → 201 JSON task → row appears at the top of the list
```

**Create meeting**
```
User types /meet Client review  (or /meet in 30m …, /meet at 15:00 …, /meet schedule)
  → Mattermost POSTs the slash command to meetsvc.py (127.0.0.1:8077)
  → meetsvc builds the room URL  MEET_BASE/<slug>-<6 hex>  and POSTs /meetings/register (X-Honco-Service-Secret)
  → plugin: INSERT honco_meetings (status active, started_at 0)  +  creates the meeting-card post (custom_honco_meeting)
  → meetsvc answers the slash command (ephemeral reply with the link in a code block)
  → the card is broadcast as "meeting_updated" so open clients render it
```

**Join meeting**
```
User clicks Join Meeting on the card
  → opens card.join_url (MeetPublicURL/<room>) in a new tab — Jitsi prejoin → the room
  → every 10 s the plugin asks Prosody /room-size?room=<room>@<XMPPDomain> (muc.go)
  → first occupant seen: started_at = now, "meeting_started" thread reply; each occupant: honco_meeting_participants
  → card shows "Meeting is active · N participants"; thread replies "joined"/"left"
```

**Recording completion**
```
Participant pressed Record in Jitsi → Jibri writes /storage/<id>/<file>.mp4 + metadata.json
  → Jibri runs /config/finalize.sh (meet/jibri-finalize.sh)
  → POST /recordings/complete  multipart: room_name, status, duration, file   (X-Jibri-Callback-Secret)
  → plugin matches room_name → honco_meetings; size ≤ MaxRecordingMB (0 ⇒ Mattermost MaxFileSize 100 MB)
  → uploads through the Mattermost file API (fileinfo row, stored under run/data/) as the honco bot
  → INSERT honco_recordings (ready | failed) → card gains "View Recording" (or "Recording failed")
  → DM "recording_ready"/"recording_failed" to the meeting creator (deduplicated)
```

**Meeting summary (Meeting Intelligence)**
```
User → Meetings tab → chooses a meeting → Generate summary
  → POST /meetings/{id}/summary                  (summary_api.go)
  → conversation window = meeting created_at … latest recording end (or now), capped at 6 h / 1000 msgs / 200 k chars,
    bot posts excluded (summarizer.go)
  → honco_meeting_summaries status pending → ssh <SummarizerHost> honco-summarize.sh  (or SummarizerCommand)
  → ready (summary, key points, decisions, action items, participants) | empty | failed
  → DM "meeting_summary_ready"/"_failed" with a permalink; card gains View Summary
  TODAY: the SSH host is unreachable → every real generation ends "failed: The summarizer is not reachable from this server."
```

**AI Assistant**
```
User → meeting card "AI Assistant" (or App Bar icon) → panel opens on the AI tab for that meeting → Start AI session
  → POST /meetings/{id}/ai/session        (ai_api.go)  → adapter POST {AIServiceURL}/v1/sessions with the bearer token
  → session stored in the plugin KV store  ai:session:<meeting_id>
  → the service pushes POST /ai/events  (X-Honco-AI-Secret): status / transcript / suggestion / insight / topics / final / error
  → plugin appends to the session, broadcasts "ai_event" on the channel → panel updates live
  → final → DM "AI Meeting Summary Ready" with a permalink; error → "AI Meeting Insights Unavailable"
  TODAY: AIServiceURL is empty → Start returns the honest "not configured / unavailable" state; nothing is faked.
```

**Support request**
```
User → Support tab → Request Support → describes the issue → Send
  → POST /support/requests  (support_api.go)  → INSERT honco_support_requests (open) + honco_support_events (created)
  → support card post in the channel; the support channel (SupportTeamName/SupportChannelName) is notified
  → an agent (member of the support channel) sees it in the queue → Accept Request / Decline → Start Session → End Session
  → every transition: UPDATE request, INSERT event, card re-rendered, "support_updated" WebSocket, DM to the other party
```

**Search**
```
User → Search tab → types a term → Enter (or Mattermost's search box → Honco results button)
  → GET /search?q=…&type=all|tasks|meetings|recordings|summaries|support&page=N   (search_api.go)
  → scope = the caller's teams and channel memberships (searchScope); SQL ILIKE with escaped wildcards
  → grouped pages with totals → results list; clicking a hit opens the relevant tab/post
```

**File upload** (Mattermost, unchanged)
```
User attaches a file → POST /api/v4/files (Mattermost) → fileinfo row + run/data/<date>/… → post carries file_ids
  → readers must be channel members; public links are disabled (EnablePublicLink=false)
```

**Notification**
```
Event (task assigned, recording ready, summary ready, support accepted, …)
  → notify.go claim(kind, subject, recipient, dedupe_key): INSERT honco_notifications (unique index) — second attempt is a no-op
  → DM from the honco bot to the recipient (Mattermost then applies the user's own desktop/e-mail preferences)
```

**Admin dashboard**
```
Admin → Admin tab
  → GET /admin/overview  (system admin only)  — counts from honco_* and Mattermost tables, config flags, recent failures
  → GET /admin/health    — live probes: plugin, PostgreSQL, Jitsi (MeetPublicURL), Jibri (127.0.0.1:2222), meetsvc, plugin state
```

---

# 4. Complete UI map

Verified against the running web client on 14 Sep 2026.

```
LOGIN  (/login — username/e-mail + password; MFA enabled server-wide as an option; "View in Browser" interstitial on first visit)
  │
  ▼
MAIN WORKSPACE (Mattermost)
  ├── Global header: Honco wordmark · search box · @ mentions · saved · settings (gear) · account menu (avatar)
  │     └── Account menu → status · Set custom status · Profile (Profile Settings / Security) · Log out
  ├── Left sidebar: team name · Find channel · Threads · CHANNELS · DIRECT MESSAGES · Add Channels / Invite Members
  ├── Channel view: header (name, members #member_rhs, pinned/files, info) · posts · composer (attach, emoji, formatting)
  ├── Threads (RHS reply view)  ·  Channel members (RHS)  ·  Channel info (RHS)
  ├── App Bar (right edge, desktop):
  │     ├── [Honco Workspace]  → Honco Workspace panel (RHS)
  │     └── [AI Assistant]     → same panel, opened on the AI Assistant tab for the channel's meeting
  ├── Channel header button "Honco Workspace" (shown when the App Bar is hidden / phone channel menu)
  ├── Mattermost search box → "Messages / Files" plus a Honco results button (plugin-registered search component;
  │     search pills are suppressed by Mattermost on an unlicensed server)
  │
  ├── HONCO WORKSPACE PANEL (RHS, tabs; expandable)
  │     ├── Tasks              All statuses ▾ · Mine ☐ · New task · list (title, assignee avatar, due/overdue, status ▾, Edit, Delete) · Previous/Next
  │     ├── Meetings           "Meeting Intelligence": Choose a meeting ▾ · Generate summary / Regenerate / Try again · summary sections
  │     ├── AI Assistant       header (status badge) · Start AI session / Stop session / Reconnect · live blocks · meeting ▾ · AI Meeting Summary
  │     ├── Support            Request Support · my requests · agent queue (Accept Request / Decline / Start Session / End Session / Cancel)
  │     ├── Search             query · All / Tasks / Meetings / Recordings / Summaries / Support · results · paging
  │     └── Admin (admins)     Refresh · System health · Usage · Files · Notifications · AI Assistant · Security · Recent failures
  │
  ├── MEETING CARD (custom post type custom_honco_meeting, posted by the honco bot)
  │     ├── title, "Started by <creator avatar+name>", status line (scheduled / active · N participants / ended), participants
  │     ├── [Join Meeting]        while status ≠ ended and a join_url exists
  │     ├── [AI Assistant]        always (opens the panel on the AI tab for this meeting)
  │     ├── [View Summary]        when has_summary
  │     ├── [View Recording]      when recording_status = ready   (badge "Recording failed" / "Recording unavailable" otherwise)
  │     └── thread replies from the bot: started · joined · left · ended · recording · summary
  │
  ├── SUPPORT CARD (custom_honco_support): "Remote Support Request", requester avatar+name, status dot, issue, agent
  │
  └── PROFILE (account menu → Profile)
        ├── Profile Settings: Full Name · Username · Nickname · Position · Email · **Profile Photo** (Change photo / Save photo / Remove photo / Cancel)
        └── Security: password, MFA, sessions, tokens (Mattermost)
SYSTEM CONSOLE (/admin_console — Mattermost, system admins): users, teams, config, plugins, logs
```

Phone width (≤ 768 px): the left hamburger opens the sidebar; the right "≡" opens the channel menu, which lists Profile, Settings, View Members, … and the Honco Workspace button.

---

# 5. Button-by-button user guide

Each entry: **Location · Who sees it · What happens · Frontend · API · Backend · Storage · Result · Errors.** Frontend is always `plugins/com.honco.workspace/webapp/dist/main.js` (one bundle; the function name is given). API paths are relative to `/plugins/com.honco.workspace/api/v1`.

### 5.1 Honco Workspace (App Bar icon / channel header button)
- **Location:** right App Bar; on phones, the channel "≡" menu.
- **Who:** every logged-in user.
- **What happens:** toggles the right-hand panel; the last tab is remembered per session; deep links (View Summary, AI Assistant) choose the tab.
- **Frontend:** `Plugin.initialize` → `registerAppBarComponent`, `registerRightHandSidebarComponent`, `registerChannelHeaderButtonAction`; `HoncoPanel`.
- **API:** none on open; each tab fetches on demand. **Errors:** none.

### 5.2 AI Assistant (App Bar icon)
- **Who:** everyone. **What:** opens the panel on the AI tab for the channel's most recent meeting (`GET /channels/{channel_id}/meetings` to find it). **Frontend:** `AIIntent`, `AIPanel`.

### 5.3 Tasks tab
| Button | Who | What happens | API / backend | Storage | Result / errors |
|---|---|---|---|---|---|
| **All statuses ▾** | all | filters the list by `todo / in_progress / done` | `GET /tasks?team_id&status&page` · `handleListTasks` | `honco_tasks` | list reloads; the panel asks for 20 per page (server default 60, max 200) |
| **Mine ☐** | all | only tasks assigned to me | `GET /tasks?assignee_id=<me>` | — | — |
| **New task** | all team members | opens the inline form (Title*, Description, Assignee ▾ (team members from `/api/v4/users?in_team`), Status ▾, Due date) | — | — | — |
| **Save task** (create) | all | validates title (required, ≤ 256), status | `POST /tasks` · `handleCreateTask` (`tasks_api.go`) | `INSERT honco_tasks`; `honco_notifications` if assigned | 201; row on top; DM to assignee. Errors: 400 "title is required" / "status must be one of todo, in_progress, done" / "assignee is not a member of this team"; 403 not a team member |
| **Edit** | creator or assignee | opens the same form pre-filled | `PATCH /tasks/{id}` · `handleUpdateTask` | `UPDATE honco_tasks` | reassignment DMs `task_reassigned`; 403 "only the creator or assignee can change this task" |
| **Status ▾** (row) | creator or assignee | changes status in place | `PUT /tasks/{id}/status` · `handleSetTaskStatus` | `UPDATE` | DM `task_status` / `task_completed` to the other party; 403 as above |
| **Delete** → **Yes** | creator only | soft-delete after confirmation | `DELETE /tasks/{id}` · `handleDeleteTask` | `deleted_at = now` | row disappears; 403 "only the creator can delete this task" |
| **Previous / Next** | all | paging | `GET /tasks?page=N` | — | "1–20" counter |

### 5.4 Meetings tab (Meeting Intelligence)
| Button | Who | What happens | API / backend | Storage | Result / errors |
|---|---|---|---|---|---|
| **Choose a meeting ▾** | channel members | lists this channel's meetings (newest first) with their summary status | `GET /channels/{channel_id}/meetings` · `handleListChannelMeetings` | `honco_meetings`, `honco_meeting_summaries` | — |
| **Generate summary** | channel members | starts a generation for the chosen meeting | `POST /meetings/{id}/summary` · `handleGenerateSummary` (`summary_api.go`) | `honco_meeting_summaries` pending → ready/empty/failed | polls `GET /meetings/{id}/summary`; ready → sections render; **today: "The summarizer is not reachable from this server."** |
| **Regenerate** | same | new generation replacing the old row | same | same | — |
| **Try again** | same | after a failure | same | same | — |

### 5.5 AI Assistant tab
| Button | Who | What happens | API / backend | Storage | Result / errors |
|---|---|---|---|---|---|
| **Start AI session / Start new AI session** | channel members of the meeting | asks the external service to attach to this meeting | `POST /meetings/{id}/ai/session` · `handleStartAISession` (`ai_api.go`) → `AIIntegrationService.StartSession` (`ai_adapter.go`) | KV `ai:session:<meeting_id>` | status → connecting → live (events arrive). **Today:** service not configured → status `not_configured`/`unavailable`, panel says so; nothing is faked |
| **Stop session** | same | ends the session | `DELETE /meetings/{id}/ai/session` · `handleEndAISession` | KV | status `ended` |
| **Reconnect** | same | re-reads session state after a WebSocket drop | `GET /meetings/{id}/ai` | — | — |
| **Show previous suggestions (N)** / **Load earlier lines** / **Jump to latest** | same | UI only | `GET /meetings/{id}/ai/transcript?before=<seq>` for older lines | KV | — |
| **Meeting ▾** | same | switch between this channel's meetings | `GET /channels/{channel_id}/meetings` | — | — |
| **Transcript** (section toggle) | same | expands the stored transcript | — | — | — |

### 5.6 Support tab
| Button | Who | What happens | API / backend | Storage | Result / errors |
|---|---|---|---|---|---|
| **Request Support** → **Send** | everyone | creates a request for the current channel with a free-text issue (≤ 1024 chars; the form warns never to include passwords) | `POST /support/requests` · `handleCreateSupportRequest` (`support_api.go`) | `honco_support_requests` (open), `honco_support_events` (created), support card post | card in the channel; support channel notified; 400 "issue is required"; 403 not a channel member |
| **Cancel** (my request) | requester | cancels while open/accepted | `POST /support/requests/{id}/cancel` | status cancelled + event | card updates |
| **Accept Request** | support agents (members of `SupportChannelName` in `SupportTeamName`) | claims the request | `POST …/accept` · `handleAcceptSupport` | accepted, agent_id | DM to requester `support_accepted`; 403 "not a support agent"; 409 already taken |
| **Decline** | agents | rejects with an optional reason | `POST …/reject` | rejected | DM `support_rejected`; the request stays visible as declined |
| **Start Session** | the assigned agent | marks the remote session active; the card shows the RustDesk hint ("Open the RustDesk client and share your ID with the agent") | `POST …/start` | active, started_at | — |
| **End Session** | the assigned agent | closes it | `POST …/end` | ended, ended_at | DM `support_ended` |
| **Refresh** | all | reloads the list | `GET /support/requests?team_id` | — | — |

### 5.7 Search tab
| Control | What happens | API |
|---|---|---|
| query + Enter / **Search** | searches everything the user may see | `GET /search?q=&type=all&page=0` · `handleSearch` (`search_api.go`, `search.go`) |
| **All / Tasks / Meetings / Recordings / Summaries / Support** chips | restricts the type | `type=` |
| **Previous / Next** | paging within a type | `page=` |
| result row | opens the owning tab / channel post | — |

### 5.8 Admin tab (system admins only)
| Control | What happens | API |
|---|---|---|
| **Refresh** | re-runs both calls | `GET /admin/overview`, `GET /admin/health` (`admin_api.go`, `admin_store.go`) |
| sections | read-only figures (see Section 15) | — |

### 5.9 Meeting card buttons (`MeetingCard`)
| Button | Shown when | What happens |
|---|---|---|
| **Join Meeting** | `status ≠ ended` and `join_url` | opens `MeetPublicURL/<room>` in a new tab (Jitsi) |
| **AI Assistant** | always | `window.HoncoOpenAIAssistant(meeting_id)` → panel, AI tab, this meeting |
| **View Summary** | `has_summary` | opens the panel on Meeting Intelligence with this meeting selected. **Known defect:** if the panel is closed it does not open it (`MeetingIntent.open` never opens the RHS); works once the panel is open. Found in the audit; not fixed. |
| **View Recording** | `recording_status = ready` | link to `/api/v4/files/<recording_file_id>` (Mattermost file download; channel membership enforced) |
| badge **Recording failed / unavailable** | `recording_status = failed` / file missing | informational |

### 5.10 Support card (`SupportCard`)
Read-only: "Remote Support Request", requester (avatar + name), status dot (Waiting for support / Accepted / Session active / ended / cancelled / declined), issue, agent. Actions are on the Support tab.

### 5.11 Profile → Profile Settings → Profile Photo
| Button | What happens | API | Notes |
|---|---|---|---|
| **Edit** | expands the section: current photo, help "JPG, PNG or BMP • maximum size 100MB" | — | — |
| **Change photo** | opens the file picker (`accept=".jpeg,.jpg,.png,.bmp"`) and shows a preview | — | client check: type ∈ {jpeg, png, bmp}, size ≤ `FileSettings.MaxFileSize` |
| **Save photo** | uploads | `POST /api/v4/users/{me}/image` (Mattermost) | success line "Profile photo updated."; failure "Unable to update profile photo. Please try again."; avatar changes everywhere without reload |
| **Remove photo** → **Save photo** | restores the default avatar | `DELETE /api/v4/users/{me}/image` | "Profile photo removed." |
| **Cancel** | discards the choice | — | — |

### 5.12 `/meet` slash command (typed in the composer)
| Form | What happens |
|---|---|
| `/meet` | room named after the channel, now |
| `/meet <name>` | named room, now |
| `/meet in 30m <name>` / `/meet at 15:00 <name>` | posts now, reminds the channel at that time (state in `run/meet-schedule.json`) |
| `/meet schedule` | the same as an interactive dialog |
| `/meet list` · `/meet history` · `/meet cancel <id>` | pending reminders · past meetings · drop one |
All forms register the room with the plugin and produce a meeting card; the reply is ephemeral with the link in a code block (Mattermost's own copy button).
