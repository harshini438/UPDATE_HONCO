package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
)

// Honco AI Assistant -- the integration boundary.
//
// The AI itself (voice recognition, live transcription, conversation
// analysis, suggestions, post-call summaries) is a separate service owned
// by another team. Nothing in this plugin listens to audio, runs a model
// or writes a sentence of transcript. This file holds what Honco owns:
// the meeting a session belongs to, who may read it, the bounded state the
// panel renders, and the fan-out to browsers over Mattermost's own
// WebSocket -- which is what makes the panel live without a browser ever
// polling or holding a credential for the AI service.
//
// Two directions, one boundary:
//
//   AI service -> Honco   POST /api/v1/ai/events, authenticated with the
//                         AI callback secret (service-to-service, exactly
//                         like the Jibri and meetsvc callbacks). Honco
//                         validates, associates with a meeting, stores a
//                         bounded copy, and broadcasts to the meeting's
//                         channel. Mattermost delivers that broadcast only
//                         to channel members.
//
//   Honco -> AI service   through AIIntegrationService (ai_adapter.go),
//                         with the service token that never leaves the
//                         server. Used to open and close a session around a
//                         Honco meeting, and to fetch the full transcript or
//                         summary when the service is the source of truth.
//
// Browsers talk only to Honco, with their ordinary session, and are
// authorized by channel membership on every read.

// AI session status, as the panel sees it.
const (
	AIStatusNotConfigured = "not_configured" // no callback secret and no service URL set
	AIStatusIdle          = "idle"           // meeting known, no AI session yet
	AIStatusConnecting    = "connecting"     // Honco asked the service to start a session
	AIStatusLive          = "live"           // the service is sending events for this meeting
	AIStatusEnded         = "ended"          // the meeting ended; final outputs may still arrive
	AIStatusCompleted     = "completed"      // final outputs (summary and/or transcript) received
	AIStatusUnavailable   = "unavailable"    // the service could not be reached or refused
	AIStatusFailed        = "failed"         // the service reported a processing failure
)

// Event types the AI service may push. Anything else is rejected with 400
// so a typo on the service side is noticed rather than silently dropped.
const (
	AIEventStatus     = "status"     // service-side session status change
	AIEventTranscript = "transcript" // one or more transcript lines
	AIEventSuggestion = "suggestion" // a live suggestion for the salesperson
	AIEventInsight    = "insight"    // a detected concern, sentiment, opportunity
	AIEventTopics     = "topics"     // the current set of important topics
	AIEventFinal      = "final"      // post-call outputs: summary, action items, transcript reference
	AIEventError      = "error"      // the service could not process this meeting
)

// Bounds. The live view keeps a window, not a history: the service is
// the source of truth for the full transcript, and a panel that re-renders
// a two-hour call on every utterance would not be live for long.
const (
	aiMaxTranscriptLines = 2000  // kept per meeting; older lines are dropped, count retained
	aiLiveWindowLines    = 200   // what a client gets on open; older pages are fetched on demand
	aiMaxSuggestions     = 50    // kept per meeting
	aiMaxInsights        = 50    // kept per meeting
	aiMaxTopics          = 30    // per meeting
	aiMaxLineChars       = 4000  // one transcript line / suggestion / insight
	aiMaxFinalChars      = 60000 // one final-output section
	aiMaxEventBytes      = 512 * 1024
	aiMaxEventsPerPush   = 100
	aiMaxParticipants    = 64
	aiMaxSpeakerChars    = 80
	aiMaxRefChars        = 512
	aiMaxTitleChars      = 200
	aiMaxSessionIDChars  = 128
	aiMaxRecentEventIDs  = 200 // replay window: event ids remembered per meeting
)

// WebSocketEventAI is what the panel subscribes to. Broadcast to the
// meeting's channel, so Mattermost enforces who receives it.
const WebSocketEventAI = "ai_event"

// AITranscriptLine is one utterance.
type AITranscriptLine struct {
	Seq     int64  `json:"seq"`               // monotonic within the meeting, assigned by Honco
	At      int64  `json:"at"`                // ms since epoch, from the service
	Speaker string `json:"speaker,omitempty"` // as labelled by the service ("Client", "Sales", a name)
	Text    string `json:"text"`
	Final   bool   `json:"final"` // false while the recogniser may still revise it
}

