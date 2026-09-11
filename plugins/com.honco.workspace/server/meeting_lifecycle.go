package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// The meeting lifecycle.
//
//	scheduled -> active -> ended -> (recording ready/failed) -> (summary)
//
// A meeting is one row and one channel post. The post is updated in place
// at every transition rather than replaced, which is what stops a busy
// meeting from filling the channel with near-identical cards.

const (
	MeetingScheduled = "scheduled"
	MeetingActive    = "active"
	MeetingEnded     = "ended"
)

const (
	// How often the plugin asks Prosody who is in each live meeting.
	//
	// Jitsi ships no push mechanism in this version, so this is a poll. It
	// is deliberately server-side and scoped to meetings that are actually
	// running: browsers are told over the WebSocket and never poll.
	participantPollInterval = 10 * time.Second

	// A meeting whose room has been empty for this long is over. Jitsi
	// destroys the room when the last person leaves, but a brief empty
	// window is normal (the first person arriving before anyone else, a
	// reconnect), so ending is not immediate.
	emptyRoomGrace = 90 * time.Second

	// A meeting nobody ever joined is not left "active" forever.
	neverJoinedTimeout = 30 * time.Minute
)

// Participant is one person the meeting server reported.
type Participant struct {
	ID          string `json:"-"`
	MeetingID   string `json:"-"`
	OccupantKey string `json:"-"` // opaque; never rendered
	DisplayName string `json:"display_name"`
	JoinedAt    int64  `json:"joined_at"`
	LeftAt      int64  `json:"left_at,omitempty"`
	Present     bool   `json:"present"`
}

// --- meeting lifecycle persistence ----------------------------------------

// SetMeetingPost records which post is this meeting's card.
func (s *Store) SetMeetingPost(meetingID, postID string) error {
	_, err := s.db.Exec(
		`UPDATE honco_meetings SET post_id = $2, updated_at = $3 WHERE id = $1`,
		meetingID, postID, nowMillis())
	return err
}

// UpdateMeetingState writes a lifecycle transition.
func (s *Store) UpdateMeetingState(m *Meeting) error {
	_, err := s.db.Exec(`
		UPDATE honco_meetings
		SET status = $2, participant_count = $3, started_at = $4,
		    ended_at = $5, updated_at = $6
		WHERE id = $1`,
		m.ID, m.Status, m.ParticipantCount, m.StartedAt, m.EndedAt, nowMillis())
	return err
}

// ListLiveMeetings returns the meetings the poller has to look after:
// anything scheduled or active. Ended meetings are never polled again.
func (s *Store) ListLiveMeetings() ([]*Meeting, error) {
	rows, err := s.db.Query(
		`SELECT ` + meetingColumnsFull + ` FROM honco_meetings
		 WHERE status IN ('scheduled','active') ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMeetings(rows)
}

// UpsertParticipant records a sighting. The unique index on
// (meeting_id, occupant_key) makes a repeated poll idempotent: the same
// person seen ten times is one row, and re-appearing after leaving clears
// the departure rather than creating a duplicate.
//
// It reports whether this was a genuinely new arrival, which is what the
// caller uses to decide if a "joined" message is warranted.
func (s *Store) UpsertParticipant(p *Participant) (bool, error) {
	var isNew bool
	err := s.db.QueryRow(`
		INSERT INTO honco_meeting_participants
			(id, meeting_id, occupant_key, display_name, joined_at, left_at, present)
		VALUES ($1,$2,$3,$4,$5,0,TRUE)
		ON CONFLICT (meeting_id, occupant_key) DO UPDATE
		SET display_name = EXCLUDED.display_name,
		    present      = TRUE,
		    left_at      = 0
		RETURNING (xmax = 0)`,
		p.ID, p.MeetingID, p.OccupantKey, p.DisplayName, p.JoinedAt,
	).Scan(&isNew)
	if err != nil {
		return false, err
	}
	return isNew, nil
}

// MarkParticipantsLeft flags everyone in the meeting who is no longer in
// the room, and returns their names so the caller can report the departure.
func (s *Store) MarkParticipantsLeft(meetingID string, stillPresent []string) ([]string, error) {
	// A room with nobody in it still has to mark everyone gone, so the
	// empty case cannot be short-circuited.
	query := `UPDATE honco_meeting_participants
	          SET present = FALSE, left_at = $2
	          WHERE meeting_id = $1 AND present = TRUE`
	args := []any{meetingID, nowMillis()}

	// Exclude everyone still in the room. Built as individual placeholders
	// rather than an array literal so the keys stay bound parameters and
	// this needs no driver-specific array support.
	if len(stillPresent) > 0 {
		placeholders := make([]string, 0, len(stillPresent))
		for _, key := range stillPresent {
			args = append(args, key)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		query += ` AND occupant_key NOT IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` RETURNING display_name`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var gone []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		gone = append(gone, name)
	}
	return gone, rows.Err()
}

