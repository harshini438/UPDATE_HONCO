# Honco Chat — the fork, and how to re-apply it

In-house chat for Honco, forked from Mattermost. Self-hosted, internal use only,
never sold. Everything lives on **ubuntu3** at `~/honco-chat`.

```
~/honco-chat/
  server/       fork of mattermost/mattermost   (chat server + web UI)
  mobile/       fork of mattermost-mobile       (iOS + Android)
  push-proxy/   fork of mattermost-push-proxy   (our own push, not the vendor's)
  branding/     the de-brand scripts -- re-runnable, see below
  build/        honcochat binary
  run/          config, logs, uploads for the running instance
  pgdata/       Postgres 16.4 data directory (user-owned, port 5433)
```

## Re-applying after an upstream pull

The de-brand is **scripted and idempotent** on purpose. A hand-done `sed` pass
would have to be redone by hand every time we pull upstream; these do not.

```bash
cd ~/honco-chat/server && git pull                 # or fetch + merge
cd ~/honco-chat
./branding/debrand-server.sh                       # non-free code + phone-home
python3 branding/patch_push.py                     # hosted push endpoints
python3 branding/debrand-sentry.py                 # crash reporter
python3 branding/debrand-support.py                # our own support links
python3 branding/debrand-polish.py                 # product name in translations
python3 branding/debrand-residual.py               # strings-check leftovers (see below)
./branding/debrand-mobile.sh                       # app identity
python3 branding/debrand-mobile-sentry.py          # mobile crash reporter

# rebuild + prove it
export PATH=$HOME/toolchain/go/bin:$PATH GOFLAGS=-buildvcs=false
cd server/server && go build -p 6 -o ~/honco-chat/build/honcochat ./cmd/mattermost
strings ~/honco-chat/build/honcochat | grep -E 'mattermost\.(com|io)|ingest.*sentry\.io'
# ^ must print nothing
```

## What was removed, and why it mattered

These were real outbound network calls, not cosmetics.

| Removed | What it did |
|---|---|
| `security_update_check.go` | Every 24h, sent server id, build, DB type, OS, and **user / team / active-user counts** to the vendor. |
| **`var SentryDSN`** in `channels/app/server.go` | A hardcoded DSN pointing at the vendor's own Sentry org, plus an HTTP middleware wrapping every request. Quiet only because our service environment is `dev` — **a production build would have reported to the vendor.** This was the most important find. |
| `upgrader_linux.go` | Downloaded a tarball from `releases.mattermost.com` and replaced the running binary in place. |
| Notices feed, plugin marketplace, cloud/CWS URLs | Vendor endpoints in the config defaults. |
| `MHPNS*` push endpoints | The vendor's hosted push service. We run our own proxy instead. |
| `server/enterprise/` | Source-Available (non-free) licence, not AGPL. Gated behind build tags we never set, so deleting it changes no behaviour — it just keeps non-free code out of our tree. Only the package placeholder remains. |
| `@sentry/react-native` | The mobile app's only third-party telemetry SDK. |
| `"feedback@mattermost.com"` literal in `email.go`'s `SendEmailChangeVerifyEmail` | Not a network call, but a real user-facing leak: the email-change verification email told users to contact the vendor for support instead of Honco. Every other email in the same file correctly reads the configured support address; this one line didn't. Caught by `debrand-residual.py`, which the `strings` check surfaces — see below. |

Support / About / Help / Terms / Privacy / app-download links are **not** blanked
— they point at Honco's own URLs, all derived from one `HONCO_BASE` variable in
`branding/debrand-support.py`.

## Four traps, all hit for real

1. **Upstream does not build out of the box.** `go.mod` pins
   `server/public v0.4.3` from the module proxy while `channels/` needs symbols
   that only exist in the local `public/` tree. The errors name missing `model.*`
   symbols and look exactly like a broken de-brand. They are not. The fix is
   upstream's own `make setup-go-work`:
   ```bash
   cd server/server && go work init && go work use . && go work use ./public
   ```

2. **Never rebrand inside `{{ ... }}`.** A blanket `Mattermost → Honco Chat`
   replace across the translation files rewrote template *identifiers* —
   `{{.MattermostUsername}}` became `{{.Honco ChatUsername}}` — and the server
   refused to boot with `function "ChatUsername" not defined`.
   `debrand-polish.py` now parses the JSON, rewrites only `translation` values,
   skips every `{{...}}` span, and asserts afterwards that no template action
   contains the brand name.

