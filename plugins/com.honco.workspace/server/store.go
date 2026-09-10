package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned when a row does not exist, or exists but is
// soft-deleted. Callers turn it into a 404.
var ErrNotFound = errors.New("honco: not found")

// Store owns every table this plugin creates.
//
// It uses the plugin API's master DB handle, which is the *same*
// PostgreSQL database Mattermost itself uses (honcochat). That is
// deliberate: the plugin adds tables, it does not add a database. Every
// table it creates is prefixed `honco_` so it is unmistakably ours and can
// never be confused with a Mattermost table.
//
// Nothing here touches a Mattermost-owned table, in any statement.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// migration is one additive schema step. Steps are applied in order and
// recorded, so a restart never re-runs one.
type migration struct {
	version int
	name    string
	stmts   []string
}

// migrations is append-only. Never edit a shipped entry -- add a new one.
// Every statement must be additive: this runs against a live database
// holding real data, and a destructive step here would be unrecoverable.
var migrations = []migration{
	{
		version: 1,
		name:    "create_honco_tasks",
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS honco_tasks (
				id           VARCHAR(26)  PRIMARY KEY,
				team_id      VARCHAR(26)  NOT NULL,
				creator_id   VARCHAR(26)  NOT NULL,
				assignee_id  VARCHAR(26)  NOT NULL DEFAULT '',
				title        VARCHAR(256) NOT NULL,
				description  TEXT         NOT NULL DEFAULT '',
				status       VARCHAR(32)  NOT NULL DEFAULT 'todo',
				due_at       BIGINT       NOT NULL DEFAULT 0,
				created_at   BIGINT       NOT NULL,
				updated_at   BIGINT       NOT NULL,
				deleted_at   BIGINT       NOT NULL DEFAULT 0
			)`,
			// The list endpoint always filters by team first, then usually
			// by status -- this is the index that serves it.
			`CREATE INDEX IF NOT EXISTS idx_honco_tasks_team_status
				ON honco_tasks (team_id, status)`,
			// "my tasks" across a team.
			`CREATE INDEX IF NOT EXISTS idx_honco_tasks_assignee
				ON honco_tasks (assignee_id, status)`,
			// Due-date ordering and the due_before filter.
			`CREATE INDEX IF NOT EXISTS idx_honco_tasks_due
				ON honco_tasks (team_id, due_at)`,
		},
	},
	{
		version: 2,
		name:    "create_honco_meetings_and_recordings",
		stmts: []string{
			// A meeting is the bridge between a Jitsi room and a Honco
			// Chat channel. Without it a recording callback has no way to
			// know where the file belongs -- and, more importantly, no way
			// to be authorized. An unregistered room is rejected.
			`CREATE TABLE IF NOT EXISTS honco_meetings (
				id          VARCHAR(26)  PRIMARY KEY,
				room_name   VARCHAR(255) NOT NULL,
				channel_id  VARCHAR(26)  NOT NULL,
				creator_id  VARCHAR(26)  NOT NULL DEFAULT '',
				topic       VARCHAR(255) NOT NULL DEFAULT '',
				created_at  BIGINT       NOT NULL
			)`,
			// One row per room: the room name is what Jibri reports back,
			// so it has to resolve to exactly one meeting.
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_honco_meetings_room
				ON honco_meetings (room_name)`,
			`CREATE INDEX IF NOT EXISTS idx_honco_meetings_channel
				ON honco_meetings (channel_id, created_at)`,

			`CREATE TABLE IF NOT EXISTS honco_recordings (
				id            VARCHAR(26)  PRIMARY KEY,
				meeting_id    VARCHAR(26)  NOT NULL,
				room_name     VARCHAR(255) NOT NULL,
				channel_id    VARCHAR(26)  NOT NULL,
				status        VARCHAR(32)  NOT NULL,
				file_id       VARCHAR(26)  NOT NULL DEFAULT '',
				file_name     VARCHAR(255) NOT NULL DEFAULT '',
				size_bytes    BIGINT       NOT NULL DEFAULT 0,
				duration_secs BIGINT       NOT NULL DEFAULT 0,
				error_message VARCHAR(1024) NOT NULL DEFAULT '',
				created_at    BIGINT       NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS idx_honco_recordings_meeting
				ON honco_recordings (meeting_id, created_at)`,
			`CREATE INDEX IF NOT EXISTS idx_honco_recordings_channel
				ON honco_recordings (channel_id, created_at)`,
		},
	},
	{
		version: 3,
		name:    "create_honco_notifications",
		stmts: []string{
			// The ledger of what has already been sent. Its whole purpose
			// is the UNIQUE constraint on dedupe_key: a send is claimed by
			// an INSERT ... ON CONFLICT DO NOTHING, so two workers racing
			// on the same event produce exactly one notification. Without
			// this, the due-date scanner would re-notify on every tick.
			`CREATE TABLE IF NOT EXISTS honco_notifications (
				id           VARCHAR(26)  PRIMARY KEY,
				kind         VARCHAR(64)  NOT NULL,
				subject_id   VARCHAR(26)  NOT NULL DEFAULT '',
				recipient_id VARCHAR(26)  NOT NULL DEFAULT '',
				dedupe_key   VARCHAR(255) NOT NULL,
				created_at   BIGINT       NOT NULL
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_honco_notifications_dedupe
				ON honco_notifications (dedupe_key)`,
			`CREATE INDEX IF NOT EXISTS idx_honco_notifications_kind
				ON honco_notifications (kind, created_at)`,
		},
	},
	{
		version: 4,
		name:    "create_honco_meeting_summaries",
		stmts: []string{
			// Meeting Intelligence. One row per meeting: the unique index
			// on meeting_id is what makes a retried "generate" request
			// update the existing summary in place instead of piling up
			// duplicates.
			//
			// team_id is denormalised from the channel at generation time
			// so a later read can be refused cross-team cheaply, without
			// having to trust the meeting row alone.
			`CREATE TABLE IF NOT EXISTS honco_meeting_summaries (
				id            VARCHAR(26)   PRIMARY KEY,
				meeting_id    VARCHAR(26)   NOT NULL,
				channel_id    VARCHAR(26)   NOT NULL,
				team_id       VARCHAR(26)   NOT NULL DEFAULT '',
				requester_id  VARCHAR(26)   NOT NULL DEFAULT '',
				status        VARCHAR(32)   NOT NULL,
				summary       TEXT          NOT NULL DEFAULT '',
				key_points    TEXT          NOT NULL DEFAULT '',
				decisions     TEXT          NOT NULL DEFAULT '',
				action_items  TEXT          NOT NULL DEFAULT '',
				participants  TEXT          NOT NULL DEFAULT '',
				raw_output    TEXT          NOT NULL DEFAULT '',
				error_message VARCHAR(1024) NOT NULL DEFAULT '',
				message_count INTEGER       NOT NULL DEFAULT 0,
				window_start  BIGINT        NOT NULL DEFAULT 0,
				window_end    BIGINT        NOT NULL DEFAULT 0,
				created_at    BIGINT        NOT NULL,
				updated_at    BIGINT        NOT NULL
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_honco_meeting_summaries_meeting
				ON honco_meeting_summaries (meeting_id)`,
			`CREATE INDEX IF NOT EXISTS idx_honco_meeting_summaries_channel
				ON honco_meeting_summaries (channel_id, created_at)`,
		},
	},
}

