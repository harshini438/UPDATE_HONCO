
---

# 3. Button-by-button manual

**How to read an entry.** FRONTEND names the function inside `plugins/com.honco.workspace/webapp/dist/main.js` (one file — the whole Honco UI). API paths are relative to `/plugins/com.honco.workspace/api/v1`. BACKEND files are in `plugins/com.honco.workspace/server/`. STATUS is the result of clicking the control in the running application on 15 Sep 2026 unless the entry says otherwise.

---

### BUTTON: Honco Workspace (App Bar icon)

**WHERE:** right edge of the window (App Bar); on a phone, the channel "≡" menu → Honco Workspace.  
**WHO CAN SEE IT:** every logged-in user.  
**PURPOSE:** opens and closes the Honco panel — the home of Tasks, Meetings, AI Assistant, Support, Search and Admin.  
**WHEN I CLICK IT:** the right-hand panel opens on the tab you used last; clicking again closes it.  
**FRONTEND:** `Plugin.initialize` → `registry.registerAppBarComponent(ICON_URL, …, HoncoPanel)`; the panel itself is `HoncoPanel`.  
**API:** none on open. Each tab fetches its own data when you switch to it.  
**BACKEND:** none.  
**DATABASE:** none.  
**EXTERNAL SERVICE:** none.  
**WEBSOCKET:** none for the click; the panel subscribes to `meeting_updated`, `support_updated` and `ai_event` while it is open.  
**RESULT:** the panel appears; the channel area narrows.  
**ERROR CASES:** none observed. If the plugin is disabled the icon disappears entirely.  
**CODE LOCATION:** `webapp/dist/main.js` (`Plugin.initialize`, `HoncoPanel`).  
**STATUS:** DONE — verified (panel opened and closed repeatedly during the pass).

---

### BUTTON: AI Assistant (App Bar icon)

**WHERE:** second App Bar icon.  
**WHO CAN SEE IT:** every logged-in user.  
**PURPOSE:** jump straight to the AI Assistant tab for the meeting in the channel you are reading.  
**WHEN I CLICK IT:** the panel opens on the **AI Assistant** tab; the newest meeting of the current channel is selected.  
**FRONTEND:** `registerAppBarComponent(AI_ICON_URL, …)` → `AIIntent.open()` → `window.HoncoOpenWorkspace()` + `initialTab='ai'`.  
**API:** `GET /channels/{channel_id}/meetings` (to find the meeting to select), then `GET /meetings/{id}/ai`.  
**BACKEND:** `meetings_api.go` (`handleListChannelMeetings`), `ai_api.go` (`handleGetAISession`).  
**DATABASE:** reads `honco_meetings`; AI session state lives in the plugin key-value store (`ai:session:<meeting_id>`), not in a Honco table.  
**EXTERNAL SERVICE:** none for this click.  
**WEBSOCKET:** subscribes to `ai_event`.  
**RESULT:** the AI panel with the meeting selector and the current session state.  
**ERROR CASES:** channel with no meetings → the tab explains there is nothing to show; not a channel member → 404 and an explanatory message.  
**CODE LOCATION:** `main.js` (`AIIntent`, `AIPanel`), `server/ai_api.go`.  
**STATUS:** DONE — verified (opened on the AI tab with the correct meeting selected).

---

### BUTTON: New task

**WHERE:** Honco panel → Tasks tab, top right.  
**WHO CAN SEE IT:** every member of the team.  
**PURPOSE:** opens the form for creating a task.  
**WHEN I CLICK IT:** an inline form appears with Title (required), Description, Assignee ▾, Status ▾ and Due date, plus **Save task** and **Cancel**.  
**FRONTEND:** `TasksPanel` → sets the editor state → `TaskEditor`.  
**API:** none (opening the form makes no request). The Assignee list is filled from Mattermost's `GET /api/v4/users?in_team=<team>&per_page=200&active=true`.  
**BACKEND:** none for the click.  
**DATABASE:** none.  
**EXTERNAL SERVICE:** none.  
**WEBSOCKET:** none.  
**RESULT:** the form opens above the list.  
**ERROR CASES:** if the member list cannot be read the Assignee dropdown falls back to "Unassigned" only; the form still works.  
**CODE LOCATION:** `main.js` (`TasksPanel`, `TaskEditor`, `fetchTeamMembers`).  
**STATUS:** DONE — verified (form opened; no API call, as expected).

---

### BUTTON: Save task (create)

