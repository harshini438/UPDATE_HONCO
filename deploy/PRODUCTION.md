# Honco Chat — production deployment

What an administrator must do to put Honco Chat on a public HTTPS
hostname, and what each step depends on. Written for whoever runs the
deployment, not for whoever wrote the code.

`OPERATIONS.md` covers running the system day to day. This covers the
one-time move from `http://localhost:8065` to `https://chat.honco.in`.

Nothing here has been executed: `honco.in` is still on GoDaddy
nameservers, `chat.honco.in` does not resolve, and no Cloudflare
credential exists on the build host. Every command below is written to be
run by someone who has that access.

## The architecture this produces

```
browser ──HTTPS──> Cloudflare edge ──named tunnel──> cloudflared
                                                        │
                                              http://127.0.0.1:8065
                                                        │
                                              Honco Chat (Mattermost)
                                                        │
                                              127.0.0.1:5433 PostgreSQL
```

PostgreSQL, Prosody, Jicofo, JVB, Jibri, the Workspace backend and its
database stay on loopback and are never published. The tunnel maps
exactly one origin.

## Order of operations

The order matters. Doing 5 before 3 leaves `SiteURL` pointing at a
hostname that does not resolve, and every websocket handshake fails.

### 1. Delegate the zone

Follow `dns/honco.in-to-cloudflare.md`. Two traps it documents and this
one repeats because they are expensive:

- **Add the `_autodiscover._tcp` SRV record by hand.** Cloudflare's zone
  scan routinely misses SRV, and losing it breaks mail-client
  autodiscovery for the whole domain.
- The zone's DMARC is `p=reject` with no DKIM. An SPF or MX mistake does
  not send mail to spam, it gets mail **rejected**.

Verify with `dns/verify-honco-dns.sh` against Cloudflare's nameservers
*before* changing them at the registrar. That is the only cheap moment to
catch a missing record.

### 2. Create the named tunnel

On the host that will be the origin — the machine actually running Honco
Chat, since the tunnel connects to `127.0.0.1:8065`:

```bash
cloudflared tunnel login                       # browser, authorises the zone
cloudflared tunnel create honcochat            # writes ~/.cloudflared/<uuid>.json
cloudflared tunnel route dns honcochat chat.honco.in
chmod 600 ~/.cloudflared/cert.pem ~/.cloudflared/*.json
```

Copy `deploy/cloudflared/config.yml.example` to `~/.cloudflared/config.yml`
and fill in the tunnel id. **Neither `cert.pem` nor `<uuid>.json` may be
committed** — both are ignored by `.gitignore`'s `*.pem` and credential
rules, but the check is worth doing before the first commit after
deployment.

A quick tunnel (`cloudflared tunnel --url`) is a diagnostic tool only. It
issues a new hostname on every restart, so `SiteURL` cannot be pinned to
it and it must never be the production answer.

### 3. Start the tunnel, with SiteURL still local

```bash
cloudflared tunnel run honcochat
curl -sS -o /dev/null -w '%{http_code} %{ssl_verify_result}\n' \
  https://chat.honco.in/api/v4/system/ping     # expect: 200 0
```

At this point the browser client will load but websockets will still be
refused with 403. That is expected — it is step 4 that fixes it, and
seeing the 403 first confirms the origin check is working.

### 4. Set SiteURL, then restart

`SiteURL` is supplied as an environment variable by `chat/honcochat.sh`
(`MM_SERVICESETTINGS_SITEURL`), so it is read at process start and is
read-only in the System Console. Changing it **requires a restart** —
which is why the tunnel comes up first.

`site_url()` resolves in one of two ways:

```bash
site_url() {
    if [ -s "$URLFILE" ]; then cat "$URLFILE"; else echo "http://${HOST_IP}:${PORT}"; fi
}
```

`$URLFILE` is `run/public-url.txt`. It exists because the quick tunnel
wrote its fresh hostname there on every start; a named tunnel's hostname
is stable, so it is written once, by hand:

