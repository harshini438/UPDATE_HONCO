package main

import (
	"fmt"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Notification kinds. These are the Honco-specific events; everything
// about *delivering* them (desktop, email, mobile, do-not-disturb, per-
// channel muting) is Mattermost's, because every notification here is an
// ordinary Mattermost post.
const (
	KindTaskAssigned    = "task_assigned"
	KindTaskCompleted   = "task_completed"
	KindTaskStatus      = "task_status"
	KindTaskDueSoon     = "task_due_soon"
	KindTaskOverdue     = "task_overdue"
	KindRecordingReady  = "recording_ready"
	KindRecordingFailed = "recording_failed"

	KindMeetingSummaryReady  = "meeting_summary_ready"
	KindMeetingSummaryFailed = "meeting_summary_failed"
)

// dueSoonWindow: how far ahead of the due date the "due soon" nudge goes
// out. One notice, then one more if it actually goes overdue -- two per
// task in its lifetime, which is the line between useful and nagging.
const dueSoonWindow = 24 * time.Hour

// dueScanInterval is how often the plugin looks for tasks coming due.
// Deliberately coarse: due dates are day-grained, so a tighter loop would
// only add database traffic.
const dueScanInterval = 10 * time.Minute

// claim records an intent to notify and reports whether this caller won.
//
// It is the de-duplication primitive for every notification in this
// plugin. The UNIQUE index on dedupe_key means the INSERT either lands
// (this caller sends) or conflicts (someone already did). That holds
// across restarts, across ticks of the due scanner, and across two server
// nodes, because the database is the arbiter -- not in-memory state.
func (p *Plugin) claim(kind, subjectID, recipientID, dedupeKey string) bool {
	claimed, err := p.store.ClaimNotification(&NotificationRecord{
		ID:          model.NewId(),
		Kind:        kind,
		SubjectID:   subjectID,
		RecipientID: recipientID,
		DedupeKey:   dedupeKey,
		CreatedAt:   nowMillis(),
	})
	if err != nil {
		// A ledger failure must not silently swallow a notification, but
		// it also must not double-send. Log and skip: the safer of the
		// two, since the event usually recurs (the scanner runs again).
		p.client.Log.Warn("honco: notification ledger unavailable", "kind", kind, "err", err.Error())
		return false
	}
	return claimed
}

// dmOnce sends a direct message from the Honco bot, at most once per
// dedupe key.
//
// A DM is the right channel for anything task-shaped: the recipient is
// always the assignee or the creator, both of whom are members of the
// task's team by construction, so no content reaches anyone not already
// entitled to it. Task text is never posted to a channel.
func (p *Plugin) dmOnce(kind, subjectID, userID, dedupeKey, message string) bool {
	if p.botID == "" || userID == "" {
		return false
	}
	if !p.claim(kind, subjectID, userID, dedupeKey) {
		return false
	}
	if err := p.client.Post.DM(p.botID, userID, &model.Post{Message: message}); err != nil {
		p.client.Log.Warn("honco: could not deliver notification", "kind", kind, "err", err.Error())
		return false
	}
	return true
}

// --- Task notifications ----------------------------------------------------

// notifyAssigned tells someone work is theirs. Assigning to yourself is
// deliberately silent -- you already know.
func (p *Plugin) notifyAssigned(task *Task, actorID string) {
	if task.AssigneeID == "" || task.AssigneeID == actorID {
		return
	}
	msg := fmt.Sprintf("**%s** assigned you a task: **%s**", p.username(actorID), task.Title)
	if task.DueAt > 0 {
		msg += fmt.Sprintf("\nDue: %s", formatDue(task.DueAt))
	}
	// UpdatedAt is part of the key so a genuine re-assignment notifies
	// again, while a repeated or retried write does not.
	key := fmt.Sprintf("%s:%s:%s:%d", KindTaskAssigned, task.ID, task.AssigneeID, task.UpdatedAt)
	p.dmOnce(KindTaskAssigned, task.ID, task.AssigneeID, key, msg)
}

// notifyCompleted tells the creator their task is done -- unless they
// completed it themselves.
func (p *Plugin) notifyCompleted(task *Task, actorID string) {
	if task.CreatorID == "" || task.CreatorID == actorID {
		return
	}
	key := fmt.Sprintf("%s:%s:%d", KindTaskCompleted, task.ID, task.UpdatedAt)
	p.dmOnce(KindTaskCompleted, task.ID, task.CreatorID,
		key, fmt.Sprintf("**%s** completed the task: **%s**", p.username(actorID), task.Title))
}

// notifyStatusChanged covers the non-completion transitions (todo <->
// in_progress). It goes to the other party only: whoever did it does not
// need telling, and nobody outside creator/assignee is involved at all.
func (p *Plugin) notifyStatusChanged(task *Task, actorID string, from, to TaskStatus) {
	if from == to || to == StatusDone {
		return // completion has its own, richer notification
	}
	recipient := statusNotifyRecipient(task, actorID)
	if recipient == "" {
		return
	}
	key := fmt.Sprintf("%s:%s:%s:%d", KindTaskStatus, task.ID, to, task.UpdatedAt)
	p.dmOnce(KindTaskStatus, task.ID, recipient, key, fmt.Sprintf(
		"**%s** moved **%s** to _%s_", p.username(actorID), task.Title, humanStatus(to)))
}

// statusNotifyRecipient decides who, if anyone, hears about a status
// move: the *other* party to the task.
//
// Only the creator and the assignee are ever candidates -- both are
// members of the task's team by construction, so a task's title can never
// reach someone outside it. The actor is never told about their own
// action, and an empty result means nobody is notified.
func statusNotifyRecipient(task *Task, actorID string) string {
	recipient := task.CreatorID
	if actorID == task.CreatorID {
		recipient = task.AssigneeID
	}
	if recipient == "" || recipient == actorID {
		return ""
	}
	return recipient
}

// --- Meeting Intelligence notifications ------------------------------------

// notifyMeetingSummary tells the person who asked for a summary that it
// is done, or that it failed.
//
// It goes to the requester by DM and to nobody else. They are the one
// waiting on it, and they are guaranteed to be authorized -- channel
// membership was proved before the generation started. The message names
// the meeting but carries none of the summary text, so nothing about the
// conversation is duplicated outside the channel it came from.
//
// A `pending` row is not a result and is never announced.
func (p *Plugin) notifyMeetingSummary(meeting *Meeting, s *MeetingSummary) {
	if s.RequesterID == "" || s.Status == SummaryPending {
		return
	}

	title := meeting.Topic
	if title == "" {
		title = meeting.RoomName
	}

	kind, msg := KindMeetingSummaryReady, ""
	switch s.Status {
	case SummaryReady:
		msg = fmt.Sprintf("Meeting summary ready for **%s**. Open the channel and choose _Meeting Intelligence_ to read it.", title)
	case SummaryEmpty:
		msg = fmt.Sprintf("No summary for **%s**: nothing was posted in the channel during the meeting.", title)
	default:
		kind = KindMeetingSummaryFailed
		msg = fmt.Sprintf("Meeting summary for **%s** could not be generated. %s", title, s.ErrorMessage)
	}

	// UpdatedAt is in the key so a deliberate regeneration is announced
	// again, while a retried write of the same result is not.
	key := fmt.Sprintf("%s:%s:%d", kind, s.MeetingID, s.UpdatedAt)
	p.dmOnce(kind, s.MeetingID, s.RequesterID, key, msg)
}

// --- Due-date scanning -----------------------------------------------------

// runDueScanner is the only piece of this plugin that notifies without a
// user action behind it. It wakes periodically, finds tasks whose due date
// has arrived (or passed) and which are not done, and nudges the person
// responsible.
//
// Every send goes through the ledger, so a task produces at most one
// "due soon" and one "overdue" for a given due date no matter how many
// times the scanner runs.
func (p *Plugin) runDueScanner() {
	ticker := time.NewTicker(dueScanInterval)
	defer ticker.Stop()

	p.scanDueTasks() // once at startup, so a restart is not a blind spot
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.scanDueTasks()
		}
	}
}