// AISuggestion is one live suggestion. Kind is free text from the service
// ("suggestion", "next_best_action", "client_concern"); the panel maps the
// ones it knows to an icon and shows the rest as plain suggestions.
type AISuggestion struct {
	ID     string `json:"id"`
	At     int64  `json:"at"`
	Kind   string `json:"kind,omitempty"`
	Title  string `json:"title,omitempty"`
	Text   string `json:"text"`
	Source string `json:"source,omitempty"` // e.g. the model or rule that produced it
	Status string `json:"status,omitempty"` // e.g. "new", "used", "dismissed" if the service tracks it
}

// AIInsight is a detected concern, sentiment or opportunity.
type AIInsight struct {
	ID    string `json:"id"`
	At    int64  `json:"at"`
	Kind  string `json:"kind,omitempty"`
	Title string `json:"title,omitempty"`
	Text  string `json:"text"`
}

// AIFinal is the post-call output set. Every field is optional: a service
// that produces only a summary sends only a summary.
type AIFinal struct {
	Summary        string   `json:"summary,omitempty"`
	KeyPoints      []string `json:"key_points,omitempty"`
	Decisions      []string `json:"decisions,omitempty"`
	ActionItems    []string `json:"action_items,omitempty"`
	KeyInsights    []string `json:"key_insights,omitempty"`
	ClientInsights []string `json:"client_insights,omitempty"`
	Topics         []string `json:"topics,omitempty"`
	// Where the full transcript lives on the service side, when it is
	// larger than what Honco keeps. Opaque to Honco; never fetched from
	// the browser. The adapter uses it server-side.
	TranscriptRef string `json:"transcript_ref,omitempty"`
	ReceivedAt    int64  `json:"received_at"`
}

// AISession is everything Honco keeps about one meeting's AI session.
// Stored in the plugin KV store under one key per meeting: no new table,
// no schema change, and the meeting row in honco_meetings stays the single
// meeting record. What is stored is a bounded window plus the final
// outputs; the service remains the source of truth for the whole call.
type AISession struct {
	MeetingID string `json:"meeting_id"`
	ChannelID string `json:"channel_id"`
	RoomName  string `json:"room_name"`

	Status            string `json:"status"`
	ExternalSessionID string `json:"external_session_id,omitempty"`
	StartedAt         int64  `json:"started_at,omitempty"`
	EndedAt           int64  `json:"ended_at,omitempty"`
	LastEventAt       int64  `json:"last_event_at,omitempty"`
	EventsReceived    int64  `json:"events_received"`

	// Transcription/recording status as reported by the service, if it
	// reports one ("listening", "paused", "not_recording"...). Free text,
	// bounded, shown as-is.
	CaptureStatus string `json:"capture_status,omitempty"`

	// Last error CLASS, never the raw message from the service. The raw
	// text goes to the server log only.
	ErrorKind string `json:"error_kind,omitempty"`

	NextSeq      int64              `json:"next_seq"`
	LineCount    int64              `json:"line_count"` // including dropped lines
	Transcript   []AITranscriptLine `json:"transcript"`
	Suggestions  []AISuggestion     `json:"suggestions"`
	Insights     []AIInsight        `json:"insights"`
	Topics       []string           `json:"topics"`
	Final        *AIFinal           `json:"final,omitempty"`
	RecentEvents []string           `json:"recent_event_ids,omitempty"`
	UpdatedAt    int64              `json:"updated_at"`
}

func aiSessionKey(meetingID string) string { return "ai:session:" + meetingID }

// aiEvent is one pushed event, after decoding and before validation.
type aiEvent struct {
	ID        string `json:"id,omitempty"` // optional; used to drop replays
	Type      string `json:"type"`
	At        int64  `json:"at,omitempty"`         // ms since epoch
	SessionID string `json:"session_id,omitempty"` // the service's own id for this session

	// status
	Status        string `json:"status,omitempty"`
	CaptureStatus string `json:"capture_status,omitempty"`

	// transcript: either one line or a batch
	Lines   []AITranscriptLine `json:"lines,omitempty"`
	Speaker string             `json:"speaker,omitempty"`
	Text    string             `json:"text,omitempty"`
	Final   *bool              `json:"final,omitempty"`

	// suggestion / insight
	Kind   string `json:"kind,omitempty"`
	Title  string `json:"title,omitempty"`
	Source string `json:"source,omitempty"`

	// topics
	Topics []string `json:"topics,omitempty"`

	// final
	Summary        string   `json:"summary,omitempty"`
	KeyPoints      []string `json:"key_points,omitempty"`
	Decisions      []string `json:"decisions,omitempty"`
	ActionItems    []string `json:"action_items,omitempty"`
	KeyInsights    []string `json:"key_insights,omitempty"`
	ClientInsights []string `json:"client_insights,omitempty"`
	TranscriptRef  string   `json:"transcript_ref,omitempty"`

	// error
	Message string `json:"message,omitempty"`
}

