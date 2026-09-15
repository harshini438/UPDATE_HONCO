package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// The /action-items slash command.
//
// It is /summarize with a different ask and a different rendering, and
// shares literally everything else: the same membership check, the same
// limiter, the same six-hour / 1000-message / 200k-character bounds, the
// same collectConversation, the same summariser transport, the same
// user-safe error mapping, the same ephemeral delivery. The only new code
// is the prompt, a parser for the reply, and the renderer below.
//
// The honesty rule here is stricter than for a summary, because an action
// item reads as an instruction to a named person by a named date. Honco
// therefore:
//
//   - asks the summariser, in the prompt, to write "Not specified" rather
//     than guess an owner or a date, and to extract only what was actually
//     said;
//   - normalises a missing field to "Not specified" when rendering, so a
//     blank can never be mistaken for an answer;
//   - never fills a field in from anywhere else. Honco adds no owner, no
//     date and no task of its own.

const actionItemsTrigger = "action-items"

// The ask. It is deliberately explicit about the format, because the
// reply is parsed: a free-form list would have to be shown raw, and then
// a missing owner would render as nothing at all.
const actionItemsAsk = "" +
	"From this conversation, extract ONLY the action items that were actually stated or clearly agreed.\n" +
	"\n" +
	"Rules you must follow:\n" +
	"- Do not invent tasks, owners, dates or facts. If something was not said, do not write it.\n" +
	"- If nobody was named as responsible, write exactly: Owner: Not specified\n" +
	"- If no date or deadline was stated, write exactly: Due: Not specified\n" +
	"- If there are no action items in this conversation, write exactly: NO ACTION ITEMS\n" +
	"\n" +
	"Use this exact format, one block per action item, under a single heading:\n" +
	"\n" +
	"## Action items\n" +
	"- Action: <what is to be done, in one line>\n" +
	"  Owner: <the person named, or Not specified>\n" +
	"  Due: <the date or deadline stated, or Not specified>\n" +
	"  Context: <a short quote or paraphrase of where this came from>\n"

// actionItem is one parsed block. Every field is exactly what the
// summariser wrote, or empty; nothing is derived.
type actionItem struct {
	Action  string
	Owner   string
	Due     string
	Context string
}

const notSpecified = "Not specified"

// buildActionItemsTranscript asks for action items over the same
// conversation body every other command uses.
func buildActionItemsTranscript(channel string, msgs []convMessage) string {
	intro := "This is a written team chat conversation from a Honco Chat channel"
	if channel != "" {
		intro += fmt.Sprintf(" called %q", channel)
	}
	intro += ". It is typed chat, not an audio transcript."
	return buildConversationText(intro, actionItemsAsk, msgs)
}

// parseActionItems reads the summariser's reply into blocks.
//
// It reuses parseSections to find the "Action items" section, so the
// heading aliases Meeting Intelligence already accepts work here too, and
// an unrelated heading cannot leak its body into the list. Within that
// section a "- Action:" line opens a block and the indented keys fill it.
//
// Anything it cannot parse is returned as ok=false rather than guessed
// at, so the caller can show the raw reply instead of a confident-looking
// list that does not match what the model said.
func parseActionItems(md string) (items []actionItem, none bool, ok bool) {
	if strings.Contains(strings.ToUpper(md), "NO ACTION ITEMS") {
		return nil, true, true
	}

	body := parseSections(md)[secActionItems]
	if strings.TrimSpace(body) == "" {
		// No recognised section. Some replies are just the list.
		body = md
	}

	var cur *actionItem
	flush := func() {
		if cur != nil && strings.TrimSpace(cur.Action) != "" {
			items = append(items, *cur)
		}
		cur = nil
	}

	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		// A new block: "- Action: ..." (the bullet is optional).
		if v, found := afterKey(line, "Action"); found && strings.HasPrefix(strings.TrimLeft(line, "-*• \t"), "Action") {
			flush()
			cur = &actionItem{Action: v}
			continue
		}
		if cur == nil {
			continue
		}
		if v, found := afterKey(line, "Owner"); found {
			cur.Owner = v
			continue
		}
		if v, found := afterKey(line, "Due"); found {
			cur.Due = v
			continue
		}
		if v, found := afterKey(line, "Context"); found {
			cur.Context = v
			continue
		}
	}
	flush()

	return items, false, len(items) > 0
}

