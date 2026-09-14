# HONCO CHAT

## Complete Project Learning & Developer Guide

**"Understand the product, follow the data, find the code."**

| | |
|---|---|
| Guide version | 1.0 — 14 September 2026 |
| Code documented | Git commit `126acc9` on `main` (documentation commit `72e3153`) |
| Companion reference | `docs/HONCO_CHAT_COMPLETE_DOCUMENTATION.pdf` (the exhaustive technical reference — use it when you need every field and every route; use *this* guide to understand) |
| Who this is for | A developer or team member who did not build Honco Chat and needs to understand it well enough to change it safely |
| How it was verified | Every file name, route, table and status below was checked against the current source, the running instance and the test suites on 14 Sep 2026. Anything that could not be checked is marked **"Not verified in current environment."** |

> **How to read this guide.** Parts 1–5 give you the mental model. Part 6 is the heart: every important button, what it does, and which file to open. Parts 7–22 teach each feature area from zero. Parts 23–25 are the practical toolkit (run it, debug it, find the code). Parts 26–30 are the status, the boundaries, the FAQ, the user journeys and a cheat sheet you can print.

---

## Table of contents

1. Understand Honco Chat first
2. The big picture
3. Frontend vs backend vs database
4. The Honco Workspace plugin
5. UI map
6. Button-by-button explanation
7. Task management
8. Meeting system
9. Jitsi
10. Jibri recording
11. Meeting Intelligence
12. AI Assistant
13. Remote Support
14. Files
15. Notifications
16. Search
17. Admin dashboard
18. Profile & avatar
19. Database
20. API concept
21. WebSockets
22. Security
23. How to run the project
24. How to debug
25. Code navigation
26. Feature status
27. What is not our responsibility
28. Common questions
29. Complete user journeys
30. One-page project cheat sheet

---

# 1. Understand Honco Chat first

### What is Honco Chat?
Honco Chat is Honco's own team-chat application. People log in, join channels, send messages, start video meetings, record them, keep track of tasks, ask for help with their computer, and search all of it — inside one web application that runs on Honco's own servers, not on a vendor's cloud.

### Why does it exist?
Without it, a team's conversation lives in one tool, meetings in another, follow-up tasks in a third, recordings on someone's disk and "can you look at my screen?" in a phone call. Honco Chat keeps those things **next to each other**: the meeting is started from the channel where the discussion happened, the recording and the notes land on the same card, the follow-up becomes a task beside the conversation, and everything is searchable together.

### What is Mattermost?
**Mattermost** is an open-source team-chat server (think of Slack, but software you install and run yourself). It provides users, teams, channels, direct messages, threads, file attachments, search, notifications, an admin console, a REST API, a WebSocket and — importantly — a **plugin system** that lets you add features without editing its core.

### Why is Honco based on Mattermost?
Because chat is a solved problem. Writing a reliable chat server (accounts, permissions, message history, mobile apps, file handling) would take years. Mattermost gives all of that, under a licence that allows Honco to modify and self-host it. Honco's effort goes into what is *specific to Honco*: meetings, recording, tasks, support, AI integration and administration.

### What has Honco customized?
Two kinds of changes:

1. **The fork ("de-brand").** Honco keeps a modified copy of the Mattermost source. Scripts in `chat/branding/` remove the vendor's telemetry ("phone-home" calls), crash reporting, update checker, marketplace and hosted push endpoints, replace the logo and product name with "Honco Chat", and (most recently) relabel the profile picture section as "Profile Photo". These scripts are **idempotent** (running them twice changes nothing more) so they can be re-applied after every upstream update.
2. **The Honco Workspace plugin** (`plugins/com.honco.workspace`). All the Honco features live here, outside the Mattermost core: a right-hand panel with Tasks, Meetings, AI Assistant, Support, Search and Admin tabs; meeting and support "cards" in channels; the `/meet` integration; the recording callback; notifications; nine database tables of its own.

