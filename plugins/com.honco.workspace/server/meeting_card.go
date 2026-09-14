package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// The meeting card.
//
// One post per meeting, updated in place. Mattermost renders it through the
// webapp bundle's post-type component; the props below are that component's
// entire input, so everything the card shows is decided here, server-side,
// where team and channel membership have already been established.
//
// The message text is a real fallback, not a placeholder: it is what shows
// in notifications, search results, the mobile app, and any client that
// does not load the plugin bundle. It has to make sense on its own.

// PostTypeMeeting is the custom post type. Mattermost requires the
// `custom_` prefix for plugin-owned post types.
const PostTypeMeeting = "custom_honco_meeting"

// WebSocketEventMeeting is broadcast when a card changes, so open clients
// update without polling and without a page reload.
const WebSocketEventMeeting = "meeting_updated"

// meetingProps is what the card component receives.
//
// Only display data goes in here. No room secrets, no callback secrets, no
// occupant JIDs -- props are visible to every client that can read the
// post, so this struct is a disclosure boundary.
type meetingProps struct {
	MeetingID    string   `json:"meeting_id"`
	Topic        string   `json:"topic"`
	Status       string   `json:"status"`
	CreatorID    string   `json:"creator_id"`
	CreatorName  string   `json:"creator_name"`
	JoinURL      string   `json:"join_url"`
	Participants []string `json:"participants"`
	Count        int      `json:"participant_count"`
	ScheduledFor string   `json:"scheduled_for,omitempty"`
	StartedAt    int64    `json:"started_at,omitempty"`
	EndedAt      int64    `json:"ended_at,omitempty"`

	// Actions the card should offer. Set only when the underlying thing
	// actually exists, so the card never advertises a dead button.
	RecordingStatus string `json:"recording_status,omitempty"`
	RecordingFileID string `json:"recording_file_id,omitempty"`
	HasSummary      bool   `json:"has_summary"`
}

// joinURL rebuilds the room address from configuration.
//
// It is derived rather than stored so that moving the deployment to a new
// address fixes every existing card at once. Critically, this never invents
// a room: the room name is the one registered when the meeting was created,
// so Join always lands in the existing call.
func (p *Plugin) joinURL(m *Meeting) string {
	base := strings.TrimRight(strings.TrimSpace(p.config().MeetPublicURL), "/")
	if base == "" {
		base = defaultMeetPublicURL
	}
	return base + "/" + m.RoomName
}

// buildMeetingProps assembles the card's state from the database.
func (p *Plugin) buildMeetingProps(m *Meeting) *meetingProps {
	props := &meetingProps{
		// An empty list, never nil: the card renders this directly and a
		// null would have to be guarded at every use site.
		Participants: []string{},
		MeetingID:    m.ID,
		Topic:        m.Topic,
		Status:       m.Status,
		CreatorID:    m.CreatorID,
		CreatorName:  p.username(m.CreatorID),
		JoinURL:      p.joinURL(m),
		Count:        m.ParticipantCount,
		StartedAt:    m.StartedAt,
		EndedAt:      m.EndedAt,
	}
	if props.Topic == "" {
		props.Topic = m.RoomName
	}
	if m.ScheduledAt > 0 {
		props.ScheduledFor = formatDue(m.ScheduledAt)
	}

	if people, err := p.store.ListPresentParticipants(m.ID); err == nil {
		for _, person := range people {
			props.Participants = append(props.Participants, person.DisplayName)
		}
		// The stored count is what the poller last saw; the list is what is
		// present now. Prefer the list when we have one.
		props.Count = len(props.Participants)
	}

	// Recording and summary are read from the features that already own
	// them. This is not a second recording system -- it is the same rows
	// the Jibri callback and Meeting Intelligence already write.
	if recs, err := p.store.ListRecordingsForChannel(m.ChannelID, 50); err == nil {
		for _, r := range recs {
			if r.MeetingID == m.ID {
				props.RecordingStatus = r.Status
				props.RecordingFileID = r.FileID
				// A recording row can outlive its file: the file was
				// removed, or Mattermost soft-deleted it with the post.
				// A card that still says "View Recording" would then be
				// a dead link, forever. Ask Mattermost whether the file is
				// still there and say "unavailable" if it is not.
				if r.Status == RecordingReady && r.FileID != "" {
					if info, ferr := p.client.File.GetInfo(r.FileID); ferr != nil || info == nil || info.DeleteAt != 0 {
						props.RecordingStatus = RecordingUnavailable
						props.RecordingFileID = ""
					}
				}
				break
			}
		}
	}
	if s, err := p.store.GetSummaryByMeeting(m.ID); err == nil && s != nil {
		props.HasSummary = s.Status == SummaryReady || s.Status == SummaryEmpty
	}
	return props
}

