# honco.in → Cloudflare: exact records to recreate

Snapshot taken **2026-09-02** authoritatively from `ns75.domaincontrol.com`,
before any change. This is the safety net: Cloudflare's auto-import is good but
it routinely misses **SRV** records, and this zone's DMARC is `p=reject`, so an
SPF or MX slip does not send mail to spam — it gets the mail **rejected outright**.

## Every record that exists today

| Type | Name | Value | Proxy |
|---|---|---|---|
| A | `@` | `13.127.206.154` | **DNS only (grey cloud)** |
| A | `www` | `13.127.206.154` | **DNS only (grey cloud)** |
| CNAME | `email` | `email.secureserver.net` | DNS only |
| MX | `@` | `smtp.secureserver.net` priority **0** | n/a |
| MX | `@` | `mailstore1.secureserver.net` priority **10** | n/a |
| TXT | `@` | `v=spf1 include:secureserver.net -all` | n/a |
| TXT | `_dmarc` | `v=DMARC1; p=reject; rua=mailto:dmarc_rua@onsecureserver.net; adkim=r; aspf=r;` | n/a |
| SRV | `_autodiscover._tcp` | priority 0, weight 0, port 443, target `autodiscover.secureserver.net` | n/a |

That is the whole zone. No AAAA, no CAA, no DKIM selector at any of the twelve
common names I probed (`default`, `selector1/2`, `s1/s2`, `k1/k2/k3`, `google`,
`mail`, `dkim`, `smtp`, `godaddy`), as either TXT or CNAME.

### Two things to get right

1. **Keep `@` and `www` on DNS-only (grey cloud) for the first switch.** Turning
   the orange cloud on changes how the origin at `13.127.206.154` is reached and
   needs that origin to accept Cloudflare's IPs with a valid certificate. Migrate
   first, verify, then decide about proxying separately.
2. **Add the SRV record by hand.** Cloudflare's scan often skips it, and losing it
   breaks Outlook/mail-client autodiscovery for everyone on the domain.

### Worth telling the mail admin, separately from this migration

There is no DKIM record on the domain, yet DMARC is set to `p=reject` with
relaxed alignment. Today that means mail passes on SPF alignment alone — any
forwarding path that breaks SPF gets the message rejected, not quarantined. Not
caused by this migration, and not something to change during it, but it is worth
someone's attention afterwards.

## The order to do it in

1. In Cloudflare, add `honco.in` as a zone. Let the scan run.
2. **Compare what it imported against the table above.** Add anything missing,
   especially the SRV. Set `@` and `www` to DNS-only.
3. Lower TTLs if you want a fast rollback window, and only then change the
   nameservers at GoDaddy to the two Cloudflare gave you.
4. Propagation is usually minutes to a couple of hours. Run the verify script
   below against the Cloudflare nameservers *before* the switch, and against the
   public resolvers after.
5. Only once mail and the website are confirmed working: create the named tunnel
   and the `chat` record.

## Verifying

`verify-honco-dns.sh` (next to this file) re-queries every record above and
diffs it against this snapshot. Run it against Cloudflare's nameservers as soon
as the zone exists — that catches a missing record *before* the nameserver
change, which is the only cheap moment to catch it.

```bash
./verify-honco-dns.sh                      # public resolvers (after the switch)
./verify-honco-dns.sh ada.ns.cloudflare.com   # Cloudflare NS (before the switch)
```

## What comes after: chat.honco.in

Once the zone is live on Cloudflare, on **ubuntu3**:

```bash
~/honco-chat/bin/cloudflared tunnel login          # prints a URL; authorise the zone in a browser
~/honco-chat/bin/cloudflared tunnel create honcochat
~/honco-chat/bin/cloudflared tunnel route dns honcochat chat.honco.in
```

That writes `~/.cloudflared/<tunnel-id>.json` and adds a proxied CNAME for
`chat` pointing at `<tunnel-id>.cfargotunnel.com`. The tunnel then replaces the
quick tunnel in `honcochat.sh`, and `SiteURL` becomes `https://chat.honco.in`
permanently instead of a new random hostname on every restart.

`cloudflared tunnel login` needs a browser and your Cloudflare account, so that
first command is yours to run.
