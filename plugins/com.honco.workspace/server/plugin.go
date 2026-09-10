package main

import (
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// Plugin is the Honco Workspace plugin.
//
// It exists so Honco features can be added to Honco Chat without editing
// the Mattermost tree. That tree is a fork whose entire delta is
// re-applied by idempotent de-brand scripts after every upstream pull;
// hand-written feature code living there would be at risk on each pull.
// A plugin is the supported extension point and survives upgrades.
type Plugin struct {
	plugin.MattermostPlugin

	client *pluginapi.Client
	store  *Store
	botID  string

	routerOnce sync.Once
	router     *mux.Router

	// stop closes when the plugin is deactivated, ending the background
	// due-date scanner. Without it the goroutine would outlive a plugin
	// reload and two scanners would run at once.
	stop     chan struct{}
	stopOnce sync.Once
}

func nowMillis() int64 { return time.Now().UnixMilli() }

// OnActivate wires everything up. Any error here stops the plugin loading,
// which is correct: a half-initialised plugin that serves requests without
// a store or a bot would be worse than one that refuses to start.
func (p *Plugin) OnActivate() error {
	p.client = pluginapi.NewClient(p.API, p.Driver)

	// The bot is how every Honco notification reaches a user. Ensuring it
	// is idempotent -- on restart it resolves to the same account.
	botID, err := p.client.Bot.EnsureBot(&model.Bot{
		Username:    "honco",
		DisplayName: "Honco",
		Description: "Honco Workspace: tasks, recordings and support notifications.",
	})
	if err != nil {
		return err
	}
	p.botID = botID

	// The same PostgreSQL database Mattermost uses. The plugin adds
	// tables to it; it never creates a database and never touches a
	// Mattermost-owned table.
	db, err := p.client.Store.GetMasterDB()
	if err != nil {
		return err
	}
	p.store = NewStore(db)

	if err := p.store.Migrate(); err != nil {
		return err
	}

	// Background due-date reminders. Every send is claimed in the
	// notification ledger first, so this can run on every node and still
	// produce exactly one message per task per due date.
	p.stop = make(chan struct{})
	go p.runDueScanner()

	p.client.Log.Info("Honco Workspace activated", "bot_id", botID)
	return nil
}

// OnDeactivate stops the background scanner. Mattermost calls this on
// plugin disable and on server shutdown; without it a reload would leave
// the previous scanner running alongside the new one.
func (p *Plugin) OnDeactivate() error {
	if p.stop != nil {
		p.stopOnce.Do(func() { close(p.stop) })
	}
	return nil
}

// ServeHTTP routes everything under /plugins/com.honco.workspace/.
//
// The server strips any client-supplied Mattermost-User-Id header and
// re-sets it only from a validated session, so its presence is proof of
// authentication and its absence is proof of anonymity. Every handler
// below relies on that and on nothing else for identity.
func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	p.routerOnce.Do(func() { p.router = p.newRouter() })
	p.router.ServeHTTP(w, r)
}
