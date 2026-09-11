package main

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// What Mattermost does NOT do for plugin HTTP.
//
// Mattermost routes /plugins/{id}/... straight to the plugin's handler
// (app/plugin_requests.go). Before it does, it validates the session,
// enforces CSRF for cookie-authenticated requests, and strips any
// Mattermost-User-Id header the client sent -- so authentication is
// sound. But that path bypasses web.Handler, which is where Mattermost
// sets its security headers and applies its rate limiter. Measured on
// this server: a plugin response carried no X-Content-Type-Options, no
// Referrer-Policy, no Cache-Control, and the rate limiter never saw it.
// This file supplies both, for this plugin's routes only.

// securityHeaders sets the headers every JSON response from this plugin
// should carry. Nothing here is a page, so it is never framed and its
// content type is never guessed; and everything here is per-user and
// authorization-sensitive, so no cache may keep a copy.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cache-Control", "no-store")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// --- rate limiting ---------------------------------------------------------

// A small token bucket per key. Memory is bounded by sweeping idle buckets;
// the map never grows without limit under a scan of random keys because
// each key's bucket is dropped once it has been quiet for sweepAfter.
type rateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	rate      float64 // tokens per second
	burst     float64
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

const (
	rateSweepEvery = time.Minute
	rateSweepAfter = 5 * time.Minute
	rateMaxKeys    = 50000
)

func newRateLimiter(perMinute, burst int) *rateLimiter {
	return &rateLimiter{
		buckets:   map[string]*bucket{},
		rate:      float64(perMinute) / 60,
		burst:     float64(burst),
		lastSweep: time.Now(),
	}
}

// allow reports whether one more request from key is within limits.
func (l *rateLimiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) > rateSweepEvery {
		for k, b := range l.buckets {
			if now.Sub(b.last) > rateSweepAfter {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}

	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= rateMaxKeys {
			// Under a flood of distinct keys, refuse rather than grow.
			return false
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Limits. Generous for a person, tight for a script.
//
//   - Authenticated mutations (tasks, support, summaries): 60 per minute
//     per user, burst 30. Nobody edits a task twice a second for a minute.
//   - Authenticated reads (search, lists): 240 per minute per user, burst
//     60 -- a busy panel refreshing after every websocket event stays well
//     inside this.
//   - Service routes (meeting registration, recording callback): 60 per
//     minute per remote address, burst 20. These authenticate with a shared
//     secret, so this is what stands between a wrong guess and the next
//     one: at 60/min a 32-byte secret is not being brute-forced this
//     century, but a broken script hammering the callback is also not
//     filling the disk.
var (
	limitMutations = newRateLimiter(60, 30)
	limitReads     = newRateLimiter(240, 60)
	limitService   = newRateLimiter(60, 20)
	// The AI event stream: 300/min with a burst of 60. Real speech is a
	// few utterances a minute per speaker; this leaves room for a service
	// that batches badly without letting a broken one flood the KV store.
	limitAIPush = newRateLimiter(300, 60)
)

// clientKey identifies the caller for limiting: the user for an
// authenticated request, the remote address otherwise. Mattermost has
// already stripped any client-supplied Mattermost-User-Id, so the header
// is trustworthy here.
func clientKey(r *http.Request) string {
	if uid := r.Header.Get("Mattermost-User-Id"); uid != "" {
		return "u:" + uid
	}
	return "ip:" + remoteIP(r)
}

func remoteIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// rateLimited wraps a handler with a limiter, answering 429 with a
// Retry-After when the caller is over. The refusal is logged at most as a
// warning with the key, never with any body or secret.
func (p *Plugin) rateLimited(l *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := clientKey(r)
		if !l.allow(key) {
			w.Header().Set("Retry-After", "10")
			writeJSON(w, http.StatusTooManyRequests, errorBody{Error: "too many requests; slow down"})
			p.client.Log.Warn("honco: rate limited", "key", key, "path", r.URL.Path)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isMutation is the method split the limiter uses.
func isMutation(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// isServiceRoute picks out the shared-secret routes.
func isServiceRoute(path string) bool {
	return strings.HasSuffix(path, "/meetings/register") || strings.HasSuffix(path, "/recordings/complete")
}

// isAIPushRoute is the AI service's event stream: chattier than the other
// service routes (an utterance every few seconds, batched or not), so it
// has its own budget rather than sharing the one sized for Jibri.
func isAIPushRoute(path string) bool {
	return strings.HasSuffix(path, "/ai/events")
}

// limitByRoute chooses the limiter for a request.
func (p *Plugin) limitByRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var l *rateLimiter
		switch {
		case isAIPushRoute(r.URL.Path):
			l = limitAIPush
		case isServiceRoute(r.URL.Path):
			l = limitService
		case isMutation(r):
			l = limitMutations
		default:
			l = limitReads
		}
		p.rateLimited(l, next).ServeHTTP(w, r)
	})
}