**WHERE:** the task form.  
**WHO CAN SEE IT:** every team member.  
**PURPOSE:** creates the task.  
**WHEN I CLICK IT:** the button shows a saving state, the form closes, and the new task appears at the top of the list.  
**FRONTEND:** `TaskEditor` submit → `request('POST', '/tasks', payload)`.  
**API:** `POST /tasks` — body `{team_id, title, description, assignee_id, status, due_at}`.  
**BACKEND:** `tasks_api.go` → `handleCreateTask` (team membership; assignee must be in the same team; validation in `task.go`).  
**DATABASE:** `INSERT` into **`honco_tasks`**; if a different person is assigned, a row is claimed in **`honco_notifications`**.  
**EXTERNAL SERVICE:** none.  
**WEBSOCKET:** none (the list reloads from the response).  
**RESULT:** 201 and the row appears; the assignee receives a direct message from the **honco** bot.  
**ERROR CASES:** 400 "title is required" / "title is too long" / "status must be one of todo, in_progress, done" / "assignee is not a member of this team" — all shown inline; 403 not a team member; 429 rate limited (60 mutations/minute); 500 shows "Request failed".  
**CODE LOCATION:** `main.js` (`TaskEditor`), `server/tasks_api.go`, `server/task.go`, `server/store.go`, `server/notify.go`.  
**STATUS:** DONE — verified today: clicking it created `honco_tasks` row `wp17f1mx…` and `POST /tasks` was observed on the wire.

---

### BUTTON: Cancel (task form)

**WHERE:** the task form. **WHO:** everyone. **PURPOSE:** abandon the form.  
**WHEN I CLICK IT:** the form closes; nothing is sent.  
**FRONTEND:** `TaskEditor` (`onCancel`). **API/BACKEND/DATABASE/EXTERNAL/WEBSOCKET:** none.  
**RESULT:** the list returns unchanged. **ERROR CASES:** none.  
**CODE LOCATION:** `main.js` (`TaskEditor`). **STATUS:** DONE — verified.

---

### BUTTON: Edit (task row)

**WHERE:** on each task row. **WHO CAN SEE IT:** everyone; only the **creator or the assignee** may save (the server enforces it).  
**PURPOSE:** change title, description, assignee or due date.  
**WHEN I CLICK IT:** the same form opens, pre-filled; **Save task** submits the change.  
**FRONTEND:** `TaskRow` → `TaskEditor` in edit mode.  
**API:** `PATCH /tasks/{task_id}` — only the fields present are changed (`"assignee_id": ""` unassigns).  
**BACKEND:** `tasks_api.go` → `handleUpdateTask`.  
**DATABASE:** `UPDATE` **`honco_tasks`**; possibly a **`honco_notifications`** claim for the new/previous assignee.  
**RESULT:** the row re-renders with the new values.  
**ERROR CASES:** 403 "only the creator or assignee can change this task"; validation errors as for create.  
**CODE LOCATION:** `main.js` (`TaskRow`, `TaskEditor`), `server/tasks_api.go`.  
**STATUS:** DONE — verified today (title changed and persisted; read back from the API).

---

### BUTTON: Assignee ▾ (assign / reassign)

**WHERE:** inside the task form. **WHO:** creator or assignee (to save).  
**PURPOSE:** choose who is responsible; "Unassigned" clears it.  
**WHEN I CLICK IT:** a dropdown of the team's active, non-bot members, sorted by username.  
**FRONTEND:** `TaskEditor` (the `assignee` select), list from `fetchTeamMembers`.  
**API:** Mattermost `GET /api/v4/users?in_team=…` to fill the list; the change is saved by `POST /tasks` or `PATCH /tasks/{id}`.  
**BACKEND:** `tasks_api.go` re-checks team membership of the chosen assignee — the browser's list is convenience, not authorization.  
**DATABASE:** `honco_tasks.assignee_id`; notifications in `honco_notifications`.  
**RESULT:** the row shows the new assignee with their profile photo; the new assignee gets "task_assigned", the previous one "task_reassigned".  
**ERROR CASES:** 400 "assignee is not a member of this team" (for example if they left the team since the list loaded).  
**CODE LOCATION:** `main.js` (`TaskEditor`, `fetchTeamMembers`, `memberLabel`), `server/tasks_api.go`, `server/notify.go`.  
**STATUS:** DONE — verified as part of create/edit.

---

### BUTTON: Status ▾ (task row)

**WHERE:** on every task row. **WHO:** creator or assignee.  
**PURPOSE:** move the task between **To do → In progress → Done**.  
**WHEN I CLICK IT:** the selector changes immediately and the row updates (Done rows are struck through).  
**FRONTEND:** `TaskRow` (the status `<select>`).  
**API:** `PUT /tasks/{task_id}/status` — body `{status}`.  
**BACKEND:** `tasks_api.go` → `handleSetTaskStatus`.  
**DATABASE:** `UPDATE` **`honco_tasks`**; a claim in **`honco_notifications`**.  
**RESULT:** the new status; a DM to the other party ("task_status", or "task_completed" to the creator when someone else finishes it).  
**ERROR CASES:** 403 not creator/assignee; 400 invalid status (not reachable from the UI, which only offers the three values).  
**CODE LOCATION:** `main.js` (`TaskRow`), `server/tasks_api.go`, `server/notify.go`.  
**STATUS:** DONE — verified today: selecting "In progress" changed the stored status (read back as `in_progress`).

---

### BUTTON: Delete → Yes (task row)

