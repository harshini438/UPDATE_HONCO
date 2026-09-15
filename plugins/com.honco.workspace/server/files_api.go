package main

import (
	"net/http"
	"strconv"
)

// handleRecentFiles serves the Files tab.
//
// The caller's visible channels are resolved through searchScope, which
// asks Mattermost -- not this plugin's tables -- which channels the user
// is in. That is the whole authorization story: a file in a channel the
// caller is not a member of never enters the query, so it cannot be
// listed, and its id never reaches the browser to be downloaded with.
//
// The response carries no path, no download URL and no token. The client
// builds the standard /api/v4/files/{id} link from the id, and Mattermost
// authorizes that request again on its own.
func (p *Plugin) handleRecentFiles(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}

	limit := 25
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			p.writeErr(w, http.StatusBadRequest, "limit must be a positive number", nil)
			return
		}
		limit = n
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			p.writeErr(w, http.StatusBadRequest, "offset must be zero or more", nil)
			return
		}
		offset = n
	}

	scope, err := p.searchScope(userID)
	if err != nil {
		// The caller gets a plain sentence; the detail goes to the log,
		// where an operator can see it and a user cannot.
		p.writeErr(w, http.StatusInternalServerError, "files are unavailable", err)
		return
	}

	// A scope that resolved to nothing means "this user can see no
	// channel", which is an empty list -- never an unscoped query.
	files, ferr := p.store.RecentFiles(scope.ChannelIDs, limit, offset)
	if ferr != nil {
		p.writeErr(w, http.StatusInternalServerError, "files are unavailable", ferr)
		return
	}

	// has_more lets the panel show a "Load more" without a second count
	// query: a full page means there may be another.
	writeJSON(w, http.StatusOK, map[string]any{
		"files":    files,
		"has_more": len(files) == limit,
		"offset":   offset,
		"limit":    limit,
	})
}
