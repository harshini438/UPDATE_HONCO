#!/usr/bin/env python3
"""Honco Meet -- the /meet slash command service.

Mattermost POSTs a form-encoded slash command here; we answer with JSON.
Listens on localhost only: the chat server is on the same box, and this endpoint
must not be reachable from anywhere else.

    /meet                        room named after the channel, now
    /meet standup                room named "standup", now
    /meet in 30m sprint review   posts now, reminds the channel in 30 minutes
    /meet at 15:00 client demo   same, at a wall-clock time today (or tomorrow
                                 if that time has already passed)
    /meet schedule               same as the two above, as a form instead of typed syntax
    /meet list                   pending reminders for this channel
    /meet history                meetings already started/sent/cancelled here
    /meet cancel <id>            drop one

Scheduling is done here rather than through Mattermost's scheduled-posts API so
it does not depend on an API shape that changes between releases. Pending items
are kept on disk, so a restart does not lose them.

Phase 2 (Honco Workspace upgrade) adds two more routes, both server-driven
Mattermost UI mechanisms rather than a webapp change or a plugin -- see
HONCO_UPGRADE_PLAN.md for why:

    POST /dialog   Interactive Dialog submission (the /meet schedule form)
    POST /action   message-action button click (Cancel; see link_block below
                   for why Copy Link isn't one of these anymore)

Every /meet response (instant, scheduled, reminder, or from the dialog) puts
the meeting URL in the message as a fenced code block (see link_block()) --
Mattermost's own post renderer already gives any code block a real one-click
copy icon (navigator.clipboard, with a document.execCommand fallback, and
"Copy code" -> "Copied" feedback that reverts on its own), so that is reused
rather than reimplemented. An earlier version of this put the URL behind a
"Copy Link" *button* instead, whose click only revealed the same code block
one step later -- confusing (users expected the button click itself to copy)
and unnecessary once the block can just be in the message to begin with. The
button and its /action handler still exist so any already-posted message
that has one keeps working, but nothing new attaches it.
"""
import json
import os
import re
import sys
import threading
import time
import traceback
import urllib.parse
import urllib.request
from datetime import datetime, timedelta
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

CHAT_URL = os.environ.get("CHAT_URL", "http://127.0.0.1:8065")
MEET_BASE = os.environ.get("MEET_BASE", "https://192.168.2.156:8443")
BOT_TOKEN = os.environ.get("BOT_TOKEN", "")
CMD_TOKEN = os.environ.get("CMD_TOKEN", "")   # Mattermost's per-command token
LISTEN = ("127.0.0.1", int(os.environ.get("PORT", "8077")))
STATE = os.path.expanduser("~/honco-chat/run/meet-schedule.json")
# Phase 2: a place to remember meetings that already happened, so /meet
# history has something to show. The scheduler previously just discarded a
# fired or cancelled item -- meet-schedule.json was only ever a queue, never
# a record. This is a queue's natural companion, not a new data model: same
# read-modify-write-via-tmp-file pattern as STATE, capped so it can't grow
# without bound on a long-lived box.
HISTORY = os.path.expanduser("~/honco-chat/run/meet-history.json")
HISTORY_MAX = 200
# Where meetsvc's own interactive callbacks (dialog open/submit, message
# actions) are reached. Must be the address Mattermost can dial, same
# loopback-only reasoning as everything else here -- see module docstring.
SELF_BASE = os.environ.get("SELF_BASE", "http://127.0.0.1:%d" % int(os.environ.get("PORT", "8077")))

# Recording integration: when a room is created, tell the Honco Workspace
# plugin which channel it belongs to. Jibri later reports a finished
# recording by room name alone, and the plugin refuses any room it was not
# told about -- so this registration is what makes a recording both
# routable to a channel and authorised at all. Server-to-server, with a
# shared secret, because meetsvc has no Mattermost session of its own.
# Unset secret = feature off, and /meet behaves exactly as before.
HONCO_SERVICE_SECRET = os.environ.get("HONCO_SERVICE_SECRET", "")
REGISTER_PATH = "/plugins/com.honco.workspace/api/v1/meetings/register"