3. **Never rename Go import paths or the Android `namespace`.**
   `github.com/mattermost/...` is a module path, not branding; renaming it breaks
   every build. Same for the Android `namespace`, which is the Java package of
   the sources on disk. Only `applicationId` (what the store and device see) was
   changed, to `com.honco.chat`.

Related: removing the Sentry HTTP middleware also forces removing the
`sentryhttp` import, or Go refuses to compile on an unused import.

4. **`debrand-server.sh` itself used to commit trap 2.** Its old step 7 ran a
   blanket `sed 's/Mattermost/Honco Chat/g'` over `server/i18n/en.json`
   *before* `debrand-polish.py` got a chance to run its template-aware
   version — so `{{.MattermostUsername}}` became `{{.Honco ChatUsername}}`
   and the server refused to boot, exactly as trap 2 describes, except the
   naive replace was baked into the "safe" script. Fixed by deleting that
   step outright: `debrand-polish.py` already handles translation strings
   correctly and runs later in the documented order, so there is nothing for
   step 7 to do. If you ever see `BROKEN TEMPLATE` in `debrand-polish.py`'s
   output, this is almost certainly why — check `debrand-server.sh` hasn't
   grown another blanket `sed` over an i18n file.

## Toolchain on ubuntu3 (no sudo, no docker there)

Everything unpacks into `~/toolchain` as a normal user:

