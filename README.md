# Honco Workspace

Everything Honco-specific behind **Honco Chat**, **Honco Meet**, meeting
transcription, and the self-hosted remote-support server.

This repo deliberately does **not** contain the Mattermost or Jitsi source. That
is ~900 MB of vendor code that can be re-cloned from upstream at any time. What
matters — and what is genuinely ours — are the scripts that turn upstream into
our system, and they are all idempotent: pull a newer upstream, re-run them,
rebuild.

## Layout

| Path | What |
|---|---|
| `chat/branding/` | The de-brand pass over the Mattermost fork. Ten scripts, re-runnable after every upstream pull. |
| `chat/honcochat.sh` | start / publish / stop / status for postgres, the chat server, the Cloudflare tunnel and the `/meet` backend. |
| `chat/meetsvc.py` | The `/meet` slash-command service: instant rooms, scheduled meetings, reminders. |
| `chat/DEBRAND.md` | What was removed from upstream and why, plus the traps. **Read this before touching the fork.** |
| `meet/` | The Jitsi deployment: branding, recording (Jibri), generated virtual backgrounds, and the compose wrapper. |
| `transcribe/` | Recording → transcript → meeting notes. Pluggable speech-to-text. |
| `summarize/` | Runs on the box where the Claude CLI is authenticated; produces the notes. |
| `dns/` | The honco.in → Cloudflare migration pack and its verifier. |

## Where things run

| Component | Host |
|---|---|
| Honco Chat (server, web, `/meet`, Postgres) | ubuntu3 |
| Honco Meet — Jitsi, Jibri | ubuntu-3 |
| Transcription + notes worker | ubuntu-3 (the GPU is there) |
| RustDesk relay (hbbs/hbbr) | ubuntu-3 |
| Summariser | mother (the Claude CLI is authenticated there) |

## Rebuilding Honco Chat from scratch

```bash
git clone --depth 1 https://github.com/mattermost/mattermost.git server
git clone --depth 1 https://github.com/mattermost/mattermost-mobile.git mobile

# de-brand
./branding/debrand-server.sh
python3 branding/patch_push.py
python3 branding/debrand-sentry.py
python3 branding/debrand-support.py
python3 branding/debrand-polish.py
python3 branding/debrand-webapp.py
python3 branding/debrand-residual.py
./branding/debrand-mobile.sh
python3 branding/debrand-mobile-sentry.py
python3 branding/debrand-native-sentry.py
python3 branding/debrand-firebase.py

# build (Go 1.26.7 — the go.mod pins it; Node 24 for the web client)
cd server/server && go work init && go work use . && go work use ./public
go build -o ~/honco-chat/build/honcochat ./cmd/mattermost
cd ../webapp && npm install-scripts approve --all && npm ci && npm run build
```

Then prove it, because a clean compile proves nothing about phone-home:

```bash
strings build/honcochat | grep -E 'mattermost\.(com|io)|ingest.*sentry\.io'   # must be empty
grep -rho 'https://[a-z0-9.-]*mattermost\.\(com\|io\)' server/webapp/channels/dist | sort -u
```

## Speech-to-text is pluggable

`transcribe/engine.conf` selects the engine. Both write `.txt` and `.vtt`, so
nothing downstream knows or cares which ran.

- `whisper` — whisper.cpp + `large-v3-turbo` on the GPU. Current default.
- `vixy` — the in-house model (CTranslate2 / faster-whisper). It returns
  romanised Hinglish rather than Devanagari, which is how these calls are
  actually written. Switch to it when the trained model is ready:

```bash
scp -r <trained-model> ubuntu-3:~/honco-transcribe/vixy-stt-model
sed -i 's/^STT_ENGINE=.*/STT_ENGINE=vixy/' ~/honco-transcribe/engine.conf
```

## Secrets are not in this repo

`.gitignore` keeps them out, but the rule is worth stating plainly: **nothing in
here should ever hold a credential.** Specifically excluded — admin and bot
tokens, `meetsvc.env`, the Jitsi `.env` (it holds every prosody/jicofo/jvb
password), `MEET-CREDENTIALS.txt`, `ADMIN-CREDENTIALS.txt`, the real
`google-services.json`, and any Android keystore.

Losing the Android upload keystore means the app can never be updated on Play
Store under the same package name. Back it up somewhere that is not this repo.

## Licence note

The Mattermost server is AGPL and the mobile app is Apache-2.0. Internal use
carries no obligation, which is why this fork exists. **Keep this repo private:**
publishing it would be distribution, and that is the point at which AGPL terms
actually apply. The same reasoning is why RustDesk stays a separate app instead
of being merged into the mobile client, which ships to public app stores.
