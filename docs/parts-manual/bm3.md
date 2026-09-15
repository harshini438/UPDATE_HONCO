
---

# 4. Main chat buttons

Everything in this part except `/meet` and the two cards is **native Mattermost** — Honco changed none of it. It is documented so you know where the boundary is.

| Control | What happens behind the scenes | Native or Honco |
|---|---|---|
| **New message / Send (➤ or Enter)** | `POST /api/v4/posts` → row in `posts` → Mattermost broadcasts `posted` on the WebSocket → the message appears for everyone in the channel; mentions trigger notifications. | Native |
| **Reply / Thread** | The reply panel opens on the right; the reply is a post with `root_id` set. Honco's bot uses the same mechanism for meeting lifecycle replies. | Native |
| **Attach file / Upload** | `POST /api/v4/files` (≤ 100 MB) → `fileinfo` row + bytes under `run/data/<date>/` → the post carries the file id. | Native |
| **Emoji / Add Reaction** | `POST /api/v4/reactions`. | Native |
| **Mention (@)** | Autocomplete from `GET /api/v4/users`; the mention is plain text in the message that Mattermost resolves at render and notification time. | Native |
| **Channel actions** (☆ favourite, channel menu, Members, Channel files, View Info, Notification Preferences, Mute) | Mattermost channel APIs; nothing Honco-specific. | Native |
| **DM / Group DM** | "Write a direct message" opens the member picker; selecting people creates a DM or group channel (`POST /api/v4/channels/direct` or `/group`). | Native |
| **Search (top box)** | Mattermost message and file search. The Honco plugin registers an extra results button here (`registerSearchComponents`); Mattermost suppresses plugin search *pills* on an unlicensed server, which is why the Honco entry point is the panel's Search tab. | Native + small Honco registration |
| **Notifications** | Desktop and e-mail behaviour is Mattermost's, driven by ⚙ Settings → Notifications. Honco's own notifications are DMs from the `honco` bot (Part 15). | Native delivery, Honco content |
| **Profile** | Account menu → Profile (Part 5). | Native + Honco wording |
| **Message menu (⋯)** | Mattermost's post menu (edit, copy link, pin, delete, …). Not modified by Honco. | Native |
| **Channel menu (name ▾)** | View Info, Mute, Notification Preferences, Channel Settings, Members, Move to. | Native |
| **`/meet …`** | The one Honco command in the composer — see Part 3 and Part 7. | **Honco** |
| **Meeting card / Support card** | Custom post types rendered by the plugin (`custom_honco_meeting`, `custom_honco_support`). | **Honco** |

---

# 5. Profile & profile photo

```
Profile Settings  →  Profile Photo → Edit
        ↓
Select Photo   ("Change photo"; the browser checks the type and the size first)
        ↓
Preview        (shown before anything is uploaded; Cancel discards it)
        ↓
Save           ("Save photo")
        ↓
Mattermost Avatar API      POST /api/v4/users/{id}/image      (only your own id; anyone else → 403)
        ↓
Existing user avatar storage  (the server resizes and stores the image under your user id in the file store;
                               the ONLY database change is Users.LastPictureUpdate)
        ↓
user_updated WebSocket event  (Mattermost tells every connected client)
        ↓
All UI locations update       (each client rebuilds the URL as /api/v4/users/{id}/image?_=<LastPictureUpdate>)
```

| Endpoint | What it does | Notes |
|---|---|---|
| `POST /api/v4/users/{id}/image` | upload/replace | multipart `image`; **authorization**: `SessionHasPermissionToUserOrBot` — you may only change your own (or a bot you manage); **validation**: `Content-Length` against `FileSettings.MaxFileSize` (413 if larger) and an image decode (400 if it is not a real image). The browser additionally restricts the picker to `.jpeg,.jpg,.png,.bmp` and checks the size first. |
| `DELETE /api/v4/users/{id}/image` | remove | same permission check; restores the generated default avatar. |
| `GET /api/v4/users/{id}/image?_=<ts>` | read | any authenticated user; **401** when anonymous. The response is cacheable for a day, which is why the timestamp is always in the URL. |

**Storage.** Under the user id in Mattermost's file store (`FileSettings.Directory`, here `~/honco-chat/run/data/`). **`last_picture_update`** is the millisecond timestamp of the last change (negative after a removal) and acts as the **cache key**: a new value means a new URL, so no browser or proxy can show the old picture. That is the whole cache-invalidation mechanism — there is nothing custom.

