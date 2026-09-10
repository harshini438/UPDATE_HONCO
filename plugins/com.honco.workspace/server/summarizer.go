package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// The Claude integration.
//
// Honco already has exactly one Claude path, and this reuses it rather
// than adding a second: summarize/honco-summarize.sh runs on `mother`,
// where the Claude CLI is authenticated, behind an SSH key pinned to that
// one script by a forced command in authorized_keys. Its contract is a
// pipe -- conversation in on stdin, markdown meeting notes out on stdout,
// non-zero exit on failure -- and that is the contract this file speaks.
//
// Two transports, one backend:
//
//	SummarizerCommand  an absolute path to a local executable honouring
//	                   that same stdin/stdout contract (a wrapper, or
//	                   honco-summarize.sh itself if it is ever installed
//	                   locally). Takes precedence when set.
//	otherwise          ssh to the configured host, which is how
//	                   transcribe/summarize.sh reaches mother today.
//
// No API key is ever read, held or logged here: authentication lives on
// mother with the CLI, and this side holds only an SSH identity.

const (
	// A meeting's conversation is bounded so a summary can never quietly
	// turn into "summarise this entire channel".
	maxMeetingWindow   = 6 * time.Hour
	maxSummaryMessages = 1000
	maxTranscriptChars = 200000

	defaultSummarizerHost    = "192.168.2.150"
	defaultSummarizerPort    = 31013
	defaultSummarizerUser    = "database"
	defaultSummarizerTimeout = 300
)

// errSummarizerUnavailable means the Claude backend could not be reached
// or refused the work. It is reported to the user as a failed generation,
// never as a summary.
var errSummarizerUnavailable = errors.New("summarizer unavailable")

// errSummarizerTimeout is the subset of the above where we gave up
// waiting rather than being refused.
//
// It is a distinct sentinel rather than a phrase to look for in the error
// text, because ssh's own stderr says "Connection timed out" when a host
// is simply unreachable -- matching on words would report an unreachable
// host as a slow one and send the user off to wait for nothing.
var errSummarizerTimeout = fmt.Errorf("%w: timed out", errSummarizerUnavailable)

// convMessage is one channel message, reduced to what a summary needs.
// Nothing else about the author travels any further than this struct --
// no email, no user id, no roles.
type convMessage struct {
	Username string
	At       int64
	Message  string
}

// --- conversation window ---------------------------------------------------

// conversationWindow decides which slice of channel history belongs to a
// meeting.
//
// It starts when the meeting was registered and ends when its recording
// arrived -- the best evidence available that the meeting finished. With
// no recording it runs to now, and either way it is clamped to
// maxMeetingWindow, so an abandoned meeting row can never be used to
// summarise weeks of unrelated channel history.
func (p *Plugin) conversationWindow(m *Meeting) (int64, int64) {
	start := m.CreatedAt
	end := nowMillis()

	if recEnd, err := p.store.LatestRecordingEnd(m.ID); err == nil && recEnd > start {
		end = recEnd
	}
	if limit := start + maxMeetingWindow.Milliseconds(); end > limit {
		end = limit
	}
	if end < start {
		end = start
	}
	return start, end
}

