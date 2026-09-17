package main

import (
	"bytes"
	"encoding/gob"
	"errors"
	"io"
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

// The adapter speaks the bot's real API (ayushhonco12/bot). This fake
// answers exactly the paths that bot serves, with its real shapes.
func TestHTTPAIServiceMapsResponses(t *testing.T) {
	var gotAuth, gotUA string
	var created, started, stopped int
	var createBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		switch r.Method + " " + r.URL.Path {
		case "GET /base/health":
			w.WriteHeader(200)
		case "POST /base/api/v1/meetings/":
			created++
			b, _ := io.ReadAll(r.Body)
			createBody = string(b)
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"id":"7b1c-uuid","title":"t","meeting_url":"https://m/x","status":"SCHEDULED","copilot_token":"cptok-1"}`))
		case "GET /base/api/v1/meetings/s-active/sales/suggestions":
			// The bot returns rows oldest-first; the empty one must be dropped.
			_, _ = w.Write([]byte(`[
			  {"id":"sg1","priority":"high","type":"NEXT_BEST_QUESTION","suggestion":"Confirm the Friday deadline","reason":"they asked","status":"delivered"},
			  {"id":"sg2","priority":"low","type":"NUDGE","suggestion":"","status":"delivered"}
			]`))
		case "POST /base/api/v1/meetings/7b1c-uuid/start":
			started++
			_, _ = w.Write([]byte(`{"status":"starting","meeting_id":"7b1c-uuid"}`))
		case "POST /base/api/v1/meetings/7b1c-uuid/stop":
			stopped++
			_, _ = w.Write([]byte(`{"status":"stop_requested"}`))
		case "POST /base/api/v1/meetings/s-409/stop":
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"detail":"Bot is not running (status: COMPLETED)"}`))
		case "POST /base/api/v1/meetings/s-401/stop":
			w.WriteHeader(401)
		case "GET /base/api/v1/meetings/s-active":
			_, _ = w.Write([]byte(`{"id":"s-active","meeting_url":"u","status":"ACTIVE"}`))
		case "GET /base/api/v1/meetings/s-active/health":
			_, _ = w.Write([]byte(`{"status":"ACTIVE","live":true}`))
		case "GET /base/api/v1/meetings/s-done":
			_, _ = w.Write([]byte(`{"id":"s-done","meeting_url":"u","status":"COMPLETED"}`))
		case "GET /base/api/v1/meetings/s-done/health":
			w.WriteHeader(500) // advisory: must not break the status
		case "GET /base/api/v1/meetings/s-done/transcripts":
			// The real bot numbers segments from 0, so segment 0 is the
			// first thing said and must not be dropped by a full pull.
			_, _ = w.Write([]byte(`[
			  {"id":"a","text":"hello there","is_final":true,"sequence":0,"speaker":"alice","created_at":"2026-09-16T10:00:00Z"},
			  {"id":"b","text":"","is_final":true,"sequence":1,"speaker":"bob"},
			  {"id":"c","text":"we ship friday","is_final":false,"sequence":2,"speaker":"bob"}
			]`))
		case "GET /base/api/v1/meetings/s-done/summary":
			_, _ = w.Write([]byte(`{"meeting_id":"s-done","summary":"Short call.",
			  "participants":["alice","bob"],
			  "decisions":["Ship Friday"],
			  "action_items":[{"task":"Write the runbook","owner":"bob","due":"Thursday"},"Ping legal",{"nonsense":1}],
			  "discussion_points":["Timeline"],
			  "unresolved_questions":["Budget?"],
			  "model_used":"openai/gpt-oss-120b"}`))
		case "GET /base/api/v1/meetings/s-500":
			w.WriteHeader(503)
		case "GET /base/api/v1/meetings/s-400/summary":
			w.WriteHeader(400)
		case "GET /base/api/v1/meetings/s-redir":
			http.Redirect(w, r, "http://evil.example/steal", http.StatusFound)
		case "GET /base/api/v1/meetings/s-slow":
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

	// Start = create + start, with the join URL and Stage 1 only.
	res, err := h.StartSession(AIStartRequest{MeetingID: "m1", Topic: "Weekly", MeetingURL: "https://m/x", Participants: []string{"alice"}, Salesman: []string{"Harshini"}})
	if err != nil || res.SessionID != "7b1c-uuid" || res.Status != AIStatusConnecting {
		t.Fatalf("start: %v %+v", err, res)
	}
	// The per-meeting copilot token is captured from the create response.
	if res.CopilotToken != "cptok-1" {
		t.Errorf("copilot token not captured: %q", res.CopilotToken)
	}
	if created != 1 || started != 1 {
		t.Fatalf("expected one create and one start, got %d/%d", created, started)
	}
	// Stage 2 co-pilot is now ON, and the host is named as the person to advise.
	for _, want := range []string{`"meeting_url":"https://m/x"`, `"external_meeting_id":"m1"`, `"title":"Weekly"`, `"sales":{"enabled":true`, `"salesman":["Harshini"]`} {
		if !strings.Contains(createBody, want) {
			t.Errorf("create body missing %s: %s", want, createBody)
		}
	}

	// Live suggestions: rows mapped to Honco suggestions; empty text dropped;
	// no token is a no-op (not an error).
	sugs, serr := h.Suggestions("s-active", "tok")
	if serr != nil || len(sugs) != 1 {
		t.Fatalf("suggestions: %v %+v", serr, sugs)
	}
	if sugs[0].ID != "sg1" || sugs[0].Text != "Confirm the Friday deadline" || sugs[0].Kind != "next_best_question" ||
		sugs[0].Title != "high" || sugs[0].Source != "copilot" || sugs[0].Status != "delivered" {
		t.Errorf("suggestion mapped wrongly: %+v", sugs[0])
	}
	if s2, e2 := h.Suggestions("s-active", ""); e2 != nil || s2 != nil {
		t.Errorf("no token should be a no-op, got %v %+v", e2, s2)
	}
	if _, err := h.StartSession(AIStartRequest{MeetingID: "m2"}); !errors.Is(err, errAIRefused) {
		t.Fatalf("no URL must be refused before any call, got %v", err)
	}

	// Stop: 409 "not running" is the state we want, not an error.
	if err := h.EndSession("7b1c-uuid"); err != nil || stopped != 1 {
		t.Fatalf("stop: %v", err)
	}
	if err := h.EndSession("s-409"); err != nil {
		t.Fatalf("409 on stop must be treated as already stopped, got %v", err)
	}
	if err := h.EndSession("s-401"); !errors.Is(err, errAIAuth) {
		t.Fatalf("401 -> auth, got %v", err)
	}

	// Status maps the bot's enum; health is advisory.
	st, err := h.SessionStatus("s-active")
	if err != nil || st.Status != AIStatusLive || st.CaptureStatus != "in the call" {
		t.Fatalf("active: %v %+v", err, st)
	}
	st, err = h.SessionStatus("s-done")
	if err != nil || st.Status != AIStatusCompleted {
		t.Fatalf("completed, with a failing health route, must still map: %v %+v", err, st)
	}

	// Transcript: empty lines dropped, after/limit applied here. after=-1
	// is the full pull the poller does; it MUST include segment 0.
	lines, err := h.Transcript("s-done", -1, 10)
	if err != nil || len(lines) != 2 {
		t.Fatalf("transcript full pull: %v %+v", err, lines)
	}
	if lines[0].Seq != 0 || lines[0].Speaker != "alice" || !lines[0].Final || lines[0].At == 0 {
		t.Errorf("segment 0 must survive a full pull, mapped wrongly: %+v", lines[0])
	}
	if lines[1].Seq != 2 || lines[1].Final {
		t.Errorf("interim line mapped wrongly: %+v", lines[1])
	}
	// after is an exclusive lower bound: after=0 drops seq 0, keeps seq 2.
	if lines, _ = h.Transcript("s-done", 0, 10); len(lines) != 1 || lines[0].Seq != 2 {
		t.Errorf("after=0 should leave only seq 2: %+v", lines)
	}
	if lines, _ = h.Transcript("s-done", -1, 1); len(lines) != 1 {
		t.Errorf("limit=1 not honoured: %+v", lines)
	}

	// Summary: sections fit AIFinal; object items rendered; nothing invented.
	fin, err := h.Summary("s-done")
	if err != nil {
		t.Fatal(err)
	}
	if fin.Summary != "Short call." || len(fin.Decisions) != 1 || fin.Decisions[0] != "Ship Friday" {
		t.Errorf("summary/decisions: %+v", fin)
	}
	if len(fin.ActionItems) != 2 || fin.ActionItems[0] != "Write the runbook — bob, due Thursday" || fin.ActionItems[1] != "Ping legal" {
		t.Errorf("action items: %+v", fin.ActionItems)
	}
	if len(fin.KeyPoints) != 2 || fin.KeyPoints[0] != "Timeline" || fin.KeyPoints[1] != "Open question: Budget?" {
		t.Errorf("key points should carry open questions: %+v", fin.KeyPoints)
	}
	if len(fin.Participants) != 2 || fin.TranscriptRef != "model:openai/gpt-oss-120b" {
		t.Errorf("participants/model: %+v", fin)
	}

	// Error classes are unchanged.
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

