package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The channel transcript must tell the summariser it is reading a channel,
// not a meeting -- and must otherwise be identical in shape to the meeting
// one, because both are parsed by the same parseSections.
func TestBuildChannelTranscript(t *testing.T) {
	msgs := []convMessage{
		{Username: "alice", At: 1700000000000, Message: "we should ship on Friday"},
		{Username: "bob", At: 1700000060000, Message: "agreed"},
	}
	out := buildChannelTranscript("Town Square", msgs)

	if !strings.Contains(out, "Honco Chat channel") {
		t.Errorf("the summariser must be told this is a channel:\n%s", out)
	}
	if strings.Contains(out, "Honco meeting") {
		t.Errorf("a channel summary must not be described as a meeting:\n%s", out)
	}
	if !strings.Contains(out, `called "Town Square"`) {
		t.Errorf("the channel name should be named:\n%s", out)
	}
	for _, h := range []string{"## Summary", "## Key discussion points", "## Decisions", "## Action items"} {
		if !strings.Contains(out, h) {
			t.Errorf("missing requested heading %q", h)
		}
	}
	if !strings.Contains(out, "---CONVERSATION---") {
		t.Error("missing the conversation marker")
	}
	for _, m := range msgs {
		if !strings.Contains(out, m.Message) {
			t.Errorf("message not carried into the transcript: %q", m.Message)
		}
	}
}

// A direct message has no display name, so nothing is named. The output
// must still be a valid transcript.
func TestBuildChannelTranscriptWithoutAName(t *testing.T) {
	out := buildChannelTranscript("", []convMessage{{Username: "a", At: 1, Message: "hello"}})
	if strings.Contains(out, "called") {
		t.Errorf("no name should be claimed when there is none:\n%s", out)
	}
	if !strings.Contains(out, "## Summary") || !strings.Contains(out, "hello") {
		t.Errorf("transcript is malformed:\n%s", out)
	}
}

// The channel transcript honours the same character cap as the meeting
// one -- they share buildConversationText, and this proves it.
func TestBuildChannelTranscriptTruncates(t *testing.T) {
	msgs := make([]convMessage, 0, 4000)
	for i := 0; i < 4000; i++ {
		msgs = append(msgs, convMessage{Username: "u", At: int64(i), Message: strings.Repeat("x", 200)})
	}
	out := buildChannelTranscript("Big", msgs)
	if len(out) > maxTranscriptChars+400 {
		t.Errorf("transcript not capped: %d chars", len(out))
	}
	if !strings.Contains(out, "truncated for length") {
		t.Error("truncation should be stated in the transcript")
	}
}

// A failure must read as a failure. No partial text, no invented summary,
// and no transport detail.
func TestSummarizeFailureText(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errSummarizerUnavailable, "not reachable"},
		{errSummarizerTimeout, "did not respond in time"},
		{fmt.Errorf("%w: ssh: connect to host 192.168.2.150 port 31013", errSummarizerUnavailable), "not reachable"},
	}
	for _, c := range cases {
		got := summarizeFailureText(c.err)
		if !strings.Contains(strings.ToLower(got), c.want) {
			t.Errorf("summarizeFailureText(%v) = %q, want it to mention %q", c.err, got, c.want)
		}
		if !strings.Contains(got, "No summary was generated") {
			t.Errorf("a failure must say plainly that nothing was produced: %q", got)
		}
		// Nothing about the transport may reach the user.
		for _, leak := range []string{"192.168", "ssh", "31013", "database@"} {
			if strings.Contains(strings.ToLower(got), leak) {
				t.Errorf("failure text leaks transport detail %q: %q", leak, got)
			}
		}
	}
}

// An error that is neither unavailable nor a timeout still must not
// produce something that reads like a summary.
func TestSummarizeFailureTextGenericError(t *testing.T) {
	got := summarizeFailureText(errors.New("something else entirely"))
	if strings.Contains(got, "something else entirely") {
		t.Errorf("internal error text must not reach the user: %q", got)
	}
	if got == "" {
		t.Error("a failure must say something")
	}
}

// The rendered summary states Honco's own count separately from the
// model's words, so a reader can tell them apart.
func TestRenderChannelSummary(t *testing.T) {
	out := renderChannelSummary(12, "## Summary\nThey agreed to ship.")
	if !strings.Contains(out, "12 messages") {
		t.Errorf("the message count should be stated: %q", out)
	}
	if !strings.Contains(out, "They agreed to ship.") {
		t.Errorf("the model's text must be preserved: %q", out)
	}
	if !strings.HasPrefix(out, "**Channel summary**") {
		t.Errorf("the summary should be labelled as one: %q", out)
	}
	// Singular reads correctly too.
	if one := renderChannelSummary(1, "x"); !strings.Contains(one, "1 message ") && !strings.Contains(one, "1 message") {
		t.Errorf("singular message count is wrong: %q", one)
	}
}

// The per-user limiter must actually bite, or an expensive command is
// unbounded. Burst is 3.
func TestSummarizeRateLimiter(t *testing.T) {
	l := newRateLimiter(6, 3)
	allowed := 0
	for i := 0; i < 10; i++ {
		if l.allow("user-a") {
			allowed++
		}
	}
	if allowed > 4 {
		t.Errorf("limiter let %d of 10 through; burst is 3", allowed)
	}
	if allowed == 0 {
		t.Error("limiter refused everything; the first call must succeed")
	}
	// A different user has their own bucket.
	if !l.allow("user-b") {
		t.Error("one user's burst must not limit another")
	}
}