func (p *Plugin) scanDueTasks() {
	now := nowMillis()
	horizon := now + dueSoonWindow.Milliseconds()

	tasks, err := p.store.ListTasksDueBefore(horizon)
	if err != nil {
		p.client.Log.Warn("honco: due-task scan failed", "err", err.Error())
		return
	}

	for _, t := range tasks {
		// Only the assignee is nudged. An unassigned task has nobody to
		// chase, and chasing the creator for work they did not take on
		// would be noise.
		if t.AssigneeID == "" {
			continue
		}
		overdue := t.DueAt <= now
		kind, key, msg := KindTaskDueSoon,
			fmt.Sprintf("%s:%s:%d", KindTaskDueSoon, t.ID, t.DueAt),
			fmt.Sprintf("Reminder: **%s** is due %s", t.Title, formatDue(t.DueAt))
		if overdue {
			kind = KindTaskOverdue
			key = fmt.Sprintf("%s:%s:%d", KindTaskOverdue, t.ID, t.DueAt)
			msg = fmt.Sprintf("**%s** is overdue (was due %s)", t.Title, formatDue(t.DueAt))
		}
		p.dmOnce(kind, t.ID, t.AssigneeID, key, msg)
	}
}

// --- helpers ---------------------------------------------------------------

func formatDue(ms int64) string {
	return model.GetTimeForMillis(ms).UTC().Format("2 Jan 2006 15:04 UTC")
}

func humanStatus(s TaskStatus) string {
	switch s {
	case StatusTodo:
		return "To do"
	case StatusInProgress:
		return "In progress"
	case StatusDone:
		return "Done"
	}
	return string(s)
}

func (p *Plugin) username(userID string) string {
	user, err := p.client.User.Get(userID)
	if err != nil || user == nil {
		return "Someone"
	}
	if user.Nickname != "" {
		return user.Nickname
	}
	return user.Username
}
