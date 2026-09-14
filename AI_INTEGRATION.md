# Honco AI Assistant — the integration contract

Honco Chat displays what an **external AI service** produces for a meeting:
live transcript, real-time suggestions for the salesperson, conversation
insights, and the post-call summary. That service is owned by another team.

**Honco owns:** the assistant UI, the meeting association, the
authorization boundary, the real-time transport to browsers, the bounded
storage, notifications, and the error/loading states.

**The AI service owns:** voice recognition, speech processing, transcript
generation, conversation understanding, suggestions, summarization, and
the models behind all of it. Honco implements none of it and never will.

> **Status (2026-09-11).** No AI/voice service was reachable from this
> deployment, and no service API contract was found in this repository (see
> *What already existed* below). The contract in §2 is therefore **Honco's
> proposal**, implemented and tested against a harness that plays the
> service. §1 — the push direction — is the half that matters most and is
> the one Honco *serves*; if the service already speaks a different shape,
> §1 is small and cheap to adapt, and §2 lives in exactly one file
> (`plugins/com.honco.workspace/server/ai_adapter.go`).

## What already existed in this repository

| Thing | What it is | Reused? |
|---|---|---|
| `transcribe/` | Batch pipeline: watches for a finished Jibri recording, runs whisper.cpp / the in-house "vixy" model, writes `.txt`/`.vtt`. Host-side worker, needs `~/honco-transcribe`, which does not exist on this host. | **No** — batch, post-call, file-based. Not a real-time service, and STT is explicitly out of scope for Honco. |
| `summarize/honco-summarize.sh` | Claude CLI on the `mother` host behind a forced-command SSH key; stdin transcript → stdout notes. | **No** (for the assistant) — it is already wired to Meeting Intelligence, which is a different feature and stays as it is. `mother` (192.168.2.150:31013) was **re-probed and is unreachable** from here. |
| `plugins/.../summarizer.go` | The plugin's Claude path for Meeting Intelligence. | **Untouched.** |
| `Honco_Workspace_V2` (sibling directory) | A separate, from-scratch product with its own transcription/AI-assistant backend. | **No** — a different application with its own database and API. Honco Chat does not read from or depend on it. |

Nothing in either project exposes a *real-time* transcript/suggestion
stream, so there was no existing contract to adopt.

## Architecture

```
Salesperson ── Honco Chat (browser)
                   │  ordinary Mattermost session cookie; no AI credential
                   ▼
      Honco AI Assistant panel  (webapp/dist/main.js)
                   │  REST for reads, Mattermost WebSocket for live updates
                   ▼
      Honco AI integration layer  (plugin: ai.go, ai_api.go, ai_adapter.go)
          ▲                              │
          │ POST /ai/events              │ AIIntegrationService
          │ X-Honco-AI-Secret            │ Authorization: Bearer <token>
          │                              ▼
              Existing teammate AI service
              (voice, transcript, analysis, suggestions, summary)
```

Two rules follow from this shape and are enforced in code:

- **The browser never holds an AI credential.** The service token and the
  callback secret exist only in the server's plugin configuration. The
  panel talks to Honco, with the session it already has.
- **A meeting id is a reference, never a capability.** Every read
  re-proves membership of the meeting's *channel* against Mattermost.

## 1. Service → Honco (what Honco serves today)

```
POST /plugins/com.honco.workspace/api/v1/ai/events
X-Honco-AI-Secret: <AICallbackSecret>
Content-Type: application/json
```

```jsonc
{
  "meeting_id": "wxepo6rqetftzqs9jiadxrcyte",  // or "room_name": "acme-corp-sales-call-288d6c"
  "session_id": "ext-sess-1",                   // the service's own id, optional
  "events": [ /* 1..100 events */ ]
}
```

Identify the meeting by **`meeting_id`** (Honco's id, handed to the service
in `StartSession`) or by **`room_name`** (the Jitsi room, which the service
already knows if it joined the call). The meeting must already exist —
the service cannot create one.

