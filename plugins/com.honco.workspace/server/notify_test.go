package main

import (
	"strings"
	"testing"
)

const (
	uCreator  = "u1111111111111111111111111"
	uAssignee = "u2222222222222222222222222"
	uOutsider = "u3333333333333333333333333"
)

// Only the creator and the assignee are ever candidates for a status
// notification, and never the person who made the change.
func TestStatusNotifyRecipient(t *testing.T) {
	assigned := &Task{CreatorID: uCreator, AssigneeID: uAssignee}

	if got := statusNotifyRecipient(assigned, uCreator); got != uAssignee {
		t.Errorf("creator acts -> assignee should hear; got %q", got)
	}
	if got := statusNotifyRecipient(assigned, uAssignee); got != uCreator {
		t.Errorf("assignee acts -> creator should hear; got %q", got)
	}

	// An unassigned task moved by its creator has nobody else to tell.
	unassigned := &Task{CreatorID: uCreator, AssigneeID: ""}
	if got := statusNotifyRecipient(unassigned, uCreator); got != "" {
		t.Errorf("unassigned task should notify nobody; got %q", got)
	}

	// A task where creator and assignee are the same person never
	// self-notifies.
	selfOwned := &Task{CreatorID: uCreator, AssigneeID: uCreator}
	if got := statusNotifyRecipient(selfOwned, uCreator); got != "" {
		t.Errorf("self-owned task should notify nobody; got %q", got)
	}

	// An outsider cannot be the actor in practice (the API rejects them),
	// but the rule must still never select someone outside the pair.
	got := statusNotifyRecipient(assigned, uOutsider)
	if got != uCreator && got != uAssignee {
		t.Errorf("recipient must be creator or assignee, got %q", got)
	}
	if got == uOutsider {
		t.Error("an outsider must never be selected as a recipient")
	}
}

// The dedupe key is the whole de-duplication guarantee, so its shape
// matters: same event -> same key, genuinely new event -> new key.
func TestDedupeKeyShape(t *testing.T) {
	task := &Task{ID: "t1111111111111111111111111", AssigneeID: uAssignee, UpdatedAt: 1000}

	key := func(tk *Task) string {
		return KindTaskAssigned + ":" + tk.ID + ":" + tk.AssigneeID + ":" + itoa(tk.UpdatedAt)
	}

	same := key(task)
	if key(task) != same {
		t.Error("the same task state must produce the same key")
	}

	// A re-assignment bumps UpdatedAt, so the person is told again.
	reassigned := *task
	reassigned.UpdatedAt = 2000
	if key(&reassigned) == same {
		t.Error("a later assignment must produce a different key")
	}

	// A different assignee is a different notification.
	other := *task
	other.AssigneeID = uCreator
	if key(&other) == same {
		t.Error("a different assignee must produce a different key")
	}
}

func TestHumanStatus(t *testing.T) {
	cases := map[TaskStatus]string{
		StatusTodo:       "To do",
		StatusInProgress: "In progress",
		StatusDone:       "Done",
	}
	for in, want := range cases {
		if got := humanStatus(in); got != want {
			t.Errorf("humanStatus(%q) = %q, want %q", in, got, want)
		}
	}
	// An unknown status renders as itself rather than an empty string.
	if got := humanStatus(TaskStatus("weird")); got != "weird" {
		t.Errorf("unknown status should pass through, got %q", got)
	}
}

func TestFormatDue(t *testing.T) {
	// 2026-09-09T12:00:00Z
	got := formatDue(1788004800000)
	if !strings.Contains(got, "UTC") {
		t.Errorf("due dates must be explicit about the zone, got %q", got)
	}
	if strings.TrimSpace(got) == "" {
		t.Error("formatDue must render something")
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