// afterKey matches a "Key: value" line, ignoring bullets and case.
func afterKey(line, key string) (string, bool) {
	trimmed := strings.TrimLeft(line, "-*• \t")
	if len(trimmed) < len(key)+1 {
		return "", false
	}
	if !strings.EqualFold(trimmed[:len(key)], key) {
		return "", false
	}
	rest := strings.TrimSpace(trimmed[len(key):])
	if !strings.HasPrefix(rest, ":") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(rest, ":")), true
}

// normaliseField turns anything absent, blank or a placeholder into the
// one phrase the product shows. This is normalisation, never invention:
// it can only ever replace "nothing" with "Not specified".
func normaliseField(v string) string {
	v = strings.TrimSpace(strings.Trim(strings.TrimSpace(v), "*_`"))
	switch strings.ToLower(strings.Trim(v, ".")) {
	case "", "-", "none", "n/a", "na", "unknown", "not specified", "unspecified", "tbd", "not stated":
		return notSpecified
	}
	return v
}

// renderActionItems formats the parsed blocks.
//
// The count line is Honco's own fact and is stated separately from the
// model's words, the same way renderChannelSummary does it. When the
// reply could not be parsed, the raw markdown is shown under a plain
// warning rather than reshaped into a list Honco cannot vouch for.
func renderActionItems(count int, md string) string {
	header := fmt.Sprintf("**Action items** — from the last %d message%s (past 6 hours)\n\n",
		count, plural(count))

	items, none, ok := parseActionItems(md)
	if none {
		return header + "No action items were found in this conversation."
	}
	if !ok {
		return header +
			"The summariser did not answer in the expected format, so its reply is shown unchanged:\n\n" +
			strings.TrimSpace(md)
	}

	var b strings.Builder
	b.WriteString(header)
	for i, it := range items {
		b.WriteString(fmt.Sprintf("**%d. %s**\n", i+1, strings.TrimSpace(it.Action)))
		b.WriteString(fmt.Sprintf("- Owner: %s\n", normaliseField(it.Owner)))
		b.WriteString(fmt.Sprintf("- Due: %s\n", normaliseField(it.Due)))
		if c := normaliseField(it.Context); c != notSpecified {
			b.WriteString(fmt.Sprintf("- Context: %s\n", c))
		}
		if i < len(items)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n_Extracted from the conversation. Nothing here was created as a task._")
	return b.String()
}

// registerActionItemsCommand makes the command visible in autocomplete.
func (p *Plugin) registerActionItemsCommand() error {
	return p.client.SlashCommand.Register(&model.Command{
		Trigger:          actionItemsTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "List the action items from the recent conversation in this channel",
		DisplayName:      "Action items",
		Description:      "Extract action items from the recent conversation in this channel using the Honco summariser.",
	})
}

// --- the command table -----------------------------------------------------

// commandKind is everything that differs between the two commands. The
// handler in summarize_command.go is written against this and knows
// nothing else about either of them.
type commandKind struct {
	trigger    string
	ack        string
	emptyText  string
	failedText string
	build      func(channel string, msgs []convMessage) string
	render     func(count int, md string) string
}

func commandKindFor(trigger string) (commandKind, bool) {
	switch {
	case strings.EqualFold(trigger, summarizeTrigger):
		return commandKind{
			trigger:    summarizeTrigger,
			ack:        "Summarising the recent conversation… the summary will appear here shortly. Only you will see it.",
			emptyText:  "There is nothing to summarise: no messages from people in the last 6 hours.",
			failedText: "The summary could not be generated.",
			build:      buildChannelTranscript,
			render:     renderChannelSummary,
		}, true
	case strings.EqualFold(trigger, actionItemsTrigger):
		return commandKind{
			trigger:    actionItemsTrigger,
			ack:        "Looking for action items… they will appear here shortly. Only you will see them.",
			emptyText:  "No action items found: there are no messages from people in the last 6 hours.",
			failedText: "The action items could not be generated.",
			build:      buildActionItemsTranscript,
			render:     renderActionItems,
		}, true
	}
	return commandKind{}, false
}
