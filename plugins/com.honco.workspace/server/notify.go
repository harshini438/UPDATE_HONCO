package main

import (
	"fmt"
	"strings"
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

	KindMeetingStarted = "meeting_started"
	KindMeetingJoined  = "meeting_joined"
	KindMeetingLeft    = "meeting_left"
	KindMeetingEnded   = "meeting_ended"

	KindTaskReassigned = "task_reassigned"
)

// dueSoonWindow: how far ahead of the due date the "due soon" nudge goes
// out. One notice, then one more if it actually goes overdue -- two per
// task in its lifetime, which is the line between useful and nagging.
const dueSoonWindow = 24 * time.Hour

// dueScanInterval is how often the plugin looks for tasks coming due.
// Deliberately coarse: due dates are day-grained, so a tighter loop would
// only add database traffic.
const dueScanInterval = 10 * time.Minute

const (
	// How long a dedupe record is kept.
	//
	// This is not a storage decision -- the table is tiny -- it is a
	// correctness one. A record must outlive any chance of its event
	// recurring, and the timeless keys (recording_ready:<id>,
	// meeting_started:<id>) have no timestamp to protect them. 90 days is
	// orders of magnitude beyond a Jibri retry or a meeting's lifetime,
	// so nothing pruned can come back.
	notificationRetention = 90 * 24 * time.Hour

	// Pruning is hygiene, not urgent work: once a day is plenty, and it
	// piggybacks on the scanner rather than adding another goroutine.
	pruneEvery = 24 * time.Hour
)

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

// notifyUnassigned tells someone a task is no longer theirs.
//
// Without this a reassignment is silent to the person losing the work: the
// new assignee is told, the old one keeps believing they own it, and two
// people work the same task or nobody does. The message deliberately does
// not name the new assignee -- who a task went to is not information the
// previous holder needs, and it keeps the notice useful without turning it
// into gossip.
//
// Nobody is told they unassigned themselves, and an unassignment from
// nobody is not an event.
func (p *Plugin) notifyUnassigned(task *Task, previousAssignee, actorID string) {
	if previousAssignee == "" || previousAssignee == actorID {
		return
	}
	// Keyed on the task and the moment of the change, so re-assigning back
	// and forth notifies each time while a retried write does not.
	key := fmt.Sprintf("%s:%s:%s:%d", KindTaskReassigned, task.ID, previousAssignee, task.UpdatedAt)
	p.dmOnce(KindTaskReassigned, task.ID, previousAssignee, key, fmt.Sprintf(
		"**%s** reassigned **%s** — it is no longer assigned to you.",
		p.username(actorID), task.Title))
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
		msg = fmt.Sprintf("Meeting summary ready for **%s**.", title)
		// A link straight to the meeting card beats telling someone where
		// to click. Only added when the card actually exists -- a dead
		// link is worse than a sentence.
		if link := p.permalink(meeting.ChannelID, meeting.PostID); link != "" {
			msg += fmt.Sprintf(" [Open the meeting](%s) and choose _Meeting Summary_.", link)
		} else {
			msg += " Open the channel and choose _Meeting Intelligence_ to read it."
		}
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

	// Deliberately not pruned at startup: a crash-loop would then prune on
	// every restart, and the one thing pruning must never be is frequent.
	lastPrune := time.Now()

	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.scanDueTasks()
			if time.Since(lastPrune) >= pruneEvery {
				lastPrune = time.Now()
				p.pruneNotificationLedger()
			}
		}
	}
}

// pruneNotificationLedger drops dedupe records that can no longer guard
// anything. Failure is logged and ignored: a ledger that grows is a
// nuisance, while a half-pruned one would be a correctness problem, so
// this never retries within a cycle.
func (p *Plugin) pruneNotificationLedger() {
	cutoff := nowMillis() - notificationRetention.Milliseconds()
	removed, err := p.store.PruneNotifications(cutoff)
	if err != nil {
		p.client.Log.Warn("honco: notification prune failed", "err", err.Error())
		return
	}
	if removed > 0 {
		p.client.Log.Info("honco: pruned old notification records", "removed", removed)
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

// permalink builds a clickable link to a post, or "" if one cannot be
// built.
//
// Mattermost permalinks are <SiteURL>/<team-name>/pl/<post-id>, so this
// needs the channel's team and the server's own SiteURL. Any missing piece
// returns empty rather than a half-formed URL: a notification with a dead
// link is worse than one that simply says where to look.
//
// This adds no authorization of its own and does not need to. The link is
// only ever sent to someone whose access was already proved, and following
// it lands on Mattermost, which checks membership again before rendering
// anything.
func (p *Plugin) permalink(channelID, postID string) string {
	if channelID == "" || postID == "" {
		return ""
	}
	cfg := p.API.GetConfig()
	if cfg == nil || cfg.ServiceSettings.SiteURL == nil || *cfg.ServiceSettings.SiteURL == "" {
		return ""
	}
	channel, err := p.client.Channel.Get(channelID)
	if err != nil || channel == nil || channel.TeamId == "" {
		return ""
	}
	team, err := p.client.Team.Get(channel.TeamId)
	if err != nil || team == nil {
		return ""
	}
	return strings.TrimRight(*cfg.ServiceSettings.SiteURL, "/") + "/" + team.Name + "/pl/" + postID
}

// PruneNotifications removes dedupe records old enough that the event they
// guard can no longer recur.
//
// The ledger is a de-duplication guard, not an audit log, so a row only has
// to outlive its event's replay window. Those windows are short: a Jibri
// callback retries within minutes, a meeting is bounded by its own
// lifetime, and every task key embeds the update timestamp that produced
// it, so a pruned task key can never be regenerated identically.
//
// The dangerous keys are the timeless ones -- recording_ready:<id>,
// meeting_started:<id>, meeting_joined:<id>:<names> -- which would re-fire
// if pruned while their subject were somehow still live. That is why the
// cutoff is deliberately far longer than any meeting or callback could
// last, and why meetings that are still scheduled or active are excluded
// outright rather than trusted to be old enough.
func (s *Store) PruneNotifications(olderThan int64) (int64, error) {
	res, err := s.db.Exec(`
		DELETE FROM honco_notifications n
		WHERE n.created_at < $1
		  AND NOT EXISTS (
		      SELECT 1 FROM honco_meetings m
		      WHERE m.id = n.subject_id
		        AND m.status IN ('scheduled','active')
		  )`, olderThan)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
