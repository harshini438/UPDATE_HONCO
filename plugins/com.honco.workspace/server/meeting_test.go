package main

import (
	"strings"
	"testing"
)

// A display name is typed into Jitsi by whoever joined. It is not
// authenticated, so it is treated as hostile input everywhere it appears.
func TestSanitiseDisplayName(t *testing.T) {
	cases := map[string]string{
		"Harshini":            "Harshini",
		"  Rahul  ":           "Rahul",
		"":                    "",
		"a\nb":                "ab",          // newlines would break the card
		"**bold**":            "bold",        // markdown must not restyle the post
		"`code`":              "code",        //
		"@channel":            "channel",     // must never become a mention
		"#town-square":        "town-square", // nor a channel link
		"under_score|pipe~td": "underscorepipetd",
	}
	for in, want := range cases {
		if got := sanitiseDisplayName(in); got != want {
			t.Errorf("sanitiseDisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitiseDisplayNameLength(t *testing.T) {
	got := sanitiseDisplayName(strings.Repeat("x", 200))
	if len([]rune(got)) > 65 {
		t.Errorf("name not capped: %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("a truncated name should show it was truncated")
	}
}

// The occupant key is what makes a repeated poll idempotent, so it has to
// be stable and derived from the resource part of the JID.
func TestOccupantKey(t *testing.T) {
	cases := map[string]string{
		"room@conference.meet.jitsi/abc123": "abc123",
		"room@conference.meet.jitsi/focus":  "focus",
		"noresource":                        "noresource",
		"trailing/":                         "trailing/",
	}
	for in, want := range cases {
		if got := occupantKey(in); got != want {
			t.Errorf("occupantKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// Somebody who joined without typing a name still needs a label.
func TestDisplayOrAnonymous(t *testing.T) {
	if got := displayOrAnonymous(occupant{DisplayName: "Amit"}); got != "Amit" {
		t.Errorf("got %q", got)
	}
	if got := displayOrAnonymous(occupant{DisplayName: ""}); got != "Guest" {
		t.Errorf("an unnamed occupant should render as Guest, got %q", got)
	}
}

func TestHumanList(t *testing.T) {
	cases := map[int]string{0: "", 1: "**a**", 2: "**a** and **b**", 3: "**a**, **b** and **c**"}
	names := []string{"a", "b", "c"}
	for n, want := range cases {
		if got := humanList(names[:n]); got != want {
			t.Errorf("humanList(%d) = %q, want %q", n, got, want)
		}
	}
}

// The fallback text is what non-plugin clients, notifications and search
// see. It has to carry the same facts as the card.
func TestCardFallbackText(t *testing.T) {
	active := &meetingProps{
		Topic: "Daily Standup", Status: MeetingActive, CreatorName: "Harshini",
		JoinURL: "https://192.168.1.11:8443/daily-standup-abc",
		Count:   2, Participants: []string{"Harshini", "Rahul"},
	}
	got := cardFallbackText(active)
	for _, want := range []string{"Daily Standup", "Harshini", "2 participants", "Rahul", active.JoinURL} {
		if !strings.Contains(got, want) {
			t.Errorf("fallback missing %q:\n%s", want, got)
		}
	}

	// An ended meeting must not still offer a join link.
	ended := &meetingProps{Topic: "Old", Status: MeetingEnded, CreatorName: "X",
		JoinURL: "https://example/room"}
	if strings.Contains(cardFallbackText(ended), "Join the meeting") {
		t.Error("an ended meeting should not advertise a join link")
	}
}

// Actions are only offered when the underlying thing exists, so the card
// never shows a button that leads nowhere.
func TestCardFallbackActions(t *testing.T) {
	none := cardFallbackText(&meetingProps{Topic: "T", Status: MeetingEnded, CreatorName: "C"})
	if strings.Contains(none, "Recording available") || strings.Contains(none, "summary available") {
		t.Error("no recording and no summary should mean no actions")
	}

	both := cardFallbackText(&meetingProps{
		Topic: "T", Status: MeetingEnded, CreatorName: "C",
		RecordingStatus: RecordingReady, HasSummary: true,
	})
	if !strings.Contains(both, "Recording available") {
		t.Error("a ready recording should be surfaced")
	}
	if !strings.Contains(both, "summary available") {
		t.Error("an existing summary should be surfaced")
	}

	// A failed recording is not something to offer for viewing.
	failed := cardFallbackText(&meetingProps{
		Topic: "T", Status: MeetingEnded, CreatorName: "C", RecordingStatus: RecordingFailed,
	})
	if strings.Contains(failed, "Recording available") {
		t.Error("a failed recording must not be advertised as available")
	}
}

// Scheduled meetings read differently from live ones.
func TestCardFallbackScheduled(t *testing.T) {
	got := cardFallbackText(&meetingProps{
		Topic: "Retro", Status: MeetingScheduled, CreatorName: "H",
		ScheduledFor: "12 Sep 2026 10:00 UTC", JoinURL: "https://x/y",
	})
	if !strings.Contains(got, "scheduled") || !strings.Contains(got, "12 Sep 2026") {
		t.Errorf("scheduled meeting should say when: %q", got)
	}
}