func TestMapBotStatus(t *testing.T) {
	cases := map[string]string{
		"SCHEDULED": AIStatusConnecting, "STARTING": AIStatusConnecting,
		"ACTIVE": AIStatusLive, "active": AIStatusLive,
		"STOPPING": AIStatusEnded, "ANALYZING": AIStatusEnded, "CANCELLED": AIStatusEnded,
		"COMPLETED": AIStatusCompleted, "FAILED": AIStatusFailed,
		"SOMETHING_NEW": "", "": "",
	}
	for in, want := range cases {
		if got := mapBotStatus(in); got != want {
			t.Errorf("mapBotStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

// ANALYZING must not read as completed: the panel shows a summary on
// completed, and there is none yet.
func TestMapBotStatusAnalyzingIsNotCompleted(t *testing.T) {
	if mapBotStatus("ANALYZING") == AIStatusCompleted {
		t.Fatal("ANALYZING mapped to completed")
	}
}

func TestBotItemText(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"  plain  ", "plain"},
		{map[string]any{"task": "Do X", "owner": "bob", "due": "Fri"}, "Do X — bob, due Fri"},
		{map[string]any{"task": "Do X"}, "Do X"},
		{map[string]any{"action": "Do Y", "assignee": "ann"}, "Do Y — ann"},
		{map[string]any{"owner": "bob"}, ""}, // no task: nothing to show, nothing invented
		{map[string]any{}, ""},
		{nil, ""},
		{3.5, "3.5"},
	}
	for _, c := range cases {
		if got := botItemText(c.in); got != c.want {
			t.Errorf("botItemText(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderAIFinalMarkdown(t *testing.T) {
	md := renderAIFinalMarkdown(&AIFinal{
		Summary: "S", KeyPoints: []string{"k"}, Decisions: []string{"d"},
		ActionItems: []string{"a"}, Participants: []string{"p"}, TranscriptRef: "model:m1",
	})
	for _, want := range []string{"## Summary\nS", "## Key discussion points\n- k", "## Decisions\n- d", "## Action items\n- a", "## Participants\n- p", "meeting bot (m1)"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	// parseSections (Meeting Intelligence's own parser) must read it back.
	got := parseSections(md)
	if got[secSummary] != "S" || got[secDecisions] != "- d" || got[secActionItems] != "- a" {
		t.Errorf("Meeting Intelligence cannot parse what we wrote: %+v", got)
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

// A service with its own vocabulary should not have to change it. The
// aliases map onto Honco's semantics; anything unknown is still refused.
func TestEventAliases(t *testing.T) {
	cases := []struct {
		in     aiEvent
		want   string
		status string
	}{
		{aiEvent{Type: "session_started"}, AIEventStatus, AIStatusLive},
		{aiEvent{Type: "session_ended"}, AIEventStatus, AIStatusEnded},
		{aiEvent{Type: "session_status", Status: AIStatusLive}, AIEventStatus, AIStatusLive},
		{aiEvent{Type: "utterance", Text: "hello"}, AIEventTranscript, ""},
		{aiEvent{Type: "transcript_line", Text: "hello"}, AIEventTranscript, ""},
		{aiEvent{Type: "topic", Topics: []string{"Pricing"}}, AIEventTopics, ""},
		{aiEvent{Type: "summary_ready", Summary: "s"}, AIEventFinal, ""},
		{aiEvent{Type: "final_summary", Summary: "s"}, AIEventFinal, ""},
		{aiEvent{Type: "processing_failed"}, AIEventError, ""},
	}
	for i, c := range cases {
		ev := c.in
		if err := validateEvent(&ev); err != nil {
			t.Fatalf("case %d (%s): %v", i, c.in.Type, err)
		}
		if ev.Type != c.want {
			t.Fatalf("case %d: %q became %q, want %q", i, c.in.Type, ev.Type, c.want)
		}
		if c.status != "" && ev.Status != c.status {
			t.Fatalf("case %d: %q implies status %q, got %q", i, c.in.Type, c.status, ev.Status)
		}
	}

	// "partial" is an interim line, so it must not be marked final.
	part := aiEvent{Type: "partial", Text: "half a sen"}
	if err := validateEvent(&part); err != nil {
		t.Fatal(err)
	}
	if part.Lines[0].Final {
		t.Fatal("a partial line is not final")
	}
	// processing_failed gets a class even when the service sent none.
	fail := aiEvent{Type: "processing_failed"}
	_ = validateEvent(&fail)
	if fail.Kind != "processing_failed" {
		t.Fatalf("kind %q", fail.Kind)
	}
	// An unknown name is still an error, not a silent drop.
	unknown := aiEvent{Type: "brainwave", Text: "x"}
	if err := validateEvent(&unknown); err == nil {
		t.Fatal("unknown type accepted")
	}
	// An aliased event still folds into the session correctly.
	sess := &AISession{Status: AIStatusIdle}
	started := aiEvent{Type: "session_started"}
	_ = validateEvent(&started)
	sess.apply(&started, 1)
	line := aiEvent{Type: "utterance", Text: "hello", Speaker: "Client"}
	_ = validateEvent(&line)
	sess.apply(&line, 2)
	done := aiEvent{Type: "summary_ready", Summary: "S"}
	_ = validateEvent(&done)
	sess.apply(&done, 3)
	if sess.Status != AIStatusCompleted || sess.Final == nil || sess.Final.Summary != "S" || len(sess.Transcript) != 1 {
		t.Fatalf("aliased events did not fold correctly: %+v", sess)
	}
}
