package main

import (
	"regexp"
	"strings"
)

// The Organization (Company) layer.
//
// An Organization sits above Mattermost teams: a team is a department, an
// organization is the company that owns several departments. Honco's
// business data (tasks, meetings, summaries, support) is already scoped to
// a team or a channel, and every team belongs to exactly one organization
// (honco_org_teams, UNIQUE team_id), so organization isolation is inherited
// from the team boundary the plugin already enforces -- this layer adds a
// grouping, a roster with an org-admin role, and an org-scoped dashboard,
// without changing how any existing resource is stored or authorized.
//
// One active organization per user is enforced in the store for now. The
// schema can already hold a user in several organizations, so lifting that
// rule later is a code change, not a migration.

const (
	OrgRoleAdmin  = "org_admin"
	OrgRoleMember = "org_member"

	OrgStatusActive = "active"

	// defaultOrgSlug / defaultOrgName identify the organization every
	// existing team is mapped into at first activation, and the one an
	// as-yet-unmapped team or user is treated as belonging to during the
	// backfill/transition window (an approved decision: it keeps legitimate
	// same-company DMs from being blocked before the roster is fully built).
	defaultOrgSlug = "honco"
	defaultOrgName = "Honco"
)

// Organization is one company.
type Organization struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// OrgMember is a user's place in an organization.
type OrgMember struct {
	OrgID     string `json:"org_id"`
	UserID    string `json:"user_id"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	AddedBy   string `json:"added_by"`
	AddedAt   int64  `json:"added_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// OrgTeam is one department's membership of a company.
type OrgTeam struct {
	OrgID   string `json:"org_id"`
	TeamID  string `json:"team_id"`
	AddedBy string `json:"added_by"`
	AddedAt int64  `json:"added_at"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`)

// slugify turns a company name into a candidate slug. It is only a
// starting point; the store enforces uniqueness.
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case r == ' ' || r == '-' || r == '_':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 64 {
		out = strings.Trim(out[:64], "-")
	}
	return out
}

func validOrgSlug(s string) bool {
	return slugRe.MatchString(s)
}

// trimTo trims surrounding whitespace and caps length, for names a client
// supplies. Rune-safe so a multibyte name is never cut mid-character.
func trimTo(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > max {
		r = r[:max]
	}
	return string(r)
}

// orgIDForTeam resolves a team to its organization, falling back to the
// default organization for a team that has not been mapped yet. The
// fallback is the approved transition behaviour: a team created outside the
// org API is treated as part of the default company until it is explicitly
// mapped, rather than being orphaned.
func (p *Plugin) orgIDForTeam(teamID string) (string, error) {
	if teamID == "" {
		return p.defaultOrgID, nil
	}
	orgID, err := p.store.OrgIDForTeam(teamID)
	if err != nil {
		return "", err
	}
	if orgID == "" {
		return p.defaultOrgID, nil
	}
	return orgID, nil
}

// orgIDForUser resolves a user to their active organization, falling back
// to the default organization for a user who has no membership row yet
// (the transition window). A guest is still resolved here; DM policy is
// applied separately.
func (p *Plugin) orgIDForUser(userID string) (string, error) {
	orgID, err := p.store.ActiveOrgForUser(userID)
	if err != nil {
		return "", err
	}
	if orgID == "" {
		return p.defaultOrgID, nil
	}
	return orgID, nil
}

// isOrgAdmin is the org-scoped authorization primitive. A System Admin is
// treated as an admin of every organization (they can already manage
// everything through the System Console); an Org Admin is one only for
// organizations they hold the role in.
func (p *Plugin) isOrgAdmin(userID, orgID string) bool {
	if p.client.User.HasPermissionTo(userID, permissionManageSystem()) {
		return true
	}
	role, err := p.store.OrgRole(orgID, userID)
	if err != nil {
		return false
	}
	return role == OrgRoleAdmin
}
