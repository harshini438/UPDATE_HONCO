/**
 * Honco Workspace -- webapp bundle.
 *
 * Written as plain ES5-compatible JavaScript against the React that the
 * Mattermost webapp already exposes on `window`. That is deliberate:
 * a webpack/babel pipeline would add a second frontend build to a project
 * that already has one, for a panel of this size. There is no new
 * framework and no new component library here.
 *
 * Everything renders inside Mattermost's own right-hand sidebar and uses
 * Mattermost's own CSS custom properties (--center-channel-color,
 * --button-bg, ...), so it inherits the current Honco theme automatically
 * and changes appearance with it. The one stylesheet below (`CSS`) is a
 * thin layer of `.hw-*` classes on those same variables -- buttons, rows,
 * tabs, empty and loading states -- so every Honco panel and card shares
 * one look, with hover and focus states that inline styles cannot give.
 */
(function () {
    'use strict';

    var React = window.React;
    var e = React.createElement;

    var PLUGIN_ID = 'com.honco.workspace';
    var API = '/plugins/' + PLUGIN_ID + '/api/v1';

    var STATUSES = [
        {value: 'todo', label: 'To do'},
        {value: 'in_progress', label: 'In progress'},
        {value: 'done', label: 'Done'},
    ];

    // All requests go through the browser session -- the same cookie the
    // rest of the webapp uses. The plugin never sees a token from here,
    // and the server resolves identity from the session, not from
    // anything this file sends.
    function request(method, path, body) {
        var opts = {
            method: method,
            credentials: 'same-origin',
            headers: {'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest'},
        };
        if (body) {
            opts.body = JSON.stringify(body);
        }
        return fetch(API + path, opts).then(function (res) {
            if (res.status === 204) {
                return null;
            }
            return res.json().then(function (data) {
                if (!res.ok) {
                    throw new Error((data && data.error) || 'Request failed');
                }
                return data;
            });
        });
    }

    function formatDue(ms) {
        if (!ms) {
            return '';
        }
        var d = new Date(ms);
        return d.toLocaleDateString(undefined, {day: 'numeric', month: 'short', year: 'numeric'});
    }

    // --- Task row ----------------------------------------------------------

    // --- Team members (for the assignee picker) ----------------------------

    // Read through Mattermost's own users API, on the caller's session.
    //
    // That matters: the server decides which profiles this user may see, so
    // the picker cannot list people from a team they are not in. The plugin
    // does not need -- and deliberately does not add -- an endpoint of its
    // own for this.
    //
    // Nothing here is authorization. The backend re-checks that an assignee
    // is a member of the task's team on every create and update; this list
    // only stops the UI offering choices that would be rejected.
    function fetchTeamMembers(teamId) {
        if (!teamId) {
            return Promise.resolve([]);
        }
        return fetch('/api/v4/users?in_team=' + encodeURIComponent(teamId) +
                     '&per_page=200&active=true', {
            credentials: 'same-origin',
            headers: {'X-Requested-With': 'XMLHttpRequest'},
        }).then(function (res) {
            if (!res.ok) {
                return [];
            }
            return res.json();
        }).then(function (users) {
            return (users || []).filter(function (u) {
                // Bots are not people to assign work to.
                return !u.is_bot && u.delete_at === 0;
            }).sort(function (a, b) {
                return (a.username || '').localeCompare(b.username || '');
            });
        }).catch(function () {
            return [];
        });
    }

    function memberLabel(u) {
        if (!u) {
            return '';
        }
        var full = [u.first_name, u.last_name].filter(Boolean).join(' ').trim();
        return full ? full + ' (@' + u.username + ')' : '@' + u.username;
    }

    // --- User avatar -------------------------------------------------------

    // The one profile picture Mattermost already keeps for a user, at the
    // URL its own UI uses: /api/v4/users/{id}/image?_={last_picture_update}.
    // The plugin stores and copies nothing. When someone changes or removes
    // their photo, Mattermost bumps last_picture_update, broadcasts
    // user_updated, the webapp store updates, and this re-renders with the
    // new URL -- so every Honco panel changes at the same moment as the
    // rest of the app. A user with no photo gets Mattermost's default.
    //
    // The image URL is ALWAYS keyed with the timestamp. The server marks a
    // served image cacheable for a day whatever the URL says, so a URL
    // without the key would be the one thing that could pin a stale photo
    // in a browser. Until the timestamp is known nothing is rendered.
    //
    // Where the timestamp comes from, in order: the webapp's own store
    // (kept current by Mattermost's user_updated event, so a change shows
    // here at the same moment as everywhere else), the caller (lists that
    // already fetched the profile), or one lookup through Mattermost's
    // users API for a person the store has not loaded.
    var pictureUpdates = {};   // userId -> last_picture_update, from that lookup
    var pictureLookups = {};   // userId -> true while a lookup is in flight
    var pictureListeners = [];

    function pictureUpdateOf(userId, fallback) {
        try {
            var st = window.store ? window.store.getState() : null;
            var u = st && st.entities.users.profiles[userId];
            if (u && typeof u.last_picture_update === 'number') {
                return u.last_picture_update;
            }
        } catch (err) {
            // fall through
        }
        if (typeof fallback === 'number') {
            return fallback;
        }
        return typeof pictureUpdates[userId] === 'number' ? pictureUpdates[userId] : null;
    }

    function lookupPictureUpdate(userId) {
        if (pictureLookups[userId] || typeof pictureUpdates[userId] === 'number') {
            return;
        }
        pictureLookups[userId] = true;
        fetch('/api/v4/users/ids', {
            method: 'POST',
            credentials: 'same-origin',
            headers: {'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest'},
            body: JSON.stringify([userId]),
        }).then(function (res) {
            return res.ok ? res.json() : [];
        }).then(function (users) {
            (users || []).forEach(function (u) {
                if (u && u.id) {
                    pictureUpdates[u.id] = typeof u.last_picture_update === 'number' ? u.last_picture_update : 0;
                }
            });
        }).catch(function () {
            // leave it unknown; the avatar simply does not render
        }).then(function () {
            delete pictureLookups[userId];
            pictureListeners.slice().forEach(function (fn) {
                fn();
            });
        });
    }

    function avatarURL(userId, lpu) {
        return '/api/v4/users/' + encodeURIComponent(userId) + '/image?_=' + lpu;
    }

    function UserAvatar(props) {
        var s = React.useState(pictureUpdateOf(props.userId, props.lastPictureUpdate));
        var lpu = s[0];
        var setLpu = s[1];
        React.useEffect(function () {
            var refresh = function () {
                var v = pictureUpdateOf(props.userId, props.lastPictureUpdate);
                setLpu(function (prev) {
                    return prev === v ? prev : v;
                });
            };
            refresh();
            if (props.userId && pictureUpdateOf(props.userId, props.lastPictureUpdate) === null) {
                lookupPictureUpdate(props.userId);
            }
            pictureListeners.push(refresh);
            var unsub = (window.store && window.store.subscribe) ? window.store.subscribe(refresh) : null;
            return function () {
                pictureListeners = pictureListeners.filter(function (fn) {
                    return fn !== refresh;
                });
                if (unsub) {
                    unsub();
                }
            };
        }, [props.userId, props.lastPictureUpdate]);
        if (!props.userId || lpu === null) {
            return null;
        }
        var size = props.size || 20;
        return e('img', {
            className: 'hw-avatar' + (props.className ? ' ' + props.className : ''),
            src: avatarURL(props.userId, lpu),
            alt: props.alt || '',
            width: size,
            height: size,
            loading: 'lazy',
            'data-user-id': props.userId,
            'data-picture-update': lpu,
            style: {width: size, height: size},
        });
    }

    // --- shared styles -----------------------------------------------------

    // One small stylesheet, injected once. Inline styles cannot express
    // :hover, :focus-visible or an animation, and those are exactly what
    // separates a panel that feels native from one that feels bolted on.
    // Every colour is a Mattermost theme variable, so light and dark (and
    // any custom theme) come for free; nothing here is a fixed hex.
    var CSS = [
        '.hw{display:flex;flex-direction:column;height:100%;min-height:0;font-size:13px;color:var(--center-channel-color);container-type:inline-size;container-name:hw}',
        '.hw *{box-sizing:border-box}',
        '.hw-tabs{display:flex;flex:none;overflow-x:auto;scrollbar-width:none;padding:0 6px;border-bottom:1px solid rgba(var(--center-channel-color-rgb),.12)}',
        '.hw-tabs::-webkit-scrollbar{display:none}',
        '.hw-tab{flex:1 0 auto;display:inline-flex;align-items:center;justify-content:center;gap:6px;white-space:nowrap;background:none;border:0;border-bottom:2px solid transparent;border-radius:0;padding:10px 7px 8px;font:inherit;font-size:12px;color:rgba(var(--center-channel-color-rgb),.75);cursor:pointer;transition:color .12s,background .12s}',
        '.hw-tab .icon{display:none;font-size:16px;line-height:1}',
        '@container hw (min-width: 560px){.hw-tab .icon{display:inline}.hw-tab{padding:10px 12px 8px;font-size:13px}}',
        '.hw-tab:hover{color:var(--center-channel-color);background:rgba(var(--center-channel-color-rgb),.04)}',
        '.hw-tab[aria-selected=true]{color:var(--button-bg);border-bottom-color:var(--button-bg);font-weight:600}',
        '.hw-bar{display:flex;flex:none;align-items:center;gap:8px;flex-wrap:wrap;padding:8px 12px;border-bottom:1px solid rgba(var(--center-channel-color-rgb),.12)}',
        '.hw-bar-title{display:inline-flex;align-items:center;gap:6px;font-weight:600;font-size:13px}',
        '.hw-bar-title .icon{font-size:16px;line-height:1;color:rgba(var(--center-channel-color-rgb),.64)}',
        '.hw-spacer{margin-left:auto}',
        '.hw-btn{display:inline-flex;align-items:center;justify-content:center;gap:6px;padding:6px 14px;border-radius:4px;border:1px solid transparent;font:inherit;font-size:13px;font-weight:600;line-height:1.3;cursor:pointer;text-decoration:none;background:var(--button-bg);color:var(--button-color);transition:background .12s,box-shadow .12s,border-color .12s}',
        '.hw-btn:hover{background:rgba(var(--button-bg-rgb),.88);color:var(--button-color);text-decoration:none}',
        '.hw-btn:active{background:rgba(var(--button-bg-rgb),.8)}',
        '.hw-btn:disabled{opacity:.55;cursor:default}',
        '.hw-btn-sm{padding:4px 10px;font-size:12px}',
        '.hw-btn-secondary{background:transparent;color:var(--button-bg);border-color:var(--button-bg)}',
        '.hw-btn-secondary:hover{background:rgba(var(--button-bg-rgb),.08);color:var(--button-bg)}',
        '.hw-btn-ghost{background:transparent;color:var(--center-channel-color);border-color:rgba(var(--center-channel-color-rgb),.24)}',
        '.hw-btn-ghost:hover{background:rgba(var(--center-channel-color-rgb),.06);color:var(--center-channel-color)}',
        '.hw-btn-link{background:none;border:0;padding:0;font-weight:400;font-size:12px;color:var(--link-color)}',
        '.hw-btn-link:hover{background:none;color:var(--link-color);text-decoration:underline}',
        '.hw-btn-danger.hw-btn-link{color:var(--error-text)}',
        '.hw-btn-danger.hw-btn-link:hover{color:var(--error-text)}',
        '.hw-input,.hw-select,.hw-textarea{width:100%;padding:6px 8px;margin-bottom:6px;border-radius:4px;border:1px solid rgba(var(--center-channel-color-rgb),.24);background:var(--center-channel-bg);color:var(--center-channel-color);font:inherit;font-size:13px;line-height:1.4;transition:border-color .12s,box-shadow .12s}',
        '.hw-input::placeholder,.hw-textarea::placeholder{color:rgba(var(--center-channel-color-rgb),.56)}',
        '.hw-input:hover,.hw-select:hover,.hw-textarea:hover{border-color:rgba(var(--center-channel-color-rgb),.4)}',
        '.hw-input:focus,.hw-select:focus,.hw-textarea:focus{outline:0;border-color:var(--button-bg);box-shadow:0 0 0 2px rgba(var(--button-bg-rgb),.2)}',
        '.hw-textarea{min-height:54px;resize:vertical}',
        '.hw-select{appearance:none;-webkit-appearance:none;padding-right:26px;cursor:pointer;background-image:url("data:image/svg+xml,%3Csvg xmlns=%27http://www.w3.org/2000/svg%27 viewBox=%270 0 24 24%27%3E%3Cpath fill=%27%238b8fa3%27 d=%27M7 10l5 5 5-5z%27/%3E%3C/svg%3E");background-repeat:no-repeat;background-position:right 6px center;background-size:16px}',
        '.hw-select-sm{width:auto;margin:0;padding:3px 24px 3px 8px;font-size:12px}',
        '.hw-check{display:inline-flex;align-items:center;gap:5px;font-size:12px;cursor:pointer;margin:0}',
        '.hw-check input{margin:0;accent-color:var(--button-bg)}',
        '.hw-form{flex:none;padding:10px 12px;border-bottom:1px solid rgba(var(--center-channel-color-rgb),.12);background:rgba(var(--center-channel-color-rgb),.03)}',
        '.hw-form-label{font-size:12px;margin-bottom:4px;color:rgba(var(--center-channel-color-rgb),.72)}',
        '.hw-form-note{font-size:11px;margin-bottom:8px;color:rgba(var(--center-channel-color-rgb),.64)}',
        '.hw-actions{display:flex;gap:8px;flex-wrap:wrap;align-items:center}',
        '.hw-list{flex:1;min-height:0;overflow-y:auto}',
        '.hw-row{padding:10px 12px;border-bottom:1px solid rgba(var(--center-channel-color-rgb),.08);transition:background .12s}',
        '.hw-row:hover{background:rgba(var(--center-channel-color-rgb),.03)}',
        '.hw-row-selected{background:rgba(var(--button-bg-rgb),.06);box-shadow:inset 3px 0 0 var(--button-bg)}',
        '.hw-row-title{font-size:13px;font-weight:600;color:var(--center-channel-color);word-break:break-word}',
        '.hw-row-done .hw-row-title{text-decoration:line-through;opacity:.7}',
        '.hw-row-desc{font-size:12px;margin-top:2px;white-space:pre-wrap;word-break:break-word;color:rgba(var(--center-channel-color-rgb),.72)}',
        '.hw-row-meta{display:flex;flex-wrap:wrap;align-items:center;gap:4px 10px;margin-top:6px;font-size:11px;color:rgba(var(--center-channel-color-rgb),.64)}',
        '.hw-row-meta .icon{font-size:13px;line-height:1;vertical-align:-1px}',
        '.hw-row-actions{display:flex;flex-wrap:wrap;align-items:center;gap:8px;margin-top:8px}',
        // Files. The row is a three-part flex (icon, body, action) that
        // wraps on a narrow panel rather than pushing the Download button
        // off the edge.
        '.hw-file-row{display:flex;align-items:flex-start;gap:10px;flex-wrap:wrap}',
        '.hw-row-body{flex:1 1 180px;min-width:0}',
        '.hw-file-icon{flex:0 0 auto;margin-top:1px;color:rgba(var(--center-channel-color-rgb),.55)}',
        '.hw-file-icon .icon{font-size:18px;line-height:1}',
        '.hw-file-name{display:block;text-decoration:none;color:var(--link-color);overflow-wrap:anywhere}',
        '.hw-file-name:hover{text-decoration:underline}',
        '.hw-file-row .hw-btn{flex:0 0 auto}',
        '.hw-more{padding:10px 12px;text-align:center}',
        '.hw-hit{display:block;width:100%;text-align:left;background:none;border:0;border-bottom:1px solid rgba(var(--center-channel-color-rgb),.08);padding:9px 16px;cursor:pointer;font:inherit;color:inherit;transition:background .12s}',
        '.hw-hit:hover{background:rgba(var(--center-channel-color-rgb),.04)}',
        '.hw-chips{display:flex;flex-wrap:wrap;gap:4px;padding:0 16px 10px}',
        '.hw-chip{padding:3px 10px;font:inherit;font-size:12px;border-radius:12px;cursor:pointer;border:1px solid rgba(var(--center-channel-color-rgb),.16);background:transparent;color:rgba(var(--center-channel-color-rgb),.75);transition:background .12s,color .12s,border-color .12s}',
        '.hw-chip:hover{background:rgba(var(--center-channel-color-rgb),.06);color:var(--center-channel-color)}',
        '.hw-chip[aria-selected=true]{background:rgba(var(--button-bg-rgb),.08);border-color:rgba(var(--button-bg-rgb),.4);color:var(--button-bg);font-weight:600}',
        '.hw-section-title{padding:10px 16px 4px;font-size:11px;font-weight:600;letter-spacing:.02em;text-transform:uppercase;color:rgba(var(--center-channel-color-rgb),.56)}',
        '.hw-badge{display:inline-flex;align-items:center;padding:1px 7px;border-radius:10px;font-size:11px;font-weight:600;line-height:16px;background:rgba(var(--center-channel-color-rgb),.08);color:rgba(var(--center-channel-color-rgb),.72)}',
        '.hw-badge-ok{background:rgba(var(--online-indicator-rgb),.12);color:var(--online-indicator)}',
        '.hw-badge-warn{background:rgba(var(--away-indicator-rgb),.16);color:rgba(var(--center-channel-color-rgb),.8)}',
        '.hw-badge-err{background:rgba(var(--error-text-color-rgb),.1);color:var(--error-text)}',
        '.hw-dot{display:inline-block;width:8px;height:8px;border-radius:50%;flex-shrink:0}',
        '.hw-avatar{display:inline-block;border-radius:50%;object-fit:cover;flex:none;vertical-align:middle;background:rgba(var(--center-channel-color-rgb),.08)}',
        '.hw-status{display:flex;align-items:center;gap:6px;font-size:13px;color:var(--center-channel-color)}',
        '.hw-empty{display:flex;flex-direction:column;align-items:center;text-align:center;gap:4px;padding:36px 24px 28px;color:rgba(var(--center-channel-color-rgb),.64);font-size:13px;line-height:1.5}',
        '.hw-empty .icon{font-size:32px;line-height:1;margin-bottom:6px;color:rgba(var(--center-channel-color-rgb),.4)}',
        '.hw-empty-title{font-size:14px;font-weight:600;color:var(--center-channel-color)}',
        '.hw-empty .hw-btn{margin-top:10px}',
        '.hw-error{display:flex;gap:8px;align-items:flex-start;margin:10px 12px;padding:8px 10px;border-radius:4px;font-size:12px;line-height:1.45;background:rgba(var(--error-text-color-rgb),.08);color:var(--error-text)}',
        '.hw-error .icon{font-size:15px;line-height:1.2;flex-shrink:0}',
        '.hw-note{padding:12px;font-size:13px;line-height:1.5;color:rgba(var(--center-channel-color-rgb),.72)}',
        '.hw-skeleton{padding:12px}',
        '.hw-skeleton i{display:block;height:11px;border-radius:4px;margin:9px 0;background:linear-gradient(90deg,rgba(var(--center-channel-color-rgb),.06) 25%,rgba(var(--center-channel-color-rgb),.12) 50%,rgba(var(--center-channel-color-rgb),.06) 75%);background-size:400px 100%;animation:hw-shimmer 1.4s linear infinite}',
        '@keyframes hw-shimmer{0%{background-position:-400px 0}100%{background-position:400px 0}}',
        '.hw-card{border:1px solid rgba(var(--center-channel-color-rgb),.16);border-radius:8px;padding:14px 16px;margin:4px 0;max-width:520px;background:rgba(var(--center-channel-color-rgb),.03);transition:border-color .12s,box-shadow .12s}',
        '.hw-card:hover{border-color:rgba(var(--center-channel-color-rgb),.28);box-shadow:0 1px 3px rgba(0,0,0,.06)}',
        '.hw-card a.hw-btn,.hw-card a.hw-btn:hover,.hw-card a.hw-btn:focus{color:var(--button-color);text-decoration:none}',
        '.hw-card a.hw-btn-secondary,.hw-card a.hw-btn-secondary:hover,.hw-card a.hw-btn-secondary:focus{color:var(--button-bg)}',
        '.hw-card-title{display:flex;align-items:center;gap:8px;margin-bottom:2px;font-size:15px;font-weight:700;color:var(--center-channel-color)}',
        '.hw-card-title .icon{font-size:20px;line-height:1;color:var(--button-bg)}',
        '.hw-card-sub{font-size:12px;margin-bottom:8px;color:rgba(var(--center-channel-color-rgb),.72)}',
        '.hw-card-line{font-size:12px;margin-top:4px;display:flex;align-items:center;gap:6px;color:rgba(var(--center-channel-color-rgb),.72)}',
        '.hw-card-line .icon{font-size:14px;line-height:1}',
        '.hw-kv{display:flex;justify-content:space-between;gap:12px;font-size:13px;padding:4px 0;border-bottom:1px solid rgba(var(--center-channel-color-rgb),.06)}',
        '.hw-kv:last-child{border-bottom:0}',
        '.hw-kv-k{color:rgba(var(--center-channel-color-rgb),.8)}',
        '.hw-kv-v{font-weight:600;text-align:right;font-variant-numeric:tabular-nums}',
        '.hw-kv-warn{color:var(--error-text)}',
        '.hw-pager{display:flex;flex:none;align-items:center;gap:8px;flex-wrap:wrap;padding:6px 12px;border-top:1px solid rgba(var(--center-channel-color-rgb),.12);font-size:11px;color:rgba(var(--center-channel-color-rgb),.56)}',
        '.hw-pager-range{margin-left:auto;font-variant-numeric:tabular-nums}',
        '.hw-pre{margin-top:6px;padding:8px;font-size:12px;line-height:1.45;white-space:pre-wrap;border-radius:4px;background:rgba(var(--center-channel-color-rgb),.04);color:var(--center-channel-color);border:0}',
        '.hw-hint{margin-top:10px;padding:8px 10px;border-radius:4px;font-size:12px;line-height:1.5;background:rgba(var(--center-channel-color-rgb),.06);color:rgba(var(--center-channel-color-rgb),.8)}',
        '.hw-ai-head{flex:none;padding:12px 12px 10px;border-bottom:1px solid rgba(var(--center-channel-color-rgb),.08);background:rgba(var(--center-channel-color-rgb),.02)}',
        '.hw-ai-head-row{display:flex;align-items:center;gap:8px;min-width:0}',
        '.hw-ai-avatar{display:inline-flex;align-items:center;justify-content:center;width:26px;height:26px;border-radius:8px;flex:none;background:rgba(var(--button-bg-rgb),.12);color:var(--button-bg)}',
        '.hw-ai-avatar .icon{font-size:16px;line-height:1}',
        '.hw-ai-head-name{font-size:14px;font-weight:600;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}',
        '.hw-ai-pill{display:inline-flex;align-items:center;gap:6px;margin-left:auto;flex:none;padding:2px 9px 2px 7px;border-radius:12px;font-size:12px;font-weight:600;background:rgba(var(--center-channel-color-rgb),.06);color:rgba(var(--center-channel-color-rgb),.8)}',
        '.hw-ai-pill-ok{background:rgba(var(--online-indicator-rgb),.12);color:var(--online-indicator)}',
        '.hw-ai-pill-warn{background:rgba(var(--away-indicator-rgb),.16);color:rgba(var(--center-channel-color-rgb),.85)}',
        '.hw-ai-pill-err{background:rgba(var(--error-text-color-rgb),.1);color:var(--error-text)}',
        '.hw-ai-glyph{font-size:11px;line-height:1}',
        '.hw-ai-pulse{animation:hw-glow 1.8s ease-in-out infinite}',
        '@keyframes hw-glow{0%,100%{opacity:1}50%{opacity:.35}}',
        '.hw-ai-head-meeting{margin-top:8px;font-size:15px;font-weight:600;line-height:1.3;word-break:break-word}',
        '.hw-ai-head-line{margin-top:3px;font-size:12px;color:rgba(var(--center-channel-color-rgb),.64);display:flex;flex-wrap:wrap;gap:4px 0}',
        '.hw-ai-head-sep:before{content:"·";margin:0 6px;opacity:.7}',
        '.hw-ai-controls{display:flex;align-items:center;gap:6px;flex-wrap:wrap;margin-top:10px}',
        '.hw-ai-controls .hw-btn{flex:none}',
        '.hw-ai-banner{display:flex;align-items:center;gap:8px;margin:10px 12px 0;padding:8px 10px;border-radius:6px;font-size:12px;line-height:1.45;background:rgba(var(--away-indicator-rgb),.14);color:var(--center-channel-color)}',
        '.hw-ai-banner .icon{font-size:15px;flex:none}',
        '.hw-ai-banner-err{background:rgba(var(--error-text-color-rgb),.08);color:var(--error-text)}',
        '.hw-ai-pick{padding:10px 12px 0}',
        '.hw-ai-intro{padding-top:8px}',
        '.hw-ai-intro .hw-empty{padding-top:28px}',
        '.hw-ai-block{padding:14px 12px 10px;border-bottom:1px solid rgba(var(--center-channel-color-rgb),.08)}',
        '.hw-ai-block:last-child{border-bottom:0}',
        '.hw-ai-block-bare{padding:0;border-bottom:0}',
        '.hw-ai-block-primary{padding-top:12px}',
        '.hw-ai-block-title{display:flex;align-items:center;gap:6px;margin-bottom:8px;font-size:11px;font-weight:700;letter-spacing:.04em;text-transform:uppercase;color:rgba(var(--center-channel-color-rgb),.64)}',
        '.hw-ai-block-title .icon{font-size:15px;line-height:1}',
        '.hw-ai-count{font-weight:400;text-transform:none;letter-spacing:0}',
        '.hw-ai-tag{margin-left:4px;padding:0 6px;font-size:10px;line-height:15px;text-transform:none;letter-spacing:0}',
        '.hw-ai-card{border:1px solid rgba(var(--center-channel-color-rgb),.12);border-left:3px solid var(--button-bg);border-radius:8px;padding:10px 12px;margin-bottom:8px;background:var(--center-channel-bg);transition:box-shadow .2s,border-color .2s}',
        '.hw-ai-card-latest{border-color:rgba(var(--button-bg-rgb),.3);border-left-width:3px;background:rgba(var(--button-bg-rgb),.05);box-shadow:0 1px 2px rgba(0,0,0,.04)}',
        '.hw-ai-card-new{box-shadow:0 0 0 2px rgba(var(--button-bg-rgb),.25)}',
        '.hw-ai-card-empty{border-left-color:rgba(var(--center-channel-color-rgb),.2);background:rgba(var(--center-channel-color-rgb),.02)}',
        '.hw-ai-card-head{display:flex;align-items:center;gap:6px;margin-bottom:6px;min-width:0}',
        '.hw-ai-card-head .icon{font-size:16px;line-height:1;flex:none}',
        '.hw-ai-card-kind{font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.04em;color:rgba(var(--center-channel-color-rgb),.64);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}',
        '.hw-ai-card-body{font-size:14px;line-height:1.5;color:var(--center-channel-color);white-space:pre-wrap;word-break:break-word}',
        '.hw-ai-card-latest .hw-ai-card-body{font-size:16px;line-height:1.45;font-weight:500}',
        '.hw-ai-card-foot{margin-top:8px;font-size:11px;color:rgba(var(--center-channel-color-rgb),.56)}',
        '.hw-ai-prev{margin-top:6px}',
        '.hw-ai-prev .hw-ai-card{padding:8px 10px;background:transparent}',
        '.hw-ai-prev .hw-ai-card-body{font-size:13px}',
        '.hw-ai-insights{display:flex;flex-direction:column;gap:2px}',
        '.hw-ai-insight{display:flex;gap:10px;align-items:flex-start;padding:7px 8px;border-radius:6px;font-size:13px;background:rgba(var(--center-channel-color-rgb),.03)}',
        '.hw-ai-insight-icon{display:inline-flex;align-items:center;justify-content:center;width:24px;height:24px;border-radius:6px;flex:none;background:rgba(var(--center-channel-color-rgb),.05)}',
        '.hw-ai-insight-icon .icon{font-size:15px;line-height:1}',
        '.hw-ai-insight-text{line-height:1.45;word-break:break-word;margin-top:1px}',
        '.hw-ai-topics{display:flex;flex-wrap:wrap;gap:6px;margin:2px 0 4px}',
        '.hw-ai-topics .hw-chip{cursor:default;max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}',
        '.hw-ai-quiet{font-size:12px;color:rgba(var(--center-channel-color-rgb),.64);padding:2px 0 6px;line-height:1.5}',
        '.hw-ai-older{font-size:12px;color:rgba(var(--center-channel-color-rgb),.64);padding:0 0 6px}',
        '.hw-ai-transcript{max-height:46vh;overflow-y:auto;border:1px solid rgba(var(--center-channel-color-rgb),.08);border-radius:8px;padding:2px 12px;background:rgba(var(--center-channel-color-rgb),.02)}',
        '.hw-ai-line{padding:8px 0;border-bottom:1px solid rgba(var(--center-channel-color-rgb),.06)}',
        '.hw-ai-line:last-child{border-bottom:0}',
        '.hw-ai-line-interim .hw-ai-text{opacity:.62;font-style:italic}',
        '.hw-ai-line-meta{display:flex;gap:8px;align-items:baseline;font-size:11px;margin-bottom:3px}',
        '.hw-ai-time{font-variant-numeric:tabular-nums;color:rgba(var(--center-channel-color-rgb),.56)}',
        '.hw-ai-speaker{font-weight:700;font-size:12px;color:var(--center-channel-color)}',
        '.hw-ai-interim-tag{font-size:10px;color:rgba(var(--center-channel-color-rgb),.56);font-style:italic}',
        '.hw-ai-text{font-size:13.5px;line-height:1.5;white-space:pre-wrap;word-break:break-word;color:var(--center-channel-color)}',
        '.hw-ai-jump{display:flex;justify-content:flex-end;padding-top:8px}',
        '.hw-ai-done-head{padding:14px 12px 10px;border-bottom:1px solid rgba(var(--center-channel-color-rgb),.08)}',
        '.hw-ai-done-title{font-size:11px;font-weight:700;letter-spacing:.06em;text-transform:uppercase;color:rgba(var(--center-channel-color-rgb),.64);margin-bottom:8px}',
        '.hw-ai-done-meta{display:flex;gap:10px;align-items:baseline;font-size:13px;margin-top:3px;min-width:0}',
        '.hw-ai-done-k{flex:none;width:56px;font-size:11px;font-weight:600;color:rgba(var(--center-channel-color-rgb),.56)}',
        '.hw-ai-done-status{display:inline-flex;align-items:center;gap:5px}',
        '.hw-ai-done-status .icon{font-size:15px;line-height:1}',
        '.hw-ai-done-ok{color:var(--online-indicator);font-weight:600}',
        '.hw-ai-done-err{color:var(--error-text);font-weight:600}',
        '.hw-ai-sec{border-bottom:1px solid rgba(var(--center-channel-color-rgb),.08)}',
        '.hw-ai-sec-head{display:flex;align-items:center;gap:8px;width:100%;padding:11px 12px;background:none;border:0;font:inherit;font-size:12px;font-weight:700;letter-spacing:.04em;text-transform:uppercase;color:rgba(var(--center-channel-color-rgb),.72);cursor:pointer;text-align:left;transition:background .12s}',
        '.hw-ai-sec-head:hover{background:rgba(var(--center-channel-color-rgb),.04)}',
        '.hw-ai-sec-head .icon{font-size:15px;line-height:1}',
        '.hw-ai-sec-open .hw-ai-sec-head{color:var(--center-channel-color)}',
        '.hw-ai-sec-body{padding:0 12px 12px}',
        '.hw-ai-summary{font-size:13.5px;line-height:1.6;white-space:pre-wrap;word-break:break-word}',
        '.hw-ai-list{margin:0;padding-left:18px;font-size:13px;line-height:1.5}',
        '.hw-ai-list li{margin-bottom:4px;word-break:break-word}',
        '.hw-ai-actions{list-style:none;margin:0;padding:0}',
        '.hw-ai-action{display:flex;gap:10px;align-items:flex-start;padding:6px 0;border-bottom:1px solid rgba(var(--center-channel-color-rgb),.06)}',
        '.hw-ai-action:last-child{border-bottom:0}',
        '.hw-ai-checkbox{flex:none;width:16px;height:16px;margin-top:2px;border:1.5px solid rgba(var(--center-channel-color-rgb),.4);border-radius:4px}',
        '.hw-ai-action-text{font-size:13px;line-height:1.45;word-break:break-word}',
        '.hw-ai-action-owner{font-size:11px;color:rgba(var(--center-channel-color-rgb),.56);margin-top:1px}',
        '.hw-ai-labelled{display:flex;flex-direction:column;gap:8px}',
        '.hw-ai-labelled-text{font-size:13px;line-height:1.45;word-break:break-word}',
        '.hw-ai-delivered{padding:8px 12px 10px;font-size:11px;color:rgba(var(--center-channel-color-rgb),.56)}',
        '.hw-fade{animation:hw-fade .16s ease-out}',
        '@keyframes hw-fade{from{opacity:0;transform:translateY(2px)}to{opacity:1;transform:none}}',
        '.hw :focus-visible{outline:2px solid var(--button-bg);outline-offset:1px}',
        '.hw-tab:focus-visible{outline-offset:-2px}',
        '@media (prefers-reduced-motion:reduce){.hw *,.hw-card,.hw-fade,.hw-skeleton i{animation:none!important;transition:none!important}}',
    ].join('\n');

    function ensureStyles() {
        if (document.getElementById('honco-workspace-css')) {
            return;
        }
        var el = document.createElement('style');
        el.id = 'honco-workspace-css';
        el.textContent = CSS;
        document.head.appendChild(el);
    }

    // --- shared components -------------------------------------------------

    function Icon(props) {
        return e('i', {className: 'icon icon-' + props.name, 'aria-hidden': true, style: props.style});
    }

    // kind: primary (default) | secondary | ghost | link | danger-link
    function Button(props) {
        var cls = 'hw-btn';
        if (props.kind === 'secondary') {
            cls += ' hw-btn-secondary';
        } else if (props.kind === 'ghost') {
            cls += ' hw-btn-ghost';
        } else if (props.kind === 'link') {
            cls += ' hw-btn-link';
        } else if (props.kind === 'danger-link') {
            cls += ' hw-btn-link hw-btn-danger';
        }
        if (props.small) {
            cls += ' hw-btn-sm';
        }
        if (props.className) {
            cls += ' ' + props.className;
        }
        var attrs = {
            className: cls,
            type: props.type || 'button',
            onClick: props.onClick,
            disabled: props.disabled,
            title: props.title,
            'aria-label': props['aria-label'],
            style: props.style,
        };
        // Passed through only when given, so every existing caller renders
        // byte for byte as before: a disclosure button needs its expanded
        // state, and a stable test hook beats guessing at DOM structure.
        if (props['aria-expanded'] !== undefined) {
            attrs['aria-expanded'] = props['aria-expanded'];
        }
        if (props['data-testid']) {
            attrs['data-testid'] = props['data-testid'];
        }
        return e('button', attrs, [
            props.icon ? e(Icon, {key: 'i', name: props.icon}) : null,
            props.children,
        ]);
    }

    function Toolbar(props) {
        return e('div', {className: 'hw-bar', role: props.role}, [
            props.icon || props.title ? e('span', {key: 't', className: 'hw-bar-title'}, [
                props.icon ? e(Icon, {key: 'i', name: props.icon}) : null,
                props.title ? e('span', {key: 'l'}, props.title) : null,
            ]) : null,
            props.children,
        ]);
    }

    function EmptyState(props) {
        return e('div', {className: 'hw-empty hw-fade', role: 'status'}, [
            props.icon ? e(Icon, {key: 'i', name: props.icon}) : null,
            props.title ? e('div', {key: 't', className: 'hw-empty-title'}, props.title) : null,
            props.children ? e('div', {key: 'b'}, props.children) : null,
            props.action ? props.action : null,
        ]);
    }

    // Three grey lines while a list loads. The list keeps its height, so
    // the panel does not jump when the data arrives.
    function Loading(props) {
        return e('div', {className: 'hw-skeleton', role: 'status', 'aria-live': 'polite', 'aria-label': props.label || 'Loading'}, [
            e('i', {key: 1, style: {width: '70%'}}),
            e('i', {key: 2, style: {width: '90%'}}),
            e('i', {key: 3, style: {width: '55%'}}),
        ]);
    }

    function ErrorNote(props) {
        return e('div', {className: 'hw-error', role: 'alert'}, [
            e(Icon, {key: 'i', name: 'alert-circle-outline'}),
            e('span', {key: 't'}, props.children),
        ]);
    }

    function Badge(props) {
        var cls = 'hw-badge' + (props.tone ? ' hw-badge-' + props.tone : '');
        return e('span', {className: cls}, props.children);
    }

    function Dot(props) {
        return e('span', {className: 'hw-dot', style: {background: props.color}});
    }

    // --- Task editor (create and edit share one form) ----------------------

    // One component for both so a field can never exist on create and go
    // missing on edit -- which is exactly how the assignee got lost before.
    function TaskEditor(props) {
        var init = props.task || {};
        var f = React.useState({
            title: init.title || '',
            description: init.description || '',
            assignee_id: init.assignee_id || '',
            status: init.status || 'todo',
            due: init.due_at ? new Date(init.due_at).toISOString().slice(0, 10) : '',
        });
        var form = f[0];
        var setForm = f[1];

        var s = React.useState({saving: false, error: null});
        var state = s[0];
        var setState = s[1];

        function set(key, value) {
            var next = {};
            next[key] = value;
            setForm(Object.assign({}, form, next));
        }

        function submit(ev) {
            ev.preventDefault();
            if (!form.title.trim()) {
                setState({saving: false, error: 'A title is required.'});
                return;
            }
            setState({saving: true, error: null});

            var payload = {
                title: form.title.trim(),
                description: form.description,
                // Sent even when empty: "" is how the API clears an
                // assignment, and the backend accepts it explicitly.
                assignee_id: form.assignee_id,
                due_at: form.due ? new Date(form.due + 'T12:00:00').getTime() : 0,
            };
            if (props.task) {
                payload.status = form.status;
            }

            props.onSave(payload).then(function () {
                setState({saving: false, error: null});
            }).catch(function (err) {
                // Show what the server said -- validation messages here are
                // the useful ones ("assignee is not a member of this team").
                setState({saving: false, error: err.message || 'Could not save.'});
            });
        }

        return e('form', {
            onSubmit: submit,
            className: 'hw-form hw-fade',
        }, [
            e('input', {
                key: 'title',
                className: 'hw-input',
                placeholder: 'Task title',
                'aria-label': 'Task title',
                value: form.title,
                maxLength: 256,
                onChange: function (ev) {
                    set('title', ev.target.value);
                },
            }),
            e('textarea', {
                key: 'desc',
                className: 'hw-textarea',
                placeholder: 'Description (optional)',
                'aria-label': 'Task description',
                value: form.description,
                onChange: function (ev) {
                    set('description', ev.target.value);
                },
            }),
            e('select', {
                key: 'assignee',
                className: 'hw-select',
                'aria-label': 'Assignee',
                value: form.assignee_id,
                onChange: function (ev) {
                    set('assignee_id', ev.target.value);
                },
            }, [e('option', {key: '', value: ''}, 'Unassigned')].concat(
                (props.members || []).map(function (u) {
                    return e('option', {key: u.id, value: u.id}, memberLabel(u));
                }),
            )),
            props.task ? e('select', {
                key: 'status',
                className: 'hw-select',
                'aria-label': 'Status',
                value: form.status,
                onChange: function (ev) {
                    set('status', ev.target.value);
                },
            }, STATUSES.map(function (s2) {
                return e('option', {key: s2.value, value: s2.value}, s2.label);
            })) : null,
            e('input', {
                key: 'due',
                className: 'hw-input',
                type: 'date',
                'aria-label': 'Due date',
                value: form.due,
                onChange: function (ev) {
                    set('due', ev.target.value);
                },
            }),

            state.error ? e('div', {
                key: 'err',
                role: 'alert',
                style: {color: 'var(--error-text)', fontSize: 12, marginBottom: 6},
            }, state.error) : null,

            e('div', {key: 'actions', className: 'hw-actions'}, [
                e(Button, {
                    key: 'save',
                    type: 'submit',
                    disabled: state.saving || !form.title.trim(),
                }, state.saving ? 'Saving…' : (props.task ? 'Save changes' : 'Create task')),
                e(Button, {key: 'cancel', kind: 'ghost', onClick: props.onCancel}, 'Cancel'),
            ]),
        ]);
    }

    // --- Task row ----------------------------------------------------------

    function TaskRow(props) {
        var t = props.task;
        var overdue = t.due_at > 0 && t.due_at < Date.now() && t.status !== 'done';

        var c = React.useState(false);
        var confirming = c[0];
        var setConfirming = c[1];

        var assignee = props.members.filter(function (u) {
            return u.id === t.assignee_id;
        })[0];

        return e('div', {
            className: 'hw-row' + (t.status === 'done' ? ' hw-row-done' : ''),
            'data-task-id': t.id,
        }, [
            e('div', {key: 'title', className: 'hw-row-title'}, t.title),

            t.description ? e('div', {key: 'desc', className: 'hw-row-desc'}, t.description) : null,

            e('div', {key: 'meta', className: 'hw-row-meta'}, [
                e('span', {key: 'as', style: {display: 'inline-flex', alignItems: 'center', gap: 4}}, [
                    t.assignee_id
                        ? e(UserAvatar, {key: 'av', userId: t.assignee_id, lastPictureUpdate: assignee ? assignee.last_picture_update : undefined, size: 18})
                        : e(Icon, {key: 'i', name: 'account-outline'}),
                    ' ' + (t.assignee_id
                        ? (assignee ? memberLabel(assignee) : 'Assigned')
                        : 'Unassigned'),
                ]),
                t.due_at ? e('span', {
                    key: 'due',
                    style: {color: overdue ? 'var(--error-text)' : 'inherit', fontWeight: overdue ? 600 : 400},
                }, [
                    e(Icon, {key: 'i', name: overdue ? 'alert-outline' : 'calendar-outline'}),
                    ' ' + (overdue ? 'Overdue · ' : 'Due ') + formatDue(t.due_at),
                ]) : null,
            ]),

            e('div', {key: 'actions', className: 'hw-row-actions'}, [
                e('select', {
                    key: 'status',
                    value: t.status,
                    className: 'hw-select hw-select-sm',
                    'aria-label': 'Task status',
                    onChange: function (ev) {
                        props.onStatus(t.id, ev.target.value);
                    },
                }, STATUSES.map(function (s) {
                    return e('option', {key: s.value, value: s.value}, s.label);
                })),

                props.canEdit ? e(Button, {
                    key: 'edit',
                    kind: 'link',
                    onClick: function () {
                        props.onEdit(t);
                    },
                }, 'Edit') : null,

                // Deleting is destructive and the row is small, so the
                // confirmation happens in place rather than in a dialog
                // that would cover the list.
                props.canDelete ? (confirming ? e('span', {
                    key: 'confirm',
                    style: {display: 'inline-flex', gap: 8, alignItems: 'center'},
                }, [
                    e('span', {key: 'q', style: {fontSize: 11}}, 'Delete?'),
                    e(Button, {
                        key: 'yes',
                        kind: 'danger-link',
                        style: {fontWeight: 600},
                        onClick: function () {
                            setConfirming(false);
                            props.onDelete(t.id);
                        },
                    }, 'Yes'),
                    e(Button, {
                        key: 'no',
                        kind: 'link',
                        onClick: function () {
                            setConfirming(false);
                        },
                    }, 'No'),
                ]) : e(Button, {
                    key: 'del',
                    kind: 'danger-link',
                    onClick: function () {
                        setConfirming(true);
                    },
                }, 'Delete')) : null,
            ]),
        ]);
    }

    // --- The right-hand sidebar panel --------------------------------------

    var PAGE_SIZE = 20;

    function TasksPanel() {
        var st = React.useState({tasks: [], loading: true, error: null});
        var state = st[0];
        var setState = st[1];

        // Filters are applied by the SERVER, not by trimming an already
        // fetched page. Filtering a page in the browser would make paging
        // lie: page 2 of "assigned to me" would be page 2 of everything,
        // with most of it thrown away.
        var fl = React.useState({status: '', mine: false});
        var filter = fl[0];
        var setFilter = fl[1];

        var pg = React.useState(0);
        var page = pg[0];
        var setPage = pg[1];

        var m = React.useState({teamId: null, userId: null, members: []});
        var ctx = m[0];
        var setCtx = m[1];

        var ed = React.useState({creating: false, editing: null});
        var editor = ed[0];
        var setEditor = ed[1];

        // Team and user come from the webapp's own store, so the panel
        // always shows the team the user is actually looking at.
        React.useEffect(function () {
            var teamId = null;
            var userId = null;
            try {
                var store = window.store;
                if (store) {
                    var s = store.getState();
                    teamId = s.entities.teams.currentTeamId;
                    userId = s.entities.users.currentUserId;
                }
            } catch (err) {
                teamId = null;
            }
            setCtx({teamId: teamId, userId: userId, members: []});
            if (teamId) {
                fetchTeamMembers(teamId).then(function (members) {
                    setCtx(function (prev) {
                        return {teamId: teamId, userId: userId, members: members};
                    });
                });
            }
        }, []);

        var load = React.useCallback(function (teamId, pageNum, f, userId) {
            if (!teamId) {
                return;
            }
            setState(function (p) {
                return {tasks: p.tasks, loading: true, error: null};
            });
            var q = '/tasks?team_id=' + encodeURIComponent(teamId) +
                    '&limit=' + PAGE_SIZE + '&page=' + pageNum;
            // "Overdue" is a view, not a status: due in the past and not
            // done. The server decides what "now" is, so the list is right
            // whatever the browser's clock says.
            if (f.status === 'overdue') {
                q += '&overdue=1';
            } else if (f.status) {
                q += '&status=' + encodeURIComponent(f.status);
            }
            if (f.mine && userId) {
                q += '&assignee_id=' + encodeURIComponent(userId);
            }
            request('GET', q).then(function (data) {
                setState({tasks: (data && data.tasks) || [], loading: false, error: null});
            }).catch(function (err) {
                setState({tasks: [], loading: false, error: err.message});
            });
        }, []);

        React.useEffect(function () {
            load(ctx.teamId, page, filter, ctx.userId);
        }, [ctx.teamId, ctx.userId, page, filter, load]);

        function reload() {
            load(ctx.teamId, page, filter, ctx.userId);
        }

        function changeFilter(next) {
            // A filter change invalidates the current page number.
            setPage(0);
            setFilter(next);
        }

        function createTask(payload) {
            payload.team_id = ctx.teamId;
            return request('POST', '/tasks', payload).then(function () {
                setEditor({creating: false, editing: null});
                setPage(0);
                load(ctx.teamId, 0, filter, ctx.userId);
            });
        }

        function saveTask(id, payload) {
            return request('PATCH', '/tasks/' + id, payload).then(function () {
                setEditor({creating: false, editing: null});
                reload();
            });
        }

        function setStatus(id, status) {
            request('PUT', '/tasks/' + id + '/status', {status: status})
                .then(reload)
                .catch(function (err) {
                    setState(function (p) {
                        return {tasks: p.tasks, loading: false, error: err.message};
                    });
                });
        }

        function remove(id) {
            request('DELETE', '/tasks/' + id)
                .then(reload)
                .catch(function (err) {
                    setState(function (p) {
                        return {tasks: p.tasks, loading: false, error: err.message};
                    });
                });
        }

        // The list endpoint returns no total, so "is there a next page" is
        // inferred from getting a full page back. On an exact boundary that
        // offers a Next which lands on an empty page -- handled below with
        // an explicit empty state rather than a silent dead end.
        var hasNext = state.tasks.length === PAGE_SIZE;
        var hasPrev = page > 0;
        var first = page * PAGE_SIZE;

        return e('div', {className: 'hw'}, [

            e(Toolbar, {key: 'bar'}, [
                e('select', {
                    key: 'status',
                    value: filter.status,
                    className: 'hw-select hw-select-sm',
                    'aria-label': 'Filter by status',
                    onChange: function (ev) {
                        changeFilter({status: ev.target.value, mine: filter.mine});
                    },
                }, [e('option', {key: 'all', value: ''}, 'All statuses')].concat(
                    STATUSES.map(function (s) {
                        return e('option', {key: s.value, value: s.value}, s.label);
                    }),
                    [e('option', {key: 'overdue', value: 'overdue'}, 'Overdue')],
                )),
                e('label', {key: 'mine', className: 'hw-check'}, [
                    e('input', {
                        key: 'cb',
                        type: 'checkbox',
                        checked: filter.mine,
                        'aria-label': 'Assigned to me',
                        onChange: function (ev) {
                            changeFilter({status: filter.status, mine: ev.target.checked});
                        },
                    }),
                    'Mine',
                ]),
                e(Button, {
                    key: 'new',
                    small: true,
                    className: 'hw-spacer',
                    icon: editor.creating ? undefined : 'plus',
                    onClick: function () {
                        setEditor({creating: !editor.creating, editing: null});
                    },
                }, editor.creating ? 'Cancel' : 'New task'),
            ]),

            editor.creating ? e(TaskEditor, {
                key: 'create',
                members: ctx.members,
                onSave: createTask,
                onCancel: function () {
                    setEditor({creating: false, editing: null});
                },
            }) : null,

            editor.editing ? e(TaskEditor, {
                key: 'edit-' + editor.editing.id,
                task: editor.editing,
                members: ctx.members,
                onSave: function (payload) {
                    return saveTask(editor.editing.id, payload);
                },
                onCancel: function () {
                    setEditor({creating: false, editing: null});
                },
            }) : null,

            state.error ? e(ErrorNote, {key: 'err'}, state.error) : null,

            e('div', {key: 'list', className: 'hw-list'},
                state.loading ? e(Loading, {label: 'Loading tasks'}) :
                    (state.tasks.length === 0 ? (page > 0
                        ? e(EmptyState, {icon: 'check-circle-outline', title: 'No more tasks'}, 'No more tasks on this page.')
                        : e(EmptyState, {
                            icon: 'check-circle-outline',
                            title: (filter.status || filter.mine) ? 'No matching tasks' : 'No tasks yet',
                            action: editor.creating ? null : e(Button, {
                                kind: 'secondary', small: true, icon: 'plus',
                                onClick: function () {
                                    setEditor({creating: true, editing: null});
                                },
                            }, 'New task'),
                        }, (filter.status || filter.mine)
                            ? 'Nothing matches this filter.'
                            : 'Use "New task" to add one for this team.')) :
                        state.tasks.map(function (t) {
                            return e(TaskRow, {
                                key: t.id,
                                task: t,
                                members: ctx.members,
                                // Mirrors the server rule (creator or
                                // assignee). The server enforces it; this
                                // only avoids offering a doomed action.
                                canEdit: t.creator_id === ctx.userId || t.assignee_id === ctx.userId,
                                canDelete: t.creator_id === ctx.userId,
                                onStatus: setStatus,
                                onEdit: function (task) {
                                    setEditor({creating: false, editing: task});
                                },
                                onDelete: remove,
                            });
                        }))),

            e('div', {key: 'foot', className: 'hw-pager'}, [
                e(Button, {
                    key: 'prev',
                    kind: 'ghost',
                    small: true,
                    disabled: !hasPrev || state.loading,
                    onClick: function () {
                        setPage(Math.max(0, page - 1));
                    },
                }, 'Previous'),
                e(Button, {
                    key: 'next',
                    kind: 'ghost',
                    small: true,
                    disabled: !hasNext || state.loading,
                    onClick: function () {
                        setPage(page + 1);
                    },
                }, 'Next'),
                e('span', {key: 'range', className: 'hw-pager-range'}, state.tasks.length === 0
                    ? 'Page ' + (page + 1)
                    : (first + 1) + '–' + (first + state.tasks.length)),
            ]),
        ]);
    }

    // --- Meeting Intelligence deep link -------------------------------------

    // Clicking "Meeting Summary" on a card has to open the summary for THAT
    // meeting, not a panel the user then has to search.
    //
    // The card lives in the centre channel and the panel lives in the RHS;
    // they are separate React trees with no shared state, so this is the
    // one small bus between them. It carries a meeting id and nothing else
    // -- the panel still fetches through the authenticated API, so the
    // server re-proves channel membership exactly as it would for any other
    // request. A meeting id here grants nothing.
    var MeetingIntent = {
        meetingId: null,
        // Set by open() and read by a panel that was still mounting when the
        // intent was raised -- the same handshake AIIntent uses, and the
        // reason a card click can open the panel straight onto this tab.
        wanted: false,
        listeners: [],

        // Remembered per channel so a refresh comes back to the meeting the
        // user was reading. sessionStorage can throw in a private window or
        // where site data is blocked, so every access is guarded and the
        // panel works perfectly well without it.
        key: function (channelId) {
            return 'honco.meeting.selected.' + (channelId || 'none');
        },
        remember: function (channelId, meetingId) {
            try {
                window.sessionStorage.setItem(this.key(channelId), meetingId || '');
            } catch (err) { /* not worth failing over */ }
        },
        recall: function (channelId) {
            try {
                return window.sessionStorage.getItem(this.key(channelId)) || null;
            } catch (err) {
                return null;
            }
        },

        open: function (meetingId) {
            if (!meetingId) {
                return;
            }
            this.meetingId = meetingId;
            this.wanted = true;
            // Open the panel first, exactly as AIIntent.open does. Without
            // this the card's "View Summary" only worked when the panel
            // already happened to be open: the intent was raised, no panel
            // was mounted to hear it, and the click did nothing at all.
            if (window.HoncoOpenWorkspace) {
                window.HoncoOpenWorkspace();
            }
            this.listeners.forEach(function (fn) {
                try {
                    fn(meetingId);
                } catch (err) { /* one bad listener must not stop the rest */ }
            });
        },
        subscribe: function (fn) {
            this.listeners.push(fn);
            var self = this;
            return function () {
                var i = self.listeners.indexOf(fn);
                if (i >= 0) {
                    self.listeners.splice(i, 1);
                }
            };
        },
    };

    // What the card calls.
    window.HoncoOpenMeetingIntelligence = function (meetingId) {
        MeetingIntent.open(meetingId);
    };

    // --- Meeting Intelligence ----------------------------------------------

    // requestStatus is the variant used where "not found" is an ordinary
    // answer rather than an error: a meeting with no summary yet is the
    // normal state, not a failure to report.
    function requestStatus(method, path, body) {
        var opts = {
            method: method,
            credentials: 'same-origin',
            headers: {'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest'},
        };
        if (body) {
            opts.body = JSON.stringify(body);
        }
        return fetch(API + path, opts).then(function (res) {
            return res.json().catch(function () {
                return null;
            }).then(function (data) {
                return {status: res.status, ok: res.ok, data: data};
            });
        });
    }

    function meetingLabel(m) {
        return m.topic || m.room_name;
    }

    // --- Creating a meeting from the panel ---------------------------------

    // The form below is a front end for the /meet command that already
    // exists, not a second way to make a meeting: whatever it collects is
    // turned into the same command text a person could type, and Mattermost
    // runs it through the same slash-command path. meetsvc therefore keeps
    // sole ownership of room names, the Jitsi base address, scheduling and
    // reminders, and the plugin still registers the meeting exactly once,
    // through /meetings/register, with its card and lifecycle unchanged.
    // Nothing here talks to Jitsi or writes honco_meetings.
    function meetCommandFor(mode, title, whenMs) {
        // One line, no stray whitespace: the command is a single line of
        // text, and meetsvc strips it before matching.
        var topic = (title || '').replace(/\s+/g, ' ').trim().slice(0, 120);
        if (mode === 'now') {
            // An empty title is allowed here: bare /meet is the existing
            // "room for this channel" behaviour, named after the channel.
            return topic ? '/meet ' + topic : '/meet';
        }
        // parse_when understands "in <n> m|h" and "at HH:MM", and the clock
        // form only ever means today or tomorrow. Minutes-from-now is the
        // one spelling that can express any future date, so every scheduled
        // meeting uses it -- and a title is required, because meetsvc reads
        // the words after the time expression as the topic.
        var mins = Math.max(1, Math.round((whenMs - Date.now()) / 60000));
        return '/meet in ' + mins + 'm ' + topic;
    }

    // Channel members, for the optional participant picker. Read through
    // Mattermost's own API on the caller's session, so the list can only
    // ever contain people they are already allowed to see in this channel;
    // a private channel they are not in returns nothing at all. The picker
    // is convenience, never authorization -- mentioning someone posts an
    // ordinary message, which Mattermost authorizes on its own terms.
    function fetchChannelMembers(channelId) {
        if (!channelId) {
            return Promise.resolve([]);
        }
        return fetch('/api/v4/users?in_channel=' + encodeURIComponent(channelId) +
                     '&per_page=200&active=true', {
            credentials: 'same-origin',
            headers: {'X-Requested-With': 'XMLHttpRequest'},
        }).then(function (res) {
            return res.ok ? res.json() : [];
        }).then(function (users) {
            return (users || []).filter(function (u) {
                return !u.is_bot && u.delete_at === 0;
            }).sort(function (a, b) {
                return (a.username || '').localeCompare(b.username || '');
            });
        }).catch(function () {
            return [];
        });
    }

    // Run the command the way the composer would. Mattermost resolves the
    // session, checks the user may post in this channel, and dispatches to
    // meetsvc; the reply is the same ephemeral text /meet always returns.
    function runMeetCommand(channelId, teamId, command) {
        return fetch('/api/v4/commands/execute', {
            method: 'POST',
            credentials: 'same-origin',
            headers: {'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest'},
            body: JSON.stringify({channel_id: channelId, team_id: teamId, command: command}),
        }).then(function (res) {
            return res.json().catch(function () {
                return null;
            }).then(function (data) {
                if (!res.ok) {
                    throw new Error((data && (data.message || data.error)) || 'Could not create the meeting.');
                }
                return data;
            });
        });
    }

    // The join link out of the command's own reply, so an invitation can
    // carry it. Best effort: if the reply ever changes shape the invitation
    // simply goes out without the link rather than with a wrong one.
    function joinURLFromReply(data) {
        var text = (data && (data.text || data.message)) || '';
        var m = /https?:\/\/[^\s)<>`"']+/.exec(text);
        return m ? m[0] : '';
    }

    // "Participants" means: tell these people, in the channel the meeting
    // belongs to. Honco has no invitee record and no per-user meeting
    // notification, so this posts the one thing that genuinely reaches
    // them -- an @-mention, which Mattermost notifies on exactly as it
    // does for any other message. Nothing is stored against the meeting.
    function mentionParticipants(channelId, users, topic, whenLabel, joinUrl) {
        if (!users || !users.length) {
            return Promise.resolve(null);
        }
        var names = users.map(function (u) {
            return '@' + u.username;
        }).join(' ');
        var message = names + ' — you are invited to **' + topic + '** (' + whenLabel + ').';
        if (joinUrl) {
            message += '\n' + joinUrl;
        }
        return fetch('/api/v4/posts', {
            method: 'POST',
            credentials: 'same-origin',
            headers: {'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest'},
            body: JSON.stringify({channel_id: channelId, message: message}),
        }).then(function (res) {
            // A failed invitation must not read as a failed meeting: the
            // meeting exists either way, and the caller says so.
            return res.ok ? res.json().catch(function () {
                return null;
            }) : null;
        }).catch(function () {
            return null;
        });
    }

    function formatWhen(ms) {
        if (!ms) {
            return '';
        }
        return new Date(ms).toLocaleString(undefined, {
            day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit',
        });
    }

    // A generated section. Markdown from the summariser is rendered as
    // pre-wrapped text rather than parsed into HTML: this content comes
    // from a model reading user-written messages, so it is never treated
    // as markup.
    function Section(props) {
        if (!props.body) {
            return null;
        }
        return e('div', {style: {marginBottom: 14}}, [
            e('div', {
                key: 'h',
                style: {
                    fontSize: 11,
                    fontWeight: 700,
                    textTransform: 'uppercase',
                    letterSpacing: 0.4,
                    color: 'rgba(var(--center-channel-color-rgb), 0.64)',
                    marginBottom: 4,
                },
            }, props.title),
            e('div', {
                key: 'b',
                style: {
                    fontSize: 13,
                    lineHeight: 1.5,
                    whiteSpace: 'pre-wrap',
                    color: 'var(--center-channel-color)',
                },
            }, props.body),
        ]);
    }

    function StatusNote(props) {
        if (props.tone === 'error') {
            return e(ErrorNote, {}, props.children);
        }
        return e('div', {className: 'hw-note'}, props.children);
    }

    // Two decimal-padded helpers for the date/time inputs' value format.
    function pad2(n) {
        return (n < 10 ? '0' : '') + n;
    }

    function defaultMeetingWhen() {
        // Default to the next quarter hour, which is what someone booking a
        // meeting almost always wants and saves them setting two fields.
        var d = new Date(Date.now() + 15 * 60000);
        d.setSeconds(0, 0);
        d.setMinutes(Math.ceil(d.getMinutes() / 15) * 15);
        return {
            date: d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()),
            time: pad2(d.getHours()) + ':' + pad2(d.getMinutes()),
        };
    }

    // Local date+time strings -> epoch ms, or 0 if either is missing or the
    // pair does not parse. Built field by field rather than by parsing a
    // combined string, which browsers disagree about.
    function whenMillis(dateStr, timeStr) {
        var d = /^(\d{4})-(\d{2})-(\d{2})$/.exec(dateStr || '');
        var t = /^(\d{2}):(\d{2})$/.exec(timeStr || '');
        if (!d || !t) {
            return 0;
        }
        var ms = new Date(Number(d[1]), Number(d[2]) - 1, Number(d[3]),
            Number(t[1]), Number(t[2]), 0, 0).getTime();
        return isNaN(ms) ? 0 : ms;
    }

    // The "+ New Meeting" form. Start now or schedule for later; both ends
    // go through meetCommandFor -> /meet, so this component owns no meeting
    // state of its own and nothing here can drift from the command.
    function MeetingCreator(props) {
        var initial = defaultMeetingWhen();
        var f0 = React.useState({
            mode: 'now', title: '', date: initial.date, time: initial.time, participants: [],
        });
        var form = f0[0];
        var setForm = f0[1];

        var s0 = React.useState({saving: false, error: null});
        var state = s0[0];
        var setState = s0[1];

        var m0 = React.useState([]);
        var members = m0[0];
        var setMembers = m0[1];

        React.useEffect(function () {
            var live = true;
            fetchChannelMembers(props.channelId).then(function (list) {
                if (live) {
                    setMembers(list);
                }
            });
            return function () {
                live = false;
            };
        }, [props.channelId]);

        function set(key, value) {
            setForm(function (prev) {
                var next = {};
                Object.keys(prev).forEach(function (k) {
                    next[k] = prev[k];
                });
                next[key] = value;
                return next;
            });
        }

        var scheduled = form.mode === 'later';
        var whenMs = scheduled ? whenMillis(form.date, form.time) : 0;
        var titled = form.title.trim().length > 0;
        // A scheduled meeting needs a title: meetsvc reads the words after
        // the time expression as the topic, so "in 30m" with nothing after
        // it would be read as a meeting called "in 30m", starting now.
        var ready = !state.saving && (scheduled ? (titled && whenMs > Date.now()) : true);

        function submit(ev) {
            ev.preventDefault();
            if (!ready) {
                return;
            }
            setState({saving: true, error: null});
            var topic = form.title.trim() || 'this channel’s meeting';
            var whenLabel = scheduled ? formatWhen(whenMs) : 'starting now';
            var chosen = members.filter(function (u) {
                return form.participants.indexOf(u.id) >= 0;
            });

            runMeetCommand(props.channelId, props.teamId, meetCommandFor(form.mode, form.title, whenMs))
                .then(function (data) {
                    return mentionParticipants(props.channelId, chosen, topic, whenLabel,
                        joinURLFromReply(data));
                })
                .then(function () {
                    setState({saving: false, error: null});
                    props.onCreated();
                })
                .catch(function (err) {
                    setState({saving: false, error: err.message || 'Could not create the meeting.'});
                });
        }

        function toggleParticipant(id) {
            set('participants', form.participants.indexOf(id) >= 0
                ? form.participants.filter(function (x) {
                    return x !== id;
                })
                : form.participants.concat([id]));
        }

        return e('form', {onSubmit: submit, className: 'hw-form hw-fade'}, [
            // Meet now vs schedule: a real choice, made before anything
            // else, because it decides whether the rest of the form applies.
            e('div', {key: 'mode', className: 'hw-form-label'}, 'When'),
            e('div', {key: 'modes', className: 'hw-chips', style: {padding: '0 0 8px'}, role: 'radiogroup', 'aria-label': 'When'}, [
                {id: 'now', label: 'Meet now'},
                {id: 'later', label: 'Schedule for later'},
            ].map(function (opt) {
                return e('button', {
                    key: opt.id,
                    type: 'button',
                    role: 'radio',
                    className: 'hw-chip',
                    'aria-checked': form.mode === opt.id,
                    'aria-selected': form.mode === opt.id,
                    'data-meet-mode': opt.id,
                    onClick: function () {
                        set('mode', opt.id);
                    },
                }, opt.label);
            })),

            e('div', {key: 'tl', className: 'hw-form-label'}, 'Meeting title'),
            e('input', {
                key: 'title',
                className: 'hw-input',
                placeholder: scheduled ? 'Sprint review' : 'Optional — defaults to this channel',
                'aria-label': 'Meeting title',
                value: form.title,
                maxLength: 120,
                onChange: function (ev) {
                    set('title', ev.target.value);
                },
            }),

            scheduled ? e('div', {key: 'dl', className: 'hw-form-label'}, 'Date and time') : null,
            scheduled ? e('div', {key: 'dt', className: 'hw-actions', style: {marginBottom: 6}}, [
                e('input', {
                    key: 'date',
                    className: 'hw-input',
                    type: 'date',
                    'aria-label': 'Date',
                    style: {flex: '1 1 130px', marginBottom: 0},
                    value: form.date,
                    onChange: function (ev) {
                        set('date', ev.target.value);
                    },
                }),
                e('input', {
                    key: 'time',
                    className: 'hw-input',
                    type: 'time',
                    'aria-label': 'Time',
                    style: {flex: '1 1 100px', marginBottom: 0},
                    value: form.time,
                    onChange: function (ev) {
                        set('time', ev.target.value);
                    },
                }),
            ]) : null,
            (scheduled && whenMs > 0 && whenMs <= Date.now()) ? e('div', {
                key: 'past', className: 'hw-form-note', style: {color: 'var(--error-text)'},
            }, 'Pick a time in the future.') : null,

            e('div', {key: 'pl', className: 'hw-form-label'},
                'Participants (optional)' + (form.participants.length ? ' · ' + form.participants.length + ' selected' : '')),
            e('div', {key: 'pnote', className: 'hw-form-note'},
                'Mentions them in this channel so they are notified. Honco does not keep an invite list.'),
            members.length ? e('div', {
                key: 'people',
                className: 'hw-chips',
                style: {padding: '0 0 8px', maxHeight: 96, overflowY: 'auto'},
            }, members.map(function (u) {
                var on = form.participants.indexOf(u.id) >= 0;
                return e('button', {
                    key: u.id,
                    type: 'button',
                    className: 'hw-chip',
                    'aria-pressed': on,
                    'aria-selected': on,
                    'data-participant': u.id,
                    onClick: function () {
                        toggleParticipant(u.id);
                    },
                }, '@' + u.username);
            })) : e('div', {key: 'nopeople', className: 'hw-form-note'}, 'No other members to invite here.'),

            e('div', {key: 'rl', className: 'hw-form-label'}, 'Reminder'),
            e('div', {key: 'rnote', className: 'hw-form-note'}, scheduled
                ? 'A reminder is posted in this channel when the meeting starts. Honco Meet sends one reminder, at the meeting time; earlier reminders are not supported.'
                : 'Not applicable — the meeting starts immediately.'),

            state.error ? e('div', {
                key: 'err',
                role: 'alert',
                style: {color: 'var(--error-text)', fontSize: 12, marginBottom: 6},
            }, state.error) : null,

            e('div', {key: 'actions', className: 'hw-actions'}, [
                e(Button, {
                    key: 'create',
                    type: 'submit',
                    icon: scheduled ? 'calendar-outline' : 'video-outline',
                    disabled: !ready,
                }, state.saving ? 'Creating…' : (scheduled ? 'Create Meeting' : 'Start meeting')),
                e(Button, {key: 'cancel', kind: 'ghost', onClick: props.onCancel}, 'Cancel'),
            ]),
        ]);
    }

    function MeetingPanel() {
        var s0 = React.useState({channelId: null, meetings: [], statuses: {}, joinUrls: {}, loading: true, error: null});
        var state = s0[0];
        var setState = s0[1];

        // Whether the "+ New Meeting" form is open.
        var c0 = React.useState(false);
        var creating = c0[0];
        var setCreating = c0[1];

        var s1 = React.useState({id: null, summary: null, loading: false, error: null, generating: false});
        var sel = s1[0];
        var setSel = s1[1];

        var s2 = React.useState(false);
        var showRaw = s2[0];
        var setShowRaw = s2[1];

        // The channel comes from the webapp's own store, so the panel
        // always follows whichever channel the user is actually reading.
        // The team goes with it: a slash command is executed against both,
        // and the channel's own team is the right one even when the user
        // reached this channel from somewhere else.
        var channelId = null;
        var teamId = null;
        try {
            var mstate = window.store ? window.store.getState() : null;
            if (mstate) {
                channelId = mstate.entities.channels.currentChannelId;
                var mch = mstate.entities.channels.channels[channelId];
                teamId = (mch && mch.team_id) || mstate.entities.teams.currentTeamId;
            }
        } catch (err) {
            channelId = null;
            teamId = null;
        }

        // A card asked for a specific meeting: select it, whether the
        // panel was already open or is opening now.
        React.useEffect(function () {
            var stop = MeetingIntent.subscribe(function (meetingId) {
                loadSummary(meetingId);
            });
            // Honour an intent raised before this panel mounted, and
            // otherwise fall back to whatever was last read in this channel.
            var pending = MeetingIntent.meetingId || MeetingIntent.recall(channelId);
            if (pending) {
                MeetingIntent.meetingId = null;
                loadSummary(pending);
            }
            return stop;
        }, [channelId]);

        React.useEffect(function () {
            if (!channelId) {
                setState({channelId: null, meetings: [], statuses: {}, loading: false, error: null});
                return;
            }
            setState(function (p) {
                return {channelId: channelId, meetings: p.meetings, statuses: p.statuses, loading: true, error: null};
            });
            loadMeetings(channelId);
        }, [channelId]);

        // A card changing anywhere in this channel -- a meeting created
        // here or from the message box, one going live, one ending, a
        // recording arriving -- is already broadcast for the card itself.
        // The list listens to the same event rather than polling.
        React.useEffect(function () {
            var handler = function (msg) {
                var d = msg && msg.data;
                if (d && d.channel_id === channelId) {
                    loadMeetings(channelId);
                }
            };
            window.HoncoMeetingBus.push(handler);
            return function () {
                var i = window.HoncoMeetingBus.indexOf(handler);
                if (i >= 0) {
                    window.HoncoMeetingBus.splice(i, 1);
                }
            };
        }, [channelId]);

        function loadMeetings(chId) {
            if (!chId) {
                return Promise.resolve();
            }
            return requestStatus('GET', '/channels/' + encodeURIComponent(chId) + '/meetings').then(function (res) {
                if (!res.ok) {
                    setState({channelId: chId, meetings: [], statuses: {}, joinUrls: {}, loading: false,
                        error: res.status === 404 ? null : 'Could not load meetings.'});
                    return;
                }
                setState({
                    channelId: chId,
                    meetings: (res.data && res.data.meetings) || [],
                    statuses: (res.data && res.data.summary_status) || {},
                    joinUrls: (res.data && res.data.join_urls) || {},
                    loading: false,
                    error: null,
                });
            }).catch(function () {
                setState({channelId: chId, meetings: [], statuses: {}, joinUrls: {}, loading: false, error: 'Could not load meetings.'});
            });
        }

        function loadSummary(meetingId) {
            // Remembered so a refresh returns to the same meeting.
            MeetingIntent.remember(channelId, meetingId);
            setSel({id: meetingId, summary: null, loading: true, error: null, generating: false});
            setShowRaw(false);
            requestStatus('GET', '/meetings/' + encodeURIComponent(meetingId) + '/summary').then(function (res) {
                if (res.status === 404) {
                    // No summary yet is the normal starting state.
                    setSel({id: meetingId, summary: null, loading: false, error: null, generating: false});
                    return;
                }
                if (!res.ok) {
                    setSel({id: meetingId, summary: null, loading: false, error: 'Could not load the summary.', generating: false});
                    return;
                }
                setSel({id: meetingId, summary: res.data.summary, loading: false, error: null, generating: false});
            }).catch(function () {
                setSel({id: meetingId, summary: null, loading: false, error: 'Could not load the summary.', generating: false});
            });
        }

        // Generation is asynchronous on the server, so the panel polls.
        // The poll is bounded: a generation that never finishes leaves a
        // clear message rather than a spinner that turns forever.
        function poll(meetingId, attempt) {
            if (attempt > 60) {
                setSel(function (p) {
                    return {id: meetingId, summary: p.summary, loading: false, generating: false,
                        error: 'The summary is taking longer than expected. Try reopening this panel shortly.'};
                });
                return;
            }
            window.setTimeout(function () {
                requestStatus('GET', '/meetings/' + encodeURIComponent(meetingId) + '/summary').then(function (res) {
                    if (!res.ok || !res.data || !res.data.summary) {
                        poll(meetingId, attempt + 1);
                        return;
                    }
                    var summary = res.data.summary;
                    if (summary.status === 'pending') {
                        poll(meetingId, attempt + 1);
                        return;
                    }
                    setSel({id: meetingId, summary: summary, loading: false, error: null, generating: false});
                    setState(function (p) {
                        var next = Object.assign({}, p.statuses);
                        next[meetingId] = summary.status;
                        return {channelId: p.channelId, meetings: p.meetings, statuses: next, loading: false, error: p.error};
                    });
                }).catch(function () {
                    poll(meetingId, attempt + 1);
                });
            }, 3000);
        }

        function generate(meetingId, force) {
            setSel(function (p) {
                return {id: meetingId, summary: p.summary, loading: false, error: null, generating: true};
            });
            requestStatus('POST', '/meetings/' + encodeURIComponent(meetingId) + '/summary', {force: Boolean(force)})
                .then(function (res) {
                    if (!res.ok) {
                        setSel({id: meetingId, summary: null, loading: false, generating: false,
                            error: (res.data && res.data.error) || 'Could not start generation.'});
                        return;
                    }
                    var summary = res.data && res.data.summary;
                    if (summary && summary.status && summary.status !== 'pending') {
                        setSel({id: meetingId, summary: summary, loading: false, error: null, generating: false});
                        return;
                    }
                    poll(meetingId, 0);
                }).catch(function () {
                    setSel({id: meetingId, summary: null, loading: false, generating: false,
                        error: 'Could not start generation.'});
                });
        }

        if (!channelId) {
            return e('div', {className: 'hw'}, e(EmptyState, {icon: 'video-outline', title: 'No channel open'}, 'Open a channel to see its meetings.'));
        }
        if (state.loading) {
            return e('div', {className: 'hw'}, e(Loading, {label: 'Loading meetings'}));
        }
        if (state.error) {
            return e('div', {className: 'hw'}, e(ErrorNote, {}, state.error));
        }
        if (state.meetings.length === 0) {
            return e('div', {className: 'hw'}, e(EmptyState, {icon: 'video-outline', title: 'No meetings yet'},
                'No meetings have been held in this channel. Start one with /meet and a summary can be generated from the conversation afterwards.'));
        }

        var summary = sel.summary;
        var body = null;

        if (sel.loading) {
            body = e(Loading, {label: 'Loading summary'});
        } else if (sel.generating || (summary && summary.status === 'pending')) {
            body = e('div', {}, [
                e(Loading, {key: 'l', label: 'Generating summary'}),
                e(StatusNote, {key: 'n'}, 'Generating the summary from this channel’s conversation… this can take a minute.'),
            ]);
        } else if (sel.error) {
            body = e('div', {}, [
                e(StatusNote, {key: 'e', tone: 'error'}, sel.error),
                e('div', {key: 'r', style: {padding: '0 12px 12px'}},
                    e(Button, {kind: 'secondary', icon: 'refresh', onClick: function () {
                        generate(sel.id, true);
                    }}, 'Try again')),
            ]);
        } else if (!summary) {
            body = e(EmptyState, {
                icon: 'text-box-outline',
                title: 'No summary yet',
                action: e(Button, {onClick: function () {
                    generate(sel.id, false);
                }}, 'Generate summary'),
            }, 'A summary can be generated from what was posted in this channel during the meeting.');
        } else if (summary.status === 'failed') {
            body = e('div', {}, [
                e(StatusNote, {key: 'e', tone: 'error'},
                    summary.error_message || 'Summary generation failed.'),
                e('div', {key: 'r', style: {padding: '0 12px 12px'}},
                    e(Button, {kind: 'secondary', icon: 'refresh', onClick: function () {
                        generate(sel.id, true);
                    }}, 'Try again')),
            ]);
        } else if (summary.status === 'empty') {
            body = e(EmptyState, {
                icon: 'text-box-outline',
                title: 'Nothing to summarise',
                action: e(Button, {kind: 'secondary', icon: 'refresh', onClick: function () {
                    generate(sel.id, true);
                }}, 'Regenerate'),
            }, summary.summary ||
                'Nothing was posted in this channel during the meeting, so there is nothing to summarise.');
        } else {
            body = e('div', {className: 'hw-fade', style: {padding: '12px'}}, [
                e(Section, {key: 's', title: 'Meeting summary', body: summary.summary}),
                e(Section, {key: 'k', title: 'Key discussion points', body: summary.key_points}),
                e(Section, {key: 'd', title: 'Decisions', body: summary.decisions}),
                e(Section, {key: 'a', title: 'Action items', body: summary.action_items}),
                e(Section, {key: 'p', title: 'Participants', body: summary.participants}),

                e('div', {
                    key: 'meta',
                    style: {
                        marginTop: 4,
                        paddingTop: 8,
                        borderTop: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
                        fontSize: 11,
                        color: 'rgba(var(--center-channel-color-rgb), 0.56)',
                    },
                }, 'From ' + summary.message_count + ' message' + (summary.message_count === 1 ? '' : 's') +
                    ' · ' + formatWhen(summary.window_start) + ' – ' + formatWhen(summary.window_end)),

                summary.raw_output ? e('div', {key: 'raw', style: {marginTop: 8}}, [
                    e(Button, {
                        key: 'toggle',
                        kind: 'link',
                        onClick: function () {
                            setShowRaw(!showRaw);
                        },
                    }, showRaw ? 'Hide full output' : 'Show full output'),
                    showRaw ? e('pre', {key: 'pre', className: 'hw-pre'}, summary.raw_output) : null,
                ]) : null,

                e('div', {key: 'regen', style: {marginTop: 12}},
                    e(Button, {
                        kind: 'secondary',
                        icon: 'refresh',
                        onClick: function () {
                            generate(sel.id, true);
                        },
                    }, 'Regenerate')),
            ]);
        }

        // Active / Upcoming / Past, from the lifecycle the server already
        // maintains -- this groups what honco_meetings says, it does not
        // decide it. A scheduled meeting stays "upcoming" until the poller
        // sees somebody in the room, which is when it becomes active.
        var groups = {active: [], upcoming: [], past: []};
        state.meetings.forEach(function (m) {
            if (m.status === 'active') {
                groups.active.push(m);
            } else if (m.status === 'scheduled') {
                groups.upcoming.push(m);
            } else {
                groups.past.push(m);
            }
        });
        groups.upcoming.sort(function (a, b) {
            return (a.scheduled_at || 0) - (b.scheduled_at || 0);
        });

        function meetingRow(m) {
            var join = state.joinUrls[m.id];
            var ready = state.statuses[m.id] === 'ready';
            var when = m.status === 'scheduled'
                ? formatWhen(m.scheduled_at)
                : formatWhen(m.started_at || m.created_at);
            return e('div', {
                key: m.id,
                className: 'hw-row' + (sel.id === m.id ? ' hw-row-selected' : ''),
                'data-meeting-id': m.id,
                style: {display: 'flex', alignItems: 'center', gap: 8},
            }, [
                e('button', {
                    key: 'pick',
                    type: 'button',
                    className: 'hw-btn-link',
                    style: {flex: 1, minWidth: 0, textAlign: 'left', whiteSpace: 'normal'},
                    'aria-label': meetingLabel(m) + ' — ' + when + (ready ? ' — summary ready' : ''),
                    onClick: function () {
                        loadSummary(m.id);
                    },
                }, [
                    e('span', {key: 't', className: 'hw-row-title', style: {display: 'block'}}, meetingLabel(m)),
                    e('span', {key: 'w', className: 'hw-row-meta', style: {display: 'block', marginTop: 2}},
                        when + (m.status === 'active' && m.participant_count
                            ? ' · ' + m.participant_count + (m.participant_count === 1 ? ' participant' : ' participants')
                            : '') + (ready ? ' · summary ✓' : '')),
                ]),
                (join && m.status !== 'ended') ? e('a', {
                    key: 'join',
                    className: 'hw-btn hw-btn-sm',
                    href: join,
                    target: '_blank',
                    rel: 'noopener noreferrer',
                    'data-meeting-join': m.id,
                }, 'Join') : null,
            ]);
        }

        function group(key, title, list, limit) {
            if (!list.length) {
                return null;
            }
            var shown = limit ? list.slice(0, limit) : list;
            return e('div', {key: key, 'data-meeting-group': key}, [
                e('div', {key: 'h', className: 'hw-section-title'}, title + ' · ' + list.length),
            ].concat(shown.map(meetingRow)).concat([
                list.length > shown.length ? e('div', {
                    key: 'more', className: 'hw-form-note', style: {padding: '4px 16px 8px'},
                }, list.length - shown.length + ' older, in the picker below') : null,
            ]));
        }

        return e('div', {className: 'hw'}, [
            e(Toolbar, {key: 'bar', icon: 'video-outline', title: 'Meetings'}, e(Button, {
                small: true,
                icon: 'plus',
                'data-testid': 'new-meeting',
                'aria-expanded': creating,
                // A meeting belongs to a channel: with none open there is
                // nothing to create it in, and the command would only fail.
                disabled: !channelId,
                title: channelId ? undefined : 'Open a channel to create a meeting',
                onClick: function () {
                    setCreating(!creating);
                },
            }, creating ? 'Close' : 'New Meeting')),

            creating ? e(MeetingCreator, {
                key: 'creator',
                channelId: channelId,
                teamId: teamId,
                onCancel: function () {
                    setCreating(false);
                },
                onCreated: function () {
                    // The card arrives over the WebSocket too, but the panel
                    // should not look empty for the round trip.
                    setCreating(false);
                    loadMeetings(channelId);
                },
            }) : null,

            state.loading ? e(Loading, {key: 'load', label: 'Loading meetings'}) : null,
            state.error ? e(ErrorNote, {key: 'lerr'}, state.error) : null,
            (!state.loading && !state.meetings.length && !creating) ? e(EmptyState, {
                key: 'none', icon: 'video-outline', title: 'No meetings in this channel yet',
            }, 'Use “New Meeting” above, or type /meet in the message box.') : null,

            e('div', {key: 'groups'}, [
                group('active', 'Active', groups.active),
                group('upcoming', 'Upcoming', groups.upcoming),
                group('past', 'Past', groups.past, 5),
            ]),

            e(Toolbar, {key: 'picker', icon: 'text-box-outline', title: 'Meeting Intelligence'}, e('select', {
                value: sel.id || '',
                className: 'hw-select',
                style: {margin: '4px 0 0', flexBasis: '100%'},
                'aria-label': 'Meeting',
                onChange: function (ev) {
                    if (ev.target.value) {
                        loadSummary(ev.target.value);
                    }
                },
            }, [e('option', {key: '', value: ''}, 'Choose a meeting…')].concat(
                state.meetings.map(function (m) {
                    var mark = state.statuses[m.id] === 'ready' ? ' ✓' : '';
                    return e('option', {key: m.id, value: m.id},
                        meetingLabel(m) + ' · ' + formatWhen(m.created_at) + mark);
                }),
            ))),

            e('div', {key: 'body', className: 'hw-list'},
                sel.id ? body : e(EmptyState, {icon: 'text-box-outline', title: 'Meeting Intelligence'},
                    'Choose a meeting above to read its summary, or generate one from the conversation.')),
        ]);
    }

    // --- AI Assistant ------------------------------------------------------

    // The assistant shows what the AI service produced for the meeting in
    // this channel: a live transcript, suggestions and insights while the
    // call runs, and the summary afterwards. Nothing here generates,
    // rewrites or invents any of it -- every line is what the server
    // stored from the service, and every update arrives over Mattermost's
    // own WebSocket (custom_<plugin>_ai_event, broadcast to the channel),
    // so the panel never polls and never holds a credential.

    var AI_WS_EVENT = 'custom_' + PLUGIN_ID + '_ai_event';

    var AIIntent = {
        meetingId: null,
        wanted: false,
        listeners: [],
        subscribe: function (fn) {
            this.listeners.push(fn);
            var self = this;
            return function () {
                var i = self.listeners.indexOf(fn);
                if (i >= 0) {
                    self.listeners.splice(i, 1);
                }
            };
        },
        // Called from a meeting card. Opens the Honco panel, switches it to
        // AI Assistant and selects THIS meeting -- never a guess, never a
        // hardcoded id.
        open: function (meetingId) {
            this.meetingId = meetingId || null;
            this.wanted = true;
            if (window.HoncoOpenWorkspace) {
                window.HoncoOpenWorkspace();
            }
            // A panel that is already open handles it now; one that is
            // still mounting reads `wanted`/`meetingId` when it arrives.
            this.listeners.forEach(function (fn) {
                try {
                    fn(meetingId || null);
                } catch (err) { /* one bad listener must not stop the rest */ }
            });
        },
    };
    window.HoncoOpenAIAssistant = function (meetingId) {
        AIIntent.open(meetingId);
    };

    // Status, as a person should read it: a glyph and a word, never colour
    // alone. `tone` picks the dot colour; `glyph` is what a screen reader
    // and a colour-blind reader get. `line` is the one-sentence explanation
    // shown under the header.
    var AI_STATUS = {
        not_configured: {label: 'Offline', glyph: '○', tone: '', line: 'AI Assistant is not set up on this server yet.'},
        idle: {label: 'Ready', glyph: '○', tone: '', line: 'Waiting for Honco AI to join this meeting.'},
        connecting: {label: 'Connecting', glyph: '●', tone: 'warn', line: 'Connecting to Honco AI…'},
        live: {label: 'Live', glyph: '●', tone: 'ok', line: 'AI Assistant is listening.'},
        reconnecting: {label: 'Reconnecting', glyph: '●', tone: 'warn', line: 'Connection interrupted. Reconnecting…'},
        ended: {label: 'Ended', glyph: '○', tone: '', line: 'AI session ended.'},
        processing: {label: 'Processing', glyph: '●', tone: 'warn', line: 'Preparing meeting insights…'},
        completed: {label: 'Completed', glyph: '✓', tone: 'ok', line: 'Meeting completed.'},
        unavailable: {label: 'Offline', glyph: '○', tone: 'err', line: 'AI Assistant is currently unavailable.'},
        failed: {label: 'Failed', glyph: '⚠', tone: 'err', line: 'Meeting insights could not be prepared.'},
    };

    // How a suggestion or insight kind from the service is presented. The
    // label is a small tag on the card; the card's headline is always what
    // the thing IS (a sales suggestion, an insight). Unknown kinds fall back
    // to the generic look rather than being hidden.
    var AI_KINDS = {
        suggestion: {icon: 'lightbulb-outline', label: 'Suggestion', accent: 'var(--button-bg)'},
        suggested_response: {icon: 'message-text-outline', label: 'Suggested response', accent: 'var(--button-bg)'},
        next_best_action: {icon: 'lightning-bolt-outline', label: 'Next best action', accent: 'var(--online-indicator)'},
        client_concern: {icon: 'alert-outline', label: 'Client concern', accent: 'var(--away-indicator)'},
        objection: {icon: 'alert-outline', label: 'Objection', accent: 'var(--away-indicator)'},
        opportunity: {icon: 'star-outline', label: 'Opportunity', accent: 'var(--online-indicator)'},
        buying_signal: {icon: 'star-outline', label: 'Buying signal', accent: 'var(--online-indicator)'},
        insight: {icon: 'lightbulb-outline', label: 'Insight', accent: 'var(--button-bg)'},
        interest: {icon: 'star-outline', label: 'Client interest', accent: 'var(--online-indicator)'},
        sentiment: {icon: 'emoticon-outline', label: 'Sentiment', accent: 'var(--button-bg)'},
        concern: {icon: 'alert-outline', label: 'Concern', accent: 'var(--away-indicator)'},
    };

    function aiKind(kind, fallback) {
        var k = (kind || '').toLowerCase().replace(/[\s-]+/g, '_');
        return AI_KINDS[k] || AI_KINDS[fallback] || AI_KINDS.suggestion;
    }

    function formatClock(ms) {
        if (!ms) {
            return '';
        }
        var d = new Date(ms);
        function two(n) {
            return (n < 10 ? '0' : '') + n;
        }
        var h = d.getHours();
        var ampm = h >= 12 ? 'PM' : 'AM';
        h = h % 12 || 12;
        return h + ':' + two(d.getMinutes()) + ' ' + ampm;
    }

    // What the panel should say for a connection that did not happen.
    // A class from the server, never a message from the transport.
    function aiErrorText(kind) {
        switch (kind) {
        case 'auth':
            return 'Honco AI declined this server’s credentials. An administrator needs to check the AI service token.';
        case 'timeout':
            return 'Honco AI did not answer in time.';
        case 'refused':
            return 'Honco AI declined this meeting.';
        case 'not_configured':
            return 'AI Assistant is not set up on this server yet.';
        default:
            return 'AI Assistant is currently unavailable.';
        }
    }

    // "Label: text" from the service becomes a label and a text; anything
    // else is shown as it came. Nothing is inferred about what the label
    // means -- the service chose it.
    function splitLabelled(s) {
        var m = /^([A-Za-z][A-Za-z /-]{1,40}):\s+(.+)$/.exec(s || '');
        return m ? {label: m[1], text: m[2]} : {label: '', text: s};
    }

    function StatusPill(props) {
        var st = AI_STATUS[props.status] || AI_STATUS.idle;
        var dot = st.tone === 'ok' ? 'var(--online-indicator)' :
            (st.tone === 'warn' ? 'var(--away-indicator)' :
                (st.tone === 'err' ? 'var(--error-text)' : 'rgba(var(--center-channel-color-rgb), 0.4)'));
        return e('span', {
            className: 'hw-ai-pill hw-ai-pill-' + (st.tone || 'muted'),
            'data-ai-status': props.status,
            role: 'status',
            'aria-label': 'AI Assistant status: ' + st.label,
        }, [
            e('span', {key: 'g', className: 'hw-ai-glyph' + (props.status === 'live' ? ' hw-ai-pulse' : ''), style: {color: dot}, 'aria-hidden': true}, st.glyph),
            e('span', {key: 'l'}, st.label),
        ]);
    }

    // One utterance. The speaker leads, the time sits with it, the text is
    // the body. An interim line is set apart so a person never mistakes a
    // guess for the record.
    function TranscriptLine(props) {
        var l = props.line;
        return e('div', {
            className: 'hw-ai-line' + (l.final ? '' : ' hw-ai-line-interim'),
            'data-seq': l.seq,
            'aria-label': (l.speaker || 'Speaker') + (l.final ? '' : ', still transcribing'),
        }, [
            e('div', {key: 'm', className: 'hw-ai-line-meta'}, [
                e('span', {key: 's', className: 'hw-ai-speaker'}, l.speaker || 'Speaker'),
                e('span', {key: 't', className: 'hw-ai-time'}, formatClock(l.at)),
                l.final ? null : e('span', {key: 'p', className: 'hw-ai-interim-tag'}, 'transcribing…'),
            ]),
            e('div', {key: 'x', className: 'hw-ai-text'}, l.text),
        ]);
    }

    // The latest suggestion is the card a salesperson reads mid-sentence, so
    // its headline says what it is and the text is the biggest thing on the
    // panel. A tag carries the service's kind; the footer says who said it.
    function SuggestionCard(props) {
        var s = props.suggestion;
        var k = aiKind(s.kind, 'suggestion');
        return e('div', {
            className: 'hw-ai-card' + (props.latest ? ' hw-ai-card-latest' : '') + (props.fresh ? ' hw-ai-card-new' : ''),
            style: {borderLeftColor: k.accent},
            'data-suggestion-id': s.id,
            role: props.latest ? 'status' : undefined,
            'aria-live': props.latest ? 'polite' : undefined,
        }, [
            e('div', {key: 'h', className: 'hw-ai-card-head'}, [
                e(Icon, {key: 'i', name: props.latest ? 'lightbulb-outline' : k.icon, style: {color: k.accent}}),
                e('span', {key: 'k', className: 'hw-ai-card-kind'}, props.latest ? 'Sales suggestion' : (s.title || k.label)),
                (props.latest && s.kind && k.label !== 'Suggestion') ? e('span', {key: 'tag', className: 'hw-badge hw-ai-tag'}, s.title || k.label) : null,
                props.fresh ? e('span', {key: 'new', className: 'hw-badge hw-badge-ok hw-ai-tag'}, 'New') : null,
                e('span', {key: 't', className: 'hw-ai-time hw-spacer'}, formatClock(s.at)),
            ]),
            e('div', {key: 'b', className: 'hw-ai-card-body'}, s.text),
            e('div', {key: 'f', className: 'hw-ai-card-foot'},
                'Suggested by ' + (s.source || 'Honco AI') + (s.status ? ' · ' + s.status : '')),
        ]);
    }

    // A compact insight row: one consistent glyph, the service's label, the
    // text. Labelled "Concern: ..." text is split so the label reads as one.
    function InsightRow(props) {
        var i = props.insight;
        var k = aiKind(i.kind, 'insight');
        var parts = i.title ? {label: i.title, text: i.text} : splitLabelled(i.text);
        return e('div', {className: 'hw-ai-insight', 'data-insight-id': i.id}, [
            e('span', {key: 'i', className: 'hw-ai-insight-icon', style: {color: k.accent}}, e(Icon, {name: k.icon})),
            e('div', {key: 'b', style: {minWidth: 0}}, [
                e('div', {key: 'k', className: 'hw-ai-card-kind'}, parts.label || k.label),
                e('div', {key: 't', className: 'hw-ai-insight-text'}, parts.text),
            ]),
        ]);
    }

    // A collapsible section of the completed view. A real button with
    // aria-expanded, so it works from the keyboard and reads correctly.
    function Section2(props) {
        var open = props.open;
        return e('section', {className: 'hw-ai-sec' + (open ? ' hw-ai-sec-open' : ''), 'aria-label': props.title}, [
            e('button', {
                key: 'h',
                type: 'button',
                className: 'hw-ai-sec-head',
                'aria-expanded': open,
                onClick: props.onToggle,
            }, [
                e(Icon, {key: 'i', name: props.icon}),
                e('span', {key: 't', className: 'hw-ai-sec-title'}, props.title),
                props.count != null ? e('span', {key: 'n', className: 'hw-badge'}, props.count) : null,
                e(Icon, {key: 'c', name: open ? 'chevron-up' : 'chevron-down', style: {marginLeft: 'auto', opacity: 0.6}}),
            ]),
            open ? e('div', {key: 'b', className: 'hw-ai-sec-body hw-fade'}, props.children) : null,
        ]);
    }

    function BulletList(props) {
        return e('ul', {className: 'hw-ai-list'}, (props.items || []).map(function (t, i) {
            return e('li', {key: i}, t);
        }));
    }

    // Action items read as a checklist. The boxes are visual: nothing here
    // creates a Honco task or writes anything back -- the service's list is
    // shown as the service gave it. "Owner: x" is split when present.
    function ActionList(props) {
        return e('ul', {className: 'hw-ai-actions'}, (props.items || []).map(function (t, i) {
            var m = /^(.*?)(?:\s+[—–-]\s+|\s*\()\s*(?:owner|assignee)\s*[:=]\s*([^)]+)\)?\s*$/i.exec(t);
            var text = m ? m[1] : t;
            var owner = m ? m[2] : '';
            return e('li', {key: i, className: 'hw-ai-action'}, [
                e('span', {key: 'b', className: 'hw-ai-checkbox', 'aria-hidden': true}),
                e('div', {key: 't', style: {minWidth: 0}}, [
                    e('div', {key: 'x', className: 'hw-ai-action-text'}, text),
                    owner ? e('div', {key: 'o', className: 'hw-ai-action-owner'}, 'Owner: ' + owner) : null,
                ]),
            ]);
        }));
    }

    // Client insights get a label/text hierarchy when the service labelled
    // them ("Interest: …", "Concern: …"); otherwise they are plain rows.
    function LabelledList(props) {
        return e('div', {className: 'hw-ai-labelled'}, (props.items || []).map(function (t, i) {
            var p = splitLabelled(t);
            return e('div', {key: i, className: 'hw-ai-labelled-row'}, [
                p.label ? e('div', {key: 'l', className: 'hw-ai-card-kind'}, p.label) : null,
                e('div', {key: 't', className: 'hw-ai-labelled-text'}, p.text),
            ]);
        }));
    }

    function AIPanel() {
        var channelId = null;
        try {
            channelId = window.store ? window.store.getState().entities.channels.currentChannelId : null;
        } catch (err) {
            channelId = null;
        }

        var c0 = React.useState({loaded: false, configured: false, service_configured: false, callback_configured: false});
        var cfg = c0[0];
        var setCfg = c0[1];

        var m0 = React.useState({loading: true, meetings: [], error: null});
        var ml = m0[0];
        var setMl = m0[1];

        var sel0 = React.useState('');
        var selected = sel0[0];
        var setSelected = sel0[1];

        // The session as last read from the server, plus the transcript
        // lines held by this panel (bounded; older pages are fetched on
        // demand and never all at once).
        var s0 = React.useState({loading: false, error: null, meeting: null, participants: [], session: null, lines: [], olderOnService: false});
        var st = s0[0];
        var setSt = s0[1];

        var ui0 = React.useState({showTranscript: false, showAll: false, olderLoading: false, starting: false, stopping: false, followLive: true});
        var ui = ui0[0];
        var setUi = ui0[1];

        var listRef = React.useRef(null);

        var w0 = React.useState(true);
        var wsUp = w0[0];
        var setWsUp = w0[1];

        // Feature wiring, once.
        React.useEffect(function () {
            request('GET', '/ai/status').then(function (d) {
                setCfg(Object.assign({loaded: true}, d || {}));
            }).catch(function () {
                setCfg({loaded: true, configured: false, service_configured: false, callback_configured: false});
            });
        }, []);

        // Meetings in this channel: pick the running one, else the latest.
        var loadMeetings = React.useCallback(function (chId) {
            if (!chId) {
                setMl({loading: false, meetings: [], error: null});
                return;
            }
            requestStatus('GET', '/channels/' + encodeURIComponent(chId) + '/meetings').then(function (res) {
                if (!res.ok) {
                    setMl({loading: false, meetings: [], error: res.status === 404 ? null : 'Could not load meetings.'});
                    return;
                }
                var list = (res.data && res.data.meetings) || [];
                setMl({loading: false, meetings: list, error: null});
                setSelected(function (cur) {
                    if (cur && list.some(function (m) { return m.id === cur; })) {
                        return cur;
                    }
                    var active = list.filter(function (m) { return m.status === 'active'; })[0];
                    return (active || list[0] || {}).id || '';
                });
            }).catch(function () {
                setMl({loading: false, meetings: [], error: 'Could not load meetings.'});
            });
        }, []);

        React.useEffect(function () {
            loadMeetings(channelId);
        }, [channelId, loadMeetings]);

        // A card asked for a specific meeting: select it, whether the panel
        // was already open or is opening now.
        React.useEffect(function () {
            var stop = AIIntent.subscribe(function (meetingId) {
                if (meetingId) {
                    setSelected(meetingId);
                }
            });
            if (AIIntent.meetingId) {
                setSelected(AIIntent.meetingId);
                AIIntent.meetingId = null;
            }
            return stop;
        }, []);

        // Whether this browser's WebSocket is up. Read from the webapp's own
        // store rather than by asking the network: a panel that says
        // "connected" while the socket is down would be lying, and polling
        // the server to find out would be worse.
        React.useEffect(function () {
            if (!window.store || !window.store.subscribe) {
                return undefined;
            }
            var read = function () {
                try {
                    var ws = window.store.getState().websocket;
                    return !ws || ws.connected !== false;
                } catch (err) {
                    return true;
                }
            };
            setWsUp(read());
            return window.store.subscribe(function () {
                var up = read();
                setWsUp(function (prev) {
                    return prev === up ? prev : up;
                });
            });
        }, []);

        // The session for the selected meeting: read once on select and
        // again after a reconnect; everything in between is the WebSocket.
        var loadSession = React.useCallback(function (meetingId) {
            if (!meetingId) {
                setSt({loading: false, error: null, meeting: null, participants: [], session: null, lines: [], olderOnService: false});
                return;
            }
            setSt(function (p) {
                return Object.assign({}, p, {loading: true, error: null});
            });
            requestStatus('GET', '/meetings/' + encodeURIComponent(meetingId) + '/ai').then(function (res) {
                if (!res.ok) {
                    setSt({loading: false, error: res.status === 404 ? 'This meeting is not available to you.' : 'Could not load the assistant.', meeting: null, participants: [], session: null, lines: [], olderOnService: false});
                    return;
                }
                var s = res.data.session || {};
                var lines = s.transcript || [];
                setSt({
                    loading: false, error: null, notice: null,
                    meeting: res.data.meeting, participants: res.data.participants || [],
                    session: s, lines: lines,
                    olderOnService: Boolean(s.transcript_gap),
                });
                setUi(function (u) {
                    return Object.assign({}, u, {followLive: true});
                });
            }).catch(function () {
                setSt(function (p) {
                    return Object.assign({}, p, {loading: false, error: 'Could not load the assistant.'});
                });
            });
        }, []);

        React.useEffect(function () {
            loadSession(selected);
        }, [selected, loadSession]);

        // Live updates. A delta is applied in place; anything the panel
        // cannot apply (a status it does not know) triggers a re-read.
        React.useEffect(function () {
            var handler = function (msg) {
                var d = msg && msg.data;
                if (!d || d.meeting_id !== selected) {
                    return;
                }
                setSt(function (p) {
                    if (!p.session) {
                        return p;
                    }
                    var s = Object.assign({}, p.session, {
                        status: d.status || p.session.status,
                        capture_status: d.capture_status,
                        error_kind: d.error_kind,
                        line_count: d.line_count != null ? d.line_count : p.session.line_count,
                        events_received: d.events_received != null ? d.events_received : p.session.events_received,
                    });
                    var lines = p.lines;
                    if (d.type === 'transcript' && d.lines && d.lines.length) {
                        var lastSeq = lines.length ? lines[lines.length - 1].seq : -1;
                        var fresh = d.lines.filter(function (l) { return l.seq > lastSeq; });
                        lines = lines.concat(fresh);
                        // Bounded in memory: the live view is a window.
                        if (lines.length > 400) {
                            lines = lines.slice(lines.length - 400);
                        }
                    } else if (d.type === 'suggestion' && d.suggestion) {
                        s.suggestions = (s.suggestions || []).filter(function (x) { return x.id !== d.suggestion.id; }).concat([d.suggestion]).slice(-50);
                    } else if (d.type === 'insight' && d.insight) {
                        s.insights = (s.insights || []).filter(function (x) { return x.id !== d.insight.id; }).concat([d.insight]).slice(-50);
                    } else if (d.type === 'topics') {
                        s.topics = d.topics || [];
                    } else if (d.type === 'final') {
                        s.final = d.final;
                        if (d.final && d.final.topics) {
                            s.topics = d.final.topics;
                        }
                    }
                    return Object.assign({}, p, {session: s, lines: lines});
                });
            };
            window.HoncoAIBus.push(handler);
            return function () {
                var i = window.HoncoAIBus.indexOf(handler);
                if (i >= 0) {
                    window.HoncoAIBus.splice(i, 1);
                }
            };
        }, [selected]);

        // Meeting lifecycle (the meeting card's own event): a meeting
        // starting or ending in this channel changes which one is shown.
        React.useEffect(function () {
            var handler = function (msg) {
                var d = msg && msg.data;
                if (d && d.channel_id === channelId) {
                    loadMeetings(channelId);
                    if (d.meeting_id === selected) {
                        loadSession(selected);
                    }
                }
            };
            window.HoncoMeetingBus.push(handler);
            return function () {
                var i = window.HoncoMeetingBus.indexOf(handler);
                if (i >= 0) {
                    window.HoncoMeetingBus.splice(i, 1);
                }
            };
        }, [channelId, selected, loadMeetings, loadSession]);

        // After the WebSocket reconnects, anything missed is re-read.
        React.useEffect(function () {
            var handler = function () {
                if (selected) {
                    loadSession(selected);
                }
            };
            window.HoncoReconnectBus.push(handler);
            return function () {
                var i = window.HoncoReconnectBus.indexOf(handler);
                if (i >= 0) {
                    window.HoncoReconnectBus.splice(i, 1);
                }
            };
        }, [selected, loadSession]);

        // Keep the live transcript pinned to the newest line unless the
        // reader scrolled up to read something.
        React.useEffect(function () {
            var el = listRef.current;
            if (el && ui.followLive) {
                el.scrollTop = el.scrollHeight;
            }
        }, [st.lines.length, ui.followLive]);

        function onScroll(ev) {
            var el = ev.target;
            var atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
            if (atBottom !== ui.followLive) {
                setUi(Object.assign({}, ui, {followLive: atBottom}));
            }
        }

        function loadOlder() {
            if (!selected || !st.lines.length || ui.olderLoading) {
                return;
            }
            setUi(Object.assign({}, ui, {olderLoading: true}));
            var before = st.lines[0].seq;
            requestStatus('GET', '/meetings/' + encodeURIComponent(selected) + '/ai/transcript?before=' + before + '&limit=200').then(function (res) {
                setUi(function (u) {
                    return Object.assign({}, u, {olderLoading: false, followLive: false});
                });
                if (!res.ok) {
                    return;
                }
                var older = res.data.lines || [];
                setSt(function (p) {
                    var lines = older.concat(p.lines);
                    if (lines.length > 1000) {
                        lines = lines.slice(0, 1000); // the DOM stays bounded; the newest are still one reload away
                    }
                    return Object.assign({}, p, {
                        lines: lines,
                        olderOnService: Boolean(res.data.older_on_service),
                    });
                });
            }).catch(function () {
                setUi(function (u) {
                    return Object.assign({}, u, {olderLoading: false});
                });
            });
        }

        // Start and Stop are idempotent by construction: the server treats a
        // start on a live session as a no-op and an end on an ended one as
        // already done, so a double click cannot make two sessions.
        function sessionCall(method, busyKey) {
            if (!selected || ui[busyKey]) {
                return;
            }
            var next = {};
            next[busyKey] = true;
            setUi(Object.assign({}, ui, next));
            requestStatus(method, '/meetings/' + encodeURIComponent(selected) + '/ai/session', method === 'POST' ? {} : null)
                .then(function (res) {
                    var done = {};
                    done[busyKey] = false;
                    setUi(function (u) {
                        return Object.assign({}, u, done);
                    });
                    if (res.data && res.data.session) {
                        setSt(function (p) {
                            return Object.assign({}, p, {session: res.data.session});
                        });
                    }
                    setSt(function (p) {
                        return Object.assign({}, p, {notice: (res.data && res.data.error) || null});
                    });
                }).catch(function () {
                    var done = {};
                    done[busyKey] = false;
                    setUi(function (u) {
                        return Object.assign({}, u, done);
                    });
                });
        }

        function startSession() {
            sessionCall('POST', 'starting');
        }

        function stopSession() {
            sessionCall('DELETE', 'stopping');
        }

        // Reconnect is "forget what this browser thinks and ask the server
        // again", plus a start if the service had dropped out. It never
        // invents state.
        function reconnect() {
            loadMeetings(channelId);
            loadSession(selected);
            if (cfg.service_configured && st.session &&
                (st.session.status === 'unavailable' || st.session.status === 'failed')) {
                startSession();
            }
        }

        // ---- render -------------------------------------------------------

        var session = st.session;
        var status = session ? session.status : (cfg.loaded && !cfg.configured ? 'not_configured' : 'idle');
        var meeting = st.meeting;
        var finished = session && (session.status === 'completed' || session.status === 'ended' || (meeting && meeting.status === 'ended'));
        // "Live" describes the assistant's view of the call. Once the session
        // is finished it must not still say Live, even if the Jitsi room is
        // technically still open.
        var meetingLive = Boolean(meeting && meeting.status === 'active' && !finished);
        // Whether anything precedes the first line on screen -- asked of what
        // is rendered right now, not of what was true when the panel opened.
        var hasOlder = st.lines.length > 0 && st.lines[0].seq > 0;
        var hasFinal = Boolean(session && session.final && (session.final.summary ||
            (session.final.key_points || []).length || (session.final.action_items || []).length ||
            (session.final.decisions || []).length || (session.final.key_insights || []).length ||
            (session.final.client_insights || []).length));

        // The status a person reads. The transport's own state comes first
        // (a down socket makes everything else stale), then the service's.
        // A session that ended with the service present but no summary yet
        // is "processing": the summary normally follows the end of the call.
        var viewStatus = status;
        if (!wsUp) {
            viewStatus = 'reconnecting';
        } else if (status === 'ended' && session && session.events_received > 0 && !hasFinal &&
            meeting && meeting.status === 'ended') {
            // The call is over and the service was there: its summary
            // normally follows. A session a person stopped mid-call is
            // simply ended.
            viewStatus = 'processing';
        } else if (status === 'completed' && !hasFinal) {
            viewStatus = 'processing';
        }
        var statusInfo = AI_STATUS[viewStatus] || AI_STATUS.idle;

        // "New" on the latest suggestion for a few seconds after it arrives.
        // The first suggestion seen after opening is not new -- it is what
        // was already there.
        var latestSug = (session && session.suggestions && session.suggestions.length)
            ? session.suggestions[session.suggestions.length - 1] : null;
        var latestSugId = latestSug ? latestSug.id : null;
        var fr = React.useState({id: null, fresh: false});
        var freshState = fr[0];
        var setFresh = fr[1];
        React.useEffect(function () {
            if (!latestSugId || freshState.id === latestSugId) {
                return undefined;
            }
            var first = freshState.id === null;
            setFresh({id: latestSugId, fresh: !first});
            if (first) {
                return undefined;
            }
            var t = window.setTimeout(function () {
                setFresh(function (p) {
                    return Object.assign({}, p, {fresh: false});
                });
            }, 6000);
            return function () {
                window.clearTimeout(t);
            };
        }, [latestSugId]);

        // Which sections of the completed view are open. The ones a person
        // reads first are open; the rest are a click away.
        var sc = React.useState({summary: true, key_points: false, decisions: false, action_items: true, key_insights: true, client_insights: true, topics: true, transcript: false});
        var sections = sc[0];
        var setSections = sc[1];
        function toggleSection(name) {
            return function () {
                var next = {};
                next[name] = !sections[name];
                setSections(Object.assign({}, sections, next));
                if (name === 'transcript') {
                    setUi(Object.assign({}, ui, {showTranscript: !sections.transcript, followLive: false}));
                }
            };
        }

        // ---- header ---------------------------------------------------------

        // The controls a person actually has, per state (never contradictory):
        //   live / connecting  -> Stop session, Reconnect
        //   idle (allowed)     -> Start AI session, Reconnect
        //   ended/failed/offline, meeting still running -> Start new AI session, Reconnect
        //   completed          -> Reconnect
        var canStart = Boolean(session && meeting && cfg.service_configured && !finished &&
            status !== 'live' && status !== 'connecting');
        var canRestart = Boolean(session && meeting && cfg.service_configured && meeting.status === 'active' &&
            (status === 'ended' || status === 'failed' || status === 'unavailable'));
        var canStop = Boolean(session && (status === 'live' || status === 'connecting'));

        // With nothing to show yet the body is an introduction whose one
        // button IS the start control, so the header does not repeat it.
        var hasContentNow = Boolean(session && (st.lines.length || (session.suggestions || []).length || (session.insights || []).length));
        var showIntro = Boolean(session && !finished && !hasContentNow &&
            (status === 'idle' || status === 'not_configured' || status === 'unavailable' || status === 'failed' || status === 'connecting'));

        var controls = null;
        if (session && meeting) {
            controls = e('div', {key: 'controls', className: 'hw-ai-controls'}, [
                canStop ? e(Button, {
                    key: 'stop', kind: 'ghost', small: true, icon: 'close', disabled: ui.stopping, onClick: stopSession,
                }, ui.stopping ? 'Stopping…' : 'Stop session') : null,
                (canStart && !canRestart && !showIntro) ? e(Button, {
                    key: 'start', small: true, icon: 'play', disabled: ui.starting, onClick: startSession,
                }, ui.starting ? 'Starting…' : 'Start AI session') : null,
                (canRestart && !showIntro) ? e(Button, {
                    key: 'restart', small: true, icon: 'play', disabled: ui.starting, onClick: startSession,
                }, ui.starting ? 'Starting…' : 'Start new AI session') : null,
                e(Button, {
                    key: 'rc', kind: 'ghost', small: true, icon: 'refresh', onClick: reconnect,
                    'aria-label': 'Reconnect',
                }, 'Reconnect'),
            ]);
        }

        var header = e('div', {key: 'head', className: 'hw-ai-head'}, [
            e('div', {key: 'r1', className: 'hw-ai-head-row'}, [
                e('span', {key: 'i', className: 'hw-ai-avatar', 'aria-hidden': true}, e(Icon, {name: 'creation-outline'})),
                e('span', {key: 'n', className: 'hw-ai-head-name'}, 'Honco AI Assistant'),
                e(StatusPill, {key: 'st', status: viewStatus}),
            ]),
            meeting ? e('div', {key: 'r2', className: 'hw-ai-head-meeting', title: meetingLabel(meeting)}, meetingLabel(meeting)) : null,
            e('div', {key: 'r3', className: 'hw-ai-head-line'}, [
                e('span', {key: 's'}, (viewStatus === 'idle' && meetingLive && (canStart || canRestart))
                    ? 'Start an AI session while your meeting is active.' : statusInfo.line),
                (meetingLive && st.participants.length) ? e('span', {key: 'p', className: 'hw-ai-head-sep', title: st.participants.join(', ')},
                    st.participants.length + ' in the call') : null,
                // The service's capture status, only when it adds something
                // ("paused", "not recording"); "listening" is what the status
                // line already says.
                (session && session.capture_status && !finished && wsUp && !/^(listening|live|recording)$/i.test(session.capture_status.trim()))
                    ? e('span', {key: 'c', className: 'hw-ai-head-sep'}, session.capture_status) : null,
            ]),
            controls,
        ]);

        // ---- body -----------------------------------------------------------

        var body;
        if (!channelId) {
            body = e(EmptyState, {icon: 'creation-outline', title: 'No channel open'}, 'Open a channel to see its meetings.');
        } else if (ml.loading || (st.loading && !session)) {
            body = e(Loading, {label: 'Loading assistant'});
        } else if (ml.error) {
            body = e(ErrorNote, {}, ml.error);
        } else if (!ml.meetings.length) {
            body = e(EmptyState, {icon: 'video-outline', title: 'No active meeting found'},
                'Start a call with /meet in this channel. The assistant becomes available when the meeting begins.');
        } else if (st.error) {
            body = e(ErrorNote, {}, st.error);
        } else if (!session) {
            body = e(Loading, {label: 'Loading assistant'});
        } else {
            var parts = [];

            // Which meeting, when there is more than one to choose from.
            if (ml.meetings.length > 1) {
                parts.push(e('div', {key: 'pick', className: 'hw-ai-pick'}, e('select', {
                    className: 'hw-select', style: {margin: 0}, 'aria-label': 'Meeting for the assistant',
                    value: selected,
                    onChange: function (ev) {
                        setSelected(ev.target.value);
                        setUi(Object.assign({}, ui, {showTranscript: false, showAll: false}));
                    },
                }, ml.meetings.map(function (m) {
                    return e('option', {key: m.id, value: m.id},
                        meetingLabel(m) + ' · ' + formatWhen(m.created_at) + (m.status === 'active' ? ' · live' : ''));
                }))));
            }

            var hasContent = hasContentNow;

            if (finished) {
                parts.push(e(AIFinished, {
                    key: 'done', session: session, meeting: meeting, lines: st.lines, ui: ui, setUi: setUi,
                    viewStatus: viewStatus, hasFinal: hasFinal, sections: sections, toggleSection: toggleSection,
                    hasOlder: hasOlder, olderOnService: st.olderOnService, loadOlder: loadOlder,
                    listRef: listRef, onScroll: onScroll,
                }));
            } else if (showIntro) {
                // Nothing to show yet: say what the assistant does, what state
                // it is in, and the one action that applies.
                var introIcon = status === 'connecting' ? 'creation-outline' :
                    (status === 'failed' || status === 'unavailable') ? 'alert-circle-outline' :
                        (status === 'not_configured' ? 'power-plug-outline' : 'creation-outline');
                var introTitle = status === 'connecting' ? 'Connecting to Honco AI…' :
                    status === 'failed' ? 'Meeting insights could not be prepared' :
                        status === 'unavailable' ? 'AI Assistant is currently unavailable' :
                            status === 'not_configured' ? 'AI Assistant is not set up yet' : 'Honco AI Assistant';
                var introText = status === 'connecting' ? 'Live transcript and suggestions appear here as soon as the service joins the call.' :
                    status === 'failed' ? 'The AI service could not process this meeting. You can try again while the meeting is running.' :
                        status === 'unavailable' ? aiErrorText(session.error_kind) :
                            status === 'not_configured' ? 'An administrator needs to connect the Honco AI service in System Console › Plugins › Honco Workspace.' :
                                'Get real-time meeting assistance, sales suggestions and meeting insights. ' +
                                (!meetingLive ? 'The assistant becomes available when your meeting is active.' :
                                    (canStart || canRestart) ? 'Start an AI session while your meeting is active.' :
                                        'Honco AI joins automatically once it is connected to this meeting.');
                var introAction = null;
                if (status === 'connecting') {
                    introAction = e(Loading, {label: 'Connecting'});
                } else if ((canStart || canRestart) && meetingLive) {
                    introAction = e(Button, {icon: 'play', disabled: ui.starting, onClick: startSession},
                        ui.starting ? 'Starting…' : (canRestart ? 'Start new AI session' : 'Start AI session'));
                } else if ((status === 'unavailable' || status === 'failed') && cfg.service_configured) {
                    introAction = e(Button, {kind: 'secondary', icon: 'refresh', disabled: ui.starting, onClick: reconnect},
                        ui.starting ? 'Retrying…' : 'Retry connection');
                }
                parts.push(e('div', {key: 'intro', className: 'hw-ai-intro'}, e(EmptyState, {
                    icon: introIcon, title: introTitle, action: introAction,
                }, introText)));
            } else {
                parts.push(e(AILive, {
                    key: 'live', session: session, lines: st.lines, ui: ui, setUi: setUi,
                    hasOlder: hasOlder, olderOnService: st.olderOnService, loadOlder: loadOlder,
                    listRef: listRef, onScroll: onScroll, status: status, errorKind: session.error_kind,
                    canRetry: cfg.service_configured, startSession: startSession, reconnect: reconnect,
                    freshId: freshState.fresh ? freshState.id : null,
                }));
            }
            body = e('div', {className: 'hw-list'}, parts);
        }

        return e('div', {className: 'hw hw-ai', 'data-ai-panel': selected || '', 'data-ai-view': viewStatus}, [
            header,
            wsUp ? null : e('div', {key: 'ws', className: 'hw-ai-banner', role: 'status'}, [
                e(Icon, {key: 'i', name: 'refresh'}),
                e('span', {key: 't'}, 'Connection interrupted. Reconnecting…'),
            ]),
            st.notice ? e('div', {key: 'notice', className: 'hw-note', style: {paddingBottom: 0}}, st.notice) : null,
            body,
        ]);
    }

    // Live view, in the order a person on a call needs it: the suggestion
    // they can use right now, then what the assistant has noticed, then the
    // topics, then the transcript. On a phone that is simply the stack.
    function AILive(props) {
        var s = props.session;
        var sugg = s.suggestions || [];
        var latest = sugg.length ? sugg[sugg.length - 1] : null;
        var previous = sugg.slice(0, -1).reverse();
        var insights = (s.insights || []).slice(-4).reverse();
        var out = [];

        if (props.status === 'unavailable' || props.status === 'failed') {
            out.push(e('div', {key: 'warn', className: 'hw-ai-banner hw-ai-banner-err', role: 'alert'}, [
                e(Icon, {key: 'i', name: 'alert-circle-outline'}),
                e('span', {key: 't', style: {flex: 1}}, props.status === 'failed'
                    ? 'Meeting insights could not be prepared. What arrived before is shown below.'
                    : aiErrorText(props.errorKind)),
                props.canRetry ? e(Button, {key: 'r', kind: 'link', onClick: props.reconnect}, 'Retry') : null,
            ]));
        }

        out.push(e('div', {key: 'sugg', className: 'hw-ai-block hw-ai-block-primary'}, [
            latest ? e(SuggestionCard, {key: 'latest', suggestion: latest, latest: true, fresh: props.freshId === latest.id}) :
                e('div', {key: 'none', className: 'hw-ai-card hw-ai-card-empty'}, [
                    e('div', {key: 'h', className: 'hw-ai-card-head'}, [
                        e(Icon, {key: 'i', name: 'lightbulb-outline'}),
                        e('span', {key: 'k', className: 'hw-ai-card-kind'}, 'Sales suggestion'),
                    ]),
                    e('div', {key: 'q', className: 'hw-ai-quiet'}, props.status === 'live'
                        ? 'Listening for the conversation. Suggestions appear here as Honco AI sends them.'
                        : 'No suggestions yet.'),
                ]),
            previous.length ? e('div', {key: 'prev'}, [
                e(Button, {
                    key: 'b', kind: 'link', icon: props.ui.showAll ? 'chevron-up' : 'chevron-down',
                    'aria-expanded': props.ui.showAll,
                    onClick: function () {
                        props.setUi(Object.assign({}, props.ui, {showAll: !props.ui.showAll}));
                    },
                }, (props.ui.showAll ? 'Hide' : 'Show') + ' previous suggestions (' + previous.length + ')'),
                props.ui.showAll ? e('div', {key: 'l', className: 'hw-ai-prev hw-fade'}, previous.map(function (x) {
                    return e(SuggestionCard, {key: x.id, suggestion: x});
                })) : null,
            ]) : null,
        ]));

        if (insights.length || (s.topics || []).length) {
            out.push(e('div', {key: 'ins', className: 'hw-ai-block'}, [
                insights.length ? e('div', {key: 'h', className: 'hw-ai-block-title'}, [
                    e(Icon, {key: 'i', name: 'lightning-bolt-outline'}), 'Insights',
                ]) : null,
                insights.length ? e('div', {key: 'rows', className: 'hw-ai-insights'}, insights.map(function (x) {
                    return e(InsightRow, {key: x.id, insight: x});
                })) : null,
                (s.topics || []).length ? e('div', {key: 'th', className: 'hw-ai-block-title', style: {marginTop: insights.length ? 10 : 0}}, [
                    e(Icon, {key: 'i', name: 'star-outline'}), 'Topics',
                ]) : null,
                (s.topics || []).length ? e('div', {key: 'topics', className: 'hw-ai-topics', role: 'list', 'aria-label': 'Detected topics'}, (s.topics || []).map(function (t, i) {
                    return e('span', {key: i, className: 'hw-chip', role: 'listitem'}, t);
                })) : null,
            ]));
        }

        out.push(e(TranscriptBlock, {
            key: 'tr', title: 'Live transcript', live: props.status === 'live', session: s,
            lines: props.lines, listRef: props.listRef, onScroll: props.onScroll, ui: props.ui, setUi: props.setUi,
            hasOlder: props.hasOlder, olderOnService: props.olderOnService, loadOlder: props.loadOlder,
            emptyText: props.status === 'live' ? 'Listening… the transcript appears here as Honco AI sends it.' : 'Transcript unavailable.',
        }));
        return e('div', {}, out);
    }

    function TranscriptBlock(props) {
        var s = props.session;
        var lines = props.lines;
        return e('div', {className: 'hw-ai-block' + (props.bare ? ' hw-ai-block-bare' : '')}, [
            props.bare ? null : e('div', {key: 'h', className: 'hw-ai-block-title'}, [
                e(Icon, {key: 'i', name: 'message-text-outline'}), props.title,
                props.live ? e('span', {key: 'live', className: 'hw-badge hw-badge-ok hw-ai-tag'}, 'Live') : null,
                s.line_count ? e('span', {key: 'n', className: 'hw-spacer hw-ai-count'},
                    s.line_count + (s.line_count === 1 ? ' line' : ' lines')) : null,
            ]),
            (props.hasOlder || props.olderOnService) ? e('div', {key: 'older', className: 'hw-ai-older'},
                props.hasOlder ? e(Button, {kind: 'link', icon: 'arrow-up', disabled: props.ui.olderLoading, onClick: props.loadOlder},
                    props.ui.olderLoading ? 'Loading…' : 'Load earlier lines') :
                    e('span', {}, 'Earlier lines are held by the AI service.')) : null,
            lines.length ? e('div', {
                key: 'list', className: 'hw-ai-transcript', ref: props.listRef, onScroll: props.onScroll,
                role: 'log', 'aria-live': props.live ? 'polite' : 'off', 'aria-label': props.title,
                tabIndex: 0,
            }, lines.map(function (l) {
                return e(TranscriptLine, {key: l.seq, line: l});
            })) : (props.live
                ? e('div', {key: 'sk'}, [e(Loading, {key: 'l', label: 'Waiting for transcript'}), e('div', {key: 'q', className: 'hw-ai-quiet'}, props.emptyText)])
                : e('div', {key: 'none', className: 'hw-ai-quiet'}, props.emptyText)),
            (!props.ui.followLive && lines.length > 3) ? e('div', {key: 'jump', className: 'hw-ai-jump'},
                e(Button, {kind: 'secondary', small: true, icon: 'arrow-down', onClick: function () {
                    props.setUi(Object.assign({}, props.ui, {followLive: true}));
                    var el = props.listRef.current;
                    if (el) {
                        el.scrollTop = el.scrollHeight;
                    }
                }}, 'Jump to latest')) : null,
        ]);
    }

    // After the call: the service's outputs as a readable report, with the
    // sections a person reads first open and the rest a click away. Or, just
    // as clearly, that there is nothing yet.
    function AIFinished(props) {
        var s = props.session;
        var f = s.final || {};
        var sections = props.sections;
        var out = [];

        var statusRow;
        if (props.hasFinal) {
            statusRow = e('span', {key: 'ok', className: 'hw-ai-done-status hw-ai-done-ok'}, [e(Icon, {key: 'i', name: 'check-circle'}), 'Analysis ready']);
        } else if (props.viewStatus === 'failed' || s.status === 'failed') {
            statusRow = e('span', {key: 'bad', className: 'hw-ai-done-status hw-ai-done-err'}, [e(Icon, {key: 'i', name: 'alert-circle-outline'}), 'Insights could not be prepared']);
        } else if (props.viewStatus === 'processing') {
            statusRow = e('span', {key: 'proc', className: 'hw-ai-done-status'}, [e(Icon, {key: 'i', name: 'refresh'}), 'Preparing meeting insights…']);
        } else {
            statusRow = e('span', {key: 'none', className: 'hw-ai-done-status'}, [e(Icon, {key: 'i', name: 'information-outline'}), 'No AI session for this meeting']);
        }

        out.push(e('div', {key: 'dh', className: 'hw-ai-done-head'}, [
            e('div', {key: 't', className: 'hw-ai-done-title'}, 'AI Meeting Summary'),
            e('div', {key: 'm', className: 'hw-ai-done-meta'}, [
                e('span', {key: 'k', className: 'hw-ai-done-k'}, 'Meeting'),
                e('span', {key: 'v'}, meetingLabel(props.meeting)),
            ]),
            e('div', {key: 's', className: 'hw-ai-done-meta'}, [
                e('span', {key: 'k', className: 'hw-ai-done-k'}, 'Status'),
                statusRow,
            ]),
        ]));

        if (props.hasFinal) {
            f.summary ? out.push(e(Section2, {key: 'summary', icon: 'text-box-outline', title: 'Summary', open: sections.summary, onToggle: props.toggleSection('summary')},
                e('div', {className: 'hw-ai-summary'}, f.summary))) : null;
            (f.key_points || []).length ? out.push(e(Section2, {key: 'kp', icon: 'format-list-bulleted', title: 'Key points', count: f.key_points.length, open: sections.key_points, onToggle: props.toggleSection('key_points')},
                e(BulletList, {items: f.key_points}))) : null;
            (f.decisions || []).length ? out.push(e(Section2, {key: 'dec', icon: 'check-circle-outline', title: 'Decisions', count: f.decisions.length, open: sections.decisions, onToggle: props.toggleSection('decisions')},
                e(BulletList, {items: f.decisions}))) : null;
            (f.action_items || []).length ? out.push(e(Section2, {key: 'act', icon: 'check-circle-outline', title: 'Action items', count: f.action_items.length, open: sections.action_items, onToggle: props.toggleSection('action_items')},
                e(ActionList, {items: f.action_items}))) : null;
            (f.key_insights || []).length ? out.push(e(Section2, {key: 'ki', icon: 'lightning-bolt-outline', title: 'Key insights', count: f.key_insights.length, open: sections.key_insights, onToggle: props.toggleSection('key_insights')},
                e(BulletList, {items: f.key_insights}))) : null;
            (f.client_insights || []).length ? out.push(e(Section2, {key: 'ci', icon: 'account-outline', title: 'Client insights', count: f.client_insights.length, open: sections.client_insights, onToggle: props.toggleSection('client_insights')},
                e(LabelledList, {items: f.client_insights}))) : null;
            (f.topics || []).length ? out.push(e(Section2, {key: 'topics', icon: 'star-outline', title: 'Important topics', count: f.topics.length, open: sections.topics, onToggle: props.toggleSection('topics')},
                e('div', {className: 'hw-ai-topics', role: 'list', 'aria-label': 'Important topics'}, f.topics.map(function (t, i) {
                    return e('span', {key: i, className: 'hw-chip', role: 'listitem'}, t);
                })))) : null;
            out.push(e('div', {key: 'meta', className: 'hw-ai-delivered'},
                'Delivered by Honco AI ' + formatWhen(f.received_at) + '.'));
        } else if (s.status === 'failed') {
            out.push(e(EmptyState, {key: 'none', icon: 'alert-circle-outline', title: 'Meeting insights could not be prepared'},
                'The AI service could not produce a summary for this meeting.'));
        } else if (props.viewStatus === 'processing') {
            out.push(e('div', {key: 'proc'}, [
                e(Loading, {key: 'l', label: 'Preparing meeting insights'}),
                e('div', {key: 'n', className: 'hw-ai-quiet', style: {padding: '0 12px 12px'}},
                    'Preparing meeting insights… The summary appears here when Honco AI delivers it.'),
            ]));
        } else {
            out.push(e(EmptyState, {key: 'none', icon: 'text-box-outline', title: 'No summary for this meeting'},
                s.events_received
                    ? 'Honco AI has not delivered a summary for this meeting yet.'
                    : 'Honco AI did not join this meeting, so there is no transcript or summary.'));
        }

        var hasTranscript = props.lines.length > 0 || s.line_count > 0;
        if (hasTranscript) {
            out.push(e(Section2, {
                key: 'tr', icon: 'message-text-outline', title: 'Transcript', count: s.line_count || props.lines.length,
                open: sections.transcript, onToggle: props.toggleSection('transcript'),
            }, e(TranscriptBlock, {
                title: 'Transcript', live: false, session: s, bare: true,
                lines: props.lines, listRef: props.listRef, onScroll: props.onScroll, ui: props.ui, setUi: props.setUi,
                hasOlder: props.hasOlder, olderOnService: props.olderOnService, loadOlder: props.loadOlder,
                emptyText: 'Transcript unavailable.',
            })));
        }
        return e('div', {className: 'hw-ai-done'}, out);
    }

    // --- The right-hand sidebar panel: Tasks and Meeting Intelligence ------

    // One entry point, two views. A second App Bar icon for a panel this
    // size would crowd the bar; tabs are what Mattermost's own RHS uses
    // for the same problem.
    // --- Remote Support ----------------------------------------------------

    // Honco manages the workflow; RustDesk makes the connection.
    //
    // RustDesk OSS exposes no API, so there is deliberately no "connect"
    // button that pretends to drive it. The card tells people where they
    // are in the process and leaves the connection to the RustDesk client,
    // which is the only thing that can actually make one.

    var SUPPORT_POST_TYPE = 'custom_honco_support';
    var SUPPORT_WS_EVENT = 'custom_' + PLUGIN_ID + '_support_updated';

    var SUPPORT_STATUS = {
        open: {label: 'Waiting for support', dot: 'var(--away-indicator, #ffbc42)'},
        accepted: {label: 'Accepted', dot: 'var(--online-indicator, #3db887)'},
        active: {label: 'Session active', dot: 'var(--online-indicator, #3db887)'},
        ended: {label: 'Session ended', dot: 'rgba(var(--center-channel-color-rgb), 0.32)'},
        cancelled: {label: 'Cancelled', dot: 'rgba(var(--center-channel-color-rgb), 0.32)'},
        rejected: {label: 'Declined', dot: 'rgba(var(--center-channel-color-rgb), 0.32)'},
    };

    function supportStatus(v) {
        return SUPPORT_STATUS[v] || SUPPORT_STATUS.ended;
    }

    function StatusDot(props) {
        var st = supportStatus(props.status);
        return e('div', {className: 'hw-status'}, [
            e(Dot, {key: 'd', color: st.dot}),
            e('span', {key: 'l'}, st.label),
        ]);
    }

    // Everything RustDesk-related lives here, so there is exactly one place
    // that describes how to connect -- and it never claims Honco did it.
    function RustDeskHint() {
        return e('div', {className: 'hw-hint'}, [
            e('div', {key: 't', style: {fontWeight: 600, marginBottom: 2}}, 'Connecting'),
            e('div', {key: 'b'},
                'Open the RustDesk client and share your ID with the agent. ' +
                'Honco tracks the session; it never sees or stores your RustDesk password.'),
        ]);
    }

    function SupportPanel() {
        var s0 = React.useState({requests: [], isAgent: false, loading: true, error: null});
        var state = s0[0];
        var setState = s0[1];

        var f = React.useState({issue: '', open: false, saving: false, error: null});
        var form = f[0];
        var setForm = f[1];

        var ctxState = React.useState({teamId: null, userId: null, channelId: null});
        var ctx = ctxState[0];
        var setCtx = ctxState[1];

        React.useEffect(function () {
            try {
                var s = window.store ? window.store.getState() : null;
                if (s) {
                    setCtx({
                        teamId: s.entities.teams.currentTeamId,
                        userId: s.entities.users.currentUserId,
                        channelId: s.entities.channels.currentChannelId,
                    });
                }
            } catch (err) {
                setCtx({teamId: null, userId: null, channelId: null});
            }
        }, []);

        var load = React.useCallback(function (teamId) {
            if (!teamId) {
                return;
            }
            request('GET', '/support/requests?team_id=' + encodeURIComponent(teamId))
                .then(function (data) {
                    setState({
                        requests: (data && data.requests) || [],
                        isAgent: Boolean(data && data.is_agent),
                        loading: false,
                        error: null,
                    });
                }).catch(function (err) {
                    setState({requests: [], isAgent: false, loading: false, error: err.message});
                });
        }, []);

        React.useEffect(function () {
            load(ctx.teamId);
        }, [ctx.teamId, load]);

        // Live updates: the server only sends these to people involved.
        React.useEffect(function () {
            var handler = function () {
                load(ctx.teamId);
            };
            window.HoncoSupportBus = window.HoncoSupportBus || [];
            window.HoncoSupportBus.push(handler);
            return function () {
                var i = window.HoncoSupportBus.indexOf(handler);
                if (i >= 0) {
                    window.HoncoSupportBus.splice(i, 1);
                }
            };
        }, [ctx.teamId, load]);

        function create(ev) {
            ev.preventDefault();
            setForm(Object.assign({}, form, {saving: true, error: null}));
            request('POST', '/support/requests', {
                team_id: ctx.teamId,
                channel_id: ctx.channelId || '',
                issue: form.issue,
            }).then(function () {
                setForm({issue: '', open: false, saving: false, error: null});
                load(ctx.teamId);
            }).catch(function (err) {
                setForm(Object.assign({}, form, {saving: false, error: err.message}));
            });
        }

        function act(id, action) {
            request('POST', '/support/requests/' + id + '/' + action)
                .then(function () {
                    load(ctx.teamId);
                }).catch(function (err) {
                    setState(function (p) {
                        return {requests: p.requests, isAgent: p.isAgent, loading: false, error: err.message};
                    });
                });
        }

        function actionsFor(r) {
            var out = [];
            var mine = r.requester_id === ctx.userId;
            var isMyAssignment = r.agent_id === ctx.userId;

            // These mirror the server's rules. The server decides; this
            // only avoids offering a button that would be refused.
            if (state.isAgent && r.status === 'open') {
                out.push(e(Button, {key: 'acc', small: true,
                    onClick: function () { act(r.id, 'accept'); }}, 'Accept Request'));
                out.push(e(Button, {key: 'rej', kind: 'ghost', small: true,
                    onClick: function () { act(r.id, 'reject'); }}, 'Decline'));
            }
            if (isMyAssignment && r.status === 'accepted') {
                out.push(e(Button, {key: 'start', small: true, icon: 'play',
                    onClick: function () { act(r.id, 'start'); }}, 'Start Session'));
            }
            if ((isMyAssignment || mine) && (r.status === 'active' || r.status === 'accepted')) {
                out.push(e(Button, {key: 'end', kind: 'ghost', small: true,
                    onClick: function () { act(r.id, 'end'); }}, 'End Session'));
            }
            if (mine && (r.status === 'open' || r.status === 'accepted')) {
                out.push(e(Button, {key: 'can', kind: 'ghost', small: true,
                    onClick: function () { act(r.id, 'cancel'); }}, 'Cancel'));
            }
            return out;
        }

        if (state.loading) {
            return e('div', {className: 'hw'}, e(Loading, {label: 'Loading support requests'}));
        }

        return e('div', {className: 'hw'}, [
            e(Toolbar, {key: 'bar', icon: 'monitor', title: 'Remote Support'}, [
                state.isAgent ? e(Badge, {key: 'badge', tone: 'ok'}, 'Support agent') : null,
                e(Button, {
                    key: 'new',
                    small: true,
                    className: 'hw-spacer',
                    icon: form.open ? undefined : 'plus',
                    onClick: function () {
                        setForm(Object.assign({}, form, {open: !form.open, error: null}));
                    },
                }, form.open ? 'Cancel' : 'Request Support'),
            ]),

            form.open ? e('form', {
                key: 'form',
                onSubmit: create,
                className: 'hw-form hw-fade',
            }, [
                e('div', {key: 'lbl', className: 'hw-form-label'}, 'What is going wrong?'),
                e('textarea', {
                    key: 'issue',
                    className: 'hw-textarea',
                    'aria-label': 'Issue description',
                    placeholder: 'My screen is not connecting',
                    value: form.issue,
                    maxLength: 1024,
                    onChange: function (ev) {
                        setForm(Object.assign({}, form, {issue: ev.target.value}));
                    },
                }),
                e('div', {key: 'note', className: 'hw-form-note'},
                    'Never include passwords. Honco does not need them and will not store them.'),
                form.error ? e('div', {
                    key: 'err', role: 'alert', style: {color: 'var(--error-text)', fontSize: 12, marginBottom: 6},
                }, form.error) : null,
                e(Button, {key: 'go', type: 'submit', disabled: form.saving},
                    form.saving ? 'Sending…' : 'Request Support'),
            ]) : null,

            state.error ? e(ErrorNote, {key: 'err'}, state.error) : null,

            e('div', {key: 'list', className: 'hw-list'},
                state.requests.length === 0 ? e(EmptyState, {
                    icon: 'monitor',
                    title: 'No support requests',
                }, 'Use "Request Support" if you need help with your screen or device. A support agent picks it up from here.') :
                    state.requests.map(function (r) {
                        var actions = actionsFor(r);
                        return e('div', {
                            key: r.id,
                            // A stable hook for the row, so tests (and any
                            // future deep link) can address one request
                            // rather than guessing at DOM structure.
                            'data-request-id': r.id,
                            className: 'hw-row',
                        }, [
                            e(StatusDot, {key: 'st', status: r.status}),
                            e('div', {key: 'who', className: 'hw-row-meta', style: {marginTop: 4, fontSize: 12, gap: 6}}, [
                                e(UserAvatar, {key: 'rav', userId: r.requester_id, size: 18}),
                                e('span', {key: 'rt'}, 'Requested by ' + (r.requester_id === ctx.userId ? 'you' : 'a colleague')),
                                r.agent_id ? e('span', {key: 'sep'}, '·') : null,
                                r.agent_id ? e(UserAvatar, {key: 'aav', userId: r.agent_id, size: 18}) : null,
                                r.agent_id ? e('span', {key: 'at'}, 'agent assigned') : null,
                            ]),
                            r.issue ? e('div', {
                                key: 'issue',
                                className: 'hw-row-desc',
                                style: {fontSize: 13, marginTop: 4, color: 'var(--center-channel-color)'},
                            }, r.issue) : null,
                            r.status === 'active' ? e(RustDeskHint, {key: 'hint'}) : null,
                            actions.length ? e('div', {key: 'actions', className: 'hw-row-actions'}, actions) : null,
                        ]);
                    })),
        ]);
    }

    // The channel card. Read-only on purpose: the actions live in the panel
    // where the viewer's own role is known, rather than offering buttons in
    // a channel to people who cannot use them.
    function SupportCard(props) {
        var post = props.post || {};
        var c = (post.props && post.props.honco_support) || {};
        ensureStyles();
        return e('div', {className: 'hw-card', 'data-honco-card': 'support'}, [
            e('div', {key: 'title', className: 'hw-card-title'}, [
                e(Icon, {key: 'i', name: 'monitor'}),
                e('span', {key: 't'}, 'Remote Support Request'),
            ]),
            e('div', {key: 'by', className: 'hw-card-sub', style: {display: 'flex', alignItems: 'center', gap: 6}}, [
                e(UserAvatar, {key: 'av', userId: c.requester_id, size: 18}),
                e('span', {key: 't'}, 'Requested by ' + (c.requester_name || 'someone')),
            ]),
            e(StatusDot, {key: 'st', status: c.status}),
            c.issue ? e('div', {
                key: 'issue',
                style: {
                    fontSize: 13, marginTop: 8, whiteSpace: 'pre-wrap',
                    wordBreak: 'break-word', color: 'var(--center-channel-color)',
                },
            }, c.issue) : null,
            c.agent_name ? e('div', {key: 'agent', className: 'hw-card-line'}, [
                c.agent_id ? e(UserAvatar, {key: 'i', userId: c.agent_id, size: 18}) : e(Icon, {key: 'i', name: 'account-outline'}),
                e('span', {key: 't'}, 'Agent: ' + c.agent_name),
            ]) : null,
        ]);
    }

    // --- Honco Administration ----------------------------------------------

    // Complements the System Console rather than replacing it. Everything
    // shown is fetched from the server, which re-checks manage_system on
    // every request -- this component never decides who is an admin, it
    // only stops rendering a tab that would 403.

    var HEALTH_STYLE = {
        healthy: {dot: 'var(--online-indicator, #3db887)', label: 'Healthy'},
        degraded: {dot: 'var(--away-indicator, #ffbc42)', label: 'Degraded'},
        unavailable: {dot: 'var(--error-text, #d24b4e)', label: 'Unavailable'},
    };

    function healthStyle(s) {
        return HEALTH_STYLE[s] || HEALTH_STYLE.unavailable;
    }

    function AdminSection(props) {
        return e('section', {style: {marginBottom: 18}, 'aria-label': props.title}, [
            e('div', {key: 'h', className: 'hw-section-title', style: {padding: '0 0 6px'}}, props.title),
            e('div', {key: 'b'}, props.children),
        ]);
    }

    function KeyValue(props) {
        return e('div', {className: 'hw-kv'}, [
            e('span', {key: 'k', className: 'hw-kv-k'}, props.label),
            e('span', {key: 'v', className: 'hw-kv-v' + (props.warn ? ' hw-kv-warn' : '')}, props.value),
        ]);
    }

    function humanBytes(n) {
        n = Number(n) || 0;
        if (n >= 1073741824) {
            return (n / 1073741824).toFixed(1) + ' GB';
        }
        if (n >= 1048576) {
            return (n / 1048576).toFixed(1) + ' MB';
        }
        if (n >= 1024) {
            return (n / 1024).toFixed(1) + ' KB';
        }
        return n + ' B';
    }

    // Booleans read better as words than as true/false, and "enabled" vs
    // "disabled" is not always the safe direction -- public file links being
    // disabled is good news, so the colour follows `good`, not the value.
    function Flag(props) {
        var on = Boolean(props.value);
        var good = props.invert ? !on : on;
        return e('div', {className: 'hw-kv'}, [
            e('span', {key: 'k', className: 'hw-kv-k'}, props.label),
            e('span', {key: 'v', className: 'hw-kv-v'},
                e(Badge, {tone: good ? 'ok' : 'err'},
                    props.words ? (on ? props.words[0] : props.words[1]) : (on ? 'Enabled' : 'Disabled'))),
        ]);
    }

    // --- Global search -----------------------------------------------------

    // Honco entities in Mattermost's own search box.
    //
    // Selecting the "Honco" pill next to Messages and Files and pressing
    // Enter hands the typed terms to SearchIntent, which opens this panel.
    // Messages and Files keep working exactly as they did -- this adds a
    // place to look, it does not replace the one that already exists.
    var SearchIntent = {
        query: '',
        listeners: [],
        open: function (query) {
            this.query = query || '';
            this.listeners.forEach(function (fn) {
                try {
                    fn(query || '');
                } catch (err) { /* one bad listener must not stop the rest */ }
            });
        },
        subscribe: function (fn) {
            this.listeners.push(fn);
            var self = this;
            return function () {
                var i = self.listeners.indexOf(fn);
                if (i >= 0) {
                    self.listeners.splice(i, 1);
                }
            };
        },
    };

    // Navigating to a result.
    //
    // Both of these go through Mattermost's own routes, which is the point:
    // a permalink is resolved by the server, against the reader's session.
    // If the caller somehow held an id they may not read, the destination
    // refuses -- the search result is a way to ask, never a grant.
    function teamNameForChannel(channelId) {
        try {
            var st = window.store.getState();
            var ch = st.entities.channels.channels[channelId];
            var teamId = (ch && ch.team_id) || st.entities.teams.currentTeamId;
            var team = st.entities.teams.teams[teamId] ||
                st.entities.teams.teams[st.entities.teams.currentTeamId];
            return team ? team.name : null;
        } catch (err) {
            return null;
        }
    }

    function goToPost(channelId, postId) {
        var team = teamNameForChannel(channelId);
        if (team && postId) {
            window.location.href = '/' + team + '/pl/' + postId;
        }
    }

    function goToChannel(channelId) {
        try {
            var st = window.store.getState();
            var ch = st.entities.channels.channels[channelId];
            var team = teamNameForChannel(channelId);
            if (ch && team) {
                window.location.href = '/' + team + '/channels/' + ch.name;
            }
        } catch (err) { /* staying put is better than a broken URL */ }
    }

    // The categories, in the order they are shown. "All" is a view over the
    // other five, not a sixth kind of thing.
    var SEARCH_TABS = [
        {id: 'all', label: 'All'},
        {id: 'tasks', label: 'Tasks'},
        {id: 'meetings', label: 'Meetings'},
        {id: 'recordings', label: 'Recordings'},
        {id: 'summaries', label: 'Summaries'},
        {id: 'support', label: 'Support'},
    ];

    var SEARCH_TITLES = {
        tasks: 'Tasks',
        meetings: 'Meetings',
        recordings: 'Recordings',
        summaries: 'Summaries',
        support: 'Support',
    };

    // A task's status reads as a sentence; a meeting's is already one.
    var TASK_STATUS_LABEL = {todo: 'To do', in_progress: 'In progress', done: 'Done'};

    function searchHitLine(hit) {
        if (hit.type === 'tasks') {
            var bits = [TASK_STATUS_LABEL[hit.status] || hit.status];
            if (hit.due_at) {
                bits.push('Due ' + formatDue(hit.due_at));
            }
            return bits.join(' · ');
        }
        if (hit.type === 'support') {
            return (SUPPORT_STATUS[hit.status] ? SUPPORT_STATUS[hit.status].label : hit.status) +
                (hit.at ? ' · ' + formatWhen(hit.at) : '');
        }
        if (hit.type === 'meetings') {
            var m = (STATUS_STYLE[hit.status] ? STATUS_STYLE[hit.status].label : hit.status);
            return m + (hit.at ? ' · ' + formatWhen(hit.at) : '');
        }
        if (hit.type === 'recordings') {
            return [hit.status, hit.subtitle, formatWhen(hit.at)].filter(Boolean).join(' · ');
        }
        return [hit.subtitle, formatWhen(hit.at)].filter(Boolean).join(' · ');
    }

    // What clicking a result does.
    //
    // Every one of these lands on an existing surface -- a channel, a
    // meeting card, the Meeting Intelligence tab -- and every one of those
    // re-authorizes on the server. Nothing here is trusted because it came
    // from a search result: the id in a result is only a way to ask, never
    // a permission to see.
    var SEARCH_ICONS = {
        tasks: 'check-circle-outline',
        meetings: 'video-outline',
        recordings: 'file-video-outline',
        summaries: 'text-box-outline',
        support: 'monitor',
    };

    function SearchResultRow(props) {
        var hit = props.hit;

        function open() {
            if (hit.type === 'summaries' || hit.type === 'meetings') {
                if (hit.channel_id && hit.meeting_id) {
                    MeetingIntent.remember(hit.channel_id, hit.meeting_id);
                }
                if (hit.post_id) {
                    goToPost(hit.channel_id, hit.post_id);
                }
                if (hit.type === 'summaries' && hit.meeting_id) {
                    MeetingIntent.open(hit.meeting_id);
                }
                return;
            }
            if (hit.type === 'recordings' || hit.type === 'support') {
                // Recordings are posted into their channel with the file
                // attached, and a support request has its own card there.
                if (hit.post_id) {
                    goToPost(hit.channel_id, hit.post_id);
                } else if (hit.channel_id) {
                    goToChannel(hit.channel_id);
                }
                return;
            }
            if (hit.type === 'tasks') {
                props.onOpenTask(hit);
            }
        }

        return e('button', {
            onClick: open,
            className: 'hw-hit',
            'data-search-type': hit.type,
            'data-search-id': hit.id,
        }, [
            e('div', {key: 't', className: 'hw-row-title', style: {display: 'flex', gap: 8, alignItems: 'center'}}, [
                e(Icon, {key: 'i', name: SEARCH_ICONS[hit.type] || 'magnify', style: {fontSize: 15, color: 'rgba(var(--center-channel-color-rgb), 0.56)', flexShrink: 0}}),
                e('span', {key: 'x', style: {minWidth: 0}}, hit.title || '(untitled)'),
            ]),
            e('div', {
                key: 's',
                style: {fontSize: 12, color: 'rgba(var(--center-channel-color-rgb), 0.64)', marginTop: 2, paddingLeft: 23},
            }, searchHitLine(hit)),
        ]);
    }

    function SearchPanel(props) {
        var q = React.useState(SearchIntent.query || '');
        var query = q[0];
        var setQuery = q[1];

        var f = React.useState('all');
        var filter = f[0];
        var setFilter = f[1];

        var p = React.useState(0);
        var page = p[0];
        var setPage = p[1];

        var r = React.useState({data: null, loading: false, error: null});
        var res = r[0];
        var setRes = r[1];

        // A search the user started from Mattermost's search box arrives
        // here. Changing the terms resets to the first page, or page 3 of
        // the previous search would silently become page 3 of this one.
        React.useEffect(function () {
            return SearchIntent.subscribe(function (next) {
                setQuery(next);
                setPage(0);
                setFilter('all');
            });
        }, []);

        var run = React.useCallback(function (term, type, pageNo) {
            if (!term || term.trim().length < 2) {
                setRes({data: null, loading: false, error: null});
                return;
            }
            setRes(function (prev) {
                return {data: prev.data, loading: true, error: null};
            });
            request('GET', '/search?q=' + encodeURIComponent(term.trim()) +
                '&type=' + encodeURIComponent(type) + '&page=' + pageNo).then(function (d) {
                setRes({data: d, loading: false, error: null});
            }).catch(function (err) {
                setRes({data: null, loading: false, error: err.message});
            });
        }, []);

        React.useEffect(function () {
            run(query, filter, page);
        }, [query, filter, page, run]);

        function openTask() {
            // A task has no post to jump to, so the useful action is the
            // Tasks tab, which loads it through the ordinary tasks endpoint
            // -- and that endpoint authorizes again, as it always has.
            if (props && props.onOpenTasks) {
                props.onOpenTasks();
            }
        }

        var body;
        if (res.error) {
            // Whatever went wrong server-side, the user sees a sentence.
            body = e(ErrorNote, {}, res.error);
        } else if (!query || query.trim().length < 2) {
            body = e(EmptyState, {icon: 'magnify', title: 'Search Honco'},
                'Type at least two characters to search tasks, meetings, recordings, summaries and support requests. Messages and files are covered by the search box at the top.');
        } else if (res.loading && !res.data) {
            body = e(Loading, {label: 'Searching'});
        } else if (res.data && res.data.total === 0) {
            body = e(EmptyState, {icon: 'magnify', title: 'No results'},
                'No Honco results for “' + query + '”.');
        } else if (res.data) {
            var sections = [];
            res.data.pages.forEach(function (pg) {
                if (!pg.hits.length) {
                    return;
                }
                sections.push(e('div', {key: pg.type + '-h', className: 'hw-section-title'},
                    SEARCH_TITLES[pg.type] + ' · ' + pg.total));
                pg.hits.forEach(function (hit) {
                    sections.push(e(SearchResultRow, {
                        key: pg.type + '-' + hit.id,
                        hit: hit,
                        onOpenTask: openTask,
                    }));
                });
                // In the All view each category is capped, so offer the way
                // to see the rest rather than pretending this is all of it.
                if (filter === 'all' && pg.has_more) {
                    sections.push(e(Button, {
                        key: pg.type + '-more',
                        kind: 'link',
                        style: {display: 'block', padding: '6px 16px 10px'},
                        onClick: (function (type) {
                            return function () {
                                setFilter(type);
                                setPage(0);
                            };
                        }(pg.type)),
                    }, 'See all ' + pg.total + ' ' + SEARCH_TITLES[pg.type].toLowerCase()));
                }
            });
            body = e('div', {className: 'hw-fade'}, sections);
        }

        // Paging controls belong to a single category: "page 2 of everything"
        // would have to interleave five result sets, and the ordering that
        // implies is not one this ranking can honestly claim.
        var pager = null;
        if (filter !== 'all' && res.data && res.data.pages.length) {
            var pg0 = res.data.pages[0];
            var from = (pg0.page * pg0.limit) + 1;
            var to = (pg0.page * pg0.limit) + pg0.hits.length;
            if (pg0.total > 0) {
                pager = e('div', {className: 'hw-pager', style: {padding: '6px 16px'}}, [
                    e(Button, {
                        key: 'prev',
                        kind: 'ghost',
                        small: true,
                        onClick: function () {
                            setPage(Math.max(0, page - 1));
                        },
                        disabled: page === 0,
                    }, 'Previous'),
                    e(Button, {
                        key: 'next',
                        kind: 'ghost',
                        small: true,
                        onClick: function () {
                            setPage(page + 1);
                        },
                        disabled: !pg0.has_more,
                    }, 'Next'),
                    e('span', {key: 'n', className: 'hw-pager-range'}, from + '–' + to + ' of ' + pg0.total),
                ]);
            }
        }

        return e('div', {className: 'hw'}, [
            e('div', {key: 'q', style: {padding: '12px 16px 8px', position: 'relative'}}, [
                e(Icon, {key: 'i', name: 'magnify', style: {
                    position: 'absolute', left: 24, top: 19, fontSize: 16,
                    color: 'rgba(var(--center-channel-color-rgb), 0.56)', pointerEvents: 'none',
                }}),
                e('input', {
                    key: 'input',
                    type: 'search',
                    className: 'hw-input',
                    value: query,
                    placeholder: 'Search Honco',
                    'aria-label': 'Search Honco',
                    autoComplete: 'off',
                    onChange: function (ev) {
                        setQuery(ev.target.value);
                        setPage(0);
                    },
                    style: {margin: 0, paddingLeft: 30},
                }),
            ]),
            e('div', {key: 'filters', role: 'tablist', className: 'hw-chips'}, SEARCH_TABS.map(function (t) {
                var active = filter === t.id;
                return e('button', {
                    key: t.id,
                    role: 'tab',
                    className: 'hw-chip',
                    'aria-selected': active,
                    // The panel's own tabs also include "Tasks" and
                    // "Support", so these carry a distinct hook rather than
                    // relying on a label that appears twice on screen.
                    'data-search-filter': t.id,
                    onClick: function () {
                        setFilter(t.id);
                        setPage(0);
                    },
                }, t.label);
            })),
            e('div', {key: 'body', className: 'hw-list'}, body),
            pager,
        ]);
    }

    // --- Files -------------------------------------------------------------

    // The Files browser. A listing only: every file here is stored and
    // served by Mattermost, and the links below are its own
    // /api/v4/files/{id} routes, which it authorizes again on click. The
    // server already filtered to channels this user belongs to, so there
    // is nothing for this component to hide.

    var FILE_KIND_ICON = {
        image: 'image-outline',
        video: 'play',
        audio: 'microphone',
        pdf: 'file-document-outline',
        document: 'file-document-outline',
        spreadsheet: 'file-document-outline',
        presentation: 'file-document-outline',
        archive: 'folder-outline',
        code: 'code-tags',
        file: 'paperclip',
    };

    var FILE_KINDS = [
        {id: 'all', label: 'All'},
        {id: 'image', label: 'Images'},
        {id: 'pdf', label: 'PDFs'},
        {id: 'document', label: 'Documents'},
        {id: 'video', label: 'Video'},
        {id: 'archive', label: 'Archives'},
    ];

    // A channel with no display name is a DM or group message; Mattermost
    // composes those names from their members in the client. Rather than
    // guess at one, say what it is.
    function fileChannelLabel(f) {
        if (f.channel_name) {
            return f.channel_name;
        }
        if (f.channel_type === 'D') {
            return 'Direct message';
        }
        if (f.channel_type === 'G') {
            return 'Group message';
        }
        return 'Channel';
    }

    function FileRow(props) {
        var f = props.file;
        var href = '/api/v4/files/' + encodeURIComponent(f.id);
        var meta = [
            humanBytes(f.size),
            f.uploader ? '@' + f.uploader : null,
            fileChannelLabel(f),
            formatWhen(f.created_at),
        ].filter(Boolean).join(' · ');

        return e('div', {className: 'hw-row hw-file-row', 'data-file-id': f.id, 'data-file-kind': f.kind}, [
            e('div', {key: 'ic', className: 'hw-file-icon'},
                e(Icon, {name: FILE_KIND_ICON[f.kind] || FILE_KIND_ICON.file})),
            e('div', {key: 'body', className: 'hw-row-body'}, [
                e('a', {
                    key: 'name',
                    className: 'hw-row-title hw-file-name',
                    href: href,
                    target: '_blank',
                    rel: 'noopener noreferrer',
                    title: f.name,
                }, f.name || '(untitled)'),
                e('div', {key: 'meta', className: 'hw-row-meta'}, meta),
            ]),
            e('a', {
                key: 'dl',
                className: 'hw-btn hw-btn-secondary hw-btn-sm',
                href: href + '?download=1',
                target: '_blank',
                rel: 'noopener noreferrer',
                'aria-label': 'Download ' + (f.name || 'file'),
                'data-file-download': f.id,
            }, [e(Icon, {key: 'i', name: 'download'}), 'Download']),
        ]);
    }

    function FilesPanel() {
        var s0 = React.useState({files: [], hasMore: false, loading: true, error: null});
        var state = s0[0];
        var setState = s0[1];
        var k0 = React.useState('all');
        var kind = k0[0];
        var setKind = k0[1];

        var PAGE = 25;

        var load = React.useCallback(function (offset, append) {
            if (!append) {
                setState(function (prev) {
                    return {files: prev.files, hasMore: prev.hasMore, loading: true, error: null};
                });
            }
            request('GET', '/files/recent?limit=' + PAGE + '&offset=' + offset)
                .then(function (data) {
                    var got = (data && data.files) || [];
                    setState(function (prev) {
                        return {
                            files: append ? prev.files.concat(got) : got,
                            hasMore: Boolean(data && data.has_more),
                            loading: false,
                            error: null,
                        };
                    });
                }).catch(function (err) {
                    setState(function (prev) {
                        return {
                            files: append ? prev.files : [],
                            hasMore: false,
                            loading: false,
                            error: err.message,
                        };
                    });
                });
        }, []);

        React.useEffect(function () {
            load(0, false);
        }, [load]);

        var shown = kind === 'all' ? state.files : state.files.filter(function (f) {
            return f.kind === kind;
        });

        var body;
        if (state.loading && !state.files.length) {
            body = e(Loading, {label: 'Loading files'});
        } else if (state.error) {
            body = e('div', {}, [
                e(ErrorNote, {key: 'e'}, state.error),
                e(Button, {
                    key: 'r', kind: 'secondary', icon: 'refresh', onClick: function () {
                        load(0, false);
                    },
                }, 'Try again'),
            ]);
        } else if (!state.files.length) {
            body = e(EmptyState, {icon: 'paperclip', title: 'No files yet'},
                'Files shared in channels you are a member of will appear here.');
        } else if (!shown.length) {
            body = e(EmptyState, {icon: 'paperclip', title: 'No matching files'},
                'No ' + kind + ' files in the most recent ' + state.files.length + '.');
        } else {
            body = shown.map(function (f) {
                return e(FileRow, {key: f.id, file: f});
            });
        }

        return e('div', {className: 'hw hw-files'}, [
            e(Toolbar, {key: 'bar', icon: 'paperclip', title: 'Files'}, [
                e('select', {
                    key: 'kind',
                    className: 'hw-select',
                    'aria-label': 'Filter files by type',
                    value: kind,
                    onChange: function (ev) {
                        setKind(ev.target.value);
                    },
                }, FILE_KINDS.map(function (k) {
                    return e('option', {key: k.id, value: k.id}, k.label);
                })),
                e(Button, {
                    key: 'refresh', kind: 'secondary', icon: 'refresh',
                    'aria-label': 'Refresh files',
                    disabled: state.loading,
                    onClick: function () {
                        load(0, false);
                    },
                }, 'Refresh'),
            ]),
            e('div', {key: 'list', className: 'hw-list'}, body),
            state.hasMore && !state.error ? e('div', {key: 'more', className: 'hw-more'},
                e(Button, {
                    kind: 'secondary',
                    disabled: state.loading,
                    'data-testid': 'files-load-more',
                    onClick: function () {
                        load(state.files.length, true);
                    },
                }, state.loading ? 'Loading…' : 'Load more')) : null,
        ]);
    }

    function AdminPanel() {
        var o = React.useState({data: null, loading: true, error: null});
        var overview = o[0];
        var setOverview = o[1];

        var h = React.useState({data: null, loading: true, error: null});
        var health = h[0];
        var setHealth = h[1];

        // Fetched on open and on an explicit refresh only. A dashboard that
        // polls every few seconds would run health probes against Jitsi and
        // Jibri forever for the benefit of a tab nobody is looking at.
        var loadOverview = React.useCallback(function () {
            setOverview(function (p) {
                return {data: p.data, loading: true, error: null};
            });
            request('GET', '/admin/overview').then(function (d) {
                setOverview({data: d, loading: false, error: null});
            }).catch(function (err) {
                setOverview({data: null, loading: false, error: err.message});
            });
        }, []);

        var loadHealth = React.useCallback(function () {
            setHealth(function (p) {
                return {data: p.data, loading: true, error: null};
            });
            request('GET', '/admin/health').then(function (d) {
                setHealth({data: d, loading: false, error: null});
            }).catch(function (err) {
                setHealth({data: null, loading: false, error: err.message});
            });
        }, []);

        React.useEffect(function () {
            loadOverview();
            loadHealth();
        }, [loadOverview, loadHealth]);

        if (overview.error) {
            return e('div', {className: 'hw'}, e(ErrorNote, {}, overview.error));
        }
        if (overview.loading && !overview.data) {
            return e('div', {className: 'hw'}, [
                e(Toolbar, {key: 'bar', icon: 'shield-outline', title: 'Honco Administration'}),
                e(Loading, {key: 'l', label: 'Loading overview'}),
            ]);
        }

        var d = overview.data || {};
        var usage = d.usage || {};
        var sec = d.security || {};
        var plug = d.plugin || {};
        var files = d.files || {};
        var notif = d.notifications || {};
        var ai = d.ai || {};
        var failures = d.failures || [];

        return e('div', {className: 'hw'}, [
            e(Toolbar, {key: 'bar', icon: 'shield-outline', title: 'Honco Administration'},
                e(Button, {
                    key: 'r',
                    small: true,
                    kind: 'ghost',
                    icon: 'refresh',
                    className: 'hw-spacer',
                    disabled: overview.loading || health.loading,
                    onClick: function () {
                        loadOverview();
                        loadHealth();
                    },
                }, 'Refresh')),

            e('div', {key: 'body', className: 'hw-list hw-fade', style: {padding: '12px'}}, [

                e(AdminSection, {key: 'health', title: 'System health'},
                    health.loading && !health.data ? e(Loading, {label: 'Checking health'}) :
                        (health.error ? e(ErrorNote, {}, health.error) :
                            ((health.data && health.data.checks) || []).map(function (c) {
                                var st = healthStyle(c.status);
                                return e('div', {key: c.name, className: 'hw-kv', style: {alignItems: 'center'}}, [
                                    e('span', {key: 'n', className: 'hw-status', style: {flex: 1}}, [
                                        e(Dot, {key: 'd', color: st.dot}),
                                        e('span', {key: 't'}, c.name),
                                    ]),
                                    e('span', {
                                        key: 's',
                                        style: {
                                            fontSize: 12,
                                            color: c.status === 'healthy'
                                                ? 'rgba(var(--center-channel-color-rgb), 0.72)'
                                                : 'var(--error-text)',
                                            textAlign: 'right',
                                        },
                                    }, (c.detail || st.label) + (c.latency_ms ? ' · ' + c.latency_ms + 'ms' : '')),
                                ]);
                            }))),

                e(AdminSection, {key: 'usage', title: 'Usage'}, [
                    e(KeyValue, {key: 'u', label: 'Users', value: usage.users}),
                    e(KeyValue, {key: 't', label: 'Teams', value: usage.teams}),
                    e(KeyValue, {key: 'c', label: 'Channels', value: usage.channels}),
                    e(KeyValue, {key: 'ma', label: 'Active meetings', value: usage.meetings_active}),
                    e(KeyValue, {key: 'mt', label: 'Meetings (total)', value: usage.meetings_total}),
                    e(KeyValue, {key: 'r', label: 'Recordings', value: usage.recordings}),
                    e(KeyValue, {key: 'k', label: 'Tasks', value: usage.tasks}),
                    e(KeyValue, {key: 's', label: 'Meeting summaries', value: usage.summaries}),
                    e(KeyValue, {key: 'so', label: 'Support requests (open)', value: usage.support_open}),
                    e(KeyValue, {key: 'st', label: 'Support requests (total)', value: usage.support_total}),
                ]),

                // Files: counts only. No path, bucket or directory is ever
                // sent to the browser, and nothing on this panel deletes.
                e(AdminSection, {key: 'files', title: 'Files'}, [
                    e(KeyValue, {key: 'sf', label: 'Stored files', value: files.stored_files}),
                    e(KeyValue, {key: 'sb', label: 'Stored size', value: humanBytes(files.stored_bytes)}),
                    e(KeyValue, {key: 'ap', label: 'Attached to a post', value: files.attached_to_post}),
                    e(KeyValue, {key: 'rc', label: 'Meeting recordings', value: files.recordings +
                        (files.recording_bytes ? ' · ' + humanBytes(files.recording_bytes) : '')}),
                    e(KeyValue, {key: 'lg', label: 'Largest file', value: humanBytes(files.largest_file_bytes)}),
                    e(KeyValue, {key: 'po', label: 'Possible orphans', value: files.possible_orphans +
                        (files.orphan_bytes ? ' · ' + humanBytes(files.orphan_bytes) : ''),
                        warn: files.possible_orphans > 0}),
                    e(KeyValue, {key: 'pd', label: 'Post deleted, file kept', value: files.post_deleted_file_kept,
                        warn: files.post_deleted_file_kept > 0}),
                    e(KeyValue, {key: 'rm', label: 'Recordings missing their file', value: files.recordings_missing_file,
                        warn: files.recordings_missing_file > 0}),
                    e(KeyValue, {key: 'sd', label: 'Soft-deleted rows', value: files.soft_deleted}),
                    e(KeyValue, {key: 'mf', label: 'Attachment limit', value: humanBytes(files.max_file_bytes)}),
                    e(KeyValue, {key: 'mr', label: 'Recording limit', value: humanBytes(files.max_recording_bytes)}),
                    e(Flag, {key: 'pl', label: 'Public file links', value: !files.public_links_enabled,
                        words: ['Disabled', 'ENABLED']}),
                    e('div', {key: 'note', className: 'hw-form-note', style: {marginTop: 6}},
                        'Diagnostic only. Nothing here is deleted automatically; "possible orphans" includes files uploaded but not yet attached.'),
                ]),

                e(AdminSection, {key: 'notif', title: 'Notifications'}, [
                    e(Flag, {key: 'bot', label: 'Notification bot', value: notif.bot_configured,
                        words: ['Configured', 'Missing']}),
                    e(KeyValue, {key: 'total', label: 'Sent (total)', value: notif.total || 0}),
                    e(KeyValue, {key: 'last', label: 'Last sent', value: notif.last_sent_at ? formatWhen(notif.last_sent_at) : 'never'}),
                ].concat(Object.keys(notif.by_kind || {}).sort().map(function (k) {
                    return e(KeyValue, {key: 'k-' + k, label: '  ' + k.replace(/_/g, ' '), value: notif.by_kind[k]});
                }))),

                // The AI assistant's wiring, as booleans and counts. The
                // service URL and both tokens stay on the server; this says
                // only whether each is set and whether the service answered
                // the health probe on this refresh.
                e(AdminSection, {key: 'ai', title: 'AI Assistant'}, [
                    e(Flag, {key: 'cb', label: 'Callback secret', value: ai.callback_configured,
                        words: ['Configured', 'Not configured']}),
                    e(Flag, {key: 'svc', label: 'Service URL', value: ai.service_configured,
                        words: ['Configured', 'Not configured']}),
                    ai.service_configured ? e(Flag, {key: 'reach', label: 'Service reachable',
                        value: ai.service_reachable, words: ['Yes', ai.service_error || 'No']}) : null,
                    e(KeyValue, {key: 'st', label: 'Sessions stored', value: ai.sessions_stored || 0}),
                    e(KeyValue, {key: 'lv', label: 'Sessions live', value: ai.sessions_live || 0}),
                    e(KeyValue, {key: 'cp', label: 'Sessions completed', value: ai.sessions_completed || 0}),
                    e('div', {key: 'note', className: 'hw-form-note', style: {marginTop: 6}},
                        (ai.callback_configured || ai.service_configured)
                            ? 'The AI service produces the transcript, suggestions and summaries; Honco stores a bounded copy per meeting and shows it to that meeting’s channel members.'
                            : 'Set the AI service URL or the AI callback secret in System Console › Plugins › Honco Workspace to enable the assistant.'),
                ]),

                e(AdminSection, {key: 'plugin', title: 'Honco plugin'}, [
                    e(KeyValue, {key: 'v', label: 'Version', value: plug.version || '—'}),
                    e(KeyValue, {key: 'hm', label: 'Honco migration', value: plug.honco_migration}),
                    e(KeyValue, {key: 'mm', label: 'Mattermost migrations', value: plug.mattermost_migration}),
                    e(Flag, {key: 'bot', label: 'Notification bot', value: plug.bot_configured,
                        words: ['Configured', 'Missing']}),
                ]),

                e(AdminSection, {key: 'fail', title: 'Recent failures'},
                    failures.length === 0 ?
                        e('div', {className: 'hw-status', style: {fontSize: 13, color: 'rgba(var(--center-channel-color-rgb), 0.72)'}}, [
                            e(Icon, {key: 'i', name: 'check-circle-outline', style: {color: 'var(--online-indicator)', fontSize: 15}}),
                            e('span', {key: 't'}, 'No recent failures.'),
                        ]) :
                        failures.map(function (f, i) {
                            return e('div', {
                                key: i,
                                style: {
                                    fontSize: 12, padding: '5px 0',
                                    borderBottom: '1px solid rgba(var(--center-channel-color-rgb), 0.06)',
                                },
                            }, [
                                e('div', {key: 'h', style: {display: 'flex', gap: 8, alignItems: 'center'}}, [
                                    e(Badge, {key: 'k', tone: 'err'}, f.kind),
                                    e('span', {
                                        key: 't',
                                        style: {
                                            marginLeft: 'auto',
                                            color: 'rgba(var(--center-channel-color-rgb), 0.56)',
                                        },
                                    }, f.at ? new Date(f.at).toLocaleString() : ''),
                                ]),
                                f.subject ? e('div', {
                                    key: 's',
                                    style: {marginTop: 2, wordBreak: 'break-word'},
                                }, f.subject) : null,
                                f.detail ? e('div', {
                                    key: 'd',
                                    style: {
                                        marginTop: 2, opacity: 0.75, wordBreak: 'break-word',
                                    },
                                }, f.detail) : null,
                            ]);
                        })),

                e(AdminSection, {key: 'sec', title: 'Security configuration'}, [
                    e(Flag, {key: 'signup', label: 'Open self-signup', value: sec.self_signup_enabled, invert: true}),
                    e(Flag, {key: 'mfa', label: 'Multi-factor auth', value: sec.mfa_enabled}),
                    e(Flag, {key: 'files', label: 'File attachments', value: sec.file_attachments_enabled}),
                    e(Flag, {key: 'public', label: 'Public file links', value: sec.public_file_links_enabled, invert: true}),
                    e(Flag, {key: 'plugins', label: 'Plugins', value: sec.plugins_enabled}),
                    e(Flag, {key: 'uploads', label: 'Plugin uploads', value: sec.plugin_uploads_enabled}),
                    e(Flag, {key: 'sig', label: 'Require plugin signature', value: sec.require_plugin_signature}),
                    e(Flag, {key: 'email', label: 'Email notifications', value: sec.email_notifications_enabled}),
                    e(Flag, {key: 'push', label: 'Push notifications', value: sec.push_notifications_enabled}),
                ]),

                // Deliberately "configured or not". No value from any of
                // these is ever sent to the browser.
                e(AdminSection, {key: 'creds', title: 'Credentials (presence only)'}, [
                    e(Flag, {key: 'smtp', label: 'SMTP', value: sec.smtp_configured,
                        words: ['Configured', 'Not configured']}),
                    e(Flag, {key: 'pushsrv', label: 'Push server', value: sec.push_server_configured,
                        words: ['Configured', 'Not configured']}),
                    e(Flag, {key: 'jibri', label: 'Jibri callback secret', value: sec.jibri_callback_configured,
                        words: ['Configured', 'Not configured']}),
                    e(Flag, {key: 'meet', label: 'Meet service secret', value: sec.meet_service_configured,
                        words: ['Configured', 'Not configured']}),
                    e(Flag, {key: 'sum', label: 'Summarizer endpoint', value: sec.summarizer_configured,
                        words: ['Configured', 'Not configured']}),
                    e(Flag, {key: 'sup', label: 'Support channel', value: sec.support_channel_configured,
                        words: ['Configured', 'Not configured']}),
                    e('div', {key: 'note', className: 'hw-form-note', style: {marginTop: 6}},
                        'Secret values are never sent to the browser — only whether each is set.'),
                ]),

                e(AdminSection, {key: 'net', title: 'Network'}, [
                    e(KeyValue, {key: 'site', label: 'Site URL', value: sec.site_url || '—'}),
                    e('div', {key: 'n', className: 'hw-form-note', style: {marginTop: 4}},
                        'Honco Chat checks the WebSocket origin against this. A stale value ' +
                        'breaks real-time updates while the server still answers HTTP.'),
                ]),

                e('div', {
                    key: 'foot',
                    className: 'hw-form-note',
                    style: {
                        paddingTop: 6, marginBottom: 0,
                        borderTop: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
                    },
                }, health.data && health.data.checked_at
                    ? 'Last checked ' + new Date(health.data.checked_at).toLocaleTimeString()
                    : ''),
            ]),
        ]);
    }

    // --- Organization (company) administration -----------------------------
    //
    // Capability-driven: rendered only when /me/capabilities reports the
    // logged-in user is an org admin (or system admin). It is not a separate
    // login and there is no role switch -- the same account simply sees these
    // controls in addition to everything else it can do. Every action calls a
    // server API that re-authorizes from stored membership.

    function orgResolveUsername(username) {
        var u = String(username || '').trim().replace(/^@/, '');
        if (!u) {
            return Promise.reject(new Error('Enter a username'));
        }
        return fetch('/api/v4/users/username/' + encodeURIComponent(u), {
            credentials: 'same-origin', headers: {'X-Requested-With': 'XMLHttpRequest'},
        }).then(function (res) {
            if (!res.ok) {
                throw new Error('No user @' + u);
            }
            return res.json();
        });
    }

    function OrgDashboard(props) {
        var d = React.useState({loading: true, data: null, error: null});
        var st = d[0], set = d[1];
        var load = React.useCallback(function () {
            set({loading: true, data: null, error: null});
            request('GET', '/orgs/' + props.orgId + '/overview').then(function (o) {
                set({loading: false, data: o, error: null});
            }).catch(function (err) {
                set({loading: false, data: null, error: err.message});
            });
        }, [props.orgId]);
        React.useEffect(function () {
            load();
        }, [load]);
        if (st.loading) {
            return e(Loading, {label: 'Loading dashboard'});
        }
        if (st.error) {
            return e(ErrorNote, {}, st.error);
        }
        var o = st.data || {};
        var tasks = o.tasks || {};
        var support = o.support || {};
        return e('div', {className: 'hw-list hw-fade', style: {padding: '12px'}}, [
            e(AdminSection, {key: 'o', title: 'Overview'}, [
                e(KeyValue, {key: 'd', label: 'Departments (teams)', value: o.teams}),
                e(KeyValue, {key: 'm', label: 'Members', value: o.members}),
                e(KeyValue, {key: 't', label: 'Tasks (total)', value: tasks.total}),
                e(KeyValue, {key: 'so', label: 'Support (open)', value: support.open}),
                e(KeyValue, {key: 'stt', label: 'Support (total)', value: support.total}),
                e(KeyValue, {key: 'ms', label: 'Meeting summaries', value: o.meeting_summaries}),
            ]),
            e('div', {key: 'note', className: 'hw-form-note'}, 'Company-wide totals across the departments this organization owns.'),
        ]);
    }

    function OrgMembers(props) {
        var d = React.useState({loading: true, rows: [], error: null});
        var st = d[0], set = d[1];
        var a = React.useState('');
        var addName = a[0], setAddName = a[1];
        var b = React.useState({busy: false, msg: '', err: ''});
        var act = b[0], setAct = b[1];

        var load = React.useCallback(function () {
            set({loading: true, rows: [], error: null});
            request('GET', '/orgs/' + props.orgId + '/members').then(function (o) {
                set({loading: false, rows: (o && o.members) || [], error: null});
            }).catch(function (err) {
                set({loading: false, rows: [], error: err.message});
            });
        }, [props.orgId]);
        React.useEffect(function () {
            load();
        }, [load]);

        function run(promise, okMsg) {
            setAct({busy: true, msg: '', err: ''});
            promise.then(function () {
                setAct({busy: false, msg: okMsg || 'Done', err: ''});
                load();
            }).catch(function (err) {
                setAct({busy: false, msg: '', err: err.message});
            });
        }
        function addMember() {
            setAct({busy: true, msg: '', err: ''});
            orgResolveUsername(addName).then(function (u) {
                return request('POST', '/orgs/' + props.orgId + '/members', {user_id: u.id, role: 'org_member'});
            }).then(function () {
                setAddName('');
                setAct({busy: false, msg: 'Member added', err: ''});
                load();
            }).catch(function (err) {
                setAct({busy: false, msg: '', err: err.message});
            });
        }

        if (st.loading) {
            return e(Loading, {label: 'Loading members'});
        }
        if (st.error) {
            return e(ErrorNote, {}, st.error);
        }
        var body = st.rows.map(function (m) {
            var teams = (m.teams || []).map(function (t) {
                return e(Badge, {key: t.team_id, tone: t.team_admin ? 'ok' : ''},
                    t.display_name + (t.team_admin ? ' · admin' : ''));
            });
            var isAdmin = m.role === 'org_admin';
            return e('div', {key: m.user_id, className: 'hw-kv', style: {alignItems: 'center', gap: 8, flexWrap: 'wrap'}}, [
                e('span', {key: 'n', style: {flex: '1 1 160px', minWidth: 120}}, [
                    e('strong', {key: 'nm'}, m.name || m.username || m.user_id),
                    e('span', {key: 'un', style: {opacity: 0.6}}, m.username ? '  @' + m.username : ''),
                    m.is_guest ? e(Badge, {key: 'g', tone: 'warn'}, 'Guest') : null,
                    m.support_agent ? e(Badge, {key: 'sa', tone: 'ok'}, 'Support') : null,
                ]),
                e('span', {key: 'r'}, e(Badge, {tone: isAdmin ? 'ok' : ''}, isAdmin ? 'Org Admin' : 'Member')),
                e('span', {key: 't', style: {display: 'flex', gap: 4, flexWrap: 'wrap'}}, teams.length ? teams : e('span', {style: {opacity: 0.5}}, '—')),
                e('span', {key: 'act', style: {display: 'flex', gap: 4}}, [
                    isAdmin
                        ? e(Button, {key: 'dm', small: true, kind: 'ghost', disabled: act.busy,
                            onClick: function () {
                                run(request('PATCH', '/orgs/' + props.orgId + '/members/' + m.user_id, {role: 'org_member'}), 'Demoted');
                            }}, 'Demote')
                        : e(Button, {key: 'pr', small: true, kind: 'ghost', disabled: act.busy,
                            onClick: function () {
                                run(request('PATCH', '/orgs/' + props.orgId + '/members/' + m.user_id, {role: 'org_admin'}), 'Promoted');
                            }}, 'Make admin'),
                    e(Button, {key: 'rm', small: true, kind: 'danger-link', disabled: act.busy,
                        onClick: function () {
                            run(request('DELETE', '/orgs/' + props.orgId + '/members/' + m.user_id), 'Removed');
                        }}, 'Remove'),
                ]),
            ]);
        });
        return e('div', {className: 'hw-list hw-fade', style: {padding: '12px'}}, [
            e('div', {key: 'add', style: {display: 'flex', gap: 6, marginBottom: 10}}, [
                e('input', {key: 'i', className: 'hw-input', style: {marginBottom: 0}, placeholder: 'Add member by @username',
                    value: addName, onChange: function (ev) {
                        setAddName(ev.target.value);
                    },
                    onKeyDown: function (ev) {
                        if (ev.key === 'Enter') {
                            addMember();
                        }
                    }}),
                e(Button, {key: 'b', small: true, disabled: act.busy || !addName, onClick: addMember}, 'Add'),
            ]),
            act.err ? e(ErrorNote, {key: 'e'}, act.err) : null,
            act.msg ? e('div', {key: 'm', className: 'hw-form-note'}, act.msg) : null,
            st.rows.length ? e('div', {key: 'list'}, body) : e(EmptyState, {key: 'empty', icon: 'account-multiple-outline', title: 'No members'}),
        ]);
    }

    function OrgTeams(props) {
        var d = React.useState({loading: true, rows: [], error: null});
        var st = d[0], set = d[1];
        var s = React.useState(null);
        var openTeam = s[0], setOpenTeam = s[1];
        var c = React.useState('');
        var newName = c[0], setNewName = c[1];
        var b = React.useState({busy: false, err: ''});
        var act = b[0], setAct = b[1];

        var load = React.useCallback(function () {
            set({loading: true, rows: [], error: null});
            request('GET', '/orgs/' + props.orgId + '/teams').then(function (o) {
                set({loading: false, rows: (o && o.teams) || [], error: null});
            }).catch(function (err) {
                set({loading: false, rows: [], error: err.message});
            });
        }, [props.orgId]);
        React.useEffect(function () {
            load();
        }, [load]);

        function createTeam() {
            setAct({busy: true, err: ''});
            request('POST', '/orgs/' + props.orgId + '/teams/create', {display_name: newName}).then(function () {
                setNewName('');
                setAct({busy: false, err: ''});
                load();
            }).catch(function (err) {
                setAct({busy: false, err: err.message});
            });
        }

        if (openTeam) {
            return e(OrgTeamMembers, {orgId: props.orgId, team: openTeam, onBack: function () {
                setOpenTeam(null);
                load();
            }});
        }
        if (st.loading) {
            return e(Loading, {label: 'Loading departments'});
        }
        if (st.error) {
            return e(ErrorNote, {}, st.error);
        }
        var rows = st.rows.map(function (t) {
            return e('div', {key: t.team_id, className: 'hw-kv', style: {alignItems: 'center'}}, [
                e('span', {key: 'n', style: {flex: 1}}, [
                    e('strong', {key: 'd'}, t.display_name || t.name),
                    e('span', {key: 'c', style: {opacity: 0.6}}, '  ' + (t.member_count || 0) + ' members'),
                ]),
                e(Button, {key: 'o', small: true, kind: 'ghost', onClick: function () {
                    setOpenTeam(t);
                }}, 'Manage'),
            ]);
        });
        return e('div', {className: 'hw-list hw-fade', style: {padding: '12px'}}, [
            e('div', {key: 'new', style: {display: 'flex', gap: 6, marginBottom: 10}}, [
                e('input', {key: 'i', className: 'hw-input', style: {marginBottom: 0}, placeholder: 'New department name (invite-only)',
                    value: newName, onChange: function (ev) {
                        setNewName(ev.target.value);
                    },
                    onKeyDown: function (ev) {
                        if (ev.key === 'Enter') {
                            createTeam();
                        }
                    }}),
                e(Button, {key: 'b', small: true, disabled: act.busy || !newName, onClick: createTeam}, 'Create'),
            ]),
            act.err ? e(ErrorNote, {key: 'e'}, act.err) : null,
            st.rows.length ? e('div', {key: 'list'}, rows) : e(EmptyState, {key: 'empty', icon: 'account-group-outline', title: 'No departments yet'}),
        ]);
    }

    function OrgTeamMembers(props) {
        var d = React.useState({loading: true, rows: [], error: null});
        var st = d[0], set = d[1];
        var b = React.useState({busy: false, err: ''});
        var act = b[0], setAct = b[1];
        var load = React.useCallback(function () {
            set({loading: true, rows: [], error: null});
            request('GET', '/orgs/' + props.orgId + '/teams/' + props.team.team_id + '/members').then(function (o) {
                set({loading: false, rows: (o && o.members) || [], error: null});
            }).catch(function (err) {
                set({loading: false, rows: [], error: err.message});
            });
        }, [props.orgId, props.team]);
        React.useEffect(function () {
            load();
        }, [load]);

        function toggleAdmin(m) {
            setAct({busy: true, err: ''});
            var path = '/orgs/' + props.orgId + '/teams/' + props.team.team_id + '/admins/' + m.user_id;
            request(m.team_admin ? 'DELETE' : 'POST', path).then(function () {
                setAct({busy: false, err: ''});
                load();
            }).catch(function (err) {
                setAct({busy: false, err: err.message});
            });
        }
        var rows = st.rows.map(function (m) {
            return e('div', {key: m.user_id, className: 'hw-kv', style: {alignItems: 'center'}}, [
                e('span', {key: 'n', style: {flex: 1}}, [
                    e('strong', {key: 'd'}, m.name || m.username),
                    m.team_admin ? e(Badge, {key: 'a', tone: 'ok'}, 'Team Admin') : null,
                    m.is_guest ? e(Badge, {key: 'g', tone: 'warn'}, 'Guest') : null,
                ]),
                m.is_bot ? null : e(Button, {key: 't', small: true, kind: 'ghost', disabled: act.busy, onClick: function () {
                    toggleAdmin(m);
                }}, m.team_admin ? 'Remove admin' : 'Make admin'),
            ]);
        });
        return e('div', {className: 'hw-list hw-fade', style: {padding: '12px'}}, [
            e(Toolbar, {key: 'bar', icon: 'account-group-outline', title: props.team.display_name || props.team.name},
                e(Button, {key: 'back', small: true, kind: 'ghost', icon: 'arrow-left', className: 'hw-spacer', onClick: props.onBack}, 'Back')),
            act.err ? e(ErrorNote, {key: 'e'}, act.err) : null,
            st.loading ? e(Loading, {key: 'l', label: 'Loading team'}) :
                (st.rows.length ? e('div', {key: 'list'}, rows) : e(EmptyState, {key: 'empty', title: 'No members'})),
        ]);
    }

    function OrgSettings(props) {
        var d = React.useState({loading: true, org: null, error: null});
        var st = d[0], set = d[1];
        var f = React.useState({name: '', display_name: ''});
        var form = f[0], setForm = f[1];
        var b = React.useState({busy: false, msg: '', err: ''});
        var act = b[0], setAct = b[1];
        var load = React.useCallback(function () {
            request('GET', '/orgs/' + props.orgId).then(function (o) {
                set({loading: false, org: o, error: null});
                setForm({name: o.name || '', display_name: o.display_name || ''});
            }).catch(function (err) {
                set({loading: false, org: null, error: err.message});
            });
        }, [props.orgId]);
        React.useEffect(function () {
            load();
        }, [load]);
        function save() {
            setAct({busy: true, msg: '', err: ''});
            request('PATCH', '/orgs/' + props.orgId, {name: form.name, display_name: form.display_name}).then(function () {
                setAct({busy: false, msg: 'Saved', err: ''});
                load();
            }).catch(function (err) {
                setAct({busy: false, msg: '', err: err.message});
            });
        }
        if (st.loading) {
            return e(Loading, {label: 'Loading settings'});
        }
        if (st.error) {
            return e(ErrorNote, {}, st.error);
        }
        var o = st.org || {};
        return e('div', {className: 'hw-list hw-fade', style: {padding: '12px'}}, [
            e('label', {key: 'ln', className: 'hw-form-note'}, 'Organization name'),
            e('input', {key: 'n', className: 'hw-input', value: form.name, onChange: function (ev) {
                setForm({name: ev.target.value, display_name: form.display_name});
            }}),
            e('label', {key: 'ld', className: 'hw-form-note'}, 'Display name'),
            e('input', {key: 'd', className: 'hw-input', value: form.display_name, onChange: function (ev) {
                setForm({name: form.name, display_name: ev.target.value});
            }}),
            e(KeyValue, {key: 'slug', label: 'Slug (fixed)', value: o.slug}),
            e(KeyValue, {key: 'status', label: 'Status', value: o.status}),
            act.err ? e(ErrorNote, {key: 'e'}, act.err) : null,
            act.msg ? e('div', {key: 'm', className: 'hw-form-note'}, act.msg) : null,
            e('div', {key: 'save', style: {marginTop: 8}},
                e(Button, {disabled: act.busy || !form.name, onClick: save}, 'Save changes')),
        ]);
    }

    function OrgPanel(props) {
        var s = React.useState('dashboard');
        var sub = s[0], setSub = s[1];
        var caps = props.caps || {};
        var org = caps.organization || null;
        if (!org || !caps.is_org_admin) {
            return e('div', {className: 'hw'}, [
                e(Toolbar, {key: 'b', icon: 'domain', title: 'Organization'}),
                e(EmptyState, {key: 'e', icon: 'lock-outline', title: 'No organization administration'},
                    'You do not administer an organization.'),
            ]);
        }
        function subTab(id, label) {
            return e('button', {key: id, className: 'hw-tab', role: 'tab', 'aria-selected': sub === id,
                onClick: function () {
                    setSub(id);
                }}, e('span', {}, label));
        }
        return e('div', {className: 'hw'}, [
            e('div', {key: 'head', className: 'hw-bar'},
                e('span', {key: 't', className: 'hw-bar-title'}, [
                    e(Icon, {key: 'i', name: 'domain'}),
                    e('span', {key: 'l'}, org.name),
                ])),
            e('div', {key: 'subtabs', role: 'tablist', 'aria-label': 'Organization', className: 'hw-tabs'}, [
                subTab('dashboard', 'Dashboard'),
                subTab('members', 'Members'),
                subTab('teams', 'Teams'),
                subTab('settings', 'Settings'),
            ]),
            e('div', {key: 'body', style: {flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column'}},
                sub === 'dashboard' ? e(OrgDashboard, {orgId: org.id}) :
                    (sub === 'members' ? e(OrgMembers, {orgId: org.id, caps: caps}) :
                        (sub === 'teams' ? e(OrgTeams, {orgId: org.id}) :
                            e(OrgSettings, {orgId: org.id})))),
        ]);
    }

    function HoncoPanel() {
        // Open on Meeting Intelligence when this channel has a remembered
        // selection, so a refresh returns the user to the summary they were
        // reading rather than dropping them back on Tasks.
        var initialTab = 'tasks';
        try {
            var ch = window.store ? window.store.getState().entities.channels.currentChannelId : null;
            if (MeetingIntent.recall(ch)) {
                initialTab = 'meetings';
            }
        } catch (err) {
            initialTab = 'tasks';
        }
        // Someone pressed "View Summary" on a card while the panel was
        // closed: land on the tab that answers that click, not on Tasks.
        if (MeetingIntent.wanted) {
            initialTab = 'meetings';
        }
        // Someone pressed "AI Assistant" on a card or in the App Bar while
        // this panel was closed: that is a direct request, and it wins over
        // whatever tab the channel was last left on.
        if (AIIntent.wanted) {
            initialTab = 'ai';
        }

        // Whether to OFFER the Admin tab. Read from the webapp's own store
        // purely so a non-admin is not shown a tab that would 403 on every
        // request. This is not authorization: /admin/* asks Mattermost for
        // manage_system on the server for every single call, and a user who
        // edits this in their browser gains exactly nothing.
        var isAdmin = false;
        try {
            var st = window.store ? window.store.getState() : null;
            if (st) {
                var me = st.entities.users.profiles[st.entities.users.currentUserId];
                isAdmin = Boolean(me && (me.roles || '').split(' ').indexOf('system_admin') >= 0);
            }
        } catch (err) {
            isAdmin = false;
        }

        // The logged-in user's capabilities, from the server. Used only to
        // decide whether to OFFER the Organization tab -- the server still
        // authorizes every /orgs call from stored membership, so a browser
        // that forces this flag gains nothing. This is how one account sees
        // org-admin controls without a separate login or a role switch.
        var cp = React.useState(null);
        var caps = cp[0];
        var setCaps = cp[1];
        React.useEffect(function () {
            request('GET', '/me/capabilities').then(function (d) {
                setCaps(d);
            }).catch(function () {
                setCaps(null);
            });
        }, []);
        var showOrg = Boolean(caps && caps.is_org_admin);

        var t = React.useState(initialTab);
        var tab = t[0];
        var setTab = t[1];

        // Opening a summary from a card should land on the right tab, not
        // leave the user looking at Tasks wondering what happened.
        React.useEffect(function () {
            var stop = MeetingIntent.subscribe(function () {
                MeetingIntent.wanted = false;
                setTab('meetings');
            });
            // Raised while this panel was closed: the click that opened it
            // fired its listeners before any of this existed, so the flag
            // is what survives the gap. MeetingPanel clears the meeting id
            // once it has selected it.
            if (MeetingIntent.wanted) {
                MeetingIntent.wanted = false;
                setTab('meetings');
            }
            return stop;
        }, []);

        // A search started from Mattermost's own search box lands here.
        React.useEffect(function () {
            return SearchIntent.subscribe(function () {
                setTab('search');
            });
        }, []);

        // "AI Assistant" on a meeting card, or the AI icon in the App Bar.
        // Both may fire before this panel exists, so the flag is read on
        // mount too; AIPanel clears the meeting id once it has selected it.
        React.useEffect(function () {
            var stop = AIIntent.subscribe(function () {
                AIIntent.wanted = false;
                setTab('ai');
            });
            if (AIIntent.wanted) {
                AIIntent.wanted = false;
                setTab('ai');
            }
            return stop;
        }, []);

        ensureStyles();

        // Six tabs have to fit the sidebar at its default width, so the
        // bar uses a 12px label there and grows (with icons) when the panel
        // is wide. Where a label is shortened on screen the full feature
        // name remains the accessible name and the tooltip.
        function tabButton(id, label, icon, shortLabel) {
            var active = tab === id;
            return e('button', {
                key: id,
                className: 'hw-tab',
                onClick: function () {
                    setTab(id);
                },
                'aria-selected': active,
                'aria-label': label,
                title: label,
                role: 'tab',
            }, [
                e(Icon, {key: 'i', name: icon}),
                e('span', {key: 'l'}, shortLabel || label),
            ]);
        }

        return e('div', {className: 'hw'}, [
            e('div', {key: 'tabs', role: 'tablist', 'aria-label': 'Honco Workspace', className: 'hw-tabs'}, [
                tabButton('tasks', 'Tasks', 'check-circle-outline'),
                tabButton('meetings', 'Meeting Intelligence', 'text-box-outline', 'Meetings'),
                tabButton('ai', 'AI Assistant', 'creation-outline'),
                tabButton('support', 'Support', 'monitor'),
                tabButton('files', 'Files', 'paperclip'),
                tabButton('search', 'Search', 'magnify'),
            ].concat(showOrg ? [tabButton('org', 'Organization', 'domain', 'Org')] : [])
                .concat(isAdmin ? [tabButton('admin', 'Admin', 'shield-outline')] : [])),

            e('div', {key: 'panel', style: {flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column'}},
                tab === 'tasks' ? e(TasksPanel) :
                    (tab === 'ai' ? e(AIPanel) :
                    (tab === 'meetings' ? e(MeetingPanel) :
                        (tab === 'support' ? e(SupportPanel) :
                        (tab === 'files' ? e(FilesPanel) :
                            (tab === 'search' ? e(SearchPanel, {
                                onOpenTasks: function () {
                                    setTab('tasks');
                                },
                            }) : (tab === 'org' ? e(OrgPanel, {caps: caps}) : e(AdminPanel)))))))),
        ]);
    }

    // --- Meeting card ------------------------------------------------------

    // Rendered for every post of type custom_honco_meeting. Everything it
    // shows comes from props the server built, after it had already proved
    // the reader may see this channel -- the component makes no
    // authorization decision of its own and asks for nothing extra.

    var MEETING_POST_TYPE = 'custom_honco_meeting';

    // Plugin websocket events arrive prefixed with custom_<plugin id>_.
    var MEETING_WS_EVENT = 'custom_' + PLUGIN_ID + '_meeting_updated';

    var STATUS_STYLE = {
        scheduled: {label: 'Scheduled', dot: 'var(--away-indicator, #ffbc42)'},
        active: {label: 'Meeting is active', dot: 'var(--online-indicator, #3db887)'},
        ended: {label: 'Meeting ended', dot: 'rgba(var(--center-channel-color-rgb), 0.32)'},
    };

    function statusOf(v) {
        return STATUS_STYLE[v] || STATUS_STYLE.ended;
    }

    function Avatar(props) {
        // A coloured initial, so a list of names reads as people at a
        // glance. Derived from the name itself so it is stable per person
        // without needing an identity we do not have.
        var name = props.name || '?';
        var hue = 0;
        for (var i = 0; i < name.length; i++) {
            hue = (hue * 31 + name.charCodeAt(i)) % 360;
        }
        return e('span', {
            style: {
                display: 'inline-flex',
                alignItems: 'center',
                justifyContent: 'center',
                width: 22,
                height: 22,
                borderRadius: '50%',
                marginRight: 8,
                flexShrink: 0,
                fontSize: 11,
                fontWeight: 700,
                color: '#fff',
                background: 'hsl(' + hue + ', 45%, 45%)',
            },
        }, name.charAt(0).toUpperCase());
    }

    // file id -> true when Mattermost said the file is gone. Session-scoped
    // memo so a channel with many cards asks about each recording once.
    var RecordingFileCheck = {};

    function MeetingCard(props) {
        var post = props.post || {};
        var p = (post.props && post.props.honco_meeting) || {};

        // The websocket tells us something changed; re-read the card from
        // the API rather than trusting anything the event carries.
        var s = React.useState(p);
        var card = s[0];
        var setCard = s[1];

        React.useEffect(function () {
            setCard(p);
        }, [post.update_at]);

        // Is the recording's file still there? The server rebuilds a card
        // only when the meeting changes state, so an ended meeting's card
        // would keep offering "View Recording" forever after the file was
        // removed. Ask Mattermost, whose answer is authorized and current:
        // a member gets 200, a deleted file gets 404, and a non-member is
        // refused -- nothing here decides anything itself. Remembered per
        // file for the session so a channel full of cards asks once each.
        var g = React.useState(null);
        var gone = g[0];
        var setGone = g[1];
        React.useEffect(function () {
            var fid = card.recording_file_id;
            if (card.recording_status !== 'ready' || !fid) {
                setGone(null);
                return;
            }
            if (Object.prototype.hasOwnProperty.call(RecordingFileCheck, fid)) {
                setGone(RecordingFileCheck[fid]);
                return;
            }
            var cancelled = false;
            fetch('/api/v4/files/' + encodeURIComponent(fid) + '/info',
                {credentials: 'same-origin', cache: 'no-store'}).then(function (res) {
                // 404 is "gone". Anything else -- including a transient
                // error -- is treated as present: a card must not claim a
                // recording is missing because a request timed out.
                var missing = res.status === 404;
                RecordingFileCheck[fid] = missing;
                if (!cancelled) {
                    setGone(missing);
                }
            }).catch(function () {
                if (!cancelled) {
                    setGone(false);
                }
            });
            return function () {
                cancelled = true;
            };
        }, [card.recording_status, card.recording_file_id]);
        if (gone === true && card.recording_status === 'ready') {
            card = Object.assign({}, card, {recording_status: 'unavailable', recording_file_id: ''});
        }

        var st = statusOf(card.status);
        var joinable = card.status !== 'ended' && card.join_url;

        function refresh() {
            if (!card.meeting_id) {
                return;
            }
            request('GET', '/meetings/' + encodeURIComponent(card.meeting_id))
                .then(function (data) {
                    if (data && data.card) {
                        setCard(data.card);
                    }
                }).catch(function () {
                    // A card that cannot refresh keeps showing what it has.
                });
        }

        React.useEffect(function () {
            var handler = function (msg) {
                if (msg && msg.data && msg.data.meeting_id === card.meeting_id) {
                    refresh();
                }
            };
            if (window.HoncoMeetingBus) {
                window.HoncoMeetingBus.push(handler);
            }
            return function () {
                if (window.HoncoMeetingBus) {
                    var i = window.HoncoMeetingBus.indexOf(handler);
                    if (i >= 0) {
                        window.HoncoMeetingBus.splice(i, 1);
                    }
                }
            };
        }, [card.meeting_id]);

        ensureStyles();

        var actions = [];
        if (card.recording_status === 'ready' && card.recording_file_id) {
            actions.push(e('a', {
                key: 'rec',
                className: 'hw-btn hw-btn-secondary hw-btn-sm',
                href: '/api/v4/files/' + card.recording_file_id,
                target: '_blank',
                rel: 'noopener noreferrer',
            }, [e(Icon, {key: 'i', name: 'play'}), 'View Recording']));
        } else if (card.recording_status === 'failed') {
            actions.push(e(Badge, {key: 'recfail', tone: 'err'}, 'Recording failed'));
        } else if (card.recording_status === 'unavailable') {
            // The recording existed but its file is gone. Said plainly,
            // rather than offering a link the server would refuse.
            actions.push(e(Badge, {key: 'recgone'}, 'Recording unavailable'));
        }
        // The assistant belongs to THIS meeting, and this is where a person
        // is when they think about it -- so the card is the entry point, not
        // an icon somewhere else. The id comes from the card's own props.
        if (card.meeting_id) {
            actions.push(e(Button, {
                key: 'ai',
                kind: 'secondary',
                small: true,
                icon: 'creation-outline',
                onClick: function () {
                    if (window.HoncoOpenAIAssistant) {
                        window.HoncoOpenAIAssistant(card.meeting_id);
                    }
                },
            }, 'AI Assistant'));
        }
        if (card.has_summary) {
            actions.push(e(Button, {
                key: 'sum',
                kind: 'secondary',
                small: true,
                icon: 'text-box-outline',
                onClick: function () {
                    // The summary lives in the Honco panel; point the user
                    // at it rather than duplicating it inside the card.
                    if (window.HoncoOpenMeetingIntelligence) {
                        window.HoncoOpenMeetingIntelligence(card.meeting_id);
                    }
                },
            }, 'View Summary'));
        }

        return e('div', {className: 'hw-card', 'data-honco-card': 'meeting'}, [
            e('div', {key: 'title', className: 'hw-card-title'}, [
                e(Icon, {key: 'i', name: 'video-outline'}),
                e('span', {key: 't'}, card.topic || 'Meeting'),
            ]),

            e('div', {key: 'by', className: 'hw-card-sub', style: {display: 'flex', alignItems: 'center', gap: 6}}, [
                e(UserAvatar, {key: 'av', userId: card.creator_id, size: 18}),
                e('span', {key: 't'}, 'Started by ' + (card.creator_name || 'someone')),
            ]),

            e('div', {key: 'status', className: 'hw-status', style: {marginBottom: 10}}, [
                e(Dot, {key: 'dot', color: st.dot}),
                e('span', {key: 'l'}, st.label),
                card.scheduled_for ? e('span', {
                    key: 'when',
                    style: {color: 'rgba(var(--center-channel-color-rgb), 0.64)'},
                }, '· ' + card.scheduled_for) : null,
            ]),

            (card.participant_count > 0 || (card.participants || []).length > 0) ? e('div', {
                key: 'people',
                style: {marginBottom: 12},
            }, [
                e('div', {key: 'count', className: 'hw-card-line', style: {fontWeight: 600, marginBottom: 6}}, [
                    e(Icon, {key: 'i', name: 'account-outline'}),
                    e('span', {key: 't'}, card.participant_count + ' participant' + (card.participant_count === 1 ? '' : 's')),
                ]),
                e('div', {key: 'list'}, (card.participants || []).map(function (name, i) {
                    return e('div', {
                        key: i,
                        style: {display: 'flex', alignItems: 'center', fontSize: 13, marginBottom: 4},
                    }, [
                        e(Avatar, {key: 'a', name: name}),
                        e('span', {key: 'n', style: {color: 'var(--center-channel-color)'}}, name),
                    ]);
                })),
            ]) : null,

            joinable ? e('div', {key: 'join', style: {marginBottom: actions.length ? 12 : 0}},
                e('a', {
                    className: 'hw-btn',
                    style: {padding: '8px 18px'},
                    href: card.join_url,
                    target: '_blank',
                    rel: 'noopener noreferrer',
                }, [e(Icon, {key: 'i', name: 'video-outline'}), 'Join Meeting'])) : null,

            (card.recording_status === 'ready' || card.has_summary) ? e('div', {
                key: 'available',
                style: {marginBottom: 8},
            }, [
                card.recording_status === 'ready' ? e('div', {key: 'r', className: 'hw-card-line'}, [
                    e(Icon, {key: 'i', name: 'file-video-outline'}), e('span', {key: 't'}, 'Recording available'),
                ]) : null,
                card.has_summary ? e('div', {key: 's', className: 'hw-card-line'}, [
                    e(Icon, {key: 'i', name: 'text-box-outline'}), e('span', {key: 't'}, 'Meeting Summary'),
                ]) : null,
            ]) : null,

            actions.length ? e('div', {key: 'actions', className: 'hw-actions'}, actions) : null,
        ]);
    }

    function ChecklistIcon() {
        return e('i', {className: 'icon icon-check-circle-outline', style: {fontSize: 15}});
    }

    // The App Bar takes an icon URL rather than a component, so the icon
    // is an inline SVG data URI -- no asset to serve and nothing to add to
    // the webapp's own build.
    //
    // AI_ICON_URL is the assistant's own entry: a spark, which is what the
    // rest of the product uses for generated content (icon-creation-outline
    // on the tab and the panel header).
    var ICON_URL = 'data:image/svg+xml;base64,' + btoa(
        '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ' +
        'stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
        '<path d="M9 11l3 3L22 4"/>' +
        '<path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/></svg>');

    var AI_ICON_URL = 'data:image/svg+xml;base64,' + btoa(
        '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ' +
        'stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
        '<path d="M12 3l1.9 4.6L18.5 9.5l-4.6 1.9L12 16l-1.9-4.6L5.5 9.5l4.6-1.9z"/>' +
        '<path d="M18 15l.8 2.2L21 18l-2.2.8L18 21l-.8-2.2L15 18l2.2-.8z"/></svg>');

    // --- Registration ------------------------------------------------------

    var Plugin = function () {};

    Plugin.prototype.initialize = function (registry, store) {
        // Kept for the panel to read the current team/user from the
        // webapp's own store rather than guessing from the URL.
        window.store = store;
        ensureStyles();

        // The meeting card. Registered as a post-type component so the
        // card IS the post -- one post per meeting, updated in place,
        // rather than a stream of near-identical messages.
        if (typeof registry.registerPostTypeComponent === 'function') {
            try {
                registry.registerPostTypeComponent(MEETING_POST_TYPE, MeetingCard);
                registry.registerPostTypeComponent(SUPPORT_POST_TYPE, SupportCard);
            } catch (err) {
                // An older server without post-type components still shows
                // the post's markdown fallback, which carries the same facts.
            }
        }

        // One websocket subscription for every card on screen. Cards add
        // themselves to this bus rather than each opening their own.
        window.HoncoMeetingBus = window.HoncoMeetingBus || [];
        window.HoncoSupportBus = window.HoncoSupportBus || [];
        window.HoncoAIBus = window.HoncoAIBus || [];
        window.HoncoReconnectBus = window.HoncoReconnectBus || [];
        // Set once the RHS is registered: how anything in the centre channel
        // opens the Honco panel without knowing how the RHS works.
        window.HoncoOpenWorkspace = window.HoncoOpenWorkspace || function () {};
        if (typeof registry.registerWebSocketEventHandler === 'function') {
            registry.registerWebSocketEventHandler(MEETING_WS_EVENT, function (msg) {
                window.HoncoMeetingBus.forEach(function (fn) {
                    try {
                        fn(msg);
                    } catch (err) {
                        /* one bad listener must not stop the rest */
                    }
                });
            });
            registry.registerWebSocketEventHandler(SUPPORT_WS_EVENT, function (msg) {
                window.HoncoSupportBus.forEach(function (fn) {
                    try {
                        fn(msg);
                    } catch (err) {
                        /* one bad listener must not stop the rest */
                    }
                });
            });
            // The AI service's events, relayed by the server to the
            // meeting's channel. The panel applies them in place.
            registry.registerWebSocketEventHandler(AI_WS_EVENT, function (msg) {
                window.HoncoAIBus.forEach(function (fn) {
                    try {
                        fn(msg);
                    } catch (err) {
                        /* one bad listener must not stop the rest */
                    }
                });
            });
        }
        // After a WebSocket reconnect the panel re-reads what it missed
        // rather than trusting its in-memory state.
        if (typeof registry.registerReconnectHandler === 'function') {
            try {
                registry.registerReconnectHandler(function () {
                    window.HoncoReconnectBus.forEach(function (fn) {
                        try {
                            fn();
                        } catch (err) {
                            /* one bad listener must not stop the rest */
                        }
                    });
                });
            } catch (err) { /* older webapp without the hook: the panel still re-reads on open */ }
        }

        // The App Bar is where current Mattermost surfaces plugin entry
        // points. Passing rhsComponent/rhsTitle makes the registry create
        // the RHS component and wire the toggle for us.
        var rhs = null;
        if (typeof registry.registerAppBarComponent === 'function') {
            try {
                var reg = registry.registerAppBarComponent(ICON_URL, undefined, 'Honco Workspace', null, HoncoPanel, 'Honco Workspace');
                rhs = reg && reg.rhsComponent ? reg.rhsComponent : null;
            } catch (err) {
                rhs = null;
            }
        }
        if (!rhs) {
            rhs = registry.registerRightHandSidebarComponent(HoncoPanel, 'Honco Workspace');
        }

        // One function every entry point uses to open the panel: the cards,
        // the AI app-bar icon and the channel-header button all go through
        // this, so there is exactly one way the panel opens.
        var openWorkspace = function () {
            try {
                store.dispatch(rhs.showRHSPlugin || rhs.toggleRHSPlugin);
            } catch (err) {
                try {
                    store.dispatch(rhs.toggleRHSPlugin);
                } catch (err2) { /* nothing else to try */ }
            }
        };
        window.HoncoOpenWorkspace = openWorkspace;

        // A second App Bar entry that goes straight to the assistant. The
        // Honco Workspace icon opens the panel where it was; this one names
        // the feature, so "AI Assistant" is reachable without knowing that
        // Honco Workspace contains it.
        if (typeof registry.registerAppBarComponent === 'function') {
            try {
                registry.registerAppBarComponent(AI_ICON_URL, function () {
                    AIIntent.open(null);
                }, 'Honco AI Assistant', null);
            } catch (err) { /* the tab inside the panel still works */ }
        }

        // Channel-header button on the SAME panel instance. Mattermost
        // hides channel-header plugin buttons on desktop while the App Bar
        // is shown, and lists them in the channel menu on phones -- where
        // there is no App Bar at all. Without this the panel is
        // unreachable on a phone.
        registry.registerChannelHeaderButtonAction(
            ChecklistIcon,
            function () {
                store.dispatch(rhs.toggleRHSPlugin);
            },
            'Honco Workspace',
            'Honco Workspace',
        );

        // A "Honco" option in Mattermost's own search box, beside Messages
        // and Files.
        //
        // Mattermost renders only messages and files in its search results
        // panel -- a plugin cannot add rows there -- so choosing Honco and
        // pressing Enter opens the Honco panel on its Search tab with the
        // same terms. The user searches from one place; Messages and Files
        // continue to work exactly as before, unmodified.
        if (typeof registry.registerSearchComponents === 'function') {
            try {
                registry.registerSearchComponents({
                    buttonComponent: function () {
                        return e('span', {}, 'Honco');
                    },
                    suggestionsComponent: function () {
                        return e('div', {
                            style: {
                                padding: '12px 20px',
                                fontSize: 12,
                                color: 'rgba(var(--center-channel-color-rgb), 0.64)',
                            },
                        }, 'Search Honco tasks, meetings, recordings, summaries and support requests.');
                    },
                    hintsComponent: function () {
                        return e('div', {
                            style: {
                                padding: '8px 20px',
                                fontSize: 12,
                                color: 'rgba(var(--center-channel-color-rgb), 0.56)',
                            },
                        }, 'Press Enter to search Honco. Only what you already have access to is searched.');
                    },
                    action: function (terms) {
                        SearchIntent.open(terms || '');
                    },
                });
            } catch (err) { /* the Search tab still works without the pill */ }
        }
    };

    window.registerPlugin(PLUGIN_ID, new Plugin());
}());