_lock = threading.Lock()


# --------------------------------------------------------------------------- state
def load():
    try:
        with open(STATE) as f:
            return json.load(f)
    except (OSError, ValueError):
        return []


def save(items):
    os.makedirs(os.path.dirname(STATE), exist_ok=True)
    tmp = STATE + ".tmp"
    with open(tmp, "w") as f:
        json.dump(items, f, indent=2)
    os.replace(tmp, STATE)


def load_history():
    try:
        with open(HISTORY) as f:
            return json.load(f)
    except (OSError, ValueError):
        return []


def record_history(entry):
    """Append one meeting record. status is one of the honestly-knowable
    values -- see the module docstring addition below for why there is no
    'in_progress' or 'completed': meetsvc has no channel back from Jitsi at
    all, so it cannot know when a room actually starts or empties."""
    entry = dict(entry)
    entry.setdefault("recorded_at", time.time())
    with _lock:
        items = load_history()
        items.append(entry)
        if len(items) > HISTORY_MAX:
            items = items[-HISTORY_MAX:]
        os.makedirs(os.path.dirname(HISTORY), exist_ok=True)
        tmp = HISTORY + ".tmp"
        with open(tmp, "w") as f:
            json.dump(items, f, indent=2)
        os.replace(tmp, HISTORY)


