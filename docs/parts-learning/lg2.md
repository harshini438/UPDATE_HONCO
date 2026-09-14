
---

# 5. UI map

Verified against the running application (14 Sep 2026).

```
HONCO CHAT
│
├── Login page                       (username/e-mail + password; "Forgot your password?")
│
├── Global header                    Honco wordmark · search box · @ mentions · saved messages · settings ⚙ · account menu (your avatar)
│     └── Account menu               status · custom status · Profile → (Profile Settings | Security) · Log out
│
├── Left sidebar                     team name · Find channel · Threads · CHANNELS · DIRECT MESSAGES · Add Channels / Invite Members
├── Channels                         message list · composer (attach, emoji, formatting) · channel header (members, info)
├── Direct Messages / Group DMs      same view; "Write a direct message" button opens the member picker
├── Threads                          replies open in the right-hand panel
├── Search (Mattermost)              Messages / Files, plus a Honco results button
├── Profile                          Profile Settings (Full Name · Username · Nickname · Position · Email · Profile Photo) · Security
│
├── App Bar (right edge)             [Honco Workspace icon]  [AI Assistant icon]
│
└── Honco Workspace panel (opens on the right)
    ├── Tasks                        All statuses ▾ · Mine ☐ · New task · task rows · Previous / Next
    ├── Meetings ("Meeting Intelligence")  Choose a meeting ▾ · Generate summary / Regenerate / Try again · notes
    ├── AI Assistant                 status badge · Start AI session / Stop session / Reconnect · live blocks · meeting ▾ · AI Meeting Summary
    ├── Support                      Request Support · my requests · (agents) queue with Accept Request / Decline / Start Session / End Session · Cancel
    ├── Search                       query · All / Tasks / Meetings / Recordings / Summaries / Support · results
    └── Admin (system admins only)   Refresh · System health · Usage · Files · Notifications · AI Assistant · Security · Recent failures

Cards posted in channels by the "honco" bot
    ├── Meeting card                 title · Started by · status · [Join Meeting] [AI Assistant] [View Summary] [View Recording]
    └── Support card                 "Remote Support Request" · requester · status · issue · agent
```

On a phone (≤ 768 px) the left "☰" opens the sidebar and the right "≡" opens a menu with Profile, Settings, View Members and the Honco Workspace entry.

<figure class="shot"><img src="images/annot-workspace.png" alt="Main workspace, annotated"><figcaption>Figure 1 — The main workspace. ① team name/menu ② Find channel ③ channel list ④ channel header ⑤ account menu (your avatar → Profile) ⑥ Honco Workspace icon ⑦ AI Assistant icon ⑧ a meeting card posted by the honco bot ⑨ message composer.</figcaption></figure>

---

# 6. Button-by-button explanation

Conventions used below. **Frontend** always means `plugins/com.honco.workspace/webapp/dist/main.js` unless it says "Mattermost". **API** paths are relative to `/plugins/com.honco.workspace/api/v1`. **Backend** files are in `plugins/com.honco.workspace/server/`. "PostgreSQL: yes" names the table.

<figure class="shot"><img src="images/annot-tasks.png" alt="Tasks tab, annotated"><figcaption>Figure 2 — The Tasks tab. ① tabs ② All statuses filter ③ Mine filter ④ New task ⑤ status selector of a row ⑥ Edit ⑦ Delete ⑧ due date / Overdue marker ⑨ Previous / Next paging.</figcaption></figure>

### BUTTON: Create Task (Tasks → **New task** → **Save task**)

Where: Honco Workspace panel → Tasks tab, top right.

What it does: creates a new task for the current team.

Flow:
```
Click "New task" → the form opens (Title*, Description, Assignee ▾, Status ▾, Due date)
Click "Save task"
 ↓ Frontend: TaskForm.onSubmit → fetch POST /tasks {team_id, title, description, assignee_id, status, due_at}
 ↓ Backend: tasks_api.go → handleCreateTask → is the caller in the team? is the assignee in the team? valid title/status?
 ↓ Database: store.go → INSERT honco_tasks
 ↓ notify.go → DM "task_assigned" to the assignee (only if it is someone else; only once)
 ↓ Response 201 with the task → the form closes and the row appears at the top
```

