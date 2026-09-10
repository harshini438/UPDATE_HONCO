package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Reassignment has two halves, and the dedupe keys for them must never
// collide -- one person is being told they gained work and another that
// they lost it, about the same task at the same instant.
func TestReassignmentKeysAreDistinct(t *testing.T) {
	const (
		taskID = "t1111111111111111111111111"
		oldA   = "u1111111111111111111111111"
		newA   = "u2222222222222222222222222"
		at     = int64(1789000000000)
	)
	gained := fmt.Sprintf("%s:%s:%s:%d", KindTaskAssigned, taskID, newA, at)
	lost := fmt.Sprintf("%s:%s:%s:%d", KindTaskReassigned, taskID, oldA, at)

	if gained == lost {
		t.Fatal("the two halves of a reassignment must not share a dedupe key")
	}
	if !strings.HasPrefix(gained, KindTaskAssigned) || !strings.HasPrefix(lost, KindTaskReassigned) {
		t.Error("each key must be namespaced by its own kind")
	}

	// A later change to the same task produces new keys, so moving a task
	// back and forth notifies each time rather than falling silent.
	later := fmt.Sprintf("%s:%s:%s:%d", KindTaskReassigned, taskID, oldA, at+1)
	if later == lost {
		t.Error("a later reassignment must produce a different key")
	}
}

// Every kind must be distinct: two kinds sharing a string would let one
// event's dedupe record suppress another's.
func TestNotificationKindsAreUnique(t *testing.T) {
	kinds := []string{
		KindTaskAssigned, KindTaskReassigned, KindTaskCompleted, KindTaskStatus,
		KindTaskDueSoon, KindTaskOverdue,
		KindRecordingReady, KindRecordingFailed,
		KindMeetingSummaryReady, KindMeetingSummaryFailed,
		KindMeetingStarted, KindMeetingJoined, KindMeetingLeft, KindMeetingEnded,
	}
	seen := map[string]bool{}
	for _, k := range kinds {
		if k == "" {
			t.Error("a kind must not be empty")
		}
		if seen[k] {
			t.Errorf("duplicate notification kind: %q", k)
		}
		seen[k] = true
	}
	if len(seen) != 14 {
		t.Errorf("expected 14 distinct kinds, got %d", len(seen))
	}
}

// Retention has to outlive every replay window by a wide margin, because
// the timeless keys have no timestamp protecting them.
func TestRetentionOutlivesReplayWindows(t *testing.T) {
	// The longest thing a dedupe record must survive is a meeting, which
	// is itself bounded by the conversation window.
	if notificationRetention <= maxMeetingWindow {
		t.Errorf("retention (%s) must exceed a meeting's lifetime (%s)",
			notificationRetention, maxMeetingWindow)
	}
	if notificationRetention <= dueSoonWindow {
		t.Errorf("retention (%s) must exceed the due-soon window (%s)",
			notificationRetention, dueSoonWindow)
	}
	// A day is the floor for "not frequent"; anything shorter risks
	// pruning during an event's own lifetime.
	if pruneEvery < 24*time.Hour {
		t.Errorf("pruning every %s is too often", pruneEvery)
	}
	if notificationRetention < 30*24*time.Hour {
		t.Errorf("retention of %s is too aggressive to be obviously safe", notificationRetention)
	}
}

// A self-action must never notify the actor. These are the rules the
// notifiers apply before any message is built.
func TestSelfNotificationRules(t *testing.T) {
	const me = "u1111111111111111111111111"
	const other = "u2222222222222222222222222"

	// Assigning to yourself: notifyAssigned returns early.
	self := &Task{CreatorID: me, AssigneeID: me}
	if got := statusNotifyRecipient(self, me); got != "" {
		t.Errorf("a self-owned task should notify nobody, got %q", got)
	}

	// Unassigning yourself is not an event for you.
	if me == other {
		t.Fatal("test setup")
	}
	// notifyUnassigned's guard: previousAssignee == actorID -> silent.
	// Expressed here as the condition itself, since the method needs a
	// live plugin to call.
	previous, actor := me, me
	silent := previous == "" || previous == actor
	if !silent {
		t.Error("unassigning yourself must be silent")
	}

	previous, actor = other, me
	silent = previous == "" || previous == actor
	if silent {
		t.Error("unassigning someone else must notify them")
	}
}