# --------------------------------------------------------------------------- Mattermost API
def mm_api(method, path, body=None):
    """Call the Mattermost API as the bot. Used for opening dialogs and
    resolving a user_id to a username for dialog submissions, which (unlike
    slash-command POSTs) don't include user_name directly. Returns the
    decoded JSON body, or None on any failure -- every caller already has to
    handle 'we don't know', same as post_to_channel's bool return."""
    if not BOT_TOKEN:
        log("mm_api: no BOT_TOKEN configured")
        return None
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        CHAT_URL + path, data=data, method=method,
        headers={"Authorization": "Bearer " + BOT_TOKEN,
                 "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return json.loads(r.read().decode()) if r.status != 204 else {}
    except Exception as e:
        log("mm_api: %s %s failed -- %s" % (method, path, e))
        return None


def register_room(url, channel_id, user, topic):
    """Tell the plugin that this Jitsi room belongs to this channel.

    Deliberately best-effort: a meeting must still start if the chat server
    or the plugin is momentarily unavailable. The only consequence of a
    missed registration is that a later recording for that room is refused,
    which is the safe direction to fail in -- an unregistered room is
    exactly the case the plugin is supposed to reject.
    """
    if not HONCO_SERVICE_SECRET:
        return
    room = url.rstrip("/").rsplit("/", 1)[-1]
    body = json.dumps({
        "room_name": room,
        "channel_id": channel_id,
        "creator_id": "",
        "topic": topic or "",
    }).encode()
    req = urllib.request.Request(
        CHAT_URL + REGISTER_PATH, data=body, method="POST",
        headers={"Content-Type": "application/json",
                 "X-Honco-Service-Secret": HONCO_SERVICE_SECRET},
    )
    try:
        with urllib.request.urlopen(req, timeout=5) as r:
            r.read()
        log("registered room %s for channel %s" % (room, channel_id))
    except Exception as e:
        # Never log the secret, and never raise: the meeting goes ahead.
        log("room registration failed for %s -- %s" % (room, e))


# --------------------------------------------------------------------------- helpers
def slugify(s, fallback="meeting"):
    s = re.sub(r"[^A-Za-z0-9]+", "-", s or "").strip("-").lower()
    return s[:48] or fallback


def room_url(name):
    # A guessable room name on a server anyone can reach is a room strangers can
    # walk into, so append entropy the humans never have to type.
    return "%s/%s-%s" % (MEET_BASE.rstrip("/"), name, os.urandom(3).hex())


WHEN_RE = re.compile(
    r"^\s*(?:in\s+(?P<rel>\d+)\s*(?P<unit>m|min|mins|minute|minutes|h|hr|hrs|hour|hours)"
    r"|at\s+(?P<hh>\d{1,2})[:.](?P<mm>\d{2}))\s+(?P<rest>.*)$",
    re.I,
)


def parse_when(text):
    """Return (datetime|None, remaining_text). Never guesses silently."""
    m = WHEN_RE.match(text or "")
    if not m:
        return None, (text or "").strip()
    now = datetime.now()
    if m.group("rel"):
        n = int(m.group("rel"))
        unit = m.group("unit").lower()
        delta = timedelta(hours=n) if unit.startswith("h") else timedelta(minutes=n)
        return now + delta, m.group("rest").strip()
    hh, mm = int(m.group("hh")), int(m.group("mm"))
    if not (0 <= hh < 24 and 0 <= mm < 60):
        return None, (text or "").strip()
    when = now.replace(hour=hh, minute=mm, second=0, microsecond=0)
    if when <= now:
        when += timedelta(days=1)      # "at 09:00" said at 10am means tomorrow
    return when, m.group("rest").strip()


def log(msg):
    # meetsvc has no logging framework -- stderr is already redirected to
    # meetsvc.log by honcochat.sh's start_svc(), so this is the whole story.
    print("%s %s" % (datetime.now().isoformat(timespec="seconds"), msg),
          file=sys.stderr, flush=True)


def post_to_channel(channel_id, message, buttons=None):
    if not BOT_TOKEN:
        log("post_to_channel: no BOT_TOKEN configured, dropping post")
        return False
    payload = {"channel_id": channel_id, "message": message}
    if buttons:
        # Message actions aren't exclusive to slash-command responses --
        # any bot post can carry props.attachments the same way, so a
        # meeting scheduled via the dialog gets the same Copy Link/Cancel
        # buttons as one scheduled by typing /meet, not a lesser version.
        payload["props"] = {"attachments": [{"actions": buttons}]}
    body = json.dumps(payload).encode()
    req = urllib.request.Request(
        CHAT_URL + "/api/v4/posts", data=body, method="POST",
        headers={"Authorization": "Bearer " + BOT_TOKEN,
                 "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            ok = 200 <= r.status < 300
            if not ok:
                log("post_to_channel: HTTP %s from chat server" % r.status)
            return ok
    except Exception as e:
        log("post_to_channel: failed -- %s" % e)
        return False


# --------------------------------------------------------------------------- scheduler
# A reminder that fails to post (chat server briefly down, bad token) used to
# be dropped from meet-schedule.json the instant it came due, whether or not
# the post actually succeeded -- lost forever, no log, no retry. Due items are
# now only removed once post_to_channel returns True; a failure puts the item
# back with an attempt count, retried on the next tick, and given up on (with
# a loud log line, not a silent one) after MAX_POST_ATTEMPTS.
MAX_POST_ATTEMPTS = 180  # ~1 hour at the 20s poll interval below


def scheduler():
    while True:
        try:
            now = time.time()
            with _lock:
                due = [i for i in load() if i["at"] <= now]

            retry = []
            for i in due:
                # @channel makes this a real notification, not just a post:
                # Mattermost only pushes desktop/mobile/unread-badge alerts
                # for messages containing a channel-wide mention (or a named
                # @user, which this already had but only ever named the
                # scheduler, never the people who need to actually join).
                # Without it, a reminder in a channel nobody has open just
                # sits there silently -- indistinguishable from any other
                # message, which defeats the point of a reminder.
                sent = post_to_channel(
                    i["channel_id"],
                    "@channel :bell: **%s** starts now — [join the meeting](%s)\n_scheduled by @%s_\n%s"
                    % (i["topic"], i["url"], i["user"], link_block(i["url"])),
                )
                if sent:
                    log("reminder sent: %s (%s)" % (i["id"], i["topic"]))
                    record_history({
                        "id": i["id"], "topic": i["topic"], "channel_id": i["channel_id"],
                        "user": i["user"], "at": i["at"], "url": i["url"],
                        "status": "reminder_sent",
                    })
                    continue
                attempts = i.get("post_attempts", 0) + 1
                if attempts >= MAX_POST_ATTEMPTS:
                    log("reminder %s (%s) DROPPED after %d failed attempts"
                        % (i["id"], i["topic"], attempts))
                    record_history({
                        "id": i["id"], "topic": i["topic"], "channel_id": i["channel_id"],
                        "user": i["user"], "at": i["at"], "url": i["url"],
                        "status": "failed",
                    })
                    continue
                i["post_attempts"] = attempts
                retry.append(i)

            if due:
                handled_ids = {i["id"] for i in due}
                retry_by_id = {i["id"]: i for i in retry}
                with _lock:
                    # Re-read rather than reuse the earlier snapshot: a /meet
                    # call could have added or cancelled items on this host
                    # while we were off posting to the chat server.
                    current = load()
                    kept = [c for c in current if c["id"] not in handled_ids]
                    kept.extend(retry_by_id[c["id"]] for c in current
                                if c["id"] in retry_by_id)
                    save(kept)
        except Exception:
            log("scheduler tick failed:\n" + traceback.format_exc())
        time.sleep(20)


# --------------------------------------------------------------------------- interactive UI (Phase 2)
# Two Mattermost mechanisms, both server-driven, neither needing a plugin,
# a webapp rebuild, or a second Go binary -- see HONCO_UPGRADE_PLAN.md for
# why this was chosen over a full plugin for this pass:
#
#   * Message actions: buttons on a bot's post. Mattermost POSTs JSON to
#     the button's `integration.url` when clicked; the handler below
#     answers with {"ephemeral_text": ...} to show the clicking user a
#     private reply -- used here for "Copy Link" (into a fenced code
#     block, which Mattermost already renders with its own copy icon, so
#     this reuses an existing UI affordance rather than inventing one) and
#     "Cancel".
#   * Interactive dialogs: a real modal form, opened by calling
#     /api/v4/actions/dialogs/open with the trigger_id a slash command POST
#     already carries. Used for `/meet schedule`.
def action_button(name, action_id, extra_context=None):
    ctx = {"action": action_id}
    if extra_context:
        ctx.update(extra_context)
    return {
        "id": action_id,
        "name": name,
        "integration": {"url": SELF_BASE + "/action", "context": ctx},
    }


def copy_link_button(url):
    return action_button("Copy Link", "copy_link", {"url": url})


def cancel_button(item_id, channel_id):
    return action_button("Cancel", "cancel", {"id": item_id, "channel_id": channel_id})


def with_actions(resp, buttons):
    """Attach message-action buttons to a slash-command response. Same
    attachments/actions shape Mattermost already documents for interactive
    messages -- not a Honco invention."""
    resp["attachments"] = [{"actions": buttons}]
    return resp


def open_dialog(trigger_id, channel_id, channel_name):
    """Show the Schedule Meeting form. Must be called within the ~3s
    Mattermost gives a slash command to respond, so this happens before any
    scheduling work, not after."""
    return mm_api("POST", "/api/v4/actions/dialogs/open", {
        "trigger_id": trigger_id,
        "url": SELF_BASE + "/dialog",
        "dialog": {
            "callback_id": "schedule_meeting",
            "title": "Schedule a Meeting",
            "introduction_text": "Posts to **#%s**. Leave \"When\" blank to start instantly instead of scheduling." % (channel_name or "this channel"),
            "elements": [
                {"display_name": "Meeting name", "name": "name", "type": "text",
                 "placeholder": "sprint review", "optional": True,
                 "help_text": "Defaults to the channel name if left blank."},
                {"display_name": "When", "name": "when", "type": "text",
                 "placeholder": "in 30m  /  at 15:00  /  leave blank for now",
                 "optional": True,
                 "help_text": "Same format as typing /meet in 30m ... or /meet at 15:00 ... directly."},
            ],
            "submit_label": "Schedule",
            # channel_id/channel_name travel in `state` so the submission
            # handler (which Mattermost calls with only user_id + whatever
            # is in `state`, not the original slash-command context) has
            # them without a second API round-trip.
            "state": json.dumps({"channel_id": channel_id, "channel_name": channel_name}),
        },
    })


def resolve_username(user_id):
    u = mm_api("GET", "/api/v4/users/%s" % user_id)
    return (u or {}).get("username", "someone")


def link_block(url):
    """A meeting URL as a fenced code block. Mattermost's own post renderer
    (code_block.tsx, via the CopyButton component in copy_button.tsx)
    already puts a real one-click copy affordance on any fenced code block
    -- real navigator.clipboard.writeText(), a document.execCommand('copy')
    fallback where clipboard isn't available, and "Copy code" -> "Copied"
    feedback that reverts on its own. That is an existing, already-tested
    piece of Mattermost, not something to reimplement here."""
    return "```\n%s\n```" % url


def cancel_meeting(item_id, channel_id):
    """Shared by the /meet cancel text command and the Cancel message-action
    button -- one cancellation path, not two. Returns the cancelled item
    (and records it to history) or None if no such pending item existed."""
    with _lock:
        items = load()
        cancelled_item = next(
            (i for i in items if i["id"] == item_id and i["channel_id"] == channel_id), None)
        if cancelled_item:
            save([i for i in items if i is not cancelled_item])
    if cancelled_item:
        record_history({**cancelled_item, "status": "cancelled"})
    return cancelled_item


def schedule_meeting(channel_id, channel_name, user, text):
    """The actual scheduling logic, shared verbatim by the /meet text
    command and the dialog submission handler -- exactly the 'reuse the
    existing /meet backend logic instead of duplicating' requirement.
    `text` uses the exact same combined syntax /meet always has ("in 30m
    sprint review"); the dialog handler builds this same string from its
    separate When/Name fields rather than parse_when gaining a second,
    subtly different calling convention.
    Returns (url, message, item_id|None) -- the two callers wrap `message`
    differently (in_channel vs. a plain bot post), so this returns the raw
    pieces, not a response dict."""
    when, topic = parse_when(text)
    if not topic:
        topic = channel_name or "meeting"
    url = room_url(slugify(topic))
    # Register before answering, so a recording started immediately after
    # the room link appears is already routable to this channel.
    register_room(url, channel_id, user, topic)

    if when is None:
        record_history({
            "id": os.urandom(3).hex(), "topic": topic, "channel_id": channel_id,
            "user": user, "at": time.time(), "url": url, "status": "started",
        })
        return url, (
            ":movie_camera: **%s** — [join the meeting](%s)\n_started by @%s_\n%s"
            % (topic, url, user, link_block(url))
        ), None

    item = {
        "id": os.urandom(3).hex(), "at": when.timestamp(), "topic": topic,
        "url": url, "channel_id": channel_id, "user": user,
    }
    with _lock:
        items = load()
        items.append(item)
        save(items)
    return url, (
        ":calendar: **%s** scheduled for **%s** — [join the meeting](%s)\n"
        "_by @%s · a reminder will land here · cancel with_ `/meet cancel %s`\n%s"
        % (topic, when.strftime("%a %d %b, %H:%M"), url, user, item["id"], link_block(url))
    ), item["id"]


# --------------------------------------------------------------------------- command
def handle(form):
    text = (form.get("text", [""])[0] or "").strip()
    channel_id = form.get("channel_id", [""])[0]
    channel_name = form.get("channel_name", [""])[0]
    user = form.get("user_name", ["someone"])[0]
    trigger_id = form.get("trigger_id", [""])[0]

    if text.lower() in ("help", "-h", "--help"):
        return ephemeral(
            "**/meet** — start or schedule a Honco Meet room\n\n"
            "`/meet` — room for this channel, right now\n"
            "`/meet standup` — room named *standup*\n"
            "`/meet in 30m sprint review` — announce now, remind the channel in 30 minutes\n"
            "`/meet at 15:00 client demo` — same, at a clock time (tomorrow if already past)\n"
            "`/meet schedule` — same thing, as a form instead of typed syntax\n"
            "`/meet list` — pending reminders here\n"
            "`/meet history` — meetings already started/sent/cancelled here\n"
            "`/meet cancel <id>` — drop one"
        )

    if text.lower() in ("schedule", "start"):
        # Interactive Dialogs need trigger_id, which only exists on the
        # original slash-command POST -- if this ever gets reached from
        # somewhere without one (defensive; Mattermost always sends it),
        # fail into the same plain-text path rather than a dead button.
        if trigger_id:
            opened = open_dialog(trigger_id, channel_id, channel_name)
            if opened is not None:
                return ephemeral("")  # the dialog itself is the response
            log("open_dialog failed for channel %s" % channel_id)
            return ephemeral(":warning: Could not open the scheduling form (chat server unreachable). Try `/meet in 30m <name>` instead.")

    if text.lower() == "list":
        with _lock:
            items = [i for i in load() if i["channel_id"] == channel_id]
        if not items:
            return ephemeral("Nothing scheduled in this channel.")
        lines = ["**Scheduled meetings here**"]
        for i in sorted(items, key=lambda x: x["at"]):
            lines.append("- `%s` — **%s** at %s" % (
                i["id"], i["topic"],
                datetime.fromtimestamp(i["at"]).strftime("%d %b %H:%M")))
        return ephemeral("\n".join(lines))

    if text.lower() in ("history", "log"):
        with _lock:
            items = [i for i in load_history() if i["channel_id"] == channel_id]
        if not items:
            return ephemeral("No meeting history for this channel yet.")
        STATUS_LABEL = {
            "started": "started", "reminder_sent": "started (scheduled)",
            "cancelled": "cancelled", "failed": "failed to send",
        }
        lines = ["**Recent meetings here** _(last %d)_" % min(len(items), 20)]
        for i in sorted(items, key=lambda x: x["at"], reverse=True)[:20]:
            lines.append("- **%s** — %s — %s" % (
                i["topic"],
                datetime.fromtimestamp(i["at"]).strftime("%d %b %H:%M"),
                STATUS_LABEL.get(i.get("status"), i.get("status", "?"))))
        return ephemeral("\n".join(lines))

    if text.lower().startswith("cancel"):
        wanted = text.split(None, 1)[1].strip() if " " in text else ""
        if not wanted:
            return ephemeral("Usage: `/meet cancel <id>` — see the id in `/meet list`.")
        cancelled_item = cancel_meeting(wanted, channel_id)
        if cancelled_item:
            return ephemeral("Cancelled **%s**." % cancelled_item["topic"])
        return ephemeral("No such id in this channel. Check `/meet list` for current ids.")

    url, message, item_id = schedule_meeting(channel_id, channel_name, user, text)
    # No Copy Link button: the URL is already in the message above as a
    # fenced code block, which gets Mattermost's own native one-click copy
    # icon for free -- a button whose only job was to reveal that same code
    # block one click later was exactly the bug report (users expected the
    # button itself to copy, and it didn't). Cancel is still a real action.
    if item_id:
        return with_actions(in_channel(message), [cancel_button(item_id, channel_id)])
    return in_channel(message)


def ephemeral(text):
    return {"response_type": "ephemeral", "text": text}


def in_channel(text):
    return {"response_type": "in_channel", "text": text}


# --------------------------------------------------------------------------- http
class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_a):
        pass

    def _json(self, code, obj):
        b = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_GET(self):
        if self.path == "/health":
            self._json(200, {"status": "ok"})
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0) or 0)
        raw = self.rfile.read(length)

        if self.path == "/dialog":
            self._handle_dialog(raw)
        elif self.path == "/action":
            self._handle_action(raw)
        else:
            self._handle_slash_command(raw)

    def _handle_slash_command(self, raw):
        form = urllib.parse.parse_qs(raw.decode("utf-8", "replace"))
        # Mattermost sends a per-command token; reject anything else so a stray
        # process on this host cannot post as the bot.
        if CMD_TOKEN and form.get("token", [""])[0] != CMD_TOKEN:
            self._json(403, {"text": "bad token"})
            return
        try:
            self._json(200, handle(form))
        except Exception as e:
            log("slash command handler failed:\n" + traceback.format_exc())
            self._json(200, ephemeral("Sorry — /meet hit an error: %s" % e))

    def _handle_dialog(self, raw):
        # Dialog submissions carry no CMD_TOKEN (Mattermost's own dialog
        # protocol doesn't include one) -- trusted the same way the rest of
        # this service already is: bound to 127.0.0.1 only, so nothing but
        # this same host's Mattermost server can ever reach it. Not a new
        # trust boundary, the same one the module docstring already states.
        try:
            payload = json.loads(raw.decode("utf-8", "replace"))
        except ValueError:
            self._json(400, {"error": "bad json"})
            return
        if payload.get("cancelled"):
            self._json(200, {})
            return
        try:
            state = json.loads(payload.get("state") or "{}")
            channel_id = state.get("channel_id", "")
            channel_name = state.get("channel_name", "")
            submission = payload.get("submission") or {}
            user = resolve_username(payload.get("user_id", ""))

            # Recombine the dialog's two fields into the one syntax
            # schedule_meeting/parse_when already understands. parse_when's
            # regex requires whitespace *after* the time expression even
            # when nothing follows it ("in 30m" alone does not match, "in
            # 30m " does) -- always joining with a single space, rather
            # than only when name_text is non-empty, satisfies that
            # unconditionally.
            when_text = (submission.get("when") or "").strip()
            name_text = (submission.get("name") or "").strip()
            combined = (when_text + " " + name_text) if when_text else name_text
            url, message, item_id = schedule_meeting(channel_id, channel_name, user, combined)
            # Same reasoning as the text-command path: no Copy Link button,
            # the code block in `message` already gives one-click native
            # copy. Cancel is still attached when there's something to cancel.
            buttons = [cancel_button(item_id, channel_id)] if item_id else None
            # A dialog submission's own HTTP response only controls the
            # dialog UI itself (close it / show a field error) -- it cannot
            # post a channel message the way a slash-command response can.
            # Posting explicitly here is the same pattern the scheduler
            # already uses for reminders, not a new mechanism.
            post_to_channel(channel_id, message, buttons)
            self._json(200, {})
        except Exception:
            log("dialog submission failed:\n" + traceback.format_exc())
            self._json(200, {"error": "Something went wrong scheduling that meeting. Check the server log."})

    def _handle_action(self, raw):
        try:
            payload = json.loads(raw.decode("utf-8", "replace"))
        except ValueError:
            self._json(400, {"error": "bad json"})
            return
        ctx = payload.get("context") or {}
        action = ctx.get("action")
        try:
            if action == "copy_link":
                self._json(200, {"ephemeral_text": "```\n%s\n```" % ctx.get("url", "")})
            elif action == "cancel":
                item = cancel_meeting(ctx.get("id", ""), ctx.get("channel_id", ""))
                if item:
                    self._json(200, {"ephemeral_text": "Cancelled **%s**." % item["topic"]})
                else:
                    self._json(200, {"ephemeral_text": "Already gone — nothing to cancel."})
            else:
                self._json(200, {"ephemeral_text": "Unknown action."})
        except Exception:
            log("action handler failed:\n" + traceback.format_exc())
            self._json(200, {"ephemeral_text": "Sorry, that action failed. Check the server log."})


if __name__ == "__main__":
    threading.Thread(target=scheduler, daemon=True).start()
    ThreadingHTTPServer(LISTEN, Handler).serve_forever()
