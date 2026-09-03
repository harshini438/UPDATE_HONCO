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
    /meet list                   pending reminders for this channel
    /meet cancel <id>            drop one

Scheduling is done here rather than through Mattermost's scheduled-posts API so
it does not depend on an API shape that changes between releases. Pending items
are kept on disk, so a restart does not lose them.
"""
import json
import os
import re
import threading
import time
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


def post_to_channel(channel_id, message):
    if not BOT_TOKEN:
        return False
    body = json.dumps({"channel_id": channel_id, "message": message}).encode()
    req = urllib.request.Request(
        CHAT_URL + "/api/v4/posts", data=body, method="POST",
        headers={"Authorization": "Bearer " + BOT_TOKEN,
                 "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return 200 <= r.status < 300
    except Exception:
        return False


# --------------------------------------------------------------------------- scheduler
def scheduler():
    while True:
        try:
            now = time.time()
            with _lock:
                items = load()
                due = [i for i in items if i["at"] <= now]
                if due:
                    save([i for i in items if i["at"] > now])
            for i in due:
                post_to_channel(
                    i["channel_id"],
                    ":bell: **%s** starts now — [join the meeting](%s)\n_scheduled by @%s_"
                    % (i["topic"], i["url"], i["user"]),
                )
        except Exception:
            pass
        time.sleep(20)


# --------------------------------------------------------------------------- command
def handle(form):
    text = (form.get("text", [""])[0] or "").strip()
    channel_id = form.get("channel_id", [""])[0]
    channel_name = form.get("channel_name", [""])[0]
    user = form.get("user_name", ["someone"])[0]

    if text.lower() in ("help", "-h", "--help"):
        return ephemeral(
            "**/meet** — start or schedule a Honco Meet room\n\n"
            "`/meet` — room for this channel, right now\n"
            "`/meet standup` — room named *standup*\n"
            "`/meet in 30m sprint review` — announce now, remind the channel in 30 minutes\n"
            "`/meet at 15:00 client demo` — same, at a clock time (tomorrow if already past)\n"
            "`/meet list` — pending reminders here\n"
            "`/meet cancel <id>` — drop one"
        )

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

    if text.lower().startswith("cancel"):
        wanted = text.split(None, 1)[1].strip() if " " in text else ""
        with _lock:
            items = load()
            keep = [i for i in items if not (i["id"] == wanted and i["channel_id"] == channel_id)]
            removed = len(items) - len(keep)
            if removed:
                save(keep)
        return ephemeral("Cancelled." if removed else "No such id in this channel.")

    when, topic = parse_when(text)
    if not topic:
        topic = channel_name or "meeting"
    url = room_url(slugify(topic))

    if when is None:
        return in_channel(
            ":movie_camera: **%s** — [join the meeting](%s)\n_started by @%s_"
            % (topic, url, user)
        )

    item = {
        "id": os.urandom(3).hex(),
        "at": when.timestamp(),
        "topic": topic,
        "url": url,
        "channel_id": channel_id,
        "user": user,
    }
    with _lock:
        items = load()
        items.append(item)
        save(items)

    # Echo the parsed time back: if the parse was wrong, the user sees it now
    # rather than when the reminder fails to arrive.
    return in_channel(
        ":calendar: **%s** scheduled for **%s** — [join the meeting](%s)\n"
        "_by @%s · a reminder will land here · cancel with_ `/meet cancel %s`"
        % (topic, when.strftime("%a %d %b, %H:%M"), url, user, item["id"])
    )


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
        form = urllib.parse.parse_qs(self.rfile.read(length).decode("utf-8", "replace"))
        # Mattermost sends a per-command token; reject anything else so a stray
        # process on this host cannot post as the bot.
        if CMD_TOKEN and form.get("token", [""])[0] != CMD_TOKEN:
            self._json(403, {"text": "bad token"})
            return
        try:
            self._json(200, handle(form))
        except Exception as e:
            self._json(200, ephemeral("Sorry — /meet hit an error: %s" % e))


if __name__ == "__main__":
    threading.Thread(target=scheduler, daemon=True).start()
    ThreadingHTTPServer(LISTEN, Handler).serve_forever()
