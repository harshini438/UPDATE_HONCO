package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AIIntegrationService is Honco's view of the teammate's AI service: the
// calls Honco makes TO it. The push direction (service -> Honco) is the
// /api/v1/ai/events endpoint in ai_api.go and needs nothing here.
//
// Two implementations:
//
//	unconfiguredAIService  when no service URL is set. Every call returns
//	                       errAIUnconfigured, which the API turns into a
//	                       clear "not configured" state -- never a fake
//	                       success.
//	httpAIService          when a URL is set. Speaks the contract in
//	                       AI_INTEGRATION.md. That contract is Honco's
//	                       PROPOSAL: at the time of writing the service
//	                       was not reachable from this deployment, so the
//	                       paths below are documented for the service
//	                       owner to confirm or for this file to be
//	                       adjusted to. Nothing else in the plugin depends
//	                       on their exact shape.
type AIIntegrationService interface {
	// StartSession asks the service to attach to a Honco meeting.
	StartSession(req AIStartRequest) (*AIStartResponse, error)
	// EndSession tells the service the meeting is over.
	EndSession(sessionID string) error
	// SessionStatus reads the service's view of a session.
	SessionStatus(sessionID string) (*AISessionStatus, error)
	// Transcript fetches transcript lines the service holds beyond what
	// Honco kept, oldest first, starting after `after`.
	Transcript(sessionID string, after int64, limit int) ([]AITranscriptLine, error)
	// Summary fetches the post-call outputs.
	Summary(sessionID string) (*AIFinal, error)
	// Suggestions fetches the live co-pilot suggestions produced so far,
	// oldest first. token is the per-meeting copilot token returned by
	// StartSession; it is held server-side only. Returns an empty slice when
	// the co-pilot is off or has produced nothing yet.
	Suggestions(sessionID, token string) ([]AISuggestion, error)
	// Health is a cheap reachability probe for the admin dashboard.
	Health() error
}

type AIStartRequest struct {
	MeetingID    string   `json:"meeting_id"`
	RoomName     string   `json:"room_name"`
	ChannelID    string   `json:"channel_id"`
	Topic        string   `json:"topic,omitempty"`
	StartedAt    int64    `json:"started_at,omitempty"`
	Participants []string `json:"participants,omitempty"`
	// Where the service should push events. Sent so the service does not
	// have to be configured with Honco's address separately.
	CallbackURL string `json:"callback_url,omitempty"`
	// The join URL the bot opens in its browser. Required by the bot; set
	// by ai.go from the same joinURL the meeting card uses.
	MeetingURL string `json:"meeting_url,omitempty"`
	// Who the live co-pilot should advise (by display name). ai.go passes the
	// meeting's host so the Stage 2 suggestions are addressed to them; empty
	// is allowed (the bot then treats everyone as the customer).
	Salesman []string `json:"salesman,omitempty"`
}

type AIStartResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status,omitempty"`
	// CopilotToken authorizes the live-suggestion endpoints for this meeting.
	// Held server-side only; never sent to the browser.
	CopilotToken string `json:"copilot_token,omitempty"`
}

type AISessionStatus struct {
	SessionID     string `json:"session_id"`
	Status        string `json:"status"`
	CaptureStatus string `json:"capture_status,omitempty"`
}

// Sentinel errors. The API maps these to states; the text never reaches a
// browser.
var (
	errAIUnconfigured = errors.New("ai service not configured")
	errAIUnavailable  = errors.New("ai service unavailable")
	errAITimeout      = fmt.Errorf("%w: timed out", errAIUnavailable)
	errAIAuth         = errors.New("ai service rejected credentials")
	errAIRefused      = errors.New("ai service refused the request")
)

// aiErrorKind reduces an error to the class the panel shows.
func aiErrorKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errAIUnconfigured):
		return "not_configured"
	case errors.Is(err, errAITimeout):
		return "timeout"
	case errors.Is(err, errAIAuth):
		return "auth"
	case errors.Is(err, errAIRefused):
		return "refused"
	default:
		return "unreachable"
	}
}

// aiService picks the implementation for the current configuration.
// Re-read on every call so a System Console change applies immediately.
func (p *Plugin) aiService() AIIntegrationService {
	c := p.config()
	base, err := aiBaseURL(c.AIServiceURL)
	if err != nil {
		if c.AIServiceURL != "" {
			p.client.Log.Warn("honco ai: service URL rejected", "err", err.Error())
		}
		return unconfiguredAIService{}
	}
	return &httpAIService{
		base:     base,
		token:    c.AIServiceToken,
		callback: p.aiCallbackURL(),
		client: &http.Client{
			Timeout: aiRequestTimeout,
			// A redirect could send the token to another host. Refuse.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("redirects are not followed")
			},
		},
	}
}