// Migrate brings the plugin's own schema up to date.
//
// It keeps its version in its own table (honco_schema_migrations) and does
// not touch Mattermost's db_migrations -- the two schemas version
// independently, which is what lets Mattermost be upgraded without this
// plugin's history getting in the way, and vice versa.
func (s *Store) Migrate() error {
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS honco_schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       VARCHAR(128) NOT NULL,
			applied_at BIGINT       NOT NULL
		)`); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	for _, m := range migrations {
		var exists int
		err := s.db.QueryRow(`SELECT count(*) FROM honco_schema_migrations WHERE version = $1`, m.version).Scan(&exists)
		if err != nil {
			return fmt.Errorf("check migration %d: %w", m.version, err)
		}
		if exists > 0 {
			continue
		}

		// Each migration is one transaction: either the whole step lands
		// or none of it does, so a failure can never leave a half-created
		// schema that the next start would trip over.
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}
		for _, stmt := range m.stmts {
			if _, err := tx.Exec(stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
			}
		}
		if _, err := tx.Exec(
			`INSERT INTO honco_schema_migrations (version, name, applied_at) VALUES ($1, $2, $3)`,
			m.version, m.name, nowMillis(),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}
	return nil
}

const taskColumns = `id, team_id, creator_id, assignee_id, title, description,
	status, due_at, created_at, updated_at, deleted_at`

func scanTask(row interface{ Scan(...any) error }) (*Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.TeamID, &t.CreatorID, &t.AssigneeID, &t.Title,
		&t.Description, &t.Status, &t.DueAt, &t.CreatedAt, &t.UpdatedAt, &t.DeletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) CreateTask(t *Task) error {
	_, err := s.db.Exec(`
		INSERT INTO honco_tasks (`+taskColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		t.ID, t.TeamID, t.CreatorID, t.AssigneeID, t.Title, t.Description,
		string(t.Status), t.DueAt, t.CreatedAt, t.UpdatedAt, t.DeletedAt)
	return err
}

// GetTask returns a live task by ID. A soft-deleted row is reported as
// not found -- callers must never be able to read a deleted task back.
//
// Note this does NOT filter by team: the caller is responsible for the
// membership check, and does it against the team_id on the returned row.
// That ordering matters and is why the API layer never calls this without
// immediately checking membership.
func (s *Store) GetTask(id string) (*Task, error) {
	row := s.db.QueryRow(`SELECT `+taskColumns+` FROM honco_tasks WHERE id = $1 AND deleted_at = 0`, id)
	return scanTask(row)
}

// ListTasks returns live tasks for one team, newest first.
//
// teamID is a separate required argument rather than part of the filter
// so it cannot be accidentally omitted -- every query this store issues
// for a list is team-scoped by construction.
func (s *Store) ListTasks(teamID string, f TaskFilter) ([]*Task, error) {
	f.Clamp()

	var (
		where = []string{"team_id = $1", "deleted_at = 0"}
		args  = []any{teamID}
	)
	add := func(clause string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if f.Status != "" {
		add("status = $%d", string(f.Status))
	}
	if f.AssigneeID != "" {
		add("assignee_id = $%d", f.AssigneeID)
	}
	if f.CreatorID != "" {
		add("creator_id = $%d", f.CreatorID)
	}
	if f.DueBefore > 0 {
		add("(due_at > 0 AND due_at <= $%d)", f.DueBefore)
	}

	args = append(args, f.Limit, f.Offset)
	q := `SELECT ` + taskColumns + ` FROM honco_tasks WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := []*Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// UpdateTask writes the mutable fields. id and team_id are deliberately
// absent from the SET list: a task cannot be moved between teams, which
// would otherwise be a way to smuggle a row past the membership check.
func (s *Store) UpdateTask(t *Task) error {
	res, err := s.db.Exec(`
		UPDATE honco_tasks
		SET assignee_id = $1, title = $2, description = $3, status = $4,
		    due_at = $5, updated_at = $6
		WHERE id = $7 AND deleted_at = 0`,
		t.AssigneeID, t.Title, t.Description, string(t.Status), t.DueAt, t.UpdatedAt, t.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SoftDeleteTask marks a task deleted, keeping the row. Nothing in this
// plugin ever issues a DELETE against honco_tasks.
func (s *Store) SoftDeleteTask(id string, at int64) error {
	res, err := s.db.Exec(
		`UPDATE honco_tasks SET deleted_at = $1, updated_at = $1 WHERE id = $2 AND deleted_at = 0`, at, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountTasksByStatus backs the admin overview: aggregate counts only,
// never task content.
func (s *Store) CountTasksByStatus(teamID string) (map[string]int, error) {
	q := `SELECT status, count(*) FROM honco_tasks WHERE deleted_at = 0`
	args := []any{}
	if teamID != "" {
		q += ` AND team_id = $1`
		args = append(args, teamID)
	}
	q += ` GROUP BY status`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}

// --- Notification ledger ---------------------------------------------------

// NotificationRecord is one row of "we already told someone this".
type NotificationRecord struct {
	ID          string
	Kind        string
	SubjectID   string
	RecipientID string
	DedupeKey   string
	CreatedAt   int64
}

// ClaimNotification returns true if THIS caller may send.
//
// The whole de-duplication guarantee lives in one statement: the unique
// index on dedupe_key means a second attempt conflicts and inserts
// nothing, so RowsAffected distinguishes "I claimed it" from "someone
// already did" without a read-then-write race.
func (s *Store) ClaimNotification(n *NotificationRecord) (bool, error) {
	res, err := s.db.Exec(`
		INSERT INTO honco_notifications (id, kind, subject_id, recipient_id, dedupe_key, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (dedupe_key) DO NOTHING`,
		n.ID, n.Kind, n.SubjectID, n.RecipientID, n.DedupeKey, n.CreatedAt)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// ListTasksDueBefore returns live, unfinished tasks whose due date falls
// at or before the horizon. Used only by the due scanner.
func (s *Store) ListTasksDueBefore(horizon int64) ([]*Task, error) {
	rows, err := s.db.Query(`
		SELECT `+taskColumns+` FROM honco_tasks
		WHERE deleted_at = 0 AND status <> 'done' AND due_at > 0 AND due_at <= $1
		ORDER BY due_at ASC LIMIT 500`, horizon)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CountNotificationsByKind backs the admin overview later; aggregate only.
func (s *Store) CountNotificationsByKind() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT kind, count(*) FROM honco_notifications GROUP BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}
