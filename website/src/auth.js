// Auth glue for the public website's login / signup / password pages.
//
// Every call goes to the REAL Honco Chat (Mattermost) REST API on the SAME
// ORIGIN (the local reverse proxy puts the website and Mattermost on one
// origin, so Mattermost's HttpOnly MMAUTHTOKEN cookie is set and sent
// automatically). Nothing here stores a token in localStorage, sessionStorage
// or IndexedDB — the cookie is the only session, exactly as Mattermost and
// the workspace already use it. There is no second user store and no JWT.

// Mattermost requires the X-CSRF-Token header (matching the non-HttpOnly
// MMCSRF cookie) on cookie-authenticated API calls. The login POST itself is
// unauthenticated and does not need it; sending it there is harmless.
function csrfToken() {
    const m = document.cookie.match(/(?:^|;\s*)MMCSRF=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
}

function headers(json) {
    const h = {'X-Requested-With': 'XMLHttpRequest'};
    if (json) h['Content-Type'] = 'application/json';
    const t = csrfToken();
    if (t) h['X-CSRF-Token'] = t;
    return h;
}

async function api(method, path, body) {
    const res = await fetch(path, {
        method,
        credentials: 'include', // send/receive the Mattermost session cookie
        headers: headers(true),
        body: body ? JSON.stringify(body) : undefined,
    });
    let data = null;
    try { data = await res.json(); } catch (_) { /* empty body */ }
    return {ok: res.ok, status: res.status, data};
}

async function currentUser() {
    try {
        const res = await fetch('/api/v4/users/me', {credentials: 'include', headers: headers(false)});
        return res.ok ? await res.json() : null;
    } catch (_) { return null; }
}

// After authentication, land the browser in the workspace. A top-level
// navigation (not an XHR) makes Mattermost's webapp load and authenticate
// from the cookie. Prefer the user's first team's channel; fall back to
// Mattermost's team selector.
async function gotoWorkspace() {
    // If we arrived here because Honco Chat wanted a specific page, honour it.
    const rt = param('redirect_to');
    if (rt && rt.charAt(0) === '/' && rt.charAt(1) !== '/') {
        window.location.assign(rt);
        return;
    }
    try {
        const res = await fetch('/api/v4/users/me/teams', {credentials: 'include', headers: headers(false)});
        if (res.ok) {
            const teams = await res.json();
            if (Array.isArray(teams) && teams.length) {
                window.location.assign('/' + teams[0].name + '/channels/town-square');
                return;
            }
        }
    } catch (_) { /* fall through */ }
    window.location.assign('/select_team');
}

function initTheme() {
    try {
        const t = localStorage.getItem('honco-theme');
        if (t === 'dark' || t === 'light') document.documentElement.setAttribute('data-theme', t);
    } catch (_) { /* ignore */ }
    const btn = document.getElementById('theme-toggle');
    if (btn) {
        btn.addEventListener('click', function () {
            const cur = document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
            document.documentElement.setAttribute('data-theme', cur);
            try { localStorage.setItem('honco-theme', cur); } catch (_) {}
        });
    }
}

function show(el, msg, kind) {
    if (!el) return;
    el.textContent = msg;
    el.className = 'auth-msg ' + (kind || '');
    el.hidden = !msg;
}

function param(name) {
    return new URLSearchParams(window.location.search).get(name) || '';
}

// --- per-page wiring --------------------------------------------------------

export function initPage(page) {
    initTheme();
    const form = document.getElementById('auth-form');
    const msg = document.getElementById('auth-msg');
    const submit = form ? form.querySelector('button[type=submit]') : null;
    const busy = (on, label) => { if (submit) { submit.disabled = on; if (label) submit.textContent = label; } };

    if (page === 'login') {
        // Already signed in? Go straight to the workspace — no second login.
        currentUser().then(u => { if (u) gotoWorkspace(); });
        form.addEventListener('submit', async e => {
            e.preventDefault();
            show(msg, '', '');
            busy(true, 'Signing in…');
            const r = await api('POST', '/api/v4/users/login', {
                login_id: form.login_id.value.trim(),
                password: form.password.value,
            });
            if (r.ok) { await gotoWorkspace(); return; }
            // Never reveal which part was wrong.
            show(msg, 'Incorrect username/email or password.', 'err');
            busy(false, 'Sign In');
        });
    }

    if (page === 'signup') {
        // Invite-based: an invite token/id must be present (EnableOpenServer=false).
        const token = param('t') || param('token');
        const inviteId = param('iid') || param('id');
        if (!token && !inviteId) {
            show(msg, 'An invitation is required to create an account. Please use the invite link you were sent.', 'err');
            if (submit) submit.disabled = true;
        }
        form.addEventListener('submit', async e => {
            e.preventDefault();
            show(msg, '', '');
            busy(true, 'Creating account…');
            let path = '/api/v4/users';
            if (token) path += '?t=' + encodeURIComponent(token);
            else if (inviteId) path += '?iid=' + encodeURIComponent(inviteId);
            const r = await api('POST', path, {
                email: form.email.value.trim(),
                username: form.username.value.trim(),
                password: form.password.value,
            });
            if (r.ok) {
                show(msg, 'Account created. Signing you in…', 'ok');
                const li = await api('POST', '/api/v4/users/login', {login_id: form.email.value.trim(), password: form.password.value});
                if (li.ok) { await gotoWorkspace(); return; }
                window.location.assign('/login');
                return;
            }
            show(msg, (r.data && r.data.message) || 'Could not create the account. Check your invite and details.', 'err');
            busy(false, 'Create account');
        });
    }

    if (page === 'forgot') {
        form.addEventListener('submit', async e => {
            e.preventDefault();
            show(msg, '', '');
            busy(true, 'Sending…');
            await api('POST', '/api/v4/users/password/reset/send', {email: form.email.value.trim()});
            // Always show the same message (do not reveal whether the email exists).
            show(msg, 'If that email has an account, a password reset link is on its way.', 'ok');
            busy(false, 'Send reset link');
        });
    }

    if (page === 'reset') {
        const token = param('token') || param('t');
        if (!token) { show(msg, 'This reset link is missing its token. Request a new one from “Forgot password”.', 'err'); if (submit) submit.disabled = true; }
        form.addEventListener('submit', async e => {
            e.preventDefault();
            show(msg, '', '');
            busy(true, 'Updating…');
            const r = await api('POST', '/api/v4/users/password/reset', {token, new_password: form.password.value});
            if (r.ok) { show(msg, 'Password updated. Redirecting to sign in…', 'ok'); setTimeout(() => window.location.assign('/login'), 1200); return; }
            show(msg, (r.data && r.data.message) || 'Could not reset the password. The link may have expired.', 'err');
            busy(false, 'Set new password');
        });
    }

    if (page === 'verify') {
        const token = param('token') || param('t');
        const box = document.getElementById('verify-status');
        if (!token) { if (box) show(box, 'This verification link is missing its token.', 'err'); return; }
        api('POST', '/api/v4/users/email/verify', {token}).then(r => {
            if (r.ok) { show(box, 'Email verified. You can now sign in.', 'ok'); }
            else { show(box, 'This verification link is invalid or has expired.', 'err'); }
        });
    }
}
