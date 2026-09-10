package main

import (
	"database/sql"
	"errors"
	"strings"
)

// Remote support: the workflow around a RustDesk session, not the session
// itself.
//
// RustDesk OSS (hbbs + hbbr) exposes no HTTP API -- the console and REST
// interface are Server Pro features -- so Honco cannot create, query or
// terminate a remote session, and nothing here pretends otherwise. What
// Honco can do, and what this is, is manage the human workflow around it:
// who asked for help, who took it, when it started, when it ended, and an
// audit trail of that. The connection itself is made in the RustDesk
// client, out of band.
//
// The most important thing about this file is what it does NOT hold. There
// is no RustDesk password, no device credential, no relay secret and no
// token anywhere in the schema, the API, the notifications or the UI.
// RustDesk's own documentation is explicit that the device ID is not an
// authentication mechanism -- the password is -- so Honco carries neither.
// A support workflow has no business holding the keys to someone's desktop.

// Request states.
//
// The set is deliberately small. Every extra state is another transition
// to get wrong, and a support request only really has: nobody has it yet,
// somebody has it, it is happening, it is over.
const (
	SupportOpen      = "open"
	SupportAccepted  = "accepted"
	SupportActive    = "active"
	SupportEnded     = "ended"
	SupportCancelled = "cancelled"
	SupportRejected  = "rejected"
)

// Audit actions.
const (
	SupportActionCreated   = "created"
	SupportActionAccepted  = "accepted"
	SupportActionRejected  = "rejected"
	SupportActionStarted   = "started"
	SupportActionEnded     = "ended"
	SupportActionCancelled = "cancelled"
)

const maxIssueLength = 1024

// ErrInvalidTransition is returned when a caller asks for a state change
// the lifecycle does not allow. It becomes a 409, not a 400: the request
// is well-formed, it is the *state* that makes it impossible.
var ErrInvalidTransition = errors.New("honco: invalid state transition")

