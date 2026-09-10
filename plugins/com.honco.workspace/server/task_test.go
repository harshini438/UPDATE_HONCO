package main

import (
	"strings"
	"testing"
)

func validTask() *Task {
	return &Task{
		TeamID:    "t1111111111111111111111111",
		CreatorID: "u1111111111111111111111111",
		Title:     "Ship the recording callback",
		Status:    StatusTodo,
	}
}

func TestTaskValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Task)
		wantErr string
	}{
		{"valid", func(*Task) {}, ""},
		{"missing team", func(x *Task) { x.TeamID = "" }, "team_id"},
		{"missing creator", func(x *Task) { x.CreatorID = "" }, "creator_id"},
		{"missing title", func(x *Task) { x.Title = "" }, "title is required"},
		{"whitespace-only title", func(x *Task) { x.Title = "   " }, "title is required"},
		{"title too long", func(x *Task) { x.Title = strings.Repeat("a", MaxTitleLength+1) }, "too long"},
		{"description too long", func(x *Task) { x.Description = strings.Repeat("a", MaxDescriptionLength+1) }, "too long"},
		{"bad status", func(x *Task) { x.Status = "nearly-done" }, "status must be"},
		{"negative due date", func(x *Task) { x.DueAt = -1 }, "due_at"},
		{"absurd due date", func(x *Task) { x.DueAt = 99999999999999 }, "due_at"},
		{"due date zero is allowed", func(x *Task) { x.DueAt = 0 }, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			task := validTask()
			c.mutate(task)
			task.Normalise()
			err := task.IsValid()

			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error = %q, want it to mention %q", err.Error(), c.wantErr)
			}
		})
	}
}

// Normalise must run before validation, or a title of spaces would be
// stored as a blank title rather than rejected.
func TestNormaliseTrimsBeforeValidation(t *testing.T) {
	task := validTask()
	task.Title = "  Fix the tunnel  "
	task.Description = "  see notes  "
	task.Normalise()

	if task.Title != "Fix the tunnel" {
		t.Errorf("title = %q, want it trimmed", task.Title)
	}
	if task.Description != "see notes" {
		t.Errorf("description = %q, want it trimmed", task.Description)
	}
}

func TestStatusValidity(t *testing.T) {
	for _, s := range []TaskStatus{StatusTodo, StatusInProgress, StatusDone} {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []TaskStatus{"", "TODO", "done ", "archived", "deleted"} {
		if TaskStatus(s).Valid() {
			t.Errorf("%q should NOT be valid", s)
		}
	}
}

// The page size cap is what stops a caller pulling the whole table.
func TestFilterClamp(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, DefaultTaskPageSize},
		{-5, DefaultTaskPageSize},
		{10, 10},
		{MaxTaskPageSize, MaxTaskPageSize},
		{MaxTaskPageSize + 1000, MaxTaskPageSize},
	}
	for _, c := range cases {
		f := TaskFilter{Limit: c.in}
		f.Clamp()
		if f.Limit != c.want {
			t.Errorf("Clamp(limit=%d) = %d, want %d", c.in, f.Limit, c.want)
		}
	}

	f := TaskFilter{Offset: -3}
	f.Clamp()
	if f.Offset != 0 {
		t.Errorf("negative offset should clamp to 0, got %d", f.Offset)
	}
}

// canMutate is the per-task permission rule. A team member who is neither
// creator nor assignee may read a task but must not change it.
func TestCanMutate(t *testing.T) {
	creator := "u1111111111111111111111111"
	assignee := "u2222222222222222222222222"
	bystander := "u3333333333333333333333333"

	task := &Task{CreatorID: creator, AssigneeID: assignee}

	if !canMutate(task, creator) {
		t.Error("creator must be able to mutate")
	}
	if !canMutate(task, assignee) {
		t.Error("assignee must be able to mutate")
	}
	if canMutate(task, bystander) {
		t.Error("an unrelated team member must NOT be able to mutate")
	}

	// An unassigned task must not be mutable by someone who simply has an
	// empty assignee ID -- guards against "" == "" matching.
	unassigned := &Task{CreatorID: creator, AssigneeID: ""}
	if canMutate(unassigned, "") {
		t.Error("empty user must never match an empty assignee")
	}
	if canMutate(unassigned, bystander) {
		t.Error("bystander must not mutate an unassigned task")
	}
}
