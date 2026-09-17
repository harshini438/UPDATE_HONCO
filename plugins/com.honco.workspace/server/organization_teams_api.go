package main

import (
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
)

// Team (department) management for Organization Admins.
//
// Departments ARE Mattermost teams -- this never replaces them. An org
// admin can create a new department, view a department's members, and
// appoint or remove Team Admins for ANY department in their own
// organization (decision J1), all performed server-side under the org-admin
// gate. Team administration itself continues to use Mattermost's native
// TeamMembers/SchemeAdmin; Honco only decides who is allowed to change it.

// requireOrgTeam checks the org-admin gate AND that the team named in the
// URL actually belongs to this organization. Returns the admin's id. A team
// in another org (or unmapped) is reported as 404 -- an org admin cannot
// reach across the boundary by supplying another org's team id.
func (p *Plugin) requireOrgTeam(w http.ResponseWriter, r *http.Request, orgID, teamID string) (string, bool) {
	adminID, ok := p.requireOrgAdmin(w, r, orgID)
	if !ok {
		return "", false
	}
	if !model.IsValidId(teamID) {
		p.notFound(w)
		return "", false
	}
	mappedOrg, err := p.store.OrgIDForTeam(teamID)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not read team mapping", err)
		return "", false
	}
	if mappedOrg != orgID {
		// Not this org's team (or unmapped): reveal nothing.
		p.notFound(w)
		return "", false
	}
	return adminID, true
}

type createDeptRequest struct {
	DisplayName string `json:"display_name"`
	Name        string `json:"name"`
}

// handleCreateOrgTeam creates a new department (a Mattermost team) inside
// the organization and maps it. New departments default to invite-only
// (decision J3): a company's departments are not open for anyone on the
// instance to join. The creating admin becomes the team's first Team Admin.
func (p *Plugin) handleCreateOrgTeam(w http.ResponseWriter, r *http.Request) {
	orgID := mux.Vars(r)["org_id"]
	adminID, ok := p.requireOrgAdmin(w, r, orgID)
	if !ok {
		return
	}
	var req createDeptRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	display := trimTo(req.DisplayName, 64)
	if display == "" {
		p.writeErr(w, http.StatusBadRequest, "display_name is required", nil)
		return
	}
	name := req.Name
	if name == "" {
		name = slugify(display)
	}
	// Mattermost team names must be url-safe and unique; keep it short and
	// add a little entropy so a second "Engineering" does not collide.
	name = strings.Trim(name, "-")
	if len(name) > 50 {
		name = name[:50]
	}
	if name == "" {
		name = "team"
	}
	name = name + "-" + strings.ToLower(model.NewId()[:6])

	created, appErr := p.API.CreateTeam(&model.Team{
		DisplayName:     display,
		Name:            name,
		Type:            model.TeamInvite, // invite-only department
		AllowOpenInvite: false,
	})
	if appErr != nil || created == nil {
		p.writeErr(w, http.StatusInternalServerError, "could not create the department", appErr)
		return
	}
	// The creating admin joins as the first Team Admin.
	if _, mErr := p.API.CreateTeamMember(created.Id, adminID); mErr != nil {
		p.client.Log.Warn("honco org: created team but could not add creator", "team_id", created.Id, "err", mErr.Error())
	} else if _, rErr := p.API.UpdateTeamMemberRoles(created.Id, adminID, "team_user team_admin"); rErr != nil {
		p.client.Log.Warn("honco org: could not make creator a team admin", "team_id", created.Id, "err", rErr.Error())
	}
	// Map the department into the org (unique team_id enforces one org).
	if err := p.store.MapTeam(orgID, created.Id, adminID); err != nil {
		p.client.Log.Warn("honco org: created team but could not map it", "team_id", created.Id, "err", err.Error())
	}
	writeJSON(w, http.StatusCreated, orgTeamInfo{
		TeamID: created.Id, Name: created.Name, DisplayName: created.DisplayName, MemberCount: 1,
	})
}

type teamMemberInfo struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Name      string `json:"name"`
	TeamAdmin bool   `json:"team_admin"`
	IsGuest   bool   `json:"is_guest"`
	IsBot     bool   `json:"is_bot"`
}

// handleListOrgTeamMembers lists a department's roster with team-admin
// status. Org admins only, and only for a team in their org.
func (p *Plugin) handleListOrgTeamMembers(w http.ResponseWriter, r *http.Request) {
	orgID := mux.Vars(r)["org_id"]
	teamID := mux.Vars(r)["team_id"]
	if _, ok := p.requireOrgTeam(w, r, orgID, teamID); !ok {
		return
	}
	tms, appErr := p.API.GetTeamMembers(teamID, 0, 400)
	if appErr != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not list team members", appErr)
		return
	}
	out := make([]teamMemberInfo, 0, len(tms))
	for _, tm := range tms {
		if tm == nil || tm.DeleteAt != 0 {
			continue
		}
		info := teamMemberInfo{UserID: tm.UserId, TeamAdmin: teamMemberIsAdmin(tm)}
		if u, uerr := p.client.User.Get(tm.UserId); uerr == nil && u != nil {
			info.Username = u.Username
			info.Name = userDisplayName(u)
			info.IsGuest = u.IsGuest()
			info.IsBot = u.IsBot
		}
		out = append(out, info)
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

// handleSetTeamAdmin appoints a Team Admin for a department in the org. The
// target must already be a member of the team (we appoint existing members,
// not add strangers). Performed with the plugin's privileges under the
// org-admin gate, so an org admin can manage any of their departments even
// if they are not personally that team's admin.
func (p *Plugin) handleSetTeamAdmin(w http.ResponseWriter, r *http.Request) {
	orgID := mux.Vars(r)["org_id"]
	teamID := mux.Vars(r)["team_id"]
	targetID := mux.Vars(r)["user_id"]
	if _, ok := p.requireOrgTeam(w, r, orgID, teamID); !ok {
		return
	}
	if !model.IsValidId(targetID) {
		p.notFound(w)
		return
	}
	if _, appErr := p.API.GetTeamMember(teamID, targetID); appErr != nil {
		p.writeErr(w, http.StatusBadRequest, "that user is not a member of this team", nil)
		return
	}
	if _, appErr := p.API.UpdateTeamMemberRoles(teamID, targetID, "team_user team_admin"); appErr != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not assign team admin", appErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"team_id": teamID, "user_id": targetID, "team_admin": true})
}

// handleRemoveTeamAdmin demotes a Team Admin back to a plain member.
func (p *Plugin) handleRemoveTeamAdmin(w http.ResponseWriter, r *http.Request) {
	orgID := mux.Vars(r)["org_id"]
	teamID := mux.Vars(r)["team_id"]
	targetID := mux.Vars(r)["user_id"]
	if _, ok := p.requireOrgTeam(w, r, orgID, teamID); !ok {
		return
	}
	if !model.IsValidId(targetID) {
		p.notFound(w)
		return
	}
	if _, appErr := p.API.GetTeamMember(teamID, targetID); appErr != nil {
		p.writeErr(w, http.StatusBadRequest, "that user is not a member of this team", nil)
		return
	}
	if _, appErr := p.API.UpdateTeamMemberRoles(teamID, targetID, "team_user"); appErr != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not remove team admin", appErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"team_id": teamID, "user_id": targetID, "team_admin": false})
}
