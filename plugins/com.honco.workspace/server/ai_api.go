package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// AI Assistant endpoints.
//
// One service route and four user routes. The service route is the only
// way AI content enters Honco; the user routes are the only way it leaves,
// and each of them re-proves channel membership through the same
// meetingContext gate Meeting Intelligence uses -- a meeting id is a
// reference, never a capability, and a guessed id from another team is a
// 404 indistinguishable from a meeting that does not exist.

// handleAIEvents is where the AI service pushes what it produced.
//
// Authenticated with the AI callback secret in constant time, exactly like
// the Jibri and meetsvc callbacks. The body is bounded, every event is
// validated and clipped, the meeting must already exist (the service
// cannot create one), and the result is stored and broadcast to the
// meeting's channel. Nothing in the body is trusted for authorization: the
// meeting's own channel decides who sees it.
func (p *Plugin) handleAIEvents(w http.ResponseWriter, r *http.Request) {
	cfg := p.config()
	if !checkSecret(r.Header.Get("X-Honco-AI-Secret"), cfg.AICallbackSecret) {
		p.client.Log.Warn("honco ai: event rejected (bad or missing callback secret)")
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: "unauthorized"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, aiMaxEventBytes)
	var push aiPush
	if err := json.NewDecoder(r.Body).Decode(&push); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			p.writeErr(w, http.StatusRequestEntityTooLarge, "payload too large", nil)
			return
		}
		p.writeErr(w, http.StatusBadRequest, "invalid JSON", nil)
		return
	}
	if len(push.Events) == 0 {
		p.writeErr(w, http.StatusBadRequest, "events is required", nil)
		return
	}
	if len(push.Events) > aiMaxEventsPerPush {
		p.writeErr(w, http.StatusBadRequest, "too many events in one push", nil)
		return
	}

	// Which meeting. By Honco id when the service has it (it was in the
	// StartSession request), by Jitsi room otherwise.
	var m *Meeting
	var err error
	switch {
	case push.MeetingID != "":
		if !validID(push.MeetingID) {
			p.writeErr(w, http.StatusBadRequest, "invalid meeting_id", nil)
			return
		}
		m, err = p.store.GetMeeting(push.MeetingID)
	case push.RoomName != "":
		push.RoomName = strings.TrimSpace(push.RoomName)
		if !roomNameOK(push.RoomName) {
			p.writeErr(w, http.StatusBadRequest, "invalid room_name", nil)
			return
		}
		m, err = p.store.GetMeetingByRoom(push.RoomName)
	default:
		p.writeErr(w, http.StatusBadRequest, "meeting_id or room_name is required", nil)
		return
	}
	if err != nil || m == nil {
		if err != nil && !errors.Is(err, ErrNotFound) {
			p.writeErr(w, http.StatusInternalServerError, "could not load meeting", err)
			return
		}
		p.writeErr(w, http.StatusNotFound, "unknown meeting", nil)
		return
	}

	for i := range push.Events {
		if push.Events[i].SessionID == "" {
			push.Events[i].SessionID = push.SessionID
		}
		if verr := validateEvent(&push.Events[i]); verr != nil {
			p.writeErr(w, http.StatusBadRequest, verr.Error(), nil)
			return
		}
	}

	now := nowMillis()
	accepted, replayed := 0, 0
	var last *aiEvent
	var session *AISession
	for i := range push.Events {
		ev := &push.Events[i]
		if ev.Type == AIEventError {
			// The raw message is for the operator, not the user.
			p.client.Log.Warn("honco ai: service reported an error", "meeting_id", m.ID, "kind", ev.Kind, "message", clip(ev.Message, 500))
		}
		s, changed, uerr := p.updateAISession(m, func(s *AISession) bool { return s.apply(ev, now) })
		if uerr != nil {
			p.writeErr(w, http.StatusInternalServerError, "could not store event", uerr)
			return
		}
		session = s
		if !changed {
			replayed++
			continue
		}
		accepted++
		last = ev
		p.broadcastAI(m, s, ev)
	}

	if last != nil && last.Type == AIEventFinal {
		p.notifyAIFinal(m, session)
	}
	if last != nil && last.Type == AIEventError {
		p.notifyAIFailed(m, session)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"meeting_id": m.ID,
		"accepted":   accepted,
		"replayed":   replayed,
		"status":     session.Status,
	})
}

// handleAIStatus tells the panel whether the feature is wired up at all,
// so it can say "not configured" instead of "connecting" forever. Booleans
// only: no URL, no secret.
func (p *Plugin) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := p.requireUser(w, r); !ok {
		return
	}
	c := p.config()
	writeJSON(w, http.StatusOK, map[string]any{
		"configured":          c.AICallbackSecret != "" || c.AIServiceURL != "",
		"callback_configured": c.AICallbackSecret != "",
		"service_configured":  c.AIServiceURL != "",
	})
}

