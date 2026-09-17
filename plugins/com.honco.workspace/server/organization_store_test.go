package main

import (
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost/server/public/model"
)

// These exercise the real SQL against a real Postgres, but only when
// HONCO_TEST_DATABASE_URL points at a THROWAWAY database (never the live
// honcochat one). Without it they skip, so the normal `go test` stays
// dependency-free -- matching the rest of the suite.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("HONCO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set HONCO_TEST_DATABASE_URL to a throwaway database to run org store tests")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	s := NewStore(db)
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func TestOrgCreateAndUniqueSlug(t *testing.T) {
	s := testStore(t)
	o := &Organization{Slug: "acme-" + model.NewId()[:8], Name: "Acme"}
	if err := s.CreateOrg(o); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetOrg(o.ID)
	if err != nil || got.Name != "Acme" {
		t.Fatalf("get: %v %+v", err, got)
	}
	// Duplicate slug -> conflict, not an internal error.
	dup := &Organization{Slug: o.Slug, Name: "Other"}
	if err := s.CreateOrg(dup); !errors.Is(err, ErrOrgConflict) {
		t.Fatalf("duplicate slug should conflict, got %v", err)
	}
}

func TestOrgMapTeamUniqueAndIdempotent(t *testing.T) {
	s := testStore(t)
	orgA := &Organization{Slug: "a-" + model.NewId()[:8], Name: "A"}
	orgB := &Organization{Slug: "b-" + model.NewId()[:8], Name: "B"}
	_ = s.CreateOrg(orgA)
	_ = s.CreateOrg(orgB)
	team := model.NewId()

	if err := s.MapTeam(orgA.ID, team, "sys"); err != nil {
		t.Fatalf("map: %v", err)
	}
	// A team belongs to exactly one org: mapping it again (even to B) conflicts.
	if err := s.MapTeam(orgB.ID, team, "sys"); !errors.Is(err, ErrOrgConflict) {
		t.Fatalf("team can only belong to one org, got %v", err)
	}
	// Idempotent form leaves the existing mapping untouched.
	if err := s.MapTeamIfAbsent(orgB.ID, team, "sys"); err != nil {
		t.Fatalf("ifabsent: %v", err)
	}
	if got, _ := s.OrgIDForTeam(team); got != orgA.ID {
		t.Fatalf("mapping changed: %s want %s", got, orgA.ID)
	}
}

func TestOrgOneActiveOrgPerUser(t *testing.T) {
	s := testStore(t)
	orgA := &Organization{Slug: "a-" + model.NewId()[:8], Name: "A"}
	orgB := &Organization{Slug: "b-" + model.NewId()[:8], Name: "B"}
	_ = s.CreateOrg(orgA)
	_ = s.CreateOrg(orgB)
	user := model.NewId()

	if err := s.AddMember(orgA.ID, user, OrgRoleMember, "sys"); err != nil {
		t.Fatalf("add: %v", err)
	}
	// One active org per user: joining B while active in A is refused.
	if err := s.AddMember(orgB.ID, user, OrgRoleMember, "sys"); !errors.Is(err, ErrOrgConflict) {
		t.Fatalf("second org should conflict, got %v", err)
	}
	// Re-adding to the SAME org is idempotent and may set the role.
	if err := s.AddMember(orgA.ID, user, OrgRoleAdmin, "sys"); err != nil {
		t.Fatalf("re-add same org: %v", err)
	}
	if role, _ := s.OrgRole(orgA.ID, user); role != OrgRoleAdmin {
		t.Fatalf("role not promoted: %q", role)
	}
	if org, _ := s.ActiveOrgForUser(user); org != orgA.ID {
		t.Fatalf("active org: %s", org)
	}
	// Appoint a second admin so removing `user` is not blocked by the
	// last-admin guard (which is exercised by its own test).
	_ = s.AddMember(orgA.ID, model.NewId(), OrgRoleAdmin, "sys")
	// Remove, then the user is free to join another org.
	if err := s.RemoveMember(orgA.ID, user); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if org, _ := s.ActiveOrgForUser(user); org != "" {
		t.Fatalf("should have no active org after removal: %s", org)
	}
	if err := s.AddMember(orgB.ID, user, OrgRoleMember, "sys"); err != nil {
		t.Fatalf("join after leaving: %v", err)
	}
}

func TestOrgEnsureAdminAndSetRole(t *testing.T) {
	s := testStore(t)
	org := &Organization{Slug: "e-" + model.NewId()[:8], Name: "E"}
	_ = s.CreateOrg(org)
	admin := model.NewId()
	if err := s.EnsureAdmin(org.ID, admin, "sys"); err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	if role, _ := s.OrgRole(org.ID, admin); role != OrgRoleAdmin {
		t.Fatalf("ensure admin role: %q", role)
	}
	// A second admin so the demotion below is not blocked by the last-admin
	// guard (covered separately by TestOrgLastAdminProtected).
	_ = s.AddMember(org.ID, model.NewId(), OrgRoleAdmin, "sys")
	if err := s.SetMemberRole(org.ID, admin, OrgRoleMember); err != nil {
		t.Fatalf("demote: %v", err)
	}
	if role, _ := s.OrgRole(org.ID, admin); role != OrgRoleMember {
		t.Fatalf("demoted role: %q", role)
	}
	if err := s.SetMemberRole(org.ID, admin, "superuser"); !errors.Is(err, ErrOrgConflict) {
		t.Fatalf("bad role should be rejected, got %v", err)
	}
}