// aiPush is the request body of POST /api/v1/ai/events: which meeting,
// and one or more events for it.
type aiPush struct {
	MeetingID string    `json:"meeting_id,omitempty"`
	RoomName  string    `json:"room_name,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Events    []aiEvent `json:"events"`
}

var errAIBadEvent = errors.New("invalid ai event")

// aiEventAliases lets a service keep its own vocabulary. Honco has one set
// of semantics; these are the other names the same event is likely to have
// on the producing side. Anything not listed here and not a known type is
// still rejected, so a typo is still caught rather than silently dropped.
var aiEventAliases = map[string]string{
	"session_started":   AIEventStatus,
	"session_ended":     AIEventStatus,
	"session_status":    AIEventStatus,
	"partial":           AIEventTranscript,
	"transcript_line":   AIEventTranscript,
	"utterance":         AIEventTranscript,
	"topic":             AIEventTopics,
	"summary":           AIEventFinal,
	"summary_ready":     AIEventFinal,
	"final_summary":     AIEventFinal,
	"processing_failed": AIEventError,
	"failed":            AIEventError,
}

// normaliseEvent rewrites an aliased type onto Honco's own, filling in what
// the alias implies (session_started means status=live, session_ended means
// status=ended) so the rest of the pipeline sees one vocabulary.
func normaliseEvent(ev *aiEvent) {
	canon, ok := aiEventAliases[ev.Type]
	if !ok {
		return
	}
	switch ev.Type {
	case "session_started":
		if ev.Status == "" {
			ev.Status = AIStatusLive
		}
	case "session_ended":
		if ev.Status == "" {
			ev.Status = AIStatusEnded
		}
	case "summary_ready", "summary", "final_summary":
		// nothing extra: the payload fields are the same
	case "processing_failed", "failed":
		if ev.Kind == "" {
			ev.Kind = "processing_failed"
		}
	case "partial":
		if ev.Final == nil {
			no := false
			ev.Final = &no
		}
	}
	ev.Type = canon
}

// validateEvent checks one event and bounds every string. Returns a
// message safe to send back to the service (it names the field, never
// echoes content).
func validateEvent(ev *aiEvent) error {
	normaliseEvent(ev)
	switch ev.Type {
	case AIEventStatus:
		if ev.Status == "" && ev.CaptureStatus == "" {
			return fmt.Errorf("%w: status event needs status or capture_status", errAIBadEvent)
		}
		switch ev.Status {
		case "", AIStatusConnecting, AIStatusLive, AIStatusEnded, AIStatusCompleted, AIStatusUnavailable, AIStatusFailed:
		default:
			return fmt.Errorf("%w: unknown status", errAIBadEvent)
		}
		ev.CaptureStatus = clip(ev.CaptureStatus, aiMaxSpeakerChars)
	case AIEventTranscript:
		if len(ev.Lines) == 0 {
			if strings.TrimSpace(ev.Text) == "" {
				return fmt.Errorf("%w: transcript event needs text or lines", errAIBadEvent)
			}
			final := true
			if ev.Final != nil {
				final = *ev.Final
			}
			ev.Lines = []AITranscriptLine{{At: ev.At, Speaker: ev.Speaker, Text: ev.Text, Final: final}}
		}
		if len(ev.Lines) > aiMaxEventsPerPush {
			return fmt.Errorf("%w: too many lines in one event", errAIBadEvent)
		}
		for i := range ev.Lines {
			l := &ev.Lines[i]
			if strings.TrimSpace(l.Text) == "" {
				return fmt.Errorf("%w: empty transcript line", errAIBadEvent)
			}
			l.Text = clip(l.Text, aiMaxLineChars)
			l.Speaker = clip(l.Speaker, aiMaxSpeakerChars)
			if l.At == 0 {
				l.At = ev.At
			}
		}
	case AIEventSuggestion, AIEventInsight:
		if strings.TrimSpace(ev.Text) == "" {
			return fmt.Errorf("%w: %s event needs text", errAIBadEvent, ev.Type)
		}
		ev.Text = clip(ev.Text, aiMaxLineChars)
		ev.Title = clip(ev.Title, aiMaxTitleChars)
		ev.Kind = clip(ev.Kind, aiMaxSpeakerChars)
		ev.Source = clip(ev.Source, aiMaxSpeakerChars)
		ev.Status = clip(ev.Status, aiMaxSpeakerChars)
	case AIEventTopics:
		if len(ev.Topics) > aiMaxTopics {
			ev.Topics = ev.Topics[:aiMaxTopics]
		}
		for i := range ev.Topics {
			ev.Topics[i] = clip(ev.Topics[i], aiMaxTitleChars)
		}
	case AIEventFinal:
		ev.Summary = clip(ev.Summary, aiMaxFinalChars)
		ev.KeyPoints = clipList(ev.KeyPoints)
		ev.Decisions = clipList(ev.Decisions)
		ev.ActionItems = clipList(ev.ActionItems)
		ev.KeyInsights = clipList(ev.KeyInsights)
		ev.ClientInsights = clipList(ev.ClientInsights)
		ev.Topics = clipList(ev.Topics)
		ev.TranscriptRef = clip(ev.TranscriptRef, aiMaxRefChars)
		if ev.Summary == "" && len(ev.KeyPoints) == 0 && len(ev.ActionItems) == 0 &&
			len(ev.Decisions) == 0 && len(ev.KeyInsights) == 0 && len(ev.ClientInsights) == 0 &&
			ev.TranscriptRef == "" {
			return fmt.Errorf("%w: final event carries nothing", errAIBadEvent)
		}
	case AIEventError:
		ev.Kind = clip(ev.Kind, aiMaxSpeakerChars)
		// ev.Message is logged server-side only, never stored or shown.
	default:
		return fmt.Errorf("%w: unknown event type", errAIBadEvent)
	}
	ev.ID = clip(ev.ID, aiMaxSessionIDChars)
	ev.SessionID = clip(ev.SessionID, aiMaxSessionIDChars)
	return nil
}

func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}

func clipList(in []string) []string {
	if len(in) > aiMaxSuggestions {
		in = in[:aiMaxSuggestions]
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if c := clip(s, aiMaxLineChars); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// apply folds one validated event into the session. Pure: no I/O, so it
// is unit-testable and the same code runs whether the event came from the
// service or from a lifecycle hook. Returns false when the event was a
// replay and changed nothing.
func (s *AISession) apply(ev *aiEvent, now int64) bool {
	if ev.ID != "" {
		for _, seen := range s.RecentEvents {
			if seen == ev.ID {
				return false
			}
		}
		s.RecentEvents = append(s.RecentEvents, ev.ID)
		if len(s.RecentEvents) > aiMaxRecentEventIDs {
			s.RecentEvents = s.RecentEvents[len(s.RecentEvents)-aiMaxRecentEventIDs:]
		}
	}
	if ev.SessionID != "" && s.ExternalSessionID == "" {
		s.ExternalSessionID = ev.SessionID
	}
	at := ev.At
	if at == 0 {
		at = now
	}
	s.EventsReceived++
	s.LastEventAt = now
	s.UpdatedAt = now

	// Any content event proves the service is talking to us.
	promoteLive := func() {
		switch s.Status {
		case AIStatusNotConfigured, AIStatusIdle, AIStatusConnecting, AIStatusUnavailable:
			s.Status = AIStatusLive
			if s.StartedAt == 0 {
				s.StartedAt = now
			}
		}
	}

	switch ev.Type {
	case AIEventStatus:
		if ev.CaptureStatus != "" {
			s.CaptureStatus = ev.CaptureStatus
		}
		if ev.Status != "" {
			s.Status = ev.Status
			if ev.Status == AIStatusLive && s.StartedAt == 0 {
				s.StartedAt = now
			}
			if (ev.Status == AIStatusEnded || ev.Status == AIStatusCompleted) && s.EndedAt == 0 {
				s.EndedAt = now
			}
			if ev.Status != AIStatusFailed && ev.Status != AIStatusUnavailable {
				s.ErrorKind = ""
			}
		}
	case AIEventTranscript:
		promoteLive()
		for _, l := range ev.Lines {
			l.Seq = s.NextSeq
			s.NextSeq++
			if l.At == 0 {
				l.At = at
			}
			s.Transcript = append(s.Transcript, l)
			s.LineCount++
		}
		if len(s.Transcript) > aiMaxTranscriptLines {
			s.Transcript = s.Transcript[len(s.Transcript)-aiMaxTranscriptLines:]
		}
	case AIEventSuggestion:
		promoteLive()
		id := ev.ID
		if id == "" {
			id = model.NewId()
		}
		s.Suggestions = append(s.Suggestions, AISuggestion{
			ID: id, At: at, Kind: ev.Kind, Title: ev.Title, Text: ev.Text, Source: ev.Source, Status: ev.Status,
		})
		if len(s.Suggestions) > aiMaxSuggestions {
			s.Suggestions = s.Suggestions[len(s.Suggestions)-aiMaxSuggestions:]
		}
	case AIEventInsight:
		promoteLive()
		id := ev.ID
		if id == "" {
			id = model.NewId()
		}
		s.Insights = append(s.Insights, AIInsight{ID: id, At: at, Kind: ev.Kind, Title: ev.Title, Text: ev.Text})
		if len(s.Insights) > aiMaxInsights {
			s.Insights = s.Insights[len(s.Insights)-aiMaxInsights:]
		}
	case AIEventTopics:
		promoteLive()
		s.Topics = dedupeStrings(ev.Topics)
	case AIEventFinal:
		f := s.Final
		if f == nil {
			f = &AIFinal{}
		}
		if ev.Summary != "" {
			f.Summary = ev.Summary
		}
		if len(ev.KeyPoints) > 0 {
			f.KeyPoints = ev.KeyPoints
		}
		if len(ev.Decisions) > 0 {
			f.Decisions = ev.Decisions
		}
		if len(ev.ActionItems) > 0 {
			f.ActionItems = ev.ActionItems
		}
		if len(ev.KeyInsights) > 0 {
			f.KeyInsights = ev.KeyInsights
		}
		if len(ev.ClientInsights) > 0 {
			f.ClientInsights = ev.ClientInsights
		}
		if len(ev.Topics) > 0 {
			f.Topics = ev.Topics
			s.Topics = dedupeStrings(ev.Topics)
		}
		if ev.TranscriptRef != "" {
			f.TranscriptRef = ev.TranscriptRef
		}
		f.ReceivedAt = now
		s.Final = f
		s.Status = AIStatusCompleted
		s.ErrorKind = ""
		if s.EndedAt == 0 {
			s.EndedAt = now
		}
	case AIEventError:
		s.Status = AIStatusFailed
		s.ErrorKind = ev.Kind
		if s.ErrorKind == "" {
			s.ErrorKind = "processing_failed"
		}
	}
	return true
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}
	return out
}

// view is what a browser gets: the session without the replay window, and
// only the live transcript window unless asked for more.
type aiSessionView struct {
	*AISession
	RecentEvents  []string           `json:"recent_event_ids,omitempty"`
	Transcript    []AITranscriptLine `json:"transcript"`
	TranscriptGap bool               `json:"transcript_gap"` // true when older lines exist beyond what Honco kept
}

func (s *AISession) view() aiSessionView {
	lines := s.Transcript
	if len(lines) > aiLiveWindowLines {
		lines = lines[len(lines)-aiLiveWindowLines:]
	}
	return aiSessionView{
		AISession:     s,
		RecentEvents:  nil,
		Transcript:    lines,
		TranscriptGap: s.LineCount > int64(len(s.Transcript)),
	}
}

// transcriptBefore returns up to limit lines with Seq < before, oldest
// first, from what Honco kept. The page is a slice of the bounded window;
// anything older than the window is only on the service.
func (s *AISession) transcriptBefore(before int64, limit int) []AITranscriptLine {
	if limit <= 0 || limit > aiLiveWindowLines {
		limit = aiLiveWindowLines
	}
	idx := sort.Search(len(s.Transcript), func(i int) bool { return s.Transcript[i].Seq >= before })
	start := idx - limit
	if start < 0 {
		start = 0
	}
	out := make([]AITranscriptLine, idx-start)
	copy(out, s.Transcript[start:idx])
	return out
}

// --- storage ---------------------------------------------------------------

// aiSessions serialises read-modify-write per meeting within this node;
// the KV store's atomic set handles the cross-node case.
type aiSessions struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (a *aiSessions) lock(meetingID string) *sync.Mutex {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.locks == nil {
		a.locks = map[string]*sync.Mutex{}
	}
	m, ok := a.locks[meetingID]
	if !ok {
		m = &sync.Mutex{}
		a.locks[meetingID] = m
	}
	return m
}

// loadAISession reads a meeting's session, or a fresh idle one. Never an
// error the caller has to think about: a missing key is the normal state
// for a meeting the AI has not touched.
func (p *Plugin) loadAISession(m *Meeting) *AISession {
	var s AISession
	if err := p.client.KV.Get(aiSessionKey(m.ID), &s); err != nil {
		p.client.Log.Warn("honco ai: could not read session", "meeting_id", m.ID, "err", err.Error())
	}
	if s.MeetingID == "" {
		s = AISession{
			MeetingID:   m.ID,
			ChannelID:   m.ChannelID,
			RoomName:    m.RoomName,
			Status:      AIStatusIdle,
			Transcript:  []AITranscriptLine{},
			Suggestions: []AISuggestion{},
			Insights:    []AIInsight{},
			Topics:      []string{},
		}
		if !p.aiConfigured() {
			s.Status = AIStatusNotConfigured
		}
		if m.Status == MeetingEnded {
			s.Status = AIStatusEnded
			s.EndedAt = m.EndedAt
		}
	}
	if s.Transcript == nil {
		s.Transcript = []AITranscriptLine{}
	}
	if s.Suggestions == nil {
		s.Suggestions = []AISuggestion{}
	}
	if s.Insights == nil {
		s.Insights = []AIInsight{}
	}
	if s.Topics == nil {
		s.Topics = []string{}
	}
	return &s
}

// updateAISession applies fn under the meeting's lock and persists.
func (p *Plugin) updateAISession(m *Meeting, fn func(s *AISession) bool) (*AISession, bool, error) {
	l := p.ai.lock(m.ID)
	l.Lock()
	defer l.Unlock()
	s := p.loadAISession(m)
	if !fn(s) {
		return s, false, nil
	}
	s.UpdatedAt = nowMillis()
	if _, err := p.client.KV.Set(aiSessionKey(m.ID), s); err != nil {
		return s, false, err
	}
	return s, true, nil
}

// generic converts a value to the plain map/slice/float shape JSON gives
// it. The WebSocket payload crosses the plugin RPC boundary gob-encoded,
// and gob refuses (and, worse, wedges the stream on) a struct type it has
// not been told about; map[string]any and []any it knows.
func generic(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

// broadcastAI tells the meeting's channel something changed. The payload
// carries the delta (what the panel needs to update in place) plus the
// session status; a client that missed events re-reads the session.
func (p *Plugin) broadcastAI(m *Meeting, s *AISession, ev *aiEvent) {
	payload := map[string]any{
		"meeting_id":      m.ID,
		"channel_id":      m.ChannelID,
		"status":          s.Status,
		"capture_status":  s.CaptureStatus,
		"error_kind":      s.ErrorKind,
		"line_count":      s.LineCount,
		"events_received": s.EventsReceived,
	}
	if ev != nil {
		payload["type"] = ev.Type
		switch ev.Type {
		case AIEventTranscript:
			n := len(ev.Lines)
			if n > len(s.Transcript) {
				n = len(s.Transcript)
			}
			payload["lines"] = generic(s.Transcript[len(s.Transcript)-n:])
		case AIEventSuggestion:
			if len(s.Suggestions) > 0 {
				payload["suggestion"] = generic(s.Suggestions[len(s.Suggestions)-1])
			}
		case AIEventInsight:
			if len(s.Insights) > 0 {
				payload["insight"] = generic(s.Insights[len(s.Insights)-1])
			}
		case AIEventTopics:
			payload["topics"] = generic(s.Topics)
		case AIEventFinal:
			payload["final"] = generic(s.Final)
		}
	} else {
		payload["type"] = AIEventStatus
	}
	p.client.Frontend.PublishWebSocketEvent(WebSocketEventAI, payload,
		&model.WebsocketBroadcast{ChannelId: m.ChannelID})
}

// --- meeting lifecycle hooks -----------------------------------------------

// aiMeetingStarted runs when a meeting becomes active. If the service is
// configured, Honco asks it to open a session; the outcome is written to
// the session and broadcast, so the panel shows "connecting" and then
// either "live" or "unavailable" without guessing.
func (p *Plugin) aiMeetingStarted(m *Meeting) {
	if !p.aiConfigured() {
		return
	}
	// Once. A session that failed stays failed until a person presses
	// Retry; every later join must not re-dial the service.
	s := p.loadAISession(m)
	if s.Status != AIStatusIdle && s.Status != AIStatusNotConfigured {
		return
	}
	go p.aiStartSession(m, "")
}

// aiMeetingEnded runs when a meeting ends. The session moves to `ended`
// unless final outputs already arrived; the service is told, best effort,
// so it can finish and push its post-call outputs.
func (p *Plugin) aiMeetingEnded(m *Meeting) {
	s, changed, err := p.updateAISession(m, func(s *AISession) bool {
		if s.Status == AIStatusCompleted || s.Status == AIStatusEnded {
			return false
		}
		if s.Status == AIStatusNotConfigured || s.Status == AIStatusIdle {
			s.Status = AIStatusEnded
			s.EndedAt = m.EndedAt
			return true
		}
		s.Status = AIStatusEnded
		s.EndedAt = m.EndedAt
		return true
	})
	if err != nil {
		p.client.Log.Warn("honco ai: could not record meeting end", "meeting_id", m.ID, "err", err.Error())
		return
	}
	if changed {
		p.broadcastAI(m, s, nil)
	}
	if s.ExternalSessionID != "" && p.aiConfigured() {
		go func() {
			if err := p.aiService().EndSession(s.ExternalSessionID); err != nil {
				p.client.Log.Warn("honco ai: end session", "meeting_id", m.ID, "err", err.Error())
			}
		}()
	}
}

// aiStartSession asks the service to attach to a meeting. requesterID is
// set when a person pressed Retry, empty for the automatic start.
func (p *Plugin) aiStartSession(m *Meeting, requesterID string) {
	s, _, err := p.updateAISession(m, func(s *AISession) bool {
		if s.Status == AIStatusLive || s.Status == AIStatusCompleted {
			return false
		}
		s.Status = AIStatusConnecting
		s.ErrorKind = ""
		return true
	})
	if err != nil {
		p.client.Log.Warn("honco ai: could not mark connecting", "meeting_id", m.ID, "err", err.Error())
		return
	}
	if s.Status != AIStatusConnecting {
		return
	}
	p.broadcastAI(m, s, nil)

	participants := []string{}
	if present, perr := p.store.ListPresentParticipants(m.ID); perr == nil {
		for i, pt := range present {
			if i >= aiMaxParticipants {
				break
			}
			participants = append(participants, pt.DisplayName)
		}
	}
	res, err := p.aiService().StartSession(AIStartRequest{
		MeetingID:    m.ID,
		RoomName:     m.RoomName,
		ChannelID:    m.ChannelID,
		Topic:        m.Topic,
		StartedAt:    m.StartedAt,
		Participants: participants,
	})
	s, _, uerr := p.updateAISession(m, func(s *AISession) bool {
		if err != nil {
			s.Status = AIStatusUnavailable
			s.ErrorKind = aiErrorKind(err)
			return true
		}
		if res.SessionID != "" {
			s.ExternalSessionID = res.SessionID
		}
		// The service decides when it is live (it sends a status event);
		// until then the panel keeps showing "connecting".
		if res.Status == AIStatusLive {
			s.Status = AIStatusLive
			if s.StartedAt == 0 {
				s.StartedAt = nowMillis()
			}
		}
		return true
	})
	if uerr != nil {
		p.client.Log.Warn("honco ai: could not record session start", "meeting_id", m.ID, "err", uerr.Error())
		return
	}
	if err != nil {
		p.client.Log.Warn("honco ai: start session", "meeting_id", m.ID, "kind", s.ErrorKind, "err", err.Error())
	}
	p.broadcastAI(m, s, nil)
}

// aiConfigured: is there any way for the service to reach us, or us to
// reach it? Either half is enough for the panel to show something real.
func (p *Plugin) aiConfigured() bool {
	c := p.config()
	return c.AICallbackSecret != "" || c.AIServiceURL != ""
}
