package main

import (
	"strings"
	"testing"
)

// The lifecycle table is the feature's safety property, so every legal and
// illegal move is asserted rather than sampled.
func TestSupportTransitions(t *testing.T) {
	legal := [][2]string{
		{SupportOpen, SupportAccepted},
		{SupportOpen, SupportRejected},
		{SupportOpen, SupportCancelled},
		{SupportAccepted, SupportActive},
		{SupportAccepted, SupportCancelled},
		{SupportAccepted, SupportEnded},
		{SupportActive, SupportEnded},
	}
	for _, m := range legal {
		if !canTransition(m[0], m[1]) {
			t.Errorf("%s -> %s should be allowed", m[0], m[1])
		}
	}

	// Nothing reopens. A closed request is closed; getting help again
	// means asking again.
	illegal := [][2]string{
		{SupportEnded, SupportActive},
		{SupportEnded, SupportAccepted},
		{SupportEnded, SupportOpen},
		{SupportCancelled, SupportActive},
		{SupportCancelled, SupportAccepted},
		{SupportRejected, SupportActive},
		{SupportRejected, SupportAccepted},
		// Skipping a step: a request cannot go live without being taken.
		{SupportOpen, SupportActive},
		{SupportOpen, SupportEnded},
		// Backwards.
		{SupportActive, SupportAccepted},
		{SupportAccepted, SupportOpen},
		// Nonsense.
		{SupportActive, SupportActive},
		{"", SupportAccepted},
		{SupportOpen, "banana"},
	}
	for _, m := range illegal {
		if canTransition(m[0], m[1]) {
			t.Errorf("%s -> %s must NOT be allowed", m[0], m[1])
		}
	}
}

func TestTerminalStatesAreTerminal(t *testing.T) {
	for _, terminal := range []string{SupportEnded, SupportCancelled, SupportRejected} {
		for _, to := range []string{SupportOpen, SupportAccepted, SupportActive,
			SupportEnded, SupportCancelled, SupportRejected} {
			if canTransition(terminal, to) {
				t.Errorf("%s is terminal but allowed a move to %s", terminal, to)
			}
		}
	}
}

func TestValidSupportStatus(t *testing.T) {
	for _, s := range []string{SupportOpen, SupportAccepted, SupportActive,
		SupportEnded, SupportCancelled, SupportRejected} {
		if !validSupportStatus(s) {
			t.Errorf("%q should be a valid status", s)
		}
	}
	for _, s := range []string{"", "OPEN", "deleted", "active ", "drop table"} {
		if validSupportStatus(s) {
			t.Errorf("%q must not be a valid status", s)
		}
	}
}

// The issue description is free text from a browser and is rendered into a
// channel post, so it is treated as hostile.
func TestSanitiseIssue(t *testing.T) {
	cases := map[string]string{
		"My screen is not connecting": "My screen is not connecting",
		"  padded  ":                  "padded",
		"":                            "",
		"line\nbreak":                 "line break",     // must not break the card
		"tab\there":                   "tab here",       //
		"`code`":                      "code",           // no formatting escapes
		"pipe|table":                  "pipetable",      // no table injection
		"@channel please":             "channel please", // never a mention
		"#town-square":                "town-square",    // never a channel link
	}
	for in, want := range cases {
		if got := sanitiseIssue(in); got != want {
			t.Errorf("sanitiseIssue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitiseIssueLength(t *testing.T) {
	got := sanitiseIssue(strings.Repeat("x", maxIssueLength*2))
	if len([]rune(got)) > maxIssueLength {
		t.Errorf("issue not capped: %d runes", len([]rune(got)))
	}
}

// The card must never carry anything that could authenticate a remote
// session -- that is the whole security posture of this feature.
func TestSupportCardCarriesNoCredential(t *testing.T) {
	props := &supportProps{
		RequestID: "r1111111111111111111111111", Status: SupportActive,
		RequesterName: "harshini", AgentName: "rahul",
		Issue: "cannot connect",
	}
	text := supportFallbackText(props)
	for _, forbidden := range []string{"password", "passwd", "token", "secret", "credential", "relay"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("the support card must never mention %q: %q", forbidden, text)
		}
	}
	if !strings.Contains(text, "harshini") || !strings.Contains(text, "rahul") {
		t.Error("the card should name the people involved")
	}
}

func TestSupportStatusLines(t *testing.T) {
	for _, s := range []string{SupportOpen, SupportAccepted, SupportActive,
		SupportEnded, SupportCancelled, SupportRejected} {
		if line := supportStatusLine(s); line == "" || line == s {
			t.Errorf("status %q should render a human line, got %q", s, line)
		}
	}
}

// Support notification kinds must not collide with any existing kind, or
// one feature's dedupe record would suppress another's.
func TestSupportKindsAreDistinct(t *testing.T) {
	all := []string{
		KindTaskAssigned, KindTaskReassigned, KindTaskCompleted, KindTaskStatus,
		KindTaskDueSoon, KindTaskOverdue, KindRecordingReady, KindRecordingFailed,
		KindMeetingSummaryReady, KindMeetingSummaryFailed,
		KindMeetingStarted, KindMeetingJoined, KindMeetingLeft, KindMeetingEnded,
		KindSupportRequested, KindSupportAccepted, KindSupportRejected,
		KindSupportStarted, KindSupportEnded, KindSupportCancelled,
	}
	seen := map[string]bool{}
	for _, k := range all {
		if seen[k] {
			t.Errorf("duplicate notification kind: %q", k)
		}
		seen[k] = true
	}
	if len(seen) != 20 {
		t.Errorf("expected 20 distinct kinds, got %d", len(seen))
	}
}