// aiCallbackURL is where the service should push events: this plugin's
// own endpoint under the site URL. Empty when SiteURL is unset, in which
// case the service must be told the address by hand.
func (p *Plugin) aiCallbackURL() string {
	site := ""
	if cfg := p.client.Configuration.GetConfig(); cfg != nil && cfg.ServiceSettings.SiteURL != nil {
		site = strings.TrimRight(*cfg.ServiceSettings.SiteURL, "/")
	}
	if site == "" {
		return ""
	}
	return site + "/plugins/" + PluginID + "/api/v1/ai/events"
}

const (
	aiRequestTimeout = 15 * time.Second
	aiMaxResponse    = 4 << 20
)

// aiBaseURL validates the configured service URL. Only an administrator
// sets it (System Console), never a user, and it is the only URL this
// plugin ever fetches for the AI feature -- which is the whole SSRF story:
// no request path takes a URL from a browser or from the service's own
// events (transcript_ref is opaque and is only ever sent back to the
// same base).
func aiBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errAIUnconfigured
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid AI service URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("AI service URL must be http or https")
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("AI service URL must be a plain base URL")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

// --- not configured --------------------------------------------------------

type unconfiguredAIService struct{}

func (unconfiguredAIService) StartSession(AIStartRequest) (*AIStartResponse, error) {
	return nil, errAIUnconfigured
}
func (unconfiguredAIService) EndSession(string) error { return errAIUnconfigured }
func (unconfiguredAIService) SessionStatus(string) (*AISessionStatus, error) {
	return nil, errAIUnconfigured
}
func (unconfiguredAIService) Transcript(string, int64, int) ([]AITranscriptLine, error) {
	return nil, errAIUnconfigured
}
func (unconfiguredAIService) Summary(string) (*AIFinal, error) { return nil, errAIUnconfigured }
func (unconfiguredAIService) Suggestions(string, string) ([]AISuggestion, error) {
	return nil, errAIUnconfigured
}
func (unconfiguredAIService) Health() error { return errAIUnconfigured }

// --- HTTP ------------------------------------------------------------------

type httpAIService struct {
	base     *url.URL
	token    string
	callback string
	client   *http.Client
}

// The bot's API, as it actually is (app/api/v1/meetings.py in the bot
// repository, confirmed against its live OpenAPI on 2026-09-16):
//
//	POST /api/v1/meetings/              {meeting_url, title, external_meeting_id,
//	                                     participants, sales} -> {id, status}
//	POST /api/v1/meetings/{id}/start    -> {status: "starting"}
//	POST /api/v1/meetings/{id}/stop     -> {status: "stop_requested"}; 409 if not running
//	GET  /api/v1/meetings/{id}          -> {id, status, ...}
//	GET  /api/v1/meetings/{id}/health   -> {status, live, ...}
//	GET  /api/v1/meetings/{id}/transcripts -> [TranscriptSegment]
//	GET  /api/v1/meetings/{id}/summary  -> MeetingSummaryResponse
//	GET  /health
//
// The bot has no authentication; the bearer header, when a token is
// configured, is sent and ignored. The bot does NOT push events to Honco,
// so everything past StartSession is a pull -- see ai_poll.go.

const botAPI = "/api/v1/meetings"

// botCreateRequest is the bot's MeetingCreate schema. Honco sends
// sales.enabled=false: this integration is the Stage 1 notetaker only, and
// disabling the co-pilot per meeting keeps the bot from needing a Gemini
// key for something Honco is not yet consuming.
type botCreateRequest struct {
	MeetingURL        string   `json:"meeting_url"`
	Title             string   `json:"title,omitempty"`
	ExternalMeetingID string   `json:"external_meeting_id,omitempty"`
	Participants      []string `json:"participants,omitempty"`
	Sales             struct {
		Enabled  bool     `json:"enabled"`
		Salesman []string `json:"salesman,omitempty"`
		Client   []string `json:"client,omitempty"`
	} `json:"sales"`
}

type botMeeting struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	CopilotToken string `json:"copilot_token,omitempty"`
}

