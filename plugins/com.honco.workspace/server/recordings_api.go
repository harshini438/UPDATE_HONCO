package main

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
)

// MaxRecordingBytes caps a single upload. Jibri sends the whole media file
// in one multipart request, so without a ceiling one call could fill the
// disk. 2 GiB is generous for a long meeting and still bounded.
const MaxRecordingBytes = 2 << 30

// configuration is the plugin's settings, as edited in the System Console.
//
// The two secrets are compared, never logged, and never returned by any
// endpoint. The Summarizer* values are not secrets -- they say where the
// existing Claude summariser lives; the credential for it is an SSH
// identity on disk, which this plugin references by path and never reads.
type configuration struct {
	JibriCallbackSecret string
	MeetServiceSecret   string

	SummarizerCommand        string
	SummarizerHost           string
	SummarizerPort           int
	SummarizerUser           string
	SummarizerKeyPath        string
	SummarizerTimeoutSeconds int
}

// summarizerTimeout keeps the configured value inside a sane band. A zero
// (unset) value means the default rather than "no timeout", because a
// generation that never returns would hold a meeting in `pending` until
// the stale window expires.
func (c configuration) summarizerTimeout() int {
	if c.SummarizerTimeoutSeconds < 10 || c.SummarizerTimeoutSeconds > 900 {
		return defaultSummarizerTimeout
	}
	return c.SummarizerTimeoutSeconds
}

func (p *Plugin) config() configuration {
	var c configuration
	_ = p.client.Configuration.LoadPluginConfiguration(&c)
	return c
}

// checkSecret compares a header against a configured secret in constant
// time. An unset secret rejects everything -- failing closed, so a
// half-configured plugin cannot accept anonymous uploads.
func checkSecret(provided, expected string) bool {
	if expected == "" || provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// roomNameOK constrains what a room name may contain before it is used in
// a query or rendered into a post. Jitsi rooms are alphanumeric with dashes
// and underscores; anything else is rejected rather than sanitised.
func roomNameOK(s string) bool {
	if s == "" || len(s) > 255 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// --- meeting registration (called by meetsvc.py) ---------------------------

type registerMeetingRequest struct {
	RoomName  string `json:"room_name"`
	ChannelID string `json:"channel_id"`
	CreatorID string `json:"creator_id"`
	Topic     string `json:"topic"`
}

// handleRegisterMeeting records that a Jitsi room belongs to a channel.
//
// This is a service-to-service call from meetsvc, authenticated by a
// shared secret rather than a user session -- meetsvc acts on behalf of
// whoever ran /meet, and has no Mattermost session of its own.
func (p *Plugin) handleRegisterMeeting(w http.ResponseWriter, r *http.Request) {
	if !checkSecret(r.Header.Get("X-Honco-Service-Secret"), p.config().MeetServiceSecret) {
		p.client.Log.Warn("honco: meeting registration rejected (bad or missing service secret)")
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: "unauthorized"})
		return
	}

	var req registerMeetingRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.RoomName = strings.TrimSpace(req.RoomName)
	if !roomNameOK(req.RoomName) {
		p.writeErr(w, http.StatusBadRequest, "invalid room_name", nil)
		return
	}
	// The channel must exist. This is also what stops a compromised
	// meetsvc from parking recordings against an arbitrary string.
	ch, err := p.client.Channel.Get(req.ChannelID)
	if err != nil || ch == nil {
		p.writeErr(w, http.StatusBadRequest, "unknown channel_id", nil)
		return
	}

	m := &Meeting{
		ID:        model.NewId(),
		RoomName:  req.RoomName,
		ChannelID: req.ChannelID,
		CreatorID: req.CreatorID,
		Topic:     strings.TrimSpace(req.Topic),
		CreatedAt: nowMillis(),
	}
	if len(m.Topic) > 255 {
		m.Topic = m.Topic[:255]
	}
	if err := p.store.UpsertMeeting(m); err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not register meeting", err)
		return
	}
	p.client.Log.Info("honco: meeting registered", "room", m.RoomName, "channel_id", m.ChannelID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "registered", "meeting_id": m.ID})
}

