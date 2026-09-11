package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	securityHeaders(inner).ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/search?q=x", nil))
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
		"Cache-Control":          "no-store",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

// A burst is allowed, the request after it is not, and the bucket refills
// with time -- the three properties that make a token bucket a limiter
// rather than a counter.
func TestRateLimiterBurstThenRefuse(t *testing.T) {
	l := newRateLimiter(60, 3)
	for i := 0; i < 3; i++ {
		if !l.allow("k") {
			t.Fatalf("request %d of the burst was refused", i+1)
		}
	}
	if l.allow("k") {
		t.Fatal("the request after the burst was allowed")
	}
	// Another key is unaffected.
	if !l.allow("other") {
		t.Fatal("a different key was limited by the first key's bucket")
	}
}

func TestRateLimiterRefills(t *testing.T) {
	l := newRateLimiter(6000, 1) // 100/s so the test does not wait long
	if !l.allow("k") || l.allow("k") {
		t.Fatal("expected exactly one request in the burst")
	}
	time.Sleep(25 * time.Millisecond)
	if !l.allow("k") {
		t.Fatal("bucket did not refill")
	}
}

// Under a flood of never-seen keys the map must stop growing, and the
// answer while it is full must be refusal, not a crash or unbounded memory.
func TestRateLimiterBoundsKeys(t *testing.T) {
	l := newRateLimiter(60, 1)
	for i := 0; i < rateMaxKeys; i++ {
		l.allow(string(rune('a'+i%26)) + string(rune(i)))
	}
	if len(l.buckets) > rateMaxKeys {
		t.Fatalf("bucket map grew to %d, limit %d", len(l.buckets), rateMaxKeys)
	}
	if l.allow("one-more-brand-new-key") {
		t.Fatal("a new key was admitted while the map was full")
	}
}

func TestClientKeyPrefersUser(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	if got := clientKey(r); got != "ip:10.0.0.1" {
		t.Errorf("anonymous key = %q", got)
	}
	r.Header.Set("Mattermost-User-Id", "abc")
	if got := clientKey(r); got != "u:abc" {
		t.Errorf("authenticated key = %q", got)
	}
}

func TestRouteClassification(t *testing.T) {
	if !isServiceRoute("/api/v1/recordings/complete") || !isServiceRoute("/api/v1/meetings/register") {
		t.Error("service routes not recognised")
	}
	if isServiceRoute("/api/v1/meetings/abc/summary") {
		t.Error("a meeting summary route was mistaken for a service route")
	}
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if !isMutation(httptest.NewRequest(m, "/x", nil)) {
			t.Errorf("%s not treated as a mutation", m)
		}
	}
	if isMutation(httptest.NewRequest("GET", "/x", nil)) {
		t.Error("GET treated as a mutation")
	}
}
