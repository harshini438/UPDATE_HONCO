package main

import (
	"net/http"
)

// Meeting collaboration endpoints.
//
// Read-only, and authorized exactly like the rest of the plugin: the caller
// must be a current member of the meeting's channel, asked of Mattermost
// rather than of our own tables. A meeting id is a reference, never a
// capability.
//
// There is deliberately no "join" endpoint. Joining is opening the Jitsi
// room, and the room address is already in the card's props; an endpoint
// that "joins on your behalf" would add a way to be wrong about which room
// someone ends up in, and would risk creating a second room.

// handleGetMeeting returns one meeting's collaboration state, including
// who is currently in the call.
//
// This is what the panel reads on open. Live updates arrive over the
// WebSocket instead, so nothing here is polled by the browser.
func (p *Plugin) handleGetMeeting(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	// Same gate as Meeting Intelligence: resolves the meeting, proves
	// channel membership, and 404s indistinguishably otherwise.
	meeting, _, ok := p.meetingContext(w, r, userID)
	if !ok {
		return
	}

	props := p.buildMeetingProps(meeting)
	writeJSON(w, http.StatusOK, map[string]any{
		"meeting": meeting,
		"card":    props,
	})
}

// handleListActiveMeetings lists the meetings currently running in a
// channel the caller can read.
//
// Scoped to one channel on purpose: a "what is happening everywhere" view
// would have to aggregate across channels the caller may not belong to,
// which is a disclosure problem rather than a feature.
func (p *Plugin) handleListActiveMeetings(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	channelID := muxVar(r, "channel_id")
	if !validID(channelID) {
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

	cards := make([]*meetingProps, 0, len(meetings))
	for _, m := range meetings {
		// ListMeetingsForChannel uses the short column set, so the
		// lifecycle fields are not populated on those rows. Read the full
		// row for anything being rendered as a card.
		full, ferr := p.store.GetMeeting(m.ID)
		if ferr != nil || full == nil {
			continue
		}
		cards = append(cards, p.buildMeetingProps(full))
	}
	writeJSON(w, http.StatusOK, map[string]any{"meetings": cards})
}
