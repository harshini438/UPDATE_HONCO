package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The parser has to cope with whatever heading style the fixed prompt on
// mother happens to produce, and must never invent a section that was not
// in the output.
func TestParseSections(t *testing.T) {
	md := `## Summary
We agreed the release slips a week.

## Key discussion points
- The migration is slower than expected
- QA needs two more days

## Decisions
- Ship on the 21st

## Action items
- Bob — rerun the migration — Friday

## Open questions
- Who owns the rollback plan?`

	got := parseSections(md)

	if !strings.Contains(got[secSummary], "release slips") {
		t.Errorf("summary not parsed: %q", got[secSummary])
	}
	if !strings.Contains(got[secKeyPoints], "migration is slower") {
		t.Errorf("key points not parsed: %q", got[secKeyPoints])
	}
	if !strings.Contains(got[secDecisions], "Ship on the 21st") {
		t.Errorf("decisions not parsed: %q", got[secDecisions])
	}
	if !strings.Contains(got[secActionItems], "rerun the migration") {
		t.Errorf("action items not parsed: %q", got[secActionItems])
	}

	// "Open questions" is not one of the five stored sections, and must
	// not be swept into one of them.
	for key, text := range got {
		if strings.Contains(text, "rollback plan") {
			t.Errorf("unrecognised section leaked into %q", key)
		}
	}
}

// An absent section stays absent. Back-filling it from another one would
// put words in the model's mouth.
func TestParseSectionsDoesNotInvent(t *testing.T) {
	got := parseSections("## Summary\nJust a short note.")

	if got[secSummary] == "" {
		t.Error("summary should be present")
	}
	for _, key := range []string{secKeyPoints, secDecisions, secActionItems} {
		if got[key] != "" {
			t.Errorf("%s should be empty, got %q", key, got[key])
		}
	}
}

// Heading wording varies between runs; these all mean the same thing.
func TestSectionKeyAliases(t *testing.T) {
	cases := map[string]string{
		"Summary":               secSummary,
		"meeting summary":       secSummary,
		"Key Discussion Points": secKeyPoints,
		"Key points":            secKeyPoints,
		"Decisions":             secDecisions,
		"Decisions made":        secDecisions,
		"Action items":          secActionItems,
		"Action Items:":         secActionItems,
		"Next steps":            secActionItems,
	}
	for heading, want := range cases {
		if got := sectionKey(heading); got != want {
			t.Errorf("sectionKey(%q) = %q, want %q", heading, got, want)
		}
	}
	if got := sectionKey("Open questions"); got != "" {
		t.Errorf("an unrecognised heading must not map to a stored section, got %q", got)
	}
}

// If the model answers in a shape the parser does not recognise at all,
// nothing is parsed -- the caller is responsible for keeping the raw text.
func TestParseSectionsUnrecognisedOutput(t *testing.T) {
	got := parseSections("Here are your notes: everything went fine.")
	if len(got) != 0 {
		t.Errorf("expected nothing parsed, got %v", got)
	}
}

// Participants are counted locally from the messages, so they are exact
// rather than inferred, and ordered by how much each person said.
func TestParticipantsOf(t *testing.T) {
	msgs := []convMessage{
		{Username: "alice", At: 1, Message: "hello"},
		{Username: "bob", At: 2, Message: "hi"},
		{Username: "alice", At: 3, Message: "shall we start"},
		{Username: "alice", At: 4, Message: "ok"},
		{Username: "bob", At: 5, Message: "yes"},
		{Username: "carol", At: 6, Message: "here"},
	}
	got := participantsOf(msgs)

	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 participants, got %d: %q", len(lines), got)
	}
	if !strings.HasPrefix(lines[0], "- alice (3 messages)") {
		t.Errorf("most active participant should lead: %q", lines[0])
	}
	if !strings.Contains(got, "carol (1 message)") {
		t.Errorf("a single message must not be pluralised: %q", got)
	}
	if strings.Contains(got, "hello") {
		t.Error("participants must not carry message content")
	}
}

func TestParticipantsOfEmpty(t *testing.T) {
	if got := participantsOf(nil); got != "" {
		t.Errorf("no messages means no participants, got %q", got)
	}
}

// The transcript carries the conversation and nothing about who asked for
// it or which channel it came from.
func TestBuildTranscript(t *testing.T) {
	msgs := []convMessage{
		{Username: "alice", At: 1757400000000, Message: "let us ship on Friday"},
		{Username: "bob", At: 1757400060000, Message: "agreed"},
	}
	got := buildTranscript("Release planning", msgs)

	for _, want := range []string{"Release planning", "alice", "let us ship on Friday", "---CONVERSATION---"} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q", want)
		}
	}
	if !strings.Contains(got, "## Action items") {
		t.Error("transcript should ask for the sections Honco stores")
	}
	if strings.Contains(strings.ToLower(got), "audio") && !strings.Contains(got, "not an audio transcript") {
		t.Error("the transcript must state that this is typed chat")
	}
}

