package main

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Store methods for the organization layer. Every method is parameterized
// and scoped by construction; none reads authorization context from a
// caller-supplied value. Nothing here touches a Mattermost-owned table:
// team and channel membership are resolved through the plugin API, and the
// org tables only ever reference ids.

// ErrOrgConflict is a policy or uniqueness violation the caller can act on
// (slug taken, team already mapped, user already in another org).
var ErrOrgConflict = errors.New("honco: organization conflict")

// ErrLastAdmin is returned when removing or demoting a member would leave
// the organization with no active org_admin. The last admin cannot lock the
// company out of its own administration.
var ErrLastAdmin = errors.New("honco: last organization admin")

// --- organizations ---------------------------------------------------------

// CreateOrg inserts a company. A duplicate slug is reported as a conflict,
// not an internal error, so the API can turn it into a 409.
func (s *Store) CreateOrg(o *Organization) error {
	if o.ID == "" {
		o.ID = model.NewId()
	}
	now := nowMillis()
	if o.CreatedAt == 0 {
		o.CreatedAt = now
	}
	o.UpdatedAt = now
	if o.Status == "" {
		o.Status = OrgStatusActive
	}
	_, err := s.db.Exec(`
		INSERT INTO honco_organizations
			(id, slug, name, display_name, status, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		o.ID, o.Slug, o.Name, o.DisplayName, o.Status, o.CreatedBy, o.CreatedAt, o.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrOrgConflict
		}
		return err
	}
	return nil
}

func scanOrg(row interface{ Scan(...any) error }) (*Organization, error) {
	var o Organization
	err := row.Scan(&o.ID, &o.Slug, &o.Name, &o.DisplayName, &o.Status,
		&o.CreatedBy, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

const orgColumns = `id, slug, name, display_name, status, created_by, created_at, updated_at`

func (s *Store) GetOrg(id string) (*Organization, error) {
	return scanOrg(s.db.QueryRow(`SELECT `+orgColumns+` FROM honco_organizations WHERE id = $1`, id))
}

func (s *Store) GetOrgBySlug(slug string) (*Organization, error) {
	return scanOrg(s.db.QueryRow(`SELECT `+orgColumns+` FROM honco_organizations WHERE slug = $1`, slug))
}

func (s *Store) ListOrgs() ([]*Organization, error) {
	rows, err := s.db.Query(`SELECT ` + orgColumns + ` FROM honco_organizations ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Organization{}
	for rows.Next() {
		o, err := scanOrg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// UpdateOrg writes the mutable fields. id, slug and created_* are
// deliberately not in the SET list: a company is not renamed into another's
// slug, and its identity never changes.
func (s *Store) UpdateOrg(o *Organization) error {
	res, err := s.db.Exec(`
		UPDATE honco_organizations
		SET name = $1, display_name = $2, status = $3, updated_at = $4
		WHERE id = $5`,
		o.Name, o.DisplayName, o.Status, nowMillis(), o.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- team mapping ----------------------------------------------------------

// MapTeam ties a team to an organization. The UNIQUE index on team_id makes
// a team that already belongs to a (possibly different) organization a
// conflict rather than a silent second row -- the isolation invariant.
func (s *Store) MapTeam(orgID, teamID, addedBy string) error {
	_, err := s.db.Exec(`
		INSERT INTO honco_org_teams (org_id, team_id, added_by, added_at)
		VALUES ($1,$2,$3,$4)`, orgID, teamID, addedBy, nowMillis())
	if err != nil {
		if isUniqueViolation(err) {
			return ErrOrgConflict
		}
		return err
	}
	return nil
}

// MapTeamIfAbsent is the idempotent form used by backfill: a team already
// mapped (to any org) is left exactly as it is.
func (s *Store) MapTeamIfAbsent(orgID, teamID, addedBy string) error {
	err := s.MapTeam(orgID, teamID, addedBy)
	if errors.Is(err, ErrOrgConflict) {
		return nil
	}
	return err
}

func (s *Store) UnmapTeam(teamID string) error {
	_, err := s.db.Exec(`DELETE FROM honco_org_teams WHERE team_id = $1`, teamID)
	return err
}

// OrgIDForTeam returns the org a team is mapped to, or "" if unmapped.
func (s *Store) OrgIDForTeam(teamID string) (string, error) {
	var orgID string
	err := s.db.QueryRow(`SELECT org_id FROM honco_org_teams WHERE team_id = $1`, teamID).Scan(&orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return orgID, err
}

func (s *Store) ListTeamsForOrg(orgID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT team_id FROM honco_org_teams WHERE org_id = $1 ORDER BY added_at ASC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- membership ------------------------------------------------------------

// ActiveOrgForUser returns the single organization a user actively belongs
// to, or "" if none. With the one-org policy this is at most one row; the
// query is written to be correct even if that policy is later relaxed
// (it returns the earliest active membership).
func (s *Store) ActiveOrgForUser(userID string) (string, error) {
	var orgID string
	err := s.db.QueryRow(`
		SELECT org_id FROM honco_org_members
		WHERE user_id = $1 AND status = 'active'
		ORDER BY added_at ASC LIMIT 1`, userID).Scan(&orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return orgID, err
}

// AddMember adds a user to an organization, enforcing one active
// organization per user. Adding a user to the org they are already in is
// idempotent (their role/status is refreshed); adding a user who is active
// in a DIFFERENT org is a conflict.
func (s *Store) AddMember(orgID, userID, role, addedBy string) error {
	if role == "" {
		role = OrgRoleMember
	}
	current, err := s.ActiveOrgForUser(userID)
	if err != nil {
		return err
	}
	if current != "" && current != orgID {
		return ErrOrgConflict
	}
	now := nowMillis()
	// Upsert on (user_id, org_id): re-adding refreshes role/status without a
	// duplicate row, which is what makes backfill safe to run repeatedly.
	_, err = s.db.Exec(`
		INSERT INTO honco_org_members (org_id, user_id, role, status, added_by, added_at, updated_at)
		VALUES ($1,$2,$3,'active',$4,$5,$5)
		ON CONFLICT (user_id, org_id)
		DO UPDATE SET role = EXCLUDED.role, status = 'active', updated_at = EXCLUDED.updated_at`,
		orgID, userID, role, addedBy, now)
	return err
}

// AddMemberIfAbsent is the backfill form: it never downgrades or changes an
// existing membership, and never moves a user between orgs.
func (s *Store) AddMemberIfAbsent(orgID, userID, role, addedBy string) error {
	current, err := s.ActiveOrgForUser(userID)
	if err != nil {
		return err
	}
	if current != "" {
		return nil // already a member of some org; leave it alone
	}
	return s.AddMember(orgID, userID, role, addedBy)
}

// EnsureAdmin makes sure a user is an org_admin of an org, adding them if
// absent and promoting them if they are a plain member. Used by backfill to
// turn existing System Admins into Org Admins without disturbing anyone
// else.
func (s *Store) EnsureAdmin(orgID, userID, addedBy string) error {
	current, err := s.ActiveOrgForUser(userID)
	if err != nil {
		return err
	}
	if current != "" && current != orgID {
		// Respect the one-org rule: do not move a system admin who is
		// already active in another org.
		return nil
	}
	return s.AddMember(orgID, userID, OrgRoleAdmin, addedBy)
}

// CountOrgAdmins returns how many active org_admins the organization has.
func (s *Store) CountOrgAdmins(orgID string) (int, error) {
	var n int
	err := s.db.QueryRow(`
		SELECT count(*) FROM honco_org_members
		WHERE org_id = $1 AND role = $2 AND status = 'active'`, orgID, OrgRoleAdmin).Scan(&n)
	return n, err
}

func (s *Store) SetMemberRole(orgID, userID, role string) error {
	if role != OrgRoleAdmin && role != OrgRoleMember {
		return ErrOrgConflict
	}
	// Demoting the last org_admin would leave the company with no
	// administrator. Refuse it; the caller must appoint another admin first.
	if role == OrgRoleMember {
		cur, err := s.OrgRole(orgID, userID)
		if err != nil {
			return err
		}
		if cur == OrgRoleAdmin {
			admins, err := s.CountOrgAdmins(orgID)
			if err != nil {
				return err
			}
			if admins <= 1 {
				return ErrLastAdmin
			}
		}
	}
	res, err := s.db.Exec(`
		UPDATE honco_org_members SET role = $1, updated_at = $2
		WHERE org_id = $3 AND user_id = $4 AND status = 'active'`,
		role, nowMillis(), orgID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveMember deactivates a membership (kept as a row for audit, matching
// the plugin's soft-delete discipline elsewhere). Removing the last active
// org_admin is refused for the same reason demoting one is.
func (s *Store) RemoveMember(orgID, userID string) error {
	cur, err := s.OrgRole(orgID, userID)
	if err != nil {
		return err
	}
	if cur == OrgRoleAdmin {
		admins, err := s.CountOrgAdmins(orgID)
		if err != nil {
			return err
		}
		if admins <= 1 {
			return ErrLastAdmin
		}
	}
	res, err := s.db.Exec(`
		UPDATE honco_org_members SET status = 'removed', updated_at = $1
		WHERE org_id = $2 AND user_id = $3 AND status = 'active'`,
		nowMillis(), orgID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) OrgRole(orgID, userID string) (string, error) {
	var role string
	err := s.db.QueryRow(`
		SELECT role FROM honco_org_members
		WHERE org_id = $1 AND user_id = $2 AND status = 'active'`, orgID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return role, err
}

func (s *Store) ListMembers(orgID string) ([]*OrgMember, error) {
	rows, err := s.db.Query(`
		SELECT org_id, user_id, role, status, added_by, added_at, updated_at
		FROM honco_org_members
		WHERE org_id = $1 AND status = 'active'
		ORDER BY added_at ASC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*OrgMember{}
	for rows.Next() {
		var m OrgMember
		if err := rows.Scan(&m.OrgID, &m.UserID, &m.Role, &m.Status, &m.AddedBy, &m.AddedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// --- org-scoped aggregates for the dashboard -------------------------------
//
// Each joins an existing team-scoped table to honco_org_teams, so the count
// is exactly the rows belonging to the org's departments -- no Mattermost
// table is read, and no per-row content is returned.

func (s *Store) CountTasksByStatusForOrg(orgID string) (map[string]int, error) {
	rows, err := s.db.Query(`
		SELECT t.status, count(*)
		FROM honco_tasks t
		JOIN honco_org_teams ot ON ot.team_id = t.team_id
		WHERE ot.org_id = $1 AND t.deleted_at = 0
		GROUP BY t.status`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

func (s *Store) CountSupportForOrg(orgID string) (open, total int, err error) {
	err = s.db.QueryRow(`
		SELECT
			count(*) FILTER (WHERE r.status NOT IN ('ended','cancelled','rejected')),
			count(*)
		FROM honco_support_requests r
		JOIN honco_org_teams ot ON ot.team_id = r.team_id
		WHERE ot.org_id = $1`, orgID).Scan(&open, &total)
	return open, total, err
}

// CountSummariesForOrg counts Meeting Intelligence records for the org's
// departments. honco_meeting_summaries carries team_id (denormalised from
// the channel), so this is the meetings metric that can be joined without
// touching a Mattermost table.
func (s *Store) CountSummariesForOrg(orgID string) (int, error) {
	var n int
	err := s.db.QueryRow(`
		SELECT count(*)
		FROM honco_meeting_summaries ms
		JOIN honco_org_teams ot ON ot.team_id = ms.team_id
		WHERE ot.org_id = $1`, orgID).Scan(&n)
	return n, err
}

func (s *Store) CountOrgTeams(orgID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM honco_org_teams WHERE org_id = $1`, orgID).Scan(&n)
	return n, err
}

func (s *Store) CountOrgMembers(orgID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM honco_org_members WHERE org_id = $1 AND status = 'active'`, orgID).Scan(&n)
	return n, err
}

// isUniqueViolation reports whether an error is a Postgres unique-constraint
// violation (SQLSTATE 23505), without importing a driver-specific type: the
// message carries the code, and this keeps the store driver-agnostic like
// the rest of the plugin.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "23505") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique constraint")
}
