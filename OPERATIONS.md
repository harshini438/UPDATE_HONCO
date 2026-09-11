# Honco Chat — Operations

How the deployed system is built, started, checked, backed up, recovered
and rolled back. Everything here was exercised on the reference host
(WSL2 Ubuntu-22.04 + Docker Desktop, 2026-09-11); the same commands apply
on a native Linux host, with the WSL-specific notes marked as such.

## 1. What runs where

| Component | Runs as | Listens | Started by |
|---|---|---|---|
| PostgreSQL 16 (`honcochat`) | `harshi`, `$ROOT/pgdata` | `127.0.0.1:5433` | `honcochat.sh start` |
| Mattermost (`build/honcochat`) | `harshi` | `:8065` (all interfaces; local-mode socket `/var/tmp/mattermost_local.socket`, 0600) | `honcochat.sh start` |
| Honco plugin `com.honco.workspace` | inside Mattermost | `/plugins/com.honco.workspace/api/v1/...` | plugin enable |
| meetsvc (`/meet`) | `harshi`, `run/meetsvc.py` | `127.0.0.1:8077` | `honcochat.sh start` |
| Jitsi (web, prosody, jicofo, jvb) | Docker, project `honco-meet` | `:8443` (web), `127.0.0.1:5280`, `:10000/udp` | `meet/meet.sh up` |
| Jibri (recorder) | Docker | `127.0.0.1:2222` (health) | `meet/meet.sh up` |

`$ROOT` is `~/honco-chat`. The Mattermost source checkout is `~/honco-workspace/server`
(also reachable as `$ROOT/server`); its `server/config/config.json` is the live
configuration — **it holds every secret, is `.gitignore`d, and must stay 0600**.

## 2. Build

```sh
export PATH=/usr/local/go/bin:$PATH GOFLAGS=-buildvcs=false

# Mattermost server, with the Honco source patches applied (all idempotent):
cd ~/honco-workspace
python3 chat/branding/harden-session-security.py   # revoke sessions on self-service password change; SameSite
python3 chat/branding/harden-file-deletion.py       # deleted attachments refuse immediately (Phase 8)
cd server/server && go build -p 6 -o ~/honco-chat/build/honcochat.new ./cmd/mattermost
strings ~/honco-chat/build/honcochat.new | grep -E 'mattermost\.(com|io)|ingest.*sentry\.io'   # must print nothing
mv ~/honco-chat/build/honcochat.new ~/honco-chat/build/honcochat

# Honco plugin:
cd ~/honco-workspace/plugins/com.honco.workspace
gofmt -l ./server; go vet ./...; go test -count=1 ./server/...
GOOS=linux GOARCH=amd64 go build -trimpath -o dist/server/dist/plugin-linux-amd64 ./server/
```

Re-run the two `harden-*.py` scripts after every upstream pull; each prints
"already patched" when there is nothing to do and warns if its anchor moved.

## 3. Install / deploy the plugin

```sh
mmctl=~/honco-chat/build/mmctl
# bundle = plugin.json + server/dist/plugin-linux-amd64 + webapp/dist/main.js, tar.gz'd
$mmctl --local plugin add --force com.honco.workspace.tar.gz
$mmctl --local plugin enable com.honco.workspace
$mmctl --local plugin list | grep honco
```

Plugin settings live under `PluginSettings.Plugins.com.honco.workspace` (keys are
stored lower-case): `jibricallbacksecret`, `meetservicesecret`, `meetpublicurl`,
`supportteamname`, `supportchannelname`, `maxrecordingmb`, and the summariser host
settings. Set them with `$mmctl --local config set PluginSettings.Plugins.com.honco.workspace.<key> <value>`.
Schema migrations are the plugin's own (`honco_schema_migrations`, currently 6) and run
on enable; they are additive only.

## 4. Start, stop, status