**WHERE:** on each task row. **WHO CAN SEE IT:** everyone, but only the **creator** may complete it.  
**PURPOSE:** remove a task.  
**WHEN I CLICK IT:** "Delete" turns into a **Yes** confirmation; clicking Yes removes the row.  
**FRONTEND:** `TaskRow` (`confirming` state).  
**API:** `DELETE /tasks/{task_id}`.  
**BACKEND:** `tasks_api.go` → `handleDeleteTask`.  
**DATABASE:** **soft delete** — `honco_tasks.deleted_at` is set; the row stays in the table and disappears from every list and from search.  
**RESULT:** the row vanishes; a later `GET /tasks/{id}` answers 404.  
**ERROR CASES:** 403 "only the creator can delete this task".  
**CODE LOCATION:** `main.js` (`TaskRow`), `server/tasks_api.go`.  
**STATUS:** DONE — verified today: confirmation shown, row deleted, `GET` afterwards returned 404.

---

### BUTTON: All statuses ▾ · Mine ☐ · Previous · Next (task filters and paging)

**WHERE:** Tasks tab toolbar and the foot of the list. **WHO:** everyone.  
**PURPOSE:** narrow the list (by status, or to tasks assigned to you) and move between pages.  
**WHEN I CLICK IT:** the list reloads; the counter at the bottom right reads "1–20", "21–40"…  
**FRONTEND:** `TasksPanel` (filter state → query string).  
**API:** `GET /tasks?team_id=…&status=…&assignee_id=<me>&page=N&limit=20`.  
**BACKEND:** `tasks_api.go` → `handleListTasks`; page sizes in `task.go` (`DefaultTaskPageSize = 60`, `MaxTaskPageSize = 200`; the panel asks for 20).  
**DATABASE:** `SELECT` from **`honco_tasks`**, always restricted to the current team.  
**RESULT:** the filtered page.  
**ERROR CASES:** 400 "page must be a non-negative number" / "limit must be a number" / "invalid status filter".  
**CODE LOCATION:** `main.js` (`TasksPanel`), `server/tasks_api.go`, `server/task.go`.  
**STATUS:** DONE — verified today (the "Mine" filter produced `…&assignee_id=<alice>` on the wire).

---

### COMMAND: `/meet` (create a meeting)

**WHERE:** typed in any channel's composer. **WHO:** every channel member.  
**PURPOSE:** create a video meeting for this channel, now or scheduled.  
**FORMS:** `/meet` · `/meet <name>` · `/meet in 30m <name>` · `/meet at 15:00 <name>` · `/meet schedule` (a form) · `/meet list` · `/meet history` · `/meet cancel <id>`.  
**WHEN I TYPE IT:** you get a private (ephemeral) reply with the meeting link in a copyable code block, and a **meeting card** is posted in the channel.  
**FRONTEND:** Mattermost's slash-command handling (no Honco JavaScript involved); the card is drawn by `MeetingCard`.  
**API:** Mattermost `POST /api/v4/commands/execute` → the command's target is **meetsvc** (`127.0.0.1:8077`), which then calls `POST /meetings/register` with the header `X-Honco-Service-Secret`.  
**BACKEND:** `chat/meetsvc.py` (room name, scheduling, reminders) → `server/recordings_api.go` → `handleRegisterMeeting`; the card is built in `server/meeting_card.go`.  
**DATABASE:** `INSERT` into **`honco_meetings`** (`status='active'`, `started_at=0`; or `scheduled` with `scheduled_at`), plus the card post in Mattermost's `posts`.  
**EXTERNAL SERVICE:** the room lives on **Jitsi** (`MeetPublicURL`), created on demand when the first person opens it.  
**WEBSOCKET:** `meeting_updated` so every open client draws the card.  
**RESULT:** an ephemeral reply with the link + a meeting card in the channel.  
**ERROR CASES:** meetsvc down → Mattermost shows a command error; wrong service secret → registration is refused with 401 and no card appears; malformed time → meetsvc answers with usage help.  
**CODE LOCATION:** `chat/meetsvc.py`, `server/recordings_api.go`, `server/meeting_card.go`, `main.js` (`MeetingCard`).  
**STATUS:** DONE — verified today: three meetings created this way, each produced a row and a card.

---

### BUTTON: Join Meeting (meeting card)