type botHealth struct {
	Status string `json:"status"`
	Live   bool   `json:"live"`
}

// botTranscriptSegment is the bot's TranscriptSegment. Only what Honco
// renders is decoded.
type botTranscriptSegment struct {
	Sequence  int64   `json:"sequence"`
	Text      string  `json:"text"`
	IsFinal   bool    `json:"is_final"`
	Speaker   string  `json:"speaker"`
	StartTime float64 `json:"start_time"`
	CreatedAt string  `json:"created_at"`
}

// botSummary is the bot's MeetingSummaryResponse. The list fields are
// untyped on the bot side (an item may be a string or an object such as
// {task, owner, due}), so they are decoded as any and rendered by
// botItemText.
type botSummary struct {
	Summary             string `json:"summary"`
	Participants        []any  `json:"participants"`
	Decisions           []any  `json:"decisions"`
	ActionItems         []any  `json:"action_items"`
	DiscussionPoints    []any  `json:"discussion_points"`
	UnresolvedQuestions []any  `json:"unresolved_questions"`
	ModelUsed           string `json:"model_used"`
}

// mapBotStatus turns the bot's MeetingStatus into Honco's session status.
// ANALYZING is reported as ended rather than completed: the call is over
// but the summary is not there yet, and "completed" is what tells the
// panel to show one.
func mapBotStatus(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "SCHEDULED", "STARTING":
		return AIStatusConnecting
	case "ACTIVE":
		return AIStatusLive
	case "STOPPING", "ANALYZING", "CANCELLED":
		return AIStatusEnded
	case "COMPLETED":
		return AIStatusCompleted
	case "FAILED":
		return AIStatusFailed
	}
	return ""
}

func (h *httpAIService) StartSession(req AIStartRequest) (*AIStartResponse, error) {
	if strings.TrimSpace(req.MeetingURL) == "" {
		// Without a URL there is nothing for the bot to join, and the bot
		// would answer 422 anyway. Refuse here so the reason is clear.
		return nil, fmt.Errorf("%w: no meeting URL to join", errAIRefused)
	}
	body := botCreateRequest{
		MeetingURL:        req.MeetingURL,
		Title:             req.Topic,
		ExternalMeetingID: req.MeetingID,
		Participants:      req.Participants,
	}
	// Stage 2 live co-pilot ON. The bot produces suggestions/insights while
	// the call runs; Honco polls them (ai_poll.go) and shows them in the AI
	// Assistant panel. req.Salesman names who to advise (the host); empty is
	// allowed. The bot returns copilot_token, which authorizes the read.
	body.Sales.Enabled = true
	body.Sales.Salesman = req.Salesman

	var created botMeeting
	if err := h.do(http.MethodPost, botAPI+"/", body, &created); err != nil {
		return nil, err
	}
	if !aiSessionIDOK(created.ID) {
		return nil, fmt.Errorf("%w: unusable meeting id", errAIRefused)
	}
	// Creating the record does not start the bot; /start does.
	if err := h.do(http.MethodPost, botAPI+"/"+url.PathEscape(created.ID)+"/start", nil, nil); err != nil {
		return nil, err
	}
	return &AIStartResponse{SessionID: created.ID, Status: AIStatusConnecting, CopilotToken: created.CopilotToken}, nil
}

// botSuggestion is one row from the bot's GET /sales/suggestions. The REST
// list serialises the suggestion text under "text" (the WebSocket envelope
// calls the same field "suggestion" -- the REST endpoint does not). Only the
// fields Honco renders are decoded.
type botSuggestion struct {
	ID       string `json:"id"`
	Priority string `json:"priority"`
	Type     string `json:"type"`
	Text     string `json:"text"`
	Reason   string `json:"reason"`
	Status   string `json:"status"`
	// The bot serialises created_at as an ISO-8601 string (FastAPI encodes
	// datetime that way), not an epoch number. Decoding it as float64 fails
	// the whole array; Honco does not use this field, so it is kept as a
	// string only so the decode succeeds.
	CreatedAt string `json:"created_at"`
}