**Where the avatar appears:** messages, threads, DMs and group DMs, the channel member list, the profile popover, the DM picker, mention search, the account menu — and inside Honco: **task rows (assignee), support rows and support cards (requester and agent), meeting cards ("Started by")**. All of them render the same URL through one component, `UserAvatar` in `main.js`, which follows `last_picture_update` from the webapp store.

**Meeting participants are the exception:** they are Jitsi display names with no Mattermost identity behind them, so they show initials rather than photos.

> **No new avatar table.** Mattermost already stores one picture per user, serves it with cache-busting, checks permission and broadcasts changes. A second Honco copy would have to be kept in sync and secured again for no benefit.

---

# 6. Tasks

```
USER                    FRONTEND                 API                    BACKEND               DATABASE            RESULT
────                    ────────                 ───                    ───────               ────────            ──────
New task          →  TaskEditor opens        (none)                  —                     —                   form
Save task         →  request POST         →  POST /tasks          →  tasks_api.go        → INSERT honco_tasks → row + DM
 (assignee set)                                                       notify.go           → honco_notifications
Edit → Save       →  TaskEditor (edit)    →  PATCH /tasks/{id}    →  handleUpdateTask    → UPDATE            → row
Status ▾          →  TaskRow              →  PUT /tasks/{id}/status→ handleSetTaskStatus → UPDATE            → row + DM
Delete → Yes      →  TaskRow (confirm)    →  DELETE /tasks/{id}   →  handleDeleteTask    → deleted_at set    → row gone
Filters / paging  →  TasksPanel           →  GET /tasks?…         →  handleListTasks     → SELECT            → list
```

| Rule | Detail |
|---|---|
| Scope | Tasks belong to a **team**. Every request carries `team_id`; you must be a member. |
| Assignment | The assignee must be a member of the same team — re-checked on the server for every create and update. |
| Status | Exactly three values: `todo`, `in_progress`, `done`. |
| Due date | Optional, stored in `due_at` (milliseconds). |
| Overdue | Not stored — computed: due date in the past and status ≠ done. Shown in red on the row. |
| Reminders | A background scanner every 10 minutes sends "due soon" (within 24 h) and "overdue" once per task per due date. |
| Authorization | Read: team members. Edit/status: creator **or** assignee. Delete: creator only. |
| Paging | Panel asks for 20; server default 60; maximum 200. |

**If I want to change Tasks:** UI → `main.js` (`TasksPanel`, `TaskEditor`, `TaskRow`); rules and page sizes → `server/task.go`; HTTP → `server/tasks_api.go`; SQL and the table → `server/store.go` (migration 1); notifications and the scanner → `server/notify.go`. Tests: `go test ./server/...`, browser `tasks-ux.js`, API `p11-tasks.sh`.

---

# 7. Meetings

```
User
 ↓ types /meet Client review
Mattermost slash command       (POST to the command's target)
 ↓
meetsvc.py  (127.0.0.1:8077)   builds  <MeetPublicURL>/client-review-<6 hex>, keeps schedules on disk
 ↓ POST /meetings/register  (X-Honco-Service-Secret)
Honco meeting registration     recordings_api.go → handleRegisterMeeting
 ↓
PostgreSQL                     INSERT honco_meetings (status active, started_at 0)
 ↓
Jitsi room                     created on demand when the first person opens the address
 ↓
Meeting card                   posted by the honco bot (custom_honco_meeting)
 ↓
Participant lifecycle          poller every 10 s asks Prosody who is in the room
 ↓
WebSocket  meeting_updated     →  Browser: the card re-renders in place
```

**Controls on the card:** Join Meeting (while not ended) · AI Assistant (always) · View Summary (when a summary exists) · View Recording (when a recording is ready) — all in Part 3. Badges replace the recording button when a recording failed or its file was deleted.

**Participant information** shown on the card is the count plus the Jitsi display names, from `honco_meeting_participants`.

**Other `/meet` forms:** `list` (pending reminders in this channel), `history` (past meetings), `cancel <id>`, `schedule` (the same as `in`/`at` but as a form). All answered by `meetsvc.py`; reminders survive a restart because they are kept in `~/honco-chat/run/meet-schedule.json`.

---

# 8. Meeting lifecycle

