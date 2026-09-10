package main

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/mattermost/mattermost/server/public/model"
)

// Read-only queries behind the admin dashboard.
//
// Every number comes from the live database. Nothing is cached, denormalised
// or copied into a metrics table -- a dashboard that reports a stale copy of
// the truth is worse than no dashboard, and these are cheap counts against
// indexed columns run only when an admin opens the page.

// permissionManageSystem is the Mattermost permission that gates the System
// Console. Wrapped in a function so the dependency is stated in one place.
func permissionManageSystem() *model.Permission {
	return model.PermissionManageSystem
}

// insecureProbeTransport is used only for liveness probes.
//
// Jitsi serves a self-signed certificate on the LAN, so a verifying client
// would report a healthy service as down. Nothing is read from the response
// body, no credential is sent, and this transport is never used for
// anything but "did something answer".
func insecureProbeTransport() *http.Transport {
	return &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // liveness probe only
	}
}

// jibriStatus reads the recorder's own status document.
//
// "Running" and "able to record" are different questions: Jibri can be up
// and already busy with another session, or up and reporting itself
// unhealthy. An admin needs the second answer.
func jibriStatus() (busy bool, healthy bool, err error) {
	client := &http.Client{Timeout: healthProbeTimeout}
	resp, err := client.Get("http://127.0.0.1:2222/jibri/api/v1.0/health")
	if err != nil {
		return false, false, err
	}
	defer resp.Body.Close()

	var doc struct {
		Status struct {
			BusyStatus string `json:"busyStatus"`
			Health     struct {
				HealthStatus string `json:"healthStatus"`
			} `json:"health"`
		} `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return false, false, err
	}
	return doc.Status.BusyStatus == "BUSY",
		doc.Status.Health.HealthStatus == "HEALTHY", nil
}

// Ping verifies the database answers, and how quickly.
func (s *Store) Ping() error {
	var one int
	return s.db.QueryRow(`SELECT 1`).Scan(&one)
}

// MigrationVersions reports both schema versions.
//
// Read, never assumed: the whole point of showing them is to notice when
// they are not what anyone expected.
func (s *Store) MigrationVersions() (honco int, mattermost int, err error) {
	if err = s.db.QueryRow(
		`SELECT coalesce(max(version), 0) FROM honco_schema_migrations`).Scan(&honco); err != nil {
		return 0, 0, err
	}
	// Mattermost's own table. Read-only, and the only place this plugin
	// ever touches it.
	if err = s.db.QueryRow(`SELECT count(*) FROM db_migrations`).Scan(&mattermost); err != nil {
		return honco, 0, err
	}
	return honco, mattermost, nil
}

// AdminUsage counts what the dashboard shows.
//
// Definitions worth stating, because a number without one is not a metric:
//   - Users/Teams are those not soft-deleted.
//   - Channels excludes direct and group messages: they are conversations,
//     not channels an admin manages.
//   - Meetings active means status 'active' -- somebody is in the room now,
//     as last seen by the participant poller.
//   - Tasks excludes soft-deleted rows.
//   - Support open means still awaiting an agent.
func (s *Store) AdminUsage() (*adminUsage, error) {
	var u adminUsage
	err := s.db.QueryRow(`
		SELECT
			(SELECT count(*) FROM users    WHERE deleteat = 0),
			(SELECT count(*) FROM teams    WHERE deleteat = 0),
			(SELECT count(*) FROM channels WHERE deleteat = 0 AND type NOT IN ('D','G')),
			(SELECT count(*) FROM honco_meetings),
			(SELECT count(*) FROM honco_meetings WHERE status = 'active'),
			(SELECT count(*) FROM honco_recordings),
			(SELECT count(*) FROM honco_tasks WHERE deleted_at = 0),
			(SELECT count(*) FROM honco_meeting_summaries),
			(SELECT count(*) FROM honco_support_requests WHERE status = 'open'),
			(SELECT count(*) FROM honco_support_requests)
	`).Scan(&u.Users, &u.Teams, &u.Channels, &u.MeetingsTotal, &u.MeetingsActive,
		&u.Recordings, &u.Tasks, &u.Summaries, &u.SupportOpen, &u.SupportTotal)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// RecentFailures gathers what actually went wrong, across features.
//
// The detail strings are the ones already stored for users to read -- they
// were written to be shown, and were sanitised at the point they were
// produced (a summariser failure, for instance, carries "not reachable
// from this server", never the host or the SSH diagnostics). Nothing here
// reads a log or a stack trace.
func (s *Store) RecentFailures(limit int) ([]adminFailure, error) {
	if limit <= 0 || limit > 100 {
		limit = 15
	}
	out := []adminFailure{}

	rows, err := s.db.Query(`
		SELECT created_at, room_name, coalesce(error_message, '')
		FROM honco_recordings WHERE status = 'failed'
		ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f adminFailure
		if err := rows.Scan(&f.At, &f.Subject, &f.Detail); err != nil {
			rows.Close()
			return nil, err
		}
		f.Kind = "recording"
		out = append(out, f)
	}
	rows.Close()

	rows, err = s.db.Query(`
		SELECT s.updated_at, coalesce(m.topic, m.room_name, ''), coalesce(s.error_message, '')
		FROM honco_meeting_summaries s
		LEFT JOIN honco_meetings m ON m.id = s.meeting_id
		WHERE s.status = 'failed'
		ORDER BY s.updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f adminFailure
		if err := rows.Scan(&f.At, &f.Subject, &f.Detail); err != nil {
			rows.Close()
			return nil, err
		}
		f.Kind = "summary"
		out = append(out, f)
	}
	rows.Close()

	// A declined support request is not an error, but it is something an
	// admin looking for friction wants to see.
	rows, err = s.db.Query(`
		SELECT updated_at, coalesce(issue, ''), coalesce(reason, '')
		FROM honco_support_requests WHERE status = 'rejected'
		ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f adminFailure
		if err := rows.Scan(&f.At, &f.Subject, &f.Detail); err != nil {
			rows.Close()
			return nil, err
		}
		f.Kind = "support declined"
		out = append(out, f)
	}
	rows.Close()

	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
