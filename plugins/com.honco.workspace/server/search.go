package main

import (
	"fmt"
	"strings"
)

// Honco global search.
//
// The rule this file exists to enforce: a row the caller may not see is
// never SELECTed. Authorization is not a filter applied to results, it is
// part of every WHERE clause. The caller's teams and channels are resolved
// from Mattermost (see searchScope in search_api.go) and passed in here; a
// query with an empty scope returns nothing rather than everything, which
// is why each builder checks for that explicitly.
//
// Text matching is ILIKE over a scope-narrowed set. That is a deliberate
// choice, not an oversight. The scope predicate is `team_id IN (...)` or
// `channel_id IN (...)`, which the existing (team_id, ...) and
// (channel_id, ...) btree indexes can serve -- verified with
// enable_seqscan=off, where the planner uses idx_honco_tasks_due and
// idx_honco_meetings_channel. At the sizes these tables are today (~100
// rows) Postgres correctly prefers a sequential scan instead, and switches
// to the index as they grow. Either way the caller's own rows are all that
// reaches the substring match.
//
// No index is added here, because at this size one would cost more to
// maintain than it saves. If these tables grow by orders of magnitude the
// honest upgrade is a tsvector column plus a GIN index -- not a bigger
// LIMIT, and not a second search engine.

// searchLimits are the bounds the API enforces. A caller can ask for less
// but never for more: an unbounded search is a denial-of-service waiting to
// be discovered by accident.
const (
	searchMinQuery     = 2
	searchMaxQuery     = 128
	searchDefaultLimit = 20
	searchMaxLimit     = 50
	searchMaxPage      = 200

	// How many of each category the "all" view shows before offering to
	// open that category on its own.
	searchAllPerType = 5

	// Bounds on how much scope we will resolve for one caller. Someone in
	// more teams or channels than this is a pathological case, and letting
	// the IN list grow without limit would turn one search into a very
	// large query.
	searchMaxTeams    = 200
	searchMaxChannels = 1000
)

// Result types. These are the only values `type` may take.
const (
	SearchTypeAll        = "all"
	SearchTypeTasks      = "tasks"
	SearchTypeMeetings   = "meetings"
	SearchTypeRecordings = "recordings"
	SearchTypeSummaries  = "summaries"
	SearchTypeSupport    = "support"
)

// searchScope is everything the caller is allowed to see, resolved from
// Mattermost before any query runs.
type searchScope struct {
	UserID     string
	TeamIDs    []string
	ChannelIDs []string
	// SupportAgent widens support results to the open queue, exactly as
	// the support list endpoint does. It is never inferred from a request.
	SupportAgent bool
}

// empty reports whether this caller can see nothing at all. A user in no
// team is not an error, but every query would be pointless.
func (s *searchScope) empty() bool {
	return len(s.TeamIDs) == 0 && len(s.ChannelIDs) == 0
}

// SearchHit is one result, projected explicitly.
//
// Explicit projection is the point: no struct here is built by scanning a
// row into a generic map, so a column added to a table later cannot start
// appearing in search output without someone deciding it should. Fields
// that identify stored files (file_id, file_name), raw generator output and
// internal error text are deliberately absent.
type SearchHit struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Subtitle  string `json:"subtitle,omitempty"`
	Status    string `json:"status,omitempty"`
	TeamID    string `json:"team_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	At        int64  `json:"at,omitempty"`

	// Navigation. Each is only an identifier; opening any of them goes
	// through that feature's own endpoint, which authorizes again.
	MeetingID string `json:"meeting_id,omitempty"`
	PostID    string `json:"post_id,omitempty"`

	// Task detail.
	AssigneeID string `json:"assignee_id,omitempty"`
	DueAt      int64  `json:"due_at,omitempty"`

	// Meeting detail. Participant count is shown only because the caller
	// is already a member of the meeting's channel to have got this far.
	Participants int `json:"participants,omitempty"`
}

// SearchPage is one page of one category.
type SearchPage struct {
	Type    string       `json:"type"`
	Hits    []*SearchHit `json:"hits"`
	Total   int          `json:"total"`
	Page    int          `json:"page"`
	Limit   int          `json:"limit"`
	HasMore bool         `json:"has_more"`
}

// escapeLike makes a user's query safe to put inside a LIKE pattern.
//
// Without this a search for "100%" matches everything and a search for "_"
// matches every single character -- not a security hole by itself, but a
// query language the user did not ask for and cannot see. The backslash is
// escaped first, or it would escape the escapes.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

// placeholders renders $n,$n+1,... for an IN list and returns the values.
// Used instead of pq.Array so this file needs no driver-specific type.
func placeholders(start int, ids []string) (string, []any) {
	parts := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("$%d", start+i)
		args[i] = id
	}
	return strings.Join(parts, ","), args
}

// rankExpr orders results the way the request asked for, and no more than
// that: an exact title match first, then a prefix match, then anything else
// that matched. It is a three-way CASE, not a relevance score, and is
// described as such wherever it is user-visible.
func rankExpr(column string, exactArg, prefixArg int) string {
	return fmt.Sprintf(
		`CASE WHEN lower(%s) = lower($%d) THEN 0 WHEN %s ILIKE $%d ESCAPE '\' THEN 1 ELSE 2 END`,
		column, exactArg, column, prefixArg)
}

