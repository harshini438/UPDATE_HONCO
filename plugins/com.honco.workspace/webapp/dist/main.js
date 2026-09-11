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
        '.hw-row-title{font-size:13px;font-weight:600;color:var(--center-channel-color);word-break:break-word}',
        '.hw-row-done .hw-row-title{text-decoration:line-through;opacity:.7}',
        '.hw-row-desc{font-size:12px;margin-top:2px;white-space:pre-wrap;word-break:break-word;color:rgba(var(--center-channel-color-rgb),.72)}',
        '.hw-row-meta{display:flex;flex-wrap:wrap;align-items:center;gap:4px 10px;margin-top:6px;font-size:11px;color:rgba(var(--center-channel-color-rgb),.64)}',
        '.hw-row-meta .icon{font-size:13px;line-height:1;vertical-align:-1px}',
        '.hw-row-actions{display:flex;flex-wrap:wrap;align-items:center;gap:8px;margin-top:8px}',
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
                e('span', {key: 'as'}, [
                    e(Icon, {key: 'i', name: 'account-outline'}),
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

    function MeetingPanel() {
        var s0 = React.useState({channelId: null, meetings: [], statuses: {}, loading: true, error: null});
        var state = s0[0];
        var setState = s0[1];

        var s1 = React.useState({id: null, summary: null, loading: false, error: null, generating: false});
        var sel = s1[0];
        var setSel = s1[1];

        var s2 = React.useState(false);
        var showRaw = s2[0];
        var setShowRaw = s2[1];

        // The channel comes from the webapp's own store, so the panel
        // always follows whichever channel the user is actually reading.
        var channelId = null;
        try {
            channelId = window.store ? window.store.getState().entities.channels.currentChannelId : null;
        } catch (err) {
            channelId = null;
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
            requestStatus('GET', '/channels/' + encodeURIComponent(channelId) + '/meetings').then(function (res) {
                if (!res.ok) {
                    setState({channelId: channelId, meetings: [], statuses: {}, loading: false,
                        error: res.status === 404 ? null : 'Could not load meetings.'});
                    return;
                }
                setState({
                    channelId: channelId,
                    meetings: (res.data && res.data.meetings) || [],
                    statuses: (res.data && res.data.summary_status) || {},
                    loading: false,
                    error: null,
                });
            }).catch(function () {
                setState({channelId: channelId, meetings: [], statuses: {}, loading: false, error: 'Could not load meetings.'});
            });
        }, [channelId]);

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

        return e('div', {className: 'hw'}, [
            e('div', {key: 'picker', className: 'hw-bar'}, e('select', {
                value: sel.id || '',
                className: 'hw-select',
                style: {margin: 0},
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
                            e('div', {key: 'who', className: 'hw-row-meta', style: {marginTop: 4, fontSize: 12}},
                                'Requested by ' + (r.requester_id === ctx.userId ? 'you' : 'a colleague') +
                                (r.agent_id ? ' · agent assigned' : '')),
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
            e('div', {key: 'by', className: 'hw-card-sub'}, 'Requested by ' + (c.requester_name || 'someone')),
            e(StatusDot, {key: 'st', status: c.status}),
            c.issue ? e('div', {
                key: 'issue',
                style: {
                    fontSize: 13, marginTop: 8, whiteSpace: 'pre-wrap',
                    wordBreak: 'break-word', color: 'var(--center-channel-color)',
                },
            }, c.issue) : null,
            c.agent_name ? e('div', {key: 'agent', className: 'hw-card-line'}, [
                e(Icon, {key: 'i', name: 'account-outline'}),
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

        var t = React.useState(initialTab);
        var tab = t[0];
        var setTab = t[1];

        // Opening a summary from a card should land on the right tab, not
        // leave the user looking at Tasks wondering what happened.
        React.useEffect(function () {
            return MeetingIntent.subscribe(function () {
                setTab('meetings');
            });
        }, []);

        // A search started from Mattermost's own search box lands here.
        React.useEffect(function () {
            return SearchIntent.subscribe(function () {
                setTab('search');
            });
        }, []);

        ensureStyles();

        // Five tabs have to fit the sidebar at its default width, so the
        // bar uses a 12px label there and grows (with icons) when the panel
        // is wide. The feature names stay visible in full.
        function tabButton(id, label, icon) {
            var active = tab === id;
            return e('button', {
                key: id,
                className: 'hw-tab',
                onClick: function () {
                    setTab(id);
                },
                'aria-selected': active,
                role: 'tab',
            }, [
                e(Icon, {key: 'i', name: icon}),
                e('span', {key: 'l'}, label),
            ]);
        }

        return e('div', {className: 'hw'}, [
            e('div', {key: 'tabs', role: 'tablist', 'aria-label': 'Honco Workspace', className: 'hw-tabs'}, [
                tabButton('tasks', 'Tasks', 'check-circle-outline'),
                tabButton('meetings', 'Meeting Intelligence', 'text-box-outline'),
                tabButton('support', 'Support', 'monitor'),
                tabButton('search', 'Search', 'magnify'),
            ].concat(isAdmin ? [tabButton('admin', 'Admin', 'shield-outline')] : [])),

            e('div', {key: 'panel', style: {flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column'}},
                tab === 'tasks' ? e(TasksPanel) :
                    (tab === 'meetings' ? e(MeetingPanel) :
                        (tab === 'support' ? e(SupportPanel) :
                            (tab === 'search' ? e(SearchPanel, {
                                onOpenTasks: function () {
                                    setTab('tasks');
                                },
                            }) : e(AdminPanel))))),
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

            e('div', {key: 'by', className: 'hw-card-sub'}, 'Started by ' + (card.creator_name || 'someone')),

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
    var ICON_URL = 'data:image/svg+xml;base64,' + btoa(
        '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ' +
        'stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
        '<path d="M9 11l3 3L22 4"/>' +
        '<path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/></svg>');

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