// The last active org_admin cannot be demoted or removed -- the company
// must never be left without an administrator.
func TestOrgLastAdminProtected(t *testing.T) {
	s := testStore(t)
	org := &Organization{Slug: "la-" + model.NewId()[:8], Name: "LA"}
	_ = s.CreateOrg(org)
	a1, a2, plain := model.NewId(), model.NewId(), model.NewId()
	if err := s.AddMember(org.ID, a1, OrgRoleAdmin, "sys"); err != nil {
		t.Fatalf("add a1: %v", err)
	}
	_ = s.AddMember(org.ID, plain, OrgRoleMember, "sys")

	// a1 is the only admin: demote and remove must both be refused.
	if err := s.SetMemberRole(org.ID, a1, OrgRoleMember); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demoting last admin should be refused, got %v", err)
	}
	if err := s.RemoveMember(org.ID, a1); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("removing last admin should be refused, got %v", err)
	}
	// Appoint a second admin; now a1 can be demoted.
	if err := s.AddMember(org.ID, a2, OrgRoleAdmin, "sys"); err != nil {
		t.Fatalf("add a2: %v", err)
	}
	if err := s.SetMemberRole(org.ID, a1, OrgRoleMember); err != nil {
		t.Fatalf("demote with two admins should work: %v", err)
	}
	// a2 is now the last admin: removal refused again.
	if err := s.RemoveMember(org.ID, a2); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("a2 is now the only admin; removal should be refused, got %v", err)
	}
}

// The core isolation guarantee at the data layer: an org's aggregate counts
// include ONLY rows belonging to its own teams, never another org's.
func TestOrgAggregatesAreIsolated(t *testing.T) {
	s := testStore(t)
	orgA := &Organization{Slug: "a-" + model.NewId()[:8], Name: "A"}
	orgB := &Organization{Slug: "b-" + model.NewId()[:8], Name: "B"}
	_ = s.CreateOrg(orgA)
	_ = s.CreateOrg(orgB)
	teamA, teamB := model.NewId(), model.NewId()
	_ = s.MapTeam(orgA.ID, teamA, "sys")
	_ = s.MapTeam(orgB.ID, teamB, "sys")

	// Two tasks in A's team, one in B's team.
	insTask := func(team, status string) {
		id := model.NewId()
		_, err := s.db.Exec(`INSERT INTO honco_tasks
			(id, team_id, creator_id, assignee_id, title, description, status, due_at, created_at, updated_at, deleted_at)
			VALUES ($1,$2,$3,'','t','',$4,0,$5,$5,0)`, id, team, model.NewId(), status, nowMillis())
		if err != nil {
			t.Fatalf("insert task: %v", err)
		}
	}
	insTask(teamA, "open")
	insTask(teamA, "done")
	insTask(teamB, "open")

	aCounts, err := s.CountTasksByStatusForOrg(orgA.ID)
	if err != nil {
		t.Fatalf("count A: %v", err)
	}
	if aCounts["open"] != 1 || aCounts["done"] != 1 {
		t.Fatalf("org A should see only its 2 tasks: %+v", aCounts)
	}
	bCounts, _ := s.CountTasksByStatusForOrg(orgB.ID)
	if bCounts["open"] != 1 || bCounts["done"] != 0 {
		t.Fatalf("org B should see only its 1 task: %+v", bCounts)
	}

	// A support request in each team.
	insSupport := func(team, status string) {
		_, err := s.db.Exec(`INSERT INTO honco_support_requests
			(id, team_id, requester_id, status, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$5)`, model.NewId(), team, model.NewId(), status, nowMillis())
		if err != nil {
			t.Fatalf("insert support: %v", err)
		}
	}
	insSupport(teamA, "open")
	insSupport(teamA, "ended")
	insSupport(teamB, "open")
	openA, totalA, err := s.CountSupportForOrg(orgA.ID)
	if err != nil {
		t.Fatalf("support A: %v", err)
	}
	if totalA != 2 || openA != 1 {
		t.Fatalf("org A support open=%d total=%d want 1/2", openA, totalA)
	}
	_, totalB, _ := s.CountSupportForOrg(orgB.ID)
	if totalB != 1 {
		t.Fatalf("org B support total=%d want 1", totalB)
	}

	// A meeting summary in A's team only.
	_, err = s.db.Exec(`INSERT INTO honco_meeting_summaries
		(id, meeting_id, channel_id, team_id, status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,'ready',$5,$5)`, model.NewId(), model.NewId(), model.NewId(), teamA, nowMillis())
	if err != nil {
		t.Fatalf("insert summary: %v", err)
	}
	if n, _ := s.CountSummariesForOrg(orgA.ID); n != 1 {
		t.Fatalf("org A summaries=%d want 1", n)
	}
	if n, _ := s.CountSummariesForOrg(orgB.ID); n != 0 {
		t.Fatalf("org B summaries=%d want 0", n)
	}
}
