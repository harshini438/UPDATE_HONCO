# De-branding checklist (Honco Chat)

Configuration that must hold on any de-branded Honco Chat instance. These are
runtime settings on the Mattermost server; verify them after every deploy or
config restore, because the container deployment and the bare-metal runbook
(`chat/honcochat.sh`) can drift apart.

## Account creation (security)
- `TeamSettings.EnableOpenServer` = **false** — no open self-registration.
  (Root cause of drift: the Docker instance does not source `chat/honcochat.sh:48`,
  which sets this correctly for bare-metal.)
- Optionally `TeamSettings.RestrictCreationToDomains` = `honco.in` as a second
  line of defence.

Verify:
```
mmctl --local config get TeamSettings.EnableOpenServer          # -> false
curl -s -o /dev/null -w '%{http_code}\n' -X POST \
  http://127.0.0.1:8065/api/v4/users -H 'Content-Type: application/json' \
  -d '{"email":"x@outsider.test","username":"x","password":"Intruder#2026x"}'   # -> 403
```

## Vendor plugins (de-branding)
All Mattermost-vendor plugins stay **disabled**; only `com.honco.workspace` is enabled.
- `PluginSettings.PluginStates.com.mattermost.nps.Enable` = **false**
  (the NPS survey collects satisfaction feedback for the vendor and shows
  vendor-branded surveys — both removed during de-branding).
- Also confirm `com.mattermost.calls`, `mattermost-ai`, `playbooks`, `github`
  are not enabled.

Verify:
```
mmctl --local config get PluginSettings.PluginStates.com.mattermost.nps   # -> {"Enable": false}
mmctl --local plugin list                                                 # -> only com.honco.workspace
```