**WHERE:** on a meeting card, while the meeting has not ended.  
**WHO CAN SEE IT:** every member of the channel.  
**PURPOSE:** open the Jitsi room.  
**WHEN I CLICK IT:** a new browser tab opens at the room address; Jitsi shows its prejoin screen.  
**FRONTEND:** `MeetingCard` — an ordinary link (`<a href={card.join_url} target="_blank">`), not a request.  
**API:** none at click time.  
**BACKEND:** none at click time. Within 10 seconds the participant poller notices you.  
**DATABASE:** indirectly — `honco_meetings.started_at` (first person seen) and rows in **`honco_meeting_participants`**.  
**EXTERNAL SERVICE:** **Jitsi** (web on `:8443`; the plugin reads occupancy from Prosody).  
**WEBSOCKET:** `meeting_updated` when the poller records the change.  
**RESULT:** you are in the meeting; the card changes to "Meeting is active · N participants" and the thread gains "started"/"joined" replies.  
**ERROR CASES:** the address must be reachable **from your browser** — `MeetPublicURL` is currently the host's LAN address `https://192.168.1.11:8443`, so a machine outside that LAN cannot join; a self-signed certificate warning must be accepted on the LAN; if Jitsi is down the tab fails to load. Honco cannot detect any of these.  
**CODE LOCATION:** `main.js` (`MeetingCard`), `server/meeting_lifecycle.go`, `server/muc.go`.  
**STATUS:** DONE — verified today: on a freshly created active meeting the link read `https://192.168.1.11:8443/summary-path-check-…-b4a60f` with `target=_blank`. (Whether that address resolves is a network property, not a button property.)

---

### BUTTON: AI Assistant (meeting card)

**WHERE:** on every meeting card. **WHO:** every channel member.  
**PURPOSE:** open the AI Assistant panel for *this* meeting.  
**WHEN I CLICK IT:** the Honco panel opens (even if it was closed) on the AI Assistant tab with this meeting selected.  
**FRONTEND:** `MeetingCard` → `window.HoncoOpenAIAssistant(meeting_id)` → `AIIntent.open()` → `window.HoncoOpenWorkspace()`.  
**API:** `GET /meetings/{id}/ai` (session state), `GET /channels/{id}/meetings` (the selector).  
**BACKEND:** `server/ai_api.go`, `server/meetings_api.go`.  
**DATABASE:** reads `honco_meetings`; session state from the plugin key-value store.  
**WEBSOCKET:** `ai_event` while the tab is open.  
**RESULT:** the AI panel for that meeting.  
**ERROR CASES:** not a channel member → 404 and an explanatory message.  
**CODE LOCATION:** `main.js` (`MeetingCard`, `AIIntent`, `AIPanel`), `server/ai_api.go`.  
**STATUS:** DONE — verified today **with the panel closed**: the panel opened on the AI tab with the right meeting.

---

### BUTTON: View Summary (meeting card)

**WHERE:** on a meeting card, **only when a usable summary exists** (`has_summary`).  
**WHO:** every channel member.  
**PURPOSE:** open the meeting's notes in the Meetings tab.  
**WHEN I CLICK IT:** *if the Honco panel is already open*, it switches to Meeting Intelligence with this meeting selected. **If the panel is closed, nothing happens.**  
**FRONTEND:** `MeetingCard` → `window.HoncoOpenMeetingIntelligence(meeting_id)` → `MeetingIntent.open()`.  
**API:** `GET /meetings/{id}/summary` (once the panel reacts).  
**BACKEND:** `server/summary_api.go`.  
**DATABASE:** reads **`honco_meeting_summaries`**.  
**RESULT (panel open):** the notes are shown. **RESULT (panel closed):** no visible change.  
**ERROR CASES:** the defect above; a summary that is `failed` or `empty` makes the button absent rather than broken.  
**CODE LOCATION:** `main.js` — compare `MeetingIntent.open` (lines ~1042) with `AIIntent.open`: the AI one calls `window.HoncoOpenWorkspace()`, the meeting one does not. The fix is one line in `MeetingIntent.open`.  
**STATUS:** **PARTIAL — known defect, reproduced today.** Works with the panel open; does nothing with the panel closed.

---

### BUTTON: View Recording (meeting card)

**WHERE:** on a meeting card when `recording_status = ready`. **WHO:** every channel member.  
**PURPOSE:** play the meeting recording.  
**WHEN I CLICK IT:** the MP4 opens in Mattermost's media viewer.  
**FRONTEND:** `MeetingCard` — a link to `/api/v4/files/<recording_file_id>`.  
**API:** Mattermost `GET /api/v4/files/{file_id}` (the card also calls `GET /api/v4/files/{id}/info` when rendering, to notice a deleted file).  
**BACKEND:** Mattermost's file service (the plugin is not involved at play time).  
**DATABASE:** `honco_recordings.file_id` → Mattermost `fileinfo`; the bytes are under `~/honco-chat/run/data/`.  
**EXTERNAL SERVICE:** the file was produced earlier by **Jibri**.  
**RESULT:** the recording plays.  
**ERROR CASES:** not a channel member → 403/404; file deleted → the card shows "Recording unavailable"; a recording that failed to arrive shows the red badge "Recording failed" with the reason.  
**CODE LOCATION:** `main.js` (`MeetingCard`), `server/recordings_api.go`, `server/recording.go`.  
**STATUS:** DONE — verified today: the link returned **HTTP 200, `video/mp4`**.

---

### BUTTON: Choose a meeting ▾ (Meetings tab)