// A conversation longer than the cap is truncated rather than sent whole,
// and says so.
func TestBuildTranscriptTruncates(t *testing.T) {
	long := strings.Repeat("x", 5000)
	msgs := make([]convMessage, 0, 100)
	for i := 0; i < 100; i++ {
		msgs = append(msgs, convMessage{Username: "alice", At: 1757400000000, Message: long})
	}
	got := buildTranscript("Long one", msgs)

	if len(got) > maxTranscriptChars+1000 {
		t.Errorf("transcript exceeded the cap: %d chars", len(got))
	}
	if !strings.Contains(got, "truncated for length") {
		t.Error("a truncated transcript must say so")
	}
}

// The summarizer argv is built without a shell, and its components are
// validated so ssh cannot be handed a flag where a host was expected.
func TestSummarizerCommandSSH(t *testing.T) {
	name, args, err := summarizerCommand(configuration{
		SummarizerHost: "192.168.2.150", SummarizerPort: 31013,
		SummarizerUser: "database", SummarizerKeyPath: "/home/harshi/.ssh/k",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "ssh" {
		t.Errorf("expected ssh, got %q", name)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"BatchMode=yes", "31013", "database@192.168.2.150", "/home/harshi/.ssh/k"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q: %v", want, args)
		}
	}
}

func TestSummarizerCommandDefaults(t *testing.T) {
	_, args, err := summarizerCommand(configuration{})
	if err != nil {
		t.Fatalf("an unconfigured plugin should still build the documented default: %v", err)
	}
	if !strings.Contains(strings.Join(args, " "), defaultSummarizerUser+"@"+defaultSummarizerHost) {
		t.Errorf("defaults not applied: %v", args)
	}
}

func TestSummarizerCommandLocal(t *testing.T) {
	name, args, err := summarizerCommand(configuration{SummarizerCommand: "/opt/honco/summarize.sh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "/opt/honco/summarize.sh" || len(args) != 0 {
		t.Errorf("local transport should run the command with no arguments, got %q %v", name, args)
	}
	if _, _, err := summarizerCommand(configuration{SummarizerCommand: "summarize.sh"}); err == nil {
		t.Error("a relative command path must be rejected")
	}
}

// A host or user that ssh would read as a flag is refused outright.
func TestSummarizerCommandRejectsFlagShapes(t *testing.T) {
	bad := []configuration{
		{SummarizerHost: "-oProxyCommand=touch /tmp/x"},
		{SummarizerUser: "-oProxyCommand=x"},
		{SummarizerHost: "host; rm -rf /"},
		{SummarizerUser: "user name"},
	}
	for _, cfg := range bad {
		if _, _, err := summarizerCommand(cfg); err == nil {
			t.Errorf("expected rejection for %+v", cfg)
		}
	}
}

func TestSummarizerCommandRejectsRelativeKey(t *testing.T) {
	if _, _, err := summarizerCommand(configuration{SummarizerKeyPath: "id_rsa"}); err == nil {
		t.Error("a relative key path must be rejected")
	}
}

// An unset or absurd timeout falls back to the default rather than to
// "no timeout", which would wedge a meeting in pending.
func TestSummarizerTimeoutClamp(t *testing.T) {
	cases := map[int]int{
		0:     defaultSummarizerTimeout,
		-5:    defaultSummarizerTimeout,
		5:     defaultSummarizerTimeout,
		99999: defaultSummarizerTimeout,
		120:   120,
	}
	for in, want := range cases {
		if got := (configuration{SummarizerTimeoutSeconds: in}).summarizerTimeout(); got != want {
			t.Errorf("timeout %d clamped to %d, want %d", in, got, want)
		}
	}
}

// Failures shown to a user must not carry host names or SSH detail, and
// must say which kind of failure it actually was.
func TestPublicSummarizerError(t *testing.T) {
	timedOut := publicSummarizerError(fmt.Errorf("%w after 5m0s", errSummarizerTimeout))
	if !strings.Contains(timedOut, "did not respond in time") {
		t.Errorf("a real timeout should be reported as one, got %q", timedOut)
	}

	// ssh says "Connection timed out" when a host is simply unreachable.
	// That must NOT be classified as a timeout: telling the user to wait
	// and retry would be advice that can never work.
	unreachable := fmt.Errorf("%w: exit status 255: %s", errSummarizerUnavailable,
		"ssh: connect to host 192.168.2.150 port 31013: Connection timed out")
	got := publicSummarizerError(unreachable)
	if strings.Contains(got, "did not respond in time") {
		t.Errorf("an unreachable host must not be reported as a timeout, got %q", got)
	}
	if !strings.Contains(got, "not reachable") {
		t.Errorf("expected an unreachable message, got %q", got)
	}
	if strings.Contains(got, "192.168.2.150") || strings.Contains(got, "31013") ||
		strings.Contains(got, "ssh") {
		t.Errorf("internal detail leaked to the user: %q", got)
	}

	if got := publicSummarizerError(errors.New("something else")); got != "Summary generation failed." {
		t.Errorf("unclassified errors should get the generic message, got %q", got)
	}
}
