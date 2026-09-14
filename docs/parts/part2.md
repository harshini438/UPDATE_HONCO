
---

# 6. Task management

**Status: DONE.** Verified by `tasks-ux.js` (23/23), `p11-tasks.sh`, `p7-idor.sh`, `p7-paging.sh`, Go unit tests in `task_test.go`.

### 6.1 What a task is
A row in `honco_tasks`: `id, team_id, creator_id, assignee_id, title (≤256), description, status (todo | in_progress | done), due_at, created_at, updated_at, deleted_at`. Tasks are **team-scoped**: every request carries `team_id`, the caller must be a member of that team, and an assignee must be a member of the same team (checked on every create/update through the Mattermost API, not trusted from the browser).

### 6.2 Operations
| Operation | Who may | How | Notification |
|---|---|---|---|
| Create | any team member | `POST /tasks` | assignee (if not the creator): `task_assigned` |
| Edit (title, description, due, assignee) | creator or assignee | `PATCH /tasks/{id}` — pointer fields, so "absent" and "set to empty" differ; `"assignee_id": ""` unassigns | new assignee: `task_assigned`; previous assignee: `task_reassigned` |
| Change status | creator or assignee | `PUT /tasks/{id}/status` | creator when someone else completes: `task_completed`; the other party on other changes: `task_status` |
| Delete | creator only | `DELETE /tasks/{id}` (soft: `deleted_at`) | none |
| List / filter | team member | `GET /tasks?team_id&status&assignee_id&creator_id&due_before&page&limit` | — |
| Due soon / overdue | background | every 10 min (`runDueScanner`): due within 24 h → `task_due_soon`; past due and not done → `task_overdue`; one DM per task per due date | assignee |

Filters in the UI: **All statuses** (todo / in_progress / done), **Mine**. The Overdue state is shown inline in red ("Overdue · date"). Paging: the panel requests 20 rows per page; the server default is 60, maximum 200 (`DefaultTaskPageSize`, `MaxTaskPageSize` in `task.go`).

### 6.3 User flow
```
Tasks tab ──► New task ──► form (Title*, Description, Assignee ▾, Status ▾, Due date) ──► Save task
                                                                                        │
      ┌─────────────────────────────────────────────────────────────────────────────────┘
      ▼
POST /tasks ──► team membership? ──► assignee in team? ──► INSERT honco_tasks ──► 201
      │                                                                          │
      └──── 403 / 400 shown inline in the form ◄───────────────────────────────  │
                                                                                 ▼
                                             row at top of list ◄── DM to assignee (task_assigned, once)
```

### 6.4 Error handling
Validation errors are returned as `{"error": "..."}` with 400 and shown inline under the form ("title is required", "title is too long", "status must be one of todo, in_progress, done", "assignee is not a member of this team"). Authorization failures are 403 with the reason text. The request body is capped at 64 KB (`MaxBytesReader`). Rate limit: 60 mutations/min (burst 30) per user.

### 6.5 Where the code is
| Layer | File |
|---|---|
| UI (form, rows, filters, paging, avatars) | `plugins/com.honco.workspace/webapp/dist/main.js` — `TaskForm`, `TaskRow`, `TasksTab`, `fetchTeamMembers`, `memberLabel` |
| HTTP handlers | `server/tasks_api.go` |
| Model + validation + page sizes | `server/task.go` |
| SQL | `server/store.go` (`CreateTask`, `ListTasks`, …) |
| Notifications | `server/notify.go` (`notifyAssigned`, `notifyUnassigned`, `notifyCompleted`, `notifyStatusChanged`, `scanDueTasks`) |
| Tests | `server/task_test.go`, `server/notify_test.go`, browser `tasks-ux.js`, API `p11-tasks.sh` |

---

# 7. Meetings

**Status: DONE**, with two external caveats (Jitsi public URL reachability from WSL; recording requires a participant to press Record). Verified by `card-e2e.sh` (17), `card-render.js` (8/8 with two real Jitsi participants), `lifecycle-e2e.js` (16/16), `qa-meet.sh`, `meeting_test.go`.

