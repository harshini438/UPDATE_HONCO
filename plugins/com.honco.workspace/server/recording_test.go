package main

import (
	"strings"
	"testing"
)

// checkSecret must fail closed: an unconfigured secret cannot be
// satisfied by any input, including an empty one.
func TestCheckSecret(t *testing.T) {
	const good = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	if !checkSecret(good, good) {
		t.Error("the correct secret must be accepted")
	}
	if checkSecret("", good) {
		t.Error("an empty header must be rejected")
	}
	if checkSecret(good, "") {
		t.Error("an unconfigured secret must reject everything (fail closed)")
	}
	if checkSecret("", "") {
		t.Error("empty vs empty must NOT be treated as a match")
	}
	if checkSecret(good+"x", good) || checkSecret(good[:63], good) {
		t.Error("length differences must be rejected")
	}
	if checkSecret(strings.Repeat("a", 64), good) {
		t.Error("a wrong secret of the same length must be rejected")
	}
}

// roomNameOK is what stops a hostile room name reaching a query or a post.
func TestRoomNameOK(t *testing.T) {
	valid := []string{
		"honco-live-recording-a2d0b7",
		"standup-a3f9c1",
		"honco-21dea015ef20176c1da90cc1bfbecaa6",
		"a", "A1_b-c.d",
	}
	for _, s := range valid {
		if !roomNameOK(s) {
			t.Errorf("%q should be a valid room name", s)
		}
	}

	invalid := []string{
		"",                       // empty
		"../etc/passwd",          // traversal
		"room/../other",          // traversal
		"room name",              // space
		"room;DROP TABLE posts",  // sql-ish
		"room\nname",             // newline
		"room<script>",           // markup
		"room'or'1'='1",          // quotes
		"room%2e%2e",             // encoded traversal
		strings.Repeat("a", 256), // too long
	}
	for _, s := range invalid {
		if roomNameOK(s) {
			t.Errorf("%q should be REJECTED as a room name", s)
		}
	}
}

// A filename arrives from outside this server and must never be able to
// influence a path.
func TestSanitiseFileName(t *testing.T) {
	cases := map[string]string{
		"meeting.mp4":               "meeting.mp4",
		"  spaced.mp4  ":            "spaced.mp4",
		"/etc/passwd":               "passwd",
		`C:\windows\system32\x.mp4`: "x.mp4",
		"../../../etc/shadow":       "shadow",
		// ".." is stripped wherever it appears, including inside an
		// encoded traversal attempt -- deliberately more aggressive than
		// only removing a leading path.
		"..%2f..%2fx.mp4": "%2f%2fx.mp4",
		"":                "",
		"   ":             "",
	}
	for in, want := range cases {
		if got := sanitiseFileName(in); got != want {
			t.Errorf("sanitiseFileName(%q) = %q, want %q", in, got, want)
		}
	}

	// No result may ever contain a path separator or "..".
	for _, in := range []string{"/a/b/c.mp4", `a\b\c.mp4`, "../../x", "..", "a/../../b.mp4"} {
		got := sanitiseFileName(in)
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("sanitiseFileName(%q) = %q, which still contains a separator", in, got)
		}
		if strings.Contains(got, "..") {
			t.Errorf("sanitiseFileName(%q) = %q, which still contains '..'", in, got)
		}
	}

	// A control character makes the whole name unusable.
	if sanitiseFileName("bad\x00name.mp4") != "" {
		t.Error("a name containing a control character must be rejected outright")
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		41:              "41 bytes",
		1024:            "1.0 KB",
		219412:          "214.3 KB",
		5 * 1024 * 1024: "5.0 MB",
		3 * 1073741824:  "3.0 GB",
	}
	for in, want := range cases {
		if got := humanSize(in); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("short strings must pass through, got %q", got)
	}
	if got := truncate(strings.Repeat("a", 50), 10); len(got) != 10 {
		t.Errorf("expected truncation to 10, got %d", len(got))
	}
}
