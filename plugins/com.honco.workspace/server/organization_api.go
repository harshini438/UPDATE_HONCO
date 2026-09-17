package main

import (
	"errors"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
)

// HTTP surface for the organization layer.
//
// The authorization discipline is the same the rest of the plugin uses:
// identity comes only from requireUser (the server-validated header); the
// organization a request may act on is taken from the URL and then checked
// against STORED membership, never against anything in the body; a caller
// with no membership in an organization is given the same 404 as a
// nonexistent one, so one company cannot even detect another's existence.

// requireOrgMember resolves the caller's relationship to the organization
// named in the URL. A System Admin is an admin of every org. A member sees
// their org (admin flag reflects their role). A non-member gets 404 --
// indistinguishable from an org that does not exist.
func (p *Plugin) requireOrgMember(w http.ResponseWriter, r *http.Request, orgID string) (userID string, admin, ok bool) {
	userID, ok = p.requireUser(w, r)
	if !ok {
		return "", false, false
	}
	if !model.IsValidId(orgID) {
		p.notFound(w)
		return "", false, false
	}
	if p.client.User.HasPermissionTo(userID, permissionManageSystem()) {
		return userID, true, true
	}
	role, err := p.store.OrgRole(orgID, userID)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not read membership", err)
		return "", false, false
	}
	if role == "" {
		// Not a member: reveal nothing.
		p.notFound(w)
		return "", false, false
	}
	return userID, role == OrgRoleAdmin, true
}

// requireOrgAdmin is requireOrgMember plus the admin gate. A member who is
// not an admin gets 403 (they already know the org exists -- they are in
// it), matching how task mutation is refused.
func (p *Plugin) requireOrgAdmin(w http.ResponseWriter, r *http.Request, orgID string) (string, bool) {
	userID, admin, ok := p.requireOrgMember(w, r, orgID)
	if !ok {
		return "", false
	}
	if !admin {
		p.writeErr(w, http.StatusForbidden, "organization administrator access is required", nil)
		return "", false
	}
	return userID, true
}

// --- organizations ---------------------------------------------------------

type createOrgRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Slug        string `json:"slug"`
}

// handleCreateOrg creates a company. Reserved for System Admins: standing up
// a new organization on the instance is a platform action, not something an
// existing org's admin does.
func (p *Plugin) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireSystemAdmin(w, r)
	if !ok {
		return
	}
	var req createOrgRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := trimTo(req.Name, 255)
	if name == "" {
		p.writeErr(w, http.StatusBadRequest, "name is required", nil)
		return
	}
	slug := req.Slug
	if slug == "" {
		slug = slugify(name)
	}
	if !validOrgSlug(slug) {
		p.writeErr(w, http.StatusBadRequest, "slug must be lowercase letters, digits and hyphens", nil)
		return
	}
	org := &Organization{
		Name:        name,
		DisplayName: trimTo(req.DisplayName, 255),
		Slug:        slug,
		CreatedBy:   userID,
	}
	if err := p.store.CreateOrg(org); err != nil {
		if errors.Is(err, ErrOrgConflict) {
			p.writeErr(w, http.StatusConflict, "an organization with that slug already exists", nil)
			return
		}
		p.writeErr(w, http.StatusInternalServerError, "could not create organization", err)
		return
	}
	writeJSON(w, http.StatusCreated, org)
}

// handleListOrgs: a System Admin sees every organization; anyone else sees
// only the one they belong to (or an empty list). No cross-org enumeration.
func (p *Plugin) handleListOrgs(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	if p.client.User.HasPermissionTo(userID, permissionManageSystem()) {
		orgs, err := p.store.ListOrgs()
		if err != nil {
			p.writeErr(w, http.StatusInternalServerError, "could not list organizations", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"organizations": orgs})
		return
	}
	orgID, err := p.store.ActiveOrgForUser(userID)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not read membership", err)
		return
	}
	out := []*Organization{}
	if orgID != "" {
		if org, err := p.store.GetOrg(orgID); err == nil {
			out = append(out, org)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"organizations": out})
}

