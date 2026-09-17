package main

import (
	"context"
	"fmt"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"net/http"
	"sync"
	"time"
)

// Honco Administration.
//
// This complements the Mattermost System Console; it does not replace it.
// System Console already does users, roles, channels and service settings
// properly, with permission enforcement that is correct. What it cannot do
// is answer the Honco-specific questions: is Jibri actually able to
// record, did recordings fail, how many meetings are live, is the
// summariser reachable. That is what this is for.
//
// Two rules shape everything below.
//
// First, authorization is Mattermost's. There is no second admin role and
// no flag the browser can set: every endpoint asks Mattermost whether this
// session holds `manage_system`, which is the same permission that gates
// the System Console itself.
//
// Second, this reports configuration, never configuration *values*. A
// secret is reported as configured or not configured. There is no code
// path here that can emit an SMTP password, a Jibri callback secret, a
// Claude credential or a connection string, because none of them is ever
// read into a response struct in the first place.

const (
	// Probes are bounded so one dead service cannot hang the dashboard.
	// They also run concurrently, so the page costs one timeout at worst
	// rather than the sum of them.
	healthProbeTimeout = 4 * time.Second

	HealthUp       = "healthy"
	HealthDegraded = "degraded"
	HealthDown     = "unavailable"
)

type healthCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
	Latency int64  `json:"latency_ms,omitempty"`
}

type adminHealth struct {
	Checks    []healthCheck `json:"checks"`
	CheckedAt int64         `json:"checked_at"`
}

type adminUsage struct {
	Users          int `json:"users"`
	Teams          int `json:"teams"`
	Channels       int `json:"channels"`
	MeetingsTotal  int `json:"meetings_total"`
	MeetingsActive int `json:"meetings_active"`
	Recordings     int `json:"recordings"`
	Tasks          int `json:"tasks"`
	Summaries      int `json:"summaries"`
	SupportOpen    int `json:"support_open"`
	SupportTotal   int `json:"support_total"`
}

type adminFailure struct {
	Kind    string `json:"kind"`
	At      int64  `json:"at"`
	Subject string `json:"subject"`
	Detail  string `json:"detail,omitempty"`
}

// adminSecurity is booleans and "is it configured", never values.
type adminSecurity struct {
	SelfSignupEnabled     bool   `json:"self_signup_enabled"`
	MFAEnabled            bool   `json:"mfa_enabled"`
	FileAttachments       bool   `json:"file_attachments_enabled"`
	PublicFileLinks       bool   `json:"public_file_links_enabled"`
	PluginsEnabled        bool   `json:"plugins_enabled"`
	PluginUploads         bool   `json:"plugin_uploads_enabled"`
	RequirePluginSig      bool   `json:"require_plugin_signature"`
	EmailNotifications    bool   `json:"email_notifications_enabled"`
	PushNotifications     bool   `json:"push_notifications_enabled"`
	SMTPConfigured        bool   `json:"smtp_configured"`
	PushServerConfigured  bool   `json:"push_server_configured"`
	JibriCallbackSecretOK bool   `json:"jibri_callback_configured"`
	MeetServiceSecretOK   bool   `json:"meet_service_configured"`
	SummarizerConfigured  bool   `json:"summarizer_configured"`
	SupportChannelOK      bool   `json:"support_channel_configured"`
	SiteURL               string `json:"site_url"`
}

type adminPlugin struct {
	ID                  string `json:"id"`
	Version             string `json:"version"`
	HoncoMigration      int    `json:"honco_migration"`
	MattermostMigration int    `json:"mattermost_migration"`
	BotConfigured       bool   `json:"bot_configured"`
}

// requireSystemAdmin is the gate on everything in this file.
//
// It asks Mattermost for `manage_system` -- the permission that gates the
// System Console -- so an admin here is exactly an admin there, and
// removing someone's admin rights removes this too, with no second place
// to remember. A non-admin gets 403 rather than 404: unlike a task or a
// meeting, the existence of an admin dashboard is not a secret, and 403 is
// the honest answer.
func (p *Plugin) requireSystemAdmin(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return "", false
	}
	if !p.client.User.HasPermissionTo(userID, permissionManageSystem()) {
		p.writeErr(w, http.StatusForbidden, "system administrator access is required", nil)
		return "", false
	}
	return userID, true
}

// --- health ----------------------------------------------------------------

func (p *Plugin) handleAdminHealth(w http.ResponseWriter, r *http.Request) {
	if _, ok := p.requireSystemAdmin(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, p.collectHealth())
}

