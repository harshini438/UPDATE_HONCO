package main

import (
	"bytes"
	"encoding/gob"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateEventRejectsUnknownAndEmpty(t *testing.T) {
	bad := []aiEvent{
		{Type: "audio"},
		{Type: AIEventTranscript},
		{Type: AIEventTranscript, Text: "   "},
		{Type: AIEventSuggestion},
		{Type: AIEventStatus},
		{Type: AIEventStatus, Status: "singing"},
		{Type: AIEventFinal},
	}
	for i, ev := range bad {
		if err := validateEvent(&ev); !errors.Is(err, errAIBadEvent) {
			t.Fatalf("case %d: want errAIBadEvent, got %v", i, err)
		}
	}
}

func TestValidateEventClipsAndNormalises(t *testing.T) {
	long := strings.Repeat("x", aiMaxLineChars+50)
	ev := aiEvent{Type: AIEventTranscript, Text: long, Speaker: strings.Repeat("s", 200), At: 5}
	if err := validateEvent(&ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.Lines) != 1 {
		t.Fatalf("single-line form should become one line, got %d", len(ev.Lines))
	}
	if l := ev.Lines[0]; len([]rune(l.Text)) != aiMaxLineChars+1 || len([]rune(l.Speaker)) != aiMaxSpeakerChars+1 {
		t.Fatalf("not clipped: text=%d speaker=%d", len([]rune(l.Text)), len([]rune(l.Speaker)))
	}
	if !ev.Lines[0].Final {
		t.Fatal("a line without an explicit final flag is final")
	}
	if ev.Lines[0].At != 5 {
		t.Fatal("line time defaults to the event time")
	}

	// A final event with only a transcript reference is still something.
	f := aiEvent{Type: AIEventFinal, TranscriptRef: "sess/abc"}
	if err := validateEvent(&f); err != nil {
		t.Fatal(err)
	}
}

func TestApplyOrdersBoundsAndDedupes(t *testing.T) {
	s := &AISession{Status: AIStatusIdle}
	now := int64(1000)

	// Content promotes idle -> live and numbers lines monotonically.
	for i := 0; i < aiMaxTranscriptLines+25; i++ {
		ev := aiEvent{Type: AIEventTranscript, Text: "line", Speaker: "Client"}
		if err := validateEvent(&ev); err != nil {
			t.Fatal(err)
		}
		if !s.apply(&ev, now) {
			t.Fatal("fresh event must be applied")
		}
	}
	if s.Status != AIStatusLive {
		t.Fatalf("status %q, want live", s.Status)
	}
	if len(s.Transcript) != aiMaxTranscriptLines {
		t.Fatalf("kept %d lines, want the bound %d", len(s.Transcript), aiMaxTranscriptLines)
	}
	if s.LineCount != int64(aiMaxTranscriptLines+25) {
		t.Fatalf("line count %d must include dropped lines", s.LineCount)
	}
	if s.Transcript[0].Seq != 25 || s.Transcript[len(s.Transcript)-1].Seq != int64(aiMaxTranscriptLines+24) {
		t.Fatalf("oldest lines must be the ones dropped: first seq %d", s.Transcript[0].Seq)
	}

	// A replayed event id changes nothing.
	sug := aiEvent{ID: "evt-1", Type: AIEventSuggestion, Text: "Ask about budget"}
	_ = validateEvent(&sug)
	if !s.apply(&sug, now) {
		t.Fatal("first delivery applies")
	}
	again := sug
	if s.apply(&again, now) {
		t.Fatal("replay must be ignored")
	}
	if len(s.Suggestions) != 1 || s.Suggestions[0].ID != "evt-1" {
		t.Fatalf("suggestions %+v", s.Suggestions)
	}

	// Final outputs complete the session and merge, not replace.
	f1 := aiEvent{Type: AIEventFinal, Summary: "S"}
	f2 := aiEvent{Type: AIEventFinal, ActionItems: []string{"a", "b"}}
	_ = validateEvent(&f1)
	_ = validateEvent(&f2)
	s.apply(&f1, now)
	s.apply(&f2, now)
	if s.Status != AIStatusCompleted || s.Final == nil || s.Final.Summary != "S" || len(s.Final.ActionItems) != 2 {
		t.Fatalf("final merge wrong: %+v", s.Final)
	}

	// A service error is a class, never the message.
	e := aiEvent{Type: AIEventError, Message: "stack trace with /secret/path", Kind: "asr_crashed"}
	_ = validateEvent(&e)
	s.apply(&e, now)
	if s.Status != AIStatusFailed || s.ErrorKind != "asr_crashed" {
		t.Fatalf("error state wrong: %s %s", s.Status, s.ErrorKind)
	}
}

func TestViewAndPaging(t *testing.T) {
	s := &AISession{Status: AIStatusLive}
	for i := 0; i < 500; i++ {
		ev := aiEvent{Type: AIEventTranscript, Text: "t"}
		_ = validateEvent(&ev)
		s.apply(&ev, 1)
	}
	v := s.view()
	if len(v.Transcript) != aiLiveWindowLines {
		t.Fatalf("view window %d, want %d", len(v.Transcript), aiLiveWindowLines)
	}
	if v.TranscriptGap {
		t.Fatal("nothing was dropped yet, so no gap")
	}
	if v.RecentEvents != nil {
		t.Fatal("replay ids must not reach a browser")
	}
	first := v.Transcript[0].Seq // 300
	page := s.transcriptBefore(first, 100)
	if len(page) != 100 || page[0].Seq != 200 || page[99].Seq != 299 {
		t.Fatalf("page wrong: len=%d first=%d", len(page), page[0].Seq)
	}
	if got := s.transcriptBefore(50, 100); len(got) != 50 || got[0].Seq != 0 {
		t.Fatalf("head page wrong: len=%d", len(got))
	}
	if got := s.transcriptBefore(0, 10); len(got) != 0 {
		t.Fatal("nothing before the first line")
	}
}

func TestAIBaseURLRejectsWhatItShould(t *testing.T) {
	bad := []string{"ftp://ai", "ai.internal", "http://", "http://user:pw@ai", "http://ai/?x=1", "http://ai/#f", "file:///etc/passwd"}
	for _, b := range bad {
		if _, err := aiBaseURL(b); err == nil {
			t.Fatalf("%q accepted", b)
		}
	}
	if _, err := aiBaseURL(""); !errors.Is(err, errAIUnconfigured) {
		t.Fatal("empty is 'not configured', not invalid")
	}
	u, err := aiBaseURL("https://ai.honco.internal/base/")
	if err != nil || u.Path != "/base" {
		t.Fatalf("good url rejected or path not trimmed: %v %q", err, u.Path)
	}
}

func TestAISessionIDOK(t *testing.T) {
	if !aiSessionIDOK("sess_01:abc-DEF.9") {
		t.Fatal("plain id refused")
	}
	for _, b := range []string{"", "a/b", "a b", "a\nb", "../x", strings.Repeat("a", aiMaxSessionIDChars+1)} {
		if aiSessionIDOK(b) {
			t.Fatalf("%q accepted", b)
		}
	}
}

func TestRedactErr(t *testing.T) {
	err := errors.New(`Post "http://ai.internal/v1/sessions?token=abc": dial tcp: connection refused`)
	got := redactErr(err)
	if strings.Contains(got, "token") || strings.Contains(got, "/v1/") {
		t.Fatalf("path not redacted: %s", got)
	}
	if !strings.Contains(got, "ai.internal") || !strings.Contains(got, "connection refused") {
		t.Fatalf("lost the useful part: %s", got)
	}
}

func TestHTTPAIServiceMapsResponses(t *testing.T) {
	var gotAuth, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		switch r.URL.Path {
		case "/base/v1/health":
			w.WriteHeader(200)
		case "/base/v1/sessions":
			_, _ = w.Write([]byte(`{"session_id":"s-1","status":"live"}`))
		case "/base/v1/sessions/s-401/end":
			w.WriteHeader(401)
		case "/base/v1/sessions/s-500":
			w.WriteHeader(503)
		case "/base/v1/sessions/s-400/summary":
			w.WriteHeader(400)
		case "/base/v1/sessions/s-redir":
			http.Redirect(w, r, "http://evil.example/steal", http.StatusFound)
		case "/base/v1/sessions/s-slow":
			time.Sleep(300 * time.Millisecond)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	base, _ := aiBaseURL(srv.URL + "/base")
	h := &httpAIService{base: base, token: "tok", client: &http.Client{Timeout: 150 * time.Millisecond,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("no") }}}

	if err := h.Health(); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok" || !strings.HasPrefix(gotUA, "honco-chat-ai/") {
		t.Fatalf("headers: %q %q", gotAuth, gotUA)
	}
	res, err := h.StartSession(AIStartRequest{MeetingID: "m"})
	if err != nil || res.SessionID != "s-1" || res.Status != "live" {
		t.Fatalf("start: %v %+v", err, res)
	}
	if err := h.EndSession("s-401"); !errors.Is(err, errAIAuth) {
		t.Fatalf("401 -> auth, got %v", err)
	}
	if _, err := h.SessionStatus("s-500"); !errors.Is(err, errAIUnavailable) {
		t.Fatalf("503 -> unavailable, got %v", err)
	}
	if _, err := h.Summary("s-400"); !errors.Is(err, errAIRefused) {
		t.Fatalf("400 -> refused, got %v", err)
	}
	if _, err := h.SessionStatus("s-redir"); err == nil || errors.Is(err, errAIAuth) {
		t.Fatalf("redirect must not be followed: %v", err)
	}
	if _, err := h.SessionStatus("s-slow"); !errors.Is(err, errAITimeout) && !errors.Is(err, errAIUnavailable) {
		t.Fatalf("timeout class: %v", err)
	}
	if aiErrorKind(errAITimeout) != "timeout" || aiErrorKind(errAIUnconfigured) != "not_configured" || aiErrorKind(errors.New("x")) != "unreachable" {
		t.Fatal("error kinds")
	}
}