**WHERE:** Honco panel → Meetings tab ("Meeting Intelligence"). **WHO:** channel members.  
**PURPOSE:** pick which meeting's notes to read or generate.  
**WHEN I CLICK IT:** a list of this channel's meetings, newest first, with date and time.  
**FRONTEND:** `MeetingPanel` (the meeting `<select>`).  
**API:** `GET /channels/{channel_id}/meetings` (returns the meetings and a `summary_status` map), then `GET /meetings/{id}/summary`.  
**BACKEND:** `server/summary_api.go`, `server/meetings_api.go`.  
**DATABASE:** **`honco_meetings`**, **`honco_meeting_summaries`**.  
**RESULT:** the chosen meeting's notes, or an invitation to generate them. The choice is remembered per channel in `sessionStorage`, so a refresh returns to it.  
**ERROR CASES:** not a channel member → 404; no meetings → an empty state.  
**CODE LOCATION:** `main.js` (`MeetingPanel`, `MeetingIntent`), `server/summary_api.go`.  
**STATUS:** DONE — verified today (26 meetings listed; selection changed the panel).

---

### BUTTON: Generate summary / Regenerate / Try again

**WHERE:** Meetings tab, after choosing a meeting. **WHO:** channel members.  
**PURPOSE:** turn the **channel conversation that happened during the meeting** into notes. (It does not listen to audio — that is not implemented anywhere in Honco Chat.)  
**WHEN I CLICK IT:** the panel shows a working state and polls until the result arrives.  
**FRONTEND:** `MeetingPanel` → `requestStatus('POST', '/meetings/{id}/summary', {force})`, then polling `GET`.  
**API:** `POST /meetings/{meeting_id}/summary` → 202 Accepted; `GET /meetings/{meeting_id}/summary` for the result.  
**BACKEND:** `server/summary_api.go` → `handleGenerateSummary`; window and transport in `server/summarizer.go`; model in `server/meeting_summary.go`.  
**DATABASE:** **`honco_meeting_summaries`** — one row per meeting, status `pending → ready | empty | failed`; a **`honco_notifications`** claim for the DM.  
**EXTERNAL SERVICE:** the **Claude summarizer** on the host "mother" (`SummarizerHost`, SSH with a key pinned to `summarize/honco-summarize.sh`), or a local executable if `SummarizerCommand` is set (it is empty here).  
**RESULT (three real outcomes, all observed):**  
- **empty** — no human messages in the window: "No messages were posted in this channel during the meeting…" The summarizer is **not** contacted.
- **failed** — the summarizer could not be reached: "The summarizer is not reachable from this server." with **Try again**.
- **ready** — Summary, Key points, Decisions, Action items, Participants, and **View Summary** appears on the card.
**ERROR CASES:** 404 not a channel member; 202 then `failed` if the summarizer times out (`SummarizerTimeoutSeconds`, clamped 10–900); a generation stuck in `pending` for 15 minutes is treated as stale and can be retried.  
**CODE LOCATION:** `main.js` (`MeetingPanel`), `server/summary_api.go`, `server/summarizer.go`, `server/meeting_summary.go`, `summarize/honco-summarize.sh`.  
**STATUS:** **Honco side DONE; external path BLOCKED.** Verified today: a meeting with no conversation returned **empty** in seconds without an external call; a meeting with four real messages returned **failed — "The summarizer is not reachable from this server."** (`message_count=4`). `192.168.2.150:31013` does not answer from this machine.

---

### BUTTON: Start AI session / Start new AI session

**WHERE:** AI Assistant tab — in the introduction block, or in the header once there is content.  
**WHO CAN SEE IT:** channel members — **but only when an AI service URL is configured and the meeting is live.**  
**PURPOSE:** ask the external AI service to attach to this meeting.  
**WHEN I CLICK IT:** the status moves to *Connecting*, then to *Live* as events arrive.  
**FRONTEND:** `AIPanel` → `startSession` → `requestStatus('POST', '/meetings/{id}/ai/session', {})`. The control renders only if `canStart` — which requires `cfg.service_configured` (from `GET /ai/status`) — and, in the introduction, `meetingLive`.  
**API:** `POST /meetings/{meeting_id}/ai/session`.  
**BACKEND:** `server/ai_api.go` → `handleStartAISession` → `server/ai_adapter.go` → `POST {AIServiceURL}/v1/sessions` with a bearer token held server-side.  
**DATABASE:** no table — the session is stored in the plugin key-value store as `ai:session:<meeting_id>`.  
**EXTERNAL SERVICE:** the **AI service owned by another team** (voice, transcript, suggestions, insights, topics, final summary).  
**WEBSOCKET:** `ai_event` for every subsequent update.  
**RESULT:** a live session; transcript lines, suggestions, insights and topics appear as they arrive.  
**ERROR CASES:** service unreachable → status `unavailable` with an explanation; service error → `failed`; not a channel member → 404.  
**CODE LOCATION:** `main.js` (`AIPanel`, `canStart`, `startSession`), `server/ai_api.go`, `server/ai_adapter.go`, `server/ai.go`; contract in `AI_INTEGRATION.md`.  
**STATUS:** **NOT TESTABLE in the current environment.** `AIServiceURL` is empty, so `service_configured` is false and **the button is not rendered at all** — verified twice today on freshly created, active meetings. The panel instead shows: *"Honco AI Assistant — Get real-time meeting assistance, sales suggestions and meeting insights. Honco AI joins automatically once it is connected to this meeting."* No AI service exists on this network to connect.