```sh
cd ~/honco-workspace/chat
./honcochat.sh start      # postgres (if not up) -> mattermost -> meetsvc; writes Jibri's callback host
./honcochat.sh stop       # mattermost + meetsvc (postgres stays up)
./honcochat.sh stop-all   # ...and postgres
./honcochat.sh status
cd ../meet && ./meet.sh up | ps | logs
```

On start the script derives the LAN address for meeting links (`meet/lan-address.sh`)
and writes the address Jibri must call back on (`~/.jitsi-meet-cfg/jibri/honco-chat.host`).
Overrides: `HONCO_LAN_IP` (meeting links), `MEET_BASE`, `HONCO_CALLBACK_HOST`.
Never hardcode an address into a script; both are re-derived every start.

`SiteURL` must match the host the browser uses, or every websocket handshake is
refused (403) and the client appears to load but never updates. It is set from
`HONCO_HOST_IP` at start (`localhost` for the reference host, the LAN or public
hostname in production) and requires a Mattermost restart to change.

## 5. Health

```sh
MEET_DIR=~/honco-meet ~/honco-workspace/chat/healthcheck.sh          # human
MEET_DIR=~/honco-meet ~/honco-workspace/chat/healthcheck.sh --quiet  # cron; exit 1 on any failure
```

Eleven checks: chat server ping, postgres, meetsvc, **plugin enabled**, Jitsi
container and HTTPS answer, Jibri container and its own `HEALTHY` report, and the
three that make up the **recording callback path** — `finalize.sh` visible inside
the Jibri container, its secret file present, and the chat server reachable from
inside the container. The last three exist because of a real failure (see §8).
Nothing in the output is a secret. The Honco Admin dashboard (system admins only)
shows the same probes live plus usage, files and recent failures.

## 6. Backup and restore

```sh
~/honco-workspace/chat/backup-honcochat.sh backup    # daily from cron; keeps 14
~/honco-workspace/chat/backup-honcochat.sh list
~/honco-workspace/chat/backup-honcochat.sh restore <dump> [newdbname]   # restores into a NEW database
```

Each backup set is three files in `$ROOT/backups`:

| File | Contents |
|---|---|
| `honcochat-<stamp>.dump` | `pg_dump -Fc` of `honcochat` (all Mattermost + all `honco_*` tables) |
| `honcochat-<stamp>.state.tar` | meetsvc's pending reminders and tokens |
| `honcochat-<stamp>.files.tar.gz` (**0600**) | `run/data` (every attachment and recording), `run/plugins` + `run/client-plugins` (the installed plugin), `server/server/config/config.json`, `~/.jitsi-meet-cfg` (Jitsi/Jibri config), `~/.honco-jibri-secret`, `~/.honco-meet-service-secret` |

`restore` refuses to target the live database by design: restore into a new name,
verify, then stop the app, swap the database names (or `MM_SQLSETTINGS_DATASOURCE`),
and start. Restore the files archive to `$ROOT` and `$HOME` with `tar -xzf`, as the
same user. Keep the backups directory off-box; that sync is deliberately not this
script's job. **A destructive restore was not run against the live database** as part
of verification; the dump was verified restorable (`pg_restore -l`, 94 tables).

## 7. Rollback

| What | How |
|---|---|
| Mattermost binary | previous binaries are kept beside the live one: `build/honcochat.pre-file-hardening-<stamp>`, `honcochat.pre-auth-audit`, `honcochat.vanilla-backup`. `stop`, copy the one you want to `build/honcochat`, `start`. |
| Plugin | `mmctl --local plugin add --force <previous bundle>`; or `plugin disable com.honco.workspace` to take it out entirely (schema stays; it is additive). |
| Configuration | `config.json` is in every files archive; or set individual keys back with `mmctl --local config set`. |
| Database | restore a dump into a new database and swap (§6). |
| Jibri hook | the previous hook is kept as `~/.jitsi-meet-cfg/jibri/finalize.sh.bak-*` (0600, root); the versioned one is `meet/jibri-finalize.sh`. |

Git: every phase is one commit on `main`; `git revert <hash>` undoes a phase without
touching data.

## 8. Failure recovery — verified behaviours