### Event types

| `type` | Fields | Meaning |
|---|---|---|
| `status` | `status` (`connecting`/`live`/`ended`/`completed`/`unavailable`/`failed`), `capture_status` (free text: "listening", "paused"…) | The service's own session state. |
| `transcript` | `lines: [{at, speaker, text, final}]`, or a single `{speaker, text, final, at}` | One or more utterances. `final: false` marks an interim result the recogniser may revise; Honco renders it dimmed and italic. |
| `suggestion` | `text`, `kind`, `title`, `source`, `status`, `id` | A suggestion for the salesperson. `kind` drives the icon/label: `suggestion`, `suggested_response`, `next_best_action`, `client_concern`, `objection`, `opportunity`. Unknown kinds render as a plain suggestion. |
| `insight` | `text`, `kind`, `title`, `id` | A detected concern, sentiment or opportunity. |
| `topics` | `topics: [string]` | The current important topics (replaces the previous set; deduped case-insensitively). |
| `final` | `summary`, `key_points[]`, `decisions[]`, `action_items[]`, `key_insights[]`, `client_insights[]`, `topics[]`, `transcript_ref` | Post-call outputs. Fields merge, so they can arrive in several pushes. Sets the session to `completed` and sends the "AI summary ready" notification. |
| `error` | `kind` (a short class, e.g. `asr_crashed`), `message` (operator detail) | Processing failed. **`message` is logged server-side only** — it never reaches a browser or an API response. |

### Event names the service may already use

A service with its own vocabulary does not have to change it. These names
are accepted and mapped onto the semantics above; anything else is still a
`400`, so a typo is caught rather than silently dropped.

| The service sends | Honco treats it as |
|---|---|
| `session_started` | `status` with `status: live` |
| `session_ended` | `status` with `status: ended` |
| `session_status` | `status` |
| `utterance`, `transcript_line` | `transcript` |
| `partial` | `transcript` with `final: false` |
| `topic` | `topics` |
| `summary`, `summary_ready`, `final_summary` | `final` |
| `processing_failed`, `failed` | `error` |

Every event may carry `id` (string) and `at` (epoch ms). **`id` makes
delivery idempotent**: an event id seen in the last 200 for that meeting is
counted as `replayed` and changes nothing, so a service that retries after
a timeout cannot duplicate a line.

**Response** `200 {"meeting_id", "accepted", "replayed", "status"}`.
Errors: `401` bad/missing secret · `404` unknown meeting · `400` malformed
(the body names the field, never echoes content) · `413` over 512 KB ·
`429` over 300 pushes/min (burst 60).

### Bounds Honco applies

| Limit | Value | Why |
|---|---|---|
| Body | 512 KB | |
| Events per push | 100 | |
| Transcript line / suggestion / insight | 4 000 chars (clipped with `…`) | |
| Final section | 60 000 chars | |
| Transcript kept per meeting | 2 000 lines | The **service is the source of truth**; Honco keeps a window. `line_count` still counts every line. |
| Live window sent on open | 200 lines | Older pages are fetched on demand. |
| Suggestions / insights kept | 50 each · topics 30 | |

## 2. Honco → Service (`AIIntegrationService`, proposed)

Implemented in `ai_adapter.go`; **only used when `AIServiceURL` is set.**
When it is unset every method returns "not configured" and the panel says
so — it never fakes a success.

| Method | Call | Purpose |
|---|---|---|
| `StartSession` | `POST {base}/v1/sessions` → `{session_id, status}` | Attach the service to a Honco meeting. Body: `meeting_id`, `room_name`, `channel_id`, `topic`, `started_at`, `participants[]`, `callback_url` (Honco tells the service where to push, so it needs no separate configuration). |
| `EndSession` | `POST {base}/v1/sessions/{id}/end` | The meeting ended. |
| `SessionStatus` | `GET {base}/v1/sessions/{id}` | The service's view of a session. |
| `Transcript` | `GET {base}/v1/sessions/{id}/transcript?after=&limit=` | Lines beyond Honco's window. |
| `Summary` | `GET {base}/v1/sessions/{id}/summary` | Post-call outputs. |
| `Health` | `GET {base}/v1/health` | Reachability, shown on the admin dashboard. Probed on open/refresh only — never on a timer. |

