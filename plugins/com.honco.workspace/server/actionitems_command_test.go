package main

import (
	"strings"
	"testing"
)

const goodReply = `## Action items
- Action: Write the migration runbook
  Owner: bob
  Due: Thursday
  Context: bob said he would write it before the release
- Action: Ask the security team about GemSetu
  Owner: Not specified
  Due: Not specified
  Context: raised as an open question
`

func TestParseActionItems(t *testing.T) {
	items, none, ok := parseActionItems(goodReply)
	if none || !ok {
		t.Fatalf("expected a parsed list, got none=%v ok=%v", none, ok)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(items), items)
	}
	if items[0].Action != "Write the migration runbook" || items[0].Owner != "bob" || items[0].Due != "Thursday" {
		t.Errorf("first item parsed wrongly: %+v", items[0])
	}
	if items[1].Owner != "Not specified" || items[1].Due != "Not specified" {
		t.Errorf("second item should carry the unspecified markers: %+v", items[1])
	}
}

// The product must show the words the requirement asks for, whether the
// summariser wrote them or simply left the field out.
func TestRenderActionItemsShowsNotSpecified(t *testing.T) {
	// Owner and Due omitted entirely by the model.
	md := "## Action items\n- Action: Send the invoice\n  Context: agreed on the call\n"
	out := renderActionItems(9, md)
	if !strings.Contains(out, "Owner: Not specified") {
		t.Errorf("a missing owner must render as Not specified:\n%s", out)
	}
	if !strings.Contains(out, "Due: Not specified") {
		t.Errorf("a missing due date must render as Not specified:\n%s", out)
	}
	if !strings.Contains(out, "Send the invoice") {
		t.Errorf("the action itself must survive:\n%s", out)
	}
}

// Placeholders the model might use instead of the exact phrase are
// normalised to it -- but nothing is ever filled in with a real value.
func TestNormaliseField(t *testing.T) {
	for _, in := range []string{"", "  ", "-", "none", "N/A", "unknown", "TBD", "not stated", "Not Specified", "**Not specified**"} {
		if got := normaliseField(in); got != notSpecified {
			t.Errorf("normaliseField(%q) = %q, want %q", in, got, notSpecified)
		}
	}
	// A real value is never rewritten.
	for _, in := range []string{"bob", "Friday", "2026-10-01", "alice and bob"} {
		if got := normaliseField(in); got != in {
			t.Errorf("normaliseField(%q) = %q, want it unchanged", in, got)
		}
	}
}

// Honco must not add an owner, a date or a task that the summariser did
// not write. The only words it may add are the placeholder and its own
// labels.
func TestRenderActionItemsInventsNothing(t *testing.T) {
	md := "## Action items\n- Action: Review the contract\n  Owner: Not specified\n  Due: Not specified\n"
	out := renderActionItems(3, md)
	for _, forbidden := range []string{"alice", "bob", "carol", "Monday", "Tuesday", "tomorrow", "next week"} {
		if strings.Contains(strings.ToLower(out), strings.ToLower(forbidden)) {
			t.Errorf("rendering invented %q:\n%s", forbidden, out)
		}
	}
	if strings.Count(out, "Action") > 3 {
		t.Errorf("unexpected extra content:\n%s", out)
	}
}

// "No action items" must be reported as exactly that, not as an empty list
// and not as a fabricated one.
func TestRenderActionItemsNone(t *testing.T) {
	for _, md := range []string{"NO ACTION ITEMS", "## Action items\nNO ACTION ITEMS\n", "no action items"} {
		out := renderActionItems(5, md)
		if !strings.Contains(out, "No action items were found") {
			t.Errorf("for %q the answer must say none were found:\n%s", md, out)
		}
		if strings.Contains(out, "Owner:") {
			t.Errorf("no item fields should be rendered when there are none:\n%s", out)
		}
	}
}

// An unparseable reply is shown unchanged rather than reshaped into a
// list Honco cannot vouch for.
func TestRenderActionItemsUnparseable(t *testing.T) {
	md := "I could not identify any structure here, sorry."
	out := renderActionItems(4, md)
	if !strings.Contains(out, md) {
		t.Errorf("the raw reply must be shown:\n%s", out)
	}
	if !strings.Contains(out, "did not answer in the expected format") {
		t.Errorf("the reader must be told the format was unexpected:\n%s", out)
	}
	if strings.Contains(out, "Owner: Not specified") {
		t.Errorf("no fields may be invented for an unparseable reply:\n%s", out)
	}
}