| Event | What happens | What to do |
|---|---|---|
| Mattermost restart | plugin reloads with it; sessions survive (DB-backed) | `honcochat.sh stop && honcochat.sh start` |
| Plugin restart | `plugin disable` / `enable`; no data loss; card and notification dedupe hold across it (tested) | as needed |
| meetsvc restart | pending reminders persist in `run/meet-schedule.json` | `honcochat.sh stop && start` |
| Jibri restart | re-registers with Prosody on start; in-flight recording lost | `meet.sh up` recreates only what changed |
| Prosody recreated | Jibri's registration is dropped and Jicofo does not re-detect it ("Unable to find an available Jibri") | restart jibri **then** jicofo |
| PostgreSQL unavailable | Mattermost returns 500s; plugin health shows PostgreSQL down; reconnects when back | bring postgres back; no restart of Mattermost needed |
| Jitsi unavailable | Join links fail; admin health shows Jitsi unavailable; recordings impossible | `meet.sh up` |
| Recording produced no media | Jibri does not run the hook; the meeting card never gains a recording | expected Jibri behaviour |
| Recording over the size ceiling | refused with 413, recorded as failed with the reason, announced in the channel | raise `maxrecordingmb` knowingly (RAM ×2) |
| Recording file removed later | card shows "Recording unavailable"; admin Files diagnostic counts it | — |

### Docker Desktop restart (WSL hosts) — known issue

After Docker Desktop is restarted **from Windows**, Jibri's `/config` bind mount can
resolve inside the docker-desktop VM instead of the Ubuntu distro. The container
comes up "healthy", records, then finalize fails with
`Cannot run program "/config/finalize.sh": No such file or directory` and **no
recording reaches Honco Chat**. `healthcheck.sh` catches it (`finalize.sh visible
in container`). Recovery, from inside the distro:

```sh
cd ~/honco-meet
docker compose -f docker-compose.yml -f jibri.yml -f docker-compose.override.yml up -d --force-recreate --no-deps jibri
docker exec honco-meet-jibri-1 test -x /config/finalize.sh && echo ok
```

No volume or image is touched. Jibri re-registers by itself.

### WSL interop lost — known issue

Docker Desktop's binfmt registrations can displace WSL's own, after which
`powershell.exe` fails with `Exec format error` from inside the distro and
`lan-address.sh` cannot ask Windows for the LAN address. Symptom: a start logs
"could not derive a LAN address" and meeting links fall back to localhost.
`lan-address.sh` now falls back to the last address it derived
(`run/lan-address.last`) and says so; the real fix is to re-register interop
from Windows:

```
wsl.exe -d Ubuntu-22.04 -u root sh -c 'echo ":WSLInterop:M::MZ::/init:PF" > /proc/sys/fs/binfmt_misc/register'
```

## 9. Secrets

| Secret | Where it lives | Rotation |
|---|---|---|
| Jibri callback secret | `~/.honco-jibri-secret` (0600) and, for the hook, `~/.jitsi-meet-cfg/jibri/honco-callback.secret` (0600, jibri uid); plugin setting `jibricallbacksecret` | generate `openssl rand -hex 32`, write all three, no restart needed |
| Meet service secret | `~/.honco-meet-service-secret`; `run/meetsvc.env`; plugin setting `meetservicesecret` | same pattern; restart meetsvc |
| SMTP password | `config.json` only | System Console / `mmctl config set` |
| Summariser SSH key | on disk, path in plugin settings; never read by the plugin | — |
| AI callback secret | `~/.honco-ai-callback-secret` (0600); plugin setting `aicallbacksecret`; also held by the AI service | generate `openssl rand -hex 32`, set both sides, no restart needed |
| AI service token | plugin setting `aiservicetoken` only (the browser never sees it) | rotate on the service, then `mmctl config patch` |

The Jibri hook (`meet/jibri-finalize.sh`) is version-controlled and carries **no
secret**; it reads the secret from the mounted file at run time. The previous hook
embedded the secret, which is why it could be neither committed nor backed up; that
secret was rotated on 2026-09-11 when the hook was replaced.

