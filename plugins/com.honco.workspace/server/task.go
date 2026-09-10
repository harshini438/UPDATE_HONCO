package main

import (
	"errors"
	"strings"
	"time"
)

// TaskStatus is the closed set of states a task may be in. Anything else
// is rejected at the API boundary -- the column is plain text, so this is
// the only thing standing between a typo and a permanently unfilterable
// row.
type TaskStatus string

const (
	StatusTodo       TaskStatus = "todo"
	StatusInProgress TaskStatus = "in_progress"
	StatusDone       TaskStatus = "done"
)

func (s TaskStatus) Valid() bool {
	switch s {
	case StatusTodo, StatusInProgress, StatusDone:
		return true
	}
	return false
}

// Field limits. Titles are indexed and shown in lists; descriptions are
// free text. Both are bounded so a single request cannot store an
// unbounded blob.
const (
	MaxTitleLength       = 256
	MaxDescriptionLength = 4096
)

// Task is a team-scoped unit of work.
//
// TeamID is the authorization anchor: every read and write is filtered by
// the caller's membership of that team, never by task ID alone. AssigneeID
// is optional (empty means unassigned) but when set must belong to the
// same team -- that is what stops a task being handed to someone who
// cannot see it.
//
// Times are Unix milliseconds, matching Mattermost's own convention
// (model.GetMillis) so the two never need converting between each other.
// DeletedAt of 0 means "live"; anything else is a soft delete.
type Task struct {
	ID          string     `json:"id"`
	TeamID      string     `json:"team_id"`
	CreatorID   string     `json:"creator_id"`
	AssigneeID  string     `json:"assignee_id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      TaskStatus `json:"status"`
	DueAt       int64      `json:"due_at"`
	CreatedAt   int64      `json:"created_at"`
	UpdatedAt   int64      `json:"updated_at"`
	DeletedAt   int64      `json:"deleted_at"`
}

// dueAtCeiling rejects absurd timestamps (year ~5138) that usually mean a
// client sent seconds where milliseconds were expected, or garbage.
var dueAtCeiling = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

// IsValid checks everything that does not require a database or the
// Mattermost API. Team/assignee membership is checked separately, in the
// API layer, because it needs both.
func (t *Task) IsValid() error {
	if t.TeamID == "" {
		return errors.New("team_id is required")
	}
	if t.CreatorID == "" {
		return errors.New("creator_id is required")
	}

	title := strings.TrimSpace(t.Title)
	if title == "" {
		return errors.New("title is required")
	}
	if len(title) > MaxTitleLength {
		return errors.New("title is too long")
	}
	if len(t.Description) > MaxDescriptionLength {
		return errors.New("description is too long")
	}
	if !t.Status.Valid() {
		return errors.New("status must be one of todo, in_progress, done")
	}
	if t.DueAt < 0 || (t.DueAt > 0 && t.DueAt > dueAtCeiling) {
		return errors.New("due_at is out of range")
	}
	return nil
}

// Normalise trims the user-supplied text fields. Called before validation
// so that a title of only whitespace is rejected rather than stored.
func (t *Task) Normalise() {
	t.Title = strings.TrimSpace(t.Title)
	t.Description = strings.TrimSpace(t.Description)
}

// TaskFilter is the set of narrowing options the list endpoint accepts.
// TeamID is not part of it because it is never optional -- it is passed
// separately and always applied.
type TaskFilter struct {
	Status     TaskStatus
	AssigneeID string
	CreatorID  string
	DueBefore  int64
	Limit      int
	Offset     int
}

const (
	DefaultTaskPageSize = 60
	MaxTaskPageSize     = 200
)

// Clamp keeps a caller from asking for the whole table in one request.
func (f *TaskFilter) Clamp() {
	if f.Limit <= 0 {
		f.Limit = DefaultTaskPageSize
	}
	if f.Limit > MaxTaskPageSize {
		f.Limit = MaxTaskPageSize
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
}
