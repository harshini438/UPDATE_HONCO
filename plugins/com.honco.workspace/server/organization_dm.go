package main

import (
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// Organization-scoped direct messages, enforced on the server.
//
// Mattermost direct (D) and group (G) message channels are server-wide: any
// two accounts can open one regardless of team, and there is no org concept
// beneath it. The plugin therefore enforces the boundary itself, at the one
// point that actually carries content -- the post. MessageWillBePosted
// rejects any message in a D/G channel whose human participants do not all
// belong to the same organization, and any message in a D/G channel that
// includes a guest. No message can cross an organization boundary, so no
// communication is possible even if a channel shell was created first.
//
// This is server-side enforcement, not UI hiding: the browser never gets a
// chance to bypass it, because the post is refused before it is stored.
//
// Safety posture (precise):
//
//   - Channel CLASSIFICATION failure (client.Channel.Get fails): fail OPEN.
//     We cannot tell whether this is even a direct message, and the hook
//     runs on EVERY post, so failing closed here would block all ordinary
//     chat during a store blip. This path is not attacker-reachable: the
//     hook fires only for a post Mattermost has already routed to a
//     resolved channel, read from the same store.
//   - Once the channel is KNOWN to be a DM/GM, any failure to determine the
//     participants' organizations -- members unreadable, a user unresolvable,
//     or a real org-lookup error -- is a SAFE DENIAL (fail CLOSED). A
//     database or authorization error must never permit a cross-org message.
//   - "Missing membership" is NOT a failure: an unmapped user resolves
//     deterministically to the default org (the approved transition rule),
//     which is an allow, not an error.
//
// Bots (the Honco notification bot, the meeting bot) are exempt as posters
// and as participants, so notifications delivered as bot DMs are never
// blocked.

// dmChannelAllowed decides whether posterID may post into channelID.
//
// Safety model (Phase 6): the guard applies ONLY to direct (D) and group
// (G) channels; a normal team channel post is never gated, so a lookup blip
// there cannot block ordinary chat. But once a channel is KNOWN to be a DM,
// any failure to determine the participants' organizations is a SAFE
// DENIAL -- a database or authorization error must never be allowed to
// permit a cross-organization message. "Missing membership" is NOT such a
// failure: an unmapped user resolves deterministically to the default org
// (the approved transition rule), which is an allow, not an error.
func (p *Plugin) dmChannelAllowed(channelID, posterID string) (bool, string) {
	ch, err := p.client.Channel.Get(channelID)
	if err != nil || ch == nil {
		// Cannot classify the channel. This is not an org-membership
		// determination for a known DM, and a normal channel post must not
		// be blocked by a channel-lookup blip, so allow -- but record it.
		if err != nil {
			p.client.Log.Warn("honco org: DM guard could not load channel", "channel_id", channelID, "err", err.Error())
		}
		return true, ""
	}
	// Only direct and group messages are gated. Team channels (O/P) are
	// already team-scoped and therefore org-scoped through the team map.
	if ch.Type != model.ChannelTypeDirect && ch.Type != model.ChannelTypeGroup {
		return true, ""
	}
	// A bot poster (Honco, meeting bot) is always allowed: bot DMs are how
	// notifications reach people and carry no cross-company risk.
	if u, uerr := p.client.User.Get(posterID); uerr == nil && u != nil && u.IsBot {
		return true, ""
	}

	// From here the channel is a DM/GM. A failure to read its members means
	// we cannot prove the message stays inside one organization, so we deny.
	members, aerr := p.API.GetChannelMembers(channelID, 0, 200)
	if aerr != nil || len(members) == 0 {
		if aerr != nil {
			p.client.Log.Warn("honco org: DM guard could not read channel members", "channel_id", channelID, "err", aerr.Error())
		}
		return dmDecision(nil, false, true) // safe denial
	}
	var orgIDs []string
	guestPresent := false
	lookupFailed := false
	for _, m := range members {
		u, uerr := p.client.User.Get(m.UserId)
		if uerr != nil || u == nil {
			// A participant we cannot resolve could belong to another org;
			// we must not guess in the permissive direction.
			lookupFailed = true
			continue
		}
		if u.IsBot {
			// A bot participant (e.g. the Honco bot in a bot-DM) does not
			// constrain the organization set.
			continue
		}
		if u.IsGuest() {
			guestPresent = true
			continue
		}
		orgID, oerr := p.orgIDForUser(u.Id)
		if oerr != nil {
			// A real error resolving org membership (not "unmapped") -> deny.
			lookupFailed = true
			continue
		}
		orgIDs = append(orgIDs, orgID)
	}
	return dmDecision(orgIDs, guestPresent, lookupFailed)
}

// dmDecision is the pure boundary rule, split out so every branch can be
// tested without a database or a running server. Precedence is deliberate:
//
//  1. lookupFailed  -> SAFE DENIAL. If the organization of any participant
//     could not be determined, the message is refused rather than risked.
//  2. guestPresent  -> blocked (guests have no DM access).
//  3. more than one organization among the human participants -> blocked.
//
// Otherwise the message stays within a single organization and is allowed.
func dmDecision(humanOrgIDs []string, guestPresent, lookupFailed bool) (bool, string) {
	if lookupFailed {
		return false, "your direct-message permissions could not be verified; please try again"
	}
	if guestPresent {
		return false, "guests cannot use direct messages"
	}
	set := map[string]struct{}{}
	for _, o := range humanOrgIDs {
		if o != "" {
			set[o] = struct{}{}
		}
	}
	if len(set) > 1 {
		return false, "direct messages are limited to members of your organization"
	}
	return true, ""
}

// MessageWillBePosted is the hard gate. A rejected post never reaches the
// database, so the block cannot be worked around from the client. Normal
// channel posts pay only one (cached) channel lookup before being allowed.
func (p *Plugin) MessageWillBePosted(_ *plugin.Context, post *model.Post) (*model.Post, string) {
	if post == nil {
		return post, ""
	}
	// System/automated posts (joins, headers, meeting cards) carry a Type
	// and are never a cross-org message; let them through untouched.
	if post.Type != "" {
		return post, ""
	}
	if allowed, reason := p.dmChannelAllowed(post.ChannelId, post.UserId); !allowed {
		// Returning a non-empty string rejects the post with that message.
		return nil, reason
	}
	return post, ""
}

// ChannelHasBeenCreated records a cross-organization or guest D/G channel
// for the operator. There is no pre-create channel hook in the plugin API,
// so a channel shell may briefly exist; it is harmless because every post
// into it is refused by MessageWillBePosted. This log makes the attempt
// visible without altering behaviour.
func (p *Plugin) ChannelHasBeenCreated(_ *plugin.Context, channel *model.Channel) {
	if channel == nil {
		return
	}
	if channel.Type != model.ChannelTypeDirect && channel.Type != model.ChannelTypeGroup {
		return
	}
	// Creator is the least-privileged relevant identity; pass "" so the
	// classification is by participants, not the creator's bot-ness.
	if allowed, reason := p.dmChannelAllowed(channel.Id, ""); !allowed {
		p.client.Log.Info("honco org: a cross-organization or guest direct channel was created; its messages will be blocked",
			"channel_id", channel.Id, "reason", reason)
	}
}