### Native Mattermost vs added by Honco

| Native Mattermost (unchanged) | Added or customized by Honco |
|---|---|
| Login, passwords, MFA, sessions | Honco Workspace panel (Tasks · Meetings · AI Assistant · Support · Search · Admin) |
| Teams, channels, DMs, group DMs, threads, reactions, mentions | `/meet` command service and meeting cards with a live lifecycle |
| Message and file search | Jibri recording delivery to the meeting card |
| File attachments and storage | Meeting Intelligence (notes from the channel conversation, via a Claude summarizer) |
| Profile settings and profile picture storage | "Profile Photo" wording and success/failure messages; avatars inside Honco panels |
| Desktop/e-mail notifications | Honco notifications from the `honco` bot (with "send once" protection) |
| System Console | Honco Administration dashboard |
| Plugin framework, REST API, WebSocket | De-brand scripts, start/stop/backup scripts |

> **Not part of Honco Chat:** "Honco Workspace V2" (a separate project and separate Docker containers on the same machine). It shares no code and no database with Honco Chat. When this guide says "Honco Workspace" it always means the *plugin inside Honco Chat*.

---

# 2. The big picture

```
        USER  (a person at a browser, desktop or phone)
          │
          ▼
        BROWSER  — the Mattermost web client + the Honco plugin's JavaScript
          │   HTTP requests (REST) and one WebSocket connection
          ▼
   ┌────────────────────────────────────────────────┐
   │ HONCO CHAT / MATTERMOST SERVER   (Go, port 8065)│
   │   ┌────────────────────────────────────────┐   │
   │   │ HONCO WORKSPACE PLUGIN  (Go + JS)      │   │
   │   │ tasks · meetings · recordings · AI ·   │   │
   │   │ support · search · admin · notifications│   │
   │   └────────────────────────────────────────┘   │
   └────────────────────────┬───────────────────────┘
                            │ SQL
                            ▼
                     POSTGRESQL  (database "honcochat", port 5433)
```

External services around it:

```
                       ┌── Jitsi ─────────► video meetings (the room people join)
                       ├── Jibri ─────────► recording (turns a meeting into an MP4)
   HONCO CHAT ─────────┼── AI service ────► AI Assistant (live transcript, suggestions, summary) [not connected]
                       ├── Claude ────────► Meeting Intelligence (notes from the conversation)  [unreachable today]
                       ├── RustDesk ──────► Remote Support (the actual screen control)          [separate host]
                       ├── SMTP ──────────► e-mail (notifications, password reset)
                       └── Cloudflare ────► a public address for the server                    [binary missing]
```

### Every box in simple language

| Box | What is it? | What does it do? | Why do we need it? |
|---|---|---|---|
| **Browser** | The user's web browser (Chrome, Edge, …) running the Honco Chat web client. | Draws the screens, reacts to clicks, talks to the server. | It is how people use the product. |
| **Honco Chat / Mattermost server** | One Go program (`~/honco-chat/build/honcochat`) listening on port 8065. | Logs users in, stores messages, serves files, hosts plugins, pushes live updates. | The core of the product. |
| **Honco Workspace plugin** | Honco's own code, loaded *inside* the server (a Go part) and *inside* the browser (a JavaScript part). | Everything Honco-specific. | Keeps Honco's features separate from Mattermost's code so updates don't collide. |
| **PostgreSQL** | A database server (port 5433). | Stores every message, user, channel, file record, and Honco's tasks, meetings, recordings, summaries, support requests and notification ledger. | Data must survive restarts and be queried quickly. |
| **meetsvc.py** (not in the diagram: a small helper) | A Python program on port 8077 (localhost only). | Answers the `/meet` command: creates a room address, schedules reminders, registers the meeting with the plugin. | Mattermost slash commands need a web service to talk to. |
| **Jitsi** | Open-source video-conferencing software running in Docker on this machine. | Provides the actual audio/video room. | Honco does not build video; it uses Jitsi. |
| **Jibri** | Jitsi's recorder (Docker). | Joins a room invisibly, records it to an MP4, then calls Honco Chat. | Recordings. |
| **AI service** | Another team's service (voice recognition, live transcript, suggestions, summary). | Sends events to Honco while a meeting runs. | Honco shows its output; Honco does not own the AI. **Not connected today.** |
| **Claude summarizer** | A script on another host ("mother") that runs the Claude command-line tool. | Turns the channel conversation of a meeting into notes. | Meeting Intelligence. **That host is unreachable today.** |
| **RustDesk** | Open-source remote-desktop software (relay on another host). | Lets a support agent see and control a colleague's screen. | Honco only manages the *request workflow*; the screen session happens in RustDesk. |
| **SMTP** | An e-mail sending server (Gmail, STARTTLS on port 587). | Mattermost sends notification and password-reset e-mails through it. | E-mail. |
| **Cloudflare tunnel** | A program (`cloudflared`) that publishes the server under a public hostname. | Makes Honco Chat reachable from the internet without opening firewall ports. | Public access. **The binary is not installed on this machine.** |

