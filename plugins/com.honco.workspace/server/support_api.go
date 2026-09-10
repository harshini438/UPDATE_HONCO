package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Remote support endpoints.
//
// Who may do what is decided here, from the authenticated session, and
// never from anything the browser sends. The browser does not get to say
// who the agent is, which team it is acting in, or what role it holds --
// those come from the Mattermost session and from channel membership,
// asked of Mattermost at the moment of the request.

// WebSocketEventSupport is broadcast when a request changes state.
const WebSocketEventSupport = "support_updated"

type createSupportRequest struct {
	TeamID    string `json:"team_id"`
	ChannelID string `json:"channel_id"`
	Issue     string `json:"issue"`
}

type supportActionRequest struct {
	// Reason is optional context for a rejection or cancellation. It is
	// free text from a browser and is sanitised like any other.
	Reason string `json:"reason"`
}

// --- who is a support agent ------------------------------------------------

// supportChannelID resolves the configured support channel.
//
// Agents are the members of one designated channel. That is the smallest
// configuration that works: it reuses Mattermost membership as the single
// source of truth, an admin adds or removes an agent by adding or removing
// them from a channel, and it deliberately does NOT mean "every system
// admin" -- administering a server and doing desk-side support are
// different jobs held by different people.
//
// An unconfigured or missing channel resolves to empty, which means nobody
// is an agent. That fails closed: requests can still be raised, and nobody
// can accept them, which is visible and safe. The alternative -- treating
// misconfiguration as "everyone is an agent" -- would be a disaster.
func (p *Plugin) supportChannelID() string {
	cfg := p.config()
	team := strings.TrimSpace(cfg.SupportTeamName)
	name := strings.TrimSpace(cfg.SupportChannelName)
	if team == "" || name == "" {
		return ""
	}
	ch, err := p.client.Channel.GetByNameForTeamName(team, name, false)
	if err != nil || ch == nil || ch.DeleteAt != 0 {
		return ""
	}
	return ch.Id
}

// isSupportAgent asks Mattermost whether this user is in the support
// channel. Never cached and never taken from the request body.
func (p *Plugin) isSupportAgent(userID string) bool {
	channelID := p.supportChannelID()
	if channelID == "" || userID == "" {
		return false
	}
	member, err := p.client.Channel.GetMember(channelID, userID)
	return err == nil && member != nil
}

// loadSupportForUser fetches a request and proves the caller may see it.
//
// Visibility is a relationship: the requester, the assigned agent, or any
// support agent (who needs to see the queue). Everyone else gets the same
// 404 as a request that does not exist, so a request id cannot be probed
// to learn that a colleague asked for help -- which is itself sensitive.
func (p *Plugin) loadSupportForUser(w http.ResponseWriter, id, userID string) (*SupportRequest, bool) {
	if !model.IsValidId(id) {
		p.notFound(w)
		return nil, false
	}
	req, err := p.store.GetSupportRequest(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			p.notFound(w)
			return nil, false
		}
		p.writeErr(w, http.StatusInternalServerError, "could not load request", err)
		return nil, false
	}

	// Team membership first: a request belongs to a team, and someone
	// outside it has no business seeing it whatever their relationship.
	if member, merr := p.client.Team.GetMember(req.TeamID, userID); merr != nil || member == nil || member.DeleteAt != 0 {
		p.notFound(w)
		return nil, false
	}

	if req.RequesterID == userID || req.AgentID == userID || p.isSupportAgent(userID) {
		return req, true
	}
	p.notFound(w)
	return nil, false
}

// --- create ----------------------------------------------------------------

func (p *Plugin) handleCreateSupportRequest(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	var body createSupportRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if !p.requireTeamMember(w, userID, body.TeamID) {
		return
	}

	// A channel is optional, but if one is given the requester must be in
	// it -- otherwise a request could be used to post into a channel they
	// cannot see.
	channelID := ""
	if body.ChannelID != "" {
		if !model.IsValidId(body.ChannelID) {
			p.writeErr(w, http.StatusBadRequest, "invalid channel_id", nil)
			return
		}
		if _, err := p.client.Channel.GetMember(body.ChannelID, userID); err != nil {
			p.notFound(w)
			return
		}
		channelID = body.ChannelID
	}

	now := nowMillis()
	req := &SupportRequest{
		ID:          model.NewId(),
		TeamID:      body.TeamID,
		ChannelID:   channelID,
		RequesterID: userID,
		Issue:       sanitiseIssue(body.Issue),
		Status:      SupportOpen,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := p.store.CreateSupportRequest(req); err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not create request", err)
		return
	}
	p.auditSupport(req, userID, SupportActionCreated, "")
	p.ensureSupportCard(req)
	p.notifySupportAgents(req)
	p.broadcastSupport(req)

	writeJSON(w, http.StatusCreated, req)
}

// --- read ------------------------------------------------------------------

func (p *Plugin) handleListSupportRequests(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	teamID := r.URL.Query().Get("team_id")
	if !p.requireTeamMember(w, userID, teamID) {
		return
	}
	reqs, err := p.store.ListSupportRequests(teamID, userID, p.isSupportAgent(userID), 50)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not list requests", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"requests": reqs,
		// So the UI can show the agent controls without guessing. This is
		// a convenience for rendering, never an authorization decision --
		// every action re-checks server-side.
		"is_agent": p.isSupportAgent(userID),
	})
}

func (p *Plugin) handleGetSupportRequest(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	req, ok := p.loadSupportForUser(w, muxVar(r, "request_id"), userID)
	if !ok {
		return
	}
	events, err := p.store.ListSupportEvents(req.ID)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not load history", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": req, "events": events})
}

// --- transitions -----------------------------------------------------------