// cardFallbackText is what non-plugin clients see. It carries the same
// facts as the card, in plain markdown.
func cardFallbackText(props *meetingProps) string {
	var b strings.Builder

	switch props.Status {
	case MeetingScheduled:
		b.WriteString(fmt.Sprintf(":calendar: **%s** — scheduled", props.Topic))
		if props.ScheduledFor != "" {
			b.WriteString(" for " + props.ScheduledFor)
		}
	case MeetingActive:
		b.WriteString(fmt.Sprintf(":movie_camera: **%s** — meeting in progress", props.Topic))
	default:
		b.WriteString(fmt.Sprintf(":movie_camera: **%s** — meeting ended", props.Topic))
	}
	b.WriteString(fmt.Sprintf("\n_started by %s_", props.CreatorName))

	if props.Status == MeetingActive || props.Count > 0 {
		b.WriteString(fmt.Sprintf("\n%d participant%s", props.Count, plural(props.Count)))
		if len(props.Participants) > 0 {
			b.WriteString(": " + strings.Join(props.Participants, ", "))
		}
	}
	if props.Status != MeetingEnded {
		b.WriteString("\n\n[Join the meeting](" + props.JoinURL + ")")
	}
	if props.RecordingStatus == RecordingReady {
		b.WriteString("\n:clapper: Recording available")
	}
	if props.HasSummary {
		b.WriteString("\n:memo: Meeting summary available")
	}
	return b.String()
}

