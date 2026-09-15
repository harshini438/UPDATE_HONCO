# HONCO CHAT

## Complete Button-by-Button User & Developer Manual

**"Understand every major action, what happens behind it, and where the code lives."**

| | |
|---|---|
| Manual version | 1.0 — 15 September 2026 |
| Code documented | Git commit `126acc9` (application code); documentation commits `72e3153`, `8e64c95`, `30503cd` |
| Application | Honco Chat — Mattermost 11.11.0 fork, Team Edition, unlicensed · plugin `com.honco.workspace` 0.1.0 |
| How buttons were verified | Every Honco control in Part 3 was **clicked in the running application on 15 Sep 2026** and its effect checked against the API and the database. 29 controls verified; results are recorded per button in the STATUS line. Native Mattermost controls were inventoried from the running UI but are not individually re-verified (they are upstream behaviour). |
| Companion documents | `docs/HONCO_CHAT_COMPLETE_DOCUMENTATION.pdf` (reference) · `docs/HONCO_CHAT_PROJECT_LEARNING_GUIDE.pdf` (learning guide). This manual supersedes both where they disagree — see the corrections box below. |

> **Corrections to the earlier documents, found by clicking the buttons this time**
> 1. **"Start AI session" is not rendered at all** in the current environment. Both earlier documents said pressing it shows a "not configured" state. In the code the control requires `cfg.service_configured` (`main.js`, `canStart`), and with `AIServiceURL` empty the panel instead shows the introduction "Honco AI joins automatically once it is connected to this meeting." Verified twice on freshly created, active meetings.
> 2. **"Generate summary" has two distinct outcomes**, not one. For a meeting with no human messages in its window it returns **empty** *without contacting the summarizer* (verified: 0 messages → "No messages were posted in this channel during the meeting"). Only a meeting with real conversation exercises the external path — verified today with four messages: status **failed**, "The summarizer is not reachable from this server."
> 3. **"View Summary" only appears when a summary exists and is usable** (`has_summary`). On a meeting whose summary is `failed` the button is correctly absent. The closed-panel defect is still present and was reproduced again today.

---

## Table of contents

1. What am I looking at?
2. Complete button map
3. Button-by-button manual
4. Main chat buttons
5. Profile & profile photo
6. Tasks
7. Meetings
8. Meeting lifecycle
9. Jitsi
10. Jibri / recording
11. Meeting Intelligence
12. AI Assistant
13. Remote Support
14. Files & attachments
15. Notifications
16. Search
17. Admin dashboard
18. Button → code map
19. Button → database map
20. Button → external service map
21. Complete data flow examples
22. What is Mattermost vs what is Honco?
23. Code navigation
24. Debugging a button
25. Common button problems
26. Feature status
27. Important limitations
28. Safe project operations
29. Quick reference
30. Beginner glossary

---

# 1. What am I looking at?

When you open Honco Chat you are looking at a **customised Mattermost**. Mattermost is an open-source team-chat server (like Slack, but installed on your own machines). Honco added one plugin — **Honco Workspace** — plus a few services around it. Everything below was read from the running application on 15 Sep 2026.

```
┌──────────────────────────────────────────────────────────────────────────────────────┐
│ ▤  H Honco Chat        [ Search ]        ?        @   ⌂   ⚙   (your avatar)          │ ← top navigation
├──────────┬────────────────────────────────────────────────────────┬──────────┬───────┤
│ Harshini │ ☆  Town Square ▾   👤6  ⧉  ⓘ                          │  Honco   │  ▣    │
│ Sharma ▾ ├────────────────────────────────────────────────────────┤ Workspace│  ✦    │ ← App Bar
│  + ⌕     │                                                        │  panel   │       │   (2 icons)
│          │   honco BOT  7:49 PM                                   │          │       │
│ Threads  │   ┌──────────────────────────────────────┐             │  Tasks   │       │
│          │   │ ▣ Client Review                      │             │ Meetings │       │
│ CHANNELS │   │   Started by  (avatar) alice         │  ← meeting  │ AI Asst. │       │
│  # Town  │   │   ● Meeting is active · 2 participants│     card    │ Support  │       │
│  # Sales │   │   [Join Meeting] [AI Assistant]      │             │ Search   │       │
│  # Dev   │   └──────────────────────────────────────┘             │ Admin*   │       │
│          │                                                        │          │       │
│ DIRECT   │   ┌──────────────────────────────────────┐             │ *admins  │       │
│ MESSAGES │   │ 🖥 Remote Support Request             │  ← support  │   only   │       │
│  @ honco │   │   (avatar) Requested by alice        │     card    │          │       │
│  @ bob   │   │   ● Waiting for support              │             │          │       │
│          │   └──────────────────────────────────────┘             │          │       │
│          ├────────────────────────────────────────────────────────┤          │       │
│          │  Write to Town Square                                  │          │       │
│          │  B I S H 🔗 <> " ≔ ≡   …   Aa 📎 ☺ ➤                   │ ← composer│       │
└──────────┴────────────────────────────────────────────────────────┴──────────┴───────┘
```

