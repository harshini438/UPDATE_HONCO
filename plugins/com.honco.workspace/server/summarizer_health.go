package main

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// Dependency probes for the two external services Honco talks to itself:
// the Claude summariser and the teammate's AI service.
//
// Both answer one operational question -- "is the thing Honco depends on
// reachable from this server right now?" -- which is exactly what an
// operator needs before deciding whether a failed summary is a bug or a
// network. Neither probe authenticates, sends a conversation, or reads a
// key, so neither can leak one: the summariser probe opens a TCP
// connection and closes it, and the AI probe uses the adapter's own
// /health.
//
// Nothing here is a substitute for the real call. A reachable port does
// not mean the forced command works, and the dashboard says so.

// summarizerDialTimeout is deliberately short. This runs when an admin
// opens a dashboard, not when a summary is generated, so it must fail
// fast rather than hold the page for the summariser's own 15s connect
// timeout.
const summarizerDialTimeout = 4 * time.Second

// probeSummarizer reports whether the summariser is reachable.
//
// Three shapes, because the transport has three:
//
//   - SummarizerCommand set: a local executable. Honco cannot verify it
//     runs correctly without running it, and running it would spend a
//     real summarisation, so this reports only that a local command is
//     configured.
//   - ssh transport: dial host:port. Open means the network path and the
//     listener exist. It does NOT mean the key is authorised -- proving
//     that needs a real SSH handshake with the real identity.
//   - no key configured: reported as degraded even when the port answers,
//     because a generation would still fail.
func (p *Plugin) probeSummarizer() healthCheck {
	return p.probeSummarizerWith(p.config())
}

// probeSummarizerWith is the probe itself, taking the configuration
// rather than reading it, so the decision table can be tested without a
// running plugin.
func (p *Plugin) probeSummarizerWith(cfg configuration) healthCheck {
	const name = "Summarizer"

	if cmd := strings.TrimSpace(cfg.SummarizerCommand); cmd != "" {
		// The path is an admin-set config value, not a secret, but it is
		// still not something a dashboard needs to show.
		return healthCheck{Name: name, Status: HealthUp,
			Detail: "local command configured (not executed by this probe)"}
	}

	host := firstNonEmpty(strings.TrimSpace(cfg.SummarizerHost), defaultSummarizerHost)
	port := cfg.SummarizerPort
	if port <= 0 || port > 65535 {
		port = defaultSummarizerPort
	}
	if !hostOK(host) {
		return healthCheck{Name: name, Status: HealthDown, Detail: "bad host configuration"}
	}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), summarizerDialTimeout)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		// The error text carries the host and port. An admin already has
		// those from the System Console, but a dashboard is a wider
		// audience than the console, so it gets the class, not the text.
		return healthCheck{Name: name, Status: HealthDown,
			Detail: "not reachable from this server", Latency: latency}
	}
	_ = conn.Close()

	if strings.TrimSpace(cfg.SummarizerKeyPath) == "" {
		// Reachable but unusable: no identity to present.
		return healthCheck{Name: name, Status: HealthDegraded,
			Detail: "reachable, but no SSH key is configured", Latency: latency}
	}
	return healthCheck{Name: name, Status: HealthUp,
		Detail: "port open (key not verified by this probe)", Latency: latency}
}

// probeAIService reports whether the teammate's AI service answers.
//
// It reuses the adapter, so it speaks the documented contract and carries
// the configured token in the Authorization header exactly as a real call
// would -- which means an auth failure is distinguishable from an
// unreachable host, and neither reveals the token.
func (p *Plugin) probeAIService() healthCheck {
	const name = "AI Service"
	cfg := p.config()
	if strings.TrimSpace(cfg.AIServiceURL) == "" {
		// Not an error. An unconfigured optional dependency is a
		// deliberate state, and the panel says "not configured" too.
		return healthCheck{Name: name, Status: HealthDegraded, Detail: "not configured"}
	}

	start := time.Now()
	err := p.aiService().Health()
	latency := time.Since(start).Milliseconds()
	if err == nil {
		return healthCheck{Name: name, Status: HealthUp, Detail: "healthy", Latency: latency}
	}
	// aiErrorKind is the same classification the panel shows, and it
	// never includes the URL or the token.
	return healthCheck{Name: name, Status: HealthDown,
		Detail: aiErrorKind(err), Latency: latency}
}