// handleGetMyOrg tells the caller which organization they are in and their
// role, for the UI to decide what to show. Any authenticated user.
func (p *Plugin) handleGetMyOrg(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	orgID, err := p.orgIDForUser(userID)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not resolve organization", err)
		return
	}
	org, err := p.store.GetOrg(orgID)
	if err != nil {
		// The default org should always exist after activation; if it does
		// not, report no org rather than an error the UI cannot use.
		writeJSON(w, http.StatusOK, map[string]any{"organization": nil, "role": ""})
		return
	}
	role, _ := p.store.OrgRole(orgID, userID)
	isAdmin := role == OrgRoleAdmin || p.client.User.HasPermissionTo(userID, permissionManageSystem())
	writeJSON(w, http.StatusOK, map[string]any{
		"organization": org,
		"role":         role,
		"is_org_admin": isAdmin,
	})
}

func (p *Plugin) handleGetOrg(w http.ResponseWriter, r *http.Request) {
	orgID := mux.Vars(r)["org_id"]
	if _, _, ok := p.requireOrgMember(w, r, orgID); !ok {
		return
	}
	org, err := p.store.GetOrg(orgID)
	if err != nil {
		p.notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, org)
}

type updateOrgRequest struct {
	Name        *string `json:"name"`
	DisplayName *string `json:"display_name"`
	Status      *string `json:"status"`
}

func (p *Plugin) handleUpdateOrg(w http.ResponseWriter, r *http.Request) {
	orgID := mux.Vars(r)["org_id"]
	if _, ok := p.requireOrgAdmin(w, r, orgID); !ok {
		return
	}
	org, err := p.store.GetOrg(orgID)
	if err != nil {
		p.notFound(w)
		return
	}
	var req updateOrgRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != nil {
		if n := trimTo(*req.Name, 255); n != "" {
			org.Name = n
		}
	}
	if req.DisplayName != nil {
		org.DisplayName = trimTo(*req.DisplayName, 255)
	}
	if req.Status != nil && (*req.Status == OrgStatusActive || *req.Status == "suspended") {
		org.Status = *req.Status
	}
	if err := p.store.UpdateOrg(org); err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not update organization", err)
		return
	}
	writeJSON(w, http.StatusOK, org)
}

// --- team mapping ----------------------------------------------------------

type mapTeamRequest struct {
	TeamID string `json:"team_id"`
}