| State | When | Who sets it | Visible effect |
|---|---|---|---|
| **Created** | `/meet` runs | `handleRegisterMeeting` | row `status='active'`, `started_at=0`; the card appears. A scheduled meeting is `scheduled` until its time. |
| **Active / Started** | the poller sees the **first** occupant | `meeting_lifecycle.go` | `started_at = now`; thread reply "started"; card reads "Meeting is active · N participants". |
| **Participant joins** | a new occupant appears | `meeting_lifecycle.go` + `muc.go` | row in `honco_meeting_participants`; "joined" reply; count updates. |
| **Participant leaves** | an occupant disappears | same | `left_at` set, `present=false`; "left" reply. |
| **Empty meeting** | occupants = 0 | same | nothing yet — the 90-second grace period starts. |
| **Ended** | empty for **90 s** (`emptyRoomGrace`) | same | `status='ended'`, `ended_at`; "ended" reply; Join Meeting disappears. |
| **30-minute fallback** | nobody ever joined for **30 min** (`neverJoinedTimeout`) | same | the meeting is ended so it cannot hang around for ever. |

Both timings are **implemented and verified** (constants `participantPollInterval = 10s`, `emptyRoomGrace = 90s`, `neverJoinedTimeout = 30min` in `server/meeting_lifecycle.go`). The 90-second path was broken until commit `e9b03ed` — `started_at` used to be stamped only on a status transition that never happened for `/meet`-registered meetings — and was then verified with two real participants (ended 108 s after the room emptied; never-joined meetings ended at 30.0–30.1 min).

Every state change publishes **`meeting_updated`** and, where appropriate, posts a reply in the card's thread through `postOnce`, which claims a row in `honco_notifications` so a repeated poll cannot post twice.

---

# 9. Jitsi

**What it is:** open-source video-conferencing software, running here as Docker containers (project `honco-meet`).
**Why Honco uses it:** Honco does not build video. Jitsi provides the room, the media and a recorder that can be self-hosted.

```
Honco Chat  ──── link on the meeting card ────►  Jitsi room (web :8443)  ────►  audio / video / screen share
Honco Chat  ◄─── "who is in room X?" every 10 s ───  Prosody (127.0.0.1:5280, mod_muc_size)
```

**When Join Meeting is clicked** the browser opens `card.join_url` — `MeetPublicURL` + the room name, for example `https://192.168.1.11:8443/client-review-a1b2c3`. Honco sends nothing at that moment; it learns you joined from the next poll.

| Part | Belongs to | Role in this deployment |
|---|---|---|
| Meeting card, `/meet`, participant tracking, lifecycle, recording delivery | **Honco** | everything around the room |
| **Jitsi web** | Jitsi | the page you join, prejoin screen, toolbar (including Record) |
| **Prosody** | Jitsi | presence/chat server; **relevant to Honco** because the plugin reads room occupancy from it |
| **Jicofo** | Jitsi | assigns resources and the Jibri recorder |
| **JVB** | Jitsi | routes the audio/video streams (UDP 10000) |

**Honco's Jitsi settings:** `MeetPublicURL` (must match meetsvc's `MEET_BASE` and be reachable by *participants*), `ProsodyHTTPURL` (loopback only), `XMPPDomain` (`meet.jitsi`).

---

# 10. Jibri / recording

```
Meeting running in Jitsi
 ↓  a participant presses "Start recording" in the Jitsi toolbar   ← Honco NEVER starts a recording
Jibri joins invisibly and records (1280×720, 25 fps)
 ↓  the participant stops it, or the room empties                   ← "Stop recording"
MP4 written to /storage/<recording-id>/ plus metadata.json (the room name is in meeting_url)
 ↓
Finalize: Jibri runs /config/finalize.sh   (source of truth: meet/jibri-finalize.sh)
 ↓   reads the secret from /config/honco-callback.secret (0600, never in Git)
Honco callback:  POST /recordings/complete   multipart room_name, status, duration, file
 ↓   header X-Jibri-Callback-Secret, compared in constant time
Mattermost file storage: uploaded by the honco bot → fileinfo row + bytes under run/data/
 ↓
honco_recordings row (ready | failed) → Recording card: "View Recording"; bot posts "Recording ready for … (size · N min)"
```