---

### BUTTON: Stop session

**WHERE:** AI Assistant tab header, while the status is *Live* or *Connecting*. **WHO:** channel members.  
**PURPOSE:** end the AI session for this meeting.  
**FRONTEND:** `AIPanel` → `stopSession` (renders only when `canStop`).  
**API:** `DELETE /meetings/{meeting_id}/ai/session`.  
**BACKEND:** `server/ai_api.go` → `handleEndAISession` → adapter `POST {AIServiceURL}/v1/sessions/{id}/end`.  
**DATABASE:** plugin key-value store only. **EXTERNAL SERVICE:** the AI service. **WEBSOCKET:** `ai_event` (status change).  
**RESULT:** status becomes *Ended*; final outputs may still arrive afterwards.  
**ERROR CASES:** service unreachable → the session is still marked ended locally.  
**CODE LOCATION:** `main.js` (`AIPanel`), `server/ai_api.go`.  
**STATUS:** **NOT TESTABLE here** — it only appears during a live session, which requires the external service.

---

### BUTTON: Reconnect (AI Assistant)

**WHERE:** AI Assistant tab header. **WHO:** channel members.  
**PURPOSE:** re-read the session from the server after a connection drop, so nothing missed is lost.  
**WHEN I CLICK IT:** the panel refetches and redraws.  
**FRONTEND:** `AIPanel` → `reconnect`.  
**API:** `GET /meetings/{meeting_id}/ai` (and `GET /channels/{id}/meetings` to refresh the selector).  
**BACKEND:** `server/ai_api.go`. **DATABASE:** plugin key-value store + `honco_meetings`.  
**RESULT:** the current state; no change if nothing moved.  
**ERROR CASES:** 404 if you lost access to the channel; network failure shows an error line.  
**CODE LOCATION:** `main.js` (`AIPanel`), `server/ai_api.go`.  
**STATUS:** DONE — verified today (a `GET` was observed and the panel redrew).

---

### BUTTONS: Transcript · Load earlier lines · Jump to latest · Show previous suggestions (N) · Retry

**WHERE:** inside the AI Assistant tab. **WHO:** channel members.  
**PURPOSE:** *Transcript* expands/collapses the stored transcript (with `aria-expanded`); *Load earlier lines* fetches older lines; *Jump to latest* scrolls to the newest line; *Show previous suggestions* expands the suggestion history; *Retry* re-attempts after a service error.  
**FRONTEND:** `TranscriptBlock`, `AILive`, `Section2`.  
**API:** `GET /meetings/{id}/ai/transcript?before=<seq>&limit=200` (Load earlier lines). The others are UI-only.  
**BACKEND:** `server/ai_api.go` → `handleGetAITranscript`. **DATABASE:** plugin key-value store.  
**RESULT:** more lines, a scrolled list, or an expanded section.  
**ERROR CASES:** if the service never sent older lines the button is not offered.  
**CODE LOCATION:** `main.js` (`TranscriptBlock`, `AILive`).  
**STATUS:** DONE for the UI-only controls (verified: the tab renders and the collapsible sections work). *Load earlier lines* and *Retry* are **NOT TESTABLE here** — they need a session with content.

---

### BUTTON: Request Support (opens the form)

**WHERE:** Honco panel → Support tab. **WHO:** everyone.  
**PURPOSE:** start a request for desk-side/remote help.  
**WHEN I CLICK IT:** a form appears with "What is going wrong?" and a warning never to include passwords; the same button becomes **Cancel** while the form is open.  
**FRONTEND:** `SupportPanel` (form state).  
**API:** none for opening. **DATABASE:** none. **RESULT:** the form.  
**CODE LOCATION:** `main.js` (`SupportPanel`). **STATUS:** DONE — verified today.

---

### BUTTON: Request Support (sends the request)

**WHERE:** the support form. **WHO:** everyone (channel members).  
**PURPOSE:** create the request and tell the support agents.  
**WHEN I CLICK IT:** the button shows "Sending…", the form closes, your request appears in the list and a **support card** is posted in the channel.  
**FRONTEND:** `SupportPanel` → `request('POST', '/support/requests', {team_id, channel_id, issue})`.  
**API:** `POST /support/requests`.  
**BACKEND:** `server/support_api.go` → `handleCreateSupportRequest`; card and notifications in `server/support_card.go`.  
**DATABASE:** `INSERT` into **`honco_support_requests`** (status `open`) and **`honco_support_events`** (action `created`); a **`honco_notifications`** claim.  
**EXTERNAL SERVICE:** none at this step (RustDesk is used later, by the people, outside Honco).  
**WEBSOCKET:** `support_updated`.  
**RESULT:** the request is queued; the configured support channel is notified.  
**ERROR CASES:** 400 issue required / too long (max 1024 characters); 403 not a member of the channel; 500 "could not create request".  
**CODE LOCATION:** `main.js` (`SupportPanel`), `server/support_api.go`, `server/support_card.go`.  
**STATUS:** DONE — verified today: the request was created with status `open` and read back through the API.