// collectConversation reads the meeting's channel over the window.
//
// Only real human messages survive: joins and other system posts carry no
// discussion, and bot posts (including this plugin's own recording
// notices) would otherwise be summarised back at the reader.
func (p *Plugin) collectConversation(channelID string, start, end int64) ([]convMessage, error) {
	// GetPostsSince treats its argument as an exclusive lower bound in
	// some versions and an inclusive one in others; asking from one
	// millisecond earlier and filtering exactly below removes the
	// dependency on which.
	list, err := p.client.Post.GetPostsSince(channelID, start-1)
	if err != nil {
		return nil, err
	}
	if list == nil {
		return nil, nil
	}

	isBot := map[string]bool{}
	out := make([]convMessage, 0, len(list.Posts))
	for _, post := range list.Posts {
		if post == nil || post.DeleteAt != 0 || post.Type != "" {
			continue
		}
		if post.CreateAt < start || post.CreateAt > end {
			continue
		}
		if strings.TrimSpace(post.Message) == "" {
			continue
		}
		if post.UserId == p.botID {
			continue
		}
		bot, seen := isBot[post.UserId]
		if !seen {
			user, uerr := p.client.User.Get(post.UserId)
			bot = uerr != nil || user == nil || user.IsBot
			isBot[post.UserId] = bot
		}
		if bot {
			continue
		}
		out = append(out, convMessage{
			Username: p.username(post.UserId),
			At:       post.CreateAt,
			Message:  post.Message,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	if len(out) > maxSummaryMessages {
		// Keep the end: decisions and action items cluster at the close of
		// a meeting, so dropping the tail loses exactly what matters most.
		out = out[len(out)-maxSummaryMessages:]
	}
	return out, nil
}

// participantsOf lists who actually spoke, most active first.
//
// This is derived locally from the messages rather than asked of Claude,
// which makes it exact rather than inferred -- and it stays correct even
// when the model omits the section entirely. Usernames only, and only of
// people who posted in a channel the reader is already a member of.
func participantsOf(msgs []convMessage) string {
	counts := map[string]int{}
	order := []string{}
	for _, m := range msgs {
		if _, seen := counts[m.Username]; !seen {
			order = append(order, m.Username)
		}
		counts[m.Username]++
	}
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })

	parts := make([]string, 0, len(order))
	for _, name := range order {
		parts = append(parts, fmt.Sprintf("- %s (%d message%s)", name, counts[name], plural(counts[name])))
	}
	return strings.Join(parts, "\n")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// buildTranscript renders the conversation for the summariser.
//
// The remote script wraps whatever arrives in its own meeting-notes
// prompt, so this adds a short header naming the sections Honco wants and
// then the conversation itself. Nothing about the requester, no ids, and
// no channel metadata beyond the meeting's own topic is included.
func buildTranscript(topic string, msgs []convMessage) string {
	var b strings.Builder
	b.WriteString("This is a written team chat conversation from a Honco meeting")
	if topic != "" {
		b.WriteString(fmt.Sprintf(" about %q", topic))
	}
	b.WriteString(". It is typed chat, not an audio transcript.\n\n")
	b.WriteString("Please include these sections, using these exact headings:\n")
	b.WriteString("## Summary\n## Key discussion points\n## Decisions\n## Action items\n\n")
	b.WriteString("---CONVERSATION---\n")

	for _, m := range msgs {
		line := fmt.Sprintf("[%s] %s: %s\n",
			model.GetTimeForMillis(m.At).UTC().Format("15:04"), m.Username, m.Message)
		if b.Len()+len(line) > maxTranscriptChars {
			b.WriteString("\n[... conversation truncated for length ...]\n")
			break
		}
		b.WriteString(line)
	}
	return b.String()
}

// --- the Claude call -------------------------------------------------------

// summarise pipes the conversation to the Claude summariser and returns
// its markdown.
//
// The conversation is written to the child's stdin rather than passed as
// an argument, so it never appears in a process listing. The command is
// built as an argv slice -- there is no shell anywhere in this path, so
// no message content can be interpreted as a command.
func (p *Plugin) summarise(text string) (string, error) {
	cfg := p.config()

	timeout := time.Duration(cfg.summarizerTimeout()) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	name, args, err := summarizerCommand(cfg)
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(text)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// stderr can carry SSH diagnostics; it is clipped to a length and
		// never contains the conversation, which only ever went to stdin.
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 200 {
			detail = detail[:200]
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("%w after %s", errSummarizerTimeout, timeout)
		}
		return "", fmt.Errorf("%w: %v: %s", errSummarizerUnavailable, err, detail)
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return "", fmt.Errorf("%w: empty response", errSummarizerUnavailable)
	}
	// honco-summarize.sh reports some of its own failures on stdout with
	// a zero exit status, so the body has to be checked too.
	if strings.HasPrefix(out, "(summary failed") {
		return "", fmt.Errorf("%w: summariser reported failure", errSummarizerUnavailable)
	}
	return out, nil
}

// summarizerCommand builds the argv for the configured transport.
//
// Every component that lands in argv is validated against a strict
// charset first. None of it is user-supplied -- these are System Console
// settings -- but a host or user beginning with "-" would be read by ssh
// as a flag, so the shapes are checked rather than assumed.
func summarizerCommand(cfg configuration) (string, []string, error) {
	if cmd := strings.TrimSpace(cfg.SummarizerCommand); cmd != "" {
		if !strings.HasPrefix(cmd, "/") {
			return "", nil, fmt.Errorf("%w: SummarizerCommand must be an absolute path", errSummarizerUnavailable)
		}
		return cmd, nil, nil
	}

	host := firstNonEmpty(strings.TrimSpace(cfg.SummarizerHost), defaultSummarizerHost)
	user := firstNonEmpty(strings.TrimSpace(cfg.SummarizerUser), defaultSummarizerUser)
	port := cfg.SummarizerPort
	if port <= 0 || port > 65535 {
		port = defaultSummarizerPort
	}
	if !hostOK(host) || !userOK(user) {
		return "", nil, fmt.Errorf("%w: summarizer host or user is not a valid shape", errSummarizerUnavailable)
	}

	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		"-o", "StrictHostKeyChecking=accept-new",
		"-p", fmt.Sprintf("%d", port),
	}
	if key := strings.TrimSpace(cfg.SummarizerKeyPath); key != "" {
		if !strings.HasPrefix(key, "/") {
			return "", nil, fmt.Errorf("%w: SummarizerKeyPath must be an absolute path", errSummarizerUnavailable)
		}
		args = append(args, "-i", key, "-o", "IdentitiesOnly=yes")
	}
	args = append(args, user+"@"+host)
	return "ssh", args, nil
}

func firstNonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func hostOK(s string) bool {
	if s == "" || len(s) > 253 || s[0] == '-' {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}

func userOK(s string) bool {
	if s == "" || len(s) > 64 || s[0] == '-' {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// --- parsing ---------------------------------------------------------------

// Section keys as stored.
const (
	secSummary     = "summary"
	secKeyPoints   = "key_points"
	secDecisions   = "decisions"
	secActionItems = "action_items"
)

// parseSections splits the summariser's markdown into the sections Honco
// stores.
//
// It is deliberately forgiving about headings, because the prompt on
// mother is fixed and its wording is not ours to change: "Action items",
// "Action Items" and "Next steps" all mean the same thing here. It is
// equally deliberately unforgiving about content -- an absent section
// stays empty rather than being back-filled from another one, so the UI
// can never present something the model did not actually say.
func parseSections(md string) map[string]string {
	out := map[string]string{}
	var current string
	var buf []string

	flush := func() {
		if current != "" {
			if text := strings.TrimSpace(strings.Join(buf, "\n")); text != "" {
				if prev := out[current]; prev != "" {
					out[current] = prev + "\n" + text
				} else {
					out[current] = text
				}
			}
		}
		buf = buf[:0]
	}

	for _, line := range strings.Split(md, "\n") {
		if heading, ok := markdownHeading(line); ok {
			// Any heading closes the section before it -- including one
			// this parser does not store. Letting an unknown heading fall
			// through would append its body to whichever section came
			// last, so "Open questions" would silently become extra
			// action items.
			flush()
			current = sectionKey(heading)
			continue
		}
		if current != "" {
			buf = append(buf, line)
		}
	}
	flush()
	return out
}

// markdownHeading recognises an ATX heading ("## Decisions") and returns
// its text. A bare "#" or a line like "#1 priority" is not a heading --
// requiring the space keeps ordinary message text from being read as one.
func markdownHeading(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	hashes := 0
	for hashes < len(trimmed) && trimmed[hashes] == '#' {
		hashes++
	}
	if hashes == 0 || hashes > 6 || hashes >= len(trimmed) || trimmed[hashes] != ' ' {
		return "", false
	}
	return strings.TrimSpace(trimmed[hashes:]), true
}

func sectionKey(heading string) string {
	switch strings.ToLower(strings.TrimSpace(strings.TrimSuffix(heading, ":"))) {
	case "summary", "meeting summary", "overview":
		return secSummary
	case "key discussion points", "key points", "discussion points",
		"discussion", "key topics", "topics discussed":
		return secKeyPoints
	case "decisions", "decisions made", "decisions taken":
		return secDecisions
	case "action items", "action item", "actions", "next steps", "follow-ups", "follow ups":
		return secActionItems
	}
	return ""
}
