package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
)

// Meeting Intelligence endpoints.
//
// Authorization here never trusts a meeting id. A meeting id is a
// reference, not a capability: anyone holding one still has to be a
// current member of the channel that meeting belongs to, checked against
// Mattermost itself at the moment of the request. That check is what
// makes cross-team access impossible -- a channel belongs to exactly one
// team, and channel membership implies team membership.

type summaryRequest struct {
	// Force regenerates a summary that already exists. Without it a
	// repeated request returns the stored one, which is what makes a
	// retry (or an impatient second click) harmless.
	Force bool `json:"force"`
}

// meetingContext resolves a meeting id to its meeting and channel, having
// first proved the caller may see it.
//
// Every failure returns the same 404 as a meeting that does not exist. A
// distinct 403 would confirm to an outsider that a given meeting id is
// real, which is exactly the enumeration oracle the rest of this plugin
// is careful not to offer.
func (p *Plugin) meetingContext(w http.ResponseWriter, r *http.Request, userID string) (*Meeting, *model.Channel, bool) {
	meetingID := mux.Vars(r)["meeting_id"]
	if !model.IsValidId(meetingID) {
		p.notFound(w)
		return nil, nil, false
	}

	meeting, err := p.store.GetMeeting(meetingID)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			p.writeErr(w, http.StatusInternalServerError, "could not load meeting", err)
			return nil, nil, false
		}
		p.notFound(w)
		return nil, nil, false
	}

	// The authorization gate: current membership of the meeting's own
	// channel, asked of Mattermost rather than of our tables.
	if _, err := p.client.Channel.GetMember(meeting.ChannelID, userID); err != nil {
		p.notFound(w)
		return nil, nil, false
	}

	channel, err := p.client.Channel.Get(meeting.ChannelID)
	if err != nil || channel == nil || channel.DeleteAt != 0 {
		p.notFound(w)
		return nil, nil, false
	}
	return meeting, channel, true
}

// handleGenerateSummary starts (or returns) a Meeting Intelligence
// summary for one meeting.
//
// Generation is asynchronous because the Claude call can take minutes; a
// request that blocked that long would time out in the browser and leave
// the row wedged. The handler claims the work, answers 202, and the
// goroutine finishes it. Callers poll the GET endpoint.
func (p *Plugin) handleGenerateSummary(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	meeting, channel, ok := p.meetingContext(w, r, userID)
	if !ok {
		return
	}

	var req summaryRequest
	if r.Body != nil {
		// An absent or malformed body simply means "no options".
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
	}

	// An existing, finished summary is returned as-is unless the caller
	// explicitly asked for a new one.
	existing, err := p.store.GetSummaryByMeeting(meeting.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		p.writeErr(w, http.StatusInternalServerError, "could not read summary", err)
		return
	}
	if existing != nil && !req.Force && existing.Status == SummaryReady {
		writeJSON(w, http.StatusOK, map[string]any{"summary": existing, "reused": true})
		return
	}

	start, end := p.conversationWindow(meeting)
	now := nowMillis()
	claim := &MeetingSummary{
		ID:          model.NewId(),
		MeetingID:   meeting.ID,
		ChannelID:   meeting.ChannelID,
		TeamID:      channel.TeamId,
		RequesterID: userID,
		Status:      SummaryPending,
		WindowStart: start,
		WindowEnd:   end,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	won, err := p.store.ClaimSummaryGeneration(claim, req.Force, now-staleGeneration.Milliseconds())
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not start generation", err)
		return
	}
	if !won {
		// Someone else is already generating this one. Report the row as
		// it stands rather than starting a second run.
		current, gerr := p.store.GetSummaryByMeeting(meeting.ID)
		if gerr != nil {
			p.writeErr(w, http.StatusInternalServerError, "could not read summary", gerr)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"summary": current, "reused": true})
		return
	}

	go p.generateSummary(meeting, channel.TeamId, userID, start, end)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"summary": &MeetingSummary{
			ID: claim.ID, MeetingID: meeting.ID, ChannelID: meeting.ChannelID,
			TeamID: channel.TeamId, RequesterID: userID, Status: SummaryPending,
			WindowStart: start, WindowEnd: end, CreatedAt: now, UpdatedAt: now,
		},
		"reused": false,
	})
}