| Tool | Why |
|---|---|
| Go **1.26.7** | `go.mod` requires exactly this line; 1.24 will not build it. |
| Node **24** + npm 11 | the webapp's `engines` field demands `^24` / `^11`. |
| Postgres **16.4** | only the *client* is installed system-wide. Server binaries come from the Zonky `embedded-postgres-binaries` jar on Maven Central (EnterpriseDB's tarballs 403). That jar ships only `initdb`, `pg_ctl` and `postgres` — use the system `psql` / `createdb` against it. |

Running Postgres:
```bash
PGBIN=$HOME/toolchain/pg16/bin
$PGBIN/pg_ctl -D ~/honco-chat/pgdata -l ~/honco-chat/logs/pg.log \
  -o "-p 5433 -k $HOME/honco-chat/pgsock -c listen_addresses=127.0.0.1" -w start
```

One-time role setup, right after `initdb` and before the app ever connects:
```bash
$PGBIN/createuser -h 127.0.0.1 -p 5433 honco
$PGBIN/createdb   -h 127.0.0.1 -p 5433 -O honco honcochat
# CREATEDB, not superuser: needed only so backup-honcochat.sh's `restore`
# subcommand can restore into a scratch database to verify a dump, without
# ever touching the live one or needing a separate admin role.
$PGBIN/psql -h 127.0.0.1 -p 5433 -d postgres -c "ALTER ROLE honco CREATEDB;"
```

## Wiring up /meet (one-time, per deployment)

`honcochat.sh` starts meetsvc.py automatically once `run/meetsvc.env` exists,
but creating the bot account and the slash command itself isn't scripted --
it's a one-time `mmctl` setup, done with the server already running:

```bash
MMCTL=~/honco-chat/build/mmctl   # build: go build -o build/mmctl ./cmd/mmctl

# Bot creation is blocked over the --local socket (Mattermost restricts it
# deliberately), so it needs one real login. A throwaway system-admin
# account works and can be deleted immediately after -- the bot it creates
# is independent and persists.
$MMCTL --local user create --email automation@honco.in --username honco-bootstrap \
    --password '<random, used once>' --system-admin --email-verified
$MMCTL auth login http://127.0.0.1:8065 --name bootstrap \
    --username honco-bootstrap --password-file <(echo '<same password>')

$MMCTL bot create honco-meet --display-name "Honco Meet" \
    --description "Posts /meet room links and reminders" --with-token --json
# -> save the "token" field as BOT_TOKEN

$MMCTL command create <team-name> --title "Honco Meet" --trigger-word meet \
    --url http://127.0.0.1:8077/ --creator honco-bootstrap \
    --response-username "Honco Meet" --autocomplete --post --json
# -> save the "token" field as CMD_TOKEN

# The bot needs real team membership -- posting the slash command's own
# immediate response works without it (Mattermost posts that itself), but
# meetsvc's scheduler posts reminders through a separate, independent API
# call using BOT_TOKEN, which 403s until the bot is actually a team member.
# This is the one step it's easy to skip and only notice when the first
# scheduled reminder silently fails.
$MMCTL --local team users add <team-name> honco-meet

$MMCTL --local user delete honco-bootstrap --confirm   # bootstrap account, not needed again
```

Then:
```bash
echo "BOT_TOKEN=<token>" > ~/honco-chat/run/meetsvc.env
echo "<cmd-token>" > ~/honco-chat/run/.cmd-token
chmod 600 ~/honco-chat/run/meetsvc.env ~/honco-chat/run/.cmd-token
./honcochat.sh start   # picks up meetsvc.env, starts meetsvc.py
```

Verify with the health check (`healthcheck.sh`) or by executing a command
through the real API rather than curling meetsvc.py directly -- the two most
likely failure modes (bad CMD_TOKEN, bot not on the team) only show up that
way:
```bash
curl -s -X POST http://127.0.0.1:8065/api/v4/commands/execute \
  -H "Authorization: Bearer <a real session token>" -H 'Content-Type: application/json' \
  -d '{"channel_id":"<id>","command":"/meet test"}'
```

## Backups

`backup-honcochat.sh` (`chat/`) does a `pg_dump -Fc` of `honcochat` plus a
small tarball of the runtime state that isn't in the database — pending
`/meet` reminders, the meetsvc tokens — pruning to the newest 14 by default.
Run it daily from cron; see the comment at the bottom of the script for the
exact line. `backup-honcochat.sh restore <dump> [name]` restores into a new,
separate database (refuses to target the live one), so a restore is always a
side-by-side verify-then-cut-over, never a blind overwrite. Both directions
are exercised in CI (see `.github/workflows/`).

## Verified state

- Server builds clean; `strings` on the binary returns **zero** vendor URLs and
  no Sentry DSN.
- Boots against Postgres, migrates **85 tables**, listens on `:8065`.
- `GET /api/v4/system/ping` → `status: OK`.
- `GET /api/v4/config/client` reports `SiteName: Honco Chat` and every support
  link on `chat.honco.in`.
- Zero mentions of the crash reporter in the boot log.

A clean compile proves nothing about phone-home. The `strings` check and the
client-config read-back are what actually prove it.

## The web client

Built with Node 24 (`npm ci && npm run build` in `server/webapp`), output in
`channels/dist`, symlinked into `run/client`. Three things worth knowing:

- **npm 11 blocks install scripts by default.** `npm ci` succeeds but silently
  skips 10 packages, including `@swc/core` (needed to compile) and
  `react-bootstrap`'s `patch-package` postinstall, which applies the four
  patches in `webapp/patches/`. Run `npm install-scripts approve --all` and
  `npm ci` again, or the build is subtly wrong.
- **Do not blanket-replace "Mattermost" in the webapp.** 4099 source files
  mention it, nearly all as `@mattermost/*` import paths or copyright headers.
  The translated strings are already handled by `debrand-polish.py` and reach the
  bundle (English is inlined into the main JS chunk, not a separate i18n file).
- **The static shell needs two separate fixes**, both in `debrand-webapp.py`:
  `channels/src/root.html` for `<title>` and `application-name`, and the
  `WebpackPwaManifest` block in `channels/webpack.config.js`, which is what
  actually injects the `apple-mobile-web-app-title` meta tag and writes
  `manifest.json`. Patching root.html alone leaves the vendor name in the served
  page.

## Not done yet

- **Logos and favicons are still the vendor's.** `channels/src/images/logo*.png|svg`
  and `channels/src/images/favicon/` need real Honco artwork; nothing here can
  invent it.
- **Push notifications** — need our own push-proxy running with Honco's Firebase
  (FCM) and Apple (APNs) keys. The vendor's push service is gone, so notifications
  stay dead until those exist.
- **Meetings / recording / AI notes / remote desktop** — separate systems, see the
  architecture note. Planned for ubuntu-3, which needs root first: it has no
  sudo, no docker, no java, and no `newuidmap` (so rootless containers are out
  too).