---

# 3. Frontend vs backend vs database

Every web application has three layers. Knowing which layer you are in tells you which language you are reading and which file to open.

### FRONTEND — what the user sees
- **What it is:** JavaScript that runs *in the browser*. Mattermost's web client is written in React (a JavaScript library for building screens). The Honco plugin's user interface is one hand-written JavaScript file that plugs into it.
- **What happens on a click:** a JavaScript function runs. It usually (1) collects what the user typed, (2) sends a request to the server, (3) waits for the answer, (4) redraws part of the screen.
- **Where the code lives:** Honco UI → `plugins/com.honco.workspace/webapp/dist/main.js` (about 3,900 lines, plain JavaScript, no build step). Mattermost UI → the fork at `~/honco-workspace/server/webapp/channels/src` (React/TypeScript; only touched through the branding scripts).

### BACKEND — the server that does the work
- **What it does:** receives requests, checks *who* is asking and *whether they may*, applies the rules, reads/writes the database, and answers.
- **REST API:** an **API** ("application programming interface") is the set of URLs the frontend can call. **REST** just means those URLs follow web conventions: `GET` to read, `POST` to create, `PATCH` to change, `DELETE` to remove, and the answer comes back as **JSON** (text formatted like `{"title": "Fix login"}`).
- **Where the code lives:** Honco backend → `plugins/com.honco.workspace/server/*.go` (Go language). Mattermost backend → the fork at `~/honco-workspace/server/server` (Go; not modified except by the de-brand scripts).

### DATABASE — where data is kept
- **What PostgreSQL does:** stores tables of rows, guarantees they are saved even if the server crashes, and answers queries like "all tasks in team X that are not done".
- **What is stored:** everything Mattermost knows (users, teams, channels, posts, file records, preferences, sessions — 85 tables) and everything Honco adds (9 tables: tasks, meetings, participants, recordings, summaries, notifications, support requests, support events, migration versions).

### One simple example — CREATE TASK

```
User clicks "New task", fills the form, clicks "Save task"
  ↓
FRONTEND   main.js → TaskForm: reads the fields, calls fetch('POST /plugins/com.honco.workspace/api/v1/tasks', {title, ...})
  ↓
API REQUEST  travels to the server as JSON, with the user's login cookie
  ↓
GO BACKEND   Mattermost checks the cookie → it is user "alice" → adds the header Mattermost-User-Id → hands the request to the plugin
  ↓
HONCO PLUGIN tasks_api.go → handleCreateTask: is alice a member of this team? is the assignee in the team? is the title valid?
  ↓
POSTGRESQL   store.go → INSERT INTO honco_tasks (...)
  ↓
RESPONSE     201 Created + the task as JSON; if someone else was assigned, a DM from the "honco" bot is sent (notify.go)
  ↓
UI           TaskForm closes, the new row appears at the top of the Tasks list
```