Nothing logs a secret. Verified by scanning the server log for every live secret
value, session token and bearer token (0 occurrences). Plugin responses report
secrets as configured/not-configured only.

## 10. Security configuration — decisions, not defaults

Set (with reasons in the Phase 9 commit): `TerminateSessionsOnPasswordChange=true`,
`EnableSentry=false`, `EnableDiagnostics=false`, `EnableRemoteMarketplace=false`,
`ConsoleLevel=INFO`, `EnableWebhookDebugging=false`. Unchanged and intentional:
`EnablePublicLink=false`, `EnableOpenServer=false` (invite-only), `MaxFileSize=100 MB`,
`RequirePluginSignature=false` (our plugin is unsigned and installed by upload),
`EnableLocalMode=true` (socket is 0600; `mmctl --local` depends on it).

**Mattermost's global rate limiter (`RateLimitSettings.Enable`) is off.** Behind the
Cloudflare tunnel every request arrives from one source address, so enabling it as
shipped (`VaryByRemoteAddr`) would throttle all users as one. Enable it only together
with `ServiceSettings.TrustedProxyIPHeader: ["CF-Connecting-IP"]` (and the tunnel in
front), or with `VaryByUser`. The plugin has its own limiter regardless (per user for
its routes; per address for the two secret-authenticated service routes).

**Session length** is 180 days web/mobile with a 30-day idle timeout; Mattermost's
default is 30 days. Reducing it is a product decision — it logs people out — and is
recommended for production, not made here.

**TLS** is terminated outside Mattermost (`ConnectionSecurity=""`): by the Cloudflare
tunnel in production, by nothing on the LAN. Do not expose `:8065` beyond the LAN
without a TLS terminator in front.

### AI Assistant — an external service

The assistant displays what a **separate, teammate-owned** AI service produces
(voice recognition, live transcript, suggestions, post-call summary). Honco runs
none of that. Three settings, none of which have a default:

```bash
MMCTL=~/honco-chat/build/mmctl
umask 077; openssl rand -hex 32 > ~/.honco-ai-callback-secret   # give this to the service owner
P=$(mktemp)
printf '{"PluginSettings":{"Plugins":{"com.honco.workspace":{"aiserviceurl":"https://ai.example.internal","aiservicetoken":"<token from the service>","aicallbacksecret":"%s"}}}}' \
  "$(cat ~/.honco-ai-callback-secret)" > "$P"
$MMCTL --local config patch "$P"; rm -f "$P"
```

`mmctl config set` cannot create a plugin key that does not exist yet — use
`config patch` the first time, `set` afterwards.

- Unset `aiserviceurl` ⇒ Honco makes no outbound AI calls and the panel says
  "not configured"; it never pretends to be connected.
- Unset `aicallbacksecret` ⇒ every push from the service is rejected (fails
  closed).
- The service pushes to `{SiteURL}/plugins/com.honco.workspace/api/v1/ai/events`
  with `X-Honco-AI-Secret`. Honco also sends that URL to the service as
  `callback_url` when a meeting starts.
- Honco Administration (the plugin's Admin tab) shows whether each is set and
  whether the service answered its health probe — never a value.

Full contract, bounds and security notes: `AI_INTEGRATION.md`.

## 11. Cloudflare, Jitsi and RustDesk boundaries

- `honcochat.sh publish` brings up a **quick** tunnel, which issues a new hostname on
  every restart; a stable address needs a named tunnel on the Cloudflare account.
  `SiteURL` must be set to that hostname or websockets fail.
- Jitsi is reached directly on `:8443` with a self-signed certificate on the LAN;
  the recording path from Jibri to Honco Chat is container → WSL eth0 (§4), never
  through the tunnel.
- RustDesk: Honco manages the support workflow only. The RustDesk relay/clients are
  a separate deployment; no RustDesk credential is stored, and there is no API
  integration — the boundary is documented in the Remote Support report.