Code: Frontend `TaskForm`, `TasksTab` · Backend `tasks_api.go`, `task.go`, `store.go`, `notify.go` · Database `honco_tasks`, `honco_notifications`.

Fails when: title empty/too long (400 shown under the form), status not one of todo/in_progress/done, assignee not a team member, caller not a team member (403), too many requests (429).

### BUTTON: Edit Task (row → **Edit** → **Save task**)

Where: each task row. Who sees it: everyone; only the creator or the assignee may save (the server checks).

Flow: click Edit → the same form pre-filled → Save → `PATCH /tasks/{id}` → `handleUpdateTask` → `UPDATE honco_tasks` → row re-renders.

Code: `TaskForm` (edit mode) · `tasks_api.go` · `honco_tasks`.

Fails when: 403 "only the creator or assignee can change this task"; validation as above.

### BUTTON: Assign Task (part of Create/Edit: **Assignee ▾**)

Where: the Assignee dropdown in the form (lists team members from Mattermost's `GET /api/v4/users?in_team=…`).

What it does: sets or changes who is responsible. Choosing "Unassigned" clears it.

Flow: Save → `POST /tasks` or `PATCH /tasks/{id}` with `assignee_id` → server re-checks the assignee is in the team → `honco_tasks.assignee_id` → new assignee gets DM "task_assigned"; the previous one gets "task_reassigned".

Code: `TaskForm`, `fetchTeamMembers` · `tasks_api.go`, `notify.go` (`notifyAssigned`, `notifyUnassigned`).

Fails when: "assignee is not a member of this team" (400).

### BUTTON: Change Task Status (row → **status ▾**: To do / In progress / Done)

Flow: change → `PUT /tasks/{id}/status {status}` → `handleSetTaskStatus` → `UPDATE honco_tasks` → DM to the other party ("task_status"; "task_completed" to the creator when someone else finishes it) → the row shows the new status (Done rows are struck through).

Code: `TaskRow` · `tasks_api.go`, `notify.go` · `honco_tasks`.

Fails when: not creator/assignee (403).

### BUTTON: Delete Task (row → **Delete** → **Yes**)

Who: the creator only. Flow: confirm → `DELETE /tasks/{id}` → `handleDeleteTask` → sets `deleted_at` (a *soft* delete: the row stays in the table but is hidden everywhere) → the row disappears. Fails when: 403 "only the creator can delete this task".

### BUTTON: Filter Tasks (**All statuses ▾**, **Mine ☐**, **Previous / Next**)

Flow: any change → `GET /tasks?team_id=…&status=…&assignee_id=<me>&page=N&limit=20` → `handleListTasks` → `SELECT … FROM honco_tasks` scoped to the team → the list reloads. The panel shows 20 per page ("1–20"); the server allows up to 200.

Code: `TasksTab` · `tasks_api.go`, `task.go` (page sizes) · `honco_tasks`. Fails when: bad page/limit values (400).

### BUTTON (command): Create Meeting (type **`/meet`** in a channel)

Where: the message composer of any channel. Forms: `/meet`, `/meet Client review`, `/meet in 30m …`, `/meet at 15:00 …`, `/meet schedule`, `/meet list`, `/meet history`, `/meet cancel <id>`.

Flow:
```
/meet Client review
 ↓ Mattermost sends the command to meetsvc.py (127.0.0.1:8077)
 ↓ meetsvc builds the room address  <MeetPublicURL>/client-review-a1b2c3
 ↓ meetsvc → POST /meetings/register (header X-Honco-Service-Secret) → recordings_api.go handleRegisterMeeting
 ↓ Database: INSERT honco_meetings (status active, started_at 0) ; the plugin posts the MEETING CARD (custom_honco_meeting) as the honco bot
 ↓ WebSocket "meeting_updated" → every open client draws the card
 ↓ meetsvc answers you privately (ephemeral message) with the link in a copyable code block
```

Code: `chat/meetsvc.py` · `recordings_api.go` · `meeting_card.go` · Frontend `MeetingCard` · Database `honco_meetings`.

Fails when: meetsvc is down (Mattermost shows "command failed"); the shared secret does not match (registration 401 — no card); the bot token is invalid.

### BUTTON: Join Meeting (on the meeting card)

Where: the card, while the meeting is not ended. What it does: opens the Jitsi room in a new tab (`card.join_url`).

Behind it: nothing is sent to Honco by the click itself. Within 10 s the plugin's poller (`meeting_lifecycle.go` → `muc.go` asks Prosody) sees you, stamps `started_at` if you are the first, writes `honco_meeting_participants`, replies "started"/"joined" in the card's thread and sends "meeting_updated" so the card says "Meeting is active · N participants".

Fails when: `MeetPublicURL` is not reachable from *your* browser (today it is the host's LAN address `https://192.168.1.11:8443`); Jitsi containers down; a self-signed certificate warning on the LAN (accept it).

### BUTTON: AI Assistant (on the card, and the App Bar icon)

Where: every meeting card; the second App Bar icon. What it does: opens the Honco panel on the AI Assistant tab for that meeting (`window.HoncoOpenAIAssistant(meeting_id)`; the icon looks up the channel's latest meeting with `GET /channels/{id}/meetings`).

Then **Start AI session** → `POST /meetings/{id}/ai/session` → `ai_api.go handleStartAISession` → `ai_adapter.go` calls the external service `POST {AIServiceURL}/v1/sessions` → session stored in the plugin key-value store `ai:session:<meeting_id>`. **Stop session** → `DELETE …/ai/session`. **Reconnect** → `GET /meetings/{id}/ai`.

PostgreSQL: no dedicated table (the key-value store lives in Mattermost's `pluginkeyvaluestore` table).

What you see today: `AIServiceURL` is empty, so Start shows the honest "not configured / unavailable" state. Nothing is simulated.

Fails when: not a member of the meeting's channel (404); service unreachable (`unavailable`); service error (`failed`).

### BUTTON: Meeting Summary (Meetings tab → **Generate summary**; card → **View Summary**)

Where: Honco panel → Meetings tab ("Meeting Intelligence"), after choosing a meeting; **View Summary** appears on a card once a summary exists.

Flow: Generate → `POST /meetings/{id}/summary` → `summary_api.go handleGenerateSummary` → `summarizer.go` collects the channel messages posted during the meeting → row in `honco_meeting_summaries` (pending) → calls the Claude summarizer over SSH → ready / empty / failed → the panel polls `GET /meetings/{id}/summary` and shows Summary, Key points, Decisions, Action items, Participants; DM with a link.

What you see today: **"The summarizer is not reachable from this server."** with **Try again** — the summarizer host cannot be reached from this machine. No real Claude summary exists in the database.

Known defect: **View Summary** on a card does nothing if the Honco panel is *closed*; open the panel first.

Code: `MeetingsTab`, `MeetingIntent` · `summary_api.go`, `summarizer.go`, `meeting_summary.go` · `honco_meeting_summaries`.

### BUTTON: View Recording (on the meeting card)

Where: the card, when `recording_status = ready`. What it does: a link to `/api/v4/files/<recording_file_id>` — Mattermost's ordinary file endpoint — and the browser plays the MP4 inline.

Behind it: the recording arrived earlier from Jibri (`POST /recordings/complete`, see Part 10) and was uploaded through Mattermost's file API, so it is a normal file: only channel members can open it, and the card checks the file still exists (otherwise "Recording unavailable").

Code: `MeetingCard` · `recordings_api.go`, `recording.go` · `honco_recordings` + Mattermost `fileinfo`.

Fails when: not a channel member (403/404); file deleted ("Recording unavailable"); the recording was refused as too large ("Recording failed").

<figure class="shot"><img src="images/annot-meeting-card.png" alt="Meeting card, annotated"><figcaption>Figure 3 — A meeting card after the meeting ended. ① topic ② Started by (avatar + name) ③ status line ④ AI Assistant. Join Meeting is shown while the meeting is active; View Summary / View Recording appear when a summary or recording exists.</figcaption></figure>

### BUTTON: Create Support Request (Support → **Request Support** → **Send**)

Where: Honco panel → Support tab. What it does: asks the support agents for help with your screen/device; you describe the issue (up to 1024 characters; the form warns never to include passwords).

Flow: Send → `POST /support/requests {team_id, channel_id, issue}` → `support_api.go handleCreateSupportRequest` → `INSERT honco_support_requests (status open)` + `honco_support_events (created)` → a **support card** is posted in the channel → the support channel is notified → WebSocket "support_updated".

Code: `SupportTab` · `support_api.go`, `support.go`, `support_card.go` · `honco_support_requests`, `honco_support_events`.

Fails when: empty issue (400); not a member of the channel (403).

### BUTTON: Accept Support Request (**Accept Request**, agents only)

Who: members of the configured support channel (`honco-support` in team `harshini-sharma`) — the server checks membership on every click; the UI only shows the button when `is_agent` is true.

Flow: `POST /support/requests/{id}/accept` → status `accepted`, `agent_id = you`, event logged → requester gets a DM → card and rows update.

Fails when: not an agent (403); someone else was faster (409 "already updated by someone else").

### BUTTON: Reject Support Request (**Decline**, agents)

Flow: `POST …/reject {reason?}` → status `rejected` + event → DM to the requester → the request stays visible as declined and appears under Admin → Recent failures ("support declined").

### BUTTON: Start Support (**Start Session**, the assigned agent)

Flow: `POST …/start` → status `active`, `started_at` → the card and the requester's row show the **RustDesk hint** ("Open the RustDesk client and share your ID with the agent"). The screen session itself happens in RustDesk, outside Honco.

### BUTTON: End Support (**End Session**, the assigned agent)

Flow: `POST …/end` → status `ended`, `ended_at` + event → DM to the requester (or to the agent if the requester ended it).

### BUTTON: Cancel Support (**Cancel**, the requester)

Flow: `POST …/cancel` → status `cancelled` + event → DM to the agent if one was assigned. Allowed while open, accepted or active.

### BUTTON: Search (Search tab → type → Enter; chips All / Tasks / Meetings / Recordings / Summaries / Support)

Flow: `GET /search?q=…&type=all&page=0` → `search_api.go handleSearch` → `searchScope` = the teams and channels *you* belong to → `search.go` runs one `ILIKE` query per category, limited to that scope → grouped results with counts → click a result to open the tab/post.

PostgreSQL: yes — `honco_tasks`, `honco_meetings`, `honco_recordings`, `honco_meeting_summaries`, `honco_support_requests`.

Fails when: empty query (the panel explains instead of searching); nothing found ("No Honco results for …").

### BUTTON: Upload File (📎 in the composer — Mattermost)

Flow: choose a file → Mattermost `POST /api/v4/files` (≤ 100 MB) → a `fileinfo` row + the file under `~/honco-chat/run/data/<date>/` → the message carries the file id → only channel members can download it; public links are disabled. Honco code is not involved (recordings use the same path, uploaded by the bot).

Fails when: over 100 MB; not a channel member; disk full.

### BUTTON: Profile Photo (account menu → Profile → Profile Settings → **Profile Photo** → **Edit** → **Change photo** → **Save photo**)

Flow: Change photo → file picker (JPG/PNG/BMP) → a **preview** appears → Save photo → Mattermost `POST /api/v4/users/{me}/image` → the server stores the image under your user id and updates `Users.LastPictureUpdate` → Mattermost broadcasts `user_updated` → every open client re-keys your avatar URL → your photo changes everywhere immediately; the section shows "Profile photo updated.". **Remove photo** → `DELETE …/image` → default avatar → "Profile photo removed.".

Code: Mattermost `user_settings_general.tsx` + `setting_picture.tsx` patched by `chat/branding/profile-photo.py`; Honco panels use `UserAvatar` in `main.js`. PostgreSQL: only `Users.LastPictureUpdate` — no new table.

Fails when: wrong type ("Only JPG, PNG or BMP…"), over 100 MB, a corrupt file (server refuses: "Unable to update profile photo. Please try again."), trying to change someone else's photo (403).

<figure class="shot"><img src="images/annot-profile.png" alt="Profile Photo section, annotated"><figcaption>Figure 4 — Profile Settings. ① Profile Settings tab ② the Profile Photo section ③ current photo ④ accepted types and the server's size limit ⑤ Change photo ⑥ Save photo (enabled once a file is chosen) ⑦ Cancel. "Remove photo" appears when a custom photo is set.</figcaption></figure>

### BUTTON: Admin Dashboard (Admin tab → **Refresh**; system admins only)

Flow: opening the tab calls `GET /admin/overview` and `GET /admin/health` → `admin_api.go` (checks the `manage_system` permission first) → counts from the Honco tables and Mattermost tables (`admin_store.go`, `files_admin.go`), configuration *flags* (never values), and six live probes → the cards render. Refresh re-runs both.

Fails when: not an admin (403 — and the tab is not offered at all).

<figure class="shot"><img src="images/annot-admin.png" alt="Admin tab, annotated"><figcaption>Figure 5 — The Admin tab. ① System health (live probes) ② Usage (counts) ③ Files ④ Refresh. Further down: Notifications, AI Assistant, Security, Recent failures.</figcaption></figure>

### NOTIFICATIONS (no button — they arrive)

What they are: direct messages from the `honco` bot (task assigned/reassigned/status/completed/due soon/overdue, summary ready/failed, support accepted/rejected/started/ended/cancelled, AI summary ready/failed) and channel posts (meeting started/joined/left/ended in the card's thread, recording ready/failed).

Behind it: every send is first *claimed* in `honco_notifications` with a unique key, so the same event never produces two messages. Desktop pop-ups and e-mail then follow the user's normal Mattermost preferences. Code: `notify.go` (+ the feature files that call it).

---

# 7. Task management

**A task** is a small piece of work with a title, an optional description, someone responsible (the assignee), a status (To do → In progress → Done) and an optional due date. Tasks belong to a **team** (the Mattermost team you are in), not to a channel.

| Question | Answer |
|---|---|
| How is it created? | Tasks tab → New task → Save task → `POST /tasks` → `honco_tasks`. |
| How does assignment work? | The Assignee dropdown lists members of *your team* (read from Mattermost). The server re-checks membership; you cannot assign to someone outside the team. Changing the assignee notifies both the new and the previous person. |
| How does status work? | Three values only: `todo`, `in_progress`, `done`. Anything else is refused with 400. Done rows are shown struck through; "task_completed" goes to the creator if someone else finished it. |
| How do due dates work? | Optional. Stored as a millisecond timestamp in `due_at`. The row shows "Due <date>". |
| How does overdue work? | Not a stored flag: it is *computed* — due date in the past and status not done. The row shows a red "Overdue · date". A background scanner (`runDueScanner`, every 10 minutes) DMs the assignee once when a task is due within 24 h ("task_due_soon") and once when it becomes overdue ("task_overdue"). |
| How do notifications work? | All through `notify.go` and the `honco_notifications` ledger (see Part 15). |
| How does authorization work? | Read/list: any member of the team. Edit/status: the creator or the assignee. Delete: the creator only. Everything is checked on the server (`tasks_api.go`); the UI only hides buttons that would fail. |
| Where is the data? | `honco_tasks` (PostgreSQL). Deleted tasks keep their row with `deleted_at` set. |

```
User ──► Create Task (form) ──► POST /tasks ──► tasks_api.go (rules) ──► store.go ──► honco_tasks
                                                       │
                                                       └──► notify.go ──► honco_notifications (claim) ──► DM from the honco bot
                                                                                                             │
UI: new row at the top  ◄──── 201 JSON ◄──────────────────────────────────────────────────────────────────────┘
```

**"If I want to change Tasks, look here."**
- Form, rows, filters, paging, avatars: `webapp/dist/main.js` → `TaskForm`, `TaskRow`, `TasksTab`
- Rules (who may, validation, page sizes): `server/task.go`
- HTTP handlers: `server/tasks_api.go`
- SQL: `server/store.go` (task functions) and migration 1 (the table)
- Notifications and the due scanner: `server/notify.go`
- Tests to run after a change: `go test ./server/...`, browser `tasks-ux.js`, API `p11-tasks.sh`

---

# 8. Meeting system

### What happens when I type `/meet`?

```
/meet Client review
 ↓
Mattermost slash command  →  POSTs the text to meetsvc.py (a small Python service, localhost:8077)
 ↓
Meeting service (meetsvc.py)  →  builds the room address  <MeetPublicURL>/client-review-<6 random hex>
 ↓
Meeting registration  →  POST /meetings/register with the shared secret  →  plugin creates the honco_meetings row (status "active", started_at 0)
 ↓
Jitsi room  →  exists the moment the first person opens the address (Jitsi creates rooms on demand)
 ↓
Meeting card  →  posted in the channel by the honco bot (custom post type custom_honco_meeting); sent to all clients by WebSocket
 ↓
Participants  →  every 10 s the plugin asks Jitsi's Prosody "who is in room X?" and records joins/leaves
 ↓
Meeting lifecycle  →  started → (joined/left…) → ended
```

### The four states, in words
- **started** — the *first* person was actually seen in the room. `started_at` is set at that moment (not when `/meet` ran). The bot replies "started" in the card's thread.
- **joined** — a new occupant appeared: a row in `honco_meeting_participants` (Jitsi display name + an occupant key), a "joined" thread reply, the card's participant count and names update.
- **left** — an occupant disappeared: `left_at` is set, `present = false`, a "left" reply.
- **ended** — the room has been empty for **90 seconds** (`emptyRoomGrace`): status `ended`, `ended_at` set, "ended" reply, the Join button disappears. Safety net: a meeting nobody ever joined ends after **30 minutes** (`neverJoinedTimeout`).

Both timings were verified with two real participants on 14 Sep 2026 (ended 108 s after the room emptied; never-joined meetings ended at 30.0–30.1 min). The 90-second path was broken until commit `e9b03ed`; do not "fix" it by moving `started_at` back to registration time.

### Participant tracking
Participants are **Jitsi identities** (whatever display name a person typed in the Jitsi prejoin screen), not Mattermost users. That is why the card shows initials for participants rather than profile photos, and why a person who joins twice may appear twice.

### WebSocket updates
Every change to a meeting (registered, started, joined/left, ended, recording arrived, summary ready) makes the plugin publish a `meeting_updated` event to the channel. The browser's `MeetingCard` listens and re-renders in place — no reload, no polling from the browser (see Part 21).

Code: `chat/meetsvc.py` (command, scheduling) · `server/recordings_api.go` (register) · `server/meeting_lifecycle.go` (the poller and the timings) · `server/muc.go` (asking Prosody) · `server/meeting_card.go` (card props, thread replies, WebSocket) · `main.js` `MeetingCard` · tables `honco_meetings`, `honco_meeting_participants`.

---

# 9. Jitsi

**What is Jitsi?** Open-source video-conferencing software (like Zoom/Meet, but self-hosted). It runs here as a set of Docker containers (project `honco-meet`, folder `~/honco-meet`).

**Why do we use it?** Honco does not write video software. Jitsi provides rooms, audio/video, screen sharing, and a recorder (Jibri) that we can run ourselves.

```
Honco Chat  ──(link on the meeting card)──►  Jitsi room  ──►  audio / video / screen sharing between participants
Honco Chat  ◄──(every 10 s: "who is in the room?")──  Prosody
```

| Part | Belongs to | What it does |
|---|---|---|
| Meeting card, `/meet`, participant tracking, lifecycle, recording delivery | **Honco** | Everything around the room. |
| **Jitsi web** (port 8443) | Jitsi | The web page people join; the prejoin screen; the toolbar (mute, share, Record). |
| **Prosody** | Jitsi | The chat/presence server (XMPP) underneath Jitsi. It knows who is in which room. Honco asks it through a small HTTP module (`mod_muc_size`) on `127.0.0.1:5280`. |
| **Jicofo** | Jitsi | The "conference focus": decides how participants are connected and assigns Jibri when someone presses Record. |
| **JVB** (Jitsi Videobridge) | Jitsi | Routes the actual audio/video streams (UDP 10000). |
| **Jibri** | Jitsi | The recorder (Part 10). |

What Honco configures: `MeetPublicURL` (the address participants open — today `https://192.168.1.11:8443`), `ProsodyHTTPURL`, `XMPPDomain` (`meet.jitsi`), plus Docker overrides in `meet/docker-compose.override.yml`, branding (`meet/brand-meet.sh`) and the Jibri hook (`meet/setup-jibri.sh`). Start/stop with `meet/meet.sh up|down|ps|logs`.

Current caveat: from *inside* this WSL machine the LAN address is not routable, so the Admin health card says "Jitsi: not reachable from this host" even though the containers are up and meetings work from browsers on the LAN.

---

# 10. Jibri recording

**What is Jibri?** A Jitsi component that joins a room as an invisible participant, captures the screen and audio with a headless browser + ffmpeg, and writes an MP4. **Why?** So a meeting can be watched later from the channel.

```
Meeting (people talking)
 ↓  a participant presses "Start recording" in the Jitsi toolbar (Honco never starts it automatically)
Jitsi (Jicofo assigns the one Jibri)
 ↓
Jibri records  (1280×720, 25 fps)  … until someone stops it or the room empties
 ↓
MP4 written to /storage/<recording-id>/  + metadata.json (contains the room name)
 ↓
finalize: Jibri runs /config/finalize.sh  (source: meet/jibri-finalize.sh)
 ↓
Honco callback: POST /recordings/complete  (multipart: room_name, status, duration, file; header X-Jibri-Callback-Secret read from a 0600 file)
 ↓
recordings_api.go: secret ok? room registered? size ≤ limit (100 MB today)?  →  upload through Mattermost's file API as the honco bot
 ↓
File storage: a normal fileinfo row + the file under ~/honco-chat/run/data/  ;  honco_recordings row (ready | failed)
 ↓
Recording card: the meeting card gains "View Recording"; the bot posts "Recording ready for <topic> (size · min)"
```

Step by step: **start** = a person's click in Jitsi; **stop** = the person's click or the room emptying; **finalize** = Jibri's hook script (skipped entirely if zero media was captured — then the card simply never gets a recording); **callback** = the authenticated upload to Honco; **file upload** = Mattermost's file API; **View Recording** = Mattermost's file endpoint, channel members only.

**If Jibri fails:** Jitsi shows "Recording failed to start" (or no Record option when no Jibri is registered) and nothing reaches Honco — the card stays without a recording. If the callback secret is wrong, Honco answers 401 and logs "recording callback rejected"; the file stays in Jibri's `/storage`. If the file is over the limit, Honco records it as **failed** and posts "Recording failed for …" — it is never silently dropped. After a Docker Desktop restart Jibri usually has to be recreated (see Part 23).

Code: `meet/jibri-finalize.sh`, `meet/setup-jibri.sh` · `server/recordings_api.go` (`handleRecordingComplete`), `server/recording.go` (limits, statuses) · `main.js` `MeetingCard` · tables `honco_recordings` + Mattermost `fileinfo`. Real end-to-end delivery was verified in the QA audit (a 271 KB MP4 from a real Jibri run, playable in the channel).