Authentication: `Authorization: Bearer <AIServiceToken>`. Timeout 15 s.
**Redirects are refused** (a redirect could hand the token to another
host); responses are read with a 4 MB cap; `401/403` → auth, `5xx` →
unavailable, `4xx` → refused, timeout → timeout. The panel shows the
class, never the message.

**If the real service's API differs, change this one file.** Nothing else
in the plugin or the UI depends on these paths.

## 3. Honco's own endpoints (browser ← server)

All require a Mattermost session; all except `/ai/status` re-prove channel
membership and 404 otherwise.

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/ai/status` | Booleans only: is a callback secret set, is a service URL set. |
| GET | `/api/v1/meetings/{id}/ai` | Meeting + participants + session view (live window, suggestions, insights, topics, final). |
| GET | `/api/v1/meetings/{id}/ai/transcript?before=&limit=` | Older lines, newest-page-first paging. |
| POST | `/api/v1/meetings/{id}/ai/session` | Start/retry. `202` with `connecting`; the outcome arrives over the WebSocket. `503` when no service URL is set. `409` if the meeting has ended. |
| DELETE | `/api/v1/meetings/{id}/ai/session` | Detach before the meeting ends. |

**Live updates** are a Mattermost WebSocket event,
`custom_com.honco.workspace_ai_event`, broadcast to the meeting's
**channel** — so Mattermost's own delivery rules decide who receives it.
The panel applies the delta in place and re-reads the session after a
reconnect. **There is no polling anywhere.**

## 3b. How a person reaches the assistant

Three entry points, none of which require knowing anything about plugins,
routes or URLs:

1. **The meeting card**, in the channel where the call is happening:
   `Join Meeting` · `AI Assistant` · `Meeting Summary`. Pressing
   **AI Assistant** opens the Honco panel on the assistant, already showing
   **that** meeting — the id comes from the card's own props, never a guess.
2. **The App Bar**, which now has a second Honco entry ("Honco AI
   Assistant", a spark) that opens the panel directly on the assistant.
3. **The Honco Workspace panel** itself: Tasks · Meetings · **AI Assistant**
   · Support · Search · Admin.

The bot's "AI summary ready" DM links to the meeting card, where the same
button is one press away.

### Controls

Contextual, never contradictory:

| State | Controls |
|---|---|
| Live / Connecting | `Stop session` · `Reconnect` |
| Ready (service URL set, meeting running) | `Start AI session` · `Reconnect` |
| Ended / Failed / Offline, meeting still running | `Start new AI session` · `Reconnect` |
| Completed | `Reconnect` |

Start on a live session and Stop on an ended one are no-ops server-side, so
repeated presses cannot create a second session. `Reconnect` re-reads the
session from the server and retries the service if it had dropped out.

The header shows the status as a glyph **and** a word (`● Live`,
`● Connecting`, `● Reconnecting`, `○ Offline`, `○ Ready`, `○ Ended`,
`● Processing`, `✓ Completed`, `⚠ Failed`) with a one-line explanation
underneath; nothing relies on colour alone. If this browser's WebSocket goes
down the header says **Reconnecting** and a banner reads "Connection
interrupted. Reconnecting…"; the panel re-reads the session when the socket
returns and never shows stale state as live.

After the call the panel becomes **AI Meeting Summary**: meeting, status
(`✓ Analysis ready` / `Preparing meeting insights…` / `Insights could not be
prepared`), then collapsible sections — Summary, Action items and Key
insights open by default; Key points, Decisions, Client insights, Important
topics and Transcript a click away. Action items render as a checklist
(visual only — nothing is written back and no Honco task is created);
"Owner: x" and "Label: text" are split for display when the service sends
them in that form, never inferred.

## 4. Meeting association

Reuses the existing meeting lifecycle exactly:

- `/meet` → `meetsvc` → `POST /meetings/register` creates the row in
  `honco_meetings` (unchanged).
- The participant poller (`meeting_lifecycle.go`) already detects when a
  room becomes occupied and when it empties. Those transitions now also
  call `aiMeetingStarted` / `aiMeetingEnded` — one extra `switch`, nothing
  else changed.
- Starting is **once per meeting**: a session that failed stays failed
  until a person presses Retry.

## 5. Configuration

System Console › Plugins › Honco Workspace (or `mmctl config patch`):

| Key | Secret? | Meaning |
|---|---|---|
| `AIServiceURL` | no | Base URL of the AI service. Must be `http`/`https`, no credentials, no query, no fragment. Empty ⇒ Honco makes no outbound AI calls. |
| `AIServiceToken` | **yes** | Bearer token Honco presents. Server-side only. |
| `AICallbackSecret` | **yes** | What the service sends as `X-Honco-AI-Secret`. Empty ⇒ every push is rejected (fails closed). |

Secrets are compared in constant time, never logged, never returned by any
endpoint, never sent to a browser, and never written to a post. The admin
dashboard reports only *whether* each is set.

## 6. Storage

**No new tables, no migration.** A session lives in the plugin KV store
under `ai:session:<meeting_id>`: status, the bounded transcript window,
suggestions, insights, topics, the final outputs, and the replay window.
The meeting itself stays the single row in `honco_meetings`.

Rationale: the service is the source of truth for the full transcript, so
duplicating it in Honco's database would be both redundant and a larger
disclosure surface. What Honco keeps is what the panel must render without
a round-trip to the service.

## 7. Security properties

- **IDOR** — every read goes through the same `meetingContext` gate as
  Meeting Intelligence: current membership of the meeting's channel, asked
  of Mattermost. A guessed or cross-team id is `404`, indistinguishable
  from a meeting that does not exist. A system admin who is not a channel
  member gets `404` too — there is no admin bypass on conversation content.
- **SSRF** — the only URL Honco ever fetches for this feature is the one an
  administrator set in the System Console. No request path takes a URL from
  a browser or from the service's own events; `transcript_ref` is opaque and
  only ever sent back to the same base.
- **Token leakage** — the token is only ever an `Authorization` header;
  redirects are refused; transport errors are redacted to scheme+host
  before logging.
- **Replay** — event ids are remembered per meeting (200 deep).
- **Oversized payloads** — 512 KB body, 100 events, per-field clipping.
- **Conversation data in logs** — never. Only the error *class* is stored;
  the service's message is logged once, clipped, for the operator.
- **Notifications** carry the meeting's name and a link, never AI content.

## 8. What is NOT claimed

- The teammate's AI service has **not** been connected. It was not
  reachable from this deployment and no endpoint for it was provided.
- Every test in this repository that produces AI content does so by
  **playing the service** through the documented callback with the shared
  secret. That proves Honco's boundary and UI. It proves nothing about the
  service.
- There is no Honco-side speech recognition, transcription, analysis or
  summarization for this feature, and none is planned here.

## 9. Connecting the real service

1. `mmctl --local config patch` (or System Console) → set `AIServiceURL`
   and `AIServiceToken`; generate an `AICallbackSecret` and give it to the
   service owner. Never paste secrets into chat.
2. The service pushes to
   `{SiteURL}/plugins/com.honco.workspace/api/v1/ai/events` with
   `X-Honco-AI-Secret`. Honco also sends this URL in `StartSession` as
   `callback_url`.
3. Start a meeting with `/meet`, open **AI Assistant** in the Honco panel,
   and watch the status pill: `Connecting…` → `Connected`.
4. If the service's API differs from §2, adjust `ai_adapter.go` only.
