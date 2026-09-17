package main

import (
	"errors"
)

// backfillOrganizations brings the organization layer up to a consistent
// state at activation. It is idempotent: it creates the default company
// once, maps every existing team into it without disturbing a team already
// mapped, adds every existing team member as an organization member, and
// promotes existing System Admins to Organization Admins. Running it again
// changes nothing.
//
// It is deliberately non-fatal: an error here logs and returns nil so a
// hiccup building the roster can never take Tasks, Meetings, Files, Search,
// Support or the AI Assistant offline. In that degraded state DM resolution
// falls open (same behaviour as before this layer existed).
func (p *Plugin) backfillOrganizations() error {
	// 1. Ensure the default organization ("Honco") exists, and remember its
	//    id for org resolution and the DM guard fallback.
	org, err := p.store.GetOrgBySlug(defaultOrgSlug)
	if errors.Is(err, ErrNotFound) {
		org = &Organization{
			Slug:        defaultOrgSlug,
			Name:        defaultOrgName,
			DisplayName: defaultOrgName,
		}
		if cerr := p.store.CreateOrg(org); cerr != nil {
			if errors.Is(cerr, ErrOrgConflict) {
				// Another node won the race; adopt its row.
				org, err = p.store.GetOrgBySlug(defaultOrgSlug)
				if err != nil {
					p.client.Log.Warn("honco org: could not read default organization", "err", err.Error())
					return nil
				}
			} else {
				p.client.Log.Warn("honco org: could not create default organization", "err", cerr.Error())
				return nil
			}
		}
	} else if err != nil {
		p.client.Log.Warn("honco org: could not look up default organization", "err", err.Error())
		return nil
	}
	p.defaultOrgID = org.ID

	// 2. Map every existing team into the default org (idempotent: a team
	//    already mapped, to any org, is left exactly as it is).
	teams, aerr := p.API.GetTeams()
	if aerr != nil {
		p.client.Log.Warn("honco org: could not list teams for backfill", "err", aerr.Error())
		return nil
	}
	mapped := 0
	for _, t := range teams {
		if err := p.store.MapTeamIfAbsent(org.ID, t.Id, ""); err != nil {
			p.client.Log.Warn("honco org: could not map team", "team_id", t.Id, "err", err.Error())
			continue
		}
		mapped++
	}

	// 3. Add every existing team member to the org, promoting System Admins
	//    to Organization Admins. Users already in an org are left untouched
	//    (AddMemberIfAbsent / EnsureAdmin respect the one-org rule).
	seen := map[string]bool{}
	members, admins := 0, 0
	for _, t := range teams {
		page := 0
		for {
			tms, merr := p.API.GetTeamMembers(t.Id, page, 200)
			if merr != nil || len(tms) == 0 {
				break
			}
			for _, tm := range tms {
				if tm.DeleteAt != 0 || seen[tm.UserId] {
					continue
				}
				seen[tm.UserId] = true
				if p.client.User.HasPermissionTo(tm.UserId, permissionManageSystem()) {
					if err := p.store.EnsureAdmin(org.ID, tm.UserId, ""); err != nil {
						p.client.Log.Warn("honco org: could not set org admin", "user_id", tm.UserId, "err", err.Error())
						continue
					}
					admins++
				} else {
					if err := p.store.AddMemberIfAbsent(org.ID, tm.UserId, OrgRoleMember, ""); err != nil {
						p.client.Log.Warn("honco org: could not add org member", "user_id", tm.UserId, "err", err.Error())
						continue
					}
					members++
				}
			}
			if len(tms) < 200 {
				break
			}
			page++
		}
	}

	p.client.Log.Info("Honco organizations backfilled",
		"org", org.Slug, "teams_mapped", mapped, "members_added", members, "admins_added", admins)
	return nil
}
