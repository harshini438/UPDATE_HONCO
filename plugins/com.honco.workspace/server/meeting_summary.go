package main

import (
	"database/sql"
	"errors"
	"time"
)

// Meeting Intelligence: a structured summary of what was discussed in a
// meeting's channel, derived from the channel conversation itself.
//
// The input is Mattermost messages, not audio. There is no transcription
// anywhere in this path -- the conversation the team already typed is the
// record being summarised.

// Summary generation states.
//
// `empty` is deliberately distinct from `failed`: a meeting nobody typed
// in is a normal outcome, not an error, and telling the two apart is the
// difference between "try again later" and "there was nothing to read".
const (
	SummaryPending = "pending"
	SummaryReady   = "ready"
	SummaryFailed  = "failed"
	SummaryEmpty   = "empty"
)

// staleGeneration is how long a `pending` row may sit before another
// request is allowed to take it over.
//
// Generation runs in a goroutine, so a server restart mid-run would
// otherwise leave a row pending forever and permanently wedge that
// meeting. This bounds that to one window.
const staleGeneration = 15 * time.Minute

// MeetingSummary is one generated summary. There is at most one row per
// meeting; regenerating overwrites it in place.
type MeetingSummary struct {
	ID           string `json:"id"`
	MeetingID    string `json:"meeting_id"`
	ChannelID    string `json:"channel_id"`
	TeamID       string `json:"team_id"`
	RequesterID  string `json:"requester_id"`
	Status       string `json:"status"`
	Summary      string `json:"summary"`
	KeyPoints    string `json:"key_points"`
	Decisions    string `json:"decisions"`
	ActionItems  string `json:"action_items"`
	Participants string `json:"participants"`
	RawOutput    string `json:"raw_output,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	MessageCount int    `json:"message_count"`
	WindowStart  int64  `json:"window_start"`
	WindowEnd    int64  `json:"window_end"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

const summaryColumns = `id, meeting_id, channel_id, team_id, requester_id, status,
	summary, key_points, decisions, action_items, participants, raw_output,
	error_message, message_count, window_start, window_end, created_at, updated_at`

func scanSummary(row interface{ Scan(...any) error }) (*MeetingSummary, error) {
	var s MeetingSummary
	err := row.Scan(&s.ID, &s.MeetingID, &s.ChannelID, &s.TeamID, &s.RequesterID, &s.Status,
		&s.Summary, &s.KeyPoints, &s.Decisions, &s.ActionItems, &s.Participants, &s.RawOutput,
		&s.ErrorMessage, &s.MessageCount, &s.WindowStart, &s.WindowEnd, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ClaimSummaryGeneration is the concurrency gate for the whole feature.
//
// It atomically creates or takes over the single row for a meeting and
// reports whether this caller is the one that should now do the work.
// Two users pressing "Generate" at the same moment produce one generation
// between them, because the decision is made by the database in a single
// statement rather than by a read-then-write in the plugin.
//
// A caller wins only when there is no row yet, or the existing row is
// finished (ready/failed/empty) and `force` was asked for, or the existing
// row failed, or a previous `pending` has gone stale (its generator died
// with the process). A fresh `pending` never yields, so a retry while a
// generation is genuinely in flight is a no-op rather than a second run.
func (s *Store) ClaimSummaryGeneration(m *MeetingSummary, force bool, staleBefore int64) (bool, error) {
	res, err := s.db.Exec(`
		INSERT INTO honco_meeting_summaries (`+summaryColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,'','','','','','','',0,$7,$8,$9,$9)
		ON CONFLICT (meeting_id) DO UPDATE
		SET status       = $6,
		    requester_id = $5,
		    window_start = $7,
		    window_end   = $8,
		    updated_at   = $9,
		    error_message = ''
		WHERE honco_meeting_summaries.status <> $10
		   OR honco_meeting_summaries.updated_at < $11
		   OR $12`,
		m.ID, m.MeetingID, m.ChannelID, m.TeamID, m.RequesterID, SummaryPending,
		m.WindowStart, m.WindowEnd, m.UpdatedAt,
		SummaryPending, staleBefore, force)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// FinishSummary writes the result of a generation. It only ever touches
// the row it was told to, and never changes the meeting or channel it is
// attached to.
func (s *Store) FinishSummary(m *MeetingSummary) error {
	_, err := s.db.Exec(`
		UPDATE honco_meeting_summaries
		SET status = $2, summary = $3, key_points = $4, decisions = $5,
		    action_items = $6, participants = $7, raw_output = $8,
		    error_message = $9, message_count = $10, updated_at = $11
		WHERE meeting_id = $1`,
		m.MeetingID, m.Status, m.Summary, m.KeyPoints, m.Decisions,
		m.ActionItems, m.Participants, m.RawOutput, m.ErrorMessage,
		m.MessageCount, m.UpdatedAt)
	return err
}

func (s *Store) GetSummaryByMeeting(meetingID string) (*MeetingSummary, error) {
	return scanSummary(s.db.QueryRow(
		`SELECT `+summaryColumns+` FROM honco_meeting_summaries WHERE meeting_id = $1`, meetingID))
}

func (s *Store) GetMeeting(meetingID string) (*Meeting, error) {
	var m Meeting
	err := s.db.QueryRow(
		`SELECT `+meetingColumns+` FROM honco_meetings WHERE id = $1`, meetingID,
	).Scan(&m.ID, &m.RoomName, &m.ChannelID, &m.CreatorID, &m.Topic, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// ListMeetingsForChannel powers the meeting picker in the UI. Like every
// other list method here it is a plain query -- the caller has already
// proved it may read this channel.
func (s *Store) ListMeetingsForChannel(channelID string, limit int) ([]*Meeting, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.db.Query(
		`SELECT `+meetingColumns+` FROM honco_meetings
		 WHERE channel_id = $1 ORDER BY created_at DESC LIMIT $2`, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Meeting{}
	for rows.Next() {
		var m Meeting
		if err := rows.Scan(&m.ID, &m.RoomName, &m.ChannelID, &m.CreatorID, &m.Topic, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// LatestRecordingEnd returns when the meeting's recording arrived, which
// is the best available marker for when the meeting actually ended. Zero
// means no recording, and the caller falls back to a time window.
func (s *Store) LatestRecordingEnd(meetingID string) (int64, error) {
	var end sql.NullInt64
	err := s.db.QueryRow(
		`SELECT max(created_at) FROM honco_recordings WHERE meeting_id = $1`, meetingID).Scan(&end)
	if err != nil {
		return 0, err
	}
	if !end.Valid {
		return 0, nil
	}
	return end.Int64, nil
}
