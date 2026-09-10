package main

import (
	"encoding/json"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
)

// The support card and its notifications.
//
// A card is only ever posted into the channel the requester themselves
// chose when raising the request. A request raised from the panel with no
// channel gets no post at all -- asking for help with your machine is not
// something Honco should announce on your behalf.
//
// Nothing here carries a credential, because nothing in this feature holds
// one. The card shows who asked, what they said was wrong, who is helping
// and what state it is in.

const PostTypeSupport = "custom_honco_support"

// Support notification kinds, using the same ledger as everything else.
const (
	KindSupportRequested = "support_requested"
	KindSupportAccepted  = "support_accepted"
	KindSupportRejected  = "support_rejected"
	KindSupportStarted   = "support_started"
	KindSupportEnded     = "support_ended"
	KindSupportCancelled = "support_cancelled"
)

type supportProps struct {
	RequestID     string `json:"request_id"`
	Status        string `json:"status"`
	RequesterName string `json:"requester_name"`
	AgentName     string `json:"agent_name,omitempty"`
	Issue         string `json:"issue"`
	CreatedAt     int64  `json:"created_at"`
}

func (p *Plugin) buildSupportProps(req *SupportRequest) *supportProps {
	props := &supportProps{
		RequestID:     req.ID,
		Status:        req.Status,
		RequesterName: p.username(req.RequesterID),
		Issue:         req.Issue,
		CreatedAt:     req.CreatedAt,
	}
	if req.AgentID != "" {
		props.AgentName = p.username(req.AgentID)
	}
	return props
}

func supportStatusLine(status string) string {
	switch status {
	case SupportOpen:
		return ":large_yellow_circle: Waiting for support"
	case SupportAccepted:
		return ":large_green_circle: Accepted"
	case SupportActive:
		return ":large_green_circle: Session active"
	case SupportEnded:
		return ":white_circle: Session ended"
	case SupportCancelled:
		return ":white_circle: Cancelled"
	case SupportRejected:
		return ":white_circle: Declined"
	}
	return status
}

// supportFallbackText is what clients without the plugin bundle see, and
// what appears in search and notification previews.
func supportFallbackText(props *supportProps) string {
	msg := fmt.Sprintf(":tools: **Remote Support Request**\n_Requested by %s_\n\n%s",
		props.RequesterName, supportStatusLine(props.Status))
	if props.Issue != "" {
		msg += "\n\nIssue: " + props.Issue
	}
	if props.AgentName != "" {
		msg += "\nAgent: " + props.AgentName
	}
	return msg
}

func supportPropsMap(props *supportProps) map[string]any {
	// Post props cross the plugin RPC boundary, which encodes only basic
	// types -- a struct makes the encode fail and the API return a nil
	// post. Same reason as the meeting card.
	raw, err := json.Marshal(props)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func (p *Plugin) ensureSupportCard(req *SupportRequest) {
	defer p.recoverSupport("ensureSupportCard", req)
	// No channel means the requester did not ask for this to be visible
	// anywhere. Respect that.
	if p.botID == "" || req.ChannelID == "" || req.PostID != "" {
		return
	}
	props := p.buildSupportProps(req)
	post := &model.Post{
		UserId:    p.botID,
		ChannelId: req.ChannelID,
		Type:      PostTypeSupport,
		Message:   supportFallbackText(props),
	}
	post.AddProp("honco_support", supportPropsMap(props))

	if err := p.client.Post.CreatePost(post); err != nil {
		p.client.Log.Warn("honco: could not post support card", "err", err.Error())
		return
	}
	if err := p.store.SetSupportPost(req.ID, post.Id); err != nil {
		p.client.Log.Warn("honco: could not record support post id", "err", err.Error())
	}
	req.PostID = post.Id
}

func (p *Plugin) refreshSupportCard(req *SupportRequest) {
	defer p.recoverSupport("refreshSupportCard", req)
	if req.PostID == "" {
		return
	}
	post, err := p.client.Post.GetPost(req.PostID)
	if err != nil || post == nil || post.DeleteAt != 0 {
		return
	}
	props := p.buildSupportProps(req)
	post.Message = supportFallbackText(props)
	post.Type = PostTypeSupport
	post.AddProp("honco_support", supportPropsMap(props))
	if err := p.client.Post.UpdatePost(post); err != nil {
		p.client.Log.Warn("honco: could not update support card", "err", err.Error())
	}
}

// recoverSupport keeps a card problem from taking the plugin down with it.
func (p *Plugin) recoverSupport(where string, req *SupportRequest) {
	if rec := recover(); rec != nil {
		id := ""
		if req != nil {
			id = req.ID
		}
		p.client.Log.Error("honco: support card panicked",
			"where", where, "request_id", id, "recovered", fmt.Sprintf("%v", rec))
	}
}

// --- notifications ---------------------------------------------------------

// notifySupportAgents tells the support channel that work has arrived.
//
// This is a post in the support channel, not a DM to each agent: agents
// are defined as the members of that channel, so posting there reaches
// exactly them and no one else, and it does not multiply into one DM per
// agent per request. Mattermost's own notification preferences then decide
// how loudly each agent hears it.
func (p *Plugin) notifySupportAgents(req *SupportRequest) {
	channelID := p.supportChannelID()
	if p.botID == "" || channelID == "" {
		return
	}
	key := fmt.Sprintf("%s:%s", KindSupportRequested, req.ID)
	if !p.claim(KindSupportRequested, req.ID, "", key) {
		return
	}
	msg := fmt.Sprintf(":tools: New remote support request from **%s**.", p.username(req.RequesterID))
	if req.Issue != "" {
		msg += "\n" + req.Issue
	}
	if err := p.client.Post.CreatePost(&model.Post{
		UserId: p.botID, ChannelId: channelID, Message: msg,
	}); err != nil {
		p.client.Log.Warn("honco: could not notify support agents", "err", err.Error())
	}
}

// notifySupportTransition tells the other party what happened.
//
// Never the actor: they just did it. Never anyone outside the requester
// and the assigned agent, so a support request cannot become a way to
// learn that a colleague needed help.
func (p *Plugin) notifySupportTransition(req *SupportRequest, actorID, action string) {
	var recipient, kind, msg string

	switch action {
	case SupportActionAccepted:
		recipient, kind = req.RequesterID, KindSupportAccepted
		msg = fmt.Sprintf("Your support request was accepted by **%s**.", p.username(actorID))
	case SupportActionRejected:
		recipient, kind = req.RequesterID, KindSupportRejected
		msg = "Your support request was declined."
		if req.Reason != "" {
			msg += " " + req.Reason
		}
	case SupportActionStarted:
		recipient, kind = req.RequesterID, KindSupportStarted
		msg = fmt.Sprintf("**%s** started the remote support session.", p.username(actorID))
	case SupportActionEnded:
		recipient, kind = req.RequesterID, KindSupportEnded
		if actorID == req.RequesterID {
			recipient = req.AgentID // the requester ended it; tell the agent
		}
		msg = "The remote support session has ended."
	case SupportActionCancelled:
		recipient, kind = req.AgentID, KindSupportCancelled
		msg = fmt.Sprintf("**%s** cancelled their support request.", p.username(actorID))
	default:
		return
	}

	if recipient == "" || recipient == actorID {
		return
	}
	key := fmt.Sprintf("%s:%s:%d", kind, req.ID, req.UpdatedAt)
	p.dmOnce(kind, req.ID, recipient, key, msg)
}