// ListPresentParticipants returns who is in the meeting right now.
func (s *Store) ListPresentParticipants(meetingID string) ([]*Participant, error) {
	rows, err := s.db.Query(`
		SELECT id, meeting_id, occupant_key, display_name, joined_at, left_at, present
		FROM honco_meeting_participants
		WHERE meeting_id = $1 AND present = TRUE
		ORDER BY joined_at`, meetingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Participant{}
	for rows.Next() {
		var p Participant
		if err := rows.Scan(&p.ID, &p.MeetingID, &p.OccupantKey, &p.DisplayName,
			&p.JoinedAt, &p.LeftAt, &p.Present); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

func scanMeetings(rows *sql.Rows) ([]*Meeting, error) {
	out := []*Meeting{}
	for rows.Next() {
		var m Meeting
		if err := rows.Scan(&m.ID, &m.RoomName, &m.ChannelID, &m.CreatorID, &m.Topic,
			&m.CreatedAt, &m.Status, &m.PostID, &m.ScheduledAt, &m.StartedAt,
			&m.EndedAt, &m.ParticipantCount, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// --- the poller ------------------------------------------------------------

// runMeetingPoller keeps every live meeting's card in step with reality.
//
// It is the only writer of participant state, so there is no race between
// two sources of truth. Like the due-date scanner it runs on a ticker and
// stops with the plugin.
func (p *Plugin) runMeetingPoller() {
	ticker := time.NewTicker(participantPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.pollMeetings()
		}
	}
}

func (p *Plugin) pollMeetings() {
	meetings, err := p.store.ListLiveMeetings()
	if err != nil {
		p.client.Log.Warn("honco: could not list live meetings", "err", err.Error())
		return
	}
	if len(meetings) == 0 {
		return
	}

	muc := newMUCClient(p.config())
	for _, m := range meetings {
		p.pollOneMeeting(muc, m)
	}
}

// pollOneMeeting reconciles one meeting against the meeting server.
func (p *Plugin) pollOneMeeting(muc *mucClient, m *Meeting) {
	// A scheduled meeting is not looked at until its time arrives; until
	// then there is no room to ask about.
	if m.Status == MeetingScheduled && m.ScheduledAt > nowMillis() {
		return
	}

	occupants, err := muc.Occupants(m.RoomName)
	if err != nil {
		// The meeting server could not be reached. That is ignorance, not
		// evidence that the room is empty -- leave the meeting exactly as
		// it is rather than ending it or zeroing its participants.
		if !errors.Is(err, errMUCUnavailable) {
			p.client.Log.Warn("honco: participant poll failed", "room", m.RoomName, "err", err.Error())
		}
		return
	}

	now := nowMillis()
	presentKeys := make([]string, 0, len(occupants))
	joined := []string{}

	for _, o := range occupants {
		key := occupantKey(o.JID)
		presentKeys = append(presentKeys, key)
		isNew, uerr := p.store.UpsertParticipant(&Participant{
			ID:          model.NewId(),
			MeetingID:   m.ID,
			OccupantKey: key,
			DisplayName: displayOrAnonymous(o),
			JoinedAt:    now,
		})
		if uerr != nil {
			p.client.Log.Warn("honco: could not record participant", "err", uerr.Error())
			continue
		}
		if isNew {
			joined = append(joined, displayOrAnonymous(o))
		}
	}

	left, err := p.store.MarkParticipantsLeft(m.ID, presentKeys)
	if err != nil {
		p.client.Log.Warn("honco: could not record departures", "err", err.Error())
	}

	changed := len(joined) > 0 || len(left) > 0
	prevStatus := m.Status
	m.ParticipantCount = len(occupants)

	switch {
	case len(occupants) > 0:
		// Somebody is in the room: the meeting is running.
		if m.Status != MeetingActive {
			m.Status = MeetingActive
			if m.StartedAt == 0 {
				m.StartedAt = now
			}
			changed = true
		}
		p.clearEmpty(m.ID)

	case m.Status == MeetingActive && m.StartedAt > 0:
		// Somebody was in this room and now nobody is. Give it a grace
		// period before declaring the meeting over -- a reconnect, or the
		// last person dropping and coming straight back, should not end a
		// live call.
		//
		// The StartedAt guard matters: a meeting is registered as active
		// the moment /meet runs, so without it an empty-but-brand-new
		// meeting would be "ended" one grace period later and anyone slow
		// to click Join would arrive at a dead card.
		if p.emptySince(m.ID) == 0 {
			p.markEmpty(m.ID, now)
		} else if now-p.emptySince(m.ID) > emptyRoomGrace.Milliseconds() {
			m.Status = MeetingEnded
			m.EndedAt = now
			p.clearEmpty(m.ID)
			changed = true
		}

	default:
		// Never started. Do not leave it hanging forever.
		started := m.StartedAt
		if started == 0 {
			started = m.CreatedAt
		}
		if now-started > neverJoinedTimeout.Milliseconds() {
			m.Status = MeetingEnded
			m.EndedAt = now
			changed = true
		}
	}

	if !changed {
		return
	}

	if err := p.store.UpdateMeetingState(m); err != nil {
		p.client.Log.Warn("honco: could not update meeting state", "err", err.Error())
		return
	}

	p.announceLifecycle(m, prevStatus, joined, left)
	p.refreshMeetingCard(m)

	// The AI assistant follows the meeting: attach when people are in the
	// call, detach when it ends. Both are best effort and never block
	// the poller.
	switch {
	case m.Status == MeetingActive && (prevStatus != MeetingActive || len(joined) > 0):
		p.aiMeetingStarted(m)
	case m.Status == MeetingEnded && prevStatus != MeetingEnded:
		p.aiMeetingEnded(m)
	}
}

// --- empty-room bookkeeping ------------------------------------------------

// The grace period is in memory on purpose: it is a debounce, not a fact
// worth persisting. A plugin restart simply restarts the clock, which at
// worst delays an ending by one grace period and never ends a live call
// early.
func (p *Plugin) emptySince(meetingID string) int64 {
	p.emptyMu.Lock()
	defer p.emptyMu.Unlock()
	return p.emptyRooms[meetingID]
}

func (p *Plugin) markEmpty(meetingID string, at int64) {
	p.emptyMu.Lock()
	defer p.emptyMu.Unlock()
	if p.emptyRooms == nil {
		p.emptyRooms = map[string]int64{}
	}
	p.emptyRooms[meetingID] = at
}

func (p *Plugin) clearEmpty(meetingID string) {
	p.emptyMu.Lock()
	defer p.emptyMu.Unlock()
	delete(p.emptyRooms, meetingID)
}