// Suggestions fetches the live co-pilot suggestions for a meeting. The
// per-meeting copilot token authorizes the read; it is passed as a query
// parameter exactly as the bot expects, and never leaves the server.
func (h *httpAIService) Suggestions(sessionID, token string) ([]AISuggestion, error) {
	if !aiSessionIDOK(sessionID) {
		return nil, errAIRefused
	}
	if strings.TrimSpace(token) == "" {
		// No token means the co-pilot is not enabled for this session; that
		// is a normal state, not an error -- there is simply nothing to read.
		return nil, nil
	}
	// status=delivered: only the suggestions the bot actually surfaced, not
	// the ones its validator rejected.
	path := botAPI + "/" + url.PathEscape(sessionID) + "/sales/suggestions?status=delivered&token=" + url.QueryEscape(token)
	var rows []botSuggestion
	if err := h.do(http.MethodGet, path, nil, &rows); err != nil {
		return nil, err
	}
	out := make([]AISuggestion, 0, len(rows))
	for _, r := range rows {
		text := strings.TrimSpace(r.Text)
		if text == "" {
			continue
		}
		out = append(out, AISuggestion{
			ID:     r.ID,
			Kind:   strings.ToLower(strings.TrimSpace(r.Type)),
			Title:  strings.TrimSpace(r.Priority),
			Text:   text,
			Source: "copilot",
			Status: strings.TrimSpace(r.Status),
		})
	}
	return out, nil
}

// EndSession asks the bot to leave. The bot answers 409 when it is not
// running -- already stopped, or never started -- which is the state this
// call wants, so that one refusal is not an error.
func (h *httpAIService) EndSession(sessionID string) error {
	if !aiSessionIDOK(sessionID) {
		return errAIRefused
	}
	err := h.do(http.MethodPost, botAPI+"/"+url.PathEscape(sessionID)+"/stop", nil, nil)
	if err != nil && errors.Is(err, errAIRefused) && strings.Contains(err.Error(), "status 409") {
		return nil
	}
	return err
}

func (h *httpAIService) SessionStatus(sessionID string) (*AISessionStatus, error) {
	if !aiSessionIDOK(sessionID) {
		return nil, errAIRefused
	}
	var m botMeeting
	if err := h.do(http.MethodGet, botAPI+"/"+url.PathEscape(sessionID), nil, &m); err != nil {
		return nil, err
	}
	out := &AISessionStatus{SessionID: sessionID, Status: mapBotStatus(m.Status)}
	// The health route says whether the bot is actually in the call. It is
	// advisory; a failure here must not turn a known status into an error.
	var hb botHealth
	if err := h.do(http.MethodGet, botAPI+"/"+url.PathEscape(sessionID)+"/health", nil, &hb); err == nil {
		if hb.Live {
			out.CaptureStatus = "in the call"
		} else {
			out.CaptureStatus = "not in the call"
		}
	}
	return out, nil
}