// searchQuery carries the argument list while a query is assembled.
type searchQuery struct {
	args []any
}

func (q *searchQuery) add(v any) int {
	q.args = append(q.args, v)
	return len(q.args)
}

// SearchTasks searches titles and descriptions within the caller's teams.
//
// Tasks are team-scoped, not channel-scoped, and Phase 3 established that
// any member of a team may read any task in it (they may not mutate one
// they neither created nor were assigned). Search follows that same rule
// rather than inventing a narrower or wider one.
func (s *Store) SearchTasks(sc *searchScope, term string, limit, offset int) ([]*SearchHit, int, error) {
	if len(sc.TeamIDs) == 0 {
		return []*SearchHit{}, 0, nil
	}
	q := &searchQuery{}
	esc := escapeLike(term)
	exact := q.add(term)
	prefix := q.add(esc + "%")
	contains := q.add("%" + esc + "%")

	in, inArgs := placeholders(len(q.args)+1, sc.TeamIDs)
	q.args = append(q.args, inArgs...)
	lim := q.add(limit)
	off := q.add(offset)

	query := fmt.Sprintf(`
		SELECT id, team_id, title, status, assignee_id, due_at, created_at,
		       %s AS rank, count(*) OVER () AS total
		FROM honco_tasks
		WHERE deleted_at = 0
		  AND team_id IN (%s)
		  AND (title ILIKE $%d ESCAPE '\' OR description ILIKE $%d ESCAPE '\')
		ORDER BY rank, created_at DESC
		LIMIT $%d OFFSET $%d`,
		rankExpr("title", exact, prefix), in, contains, contains, lim, off)

	rows, err := s.db.Query(query, q.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []*SearchHit{}
	total := 0
	for rows.Next() {
		h := &SearchHit{Type: SearchTypeTasks}
		var rank int
		if err := rows.Scan(&h.ID, &h.TeamID, &h.Title, &h.Status, &h.AssigneeID,
			&h.DueAt, &h.At, &rank, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, h)
	}
	return out, total, rows.Err()
}

// SearchMeetings searches meeting topics and room names in the caller's
// channels. Channel membership is the gate, so a private channel the caller
// is not in contributes nothing -- and so does a direct message channel
// they are not part of.
func (s *Store) SearchMeetings(sc *searchScope, term string, limit, offset int) ([]*SearchHit, int, error) {
	if len(sc.ChannelIDs) == 0 {
		return []*SearchHit{}, 0, nil
	}
	q := &searchQuery{}
	esc := escapeLike(term)
	exact := q.add(term)
	prefix := q.add(esc + "%")
	contains := q.add("%" + esc + "%")

	in, inArgs := placeholders(len(q.args)+1, sc.ChannelIDs)
	q.args = append(q.args, inArgs...)
	lim := q.add(limit)
	off := q.add(offset)

	query := fmt.Sprintf(`
		SELECT id, channel_id, coalesce(nullif(topic, ''), room_name), status,
		       creator_id, participant_count, post_id,
		       CASE WHEN started_at > 0 THEN started_at
		            WHEN scheduled_at > 0 THEN scheduled_at
		            ELSE created_at END,
		       %s AS rank, count(*) OVER () AS total
		FROM honco_meetings
		WHERE channel_id IN (%s)
		  AND (topic ILIKE $%d ESCAPE '\' OR room_name ILIKE $%d ESCAPE '\')
		ORDER BY rank, created_at DESC
		LIMIT $%d OFFSET $%d`,
		rankExpr("coalesce(nullif(topic, ''), room_name)", exact, prefix),
		in, contains, contains, lim, off)

	rows, err := s.db.Query(query, q.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []*SearchHit{}
	total := 0
	for rows.Next() {
		h := &SearchHit{Type: SearchTypeMeetings}
		var rank int
		if err := rows.Scan(&h.ID, &h.ChannelID, &h.Title, &h.Status, &h.Subtitle,
			&h.Participants, &h.PostID, &h.At, &rank, &total); err != nil {
			return nil, 0, err
		}
		h.MeetingID = h.ID
		out = append(out, h)
	}
	return out, total, rows.Err()
}

// SearchRecordings searches by the meeting a recording belongs to.
//
// The stored file name and file id are deliberately not searched and not
// returned. A recording is found by the meeting it recorded, which is how
// people actually look for one, and the storage identifiers stay server
// side where they belong.
func (s *Store) SearchRecordings(sc *searchScope, term string, limit, offset int) ([]*SearchHit, int, error) {
	if len(sc.ChannelIDs) == 0 {
		return []*SearchHit{}, 0, nil
	}
	q := &searchQuery{}
	esc := escapeLike(term)
	exact := q.add(term)
	prefix := q.add(esc + "%")
	contains := q.add("%" + esc + "%")

	in, inArgs := placeholders(len(q.args)+1, sc.ChannelIDs)
	q.args = append(q.args, inArgs...)
	lim := q.add(limit)
	off := q.add(offset)

	title := `coalesce(nullif(m.topic, ''), r.room_name)`
	query := fmt.Sprintf(`
		SELECT r.id, r.channel_id, %s, r.status, r.meeting_id, r.duration_secs,
		       r.created_at, %s AS rank, count(*) OVER () AS total
		FROM honco_recordings r
		LEFT JOIN honco_meetings m ON m.id = r.meeting_id
		WHERE r.channel_id IN (%s)
		  AND (m.topic ILIKE $%d ESCAPE '\' OR r.room_name ILIKE $%d ESCAPE '\')
		ORDER BY rank, r.created_at DESC
		LIMIT $%d OFFSET $%d`,
		title, rankExpr(title, exact, prefix), in, contains, contains, lim, off)

	rows, err := s.db.Query(query, q.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []*SearchHit{}
	total := 0
	for rows.Next() {
		h := &SearchHit{Type: SearchTypeRecordings}
		var rank int
		var secs int64
		if err := rows.Scan(&h.ID, &h.ChannelID, &h.Title, &h.Status, &h.MeetingID,
			&secs, &h.At, &rank, &total); err != nil {
			return nil, 0, err
		}
		if secs > 0 {
			h.Subtitle = fmt.Sprintf("%d min", secs/60)
		}
		out = append(out, h)
	}
	return out, total, rows.Err()
}

// SearchSummaries searches stored meeting summaries.
//
// This is the most sensitive surface in the feature: a summary is a
// condensation of a conversation, so matching its body means matching
// things people said. It is gated on membership of the summary's channel
// exactly like the summary endpoint itself, and the searched columns are
// the ones already shown in the Meeting Intelligence panel to anyone who
// can open it -- summary, key points, decisions and action items. raw_output
// and error_message are neither searched nor returned.
func (s *Store) SearchSummaries(sc *searchScope, term string, limit, offset int) ([]*SearchHit, int, error) {
	if len(sc.ChannelIDs) == 0 {
		return []*SearchHit{}, 0, nil
	}
	q := &searchQuery{}
	esc := escapeLike(term)
	exact := q.add(term)
	prefix := q.add(esc + "%")
	contains := q.add("%" + esc + "%")

	in, inArgs := placeholders(len(q.args)+1, sc.ChannelIDs)
	q.args = append(q.args, inArgs...)
	lim := q.add(limit)
	off := q.add(offset)

	title := `coalesce(nullif(m.topic, ''), m.room_name, '')`
	query := fmt.Sprintf(`
		SELECT s.id, s.channel_id, s.team_id, %s, s.status, s.meeting_id,
		       s.message_count, s.updated_at, %s AS rank, count(*) OVER () AS total
		FROM honco_meeting_summaries s
		LEFT JOIN honco_meetings m ON m.id = s.meeting_id
		WHERE s.channel_id IN (%s)
		  AND s.status = 'ready'
		  AND (m.topic ILIKE $%d ESCAPE '\'
		       OR s.summary ILIKE $%d ESCAPE '\'
		       OR s.key_points ILIKE $%d ESCAPE '\'
		       OR s.decisions ILIKE $%d ESCAPE '\'
		       OR s.action_items ILIKE $%d ESCAPE '\')
		ORDER BY rank, s.updated_at DESC
		LIMIT $%d OFFSET $%d`,
		title, rankExpr(title, exact, prefix), in,
		contains, contains, contains, contains, contains, lim, off)

	rows, err := s.db.Query(query, q.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []*SearchHit{}
	total := 0
	for rows.Next() {
		h := &SearchHit{Type: SearchTypeSummaries}
		var rank, msgs int
		if err := rows.Scan(&h.ID, &h.ChannelID, &h.TeamID, &h.Title, &h.Status,
			&h.MeetingID, &msgs, &h.At, &rank, &total); err != nil {
			return nil, 0, err
		}
		if msgs > 0 {
			h.Subtitle = fmt.Sprintf("From %d messages", msgs)
		}
		out = append(out, h)
	}
	return out, total, rows.Err()
}

// SearchSupport searches support requests the caller may see.
//
// Visibility is the same relationship the support endpoints enforce: the
// requester, the assigned agent, or any support agent (who needs the
// queue). It is additionally confined to the caller's teams, so an agent
// does not see requests from a team they left. `reason` -- the text an
// agent gives when declining -- is searched by nobody and returned to
// nobody here; it is between the two people involved.
func (s *Store) SearchSupport(sc *searchScope, term string, limit, offset int) ([]*SearchHit, int, error) {
	if len(sc.TeamIDs) == 0 {
		return []*SearchHit{}, 0, nil
	}
	q := &searchQuery{}
	esc := escapeLike(term)
	exact := q.add(term)
	prefix := q.add(esc + "%")
	contains := q.add("%" + esc + "%")
	user := q.add(sc.UserID)

	in, inArgs := placeholders(len(q.args)+1, sc.TeamIDs)
	q.args = append(q.args, inArgs...)

	// The relationship test. An agent additionally sees the open queue,
	// which is the same widening ListSupportRequests applies.
	visible := fmt.Sprintf(`(requester_id = $%d OR agent_id = $%d`, user, user)
	if sc.SupportAgent {
		visible += ` OR status = '` + SupportOpen + `'`
	}
	visible += `)`

	lim := q.add(limit)
	off := q.add(offset)

	query := fmt.Sprintf(`
		SELECT id, team_id, channel_id, issue, status, requester_id, agent_id,
		       post_id, created_at, %s AS rank, count(*) OVER () AS total
		FROM honco_support_requests
		WHERE team_id IN (%s)
		  AND %s
		  AND issue ILIKE $%d ESCAPE '\'
		ORDER BY rank, created_at DESC
		LIMIT $%d OFFSET $%d`,
		rankExpr("issue", exact, prefix), in, visible, contains, lim, off)

	rows, err := s.db.Query(query, q.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []*SearchHit{}
	total := 0
	for rows.Next() {
		h := &SearchHit{Type: SearchTypeSupport}
		var rank int
		var requester, agent string
		if err := rows.Scan(&h.ID, &h.TeamID, &h.ChannelID, &h.Title, &h.Status,
			&requester, &agent, &h.PostID, &h.At, &rank, &total); err != nil {
			return nil, 0, err
		}
		h.AssigneeID = agent
		h.Subtitle = requester
		out = append(out, h)
	}
	return out, total, rows.Err()
}