// --- Jibri recording callback ---------------------------------------------

// handleRecordingComplete receives a finished recording from Jibri's
// finalize hook.
//
// It speaks the multipart contract the existing hook already sends:
// room_name, status, error_message and duration_seconds arrive BEFORE the
// file part, which matters because the body is streamed rather than
// buffered -- the metadata has to be known by the time the file is reached.
//
// Authentication is a shared secret, compared in constant time. There is
// no Mattermost session here: Jibri is a service, not a user.
func (p *Plugin) handleRecordingComplete(w http.ResponseWriter, r *http.Request) {
	if !checkSecret(r.Header.Get("X-Jibri-Callback-Secret"), p.config().JibriCallbackSecret) {
		// Deliberately terse, and logged without the provided value.
		p.client.Log.Warn("honco: recording callback rejected (bad or missing secret)",
			"remote", r.RemoteAddr)
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: "unauthorized"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxRecordingBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		p.writeErr(w, http.StatusBadRequest, "expected a multipart request", err)
		return
	}

	var (
		roomName string
		status   string
		errMsg   string
		duration int64
		meeting  *Meeting
		rec      *Recording
	)

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			p.writeErr(w, http.StatusBadRequest, "malformed multipart body", err)
			return
		}

		switch part.FormName() {
		case "file":
			// Everything needed to authorize and place this file must
			// already have arrived; if it has not, refuse rather than
			// buffer an unbounded body while we work it out.
			if meeting == nil {
				_ = part.Close()
				p.writeErr(w, http.StatusBadRequest, "file part arrived before a known room_name", nil)
				return
			}
			rec, err = p.storeRecording(part, meeting, roomName, duration)
			if err != nil {
				p.writeErr(w, http.StatusInternalServerError, "could not store recording", err)
				return
			}
		default:
			value, err := readSmallPart(part)
			if err != nil {
				p.writeErr(w, http.StatusBadRequest, "malformed field", err)
				return
			}
			switch part.FormName() {
			case "room_name":
				roomName = strings.TrimSpace(value)
				if !roomNameOK(roomName) {
					p.writeErr(w, http.StatusBadRequest, "invalid room_name", nil)
					return
				}
				// Unknown room => reject. A recording is only ever
				// accepted for a room this server registered.
				meeting, err = p.store.GetMeetingByRoom(roomName)
				if errors.Is(err, ErrNotFound) {
					p.client.Log.Warn("honco: recording callback for an unregistered room", "room", roomName)
					writeJSON(w, http.StatusNotFound, errorBody{Error: "unknown room"})
					return
				}
				if err != nil {
					p.writeErr(w, http.StatusInternalServerError, "could not resolve room", err)
					return
				}
			case "status":
				status = strings.TrimSpace(value)
			case "error_message":
				errMsg = truncate(strings.TrimSpace(value), 1024)
			case "duration_seconds":
				if n, convErr := strconv.ParseInt(strings.TrimSpace(value), 10, 64); convErr == nil && n >= 0 {
					duration = n
				}
			}
		}
	}

	if meeting == nil {
		p.writeErr(w, http.StatusBadRequest, "room_name is required", nil)
		return
	}

	// A failed session still gets a row and a message -- silence would
	// leave the person who pressed record wondering.
	if rec == nil {
		rec = &Recording{
			ID:           model.NewId(),
			MeetingID:    meeting.ID,
			RoomName:     roomName,
			ChannelID:    meeting.ChannelID,
			Status:       RecordingFailed,
			DurationSecs: duration,
			ErrorMessage: errMsg,
			CreatedAt:    nowMillis(),
		}
		if status == "" {
			rec.ErrorMessage = "Jibri reported no media for this session"
		}
		if err := p.store.CreateRecording(rec); err != nil {
			p.writeErr(w, http.StatusInternalServerError, "could not record failure", err)
			return
		}
		p.postRecordingMessage(meeting, rec)
		writeJSON(w, http.StatusOK, map[string]string{"message": "recording failure recorded"})
		return
	}

	p.postRecordingMessage(meeting, rec)
	writeJSON(w, http.StatusOK, map[string]string{
		"message":      "recording completion accepted",
		"recording_id": rec.ID,
	})
}

