package main

import (
	"net/http"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Capabilities: the single-account permission model.
//
// A Honco account is ONE identity that carries several independent,
// scoped capabilities at once -- System Admin (platform), Organization
// Admin (an org), Team Admin (specific teams), plain membership (other
// teams), and the functional Support Agent capability. None of these is a
// separate login or a "current role": each is derived, per request, from
// where it is actually stored (Mattermost roles, the org roster, Mattermost
// team membership, the support channel). This endpoint gathers them so the
// UI can decide which controls to show -- but the UI decision is only UX;
// every action is still authorized on the server from the same sources.

// teamMemberIsAdmin reports whether a team membership carries team-admin.
// Both the scheme flag and the explicit role string are checked, because a
// team can use either depending on how it was configured.
func teamMemberIsAdmin(tm *model.TeamMember) bool {
	if tm == nil {
		return false
	}
	return tm.SchemeAdmin || strings.Contains(" "+tm.Roles+" ", " team_admin ")
}

// userDisplayName is the friendly name for a user, falling back through the
// same order Mattermost itself uses.
func userDisplayName(u *model.User) string {
	if u == nil {
		return ""
	}
	if n := strings.TrimSpace(u.Nickname); n != "" {
		return n
	}
	full := strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
	if full != "" {
		return full
	}
	return u.Username
}

// handleMyCapabilities returns the caller's full capability set. Any
// authenticated user may ask about THEMSELVES; the answer is derived
// entirely server-side and reflects only what is actually stored.
func (p *Plugin) handleMyCapabilities(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	systemAdmin := p.client.User.HasPermissionTo(userID, permissionManageSystem())

	orgID, _ := p.orgIDForUser(userID)
	orgRole, _ := p.store.OrgRole(orgID, userID)
	isOrgAdmin := orgRole == OrgRoleAdmin || systemAdmin

	var org map[string]any
	if o, err := p.store.GetOrg(orgID); err == nil {
		org = map[string]any{"id": o.ID, "slug": o.Slug, "name": o.Name, "role": orgRole}
	}

	// Team capabilities come from Mattermost's own team membership, so a
	// user who is team-admin of one team and a plain member of another is
	// represented exactly as Mattermost sees them -- no Honco duplication.
	teamAdminOf := []string{}
	memberOf := []string{}
	if tms, err := p.client.Team.ListMembersForUser(userID, 0, 200); err == nil {
		for _, tm := range tms {
			if tm == nil || tm.DeleteAt != 0 {
				continue
			}
			memberOf = append(memberOf, tm.TeamId)
			if teamMemberIsAdmin(tm) {
				teamAdminOf = append(teamAdminOf, tm.TeamId)
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":       userID,
		"system_admin":  systemAdmin,
		"is_org_admin":  isOrgAdmin,
		"organization":  org,
		"team_admin_of": teamAdminOf,
		"member_of":     memberOf,
		"support_agent": p.isSupportAgent(userID),
		"is_guest":      p.userIsGuest(userID),
	})
}

// userIsGuest reports whether a user is a Mattermost guest.
func (p *Plugin) userIsGuest(userID string) bool {
	u, err := p.client.User.Get(userID)
	return err == nil && u != nil && u.IsGuest()
}