// collectHealth probes every dependency at once.
//
// Concurrent on purpose: six sequential probes with a four second timeout
// each would make a dashboard take half a minute the moment one service
// died. Fan out, wait, and the worst case is one timeout.
func (p *Plugin) collectHealth() *adminHealth {
	cfg := p.config()
	type probe struct {
		name string
		run  func() healthCheck
	}
	probes := []probe{
		{"Honco Chat", p.probeSelf},
		{"PostgreSQL", p.probeDatabase},
		{"Honco Plugin", p.probePlugin},
		{"Jitsi", func() healthCheck {
			return httpProbe("Jitsi", firstNonEmpty(cfg.MeetPublicURL, defaultMeetPublicURL), nil)
		}},
		{"Jibri", p.probeJibri},
		{"Meeting Service", func() healthCheck {
			return httpProbe("Meeting Service", "http://127.0.0.1:8077/health", nil)
		}},
		// The two services Honco calls out to. Both probes are
		// reachability only -- neither sends a conversation and neither
		// can leak a credential. See summarizer_health.go.
		{"Summarizer", p.probeSummarizer},
		{"AI Service", p.probeAIService},
	}

	out := make([]healthCheck, len(probes))
	var wg sync.WaitGroup
	for i, pr := range probes {
		wg.Add(1)
		go func(i int, pr probe) {
			defer wg.Done()
			defer func() {
				// A panicking probe must not take the dashboard with it.
				if rec := recover(); rec != nil {
					out[i] = healthCheck{Name: pr.name, Status: HealthDown, Detail: "probe failed"}
				}
			}()
			out[i] = pr.run()
		}(i, pr)
	}
	wg.Wait()

	return &adminHealth{Checks: out, CheckedAt: nowMillis()}
}

// httpProbe reports whether an endpoint answers. The detail is a status
// code or a short transport reason -- never a body, which could contain
// anything.
func httpProbe(name, url string, extra func(status int) string) healthCheck {
	ctx, cancel := context.WithTimeout(context.Background(), healthProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return healthCheck{Name: name, Status: HealthDown, Detail: "bad endpoint configuration"}
	}
	client := &http.Client{
		Timeout: healthProbeTimeout,
		// Jitsi serves a self-signed certificate on the LAN. Skipping
		// verification is acceptable for a liveness probe and nowhere
		// else -- nothing is read from the response.
		Transport: insecureProbeTransport(),
	}

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return healthCheck{Name: name, Status: HealthDown, Detail: "not reachable from this host", Latency: latency}
	}
	defer resp.Body.Close()

	status := HealthUp
	detail := ""
	if resp.StatusCode >= 500 {
		status, detail = HealthDown, fmt.Sprintf("HTTP %d", resp.StatusCode)
	} else if resp.StatusCode >= 400 {
		status, detail = HealthDegraded, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	if extra != nil {
		if d := extra(resp.StatusCode); d != "" {
			detail = d
		}
	}
	return healthCheck{Name: name, Status: status, Detail: detail, Latency: latency}
}

func (p *Plugin) probeSelf() healthCheck {
	// The plugin only runs because the server does, so reaching this code
	// is the evidence. Reporting a self-probe over HTTP would mostly test
	// the loopback interface.
	return healthCheck{Name: "Honco Chat", Status: HealthUp, Detail: "serving plugin requests"}
}

func (p *Plugin) probeDatabase() healthCheck {
	start := time.Now()
	err := p.store.Ping()
	latency := time.Since(start).Milliseconds()
	if err != nil {
		// The error can name a host or a user; it is not passed through.
		return healthCheck{Name: "PostgreSQL", Status: HealthDown,
			Detail: "query failed", Latency: latency}
	}
	status := HealthUp
	detail := ""
	if latency > 500 {
		status, detail = HealthDegraded, "slow response"
	}
	return healthCheck{Name: "PostgreSQL", Status: status, Detail: detail, Latency: latency}
}

func (p *Plugin) probePlugin() healthCheck {
	detail := "active"
	status := HealthUp
	if p.botID == "" {
		// Without the bot nothing can be delivered, which is degraded
		// rather than down: the API still works.
		status, detail = HealthDegraded, "notification bot unavailable"
	}
	return healthCheck{Name: "Honco Plugin", Status: status, Detail: detail}
}

// probeJibri reports whether the recorder can actually take a job, which
// is a different question from whether the container is running.
func (p *Plugin) probeJibri() healthCheck {
	check := httpProbe("Jibri", "http://127.0.0.1:2222/jibri/api/v1.0/health", nil)
	if check.Status != HealthUp {
		return check
	}
	busy, healthy, err := jibriStatus()
	if err != nil {
		check.Status, check.Detail = HealthDegraded, "status unreadable"
		return check
	}
	switch {
	case !healthy:
		check.Status, check.Detail = HealthDown, "reports unhealthy"
	case busy:
		check.Detail = "busy (recording)"
	default:
		check.Detail = "idle"
	}
	return check
}

// --- overview --------------------------------------------------------------