// propsMap converts the card's state into the plain map Mattermost post
// props must hold.
//
// This is not cosmetic. Post props cross the plugin RPC boundary, which
// encodes only basic types -- handing it a Go struct makes the encode fail
// and the API return (nil, nil), which pluginapi then dereferences. The
// symptom is the whole plugin process dying with a nil-pointer panic, far
// from the cause. Round-tripping through JSON guarantees only strings,
// numbers, bools, slices and maps survive.
func propsMap(props *meetingProps) map[string]any {
	raw, err := json.Marshal(props)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// --- creating and refreshing the card --------------------------------------

// ensureMeetingCard posts the card for a meeting, once.
//
// The post id is stored on the meeting, so a second call updates rather
// than posts again -- which is what makes this safe to call from both the
// registration path and the poller.
func (p *Plugin) ensureMeetingCard(m *Meeting) {
	defer p.recoverCard("ensureMeetingCard", m)
	if p.botID == "" {
		return
	}
	if m.PostID != "" {
		p.refreshMeetingCard(m)
		return
	}

	props := p.buildMeetingProps(m)
	post := &model.Post{
		UserId:    p.botID,
		ChannelId: m.ChannelID,
		Type:      PostTypeMeeting,
		Message:   cardFallbackText(props),
	}
	post.AddProp("honco_meeting", propsMap(props))

	if err := p.client.Post.CreatePost(post); err != nil {
		p.client.Log.Warn("honco: could not post meeting card", "err", err.Error())
		return
	}
	if err := p.store.SetMeetingPost(m.ID, post.Id); err != nil {
		p.client.Log.Warn("honco: could not record meeting post id", "err", err.Error())
	}
	m.PostID = post.Id

	// The start is announced here, at creation, rather than on the first
	// status transition.
	//
	// A meeting is registered as `active` the moment /meet runs, so there
	// is no scheduled->active edge for the poller to notice, and keying the
	// announcement off one would mean it never fired. Creation is also the
	// truthful moment: somebody started this meeting, whether or not anyone
	// has walked into the room yet. Scheduled meetings are excluded --
	// nothing has started yet, and they already say when they will.
	if m.Status != MeetingScheduled {
		title := m.Topic
		if title == "" {
			title = m.RoomName
		}
		p.postOnce(m, KindMeetingStarted,
			fmt.Sprintf("%s:%s", KindMeetingStarted, m.ID),
			fmt.Sprintf(":movie_camera: **%s** started **%s**", p.username(m.CreatorID), title))
	}

	p.broadcastMeeting(m, props)
}

// refreshMeetingCard updates the existing card in place and tells open
// clients. Both steps are best effort: a failed broadcast must not lose the
// stored state, and a missing post must not stop the meeting progressing.
func (p *Plugin) refreshMeetingCard(m *Meeting) {
	defer p.recoverCard("refreshMeetingCard", m)
	props := p.buildMeetingProps(m)

	if m.PostID != "" {
		post, err := p.client.Post.GetPost(m.PostID)
		if err == nil && post != nil && post.DeleteAt == 0 {
			post.Message = cardFallbackText(props)
			post.Type = PostTypeMeeting
			post.AddProp("honco_meeting", propsMap(props))
			if uerr := p.client.Post.UpdatePost(post); uerr != nil {
				p.client.Log.Warn("honco: could not update meeting card", "err", uerr.Error())
			}
		}
	}
	p.broadcastMeeting(m, props)
}

// broadcastMeeting pushes the card's new state to clients.
//
// Scoped to the meeting's channel, so Mattermost delivers it only to users
// who can read that channel -- the same boundary the post itself has. This
// is why the UI never has to poll.
func (p *Plugin) broadcastMeeting(m *Meeting, props *meetingProps) {
	p.client.Frontend.PublishWebSocketEvent(
		WebSocketEventMeeting,
		map[string]any{
			"meeting_id": m.ID,
			"channel_id": m.ChannelID,
			"status":     m.Status,
			"count":      props.Count,
		},
		&model.WebsocketBroadcast{ChannelId: m.ChannelID},
	)
}

// --- lifecycle announcements ----------------------------------------------

// announceLifecycle posts the short status lines that make a channel feel
// like a meeting is happening in it.
//
// Every line is claimed in the Feature 3 notification ledger first, so a
// repeated poll, a plugin restart, or two nodes racing produce exactly one
// message. Joins and leaves are batched into a single line per poll rather
// than one post each, because ten people arriving at once should not be ten
// posts.
func (p *Plugin) announceLifecycle(m *Meeting, prevStatus string, joined, left []string) {
	title := m.Topic
	if title == "" {
		title = m.RoomName
	}

	if len(joined) > 0 {
		p.postOnce(m, KindMeetingJoined,
			fmt.Sprintf("%s:%s:%s", KindMeetingJoined, m.ID, strings.Join(joined, ",")),
			fmt.Sprintf(":busts_in_silhouette: %s joined the meeting", humanList(joined)))
	}
	if len(left) > 0 {
		p.postOnce(m, KindMeetingLeft,
			fmt.Sprintf("%s:%s:%s:%d", KindMeetingLeft, m.ID, strings.Join(left, ","), m.UpdatedAt),
			fmt.Sprintf(":door: %s left the meeting", humanList(left)))
	}

	if prevStatus != MeetingEnded && m.Status == MeetingEnded {
		p.postOnce(m, KindMeetingEnded,
			fmt.Sprintf("%s:%s:%d", KindMeetingEnded, m.ID, m.EndedAt),
			fmt.Sprintf(":stop_button: **%s** ended", title))
	}
}

// postOnce posts to the meeting's channel at most once per dedupe key,
// reusing the ledger that Feature 3 already relies on.
func (p *Plugin) postOnce(m *Meeting, kind, dedupeKey, message string) {
	if p.botID == "" {
		return
	}
	if !p.claim(kind, m.ID, "", dedupeKey) {
		return
	}
	if err := p.client.Post.CreatePost(&model.Post{
		UserId:    p.botID,
		ChannelId: m.ChannelID,
		Message:   message,
		RootId:    m.PostID, // keep the chatter threaded under the card
	}); err != nil {
		p.client.Log.Warn("honco: could not post meeting update", "kind", kind, "err", err.Error())
	}
}

// humanList renders names the way a person would say them.
func humanList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return "**" + names[0] + "**"
	case 2:
		return "**" + names[0] + "** and **" + names[1] + "**"
	}
	quoted := make([]string, 0, len(names))
	for _, n := range names[:len(names)-1] {
		quoted = append(quoted, "**"+n+"**")
	}
	return strings.Join(quoted, ", ") + " and **" + names[len(names)-1] + "**"
}

// recoverCard keeps a card problem from killing the plugin.
//
// The card is a convenience layered on top of meetings, recordings and
// summaries. If rendering one panics, the meeting, its recording and its
// notifications must all still work -- so this contains the blast radius
// to the card and leaves a log line pointing at the meeting involved.
func (p *Plugin) recoverCard(where string, m *Meeting) {
	if rec := recover(); rec != nil {
		id := ""
		if m != nil {
			id = m.ID
		}
		p.client.Log.Error("honco: meeting card panicked",
			"where", where, "meeting_id", id, "recovered", fmt.Sprintf("%v", rec))
	}
}