func TestUnconfiguredServiceNeverSucceeds(t *testing.T) {
	var s AIIntegrationService = unconfiguredAIService{}
	if _, err := s.StartSession(AIStartRequest{}); !errors.Is(err, errAIUnconfigured) {
		t.Fatal("start")
	}
	if err := s.Health(); !errors.Is(err, errAIUnconfigured) {
		t.Fatal("health")
	}
	if _, err := s.Summary("x"); !errors.Is(err, errAIUnconfigured) {
		t.Fatal("summary")
	}
}

// The WebSocket payload crosses the plugin RPC boundary gob-encoded. A
// struct hidden inside map[string]any is not something gob will encode,
// and a failed encode wedges the RPC stream -- so every value in the
// payload must be the generic JSON shape.
func TestBroadcastPayloadIsGobSafe(t *testing.T) {
	gob.Register(map[string]any{})
	gob.Register([]any{})
	s := &AISession{Status: AIStatusLive}
	for _, ev := range []aiEvent{
		{Type: AIEventTranscript, Text: "hello", Speaker: "Client"},
		{Type: AIEventSuggestion, Text: "ask", Kind: "next_best_action"},
		{Type: AIEventInsight, Text: "cost", Kind: "client_concern"},
		{Type: AIEventTopics, Topics: []string{"a", "b"}},
		{Type: AIEventFinal, Summary: "s", ActionItems: []string{"x"}},
	} {
		ev := ev
		_ = validateEvent(&ev)
		s.apply(&ev, 1)
		payload := map[string]any{"type": ev.Type}
		switch ev.Type {
		case AIEventTranscript:
			payload["lines"] = generic(s.Transcript)
		case AIEventSuggestion:
			payload["suggestion"] = generic(s.Suggestions[0])
		case AIEventInsight:
			payload["insight"] = generic(s.Insights[0])
		case AIEventTopics:
			payload["topics"] = generic(s.Topics)
		case AIEventFinal:
			payload["final"] = generic(s.Final)
		}
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(payload); err != nil {
			t.Fatalf("%s payload is not gob-encodable: %v", ev.Type, err)
		}
		// and the raw struct would not have been
		var raw bytes.Buffer
		if err := gob.NewEncoder(&raw).Encode(map[string]any{"x": s.Suggestions}); err == nil {
			t.Fatal("expected the raw struct slice to be refused (the bug this guards against)")
		}
	}
}
