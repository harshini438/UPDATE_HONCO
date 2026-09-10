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
}

// Recording statuses. `ready` means a media file arrived and was stored;
// `failed` means Jibri reported the session produced nothing usable.
const (
	RecordingReady  = "ready"
	RecordingFailed = "failed"
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

// UpsertMeeting registers a room, or refreshes it if the same room is
// created again. Rooms carry entropy and are effectively single-use, but
// making this idempotent means a retried registration is harmless.
func (s *Store) UpsertMeeting(m *Meeting) error {
	_, err := s.db.Exec(`
		INSERT INTO honco_meetings (`+meetingColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (room_name) DO UPDATE
		SET channel_id = EXCLUDED.channel_id,
		    creator_id = EXCLUDED.creator_id,
		    topic      = EXCLUDED.topic`,
		m.ID, m.RoomName, m.ChannelID, m.CreatorID, m.Topic, m.CreatedAt)
	return err
}

func (s *Store) GetMeetingByRoom(room string) (*Meeting, error) {
	var m Meeting
	err := s.db.QueryRow(
		`SELECT `+meetingColumns+` FROM honco_meetings WHERE room_name = $1`, room,
	).Scan(&m.ID, &m.RoomName, &m.ChannelID, &m.CreatorID, &m.Topic, &m.CreatedAt)
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