### 7.1 The pieces
| Piece | Role |
|---|---|
| `/meet` (`chat/meetsvc.py`, 127.0.0.1:8077) | Slash-command service. Builds the room URL `MEET_BASE/<slug>-<6 hex>`, handles `in`/`at`/`schedule`/`list`/`history`/`cancel`, keeps reminders in `run/meet-schedule.json` and history in `run/meet-history.json`, registers the room with the plugin. |
| `POST /meetings/register` (`recordings_api.go`) | Service route (`X-Honco-Service-Secret`). Inserts `honco_meetings` (`status active`, `started_at 0`, or `scheduled` with `scheduled_at`) and creates the meeting-card post. |
| Meeting card (`meeting_card.go` + `MeetingCard` in `main.js`) | Custom post type `custom_honco_meeting`; props are display-only (no room secrets, no occupant JIDs); re-rendered on every change through the `meeting_updated` WebSocket event. |
| Jitsi room | `MeetPublicURL/<room>` (config `meetpublicurl`, must equal meetsvc's `MEET_BASE`). |
| Participant poller (`meeting_lifecycle.go`, `muc.go`) | Every 10 s asks Prosody `mod_muc_size` (`ProsodyHTTPURL`, loopback) for the occupants of every non-ended room. |
| `honco_meeting_participants` | One row per occupant (Jitsi display name + occupant key), `joined_at / left_at / present`. These are Jitsi identities, **not** Mattermost users. |

### 7.2 Lifecycle (as implemented)
```
/meet ──► registered: status=active, started_at=0        (or scheduled, until scheduled_at passes)
   │
   │  poller sees ≥1 occupant
   ▼
started_at = now (first person actually seen)  ── thread reply "started"; card "Meeting is active · N participants"
   │  occupants change → participants rows; thread replies "joined" / "left" (batched per poll)
   │
   │  occupants = 0 for 90 s (emptyRoomGrace)
   ▼
status = ended, ended_at = now ── thread reply "ended"; card "Meeting ended"; Join button disappears
   │
   └── never joined: status active for 30 min (neverJoinedTimeout) ──► ended
```
Constants: `participantPollInterval = 10 s`, `emptyRoomGrace = 90 s`, `neverJoinedTimeout = 30 min`. The 90-second path was broken until commit `e9b03ed` (started_at was only stamped on a status transition that never happened for `/meet`-registered meetings); it is now verified end-to-end with two real participants: ended 108 s after the room emptied, and never-joined meetings ended at 30.0–30.1 min.

### 7.3 Card states and buttons
| Card state | Line shown | Buttons |
|---|---|---|
| scheduled | "Scheduled for …" | Join Meeting, AI Assistant |
| active | "Meeting is active · N participants" + names | Join Meeting, AI Assistant, (View Summary / View Recording when available) |
| ended | "Meeting ended" | AI Assistant, View Summary (if any), View Recording / Recording failed / Recording unavailable |

### 7.4 Full flow diagram
```
User ─/meet─► Mattermost ─POST─► meetsvc.py ─register─► plugin ─► honco_meetings + card post ─ws─► all clients
                                                             ▲                                        │
User ─Join Meeting─► Jitsi web :8443 ─► prosody/jicofo/jvb    │ poll 10 s (muc.go)                    │
                                       │                      │                                        │
                                       └─► occupants ─────────┘ ─► participants / started / ended ─ws─┘
                                       │
              participant presses Record ─► jibri ─► MP4 + finalize.sh ─POST /recordings/complete─► plugin
                                                                                                      │
                                                          fileinfo (Mattermost file API) ◄────────────┘
                                                          honco_recordings ready|failed
                                                          card: View Recording · channel post "Recording ready for …"
```

### 7.5 Jitsi configuration (Docker project `honco-meet`, `~/honco-meet`)
`docker-compose.yml` + `jibri.yml` + `docker-compose.override.yml` (Honco's overrides: `mod_muc_size` on Prosody, Jibri callback host, branding). `.env`: `XMPP_DOMAIN=meet.jitsi`, `PUBLIC_URL=https://localhost:${HTTPS_PORT}`, `ENABLE_RECORDING=1`, `JIBRI_RECORDING_DIR=/storage`, `JIBRI_FINALIZE_RECORDING_SCRIPT_PATH=/config/finalize.sh`, `JIBRI_RECORDING_RESOLUTION=1280x720`, `JIBRI_RECORDING_FRAMERATE=25`. Roles: **prosody** (XMPP, room presence — the plugin reads occupancy from it), **jicofo** (conference focus), **jvb** (media bridge, UDP 10000), **web** (the Jitsi UI on 8443), **jibri** (headless Chrome + ffmpeg recorder). `meet/meet.sh {up|down|ps|logs|restart|exec|config}` wraps `docker compose` with the right file set; `meet/setup-jibri.sh` installs the finalize hook and the callback secret file; `meet/brand-meet.sh` and `gen-backgrounds.py` do the Jitsi branding.

### 7.6 Where the code is
`chat/meetsvc.py` · `server/recordings_api.go` (register, complete, list) · `server/meetings_api.go` (get meeting, list by channel, active meetings) · `server/meeting_lifecycle.go` · `server/muc.go` · `server/meeting_card.go` · `main.js` `MeetingCard`, `MeetingsTab`, `MeetingIntent` · tests `meeting_test.go`, `lifecycle-e2e.js`, `card-render.js`, `card-e2e.sh`.

---

# 8. Jibri recording

**Status: DONE** (real end-to-end delivery verified in the audit: room `lan-recording-test-4ebd1b`, 271,429-byte MP4, finalize log "delivered to Honco Chat", row `ready`, playable in the channel).

| Step | What happens | Where |
|---|---|---|
| Start | A participant presses **Record** in the Jitsi toolbar. Jicofo assigns the (single) Jibri, which joins the room as a hidden participant and records with ffmpeg. Honco does **not** start recordings automatically. | Jitsi / Jibri |
| Stop | The participant stops recording or the room empties. Jibri writes `/storage/<recording-id>/<file>.mp4` and `metadata.json` (its `meeting_url` ends in the room name). | Jibri |
| Finalize hook | Jibri runs `/config/finalize.sh` (`meet/jibri-finalize.sh`, installed by `setup-jibri.sh`). It is **not** run when zero media was captured — that case appears as a meeting whose card never gains a recording. The hook reads the callback secret from `/config/honco-callback.secret` (0600, never in git), finds Honco Chat via `/config/honco-chat.host`, `host.docker.internal` or the docker gateway, and POSTs the file. Log: `/storage/finalize.log`. | `meet/jibri-finalize.sh` |
| Callback | `POST /recordings/complete`, multipart (`room_name`, `status`, `duration`, `file`), header `X-Jibri-Callback-Secret` compared in constant time with `JibriCallbackSecret`. Missing/bad secret → 401 and a warning in the server log. Rate-limited as a service route (60/min, burst 20). | `recordings_api.go: handleRecordingComplete` |
| Size limit | `MaxRecordingMB` (0 ⇒ Mattermost `MaxFileSize`, currently 100 MB; hard ceiling 2048). The request body is bounded; an oversized recording is recorded as **failed** with an error message and reported in the channel — never silently dropped. The file is held in memory (~2× its size) while stored. | `recording.go`, `recordings_api.go` |
| Storage | Uploaded through Mattermost's file API as the `honco` bot → a `fileinfo` row and a file under `~/honco-chat/run/data/<date>/…` (`FileSettings.DriverName=local`). `honco_recordings` keeps `file_id, file_name, size_bytes, duration_secs, status`. | Mattermost files |
| Card | `recording_status=ready` + `recording_file_id` → **View Recording** (`/api/v4/files/<id>`; the browser plays the MP4 in Mattermost's media viewer). `failed` → red badge with the reason. `unavailable` → the card checks the file still exists (`GET /api/v4/files/<id>/info`) and shows "Recording unavailable" if it was deleted. | `MeetingCard` |
| Notification | Channel post by the bot: "Recording ready for **topic** (size · N min)" or "Recording failed for **room**. <reason>", deduplicated per recording. | `recordings_api.go` |
| Listing | `GET /channels/{channel_id}/recordings` (channel members). | `recordings_api.go` |

**If Jibri is unavailable:** Jitsi shows "Recording failed to start" (or no Record button when no Jibri is registered); nothing reaches Honco; the meeting card simply never gains a recording. The Admin health card shows Jibri as unavailable (probe of `127.0.0.1:2222`). After a Docker Desktop restart Jibri must be recreated (`OPERATIONS.md` §8) — a known issue.

**Security:** the secret is never in the script; the plugin never trusts the file name for the room (it uses `room_name` and must find a registered meeting); files are readable only by channel members; public links are disabled.

**Testing:** `lan-record-e2e.js` (two real participants, real Record, real delivery), `rec-api-test.sh` (16), `recording-unavailable.js` (8), `recording_test.go`.

---

# 9. Meeting Intelligence / summary

**Status: BLOCKED (external).** The code path is complete and tested with a stub summarizer, but the real summarizer host is unreachable, so no genuine Claude summary can be produced today.

### 9.1 What it does — precisely
Meeting Intelligence turns the **channel conversation during a meeting** into structured notes. It does **not** transcribe audio: Honco Chat implements no speech-to-text (a separate batch transcription worker exists in `transcribe/` on another host and is not called by Honco Chat).

```
Meeting (honco_meetings)
  │  conversation window: created_at … latest recording end (or now); cap 6 h, 1000 messages, 200 000 chars
  ▼
Channel posts in that window, excluding bot posts and system messages     (summarizer.go: conversationFor)
  │
  ▼
Summary request  POST /meetings/{id}/summary  → honco_meeting_summaries status=pending
  │
  ▼
Claude summarizer:  ssh -i <SummarizerKeyPath> -p <SummarizerPort> <SummarizerUser>@<SummarizerHost>   (forced command → summarize/honco-summarize.sh → claude -p)
                    or, if SummarizerCommand is set, that local executable (stdin conversation → stdout markdown)
  │
  ▼
Parsed into summary · key_points · decisions · action_items · participants (raw_output kept)  → status ready | empty | failed
  │
  ▼
Meetings tab renders the sections; the meeting card gains View Summary; DM "Meeting summary ready/failed" with a permalink
```

Statuses: `pending`, `ready`, `failed`, `empty` (no messages in the window — the honest result for a meeting nobody typed during). A generation older than 15 min still `pending` is treated as stale and can be retried. Timeout `SummarizerTimeoutSeconds` (10–900).

### 9.2 Fields
Summary, Key points, Decisions, Action items, Participants — these are what `honco_meeting_summaries` stores and what the panel shows. **Key Insights, Client Insights and Important Topics are not Meeting Intelligence fields**; they belong to the AI Assistant (Section 10) and come from the external AI service.

### 9.3 Current state (verified 14 Sep 2026)
- Config: `SummarizerHost=192.168.2.150`, port 31013, `SummarizerCommand` empty → SSH path. `192.168.2.150:22` does not answer from this machine; the one real attempt in the database ("Release planning", 9 Sep) is `failed` and the panel shows **"The summarizer is not reachable from this server."** with **Try again**.
- The other `ready` rows in the database are `STUB-ECHO` outputs from test fixtures (`mi-seed-ready.sh` sets `SummarizerCommand` to a stub); they are not Claude output.
- The `empty` state and the whole UI (`meeting-intel.js`), the deep link (View Summary → panel), notifications and the API (`summary_test.go`) are verified.

Code: `server/summarizer.go` (window, transport, parsing), `server/meeting_summary.go` (model, store), `server/summary_api.go` (routes), `summarize/honco-summarize.sh` (runs on "mother"), `main.js` `MeetingsTab` / `SummaryView`.

---

# 10. AI Assistant

**Status: UI and integration infrastructure DONE; real AI E2E BLOCKED (external service not connected).**

### 10.1 Who owns what
| IMPLEMENTED IN HONCO | PROVIDED BY THE EXTERNAL AI SERVICE (other team) |
|---|---|
| Entry points (meeting card button, App Bar icon, panel tab), the whole panel UI (live / completed / idle / error states, transcript paging, suggestions history, dark theme, phone layout, accessibility) | Voice capture and speech recognition |
| Meeting association (`meeting_id` ↔ session) | Live transcript lines |
| `POST /ai/events` callback endpoint with shared-secret authentication, event aliases, bounds | Sales suggestions, insights (concern / sentiment / opportunity), topics |
| Adapter `AIIntegrationService` (`StartSession`, `EndSession`) with SSRF-checked base URL and bearer token held server-side | Post-call summary, action items, transcript reference |
| Session storage in the plugin KV store (`ai:session:<meeting_id>`) | Any AI model |
| WebSocket broadcast `ai_event` to the channel; reconnect handler | — |
| DM notifications "AI Meeting Summary Ready" / "AI Meeting Insights Unavailable" | — |
| Admin card (configured? reachable? sessions live/completed/stored) | — |

Honco Chat does **not** implement an AI engine, STT or transcription for this feature.

### 10.2 States and controls
`not_configured` → (Start) `connecting` → `live` (events arriving) → `ended` (meeting over; final outputs may still arrive) → `completed` (final received) · `unavailable` (service unreachable/refused) · `failed` (service reported an error). Header badge words: Live / Offline / Failed / Ended / Connecting. Controls: **Start AI session** (idle), **Stop session** (live), **Start new AI session** (after end), **Reconnect** (re-read state), **Show previous suggestions (N)**, **Load earlier lines**, **Jump to latest**, meeting selector, **Transcript** toggle.

### 10.3 Flows
```
Browser ──POST /meetings/{id}/ai/session──► plugin ──POST {AIServiceURL}/v1/sessions (Bearer AIServiceToken, callback URL, meeting info)──► AI service
Browser ◄──ai_event (WebSocket)──────────── plugin ◄──POST /ai/events  X-Honco-AI-Secret ─────────────────────────────────────────── AI service
                                                      events: status | transcript | suggestion | insight | topics | final | error (+ aliases)
Browser ──GET /meetings/{id}/ai──────────► plugin (session state, last N lines)      Browser ──GET /meetings/{id}/ai/transcript?before=seq──► older lines
Browser ──DELETE /meetings/{id}/ai/session► plugin ──POST /v1/sessions/{sid}/end──► AI service
```
Authentication: browser routes need a Mattermost session and channel membership for the meeting; the push route needs the callback secret (constant-time compare; unset secret ⇒ every push rejected, 401); the outbound call carries the bearer token, which never reaches a browser or a log (`redactErr`). Rate limit for pushes: 300/min, burst 60. Payloads are size-bounded and JSON-round-tripped to plain maps before broadcast (a struct in a WebSocket payload wedged the gob RPC once — guarded by `TestBroadcastPayloadIsGobSafe`).

### 10.4 Current state
`aiserviceurl` is empty; the callback secret is set. Admin card: `service_configured:false`, `service_reachable:false`, `sessions_stored:24`, `sessions_completed:13` (test sessions driven by the fixture pusher `ai-push.sh`). Pressing Start today produces the honest "not configured" state. No teammate service exists on the network (no ports, compose or config were found; `mother` is unreachable; Honco Workspace V2 is a different product with batch transcription and a Claude Q&A, not this real-time contract).

Code: `server/ai.go` (session model, events, aliases, KV, lifecycle hooks), `server/ai_adapter.go` (service interface, HTTP adapter, URL checks), `server/ai_api.go` (routes, DMs), `server/hardening.go` (`isAIPushRoute`, `limitAIPush`), `main.js` `AIPanel`, `AILive`, `AIFinished`, `SuggestionCard`, `TranscriptBlock`, `AIIntent`, `HoncoAIBus`; contract document `AI_INTEGRATION.md`; tests `ai_test.go` (event aliases, bounds, gob safety), `ai-api.sh` (94), `ai-e2e.js` (56), `ai-entry-e2e.js` (33), `ai-a11y.js` (31), `ai-notif-e2e.js` (10).

---

# 11. Remote Support

**Status: DONE (workflow).** RustDesk itself is an external dependency with **no integration** (by design). Verified by `support-e2e.js` (23/23, two users), `support-api.sh` (43), `support_test.go`.

### 11.1 Roles
- **Requester:** any user, from any channel they belong to.
- **Support agents:** the members of the channel `SupportChannelName` in team `SupportTeamName` (currently `honco-support` in `harshini-sharma`). Add/remove an agent by adding/removing them from that channel. Being a system admin does **not** make someone an agent. If either setting is blank, requests can still be raised but nobody can accept them (fails closed).

### 11.2 State machine
```
        Request Support                     Accept Request              Start Session            End Session
 ──────────────────► open ───────────────────► accepted ─────────────────► active ─────────────────► ended
                       │  Decline (agent)         │  Cancel (requester)      │  Cancel (requester)
                       └──────────► rejected      └──────────► cancelled     └──────────► cancelled
                       └──────────► cancelled (requester)
```
Every transition: optimistic update guarded against concurrent edits (409 "this request was already updated by someone else"), an `honco_support_events` row (`created / accepted / rejected / started / ended / cancelled`, actor, detail) — the audit trail — a re-rendered support card, a `support_updated` WebSocket event, and a DM to the other party (`support_accepted / rejected / started / ended` to the requester; `support_cancelled` to the agent; `support_ended` goes to the agent when the requester ended it). New requests notify the support channel (`notifySupportAgents`, deduplicated).

### 11.3 The RustDesk boundary
When a session is **active** the card and the requester's row show the hint: "Open the RustDesk client and share your ID with the agent." Honco stores no RustDesk credential, calls no RustDesk API and cannot see whether a RustDesk session actually happened. The RustDesk relay (hbbs/hbbr) runs on ubuntu-3 per `README.md`; it is not on this host and was not tested from here. If RustDesk is down, the Honco workflow still works but the agent cannot take the screen — an external dependency.

Tables: `honco_support_requests`, `honco_support_events`. APIs: `POST/GET /support/requests`, `GET /support/requests/{id}`, `POST …/{accept|reject|start|end|cancel}` (`is_agent` is returned by the list call so the UI can show agent controls; the server re-checks on every action). Code: `server/support.go`, `support_api.go`, `support_card.go`; `main.js` `SupportTab`, `SupportCard`, `RustDeskHint`.

---

# 12. Files & attachments

**Status: DONE (Mattermost) + Honco recording handling.** Verified by `files-e2e.js` (32/32), `p8-security.sh` (56), `retention.sh` (5), `files_test.go`.

| Topic | Fact (verified) |
|---|---|
| Normal attachments | Mattermost's own upload (`POST /api/v4/files`), stored under `FileSettings.Directory = ~/honco-chat/run/data/` (driver `local`), organised by date. |
| Recording files | Uploaded by the plugin as the `honco` bot through the same file API, so they are ordinary `fileinfo` rows attached to the recording post. |
| Permissions | A file is readable only by members of the channel of the post it is attached to; a non-member gets 403/404 (verified: alice refused on bob's private channel). |
| Private channel access | As above; Honco search never returns hits from channels the caller is not a member of. |
| Deletion | Deleting the post soft-deletes the file (Mattermost). The meeting card then shows "Recording unavailable" (it probes `/files/{id}/info`). The admin Files card counts `post_deleted_file_kept` and `possible_orphans`. |
| Cache invalidation | Recording links are `/api/v4/files/<id>`; the card re-checks existence when rendered. Profile pictures are keyed by `last_picture_update` (Section 16). |
| Maximum size | `FileSettings.MaxFileSize = 104857600` (100 MB) for uploads; recordings use `MaxRecordingMB` (0 ⇒ same 100 MB). Larger recordings are refused and reported as failed. |
| Public links | `FileSettings.EnablePublicLink = false` — no anonymous file URLs. |
| Playback | The browser plays MP4 recordings in Mattermost's media viewer from the file endpoint; verified with a real Jibri recording. |
| Security | Path traversal on the plugin routes → 301/404 (verified); oversized bodies → 400; security headers on every plugin response. |
| Admin diagnostics | Files card: stored files/bytes, attached to a post, recordings and bytes, largest file, possible orphans, post-deleted-file-kept, recordings missing their file, max sizes, public links flag (`files_admin.go`). |
| Retention | **There is no retention policy.** Nothing deletes files or recordings automatically; `notification` ledger rows older than 90 days are pruned daily (that is the only pruning). Mattermost's own data-retention job is an Enterprise feature and is not available on this Team Edition server. |

---

# 13. Notifications

All Honco notifications are **direct messages from the `honco` bot** (or channel posts for meeting/recording events), each claimed first in the `honco_notifications` ledger (unique `dedupe_key`), so a retried or repeated event never produces a second message. Desktop pop-ups and **e-mail** are then Mattermost's own behaviour for a DM: e-mail is sent by Mattermost only if `SendEmailNotifications` is on (it is), SMTP is configured (it is) and the recipient is away/offline with e-mail notifications enabled in their settings. No Honco code sends e-mail directly. Push notifications are **not** configured (`push_server_configured:false`).

| Event | Recipient | Type | Trigger | Dedupe key | UI behaviour | E-mail |
|---|---|---|---|---|---|---|
| Task assigned | assignee (≠ actor) | DM | create / edit with a new assignee | `task_assigned:<task>:<assignee>:<updated_at>` | DM with task title | Mattermost DM rules |
| Task reassigned | previous assignee | DM | edit changing assignee | `task_reassigned:<task>:<prev>:<updated_at>` | DM | same |
| Task status changed | the other party (creator or assignee) | DM | status change (not to done) | `task_status:<task>:<to>:<updated_at>` | DM | same |
| Task completed | creator (when someone else completes) | DM | status → done | `task_completed:<task>:<updated_at>` | DM | same |
| Task due soon | assignee | DM | scanner, due within 24 h, not done | `task_due_soon:<task>:<due_at>` | once per due date | same |
| Task overdue | assignee | DM | scanner, past due, not done | `task_overdue:<task>:<due_at>` | once per due date | same |
| Meeting scheduled / reminder | channel | channel post by meetsvc's bot token | `/meet in|at|schedule` | meetsvc state file | reminder post at the time | channel rules |
| Meeting started / joined / left / ended | channel (thread under the card) | bot reply | poller transitions | `meeting_<kind>:<meeting>[:names|:ended_at]` | thread replies; card re-renders | — |
| Recording ready | channel | bot post | Jibri callback stored | `recording_ready:<rec>` | "Recording ready for … (size · min)"; card gets View Recording | channel rules |
| Recording failed | channel | bot post | callback refused / oversize / store error | `recording_failed:<rec>` | "Recording failed for … <reason>"; red badge | channel rules |
| Meeting summary ready / failed | requester | DM with permalink | generation finished | `meeting_summary_<kind>:<meeting>:<updated_at>` | "View Summary" appears on the card | DM rules |
| Support requested | support channel | post | new request | `support_requested:<request>` | agents see the queue | channel rules |
| Support accepted / rejected / started / ended | requester (ended → agent if the requester ended it) | DM | transition | `support_<action>:<request>:<updated_at>` | card + row update | DM rules |
| Support cancelled | agent | DM | requester cancels | `support_cancelled:<request>:<updated_at>` | — | DM rules |
| AI summary ready | meeting creator | DM "**AI Meeting Summary Ready**" + permalink | `final` event from the AI service | `ai_summary_ready:<meeting>` | opens the AI tab | DM rules |
| AI failure | meeting creator | DM "**AI Meeting Insights Unavailable**" | `error` event | `ai_processing_failed:<meeting>` | — | DM rules |

Ledger figures today (admin card): task_assigned 206, task_reassigned 57, task_status 89, task_completed 89, due_soon 19, overdue 29, recording_ready 82, recording_failed 37, meeting_started 261, ended 162, summary ready 27 / failed 14, support requested 98 …, ai_summary_ready 17, ai_processing_failed 10. Code: `server/notify.go`, plus the call sites named above; tests `notify_test.go`, `notify_phase4_test.go`, `notif-e2e.sh`, `notif-phase4.sh`, `notifications.js`, `notif-phase4.js`.
