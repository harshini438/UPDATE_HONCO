package main

import (
	"database/sql"
	"errors"
)

// Meeting links a Jitsi room to a Honco Chat channel.
//
// This registry is what makes the recording callback safe. Jibri reports
// only a room name; without a row here, the plugin has no channel to file
// the recording against and no way to know the room is one of ours -- so
// an unregistered room is rejected outright rather than being trusted.
type Meeting struct {
	ID        string `json:"id"`
	RoomName  string `json:"room_name"`
	ChannelID string `json:"channel_id"`
	CreatorID string `json:"creator_id"`
	Topic     string `json:"topic"`
	CreatedAt int64  `json:"created_at"`

	// Lifecycle, added in migration 5. Meetings registered before that
	// migration default to `ended` -- their calls are long over, and
	// defaulting them to active would resurrect them in the channel.
	Status           string `json:"status"`
	PostID           string `json:"post_id,omitempty"`
	ScheduledAt      int64  `json:"scheduled_at,omitempty"`
	StartedAt        int64  `json:"started_at,omitempty"`
	EndedAt          int64  `json:"ended_at,omitempty"`
	ParticipantCount int    `json:"participant_count"`
	UpdatedAt        int64  `json:"updated_at,omitempty"`
}

// Recording statuses. `ready` means a media file arrived and was stored;
// `failed` means Jibri reported the session produced nothing usable.
//
// `unavailable` is never written to the database. It is what the meeting
// card shows when a `ready` row's file has since been removed -- the row
// is history, the file is gone, and the card must not offer a dead link.
const (
	RecordingReady       = "ready"
	RecordingFailed      = "failed"
	RecordingUnavailable = "unavailable"
)

type Recording struct {
	ID           string `json:"id"`
	MeetingID    string `json:"meeting_id"`
	RoomName     string `json:"room_name"`
	ChannelID    string `json:"channel_id"`
	Status       string `json:"status"`
	FileID       string `json:"file_id"`
	FileName     string `json:"file_name"`
	SizeBytes    int64  `json:"size_bytes"`
	DurationSecs int64  `json:"duration_seconds"`
	ErrorMessage string `json:"error_message,omitempty"`
	CreatedAt    int64  `json:"created_at"`
}

const meetingColumns = `id, room_name, channel_id, creator_id, topic, created_at`

// meetingColumnsFull adds the lifecycle columns. The short list is kept for
// the recording paths, which predate the lifecycle and do not need it.
const meetingColumnsFull = meetingColumns + `, status, post_id, scheduled_at,
	started_at, ended_at, participant_count, updated_at`

// UpsertMeeting registers a room, or refreshes it if the same room is
// created again. Rooms carry entropy and are effectively single-use, but
// making this idempotent means a retried registration is harmless.
func (s *Store) UpsertMeeting(m *Meeting) error {
	// status and scheduled_at are set explicitly rather than left to the
	// column default: that default is 'ended', which exists only to stop
	// migration 5 resurrecting meetings that finished before there was a
	// lifecycle. A meeting being registered now is not one of those.
	_, err := s.db.Exec(`
		INSERT INTO honco_meetings (`+meetingColumns+`, status, scheduled_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$6)
		ON CONFLICT (room_name) DO UPDATE
		SET channel_id   = EXCLUDED.channel_id,
		    creator_id   = EXCLUDED.creator_id,
		    topic        = EXCLUDED.topic,
		    status       = EXCLUDED.status,
		    scheduled_at = EXCLUDED.scheduled_at,
		    updated_at   = EXCLUDED.updated_at`,
		m.ID, m.RoomName, m.ChannelID, m.CreatorID, m.Topic, m.CreatedAt,
		m.Status, m.ScheduledAt)
	return err
}

// GetMeetingByRoom resolves the room name Jibri reports back to a meeting.
// Like GetMeeting it reads the full row, so the caller can update the card
// without a second query -- and without mistaking a missing PostID for a
// meeting that has no card.
func (s *Store) GetMeetingByRoom(room string) (*Meeting, error) {
	var m Meeting
	err := s.db.QueryRow(
		`SELECT `+meetingColumnsFull+` FROM honco_meetings WHERE room_name = $1`, room,
	).Scan(&m.ID, &m.RoomName, &m.ChannelID, &m.CreatorID, &m.Topic, &m.CreatedAt,
		&m.Status, &m.PostID, &m.ScheduledAt, &m.StartedAt, &m.EndedAt,
		&m.ParticipantCount, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

const recordingColumns = `id, meeting_id, room_name, channel_id, status, file_id,
	file_name, size_bytes, duration_secs, error_message, created_at`

func (s *Store) CreateRecording(r *Recording) error {
	_, err := s.db.Exec(`
		INSERT INTO honco_recordings (`+recordingColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		r.ID, r.MeetingID, r.RoomName, r.ChannelID, r.Status, r.FileID,
		r.FileName, r.SizeBytes, r.DurationSecs, r.ErrorMessage, r.CreatedAt)
	return err
}

// ListRecordingsForChannel powers the "recordings in this channel" view.
// The caller is responsible for having checked channel membership first --
// this is a plain query, not an authorization boundary.
func (s *Store) ListRecordingsForChannel(channelID string, limit int) ([]*Recording, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT `+recordingColumns+` FROM honco_recordings
		 WHERE channel_id = $1 ORDER BY created_at DESC LIMIT $2`, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Recording{}
	for rows.Next() {
		var r Recording
		if err := rows.Scan(&r.ID, &r.MeetingID, &r.RoomName, &r.ChannelID, &r.Status,
			&r.FileID, &r.FileName, &r.SizeBytes, &r.DurationSecs, &r.ErrorMessage, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *Store) CountRecordings() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM honco_recordings`).Scan(&n)
	return n, err
}
