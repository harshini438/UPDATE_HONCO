#!/usr/bin/env python3
"""Honco Chat: two small, targeted session-security fixes on top of upstream
Mattermost, found by audit (not upstream bugs -- deliberate Honco hardening
choices upstream doesn't make by default). Same idempotent pattern as
debrand-residual.py: safe to re-run after an upstream pull, does nothing on
a tree that's already patched.

1. Revoke a user's other sessions after a *self-service* password change.
   Upstream's UpdatePasswordAsUser (channels/app/user.go) changes the
   password and emails a notice, but never revokes existing sessions --
   traced the full call chain (api4.updatePassword -> UpdatePasswordAsUser
   -> UpdatePasswordSendEmail), zero Revoke* calls anywhere in it. A
   password change is often *because* a session/device is suspected
   compromised, so leaving old sessions alive defeats the point. Scoped
   deliberately narrow: only the user's own self-service change (the
   `currentPassword` branch in api4/user.go's updatePassword), not the
   admin-changes-another-user's-password path, which has its own separate,
   already-audited behavior (UpdatePasswordByUserIdSendEmail) that this
   does not touch.

   The current session must survive (the user just proved who they are
   and shouldn't be logged out by their own successful action).
   RevokeAllSessions revokes everything including the current one, and no
   general "recreate a session" function exists in this codebase (checked
   before writing this, not assumed) -- so instead this lists the user's
   sessions (GetSessions, already exists) and revokes each individually
   (RevokeSession, already exists) except the one making this request,
   using only functions already verified present, not inventing a new one.

2. Set SameSite=Lax explicitly on the session/user/CSRF cookies in the
   normal (non-embedded) case. Upstream only ever sets SameSite at all for
   the embedded-iframe case (SameSiteNoneMode); every other cookie relies
   on the browser's own unset-defaults-to-Lax behavior. That already works
   in every current browser, but making it explicit is cheap, safe (Lax
   still allows normal top-level navigation and same-site fetch/XHR, so it
   does not affect local HTTP dev), and turns an implicit assumption into
   a verifiable one.
"""
import os
import re

ROOT = os.path.expanduser("~/honco-chat/server")
changed = []

# --- 1. revoke other sessions on self-service password change --------------
p = os.path.join(ROOT, "server/channels/app/user.go")
src = open(p).read()
orig = src

old_sig = "func (a *App) UpdatePasswordAsUser(rctx request.CTX, userID, currentPassword, newPassword string) *model.AppError {"
marker = "// Honco Chat: revoke other sessions after a self-service password change"
if old_sig in src and marker not in src:
    old_tail = '\treturn a.UpdatePasswordSendEmail(rctx, user, newPassword, T("api.user.update_password.menu"))\n}'
    new_tail = (
        '\tif err := a.UpdatePasswordSendEmail(rctx, user, newPassword, T("api.user.update_password.menu")); err != nil {\n'
        '\t\treturn err\n'
        '\t}\n\n'
        '\t' + marker + ' --\n'
        '\t// the user just proved their identity by supplying the correct current\n'
        '\t// password, so only *other* sessions need to go. List + individually\n'
        '\t// revoke rather than RevokeAllSessions (which would also kill the\n'
        '\t// session making this very request) -- both GetSessions and RevokeSession\n'
        '\t// already exist and are used exactly this way elsewhere in this codebase.\n'
        '\tcurrentSessionId := ""\n'
        '\tif rctx.Session() != nil {\n'
        '\t\tcurrentSessionId = rctx.Session().Id\n'
        '\t}\n'
        '\tif sessions, sessErr := a.GetSessions(rctx, userID); sessErr != nil {\n'
        '\t\trctx.Logger().Warn("Failed to list sessions for password-change revocation", mlog.Err(sessErr))\n'
        '\t} else {\n'
        '\t\tfor _, s := range sessions {\n'
        '\t\t\tif s.Id == currentSessionId {\n'
        '\t\t\t\tcontinue\n'
        '\t\t\t}\n'
        '\t\t\tif revokeErr := a.RevokeSession(rctx, s); revokeErr != nil {\n'
        '\t\t\t\trctx.Logger().Warn("Failed to revoke a session after password change", mlog.Err(revokeErr))\n'
        '\t\t\t}\n'
        '\t\t}\n'
        '\t}\n\n'
        '\treturn nil\n}'
    )
    if old_tail in src:
        src = src.replace(old_tail, new_tail)
        changed.append("channels/app/user.go: revoke-other-sessions-on-password-change")
    else:
        print("WARNING: expected tail of UpdatePasswordAsUser not found -- upstream may have changed, check manually")

if src != orig:
    open(p, "w").write(src)
    print("patched channels/app/user.go")
else:
    print("channels/app/user.go: already patched (or nothing matched -- see warnings above)")

# --- 2. explicit SameSite=Lax on the normal cookie path ---------------------
for rel in ("server/channels/app/login.go", "server/channels/api4/user.go"):
    p = os.path.join(ROOT, rel)
    if not os.path.exists(p):
        continue
    src = open(p).read()
    orig = src
    # Right after the existing embedded-cookie SameSiteNone block, add the
    # explicit Lax default for the normal case. Matches the exact three
    # cookie variable names already used at each of these two call sites.
    pattern = re.compile(
        r'(if secure && utils\.CheckEmbeddedCookie\(r\) \{\n'
        r'(?:\t+\w+\.SameSite = http\.SameSiteNoneMode\n)+'
        r'\t\}\n)'
    )
    m = pattern.search(src)
    if m and "Honco Chat: explicit SameSite=Lax" not in src:
        block = m.group(1)
        cookie_vars = re.findall(r'(\w+)\.SameSite = http\.SameSiteNoneMode', block)
        cond = re.search(r'if (secure && utils\.CheckEmbeddedCookie\(r\)) \{', block).group(1)
        # A standalone `if !(...) { }` block, not `} else {` tacked onto the
        # existing if -- Go requires `else` to be on the same line as the
        # preceding `}` (auto-semicolon-insertion breaks it otherwise), and
        # a comment line in between is exactly the trap that hits. A second,
        # independent if avoids the adjacency rule entirely.
        lax_block = (
            "\t// Honco Chat: explicit SameSite=Lax for the normal (non-embedded) case.\n"
            "\t// Upstream leaves this unset here and relies on the browser's own\n"
            "\t// unset-defaults-to-Lax behaviour; explicit is safer than implicit, and\n"
            "\t// Lax still allows ordinary top-level navigation and same-site\n"
            "\t// fetch/XHR, so local HTTP development is unaffected.\n"
            "\tif !(%s) {\n" % cond
            + "".join("\t\t%s.SameSite = http.SameSiteLaxMode\n" % v for v in cookie_vars)
            + "\t}\n"
        )
        insert_at = m.end(1)
        src = src[:insert_at] + lax_block + src[insert_at:]
    if src != orig:
        open(p, "w").write(src)
        changed.append(rel + ": explicit SameSite=Lax")
        print("patched", rel)
    else:
        print(rel, ": already patched (or pattern not found)")

print("\nchanged:", changed or "(none -- already applied)")