```bash
echo 'https://chat.honco.in' > ~/honco-chat/run/public-url.txt
~/honco-chat/chat/honcochat.sh stop
~/honco-chat/chat/honcochat.sh start
~/honco-chat/chat/honcochat.sh status     # prints SiteURL; confirm it
```

`run/public-url.txt` is host-specific live config and is already excluded
by `.gitignore`. Setting `HONCO_HOST_IP` instead only changes the *host*
in an `http://host:8065` URL — it cannot produce an `https://` one, so it
is not the mechanism for this step.

Verify a real websocket, not an HTTP 200 — a 200 proves nothing about the
Upgrade path:

```bash
curl -sS -i -o - --max-time 10 \
  -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
  -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
  -H 'Origin: https://chat.honco.in' \
  https://chat.honco.in/api/v4/websocket | head -1   # expect: HTTP/1.1 101
```

### 5. Harden the origin

Once the tunnel fronts the server, three changes become correct that were
not before. None is safe to make earlier.

**Bind to loopback.** `ListenAddress` is `:8065`, so the server is
currently reachable across the LAN in plaintext. cloudflared connects
locally, so the origin no longer needs a routable interface:

```
MM_SERVICESETTINGS_LISTENADDRESS=127.0.0.1:8065
```

Note this also cuts off LAN browser access, which is how the system is
reached today — make it in the same window as step 4, not before.

**Trust Cloudflare's client-IP header.** Behind a tunnel every request
arrives from one address:

```
ServiceSettings.TrustedProxyIPHeader: ["CF-Connecting-IP"]
```

**Only then, rate limiting.** `RateLimitSettings.Enable` is off
deliberately. Turning it on as shipped (`VaryByRemoteAddr`) with every
request arriving from the tunnel would throttle all users as one. Enable
it only *after* the header above, or with `VaryByUser`. The Honco plugin
has its own per-user limiter regardless, so this is defence in depth
rather than the only control.

### 6. Check what the change touched

`SiteURL` is read by more than the browser:

| Reads SiteURL | Effect of the change |
|---|---|
| Websocket origin check | 403 → 101. The reason for step 4. |
| Password reset and notification emails | Links stop pointing at `localhost` and start working off-host. |
| Honco permalinks (`notify.go`) | Task and summary notifications link correctly. |
| AI Assistant `callback_url` (`ai_adapter.go`) | Only then can an off-host AI service reach Honco. |
| Admin dashboard | Displays the new value. |

Jibri's recording callback does **not** use SiteURL — it reaches the
server over the container network (`OPERATIONS.md` §4), so recordings are
unaffected by this change.

## Health checks

`GET /plugins/com.honco.workspace/api/v1/admin/health` (system admin
only) probes, in parallel: Honco Chat, PostgreSQL, the plugin, Jitsi,
Jibri, the meeting service, **the summariser** and **the AI service**.

The last two are reachability probes. They open a connection and close
it; they do not authenticate and cannot leak a credential. A `healthy`
summariser means the port answered — it does **not** mean the SSH key is
authorised, and the dashboard says so in as many words.

For an uptime monitor, `GET /api/v4/system/ping` is unauthenticated and
cheap.

## What is still external

Deployment is not finished by this document alone. These are other
people's to supply:

| Dependency | Needed for | Owner |
|---|---|---|
| Cloudflare account + zone delegation | everything above | domain owner |
| Summariser route to `192.168.2.0/24` + SSH key | Meeting Intelligence, `/summarize`, `/action-items` | network + summariser owner |
| AI service URL + token | AI Assistant live features | the service owner |
| Public Jitsi hostname + real certificate | meetings for anyone off the LAN | Jitsi owner |
| Push proxy + APNs/FCM | mobile push | push owner |
| RustDesk hbbs/hbbr + clients | actual remote control | infrastructure owner |

Publishing Honco Chat does **not** publish meetings: `meetpublicurl` is
`https://192.168.1.11:8443`, a private address with a self-signed
certificate. An external user will log in successfully and then find the
join link unreachable. That is a separate deployment, not a
configuration toggle.
