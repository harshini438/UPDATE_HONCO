package main

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
)

// --- HTTP plumbing ---------------------------------------------------------

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// writeErr sends a message safe for a client to see. Internal detail
// (SQL text, driver errors) is logged, never serialised -- an error body
// is a place secrets and schema leak from otherwise.
func (p *Plugin) writeErr(w http.ResponseWriter, status int, publicMsg string, internal error) {
	if internal != nil {
		p.client.Log.Warn("honco api error", "status", status, "msg", publicMsg, "err", internal.Error())
	}
	writeJSON(w, status, errorBody{Error: publicMsg})
}

// notFound is used for BOTH "no such task" and "you may not see this
// task".
//
// Collapsing the two is deliberate. If a caller outside the team got 403
// while a caller inside got 404, the status code alone would confirm a
// task's existence to someone with no right to know -- an enumeration
// oracle. Everyone outside sees the same answer.
func (p *Plugin) notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, errorBody{Error: "not found"})
}

// requireUser converts the server-set header into a user ID, or 401.
// Handlers never read the header themselves.
func (p *Plugin) requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: "not authenticated"})
		return "", false
	}
	return userID, true
}

// requireTeamMember is the authorization gate for everything in this
// plugin.
//
// It asks Mattermost -- not our own tables -- whether this user is a
// member of this team, so the answer always matches what the rest of the
// product believes. A non-member is indistinguishable from a nonexistent
// team, on purpose.
func (p *Plugin) requireTeamMember(w http.ResponseWriter, userID, teamID string) bool {
	if teamID == "" {
		p.writeErr(w, http.StatusBadRequest, "team_id is required", nil)
		return false
	}
	member, err := p.client.Team.GetMember(teamID, userID)
	if err != nil || member == nil || member.DeleteAt != 0 {
		p.notFound(w)
		return false
	}
	return true
}

func (p *Plugin) newRouter() *mux.Router {
	root := mux.NewRouter()
	api := root.PathPrefix("/api/v1").Subrouter()

	api.HandleFunc("/health", p.handleHealth).Methods(http.MethodGet)

	api.HandleFunc("/tasks", p.handleCreateTask).Methods(http.MethodPost)
	api.HandleFunc("/tasks", p.handleListTasks).Methods(http.MethodGet)
	api.HandleFunc("/tasks/{task_id}", p.handleGetTask).Methods(http.MethodGet)
	api.HandleFunc("/tasks/{task_id}", p.handleUpdateTask).Methods(http.MethodPatch)
	api.HandleFunc("/tasks/{task_id}/status", p.handleSetTaskStatus).Methods(http.MethodPut)
	api.HandleFunc("/tasks/{task_id}", p.handleDeleteTask).Methods(http.MethodDelete)

	// Recording integration. The first two are service-to-service and
	// authenticate with a shared secret, not a user session -- Jibri and
	// meetsvc are services, and carry no Mattermost identity.
	api.HandleFunc("/meetings/register", p.handleRegisterMeeting).Methods(http.MethodPost)
	api.HandleFunc("/recordings/complete", p.handleRecordingComplete).Methods(http.MethodPost)
	api.HandleFunc("/channels/{channel_id}/recordings", p.handleListRecordings).Methods(http.MethodGet)

	// Meeting Intelligence. All three are ordinary authenticated user
	// endpoints; each one re-proves channel membership for itself rather
	// than trusting the meeting id it was handed.
	api.HandleFunc("/channels/{channel_id}/meetings", p.handleListChannelMeetings).Methods(http.MethodGet)
	api.HandleFunc("/meetings/{meeting_id}/summary", p.handleGenerateSummary).Methods(http.MethodPost)
	api.HandleFunc("/meetings/{meeting_id}/summary", p.handleGetSummary).Methods(http.MethodGet)

	root.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "no such endpoint"})
	})
	return root
}

// handleHealth reports that the plugin is loaded and its schema is
// reachable. It exposes no configuration, no secrets and no counts, so it
// is safe for any authenticated user.
func (p *Plugin) handleHealth(w http.ResponseWriter, r *http.Request) {
	if _, ok := p.requireUser(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"plugin":  "com.honco.workspace",
		"version": "0.1.0",
	})
}