// SupportRequest is one request for remote help.
type SupportRequest struct {
	ID          string `json:"id"`
	TeamID      string `json:"team_id"`
	ChannelID   string `json:"channel_id,omitempty"`
	PostID      string `json:"post_id,omitempty"`
	RequesterID string `json:"requester_id"`
	AgentID     string `json:"agent_id,omitempty"`
	Issue       string `json:"issue"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	AcceptedAt  int64  `json:"accepted_at,omitempty"`
	StartedAt   int64  `json:"started_at,omitempty"`
	EndedAt     int64  `json:"ended_at,omitempty"`
	UpdatedAt   int64  `json:"updated_at"`
}

// SupportEvent is one line of the audit trail.
type SupportEvent struct {
	ID        string `json:"id"`
	RequestID string `json:"request_id"`
	ActorID   string `json:"actor_id"`
	Action    string `json:"action"`
	Detail    string `json:"detail,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// canTransition is the whole lifecycle, in one place.
//
// Expressed as a table rather than scattered `if` statements so that the
// illegal moves are as visible as the legal ones. Nothing reopens: an
// ended, cancelled or rejected request is final, and getting help again
// means asking again. That is deliberate -- resurrecting a closed request
// would make the audit trail ambiguous about which session was which.
func canTransition(from, to string) bool {
	allowed := map[string][]string{
		SupportOpen:     {SupportAccepted, SupportRejected, SupportCancelled},
		SupportAccepted: {SupportActive, SupportCancelled, SupportEnded},
		SupportActive:   {SupportEnded},
		// Terminal.
		SupportEnded:     {},
		SupportCancelled: {},
		SupportRejected:  {},
	}
	for _, s := range allowed[from] {
		if s == to {
			return true
		}
	}
	return false
}

// validSupportStatus guards anything arriving from outside.
func validSupportStatus(s string) bool {
	switch s {
	case SupportOpen, SupportAccepted, SupportActive,
		SupportEnded, SupportCancelled, SupportRejected:
		return true
	}
	return false
}

// sanitiseIssue bounds and cleans the one piece of free text a requester
// supplies.
//
// It is rendered into a channel post, so control characters and anything
// that could forge structure are removed rather than escaped at each use
// site. An empty description is allowed -- "my machine is broken" is often
// all someone can say -- but it is capped so a request cannot become a
// wall of text in a channel.
func sanitiseIssue(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f:
			continue
		case r == '`' || r == '|':
			continue // would break out of the card's formatting
		case r == '@' || r == '#':
			continue // must never become a mention or a channel link
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if len([]rune(out)) > maxIssueLength {
		out = string([]rune(out)[:maxIssueLength])
	}
	return out
}

// --- store -----------------------------------------------------------------

const supportColumns = `id, team_id, channel_id, post_id, requester_id, agent_id,
	issue, status, reason, created_at, accepted_at, started_at, ended_at, updated_at`

func scanSupport(row interface{ Scan(...any) error }) (*SupportRequest, error) {
	var r SupportRequest
	err := row.Scan(&r.ID, &r.TeamID, &r.ChannelID, &r.PostID, &r.RequesterID, &r.AgentID,
		&r.Issue, &r.Status, &r.Reason, &r.CreatedAt, &r.AcceptedAt, &r.StartedAt,
		&r.EndedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) CreateSupportRequest(r *SupportRequest) error {
	_, err := s.db.Exec(`
		INSERT INTO honco_support_requests (`+supportColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		r.ID, r.TeamID, r.ChannelID, r.PostID, r.RequesterID, r.AgentID,
		r.Issue, r.Status, r.Reason, r.CreatedAt, r.AcceptedAt, r.StartedAt,
		r.EndedAt, r.UpdatedAt)
	return err
}

func (s *Store) GetSupportRequest(id string) (*SupportRequest, error) {
	return scanSupport(s.db.QueryRow(
		`SELECT `+supportColumns+` FROM honco_support_requests WHERE id = $1`, id))
}

// TransitionSupportRequest moves a request to a new state, but only from
// the state it is actually in.
//
// The `WHERE status = $3` is the concurrency control: two agents pressing
// Accept at the same moment both pass the in-memory check, and exactly one
// of them updates a row. The loser gets ErrInvalidTransition rather than
// silently stealing the request.
func (s *Store) TransitionSupportRequest(r *SupportRequest, from string) error {
	res, err := s.db.Exec(`
		UPDATE honco_support_requests
		SET status = $2, agent_id = $4, reason = $5,
		    accepted_at = $6, started_at = $7, ended_at = $8, updated_at = $9
		WHERE id = $1 AND status = $3`,
		r.ID, r.Status, from, r.AgentID, r.Reason,
		r.AcceptedAt, r.StartedAt, r.EndedAt, r.UpdatedAt)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrInvalidTransition
	}
	return nil
}

func (s *Store) SetSupportPost(requestID, postID string) error {
	_, err := s.db.Exec(
		`UPDATE honco_support_requests SET post_id = $2 WHERE id = $1`, requestID, postID)
	return err
}

// ListSupportRequests returns the requests one user is entitled to see in
// a team.
//
// Authorization is in the query, not applied afterwards: a requester sees
// their own, an agent additionally sees the open queue and anything
// assigned to them. There is no code path that fetches everything and
// filters in Go, because that is the shape that leaks when someone later
// forgets the filter.
func (s *Store) ListSupportRequests(teamID, userID string, isAgent bool, limit int) ([]*SupportRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `SELECT ` + supportColumns + ` FROM honco_support_requests
	          WHERE team_id = $1 AND (requester_id = $2`
	args := []any{teamID, userID}
	if isAgent {
		query += ` OR agent_id = $2 OR status = '` + SupportOpen + `'`
	}
	query += `) ORDER BY created_at DESC LIMIT $3`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*SupportRequest{}
	for rows.Next() {
		r, err := scanSupport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddSupportEvent appends to the audit trail. Failure is reported to the
// caller rather than swallowed: an action that happened without a record
// of it is exactly what an audit trail exists to prevent.
func (s *Store) AddSupportEvent(e *SupportEvent) error {
	_, err := s.db.Exec(`
		INSERT INTO honco_support_events (id, request_id, actor_id, action, detail, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		e.ID, e.RequestID, e.ActorID, e.Action, e.Detail, e.CreatedAt)
	return err
}

func (s *Store) ListSupportEvents(requestID string) ([]*SupportEvent, error) {
	rows, err := s.db.Query(`
		SELECT id, request_id, actor_id, action, detail, created_at
		FROM honco_support_events WHERE request_id = $1 ORDER BY created_at`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*SupportEvent{}
	for rows.Next() {
		var e SupportEvent
		if err := rows.Scan(&e.ID, &e.RequestID, &e.ActorID, &e.Action, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