// supportAction is the one place a request changes state.
//
// Every endpoint below funnels through here, so the rules -- who may act,
// which moves are legal, what gets audited, who is told -- exist once
// rather than five times with four subtle differences.
func (p *Plugin) supportAction(w http.ResponseWriter, r *http.Request, to, action string,
	authorized func(req *SupportRequest, userID string) bool) {

	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	req, ok := p.loadSupportForUser(w, muxVar(r, "request_id"), userID)
	if !ok {
		return
	}

	// Being able to SEE a request is not being able to change it.
	if !authorized(req, userID) {
		p.writeErr(w, http.StatusForbidden, "you may not perform this action on this request", nil)
		return
	}
	if !canTransition(req.Status, to) {
		p.writeErr(w, http.StatusConflict,
			fmt.Sprintf("a request that is %s cannot become %s", req.Status, to), nil)
		return
	}

	var body supportActionRequest
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	}

	from := req.Status
	now := nowMillis()
	req.Status = to
	req.UpdatedAt = now
	if reason := sanitiseIssue(body.Reason); reason != "" {
		req.Reason = reason
	}

	switch to {
	case SupportAccepted:
		req.AgentID = userID
		req.AcceptedAt = now
	case SupportActive:
		req.StartedAt = now
	case SupportEnded, SupportCancelled, SupportRejected:
		req.EndedAt = now
		if to == SupportRejected {
			// A rejection leaves the request unassigned: it was refused,
			// not handled, and another agent should still be able to see
			// it in the history as unclaimed.
			req.AgentID = ""
		}
	}

	if err := p.store.TransitionSupportRequest(req, from); err != nil {
		if errors.Is(err, ErrInvalidTransition) {
			// Lost a race with another agent.
			p.writeErr(w, http.StatusConflict, "this request was already updated by someone else", nil)
			return
		}
		p.writeErr(w, http.StatusInternalServerError, "could not update request", err)
		return
	}

	p.auditSupport(req, userID, action, req.Reason)
	p.refreshSupportCard(req)
	p.notifySupportTransition(req, userID, action)
	p.broadcastSupport(req)

	writeJSON(w, http.StatusOK, req)
}

// Only a support agent may accept or reject; only the requester may
// cancel; either party may start or end. These predicates are the
// authorization model, kept next to the routes that use them.
func (p *Plugin) handleAcceptSupport(w http.ResponseWriter, r *http.Request) {
	p.supportAction(w, r, SupportAccepted, SupportActionAccepted,
		func(_ *SupportRequest, userID string) bool { return p.isSupportAgent(userID) })
}

func (p *Plugin) handleRejectSupport(w http.ResponseWriter, r *http.Request) {
	p.supportAction(w, r, SupportRejected, SupportActionRejected,
		func(_ *SupportRequest, userID string) bool { return p.isSupportAgent(userID) })
}

func (p *Plugin) handleStartSupport(w http.ResponseWriter, r *http.Request) {
	// The assigned agent starts the session. The requester cannot start it
	// on the agent's behalf -- somebody has to actually be at the far end.
	p.supportAction(w, r, SupportActive, SupportActionStarted,
		func(req *SupportRequest, userID string) bool { return req.AgentID == userID })
}

func (p *Plugin) handleEndSupport(w http.ResponseWriter, r *http.Request) {
	// Either party ends it. The requester especially: someone must always
	// be able to stop a support session on their own machine.
	p.supportAction(w, r, SupportEnded, SupportActionEnded,
		func(req *SupportRequest, userID string) bool {
			return req.AgentID == userID || req.RequesterID == userID
		})
}

func (p *Plugin) handleCancelSupport(w http.ResponseWriter, r *http.Request) {
	p.supportAction(w, r, SupportCancelled, SupportActionCancelled,
		func(req *SupportRequest, userID string) bool { return req.RequesterID == userID })
}

// --- audit -----------------------------------------------------------------

// auditSupport appends to the trail. A failure here is logged loudly: the
// action already happened, so the record is the only thing at risk, and
// losing it silently is the failure mode an audit trail exists to avoid.
func (p *Plugin) auditSupport(req *SupportRequest, actorID, action, detail string) {
	if err := p.store.AddSupportEvent(&SupportEvent{
		ID:        model.NewId(),
		RequestID: req.ID,
		ActorID:   actorID,
		Action:    action,
		Detail:    detail,
		CreatedAt: nowMillis(),
	}); err != nil {
		p.client.Log.Error("honco: could not write support audit record",
			"request_id", req.ID, "action", action, "err", err.Error())
	}
}

// --- realtime --------------------------------------------------------------

// broadcastSupport tells the people involved that something changed.
//
// Scoped to named users rather than a channel: a support request is
// private to the requester and the agent, and a channel-wide broadcast
// would hand its existence to everyone in the channel. The payload carries
// ids and a status, never the issue text.
func (p *Plugin) broadcastSupport(req *SupportRequest) {
	payload := map[string]any{
		"request_id": req.ID,
		"status":     req.Status,
		"team_id":    req.TeamID,
	}
	recipients := []string{req.RequesterID}
	if req.AgentID != "" {
		recipients = append(recipients, req.AgentID)
	}
	for _, uid := range recipients {
		p.client.Frontend.PublishWebSocketEvent(WebSocketEventSupport, payload,
			&model.WebsocketBroadcast{UserId: uid})
	}
	// Agents need to see new work arrive. Only the open queue is
	// broadcast to the support channel, and only ever as a status -- an
	// agent learns that a request exists, not what it says.
	if req.Status == SupportOpen {
		if ch := p.supportChannelID(); ch != "" {
			p.client.Frontend.PublishWebSocketEvent(WebSocketEventSupport, payload,
				&model.WebsocketBroadcast{ChannelId: ch})
		}
	}
}