// Transcript returns the bot's segments as Honco lines. The bot has no
// paging, so after/limit are applied here: the bot is the source of truth
// and Honco keeps a window, exactly as with a pushing service.
func (h *httpAIService) Transcript(sessionID string, after int64, limit int) ([]AITranscriptLine, error) {
	if !aiSessionIDOK(sessionID) {
		return nil, errAIRefused
	}
	var segs []botTranscriptSegment
	if err := h.do(http.MethodGet, botAPI+"/"+url.PathEscape(sessionID)+"/transcripts", nil, &segs); err != nil {
		return nil, err
	}
	out := make([]AITranscriptLine, 0, len(segs))
	for _, sg := range segs {
		if sg.Sequence <= after || strings.TrimSpace(sg.Text) == "" {
			continue
		}
		out = append(out, AITranscriptLine{
			Seq:     sg.Sequence,
			At:      botSegmentTime(sg),
			Speaker: sg.Speaker,
			Text:    sg.Text,
			Final:   sg.IsFinal,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// botSegmentTime prefers the segment's created_at (RFC 3339); a missing
// or unparsable value leaves At zero, which the panel renders without a
// timestamp rather than with a wrong one.
func botSegmentTime(sg botTranscriptSegment) int64 {
	if sg.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, sg.CreatedAt); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

func (h *httpAIService) Summary(sessionID string) (*AIFinal, error) {
	if !aiSessionIDOK(sessionID) {
		return nil, errAIRefused
	}
	var bs botSummary
	if err := h.do(http.MethodGet, botAPI+"/"+url.PathEscape(sessionID)+"/summary", nil, &bs); err != nil {
		return nil, err
	}
	return mapBotSummary(&bs), nil
}

// mapBotSummary fits the bot's sections into AIFinal. Unresolved questions
// have no slot of their own, so they follow the discussion points under
// their own label rather than being dropped -- a question the meeting left
// open is exactly the kind of thing a reader wants to see.
func mapBotSummary(bs *botSummary) *AIFinal {
	f := &AIFinal{
		Summary:      strings.TrimSpace(bs.Summary),
		KeyPoints:    botItemTexts(bs.DiscussionPoints),
		Decisions:    botItemTexts(bs.Decisions),
		ActionItems:  botItemTexts(bs.ActionItems),
		Participants: botItemTexts(bs.Participants),
		ReceivedAt:   nowMillis(),
	}
	for _, q := range botItemTexts(bs.UnresolvedQuestions) {
		f.KeyPoints = append(f.KeyPoints, "Open question: "+q)
	}
	if bs.ModelUsed != "" {
		f.TranscriptRef = "model:" + bs.ModelUsed
	}
	return f
}

// botItemTexts renders each untyped list item as one line. A string is
// used as-is; an object is rendered from the keys the bot uses for action
// items (task/owner/due and their synonyms), so "{task: X, owner: Y}"
// becomes "X — Y". Nothing is invented: a key that is absent is simply not
// printed.
func botItemTexts(items []any) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if t := botItemText(it); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func botItemText(it any) string {
	switch v := it.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		main := firstString(v, "task", "action", "text", "item", "title", "name", "point", "question", "decision")
		if main == "" {
			return ""
		}
		var extra []string
		if owner := firstString(v, "owner", "assignee", "who", "responsible"); owner != "" {
			extra = append(extra, owner)
		}
		if due := firstString(v, "due", "due_date", "deadline", "when"); due != "" {
			extra = append(extra, "due "+due)
		}
		if len(extra) == 0 {
			return main
		}
		return main + " — " + strings.Join(extra, ", ")
	case float64, bool:
		return fmt.Sprint(v)
	}
	return ""
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func (h *httpAIService) Health() error {
	return h.do(http.MethodGet, "/health", nil, nil)
}

// aiSessionIDOK keeps a service-issued id inside what can safely go into a
// path: no separators, no control characters, bounded.
func aiSessionIDOK(id string) bool {
	if id == "" || len(id) > aiMaxSessionIDChars {
		return false
	}
	for _, r := range id {
		if !(r == '-' || r == '_' || r == '.' || r == ':' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
}

// do performs one request against the configured base. The token goes in
// the Authorization header and nowhere else; the error text never includes
// the URL's credentials (there are none) or the response body.
func (h *httpAIService) do(method, path string, body any, out any) error {
	u := *h.base
	// path may carry a query string; keep base path prefix.
	rel, err := url.Parse(path)
	if err != nil {
		return errAIRefused
	}
	u.Path = h.base.Path + rel.Path
	u.RawQuery = rel.RawQuery

	var rdr io.Reader
	if body != nil {
		b, merr := json.Marshal(body)
		if merr != nil {
			return merr
		}
		rdr = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), aiRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "honco-chat-ai/"+PluginVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}

	res, err := h.client.Do(req)
	if err != nil {
		var ne net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
			return errAITimeout
		}
		return fmt.Errorf("%w: %v", errAIUnavailable, redactErr(err))
	}
	defer res.Body.Close()
	limited := io.LimitReader(res.Body, aiMaxResponse)

	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return errAIAuth
	case res.StatusCode >= 500:
		return fmt.Errorf("%w: status %d", errAIUnavailable, res.StatusCode)
	case res.StatusCode >= 400:
		return fmt.Errorf("%w: status %d", errAIRefused, res.StatusCode)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, limited)
		return nil
	}
	if err := json.NewDecoder(limited).Decode(out); err != nil {
		return fmt.Errorf("%w: unreadable response", errAIRefused)
	}
	return nil
}

// redactErr strips the path of any URL in a transport error, so a log line
// carries the host it could not reach and nothing that was sent to it.
func redactErr(err error) string {
	s := err.Error()
	i := strings.Index(s, "://")
	if i < 0 {
		return s
	}
	rest := s[i+3:]
	end := strings.Index(rest, "\"")
	if end < 0 {
		end = len(rest)
	}
	urlPart := rest[:end]
	k := strings.IndexAny(urlPart, "/?#")
	if k < 0 {
		return s
	}
	return s[:i+3] + urlPart[:k] + "/…" + rest[end:]
}

// PluginID and PluginVersion identify this plugin to the AI service.
const (
	PluginID      = "com.honco.workspace"
	PluginVersion = "0.1.0"
)