type orgTeamInfo struct {
	TeamID      string `json:"team_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	MemberCount int64  `json:"member_count"`
}

// handleListOrgTeams lists the org's departments with their display names
// and member counts. Any org member may view the department list (it is not
// sensitive); mutations require an admin.
func (p *Plugin) handleListOrgTeams(w http.ResponseWriter, r *http.Request) {
	orgID := mux.Vars(r)["org_id"]
	if _, _, ok := p.requireOrgMember(w, r, orgID); !ok {
		return
	}
	teamIDs, err := p.store.ListTeamsForOrg(orgID)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not list teams", err)
		return
	}
	out := make([]orgTeamInfo, 0, len(teamIDs))
	for _, tid := range teamIDs {
		info := orgTeamInfo{TeamID: tid}
		if t, terr := p.client.Team.Get(tid); terr == nil && t != nil {
			info.Name = t.Name
			info.DisplayName = t.DisplayName
		}
		if st, serr := p.API.GetTeamStats(tid); serr == nil && st != nil {
			info.MemberCount = st.TotalMemberCount
		}
		out = append(out, info)
	}
	// team_ids is kept for any existing caller; teams carries the detail.
	writeJSON(w, http.StatusOK, map[string]any{"team_ids": teamIDs, "teams": out})
}

func (p *Plugin) handleMapTeam(w http.ResponseWriter, r *http.Request) {
	orgID := mux.Vars(r)["org_id"]
	adminID, ok := p.requireOrgAdmin(w, r, orgID)
	if !ok {
		return
	}
	var req mapTeamRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !model.IsValidId(req.TeamID) {
		p.writeErr(w, http.StatusBadRequest, "team_id is required", nil)
		return
	}
	// The team must exist; a mapping to a nonexistent team would be a
	// dangling row.
	if _, err := p.client.Team.Get(req.TeamID); err != nil {
		p.writeErr(w, http.StatusBadRequest, "no such team", nil)
		return
	}
	if err := p.store.MapTeam(orgID, req.TeamID, adminID); err != nil {
		if errors.Is(err, ErrOrgConflict) {
			p.writeErr(w, http.StatusConflict, "that team already belongs to an organization", nil)
			return
		}
		p.writeErr(w, http.StatusInternalServerError, "could not map team", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"org_id": orgID, "team_id": req.TeamID})
}

func (p *Plugin) handleUnmapTeam(w http.ResponseWriter, r *http.Request) {
	orgID := mux.Vars(r)["org_id"]
	teamID := mux.Vars(r)["team_id"]
	if _, ok := p.requireOrgAdmin(w, r, orgID); !ok {
		return
	}
	// Only unmap a team that is actually in THIS org (never another's).
	cur, err := p.store.OrgIDForTeam(teamID)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not read mapping", err)
		return
	}
	if cur != orgID {
		p.notFound(w)
		return
	}
	if err := p.store.UnmapTeam(teamID); err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not unmap team", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"org_id": orgID, "team_id": teamID, "unmapped": true})
}

// --- members ---------------------------------------------------------------

type addMemberRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

// enrichedOrgMember is a member row with the identity and cross-capability
// information the Members page needs, all resolved server-side.
type enrichedOrgMember struct {
	UserID       string       `json:"user_id"`
	Username     string       `json:"username"`
	Name         string       `json:"name"`
	Role         string       `json:"role"`
	Status       string       `json:"status"`
	IsGuest      bool         `json:"is_guest"`
	IsBot        bool         `json:"is_bot"`
	SupportAgent bool         `json:"support_agent"`
	Teams        []memberTeam `json:"teams"`
}

type memberTeam struct {
	TeamID      string `json:"team_id"`
	DisplayName string `json:"display_name"`
	TeamAdmin   bool   `json:"team_admin"`
}

// handleListOrgMembers returns the org roster enriched with username,
// display name, org role, the org teams each member is in (with team-admin
// status), and the functional Support Agent capability. Org admins only.
func (p *Plugin) handleListOrgMembers(w http.ResponseWriter, r *http.Request) {
	orgID := mux.Vars(r)["org_id"]
	if _, ok := p.requireOrgAdmin(w, r, orgID); !ok {
		return
	}
	members, err := p.store.ListMembers(orgID)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not list members", err)
		return
	}

	// Which teams belong to this org, and their display names -- resolved
	// once, not per member.
	orgTeamIDs, _ := p.store.ListTeamsForOrg(orgID)
	orgTeamName := map[string]string{}
	for _, tid := range orgTeamIDs {
		if t, terr := p.client.Team.Get(tid); terr == nil && t != nil {
			orgTeamName[tid] = t.DisplayName
		} else {
			orgTeamName[tid] = ""
		}
	}

	out := make([]enrichedOrgMember, 0, len(members))
	for _, m := range members {
		em := enrichedOrgMember{UserID: m.UserID, Role: m.Role, Status: m.Status, Teams: []memberTeam{}}
		if u, uerr := p.client.User.Get(m.UserID); uerr == nil && u != nil {
			em.Username = u.Username
			em.Name = userDisplayName(u)
			em.IsGuest = u.IsGuest()
			em.IsBot = u.IsBot
		}
		em.SupportAgent = p.isSupportAgent(m.UserID)
		// Intersect the member's team memberships with this org's teams.
		if tms, terr := p.client.Team.ListMembersForUser(m.UserID, 0, 200); terr == nil {
			for _, tm := range tms {
				if tm == nil || tm.DeleteAt != 0 {
					continue
				}
				if name, ok := orgTeamName[tm.TeamId]; ok {
					em.Teams = append(em.Teams, memberTeam{
						TeamID:      tm.TeamId,
						DisplayName: name,
						TeamAdmin:   teamMemberIsAdmin(tm),
					})
				}
			}
		}
		out = append(out, em)
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (p *Plugin) handleAddOrgMember(w http.ResponseWriter, r *http.Request) {
	orgID := mux.Vars(r)["org_id"]
	adminID, ok := p.requireOrgAdmin(w, r, orgID)
	if !ok {
		return
	}
	var req addMemberRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !model.IsValidId(req.UserID) {
		p.writeErr(w, http.StatusBadRequest, "user_id is required", nil)
		return
	}
	if _, err := p.client.User.Get(req.UserID); err != nil {
		p.writeErr(w, http.StatusBadRequest, "no such user", nil)
		return
	}
	role := req.Role
	if role != OrgRoleAdmin {
		role = OrgRoleMember
	}
	if err := p.store.AddMember(orgID, req.UserID, role, adminID); err != nil {
		if errors.Is(err, ErrOrgConflict) {
			p.writeErr(w, http.StatusConflict, "that user already belongs to another organization", nil)
			return
		}
		p.writeErr(w, http.StatusInternalServerError, "could not add member", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"org_id": orgID, "user_id": req.UserID, "role": role})
}

type setRoleRequest struct {
	Role string `json:"role"`
}

func (p *Plugin) handleSetOrgMemberRole(w http.ResponseWriter, r *http.Request) {
	orgID := mux.Vars(r)["org_id"]
	userID := mux.Vars(r)["user_id"]
	if _, ok := p.requireOrgAdmin(w, r, orgID); !ok {
		return
	}
	var req setRoleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := p.store.SetMemberRole(orgID, userID, req.Role); err != nil {
		if errors.Is(err, ErrOrgConflict) {
			p.writeErr(w, http.StatusBadRequest, "role must be org_admin or org_member", nil)
			return
		}
		if errors.Is(err, ErrLastAdmin) {
			p.writeErr(w, http.StatusConflict, "this is the organization's only administrator; appoint another admin before demoting this one", nil)
			return
		}
		if errors.Is(err, ErrNotFound) {
			p.notFound(w)
			return
		}
		p.writeErr(w, http.StatusInternalServerError, "could not set role", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"org_id": orgID, "user_id": userID, "role": req.Role})
}

func (p *Plugin) handleRemoveOrgMember(w http.ResponseWriter, r *http.Request) {
	orgID := mux.Vars(r)["org_id"]
	userID := mux.Vars(r)["user_id"]
	if _, ok := p.requireOrgAdmin(w, r, orgID); !ok {
		return
	}
	if err := p.store.RemoveMember(orgID, userID); err != nil {
		if errors.Is(err, ErrLastAdmin) {
			p.writeErr(w, http.StatusConflict, "this is the organization's only administrator; appoint another admin before removing this one", nil)
			return
		}
		if errors.Is(err, ErrNotFound) {
			p.notFound(w)
			return
		}
		p.writeErr(w, http.StatusInternalServerError, "could not remove member", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"org_id": orgID, "user_id": userID, "removed": true})
}

// --- org-scoped dashboard --------------------------------------------------

// handleOrgOverview is the Organization Admin's dashboard: aggregate counts
// for the org's own departments only. It reports numbers, never rows, and is
// bounded strictly to the org in the URL -- an org admin cannot read another
// company's figures.
func (p *Plugin) handleOrgOverview(w http.ResponseWriter, r *http.Request) {
	orgID := mux.Vars(r)["org_id"]
	if _, ok := p.requireOrgAdmin(w, r, orgID); !ok {
		return
	}
	org, err := p.store.GetOrg(orgID)
	if err != nil {
		p.notFound(w)
		return
	}
	teams, _ := p.store.CountOrgTeams(orgID)
	members, _ := p.store.CountOrgMembers(orgID)
	tasks, err := p.store.CountTasksByStatusForOrg(orgID)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not read task counts", err)
		return
	}
	supportOpen, supportTotal, err := p.store.CountSupportForOrg(orgID)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not read support counts", err)
		return
	}
	summaries, err := p.store.CountSummariesForOrg(orgID)
	if err != nil {
		p.writeErr(w, http.StatusInternalServerError, "could not read meeting counts", err)
		return
	}
	var taskTotal int
	for _, n := range tasks {
		taskTotal += n
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"organization": org,
		"teams":        teams,
		"members":      members,
		"tasks": map[string]any{
			"total":     taskTotal,
			"by_status": tasks,
		},
		"support": map[string]any{
			"open":  supportOpen,
			"total": supportTotal,
		},
		"meeting_summaries": summaries,
	})
}