---

# 4. The Honco Workspace plugin

### What is it?
A **plugin** is a package of code that a host program loads to gain features. Mattermost plugins have two halves: a **server half** (Go, run by the Mattermost server as a separate process and talked to over an internal connection) and a **webapp half** (JavaScript, loaded into every user's browser). The Honco Workspace plugin (`com.honco.workspace`, version 0.1.0) has both.

### Why a plugin instead of editing Mattermost?
- Mattermost gets updates; a plugin survives them unchanged, while edits inside Mattermost's code would have to be redone (that is why even the branding is a *script*, not hand edits).
- The plugin has its own database tables, own URLs and own JavaScript, so a mistake in Honco code cannot corrupt Mattermost's data.
- It can be built, tested and redeployed on its own in a minute.

### What does it own?
Tasks · meeting registration, cards and lifecycle · recording callback and cards · Meeting Intelligence · AI Assistant UI and integration · Remote Support workflow · Honco global search · Admin dashboard · Honco notifications · the security layer around all of that (authentication check, rate limits, security headers).

### How does it talk to Mattermost?
- **Incoming:** every URL under `/plugins/com.honco.workspace/api/v1/…` is routed by Mattermost to the plugin's `ServeHTTP`. Mattermost has already identified the user and sets the `Mattermost-User-Id` header (a browser cannot forge it — the server strips any client-supplied copy).
- **Outgoing:** the plugin uses Mattermost's *plugin API* to create posts and direct messages (as the `honco` bot), read users/teams/channels/memberships, upload files, store small key-value data, and publish WebSocket events.

### Where it is
Source in the Git repository: `plugins/com.honco.workspace/`. Installed copy on the server: `~/honco-chat/run/plugins/com.honco.workspace/` (never edit that one; redeploy instead).

```
plugins/com.honco.workspace/
├── plugin.json                 manifest: id, version, and the SETTINGS SCHEMA (every configuration key, which are secret)
├── server/                     the Go half
│   ├── main.go                 program entry (starts the plugin)
│   ├── plugin.go               OnActivate (migrations, bot, background jobs), ServeHTTP (the front door)
│   ├── api.go                  THE ROUTE TABLE: every URL → handler; requireUser; membership helpers
│   ├── hardening.go            security headers, rate limiter, which routes are "service" or "AI push" routes
│   ├── store.go                database connection, MIGRATIONS (table definitions), task SQL
│   ├── task.go / tasks_api.go  task model + rules / task HTTP handlers
│   ├── notify.go               all task/summary notifications, the "send once" ledger, the due-date scanner
│   ├── recordings_api.go       /meetings/register (from meetsvc), /recordings/complete (from Jibri), recordings list
│   ├── recording.go            recording model, size limits, statuses ready/failed/unavailable
│   ├── meetings_api.go         read meetings (one, per channel, active)
│   ├── meeting_lifecycle.go    the 10-second poller: started / joined / left / ended, 90 s grace, 30 min fallback
│   ├── muc.go                  asks Jitsi (Prosody) who is in a room
│   ├── meeting_card.go         the meeting card's data (props), thread replies, WebSocket event
│   ├── summarizer.go / meeting_summary.go / summary_api.go   Meeting Intelligence
│   ├── ai.go / ai_adapter.go / ai_api.go                       AI Assistant sessions, service adapter, routes
│   ├── support.go / support_api.go / support_card.go           Remote Support
│   ├── search.go / search_api.go                               Honco global search
│   ├── admin_api.go / admin_store.go / files_admin.go          Admin dashboard
│   └── *_test.go               79 Go unit tests
└── webapp/dist/main.js         the JavaScript half: the panel, tabs, cards, avatars, WebSocket handlers
```