---

### BUTTONS: Accept Request · Decline (support queue)

**WHERE:** Support tab, in the queue. **WHO CAN SEE THEM:** **support agents only** — the members of the channel named in the plugin settings (`SupportChannelName` in `SupportTeamName`, currently `honco-support` in `harshini-sharma`). Being a system administrator does **not** make you an agent.  
**PURPOSE:** take the request, or refuse it with an optional reason.  
**FRONTEND:** `SupportPanel` → `request('POST', '/support/requests/{id}/accept' | '/reject')`.  
**API:** `POST /support/requests/{request_id}/accept` · `/reject`.  
**BACKEND:** `server/support_api.go` → `handleAcceptSupport` / `handleRejectSupport` (agent membership re-checked server-side on every call).  
**DATABASE:** `UPDATE` **`honco_support_requests`** (status `accepted`/`rejected`, `agent_id`, `accepted_at`) + `INSERT` **`honco_support_events`**; a `honco_notifications` claim.  
**WEBSOCKET:** `support_updated`. **RESULT:** the card and the rows update; the requester gets a DM.  
**ERROR CASES:** 403 "you may not perform this action on this request" (not an agent); **409 "this request was already updated by someone else"** when two agents click at once.  
**CODE LOCATION:** `main.js` (`SupportPanel`), `server/support_api.go`, `server/support_card.go`.  
**STATUS:** DONE — verified today: accept returned 200 and the status became `accepted`.

---

### BUTTONS: Start Session · End Session (support)

**WHERE:** Support tab, on a request you have accepted. **WHO:** the assigned agent.  
**PURPOSE:** mark the remote session as running, then finished.  
**WHEN I CLICK Start Session:** the status becomes *Session active* and the card shows the RustDesk hint — "Open the RustDesk client and share your ID with the agent."  
**API:** `POST /support/requests/{id}/start` · `/end`.  
**BACKEND:** `server/support_api.go` → `handleStartSupport` / `handleEndSupport`.  
**DATABASE:** **`honco_support_requests`** (`active`/`ended`, `started_at`/`ended_at`) + **`honco_support_events`**.  
**EXTERNAL SERVICE:** **RustDesk** — and only as a hint. Honco stores no RustDesk credential, calls no RustDesk API, and cannot tell whether a screen session actually happened.  
**RESULT:** status changes; the requester (or the agent, if the requester ended it) gets a DM.  
**ERROR CASES:** 403 if you are not the assigned agent; 409 on a concurrent change.  
**CODE LOCATION:** `main.js` (`SupportPanel`, `RustDeskHint`), `server/support_api.go`.  
**STATUS:** DONE for the workflow — verified today (`active` then `ended`, with `honco_support_events` recording created → accepted → started → ended). The RustDesk session itself is **NOT TESTABLE** from this machine.

---

### BUTTON: Cancel (my support request)

**WHERE:** Support tab, on your own open/accepted/active request. **WHO:** the requester.  
**API:** `POST /support/requests/{id}/cancel` → `handleCancelSupport`.  
**DATABASE:** `honco_support_requests` status `cancelled` + an event; the assigned agent gets a DM.  
**RESULT:** the request closes. **ERROR CASES:** 403 if it is not yours; 409 if it changed meanwhile.  
**CODE LOCATION:** `main.js` (`SupportPanel`), `server/support_api.go`. **STATUS:** DONE (same code path as the other transitions, all verified).

---

### BUTTON: Search + Enter, and the category chips

