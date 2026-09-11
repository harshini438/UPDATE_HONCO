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
}

type AIStartResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status,omitempty"`
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
func (unconfiguredAIService) Health() error                    { return errAIUnconfigured }

// --- HTTP ------------------------------------------------------------------

type httpAIService struct {
	base     *url.URL
	token    string
	callback string
	client   *http.Client
}

func (h *httpAIService) StartSession(req AIStartRequest) (*AIStartResponse, error) {
	req.CallbackURL = h.callback
	var out AIStartResponse
	if err := h.do(http.MethodPost, "/v1/sessions", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (h *httpAIService) EndSession(sessionID string) error {
	if !aiSessionIDOK(sessionID) {
		return errAIRefused
	}
	return h.do(http.MethodPost, "/v1/sessions/"+url.PathEscape(sessionID)+"/end", nil, nil)
}

func (h *httpAIService) SessionStatus(sessionID string) (*AISessionStatus, error) {
	if !aiSessionIDOK(sessionID) {
		return nil, errAIRefused
	}
	var out AISessionStatus
	if err := h.do(http.MethodGet, "/v1/sessions/"+url.PathEscape(sessionID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (h *httpAIService) Transcript(sessionID string, after int64, limit int) ([]AITranscriptLine, error) {
	if !aiSessionIDOK(sessionID) {
		return nil, errAIRefused
	}
	var out struct {
		Lines []AITranscriptLine `json:"lines"`
	}
	path := fmt.Sprintf("/v1/sessions/%s/transcript?after=%d&limit=%d", url.PathEscape(sessionID), after, limit)
	if err := h.do(http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Lines, nil
}

func (h *httpAIService) Summary(sessionID string) (*AIFinal, error) {
	if !aiSessionIDOK(sessionID) {
		return nil, errAIRefused
	}
	var out AIFinal
	if err := h.do(http.MethodGet, "/v1/sessions/"+url.PathEscape(sessionID)+"/summary", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (h *httpAIService) Health() error {
	return h.do(http.MethodGet, "/v1/health", nil, nil)
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
