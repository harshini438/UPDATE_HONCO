package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Who is actually in a meeting, according to the meeting server.
//
// Jitsi is the only thing that knows this, and in the version deployed here
// (stable-9955) it offers exactly one supported way to ask: Prosody's
// mod_muc_size, which Jitsi ships itself. It answers two queries:
//
//	GET /room-size?room=<name>&domain=<xmpp domain>  -> {"participants": N}
//	GET /room?room=<name>&domain=<xmpp domain>       -> [{jid, email, display_name}, ...]
//
// There is deliberately no push here, because this Jitsi ships none: the
// mod_event_sync_component that would POST occupant-joined/left events is
// not in the image. So the plugin asks, on a timer, and only while a
// meeting is actually running. Browsers never poll -- they are told over
// the Mattermost WebSocket when something changes.
//
// Everything this returns is what the meeting server saw. A display name is
// a label a participant typed into Jitsi; it is NOT a Mattermost identity
// and is never treated as one.

// mucTimeout bounds a single query. Prosody is on the same host, so this is
// generous; the point is that a hung meeting server can never wedge the
// lifecycle poller.
const mucTimeout = 5 * time.Second

// errMUCUnavailable means the meeting server could not be asked. It is
// distinct from "the room is empty": one is ignorance, the other is a fact,
// and conflating them would show a real meeting as having nobody in it.
var errMUCUnavailable = errors.New("meeting server unavailable")

// occupant is one person in a Jitsi room, as Prosody reports them.
type occupant struct {
	// JID is the occupant's room JID. Its resource part is the only stable
	// per-occupant key Jitsi gives us, so it identifies a participant
	// across polls. It is never shown to anyone.
	JID string `json:"jid"`

	// DisplayName is what the participant typed into Jitsi. It may be
	// empty, may be duplicated, and is not authenticated.
	DisplayName string `json:"display_name"`

	// Email is reported by mod_muc_size but deliberately ignored: it is
	// self-asserted, it is personal data, and nothing here needs it.
	Email string `json:"email"`
}

// occupantKey is the stable identity for one occupant within one room.
//
// The full JID is room@conference.domain/NICK, where NICK is assigned per
// connection. The resource is what distinguishes two people in the same
// room, so it is the key -- and because it is opaque, it is stored rather
// than displayed.
func occupantKey(jid string) string {
	if i := strings.LastIndex(jid, "/"); i >= 0 && i+1 < len(jid) {
		return jid[i+1:]
	}
	return jid
}

// mucClient talks to Prosody's HTTP interface.
type mucClient struct {
	baseURL string
	domain  string

	// mucHost is the Host header the request must carry.
	//
	// Prosody serves each component's HTTP routes under that component's
	// own virtual host, and mod_muc_size lives on the MUC component --
	// "Serving 'muc_size' at http://muc.<domain>:5280/". A request to the
	// same port without this header reaches a different virtual host and
	// gets Prosody's generic 404, which is indistinguishable from "no such
	// room" unless you know to look for the HTML body.
	mucHost string

	http *http.Client
}

func newMUCClient(cfg configuration) *mucClient {
	base := strings.TrimRight(strings.TrimSpace(cfg.ProsodyHTTPURL), "/")
	if base == "" {
		base = defaultProsodyHTTPURL
	}
	domain := strings.TrimSpace(cfg.XMPPDomain)
	if domain == "" {
		domain = defaultXMPPDomain
	}
	return &mucClient{
		baseURL: base,
		domain:  domain,
		mucHost: "muc." + domain,
		http:    &http.Client{Timeout: mucTimeout},
	}
}

// Occupants returns everyone currently in the room.
//
// A room that does not exist is not an error: Jitsi destroys a room the
// moment the last person leaves, so "no such room" is the normal way a
// finished meeting looks. That case returns an empty list and no error, and
// the caller reads it as "the meeting is over".
func (m *mucClient) Occupants(roomName string) ([]occupant, error) {
	if !roomNameOK(roomName) {
		return nil, fmt.Errorf("%w: invalid room name", errMUCUnavailable)
	}

	q := url.Values{}
	q.Set("room", roomName)
	q.Set("domain", m.domain)
	endpoint := m.baseURL + "/room?" + q.Encode()

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errMUCUnavailable, err)
	}
	// Must be req.Host, not a header: Go writes the Host header from this
	// field and ignores Header["Host"].
	req.Host = m.mucHost

	resp, err := m.http.Do(req)
	if err != nil {
		// Connection refused / DNS / timeout: the meeting server is not
		// answering. Never reported as "nobody is here".
		var netErr net.Error
		if errors.As(err, &netErr) || strings.Contains(err.Error(), "connection refused") {
			return nil, fmt.Errorf("%w: %v", errMUCUnavailable, err)
		}
		return nil, fmt.Errorf("%w: %v", errMUCUnavailable, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// The room is gone, which means the call ended.
		return []occupant{}, nil
	default:
		return nil, fmt.Errorf("%w: prosody returned %d", errMUCUnavailable, resp.StatusCode)
	}

	var out []occupant
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: malformed response", errMUCUnavailable)
	}

	// mod_muc_size already filters the /focus occupant, but Jicofo's focus
	// user is not a participant and must never be counted or shown, so the
	// guarantee is enforced here too rather than assumed.
	cleaned := make([]occupant, 0, len(out))
	for _, o := range out {
		if o.JID == "" || strings.HasSuffix(o.JID, "/focus") {
			continue
		}
		o.DisplayName = sanitiseDisplayName(o.DisplayName)
		o.Email = "" // never carried further
		cleaned = append(cleaned, o)
	}
	return cleaned, nil
}

// sanitiseDisplayName makes a self-asserted name safe to store and render.
//
// This string comes from a text box in someone's browser, so it is treated
// as hostile: control characters and markdown that could forge a message
// are stripped, and it is length-capped. The UI renders it as text, but
// this is the layer that must not pass anything dangerous along.
func sanitiseDisplayName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f: // control characters, including newlines
			continue
		case r == '`' || r == '*' || r == '_' || r == '|' || r == '~':
			continue // markdown that could restyle or break the card
		case r == '@' || r == '#':
			continue // never let a name become a mention or a channel link
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if len([]rune(out)) > 64 {
		out = string([]rune(out)[:64]) + "…"
	}
	return out
}

// displayOrAnonymous is what the card shows for one occupant. Jitsi allows
// joining without typing a name, and an empty label would render as a gap.
func displayOrAnonymous(o occupant) string {
	if o.DisplayName == "" {
		return "Guest"
	}
	return o.DisplayName
}
