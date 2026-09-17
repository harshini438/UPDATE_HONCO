package main

import (
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Pulling from the bot.
//
// AI_INTEGRATION.md was written for a service that pushes events to
// /api/v1/ai/events. The bot that actually exists (ayushhonco12/bot) does
// not push anything: it records, analyses, and then serves its results
// over GET. So after StartSession, Honco polls -- status while the call
// runs, then the transcript and summary once the bot reports COMPLETED.
//
// What it fetches is turned into the same aiEvents the push endpoint
// accepts, validated by the same validateEvent, and fed through the same
// aiIngest. The panel, the WebSocket broadcast, the notifications and the
// Meeting Intelligence summary row therefore behave identically whether
// the content was pushed or pulled; nothing downstream knows the
// difference. This file adds no table and no field to the data model.
//
// Live suggestions are NOT implemented here. The bot's co-pilot is
// disabled per meeting at StartSession (sales.enabled=false), and this
// poller reads only status, transcript and summary.

const (
	// The poll cadence, matching the participant poller: fast enough that
	// "Connected" appears within seconds of the bot joining, slow enough
	// that a long call is a few hundred cheap GETs.
	aiPollInterval = 10 * time.Second

	// How long a poller lives. A meeting is bounded at maxMeetingWindow;
	// the bot then needs time to transcribe and analyse, so it gets the
	// window plus a margin. A poller that outlives this logs and stops --
	// it does not mark anything failed, because the bot may simply be
	// slow, and the next Retry starts a fresh one.
	aiPollMaxAge = maxMeetingWindow + 30*time.Minute
)

// aiPollBot follows one bot session to its end. meetingID and sessionID
// are captured at start so a Retry, which replaces the session id, makes
// the previous poller notice and exit rather than write stale results.
func (p *Plugin) aiPollBot(meetingID, sessionID string) {
	defer func() {
		if rec := recover(); rec != nil {
			p.client.Log.Error("honco ai: poller panicked", "meeting_id", meetingID)
		}
	}()

	deadline := time.Now().Add(aiPollMaxAge)
	lastStatus := ""
	lastErr := ""

	for time.Now().Before(deadline) {
		select {
		case <-p.stop:
			return
		case <-time.After(aiPollInterval):
		}

		m, err := p.store.GetMeeting(meetingID)
		if err != nil || m == nil {
			return
		}
		cur := p.loadAISession(m)
		if cur.ExternalSessionID != sessionID {
			// A retry started a new session; that one has its own poller.
			return
		}
		if cur.Status == AIStatusCompleted || cur.Status == AIStatusFailed {
			return
		}

		st, serr := p.aiService().SessionStatus(sessionID)
		if serr != nil {
			// Transient: the bot restarted, or the network blinked. Log a
			// change of error, not every tick, and keep polling; the
			// session stays at its last known status rather than flapping
			// to unavailable on one missed poll.
			if kind := aiErrorKind(serr); kind != lastErr {
				lastErr = kind
				p.client.Log.Warn("honco ai: poll", "meeting_id", meetingID, "kind", kind, "err", redactErr(serr))
			}
			continue
		}
		lastErr = ""

		if st.Status != "" && st.Status != lastStatus {
			lastStatus = st.Status
			p.aiIngestSynthetic(m, []aiEvent{{
				Type:          AIEventStatus,
				Status:        st.Status,
				CaptureStatus: st.CaptureStatus,
				SessionID:     sessionID,
				At:            nowMillis(),
			}})
		}

		// While the call is live, pull the co-pilot's suggestions and feed
		// them through the same ingest path as everything else. aiIngest
		// drops any suggestion whose id was already seen (RecentEvents), so
		// re-fetching the whole list every tick surfaces each suggestion in
		// the AI Assistant panel exactly once, live, as it is produced.
		if st.Status == AIStatusLive {
			p.aiPollSuggestions(m, meetingID, sessionID)
		}

		switch st.Status {
		case AIStatusCompleted:
			p.aiFetchFinal(m, sessionID)
			return
		case AIStatusFailed:
			p.aiIngestSynthetic(m, []aiEvent{{
				Type:      AIEventError,
				Kind:      "bot_failed",
				Message:   "the bot reported FAILED",
				SessionID: sessionID,
				At:        nowMillis(),
			}})
			return
		}
	}
	p.client.Log.Warn("honco ai: poller gave up waiting for the bot", "meeting_id", meetingID)
}

// aiPollSuggestions pulls the live co-pilot suggestions for one poll tick and
// ingests any that are new. The per-meeting copilot token is read from its
// own KV key and never leaves the server. Transient fetch errors are ignored
// (the next tick retries); the bot's own cooldown/fingerprinting already
// prevents duplicate suggestions being generated.
func (p *Plugin) aiPollSuggestions(m *Meeting, meetingID, sessionID string) {
	token := p.loadCopilotToken(meetingID)
	if token == "" {
		return // co-pilot not enabled for this meeting
	}
	sugs, err := p.aiService().Suggestions(sessionID, token)
	if err != nil {
		// Do not swallow this silently: a decode or transport fault here is
		// exactly how live suggestions can vanish while everything upstream
		// looks healthy. redactErr strips the URL (and its token) and the
		// suggestion body never reaches the log, so no secret or meeting
		// content is written.
		p.client.Log.Warn("honco ai: suggestion poll failed", "meeting_id", meetingID, "err", redactErr(err))
		return
	}
	if len(sugs) == 0 {
		return
	}
	events := make([]aiEvent, 0, len(sugs))
	for _, sg := range sugs {
		events = append(events, aiEvent{
			Type:      AIEventSuggestion,
			ID:        sg.ID, // dedup key: aiIngest drops an id it has seen
			Kind:      sg.Kind,
			Title:     sg.Title,
			Text:      sg.Text,
			Source:    sg.Source,
			Status:    sg.Status,
			SessionID: sessionID,
			At:        nowMillis(),
		})
	}
	p.aiIngestSynthetic(m, events)
}

// aiFetchFinal pulls the transcript and the summary once the bot is done,
// and stores the summary where Meeting Intelligence reads it.
func (p *Plugin) aiFetchFinal(m *Meeting, sessionID string) {
	now := nowMillis()
	var events []aiEvent

	// Transcript first, so the final event lands on a session that already
	// holds the lines it summarises. The bot has no paging; Honco keeps
	// its usual window (validateEvent enforces the per-event cap, so the
	// lines are chunked to it).
	//
	// after = -1, not 0: `after` is an EXCLUSIVE lower bound on the bot's
	// segment sequence, and the bot numbers segments from 0. Passing 0
	// would drop segment 0 -- the first thing anyone said in the meeting.
	lines, terr := p.aiService().Transcript(sessionID, -1, aiMaxTranscriptLines)
	if terr != nil {
		p.client.Log.Warn("honco ai: transcript fetch", "meeting_id", m.ID, "err", redactErr(terr))
	}
	for start := 0; start < len(lines); start += aiMaxEventsPerPush {
		end := start + aiMaxEventsPerPush
		if end > len(lines) {
			end = len(lines)
		}
		events = append(events, aiEvent{
			Type:      AIEventTranscript,
			Lines:     lines[start:end],
			SessionID: sessionID,
			At:        now,
		})
	}

	final, ferr := p.aiService().Summary(sessionID)
	if ferr != nil {
		p.client.Log.Warn("honco ai: summary fetch", "meeting_id", m.ID, "err", redactErr(ferr))
		events = append(events, aiEvent{
			Type:      AIEventError,
			Kind:      "summary_unavailable",
			Message:   "the bot finished but its summary could not be fetched",
			SessionID: sessionID,
			At:        now,
		})
		p.aiIngestSynthetic(m, events)
		return
	}

	events = append(events, aiEvent{
		Type:          AIEventFinal,
		Summary:       final.Summary,
		KeyPoints:     final.KeyPoints,
		Decisions:     final.Decisions,
		ActionItems:   final.ActionItems,
		TranscriptRef: final.TranscriptRef,
		SessionID:     sessionID,
		At:            now,
	})
	p.aiIngestSynthetic(m, events)

	// Also into Meeting Intelligence, so "View Summary" on the card and
	// the Summaries tab show it. This is the existing table, written
	// through the existing claim/finish pair, marked ready. finishSummary
	// is not used because it would send a second "summary ready" DM on top
	// of the one aiIngest just sent for the same meeting.
	p.aiStoreSummaryForIntelligence(m, final, len(lines))
}

// aiIngestSynthetic validates events the way the push endpoint does and
// then ingests them. Validation matters even for events Honco built
// itself: it applies the same clipping, and it refuses a final event that
// carries nothing, which is how an empty bot summary is kept out of the
// panel rather than shown as a blank success.
func (p *Plugin) aiIngestSynthetic(m *Meeting, events []aiEvent) {
	kept := events[:0]
	for i := range events {
		if verr := validateEvent(&events[i]); verr != nil {
			p.client.Log.Warn("honco ai: pulled event rejected", "meeting_id", m.ID, "type", events[i].Type, "err", verr.Error())
			continue
		}
		kept = append(kept, events[i])
	}
	if len(kept) == 0 {
		return
	}
	if _, _, _, err := p.aiIngest(m, kept); err != nil {
		p.client.Log.Warn("honco ai: could not store pulled events", "meeting_id", m.ID, "err", err.Error())
	}
}

// aiStoreSummaryForIntelligence writes the bot's summary into
// honco_meeting_summaries. The row is claimed with force, because a
// meeting may already hold a failed row from the channel-conversation
// summariser (which is unreachable from this deployment), and the bot's
// real output should replace it.
func (p *Plugin) aiStoreSummaryForIntelligence(m *Meeting, final *AIFinal, lineCount int) {
	teamID := ""
	if ch, err := p.client.Channel.Get(m.ChannelID); err == nil && ch != nil {
		teamID = ch.TeamId
	}
	now := nowMillis()
	start, end := p.conversationWindow(m)

	claim := &MeetingSummary{
		ID:          model.NewId(),
		MeetingID:   m.ID,
		ChannelID:   m.ChannelID,
		TeamID:      teamID,
		RequesterID: m.CreatorID,
		Status:      SummaryPending,
		WindowStart: start,
		WindowEnd:   end,
		UpdatedAt:   now,
	}
	if _, err := p.store.ClaimSummaryGeneration(claim, true, now); err != nil {
		p.client.Log.Warn("honco ai: could not claim summary row", "meeting_id", m.ID, "err", err.Error())
		return
	}

	result := &MeetingSummary{
		MeetingID:    m.ID,
		ChannelID:    m.ChannelID,
		TeamID:       teamID,
		RequesterID:  m.CreatorID,
		Status:       SummaryReady,
		Summary:      final.Summary,
		KeyPoints:    strings.Join(final.KeyPoints, "\n"),
		Decisions:    strings.Join(final.Decisions, "\n"),
		ActionItems:  strings.Join(final.ActionItems, "\n"),
		Participants: strings.Join(final.Participants, "\n"),
		RawOutput:    renderAIFinalMarkdown(final),
		MessageCount: lineCount,
		WindowStart:  start,
		WindowEnd:    end,
		UpdatedAt:    now,
	}
	if err := p.store.FinishSummary(result); err != nil {
		p.client.Log.Error("honco ai: could not store summary for Meeting Intelligence", "meeting_id", m.ID, "err", err.Error())
		return
	}
	if fresh, err := p.store.GetMeeting(m.ID); err == nil && fresh != nil {
		p.refreshMeetingCard(fresh)
	}
}

// renderAIFinalMarkdown is the raw_output column: the whole summary as
// one document, in the headings Meeting Intelligence already knows, with
// the model named so a reader can tell where it came from.
func renderAIFinalMarkdown(f *AIFinal) string {
	var b strings.Builder
	section := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		b.WriteString("## " + title + "\n")
		for _, it := range items {
			b.WriteString("- " + it + "\n")
		}
		b.WriteString("\n")
	}
	if f.Summary != "" {
		b.WriteString("## Summary\n" + f.Summary + "\n\n")
	}
	section("Key discussion points", f.KeyPoints)
	section("Decisions", f.Decisions)
	section("Action items", f.ActionItems)
	section("Participants", f.Participants)
	if strings.HasPrefix(f.TranscriptRef, "model:") {
		b.WriteString("_Generated by the Honco meeting bot (" + strings.TrimPrefix(f.TranscriptRef, "model:") + ")._\n")
	}
	return strings.TrimSpace(b.String())
}
