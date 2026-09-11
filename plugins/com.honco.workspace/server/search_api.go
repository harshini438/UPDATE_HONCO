package main

import (
	"net/http"
	"strconv"
	"strings"
)

// The search endpoint.
//
// Shape of the request:
//
//	GET /api/v1/search?q=login&type=tasks&page=0&limit=20
//
// `type` may be all, tasks, meetings, recordings, summaries or support.
// Messages and files are deliberately absent: Mattermost already searches
// those, it does so better than this plugin could, and replacing working
// search with a worse copy of it would be a poor trade. The UI keeps
// Mattermost's own Messages and Files tabs and adds Honco's entities
// alongside them.

// searchResponse is what the browser receives.
type searchResponse struct {
	Query string        `json:"query"`
	Type  string        `json:"type"`
	Pages []*SearchPage `json:"pages"`
	// Total across the categories in this response, so the UI can say
	// "no results" without adding up pages itself.
	Total int `json:"total"`
}

// searchScope resolves what this caller may see, from Mattermost.
//
// This is the whole authorization model of the feature. It asks Mattermost
// for the caller's teams and channels rather than deriving them from
// Honco's own tables, so the answer always matches what the rest of the
// product believes -- including private channels they belong to, and
// excluding ones they do not. Every query is then confined to these ids,
// which is why no result can be unauthorized: an unauthorized row is never
// selected in the first place.
func (p *Plugin) searchScope(userID string) (*searchScope, error) {
	sc := &searchScope{UserID: userID}

	members, err := p.client.Team.ListMembersForUser(userID, 0, searchMaxTeams)
	if err != nil {
		return nil, err
	}
	seenChannel := map[string]bool{}
	for _, m := range members {
		if m == nil || m.DeleteAt != 0 {
			continue
		}
		sc.TeamIDs = append(sc.TeamIDs, m.TeamId)

		channels, err := p.client.Channel.ListForTeamForUser(m.TeamId, userID, false)
		if err != nil {
			// One unreadable team must not blank out the whole search.
			// Skipping it errs towards showing less, never more.
			continue
		}
		for _, ch := range channels {
			if ch == nil || ch.DeleteAt != 0 || seenChannel[ch.Id] {
				continue
			}
			if len(sc.ChannelIDs) >= searchMaxChannels {
				break
			}
			seenChannel[ch.Id] = true
			sc.ChannelIDs = append(sc.ChannelIDs, ch.Id)
		}
	}
	sc.SupportAgent = p.isSupportAgent(userID)
	return sc, nil
}

// searchParams is the validated request.
type searchParams struct {
	Query string
	Type  string
	Page  int
	Limit int
}

// parseSearchParams validates before anything touches the database.
//
// A too-short query is rejected rather than run: a single character
// matches most of the table, which is slow and useless in equal measure.
// A too-long one is rejected rather than truncated, because silently
// searching for something other than what was asked is worse than saying no.
func parseSearchParams(r *http.Request) (*searchParams, string) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		return nil, "a search term is required"
	}
	if len([]rune(q)) < searchMinQuery {
		return nil, "a search term must be at least 2 characters"
	}
	if len([]rune(q)) > searchMaxQuery {
		return nil, "a search term must be at most 128 characters"
	}

	t := strings.TrimSpace(r.URL.Query().Get("type"))
	if t == "" {
		t = SearchTypeAll
	}
	switch t {
	case SearchTypeAll, SearchTypeTasks, SearchTypeMeetings,
		SearchTypeRecordings, SearchTypeSummaries, SearchTypeSupport:
	default:
		return nil, "unknown search type"
	}

	// Page and limit are clamped rather than refused: a browser sending
	// limit=100000 is asking for too much, not attacking, and the useful
	// answer is the biggest page we are willing to serve.
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	if page > searchMaxPage {
		page = searchMaxPage
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = searchDefaultLimit
	}
	if limit > searchMaxLimit {
		limit = searchMaxLimit
	}

	return &searchParams{Query: q, Type: t, Page: page, Limit: limit}, ""
}

// handleSearch runs the search for one authenticated caller.
func (p *Plugin) handleSearch(w http.ResponseWriter, r *http.Request) {
	userID, ok := p.requireUser(w, r)
	if !ok {
		return
	}
	params, problem := parseSearchParams(r)
	if problem != "" {
		p.writeErr(w, http.StatusBadRequest, problem, nil)
		return
	}

	scope, err := p.searchScope(userID)
	if err != nil {
		// The caller gets a plain sentence. The detail goes to the log,
		// where an operator can see it and a user cannot.
		p.writeErr(w, http.StatusInternalServerError, "search is unavailable", err)
		return
	}
	resp := &searchResponse{Query: params.Query, Type: params.Type, Pages: []*SearchPage{}}
	if scope.empty() {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// In the "all" view each category shows a short list and says whether
	// there is more, so the user can open that one category and page
	// through it. Ordering results *across* categories would mean claiming
	// a task is more relevant than a meeting, which is not a judgement
	// this ranking can honestly make.
	types := []string{params.Type}
	limit, offset := params.Limit, params.Page*params.Limit
	if params.Type == SearchTypeAll {
		types = []string{SearchTypeTasks, SearchTypeMeetings, SearchTypeRecordings,
			SearchTypeSummaries, SearchTypeSupport}
		limit, offset = searchAllPerType, 0
	}

	for _, t := range types {
		page, err := p.searchOne(scope, t, params.Query, limit, offset)
		if err != nil {
			p.writeErr(w, http.StatusInternalServerError, "search is unavailable", err)
			return
		}
		page.Page = params.Page
		if params.Type == SearchTypeAll {
			page.Page = 0
		}
		resp.Pages = append(resp.Pages, page)
		resp.Total += page.Total
	}
	writeJSON(w, http.StatusOK, resp)
}

// searchOne runs a single category and wraps it as a page.
func (p *Plugin) searchOne(sc *searchScope, t, query string, limit, offset int) (*SearchPage, error) {
	var (
		hits  []*SearchHit
		total int
		err   error
	)
	switch t {
	case SearchTypeTasks:
		hits, total, err = p.store.SearchTasks(sc, query, limit, offset)
	case SearchTypeMeetings:
		hits, total, err = p.store.SearchMeetings(sc, query, limit, offset)
	case SearchTypeRecordings:
		hits, total, err = p.store.SearchRecordings(sc, query, limit, offset)
	case SearchTypeSummaries:
		hits, total, err = p.store.SearchSummaries(sc, query, limit, offset)
	case SearchTypeSupport:
		hits, total, err = p.store.SearchSupport(sc, query, limit, offset)
	default:
		// Unreachable: the type was validated before we got here.
		return &SearchPage{Type: t, Hits: []*SearchHit{}}, nil
	}
	if err != nil {
		return nil, err
	}
	return &SearchPage{
		Type:    t,
		Hits:    hits,
		Total:   total,
		Limit:   limit,
		HasMore: offset+len(hits) < total,
	}, nil
}