| Area | What it is |
|---|---|
| **Login screen** | Email or Username · Password · Show password · "Forgot your password?" · Log in. No sign-up link — accounts are created by an administrator (`EnableOpenServer=false`). |
| **Main workspace** | Everything after login: sidebar, conversation, panels. |
| **Left sidebar** | Your team name (with a menu), "+" to browse/create channels, Find Channels, **Threads**, the **CHANNELS** list, the **DIRECT MESSAGES** list, and per-channel "⋮" option menus. |
| **Team** | A group of channels and people. Honco Chat currently has 2 teams; you can belong to several and switch between them. |
| **Channels** | Named conversations — public (#) or private (🔒). |
| **Direct Messages** | One-to-one conversations; a **group DM** is the same thing with several people. |
| **Main conversation area** | The channel header (favourite ☆, channel name menu, Members, Channel files, View Info), the message list — including Honco's **meeting cards** and **support cards** — and the message composer. |
| **Right-side Honco Workspace panel** | Honco's own panel: **Tasks · Meetings · AI Assistant · Support · Search** and, for system administrators only, **Admin**. |
| **Top navigation** | Product switch menu · Search box · Help · Recent mentions (@) · Saved messages · Settings (⚙) · your account menu (your avatar). |
| **Profile** | Account menu → **Profile** → Profile Settings (Full Name, Username, Nickname, Position, Email, **Profile Photo**) and Security (password, MFA, sessions). |
| **Search** | Two different searches: Mattermost's box at the top (messages and files) and Honco's **Search tab** in the panel (tasks, meetings, recordings, summaries, support). |
| **Admin area** | Two separate things: Honco's **Admin tab** (read-only dashboard, in the panel) and Mattermost's **System Console** at `/admin_console` (full configuration). |

---

# 2. Complete button map

Every interactive control visible in the current application, grouped by location. "Honco" marks controls added by the Honco Workspace plugin; the rest are native Mattermost. Names are exactly as they appear on screen (or the accessible name where the control is an icon).

### A. Login
| Control | Type | Who | Action | Status |
|---|---|---|---|---|
| Email or Username / Password | input | everyone | credentials | Native · DONE |
| Show password (👁) | toggle | everyone | reveals the password | Native · DONE |
| Log in | button | everyone | starts a session | Native · DONE |
| Forgot your password? | link | everyone | password-reset e-mail | Native · DONE |

### B. Top navigation
| Control | Icon | Who | Action | Status |
|---|---|---|---|---|
| Product switch menu | ▤ | everyone | switches product/back to channels | Native |
| Search | box | everyone | Mattermost message/file search | Native |
| Help | ? | everyone | opens the configured help link | Native |
| Recent mentions | @ | everyone | mentions panel | Native |
| Saved messages | 🔖 | everyone | saved posts panel | Native |
| Settings | ⚙ | everyone | display/notification settings | Native |
| User's account menu | avatar | everyone | status, custom status, **Profile**, Log out | Native |

### C. Left sidebar
| Control | Who | Action | Status |
|---|---|---|---|
| Team name ▾ | everyone | team menu (invite, settings, leave) | Native |
| Browse or create channels (+) | everyone | join/create a channel | Native |
| Find Channels (⌕) | everyone | channel switcher | Native |
| Threads | everyone | followed threads | Native |
| Channels category options / Channel options for … (⋮) | everyone | mute, favourite, leave, move | Native |
| Write a direct message (+) | everyone | opens the member picker | Native |

### D. Channel header
| Control | Who | Action | Status |
|---|---|---|---|
| add to favorites (☆) | everyone | favourite this channel | Native |
| \<channel\> channel menu ▾ | everyone | View Info, Mute, Notification Preferences, Channel Settings, Members, Move to | Native |
| Members (👤n) | everyone | member list in the right panel | Native |
| Channel files | everyone | files posted here | Native |
| Open in new window | everyone | pops the channel out | Native |
| View Info (ⓘ) | everyone | channel info panel | Native |

### E. Message composer
| Control | Who | Action | Status |
|---|---|---|---|
| bold · italic · strike through · heading · link · code · quote · bulleted list · numbered list | everyone | Markdown formatting | Native |
| Message priority | everyone | marks a message urgent/important | Native |
| formatting (Aa) | everyone | shows/hides the formatting bar | Native |
| attachment (📎) | everyone | **upload a file** | Native |
| select an emoji (☺) | everyone | emoji picker | Native |
| Send Now (➤) / Enter | everyone | **send the message** | Native |
| `/meet …` typed in the composer | everyone | **creates a meeting** | **Honco** · DONE |

### F. Message actions (hover a message)
| Control | Who | Action | Status |
|---|---|---|---|
| Add Reaction | everyone | emoji reaction | Native |
| save message | everyone | adds to Saved messages | Native |
| reply | everyone | opens the thread in the right panel | Native |
| more (⋯) | everyone | Mattermost's post menu (edit, copy link, delete, …) | Native |

### G. Profile menu (account menu)
| Control | Who | Action | Status |
|---|---|---|---|
| Online / Away / Do not disturb / Offline | everyone | presence | Native |
| Set custom status | everyone | status text/emoji | Native |
| Profile | everyone | opens Profile Settings | Native |
| Log out | everyone | ends the session | Native |

### H. Profile settings
| Control | Who | Action | Status |
|---|---|---|---|
| Profile Settings / Security tabs | everyone | switches section | Native |
| Edit (Full Name, Username, Nickname, Position, Email) | everyone | edit that field | Native |
| **Profile Photo → Edit** | everyone | expands the photo section | Native + **Honco wording** · DONE |
| **Change photo** | everyone | file picker + preview | Native + Honco wording · DONE |
| **Save photo** | everyone | uploads the photo | Native + Honco wording · DONE |
| **Remove photo** | everyone (only when a photo is set) | restores the default avatar | Native + Honco wording · DONE |
| Cancel | everyone | discards the choice | Native · DONE |

### I. Tasks (Honco)
New task · Save task · Cancel · Edit · Delete → Yes · status ▾ (To do / In progress / Done) · Assignee ▾ · Due date · All statuses ▾ · Mine ☐ · Previous · Next — **all DONE and verified today.**

### J. Meetings / Meeting Intelligence (Honco)
Choose a meeting ▾ · Generate summary · Regenerate · Try again — **DONE inside Honco; the external summarizer is BLOCKED.**

### K. AI Assistant (Honco)
Meeting ▾ · Reconnect · Transcript (section toggle) · Jump to latest · Load earlier lines · Show previous suggestions (N) · Retry — **DONE.**
Start AI session · Start new AI session · Stop session · Retry connection — **NOT TESTABLE here: these render only when an AI service URL is configured.**

### L. Remote Support (Honco)
Request Support (opens the form) · Request Support (sends) · Cancel · Accept Request · Decline · Start Session · End Session · Refresh — **all DONE and verified today.**

### M. Search (Honco)
Search field + Enter · chips **All / Tasks / Meetings / Recordings / Summaries / Support** · See all N … · Previous · Next · result rows — **DONE and verified today.**

### N. Admin (Honco, system admins only)
Admin tab · Refresh · read-only cards (System health, Usage, Files, Notifications, AI Assistant, Security, Recent failures, versions) — **DONE and verified today.**

### O. Files / attachments
attachment (📎) · file preview/download · **View Recording** (Honco meeting card) · Channel files — Native, plus Honco's recording link · **DONE.**

### P. Notifications
No buttons: Honco notifications arrive as direct messages from the **honco** bot and as channel posts. Mattermost's own notification settings live in ⚙ Settings → Notifications.

### Q. Meeting cards (Honco)
Join Meeting · AI Assistant · View Summary · View Recording · badges "Recording failed" / "Recording unavailable" — **DONE** (View Summary has a known defect, see Part 3).

### R. Support cards (Honco)
Read-only: title, requester avatar + name, status dot, issue text, agent. All actions are on the Support tab.

### S. Other Honco controls
App Bar icon **Honco Workspace** (opens the panel) · App Bar icon **AI Assistant** (opens the panel on the AI tab) · channel-header button "Honco Workspace" (when the App Bar is hidden / phone) · the Honco results button registered into Mattermost's search box.