// storeRecording streams the media straight into Mattermost's own file
// store, attached to the meeting's channel. Using the platform's file
// service means the recording inherits Mattermost's existing access
// control: only channel members can fetch it, enforced by the server, not
// by this plugin.
func (p *Plugin) storeRecording(part *multipart.Part, m *Meeting, room string, duration int64) (*Recording, error) {
	name := sanitiseFileName(part.FileName())
	if name == "" {
		name = room + ".mp4"
	}

	info, err := p.client.File.Upload(part, name, m.ChannelID)
	if err != nil {
		return nil, err
	}

	rec := &Recording{
		ID:           model.NewId(),
		MeetingID:    m.ID,
		RoomName:     room,
		ChannelID:    m.ChannelID,
		Status:       RecordingReady,
		FileID:       info.Id,
		FileName:     info.Name,
		SizeBytes:    info.Size,
		DurationSecs: duration,
		CreatedAt:    nowMillis(),
	}
	if err := p.store.CreateRecording(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// postRecordingMessage announces the outcome in the meeting's channel, as
// the Honco bot, with the file attached when there is one. Channel
// membership is what decides who can see it -- the same rule as any other
// post, which is exactly the point.
func (p *Plugin) postRecordingMessage(m *Meeting, rec *Recording) {
	if p.botID == "" {
		return
	}

	// Claimed in the ledger first, keyed on the recording id, so a
	// retried callback (Jibri's hook tries several hosts) can never
	// announce the same recording twice.
	kind := KindRecordingReady
	if rec.Status != RecordingReady {
		kind = KindRecordingFailed
	}
	if !p.claim(kind, rec.ID, "", kind+":"+rec.ID) {
		return
	}

	post := &model.Post{UserId: p.botID, ChannelId: m.ChannelID}

	if rec.Status == RecordingReady {
		title := m.Topic
		if title == "" {
			title = m.RoomName
		}
		post.Message = fmt.Sprintf("Recording ready for **%s** (%s)", title, humanSize(rec.SizeBytes))
		if rec.DurationSecs > 0 {
			post.Message += fmt.Sprintf(" · %d min", rec.DurationSecs/60)
		}
		if rec.FileID != "" {
			post.FileIds = model.StringArray{rec.FileID}
		}
	} else {
		post.Message = fmt.Sprintf("Recording failed for **%s**.", m.RoomName)
		if rec.ErrorMessage != "" {
			post.Message += " " + rec.ErrorMessage
		}
	}

	if err := p.client.Post.CreatePost(post); err != nil {
		p.client.Log.Warn("honco: could not post recording message", "err", err.Error())
	}
}

// --- recordings listing (authenticated users) ------------------------------

// handleListRecordings returns the recordings for a channel the caller can
// actually read. Membership is checked against Mattermost, not our tables.
func (p *Plugin) handleListRecordings(w http.ResponseWriter, r *http.Request) {
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
		// Not a member: same answer as a channel that does not exist.
		p.notFound(w)
		return
	}
	recs, err := p.store.ListRecordingsForChannel(channelID, 50)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not list recordings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recordings": recs})
}

// --- helpers ---------------------------------------------------------------

// readSmallPart reads a non-file field with a hard cap, so a hostile
// caller cannot pad the request with an enormous "status" value.
func readSmallPart(part *multipart.Part) (string, error) {
	b, err := io.ReadAll(io.LimitReader(part, 64*1024))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// sanitiseFileName strips any directory component and rejects anything
// that is not a plain, recognisable file name -- a filename arrives from
// outside this server and must never influence a path.
func sanitiseFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.ReplaceAll(name, "..", "")
	if len(name) > 200 {
		name = name[len(name)-200:]
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return name
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