// handleGetSummary returns the stored summary for a meeting, to any
// current member of its channel.
func (p *Plugin) handleGetSummary(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	meeting, _, ok := p.meetingContext(w, r, userID)
	if !ok {
		return
	}

	summary, err := p.store.GetSummaryByMeeting(meeting.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			p.notFound(w)
			return
		}
		p.writeErr(w, http.StatusInternalServerError, "could not read summary", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": summary, "meeting": meeting})
}

// handleListChannelMeetings lists the meetings in a channel the caller can
// read, so the UI has something to offer a summary for.
func (p *Plugin) handleListChannelMeetings(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	channelID := mux.Vars(r)["channel_id"]
	if !model.IsValidId(channelID) {
		p.notFound(w)
		return
	}
	if _, err := p.client.Channel.GetMember(channelID, userID); err != nil {
		p.notFound(w)
		return
	}

	meetings, err := p.store.ListMeetingsForChannel(channelID, 25)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not list meetings", err)
		return
	}

	// Which of them already have a summary, so the UI can label them
	// without a request per meeting.
	statuses := map[string]string{}
	for _, m := range meetings {
		if s, serr := p.store.GetSummaryByMeeting(m.ID); serr == nil && s != nil {
			statuses[m.ID] = s.Status
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"meetings": meetings, "summary_status": statuses})
}

// --- generation ------------------------------------------------------------

// generateSummary does the work claimed by the handler: read the
// conversation, ask Claude, store the result, notify.
//
// It never returns an error anywhere -- there is no caller left to return
// one to. Every outcome, including failure, is written to the row so the
// UI can show it, because a generation that fails silently would leave a
// spinner turning forever.
func (p *Plugin) generateSummary(meeting *Meeting, teamID, requesterID string, start, end int64) {
	defer func() {
		// A panic in here would otherwise take the whole plugin process
		// down and leave the row pending.
		if rec := recover(); rec != nil {
			p.client.Log.Error("honco: summary generation panicked", "meeting_id", meeting.ID)
			p.finishSummary(meeting, teamID, requesterID, &MeetingSummary{
				Status: SummaryFailed, ErrorMessage: "internal error during generation",
			})
		}
	}()

	msgs, err := p.collectConversation(meeting.ChannelID, start, end)
	if err != nil {
		p.client.Log.Warn("honco: could not read conversation", "meeting_id", meeting.ID, "err", err.Error())
		p.finishSummary(meeting, teamID, requesterID, &MeetingSummary{
			Status: SummaryFailed, ErrorMessage: "could not read the channel conversation",
		})
		return
	}

	// A meeting nobody typed in is a normal outcome, not a failure, and
	// there is nothing to send anywhere.
	if len(msgs) == 0 {
		p.finishSummary(meeting, teamID, requesterID, &MeetingSummary{
			Status:  SummaryEmpty,
			Summary: "No messages were posted in this channel during the meeting, so there is nothing to summarise.",
		})
		return
	}

	out, err := p.summarise(buildTranscript(meeting.Topic, msgs))
	if err != nil {
		// The error text is a transport diagnostic, never the
		// conversation and never a credential.
		p.client.Log.Warn("honco: summariser failed", "meeting_id", meeting.ID, "err", err.Error())
		p.finishSummary(meeting, teamID, requesterID, &MeetingSummary{
			Status:       SummaryFailed,
			ErrorMessage: publicSummarizerError(err),
			MessageCount: len(msgs),
			Participants: participantsOf(msgs),
		})
		return
	}

	sections := parseSections(out)
	result := &MeetingSummary{
		Status:       SummaryReady,
		Summary:      sections[secSummary],
		KeyPoints:    sections[secKeyPoints],
		Decisions:    sections[secDecisions],
		ActionItems:  sections[secActionItems],
		Participants: participantsOf(msgs),
		RawOutput:    out,
		MessageCount: len(msgs),
	}
	// If the model answered in a shape we could not parse at all, keep
	// its text as the summary rather than showing an empty panel.
	if result.Summary == "" && result.KeyPoints == "" && result.Decisions == "" && result.ActionItems == "" {
		result.Summary = out
	}
	p.finishSummary(meeting, teamID, requesterID, result)
}

// publicSummarizerError turns an internal error into something safe and
// useful to show a user. It deliberately does not pass the underlying
// message through: that can contain host names and SSH detail.
func publicSummarizerError(err error) string {
	switch {
	case errors.Is(err, errSummarizerTimeout):
		return "The summarizer did not respond in time. Please try again."
	case errors.Is(err, errSummarizerUnavailable):
		return "The summarizer is not reachable from this server."
	}
	return "Summary generation failed."
}

// finishSummary writes the outcome and notifies. Both steps are best
// effort: a notification that cannot be delivered must not lose the
// summary that was successfully generated.
func (p *Plugin) finishSummary(meeting *Meeting, teamID, requesterID string, result *MeetingSummary) {
	result.MeetingID = meeting.ID
	result.ChannelID = meeting.ChannelID
	result.TeamID = teamID
	result.RequesterID = requesterID
	result.UpdatedAt = nowMillis()

	if err := p.store.FinishSummary(result); err != nil {
		p.client.Log.Error("honco: could not store summary", "meeting_id", meeting.ID, "err", err.Error())
		return
	}
	p.notifyMeetingSummary(meeting, result)
}
