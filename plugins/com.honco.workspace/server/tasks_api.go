package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
)

// createTaskRequest is what a client may send. Note what is absent:
// creator_id, created_at, updated_at, deleted_at and id are all set by the
// server. A client cannot forge authorship or resurrect a deleted task by
// sending those fields -- they are simply not read.
type createTaskRequest struct {
	TeamID      string `json:"team_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	AssigneeID  string `json:"assignee_id"`
	Status      string `json:"status"`
	DueAt       int64  `json:"due_at"`
}

// updateTaskRequest uses pointers so "field absent" and "field set to
// empty" are distinguishable. Sending `"assignee_id": ""` unassigns;
// omitting it leaves the assignee alone.
type updateTaskRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	AssigneeID  *string `json:"assignee_id"`
	Status      *string `json:"status"`
	DueAt       *int64  `json:"due_at"`
}

type statusRequest struct {
	Status string `json:"status"`
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	// Bounded read: a malformed or hostile client cannot make the server
	// buffer an unbounded body.
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "malformed request body"})
		return false
	}
	return true
}

// assigneeMustBeInTeam enforces that you cannot assign work to someone who
// cannot see it. Empty assignee (unassigned) is always allowed.
func (p *Plugin) assigneeMustBeInTeam(w http.ResponseWriter, assigneeID, teamID string) bool {
	if assigneeID == "" {
		return true
	}
	member, err := p.client.Team.GetMember(teamID, assigneeID)
	if err != nil || member == nil || member.DeleteAt != 0 {
		p.writeErr(w, http.StatusBadRequest, "assignee is not a member of this team", nil)
		return false
	}
	return true
}

// loadTaskForUser is the single path by which a task is fetched for any
// per-task operation.
//
// The order is the security property: fetch by ID, then check the caller
// against the team ON THE ROW. Authorization is never derived from
// anything the client sent -- only from the stored team_id. A task ID
// alone is never sufficient.
func (p *Plugin) loadTaskForUser(w http.ResponseWriter, taskID, userID string) (*Task, bool) {
	if !model.IsValidId(taskID) {
		p.notFound(w)
		return nil, false
	}
	task, err := p.store.GetTask(taskID)
	if errors.Is(err, ErrNotFound) {
		p.notFound(w)
		return nil, false
	}
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not load task", err)
		return nil, false
	}

	member, mErr := p.client.Team.GetMember(task.TeamID, userID)
	if mErr != nil || member == nil || member.DeleteAt != 0 {
		// Outside the team: same answer as "does not exist".
		p.notFound(w)
		return nil, false
	}
	return task, true
}

// canMutate: the creator manages their task; the assignee may act on the
// task they have been given. Other team members can read but not change.
func canMutate(t *Task, userID string) bool {
	return t.CreatorID == userID || (t.AssigneeID != "" && t.AssigneeID == userID)
}

// --- Handlers --------------------------------------------------------------

func (p *Plugin) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	var req createTaskRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !p.requireTeamMember(w, userID, req.TeamID) {
		return
	}
	if !p.assigneeMustBeInTeam(w, req.AssigneeID, req.TeamID) {
		return
	}

	status := TaskStatus(req.Status)
	if req.Status == "" {
		status = StatusTodo
	}

	now := nowMillis()
	task := &Task{
		ID:          model.NewId(),
		TeamID:      req.TeamID,
		CreatorID:   userID, // from the session, never from the body
		AssigneeID:  req.AssigneeID,
		Title:       req.Title,
		Description: req.Description,
		Status:      status,
		DueAt:       req.DueAt,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	task.Normalise()
	if err := task.IsValid(); err != nil {
		p.writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if err := p.store.CreateTask(task); err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not create task", err)
		return
	}

	p.notifyAssigned(task, userID)
	writeJSON(w, http.StatusCreated, task)
}

func (p *Plugin) handleListTasks(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	teamID := q.Get("team_id")
	if !p.requireTeamMember(w, userID, teamID) {
		return
	}

	filter := TaskFilter{
		AssigneeID: q.Get("assignee_id"),
		CreatorID:  q.Get("creator_id"),
	}
	// "me" saves a client from having to know its own ID.
	if filter.AssigneeID == "me" {
		filter.AssigneeID = userID
	}
	if filter.CreatorID == "me" {
		filter.CreatorID = userID
	}
	if s := q.Get("status"); s != "" {
		if !TaskStatus(s).Valid() {
			p.writeErr(w, http.StatusBadRequest, "invalid status filter", nil)
			return
		}
		filter.Status = TaskStatus(s)
	}
	if v := q.Get("due_before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			p.writeErr(w, http.StatusBadRequest, "due_before must be a Unix millisecond timestamp", nil)
			return
		}
		filter.DueBefore = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			p.writeErr(w, http.StatusBadRequest, "limit must be a number", nil)
			return
		}
		filter.Limit = n
	}
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			p.writeErr(w, http.StatusBadRequest, "page must be a non-negative number", nil)
			return
		}
		filter.Clamp()
		filter.Offset = n * filter.Limit
	}

	tasks, err := p.store.ListTasks(teamID, filter)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not list tasks", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (p *Plugin) handleGetTask(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	task, ok := p.loadTaskForUser(w, mux.Vars(r)["task_id"], userID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (p *Plugin) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	task, ok := p.loadTaskForUser(w, mux.Vars(r)["task_id"], userID)
	if !ok {
		return
	}
	if !canMutate(task, userID) {
		// A team member who is neither creator nor assignee: they may
		// read this task, so its existence is not a secret from them and
		// 403 is the honest answer.
		p.writeErr(w, http.StatusForbidden, "only the creator or assignee can change this task", nil)
		return
	}

	var req updateTaskRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	previousAssignee := task.AssigneeID
	previousStatus := task.Status

	if req.Title != nil {
		task.Title = *req.Title
	}
	if req.Description != nil {
		task.Description = *req.Description
	}
	if req.DueAt != nil {
		task.DueAt = *req.DueAt
	}
	if req.Status != nil {
		task.Status = TaskStatus(*req.Status)
	}
	if req.AssigneeID != nil {
		if !p.assigneeMustBeInTeam(w, *req.AssigneeID, task.TeamID) {
			return
		}
		task.AssigneeID = *req.AssigneeID
	}

	task.Normalise()
	if err := task.IsValid(); err != nil {
		p.writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	task.UpdatedAt = nowMillis()

	if err := p.store.UpdateTask(task); err != nil {
		if errors.Is(err, ErrNotFound) {
			p.notFound(w)
			return
		}
		p.writeErr(w, http.StatusInternalServerError, "could not update task", err)
		return
	}

	if task.AssigneeID != previousAssignee {
		p.notifyAssigned(task, userID)
	}
	if task.Status != previousStatus {
		if task.Status == StatusDone {
			p.notifyCompleted(task, userID)
		} else {
			p.notifyStatusChanged(task, userID, previousStatus, task.Status)
		}
	}
	writeJSON(w, http.StatusOK, task)
}

func (p *Plugin) handleSetTaskStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	task, ok := p.loadTaskForUser(w, mux.Vars(r)["task_id"], userID)
	if !ok {
		return
	}
	if !canMutate(task, userID) {
		p.writeErr(w, http.StatusForbidden, "only the creator or assignee can change this task", nil)
		return
	}
	var req statusRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	status := TaskStatus(req.Status)
	if !status.Valid() {
		p.writeErr(w, http.StatusBadRequest, "status must be one of todo, in_progress, done", nil)
		return
	}

	previousStatus := task.Status
	task.Status = status
	task.UpdatedAt = nowMillis()
	if err := p.store.UpdateTask(task); err != nil {
		if errors.Is(err, ErrNotFound) {
			p.notFound(w)
			return
		}
		p.writeErr(w, http.StatusInternalServerError, "could not update task", err)
		return
	}

	if status != previousStatus {
		if status == StatusDone {
			p.notifyCompleted(task, userID)
		} else {
			p.notifyStatusChanged(task, userID, previousStatus, status)
		}
	}
	writeJSON(w, http.StatusOK, task)
}

// handleDeleteTask soft-deletes. Only the creator may: an assignee can
// finish or update work, but removing the record is the owner's call.
func (p *Plugin) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	task, ok := p.loadTaskForUser(w, mux.Vars(r)["task_id"], userID)
	if !ok {
		return
	}
	if task.CreatorID != userID {
		p.writeErr(w, http.StatusForbidden, "only the creator can delete this task", nil)
		return
	}
	if err := p.store.SoftDeleteTask(task.ID, nowMillis()); err != nil {
		if errors.Is(err, ErrNotFound) {
			p.notFound(w)
			return
		}
		p.writeErr(w, http.StatusInternalServerError, "could not delete task", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
