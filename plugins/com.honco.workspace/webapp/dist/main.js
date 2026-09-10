/**
 * Honco Workspace -- webapp bundle.
 *
 * Written as plain ES5-compatible JavaScript against the React that the
 * Mattermost webapp already exposes on `window`. That is deliberate:
 * a webpack/babel pipeline would add a second frontend build to a project
 * that already has one, for a panel of this size. There is no new
 * framework, no new component library and no new design system here.
 *
 * Everything renders inside Mattermost's own right-hand sidebar and uses
 * Mattermost's own CSS custom properties (--center-channel-color,
 * --button-bg, ...), so it inherits the current Honco theme automatically
 * and changes appearance with it.
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

    var INPUT = {
        width: '100%',
        boxSizing: 'border-box',
        padding: '6px 8px',
        marginBottom: 6,
        borderRadius: 4,
        border: '1px solid rgba(var(--center-channel-color-rgb), 0.24)',
        background: 'var(--center-channel-bg)',
        color: 'var(--center-channel-color)',
        fontSize: 13,
    };
    var BTN = {
        background: 'var(--button-bg)',
        color: 'var(--button-color)',
        border: 'none',
        borderRadius: 4,
        padding: '6px 14px',
        fontSize: 13,
        fontWeight: 600,
        cursor: 'pointer',
    };
    var LINKBTN = {
        background: 'none',
        border: 'none',
        padding: 0,
        fontSize: 12,
        color: 'var(--link-color)',
        cursor: 'pointer',
    };

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
            style: {
                padding: '10px 12px',
                borderBottom: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
                background: 'rgba(var(--center-channel-color-rgb), 0.03)',
            },
        }, [
            e('input', {
                key: 'title',
                style: INPUT,
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
                style: Object.assign({}, INPUT, {minHeight: 54, resize: 'vertical'}),
                placeholder: 'Description (optional)',
                'aria-label': 'Task description',
                value: form.description,
                onChange: function (ev) {
                    set('description', ev.target.value);
                },
            }),
            e('select', {
                key: 'assignee',
                style: INPUT,
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
                style: INPUT,
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
                style: INPUT,
                type: 'date',
                'aria-label': 'Due date',
                value: form.due,
                onChange: function (ev) {
                    set('due', ev.target.value);
                },
            }),

            state.error ? e('div', {
                key: 'err',
                style: {color: 'var(--error-text)', fontSize: 12, marginBottom: 6},
            }, state.error) : null,

            e('div', {key: 'actions', style: {display: 'flex', gap: 8, flexWrap: 'wrap'}}, [
                e('button', {
                    key: 'save',
                    type: 'submit',
                    disabled: state.saving || !form.title.trim(),
                    style: Object.assign({}, BTN, {
                        opacity: (state.saving || !form.title.trim()) ? 0.6 : 1,
                    }),
                }, state.saving ? 'Saving…' : (props.task ? 'Save changes' : 'Create task')),
                e('button', {
                    key: 'cancel',
                    type: 'button',
                    onClick: props.onCancel,
                    style: Object.assign({}, BTN, {
                        background: 'transparent',
                        color: 'var(--center-channel-color)',
                        border: '1px solid rgba(var(--center-channel-color-rgb), 0.24)',
                    }),
                }, 'Cancel'),
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
            style: {
                padding: '10px 12px',
                borderBottom: '1px solid rgba(var(--center-channel-color-rgb), 0.08)',
            },
        }, [
            e('div', {
                key: 'title',
                style: {
                    fontSize: 13,
                    fontWeight: 600,
                    color: 'var(--center-channel-color)',
                    textDecoration: t.status === 'done' ? 'line-through' : 'none',
                    opacity: t.status === 'done' ? 0.7 : 1,
                    wordBreak: 'break-word',
                },
            }, t.title),

            t.description ? e('div', {
                key: 'desc',
                style: {
                    fontSize: 12,
                    color: 'rgba(var(--center-channel-color-rgb), 0.72)',
                    marginTop: 2,
                    whiteSpace: 'pre-wrap',
                    wordBreak: 'break-word',
                },
            }, t.description) : null,

            e('div', {
                key: 'meta',
                style: {
                    display: 'flex',
                    flexWrap: 'wrap',
                    alignItems: 'center',
                    gap: 8,
                    marginTop: 6,
                    fontSize: 11,
                    color: 'rgba(var(--center-channel-color-rgb), 0.64)',
                },
            }, [
                e('span', {key: 'as'}, t.assignee_id
                    ? (assignee ? memberLabel(assignee) : 'Assigned')
                    : 'Unassigned'),
                t.due_at ? e('span', {
                    key: 'due',
                    style: {color: overdue ? 'var(--error-text)' : 'inherit'},
                }, (overdue ? 'Overdue · ' : 'Due ') + formatDue(t.due_at)) : null,
            ]),

            e('div', {
                key: 'actions',
                style: {display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8, marginTop: 8},
            }, [
                e('select', {
                    key: 'status',
                    value: t.status,
                    'aria-label': 'Task status',
                    onChange: function (ev) {
                        props.onStatus(t.id, ev.target.value);
                    },
                    style: {
                        fontSize: 12,
                        padding: '3px 6px',
                        borderRadius: 4,
                        background: 'var(--center-channel-bg)',
                        color: 'var(--center-channel-color)',
                        border: '1px solid rgba(var(--center-channel-color-rgb), 0.24)',
                    },
                }, STATUSES.map(function (s) {
                    return e('option', {key: s.value, value: s.value}, s.label);
                })),

                props.canEdit ? e('button', {
                    key: 'edit',
                    style: LINKBTN,
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
                    e('button', {
                        key: 'yes',
                        style: Object.assign({}, LINKBTN, {color: 'var(--error-text)', fontWeight: 600}),
                        onClick: function () {
                            setConfirming(false);
                            props.onDelete(t.id);
                        },
                    }, 'Yes'),
                    e('button', {
                        key: 'no',
                        style: LINKBTN,
                        onClick: function () {
                            setConfirming(false);
                        },
                    }, 'No'),
                ]) : e('button', {
                    key: 'del',
                    style: Object.assign({}, LINKBTN, {color: 'var(--error-text)'}),
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
            if (f.status) {
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

        var selectStyle = {
            fontSize: 12,
            padding: '3px 6px',
            borderRadius: 4,
            background: 'var(--center-channel-bg)',
            color: 'var(--center-channel-color)',
            border: '1px solid rgba(var(--center-channel-color-rgb), 0.24)',
        };

        return e('div', {style: {display: 'flex', flexDirection: 'column', height: '100%'}}, [

            e('div', {
                key: 'bar',
                style: {
                    padding: '8px 12px',
                    borderBottom: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
                    display: 'flex',
                    gap: 6,
                    alignItems: 'center',
                    flexWrap: 'wrap',
                },
            }, [
                e('select', {
                    key: 'status',
                    value: filter.status,
                    'aria-label': 'Filter by status',
                    onChange: function (ev) {
                        changeFilter({status: ev.target.value, mine: filter.mine});
                    },
                    style: selectStyle,
                }, [e('option', {key: 'all', value: ''}, 'All statuses')].concat(
                    STATUSES.map(function (s) {
                        return e('option', {key: s.value, value: s.value}, s.label);
                    }),
                )),
                e('label', {
                    key: 'mine',
                    style: {fontSize: 12, display: 'inline-flex', alignItems: 'center', gap: 4},
                }, [
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
                e('button', {
                    key: 'new',
                    onClick: function () {
                        setEditor({creating: !editor.creating, editing: null});
                    },
                    style: Object.assign({}, BTN, {marginLeft: 'auto', padding: '5px 12px', fontSize: 12}),
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

            state.error ? e('div', {
                key: 'err',
                style: {padding: '10px 12px', color: 'var(--error-text)', fontSize: 13},
            }, state.error) : null,

            e('div', {key: 'list', style: {overflowY: 'auto', flex: 1}},
                state.loading ? e('div', {
                    style: {padding: 16, fontSize: 13, opacity: 0.7},
                }, 'Loading tasks…') :
                    (state.tasks.length === 0 ? e('div', {
                        style: {padding: 16, fontSize: 13, opacity: 0.7},
                    }, page > 0
                        ? 'No more tasks on this page.'
                        : 'No tasks yet. Use "New task" to add one.') :
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

            e('div', {
                key: 'foot',
                style: {
                    padding: '6px 12px',
                    borderTop: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
                    display: 'flex',
                    alignItems: 'center',
                    gap: 8,
                    flexWrap: 'wrap',
                    fontSize: 11,
                    color: 'rgba(var(--center-channel-color-rgb), 0.56)',
                },
            }, [
                e('button', {
                    key: 'prev',
                    disabled: !hasPrev || state.loading,
                    onClick: function () {
                        setPage(Math.max(0, page - 1));
                    },
                    style: Object.assign({}, selectStyle, {
                        cursor: hasPrev ? 'pointer' : 'default',
                        opacity: hasPrev ? 1 : 0.45,
                    }),
                }, 'Previous'),
                e('button', {
                    key: 'next',
                    disabled: !hasNext || state.loading,
                    onClick: function () {
                        setPage(page + 1);
                    },
                    style: Object.assign({}, selectStyle, {
                        cursor: hasNext ? 'pointer' : 'default',
                        opacity: hasNext ? 1 : 0.45,
                    }),
                }, 'Next'),
                e('span', {key: 'range'}, state.tasks.length === 0
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
        var tone = props.tone === 'error' ? 'var(--error-text)' : 'rgba(var(--center-channel-color-rgb), 0.72)';
        return e('div', {
            style: {padding: '12px', fontSize: 13, color: tone, lineHeight: 1.5},
        }, props.children);
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

        var buttonStyle = {
            background: 'var(--button-bg)',
            color: 'var(--button-color)',
            border: 'none',
            borderRadius: 4,
            padding: '6px 14px',
            fontSize: 13,
            fontWeight: 600,
            cursor: 'pointer',
        };
        var linkStyle = {
            background: 'none',
            border: 'none',
            padding: 0,
            fontSize: 12,
            color: 'var(--link-color)',
            cursor: 'pointer',
        };

        if (!channelId) {
            return e(StatusNote, {}, 'Open a channel to see its meetings.');
        }
        if (state.loading) {
            return e(StatusNote, {}, 'Loading meetings…');
        }
        if (state.error) {
            return e(StatusNote, {tone: 'error'}, state.error);
        }
        if (state.meetings.length === 0) {
            return e(StatusNote, {}, 'No meetings have been held in this channel yet. Start one with /meet and a summary can be generated from the conversation afterwards.');
        }

        var summary = sel.summary;
        var body = null;

        if (sel.loading) {
            body = e(StatusNote, {}, 'Loading summary…');
        } else if (sel.generating || (summary && summary.status === 'pending')) {
            body = e(StatusNote, {}, 'Generating the summary from this channel’s conversation… this can take a minute.');
        } else if (sel.error) {
            body = e('div', {}, [
                e(StatusNote, {key: 'e', tone: 'error'}, sel.error),
                e('div', {key: 'r', style: {padding: '0 12px 12px'}},
                    e('button', {style: buttonStyle, onClick: function () {
                        generate(sel.id, true);
                    }}, 'Try again')),
            ]);
        } else if (!summary) {
            body = e('div', {}, [
                e(StatusNote, {key: 'n'}, 'No summary has been generated for this meeting yet.'),
                e('div', {key: 'b', style: {padding: '0 12px 12px'}},
                    e('button', {style: buttonStyle, onClick: function () {
                        generate(sel.id, false);
                    }}, 'Generate summary')),
            ]);
        } else if (summary.status === 'failed') {
            body = e('div', {}, [
                e(StatusNote, {key: 'e', tone: 'error'},
                    summary.error_message || 'Summary generation failed.'),
                e('div', {key: 'r', style: {padding: '0 12px 12px'}},
                    e('button', {style: buttonStyle, onClick: function () {
                        generate(sel.id, true);
                    }}, 'Try again')),
            ]);
        } else if (summary.status === 'empty') {
            body = e('div', {}, [
                e(StatusNote, {key: 'n'}, summary.summary ||
                    'Nothing was posted in this channel during the meeting, so there is nothing to summarise.'),
                e('div', {key: 'r', style: {padding: '0 12px 12px'}},
                    e('button', {style: buttonStyle, onClick: function () {
                        generate(sel.id, true);
                    }}, 'Regenerate')),
            ]);
        } else {
            body = e('div', {style: {padding: '12px'}}, [
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
                    e('button', {
                        key: 'toggle',
                        style: linkStyle,
                        onClick: function () {
                            setShowRaw(!showRaw);
                        },
                    }, showRaw ? 'Hide full output' : 'Show full output'),
                    showRaw ? e('pre', {
                        key: 'pre',
                        style: {
                            marginTop: 6,
                            padding: 8,
                            fontSize: 12,
                            lineHeight: 1.45,
                            whiteSpace: 'pre-wrap',
                            background: 'rgba(var(--center-channel-color-rgb), 0.04)',
                            borderRadius: 4,
                            color: 'var(--center-channel-color)',
                        },
                    }, summary.raw_output) : null,
                ]) : null,

                e('div', {key: 'regen', style: {marginTop: 12}},
                    e('button', {
                        style: buttonStyle,
                        onClick: function () {
                            generate(sel.id, true);
                        },
                    }, 'Regenerate')),
            ]);
        }

        return e('div', {style: {display: 'flex', flexDirection: 'column', height: '100%'}}, [
            e('div', {
                key: 'picker',
                style: {
                    padding: '8px 12px',
                    borderBottom: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
                },
            }, e('select', {
                value: sel.id || '',
                'aria-label': 'Meeting',
                onChange: function (ev) {
                    if (ev.target.value) {
                        loadSummary(ev.target.value);
                    }
                },
                style: {
                    width: '100%',
                    fontSize: 13,
                    padding: '5px 6px',
                    borderRadius: 4,
                    background: 'var(--center-channel-bg)',
                    color: 'var(--center-channel-color)',
                    border: '1px solid rgba(var(--center-channel-color-rgb), 0.24)',
                },
            }, [e('option', {key: '', value: ''}, 'Choose a meeting…')].concat(
                state.meetings.map(function (m) {
                    var mark = state.statuses[m.id] === 'ready' ? ' ✓' : '';
                    return e('option', {key: m.id, value: m.id},
                        meetingLabel(m) + ' · ' + formatWhen(m.created_at) + mark);
                }),
            ))),

            e('div', {key: 'body', style: {overflowY: 'auto', flex: 1}},
                sel.id ? body : e(StatusNote, {}, 'Choose a meeting to see or generate its summary.')),
        ]);
    }

    // --- The right-hand sidebar panel: Tasks and Meeting Intelligence ------

    // One entry point, two views. A second App Bar icon for a panel this
    // size would crowd the bar; tabs are what Mattermost's own RHS uses
    // for the same problem.
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

        function tabButton(id, label) {
            var active = tab === id;
            return e('button', {
                key: id,
                onClick: function () {
                    setTab(id);
                },
                'aria-selected': active,
                role: 'tab',
                style: {
                    flex: 1,
                    background: 'none',
                    border: 'none',
                    borderBottom: active ? '2px solid var(--button-bg)' : '2px solid transparent',
                    padding: '9px 8px',
                    fontSize: 13,
                    fontWeight: active ? 600 : 400,
                    color: active ? 'var(--center-channel-color)' : 'rgba(var(--center-channel-color-rgb), 0.64)',
                    cursor: 'pointer',
                },
            }, label);
        }

        return e('div', {style: {display: 'flex', flexDirection: 'column', height: '100%'}}, [
            e('div', {
                key: 'tabs',
                role: 'tablist',
                style: {
                    display: 'flex',
                    borderBottom: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
                },
            }, [tabButton('tasks', 'Tasks'), tabButton('meetings', 'Meeting Intelligence')]),

            e('div', {key: 'panel', style: {flex: 1, minHeight: 0}},
                tab === 'tasks' ? e(TasksPanel) : e(MeetingPanel)),
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

        var btn = {
            background: 'var(--button-bg)',
            color: 'var(--button-color)',
            border: 'none',
            borderRadius: 4,
            padding: '8px 18px',
            fontSize: 13,
            fontWeight: 600,
            cursor: 'pointer',
            textDecoration: 'none',
            display: 'inline-block',
        };
        var secondary = {
            background: 'transparent',
            color: 'var(--button-bg)',
            border: '1px solid var(--button-bg)',
            borderRadius: 4,
            padding: '6px 14px',
            fontSize: 12,
            fontWeight: 600,
            cursor: 'pointer',
            marginRight: 8,
        };

        var actions = [];
        if (card.recording_status === 'ready' && card.recording_file_id) {
            actions.push(e('a', {
                key: 'rec',
                href: '/api/v4/files/' + card.recording_file_id,
                target: '_blank',
                rel: 'noopener noreferrer',
                style: Object.assign({}, secondary, {textDecoration: 'none'}),
            }, 'View Recording'));
        } else if (card.recording_status === 'failed') {
            actions.push(e('span', {
                key: 'recfail',
                style: {fontSize: 12, color: 'var(--error-text)', marginRight: 8},
            }, 'Recording failed'));
        }
        if (card.has_summary) {
            actions.push(e('button', {
                key: 'sum',
                style: secondary,
                onClick: function () {
                    // The summary lives in the Honco panel; point the user
                    // at it rather than duplicating it inside the card.
                    if (window.HoncoOpenMeetingIntelligence) {
                        window.HoncoOpenMeetingIntelligence(card.meeting_id);
                    }
                },
            }, 'View Summary'));
        }

        return e('div', {
            style: {
                border: '1px solid rgba(var(--center-channel-color-rgb), 0.16)',
                borderRadius: 8,
                padding: '14px 16px',
                margin: '4px 0',
                maxWidth: 520,
                background: 'rgba(var(--center-channel-color-rgb), 0.03)',
            },
        }, [
            e('div', {
                key: 'title',
                style: {display: 'flex', alignItems: 'center', gap: 8, marginBottom: 2},
            }, [
                e('span', {key: 'i', style: {fontSize: 16}}, '🎥'),
                e('span', {
                    key: 't',
                    style: {fontSize: 15, fontWeight: 700, color: 'var(--center-channel-color)'},
                }, card.topic || 'Meeting'),
            ]),

            e('div', {
                key: 'by',
                style: {fontSize: 12, color: 'rgba(var(--center-channel-color-rgb), 0.72)', marginBottom: 8},
            }, 'Started by ' + (card.creator_name || 'someone')),

            e('div', {
                key: 'status',
                style: {display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, marginBottom: 10},
            }, [
                e('span', {
                    key: 'dot',
                    style: {
                        width: 8, height: 8, borderRadius: '50%',
                        background: st.dot, display: 'inline-block',
                    },
                }),
                e('span', {key: 'l', style: {color: 'var(--center-channel-color)'}}, st.label),
                card.scheduled_for ? e('span', {
                    key: 'when',
                    style: {color: 'rgba(var(--center-channel-color-rgb), 0.64)'},
                }, '· ' + card.scheduled_for) : null,
            ]),

            (card.participant_count > 0 || (card.participants || []).length > 0) ? e('div', {
                key: 'people',
                style: {marginBottom: 12},
            }, [
                e('div', {
                    key: 'count',
                    style: {
                        fontSize: 12,
                        fontWeight: 600,
                        color: 'rgba(var(--center-channel-color-rgb), 0.72)',
                        marginBottom: 6,
                    },
                }, '👥 ' + card.participant_count + ' participant' + (card.participant_count === 1 ? '' : 's')),
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
                    href: card.join_url,
                    target: '_blank',
                    rel: 'noopener noreferrer',
                    style: btn,
                }, 'Join Meeting')) : null,

            (card.recording_status === 'ready' || card.has_summary) ? e('div', {
                key: 'available',
                style: {
                    fontSize: 12,
                    color: 'rgba(var(--center-channel-color-rgb), 0.72)',
                    marginBottom: 6,
                },
            }, [
                card.recording_status === 'ready' ? e('div', {key: 'r'}, '🎬 Recording available') : null,
                card.has_summary ? e('div', {key: 's'}, '📝 Meeting Summary') : null,
            ]) : null,

            actions.length ? e('div', {key: 'actions'}, actions) : null,
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

        // The meeting card. Registered as a post-type component so the
        // card IS the post -- one post per meeting, updated in place,
        // rather than a stream of near-identical messages.
        if (typeof registry.registerPostTypeComponent === 'function') {
            try {
                registry.registerPostTypeComponent(MEETING_POST_TYPE, MeetingCard);
            } catch (err) {
                // An older server without post-type components still shows
                // the post's markdown fallback, which carries the same facts.
            }
        }

        // One websocket subscription for every card on screen. Cards add
        // themselves to this bus rather than each opening their own.
        window.HoncoMeetingBus = window.HoncoMeetingBus || [];
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
        }

        // The App Bar is where current Mattermost surfaces plugin entry
        // points. Passing rhsComponent/rhsTitle makes the registry create
        // the RHS component and wire the toggle for us.
        var appBarRegistered = false;
        if (typeof registry.registerAppBarComponent === 'function') {
            try {
                registry.registerAppBarComponent(ICON_URL, undefined, 'Honco Workspace', null, HoncoPanel, 'Honco Workspace');
                appBarRegistered = true;
            } catch (err) {
                appBarRegistered = false;
            }
        }

        // Channel-header button as well, so the panel is reachable even
        // where the App Bar is disabled. Registered against its own RHS
        // instance only when the App Bar did not already create one.
        if (!appBarRegistered) {
            var rhs = registry.registerRightHandSidebarComponent(HoncoPanel, 'Honco Workspace');
            registry.registerChannelHeaderButtonAction(
                ChecklistIcon,
                function () {
                    store.dispatch(rhs.toggleRHSPlugin);
                },
                'Honco Workspace',
                'Honco Workspace',
            );
        }
    };

    window.registerPlugin(PLUGIN_ID, new Plugin());
}());