func (p *Plugin) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := p.requireSystemAdmin(w, r); !ok {
		return
	}

	usage, err := p.store.AdminUsage()
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not read usage", err)
		return
	}
	failures, err := p.store.RecentFailures(15)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not read failures", err)
		return
	}
	migration, mmMigration, err := p.store.MigrationVersions()
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not read migration state", err)
		return
	}
	files, err := p.store.AdminFiles()
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not read file statistics", err)
		return
	}
	// Notification status: what has been sent, by kind, and when the last
	// one went out. Aggregates only -- no recipient, no text.
	notifKinds, err := p.store.CountNotificationsByKind()
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not read notification counts", err)
		return
	}
	var notifTotal int
	for _, n := range notifKinds {
		notifTotal += n
	}
	var notifLast int64
	_ = p.store.db.QueryRow(`SELECT coalesce(max(created_at), 0) FROM honco_notifications`).Scan(&notifLast)
	notifications := map[string]any{
		"total":          notifTotal,
		"by_kind":        notifKinds,
		"last_sent_at":   notifLast,
		"bot_configured": p.botID != "",
	}

	files.MaxRecordingBytes = p.recordingLimit()
	if cfg := p.API.GetConfig(); cfg != nil {
		if cfg.FileSettings.MaxFileSize != nil {
			files.MaxFileBytes = *cfg.FileSettings.MaxFileSize
		}
		if cfg.FileSettings.EnablePublicLink != nil {
			files.PublicLinks = *cfg.FileSettings.EnablePublicLink
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"usage":         usage,
		"failures":      failures,
		"files":         files,
		"notifications": notifications,
		"ai":            p.adminAI(),
		"security":      p.adminSecurity(),
		"plugin": adminPlugin{
			ID:                  "com.honco.workspace",
			Version:             "0.1.0",
			HoncoMigration:      migration,
			MattermostMigration: mmMigration,
			BotConfigured:       p.botID != "",
		},
	})
}

// adminSecurity reports whether things are on and whether secrets exist.
// It deliberately reads only booleans and emptiness -- no value from the
// configuration is copied into the response.
func (p *Plugin) adminSecurity() adminSecurity {
	cfg := p.API.GetConfig()
	pc := p.config()

	sec := adminSecurity{
		JibriCallbackSecretOK: pc.JibriCallbackSecret != "",
		MeetServiceSecretOK:   pc.MeetServiceSecret != "",
		// "Configured" means Honco knows where to reach the summariser.
		// Whether it answers is a health question, not a config one.
		SummarizerConfigured: pc.SummarizerCommand != "" || pc.SummarizerHost != "" || defaultSummarizerHost != "",
		SupportChannelOK:     p.supportChannelID() != "",
	}
	if cfg == nil {
		return sec
	}

	sec.SelfSignupEnabled = derefBool(cfg.TeamSettings.EnableOpenServer)
	sec.MFAEnabled = derefBool(cfg.ServiceSettings.EnableMultifactorAuthentication)
	sec.FileAttachments = derefBool(cfg.FileSettings.EnableFileAttachments)
	sec.PublicFileLinks = derefBool(cfg.FileSettings.EnablePublicLink)
	sec.PluginsEnabled = derefBool(cfg.PluginSettings.Enable)
	sec.PluginUploads = derefBool(cfg.PluginSettings.EnableUploads)
	sec.RequirePluginSig = derefBool(cfg.PluginSettings.RequirePluginSignature)
	sec.EmailNotifications = derefBool(cfg.EmailSettings.SendEmailNotifications)
	sec.PushNotifications = derefBool(cfg.EmailSettings.SendPushNotifications)
	sec.SMTPConfigured = derefString(cfg.EmailSettings.SMTPServer) != ""
	sec.PushServerConfigured = derefString(cfg.EmailSettings.PushNotificationServer) != ""
	// The SiteURL is not a secret and is operationally important -- a
	// stale one silently breaks websockets, which is worth an admin
	// being able to see at a glance.
	sec.SiteURL = derefString(cfg.ServiceSettings.SiteURL)
	return sec
}

func derefBool(b *bool) bool {
	return b != nil && *b
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// adminAI: is the assistant wired up, and is the service answering. The
// health probe is the one outbound call the dashboard makes, and only on
// an explicit open/refresh -- never on a timer. Booleans and counts only.
func (p *Plugin) adminAI() map[string]any {
	pc := p.config()
	out := map[string]any{
		"callback_configured": pc.AICallbackSecret != "",
		"service_configured":  pc.AIServiceURL != "",
		"service_reachable":   false,
		"service_error":       "",
		"sessions_stored":     0,
		"sessions_live":       0,
		"sessions_completed":  0,
	}
	if pc.AIServiceURL != "" {
		if err := p.aiService().Health(); err != nil {
			out["service_error"] = aiErrorKind(err)
		} else {
			out["service_reachable"] = true
		}
	}
	// Bounded scan of stored sessions. The KV store lists keys in pages;
	// two pages is plenty for a dashboard number and keeps this cheap.
	stored, live, completed := 0, 0, 0
	for page := 0; page < 2; page++ {
		keys, err := p.client.KV.ListKeys(page, 200, pluginapi.WithPrefix("ai:session:"))
		if err != nil || len(keys) == 0 {
			break
		}
		for _, k := range keys {
			var s AISession
			if err := p.client.KV.Get(k, &s); err != nil || s.MeetingID == "" {
				continue
			}
			stored++
			switch s.Status {
			case AIStatusLive, AIStatusConnecting:
				live++
			case AIStatusCompleted:
				completed++
			}
		}
		if len(keys) < 200 {
			break
		}
	}
	out["sessions_stored"] = stored
	out["sessions_live"] = live
	out["sessions_completed"] = completed
	return out
}