// An unrelated heading's body must not become action items. parseSections
// already guarantees this; the test pins it for this command.
func TestParseActionItemsIgnoresOtherSections(t *testing.T) {
	md := "## Summary\n- Action: this is in the summary, not the action items\n\n## Action items\n- Action: the real one\n  Owner: bob\n"
	items, _, ok := parseActionItems(md)
	if !ok {
		t.Fatal("expected a parse")
	}
	if len(items) != 1 || items[0].Action != "the real one" {
		t.Errorf("content leaked from another section: %+v", items)
	}
}

// The prompt must carry the anti-invention rules, because that is the
// first line of defence before anything is parsed.
func TestActionItemsAskForbidsInvention(t *testing.T) {
	for _, want := range []string{
		"Do not invent", "Owner: Not specified", "Due: Not specified", "NO ACTION ITEMS",
	} {
		if !strings.Contains(actionItemsAsk, want) {
			t.Errorf("the ask must contain %q", want)
		}
	}
}

// The transcript must use the same bounded conversation body as every
// other command, and must ask for action items rather than the standard
// four sections.
func TestBuildActionItemsTranscript(t *testing.T) {
	msgs := []convMessage{{Username: "alice", At: 1700000000000, Message: "bob will write the runbook by Thursday"}}
	out := buildActionItemsTranscript("Town Square", msgs)
	if !strings.Contains(out, "## Action items") {
		t.Error("the ask should name the Action items heading")
	}
	if strings.Contains(out, "## Key discussion points") {
		t.Error("this command should not request the summary sections")
	}
	if !strings.Contains(out, "---CONVERSATION---") || !strings.Contains(out, "bob will write the runbook") {
		t.Errorf("the conversation body is missing:\n%s", out)
	}
	if !strings.Contains(out, `called "Town Square"`) {
		t.Error("the channel should be named")
	}
}

func TestBuildActionItemsTranscriptTruncates(t *testing.T) {
	msgs := make([]convMessage, 0, 4000)
	for i := 0; i < 4000; i++ {
		msgs = append(msgs, convMessage{Username: "u", At: int64(i), Message: strings.Repeat("y", 200)})
	}
	out := buildActionItemsTranscript("Big", msgs)
	if len(out) > maxTranscriptChars+600 {
		t.Errorf("transcript not capped: %d chars", len(out))
	}
	if !strings.Contains(out, "truncated for length") {
		t.Error("truncation should be stated")
	}
}

// Both triggers resolve, anything else does not, and each carries its own
// wording so neither can answer as the other.
func TestCommandKindFor(t *testing.T) {
	s, ok := commandKindFor("summarize")
	if !ok || s.trigger != summarizeTrigger {
		t.Fatalf("summarize did not resolve: %+v %v", s, ok)
	}
	a, ok := commandKindFor("action-items")
	if !ok || a.trigger != actionItemsTrigger {
		t.Fatalf("action-items did not resolve: %+v %v", a, ok)
	}
	if a.ack == s.ack || a.emptyText == s.emptyText {
		t.Error("the two commands must not share their user-facing wording")
	}
	if !strings.Contains(strings.ToLower(a.emptyText), "no action items") {
		t.Errorf("the empty answer should say no action items: %q", a.emptyText)
	}
	for _, bad := range []string{"meet", "summarise", "action_items", "actionitems", "", "help"} {
		if _, ok := commandKindFor(bad); ok {
			t.Errorf("%q must not be handled by this plugin", bad)
		}
	}
	// Case must not matter for the ones we do own.
	if _, ok := commandKindFor("Action-Items"); !ok {
		t.Error("trigger matching should be case-insensitive")
	}
}

// The count line is Honco's own fact, stated separately from the model's
// words, and the footer must say plainly that nothing became a task.
func TestRenderActionItemsHeaderAndFooter(t *testing.T) {
	out := renderActionItems(12, goodReply)
	if !strings.Contains(out, "12 messages") {
		t.Errorf("the message count should be stated: %q", out)
	}
	if !strings.HasPrefix(out, "**Action items**") {
		t.Errorf("the answer should be labelled: %q", out)
	}
	if !strings.Contains(out, "Nothing here was created as a task.") {
		t.Errorf("the reader must be told no task was created: %q", out)
	}
	if !strings.Contains(out, "Owner: bob") || !strings.Contains(out, "Due: Thursday") {
		t.Errorf("stated owner and due date must be preserved: %q", out)
	}
}