**WHERE:** Honco panel → Search tab. **WHO:** everyone (results are limited to what you may see).  
**PURPOSE:** find Honco objects — tasks, meetings, recordings, summaries, support requests. (Message text is **not** searched here; use Mattermost's own box for that.)  
**WHEN I CLICK/TYPE:** results appear grouped by category with counts; the chips **All / Tasks / Meetings / Recordings / Summaries / Support** narrow it; **See all N …** jumps to one category; **Previous / Next** page through.  
**FRONTEND:** `SearchPanel`; the chips carry `data-search-filter="all|tasks|…"` (a deliberate hook, because "Tasks" and "Support" also name panel tabs).  
**API:** `GET /search?q=<term>&type=<all|tasks|…>&page=N&limit=20` (maximum 50).  
**BACKEND:** `server/search_api.go` → `handleSearch`; queries and scope in `server/search.go`.  
**DATABASE:** `SELECT` with `ILIKE` over **`honco_tasks`**, **`honco_meetings`**, **`honco_recordings`**, **`honco_meeting_summaries`**, **`honco_support_requests`** — every query restricted to your teams and channel memberships.  
**RESULT:** grouped hits; clicking one opens the relevant tab or post.  
**ERROR CASES:** empty query → the panel explains instead of searching; no matches → "No Honco results for …"; 400 for a bad page value.  
**CODE LOCATION:** `main.js` (`SearchPanel`), `server/search_api.go`, `server/search.go`.  
**STATUS:** DONE — verified today: Enter produced `GET …/search?q=meeting&type=all&page=0`; the Tasks chip produced `type=tasks` and marked itself `aria-selected=true`.

---

### BUTTON: Admin tab · Refresh

**WHERE:** Honco panel, last tab. **WHO CAN SEE IT:** **system administrators only** (permission `manage_system`).  
**PURPOSE:** a read-only dashboard for the Honco parts: health, usage, files, notifications, AI, security flags, recent failures.  
**WHEN I CLICK IT:** both calls run; **Refresh** runs them again.  
**FRONTEND:** `AdminPanel`.  
**API:** `GET /admin/overview` and `GET /admin/health`.  
**BACKEND:** `server/admin_api.go` (permission checked on every call), counts in `server/admin_store.go`, file figures in `server/files_admin.go`.  
**DATABASE:** counts from the `honco_*` tables and Mattermost's tables; `fileinfo` for the file card.  
**EXTERNAL SERVICE:** the health card probes **Jitsi** (`MeetPublicURL`, 4-second timeout), **Jibri** (`127.0.0.1:2222`) and **meetsvc** (`127.0.0.1:8077`).  
**RESULT:** the cards render; unreachable components are shown in red with the probe time.  
**ERROR CASES:** a normal user does not see the tab **and** the API answers **403**; a slow Jitsi probe delays the health card by up to 4 seconds.  
**CODE LOCATION:** `main.js` (`AdminPanel`), `server/admin_api.go`, `server/admin_store.go`, `server/files_admin.go`.  
**STATUS:** DONE — verified today: both calls observed, Refresh re-ran them, the tab was **absent** for a normal user and `/admin/overview` returned **403** for her.

---

### BUTTONS: Change photo · Save photo · Remove photo · Cancel (Profile Photo)

**WHERE:** account menu → Profile → Profile Settings → **Profile Photo** → Edit. **WHO:** everyone, for their own photo only.  
**PURPOSE:** set, replace or remove your profile picture.  
**WHEN I CLICK THEM:** *Change photo* opens the file picker and shows a **preview**; *Save photo* uploads it and the section reports "Profile photo updated."; *Remove photo* (only when a photo is set) followed by Save restores the default and reports "Profile photo removed."; *Cancel* discards the choice.  
**FRONTEND:** Mattermost's own `user_settings_general.tsx` + `setting_picture.tsx`, with Honco's wording and messages applied by `chat/branding/profile-photo.py`.  
**API:** `POST /api/v4/users/{user_id}/image` (multipart) · `DELETE /api/v4/users/{user_id}/image` · `GET /api/v4/users/{user_id}/image?_=<last_picture_update>`.  
**BACKEND:** Mattermost `channels/api4/user.go` (`setProfileImage`, `setDefaultProfileImage`, `getProfileImage`) — the Honco plugin is not involved.  
**DATABASE:** **no new table.** The image goes to the file store under your user id; the only database change is `Users.LastPictureUpdate`.  
**WEBSOCKET:** Mattermost's `user_updated` — every open client re-keys your avatar URL, so the new photo appears everywhere at once.  
**RESULT:** your photo changes in messages, threads, DMs, member lists, pickers, mention search — and in the Honco panels and cards, which render the same URL.  
**ERROR CASES:** wrong type → "Only JPG, PNG or BMP images can be used as a profile photo."; too large → "…larger than the maximum allowed size." (limit `FileSettings.MaxFileSize`, 100 MB here); a corrupt file → the server refuses and the section shows "Unable to update profile photo. Please try again."; changing someone else's photo → **403**.  
**CODE LOCATION:** `chat/branding/profile-photo.py` (wording/behaviour, then rebuild the web client); Honco avatars: `main.js` (`UserAvatar`, `pictureUpdateOf`).  
**STATUS:** DONE — verified in the previous session with two real users (57/57 settings checks, 44/44 two-user checks) and unchanged since.

---

### BUTTON: attachment 📎 / Send (native, for completeness)

**WHERE:** the message composer. **WHO:** channel members.  
**PURPOSE:** attach a file; send the message.  
**API:** Mattermost `POST /api/v4/files` then `POST /api/v4/posts`.  
**BACKEND:** Mattermost's file and post services (no Honco code).  
**DATABASE:** Mattermost `fileinfo` and `posts`; bytes under `~/honco-chat/run/data/<date>/`.  
**RESULT:** the file appears in the message, downloadable by channel members only (public links are disabled).  
**ERROR CASES:** over 100 MB → refused; not a channel member → 403.  
**CODE LOCATION:** Mattermost fork (`~/honco-workspace/server`), not this repository.  
**STATUS:** DONE (native upstream behaviour; exercised regularly by the file test suite, 32/32).