// handleGetAISession is what the panel reads on open and after a
// reconnect: the meeting, who is in it, and the session's live window.
func (p *Plugin) handleGetAISession(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	m, _, ok := p.meetingContext(w, r, userID)
	if !ok {
		return
	}
	s := p.loadAISession(m)

	names := []string{}
	if present, err := p.store.ListPresentParticipants(m.ID); err == nil {
		for _, pt := range present {
			names = append(names, pt.DisplayName)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"meeting":      m,
		"participants": names,
		"session":      s.view(),
	})
}

// handleGetAITranscript pages backwards through what Honco kept.
func (p *Plugin) handleGetAITranscript(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	m, _, ok := p.meetingContext(w, r, userID)
	if !ok {
		return
	}
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	s := p.loadAISession(m)
	if before <= 0 {
		before = s.NextSeq
	}
	lines := s.transcriptBefore(before, limit)
	firstKept := int64(0)
	if len(s.Transcript) > 0 {
		firstKept = s.Transcript[0].Seq
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lines":    lines,
		"has_more": len(lines) > 0 && lines[0].Seq > firstKept,
		"older_on_service": s.LineCount > int64(len(s.Transcript)) &&
			(len(lines) == 0 || lines[0].Seq <= firstKept),
		"line_count": s.LineCount,
	})
}

// handleStartAISession is Retry / Start from the panel: ask the service to
// attach to this meeting. Membership is proved first; the service is
// asked in the background so the request returns at once with the
// `connecting` state, and the outcome arrives over the WebSocket.
func (p *Plugin) handleStartAISession(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	m, _, ok := p.meetingContext(w, r, userID)
	if !ok {
		return
	}
	// Starting is an OUTBOUND call, so it needs the service URL; a
	// deployment where the service only pushes to us has nothing to
	// start and says so.
	if _, err := aiBaseURL(p.config().AIServiceURL); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "AI service is not configured",
			"session": p.loadAISession(m).view(),
		})
		return
	}
	if m.Status == MeetingEnded {
		p.writeErr(w, http.StatusConflict, "the meeting has ended", nil)
		return
	}
	if cur := p.loadAISession(m); cur.Status == AIStatusLive || cur.Status == AIStatusCompleted {
		writeJSON(w, http.StatusOK, map[string]any{"session": cur.view()})
		return
	}
	go p.aiStartSession(m, userID)
	// Give the synchronous part (status -> connecting) a moment so the
	// response already reflects it; the broadcast covers the rest.
	time.Sleep(50 * time.Millisecond)
	writeJSON(w, http.StatusAccepted, map[string]any{"session": p.loadAISession(m).view()})
}

// handleEndAISession lets a participant detach the assistant before the
// meeting ends. Honco's state moves to `ended`; the service is told.
func (p *Plugin) handleEndAISession(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	m, _, ok := p.meetingContext(w, r, userID)
	if !ok {
		return
	}
	if m.EndedAt == 0 {
		m.EndedAt = nowMillis()
	}
	p.aiMeetingEnded(m)
	writeJSON(w, http.StatusOK, map[string]any{"session": p.loadAISession(m).view()})
}

// --- notifications ---------------------------------------------------------

// Two more kinds through the existing ledger and bot DM. Recipient: the
// meeting's creator (the person who ran /meet -- for a sales call, the
// salesperson). The message names the meeting and links the card; none of
// the AI content travels in the DM.
const (
	KindAISummaryReady = "ai_summary_ready"
	KindAIFailed       = "ai_processing_failed"
)

func (p *Plugin) notifyAIFinal(m *Meeting, s *AISession) {
	if m.CreatorID == "" || s == nil || s.Final == nil {
		return
	}
	title := m.Topic
	if title == "" {
		title = m.RoomName
	}
	msg := "AI summary ready for **" + title + "**."
	if link := p.permalink(m.ChannelID, m.PostID); link != "" {
		msg += " [Open the meeting](" + link + ") and press **AI Assistant** on its card."
	} else {
		msg += " Open the channel and press **AI Assistant** on the meeting's card to read it."
	}
	key := KindAISummaryReady + ":" + m.ID + ":" + strconv.FormatInt(s.Final.ReceivedAt, 10)
	p.dmOnce(KindAISummaryReady, m.ID, m.CreatorID, key, msg)
}

func (p *Plugin) notifyAIFailed(m *Meeting, s *AISession) {
	if m.CreatorID == "" || s == nil {
		return
	}
	title := m.Topic
	if title == "" {
		title = m.RoomName
	}
	key := KindAIFailed + ":" + m.ID + ":" + strconv.FormatInt(s.LastEventAt, 10)
	p.dmOnce(KindAIFailed, m.ID, m.CreatorID, key,
		"The AI assistant could not process **"+title+"**. The transcript and summary may be unavailable.")
}