| Step | Detail |
|---|---|
| Start / stop recording | Jitsi toolbar, by a participant. There is no Honco button for this. |
| Finalize | **Not run at all** when zero media was captured — that case shows up as a meeting whose card never gains a recording. Log inside the container: `/storage/finalize.log`. |
| Callback authentication | `X-Jibri-Callback-Secret` must match `JibriCallbackSecret`; otherwise **401** and a warning in the server log. Rate-limited as a service route (60/min). |
| Size limit | `MaxRecordingMB` (0 ⇒ Mattermost's `MaxFileSize`, 100 MB here; hard ceiling 2048). Oversized recordings are stored as **failed** with a reason and reported in the channel — never silently dropped. |
| Storage | Ordinary Mattermost file, so channel permissions apply for free. |
| View Recording | `/api/v4/files/<id>` — verified today returning **HTTP 200 `video/mp4`**. |
| Recording unavailable | The card probes `/api/v4/files/{id}/info`; if the file was deleted it shows "Recording unavailable" instead of a broken player. |
| Recording failed | Red badge with the reason from `honco_recordings.error_message`. |
| **If Jibri is unavailable** | Jitsi shows "Recording failed to start" (or offers no Record button); nothing reaches Honco; the card simply never gains a recording; the Admin health card shows Jibri unavailable. After a Docker Desktop restart Jibri usually has to be recreated. |

**Code:** `meet/jibri-finalize.sh`, `meet/setup-jibri.sh`, `server/recordings_api.go` (`handleRecordingComplete`), `server/recording.go`, `main.js` (`MeetingCard`). **Tables:** `honco_recordings` + Mattermost `fileinfo`.

---

# 11. Meeting Intelligence

Four things people mix up:

| Term | What it is |
|---|---|
| **Meeting** | the room, the card, who joined — `honco_meetings` |
| **Recording** | the MP4 from Jibri — `honco_recordings` |
| **Meeting Intelligence** | **written notes made from the channel conversation during the meeting** — `honco_meeting_summaries` |
| **AI Assistant** | a **live** assistant during the call, from an external AI service — plugin key-value store |

```
Meeting
 ↓
Channel conversation      messages posted in the meeting's channel between its creation and the end of its last
                          recording (or now); capped at 6 hours / 1000 messages / 200 000 characters; bot posts excluded
 ↓
Summary request           Meetings tab → Generate summary → POST /meetings/{id}/summary → row status "pending"
 ↓
External Claude service   ssh <SummarizerHost> with a key pinned to summarize/honco-summarize.sh → claude -p
                          (or a local executable if SummarizerCommand is set — it is empty here)
 ↓
Summary                   parsed into Summary / Key points / Decisions / Action items / Participants
 ↓
Honco UI                  the sections render; the card gains View Summary; the requester gets a DM
```

| Part | Owner |
|---|---|
| The window, the request, the states, parsing, storage, the panel, the deep link, the notifications, "Try again" | **HONCO CODE** |
| Producing the actual notes (the Claude CLI on the host "mother") | **EXTERNAL CLAUDE SERVICE** |

**Status today — BLOCKED, verified by running it:**
- A meeting with **no human messages** returns **empty** in seconds and never contacts the summarizer: *"No messages were posted in this channel during the meeting…"* (observed twice, `message_count = 0`).
- A meeting with **four real messages** returned **failed** — *"The summarizer is not reachable from this server."* (`message_count = 4`, 15 Sep 2026). `192.168.2.150:31013` does not answer from this machine.
- The `ready` rows in the database are **stub output** from a test fixture (they contain "STUB-ECHO"); **no genuine Claude summary exists here.**

Honco Chat performs **no speech-to-text** anywhere. A separate batch transcription worker exists in `transcribe/` for another host and is not called by the plugin.

---

# 12. AI Assistant

```
User
 ↓  clicks AI Assistant (card or App Bar)
Honco AI Assistant UI          main.js AIPanel — states, transcript, suggestions, insights, topics, summary view
 ↓  Start AI session (only rendered when a service URL is configured)
Honco integration              POST /meetings/{id}/ai/session → ai_api.go → ai_adapter.go
 ↓                              → POST {AIServiceURL}/v1/sessions   (Bearer token, callback URL, meeting info)
External AI service            listens to the call — voice, transcript, suggestions, insights, topics, summary
 ↓  AI event
Honco callback                 POST /ai/events  with X-Honco-AI-Secret  → validated, stored in the session
 ↓
WebSocket  ai_event            published to the meeting's channel
 ↓
Browser                        the panel appends the line / suggestion / insight / topic live
```

| HONCO OWNS THIS | EXTERNAL SERVICE OWNS THIS |
|---|---|
| The whole UI and its states (idle · connecting · live · reconnecting · processing · ended · completed · unavailable · failed) | Voice capture |
| Session handling (start, stop, reconnect), one session per meeting, stored as `ai:session:<meeting_id>` | Speech-to-text and the transcript lines |
| The callback endpoint `/ai/events` with its shared secret, event aliases and size bounds | Sales suggestions, insights, topics |
| The adapter (`POST {AIServiceURL}/v1/sessions`), bearer token kept server-side and never logged | The post-call summary and action items |
| WebSocket delivery, reconnect handling, authorization (channel membership), rate limiting (300 pushes/min) | Any AI model |
| DM notifications "AI Meeting Summary Ready" / "AI Meeting Insights Unavailable"; the Admin AI card | — |

**Honco does not own the AI engine.** There is no model, no speech recognition and no transcription in this codebase.

**Status today:** `AIServiceURL` is empty → `service_configured = false` → **Start / Stop / Retry connection are not rendered at all**; the panel shows its introduction instead. Verified twice on fresh, active meetings. `Reconnect`, the meeting selector, the transcript section and the empty states all work. The 24 stored sessions visible in the Admin card came from a test fixture, not from a real service. Everything Honco owns is covered by tests (94 API checks, 56 browser checks, 33 entry-point checks, 31 accessibility checks); **real AI end-to-end is BLOCKED** until a service is connected. The contract is written down in `AI_INTEGRATION.md`.

---

# 13. Remote Support

```
Support request   (anyone)            status: open        → support card in the channel + the support channel is told
 ↓
Agent queue       (support-channel members only)
 ↓
Accept            (agent)             accepted            → DM to the requester        [Decline → rejected]
 ↓
Start             (agent)             active              → the card shows the RustDesk hint
 ↓
RustDesk          the requester opens the RustDesk client and shares their ID; the agent controls the screen THERE
 ↓
End               (agent)             ended               → DM        [requester may Cancel → cancelled]
```

| | |
|---|---|
| **HONCO** | the request, its status and timestamps, who the agent is, the audit trail (`honco_support_events`: created / accepted / rejected / started / ended / cancelled with actor and detail), the card, the DMs, live updates by WebSocket. |
| **RUSTDESK** | the actual remote control. Honco stores no RustDesk credential, calls no RustDesk API, and **cannot tell whether a screen session happened**. |
| Who is an agent | Members of the channel in the plugin settings (`honco-support` in `harshini-sharma`). Not system administrators. Blank setting → requests can be raised but nobody can accept them. |
| Concurrency | Two agents accepting at once: the second gets **409**. |

**Verified today:** create → accept → start → end, each returning 200 with the expected status, and `honco_support_events` containing `created,accepted,started,ended`. **A real RustDesk session is NOT TESTABLE from this machine** (the relay is on another host).

---

# 14. Files & attachments

| | Normal attachment | Meeting recording |
|---|---|---|
| Uploaded by | a person (📎) | the plugin, as the `honco` bot |
| API | `POST /api/v4/files` | the same file API, called from Go |
| Stored | `~/honco-chat/run/data/<date>/…` | the same |
| Database | `fileinfo` | `fileinfo` **+** `honco_recordings` |
| Limit | 100 MB (`FileSettings.MaxFileSize`) | `MaxRecordingMB` (0 ⇒ the same 100 MB) |

- **Permissions:** a file is readable only by members of the channel whose post carries it; others get 403/404.
- **Private channels:** the same rule; Honco search never returns hits from channels you are not in.
- **Delete:** deleting the post soft-deletes the file; the meeting card then shows "Recording unavailable".
- **Cache behaviour:** recording links are plain file URLs re-checked when the card renders; profile pictures use the `?_=<last_picture_update>` key (Part 5).
- **Public links:** disabled (`EnablePublicLink=false`) — a file URL cannot be shared with someone outside the channel.
- **Retention:** **there is none.** Nothing deletes files automatically (Mattermost's retention job is an Enterprise feature; this is Team Edition). The only automatic cleanup in the system is pruning `honco_notifications` rows older than 90 days.

---

# 15. Notifications

```
Task assigned        →  notify.go claims a row in honco_notifications  →  DM from the honco bot  →  sidebar badge
Meeting scheduled    →  meetsvc posts the reminder in the channel at the chosen time
Meeting started/ended→  the honco bot replies in the meeting card's thread
Recording ready      →  the honco bot posts "Recording ready for <topic> (size · N min)" in the channel
Support request      →  a post in the support channel; each transition DMs the other party
AI summary ready     →  DM "AI Meeting Summary Ready" with a permalink
```

**How it reaches you.** The plugin first *claims* the event in **`honco_notifications`** using a unique key (kind + subject + recipient + a timestamp). If the key already exists nothing is sent — that is why a retried event never produces a second message. The message is then created as a direct message from the **honco** bot, and Mattermost delivers it like any DM.

| Channel | Works? |
|---|---|
| **In-app** (sidebar badge, DM, channel post, thread reply) | **Yes — verified** |
| **E-mail** | Mattermost's own behaviour for a DM: it sends only if you are away/offline and have e-mail notifications enabled. SMTP is configured (`smtp.gmail.com:587`, STARTTLS) and the admin card reports `smtp_configured: true`, **but no e-mail delivery was exercised in this pass — unverified.** |
| **Push (phones)** | **Not available** — no push server is configured (`push_server_configured: false`). |

Kinds in use: `task_assigned`, `task_reassigned`, `task_status`, `task_completed`, `task_due_soon`, `task_overdue`, `meeting_started/joined/left/ended`, `recording_ready`, `recording_failed`, `meeting_summary_ready`, `meeting_summary_failed`, `support_requested/accepted/rejected/started/ended/cancelled`, `ai_summary_ready`, `ai_processing_failed`.

---

# 16. Search

| Mattermost message search (top box) | Honco global search (Search tab) |
|---|---|
| Searches **messages and files**. | Searches **tasks, meetings, recordings, summaries, support requests**. |
| Native, untouched. | `GET /search` in the plugin. |

```
Search  →  GET /search?q=…&type=all|tasks|meetings|recordings|summaries|support&page=N
 ↓
Honco Search API       search_api.go → handleSearch
 ↓
Authorization          searchScope(you) = your team ids and channel ids; every query is limited to them
 ↓
Search categories      one ILIKE query per category (search.go), wildcards escaped, 20 per page (max 50)
 ↓
Results                grouped with per-category totals → clicking a hit opens the tab or the post
```

- **Tasks** — title and description; **Meetings** — topic and room name; **Recordings** — file name and room; **Summaries** — summary text; **Support** — the issue text.
- Nothing you may not see can appear: another team's tasks and private channels you are not in are excluded by the scope, not by filtering afterwards.
- Injection-safe: terms are parameterised and `%`/`_` are escaped.
- Limits: substring matching only, ranking favours recent rows, message text is not included.

---

# 17. Admin dashboard

**Who:** system administrators (`manage_system`) — the tab is hidden for everyone else **and** every call is re-checked on the server (verified: a normal user gets **403**).
**Why:** Mattermost's System Console knows nothing about Honco tables, Jitsi, Jibri, meetsvc or the AI service. This dashboard is the one place where the Honco parts report in. It is **read-only** and never shows a secret value — only whether something is configured.

| Section | What it means | Where the data comes from |
|---|---|---|
| **System health** | six live probes run when you open the tab: Honco Chat, PostgreSQL (with latency), Honco Plugin, Jitsi (`MeetPublicURL`, 4 s timeout), Jibri (`127.0.0.1:2222`), Meeting Service (`127.0.0.1:8077`) | `admin_api.go` → `handleAdminHealth` |
| **Usage** | Users, Teams, Channels (Mattermost) · Active meetings, Meetings, Recordings, Tasks, Summaries, Support open/total (Honco) | `admin_store.go` counts |
| **Honco Workspace** | plugin version, Honco migration number, Mattermost migration number, bot configured | plugin + `db_migrations` |
| **Recent failures** | latest failed recordings, failed summaries, declined support requests | the three tables |
| **Notifications** | bot configured; how many of each kind have been sent | `honco_notifications` grouped by kind |
| **Files** | stored files and bytes, attached to a post, recordings and bytes, largest file, possible orphans, post-deleted-file-kept, recordings missing their file, limits, public-links flag | `files_admin.go` over `fileinfo` + `honco_recordings` |
| **AI** | callback secret configured, service configured, service reachable, sessions live/completed/stored | plugin config + the key-value store |
| **Security** | yes/no flags only: self-signup, MFA, public links, plugin uploads, e-mail/push, SMTP, Jibri/meet/summarizer/support configured, SiteURL | Mattermost configuration |
| **Refresh** | re-runs both calls | — |

Reading on 15 Sep 2026: 10 users, 2 teams, 34 channels, 184+ meetings, 98 recordings, 144 tasks, 14 summaries, 87 support requests, 73 stored files (2.2 MB) — and **"Jitsi — not reachable from this host · ~4000 ms"**, because the probe uses the LAN address, which is not routable from inside WSL. The containers are up and meetings work from LAN browsers.
