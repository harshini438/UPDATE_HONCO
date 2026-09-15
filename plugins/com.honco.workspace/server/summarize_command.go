package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// The /summarize slash command.
//
// It adds a command surface to machinery that already exists: the window
// and message filtering from Meeting Intelligence (collectConversation),
// the same transcript builder, the same summariser transport
// (p.summarise), the same section parser and the same user-safe error
// mapper. No second summariser, no AI engine, no transcription, and
// nothing to do with the teammate's AI Assistant service.
//
// Two deliberate choices:
//
//   - The answer is EPHEMERAL. A summary is derived from messages the
//     requester can already read; posting it to the channel would
//     broadcast a rendering of that conversation to everyone, which is a
//     different act from letting one person read it back. If someone
//     wants to share it, they can paste it.
//   - The work runs in the background and the command returns at once. A
//     summary can legitimately take minutes (the summariser's timeout is
//     up to 900s), and a slash command that blocks that long is a broken
//     command, not a slow one.

const (
	summarizeTrigger = "summarize"

	// How far back a channel summary looks. The same six-hour bound
	// Meeting Intelligence uses, for the same reason: a command should
	// summarise a conversation, never the entire history of a channel.
	summarizeWindow = maxMeetingWindow
)

// Summarising is expensive -- it can hold an SSH connection open for
// minutes -- so this is much tighter than the HTTP read limiter. Per
// user: a few in a row is fine, a loop is not. ExecuteCommand is not an
// HTTP route, so hardening.go's middleware never sees it; this is its
// equivalent.
var limitSummarize = newRateLimiter(6, 3)

// registerSummarizeCommand makes the command visible in the autocomplete.
// Registration is idempotent: Mattermost replaces any previous
// registration for the same trigger from this plugin.
func (p *Plugin) registerSummarizeCommand() error {
	return p.client.SlashCommand.Register(&model.Command{
		Trigger:          summarizeTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "Summarise the recent conversation in this channel",
		AutoCompleteHint: "",
		DisplayName:      "Summarize",
		Description:      "Summarise the recent conversation in this channel using the Honco summariser.",
	})
}

// ExecuteCommand handles /summarize and /action-items.
//
// Both are the same shape -- prove membership, bound the conversation,
// ask the summariser, deliver privately -- so they share every step and
// differ only in what they ask for and how the answer is rendered. That
// difference lives in commandKinds; nothing else is duplicated.
//
// Authorization is proved before any message is read: the caller must be
// a member of the channel they are invoking in. Mattermost only routes a
// command from a channel the user has open, but that is a property of the
// client, not a guarantee, and this handler does not rely on it.
func (p *Plugin) ExecuteCommand(_ *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if args == nil {
		return nil, model.NewAppError("ExecuteCommand", "honco.command.bad_request", nil, "", 400)
	}
	trigger := strings.TrimPrefix(strings.Fields(args.Command + " ")[0], "/")
	kind, known := commandKindFor(trigger)
	if !known {
		// Not ours. Returning nil lets Mattermost try other handlers.
		return nil, nil
	}

	if args.UserId == "" || args.ChannelId == "" {
		return ephemeral("This command needs to be run from a channel."), nil
	}

	// Authorization first, before a single post is read. A non-member is
	// told the same thing as someone in a channel that does not exist.
	if _, err := p.client.Channel.GetMember(args.ChannelId, args.UserId); err != nil {
		return ephemeral("You do not have access to this channel."), nil
	}

	// One limiter for both: they cost the same and a user alternating
	// between them is still the same load on the summariser.
	if !limitSummarize.allow(args.UserId) {
		return ephemeral("You are asking too quickly. Please wait a moment and try again."), nil
	}

	// Everything after this point can take minutes, so it happens in the
	// background and the command answers immediately.
	go p.runChannelSummary(kind, args.UserId, args.ChannelId, args.RootId)

	return ephemeral(kind.ack), nil
}

// runChannelSummary does the work and delivers the result as an ephemeral
// post to the requester.
//
// It re-reads nothing about who the user is: the caller already proved
// membership, and the window is built from that same channel id. Any
// failure is reported to the user in plain words and never as an empty
// or invented summary.
func (p *Plugin) runChannelSummary(kind commandKind, userID, channelID, rootID string) {
	defer func() {
		if rec := recover(); rec != nil {
			p.client.Log.Error("honco: command panicked", "command", kind.trigger, "channel_id", channelID)
			p.sendSummaryPost(userID, channelID, rootID, kind.failedText)
		}
	}()

	end := nowMillis()
	start := end - summarizeWindow.Milliseconds()

	// The same collector Meeting Intelligence uses: human messages only,
	// no system posts, no bots, no deleted posts, oldest first, capped.
	msgs, err := p.collectConversation(channelID, start, end)
	if err != nil {
		p.client.Log.Warn("honco: command could not read the conversation",
			"command", kind.trigger, "channel_id", channelID, "err", err.Error())
		p.sendSummaryPost(userID, channelID, rootID, "The conversation could not be read. Please try again.")
		return
	}
	if len(msgs) == 0 {
		// The summariser is never called for an empty conversation:
		// there is nothing to send it, and asking a model about silence
		// invites it to invent.
		p.sendSummaryPost(userID, channelID, rootID, kind.emptyText)
		return
	}

	title := p.channelLabel(channelID)
	out, serr := p.summarise(kind.build(title, msgs))
	if serr != nil {
		// publicSummarizerError is the same user-safe mapping the
		// Meeting Intelligence API uses -- it never passes the
		// underlying message through, which can carry host and SSH
		// detail.
		p.client.Log.Warn("honco: summariser failed",
			"command", kind.trigger, "channel_id", channelID, "err", serr.Error())
		p.sendSummaryPost(userID, channelID, rootID, summarizeFailureText(serr))
		return
	}

	p.sendSummaryPost(userID, channelID, rootID, kind.render(len(msgs), out))
}

// summarizeFailureText is the honest failure message. There is no stub
// and no partial summary: if the summariser did not answer, the user is
// told exactly that.
func summarizeFailureText(err error) string {
	msg := publicSummarizerError(err)
	if errors.Is(err, errSummarizerUnavailable) || errors.Is(err, errSummarizerTimeout) {
		return msg + "\n\nNo summary was generated. Nothing has been saved."
	}
	return msg
}

// renderChannelSummary formats what the summariser returned.
//
// The model's markdown is passed through as the body of a post, the same
// as Meeting Intelligence stores it. The counted line above it is Honco's
// own fact, not the model's, so it is stated separately: a reader can
// always tell how much conversation the summary was built from.
func renderChannelSummary(count int, md string) string {
	header := fmt.Sprintf("**Channel summary** — from the last %d message%s (past 6 hours)\n\n",
		count, plural(count))
	return header + strings.TrimSpace(md)
}

// buildChannelTranscript is buildTranscript's sibling for a channel.
//
// The two differ only in the opening sentence: the summariser is told
// what it is reading, and calling a channel's conversation a "meeting"
// would be a lie to the model that shows up in its output. Everything
// that matters -- the requested section headings, the line format, the
// character cap -- is shared through buildConversationText.
func buildChannelTranscript(channel string, msgs []convMessage) string {
	intro := "This is a written team chat conversation from a Honco Chat channel"
	if channel != "" {
		intro += fmt.Sprintf(" called %q", channel)
	}
	intro += ". It is typed chat, not an audio transcript."
	return buildConversationText(intro, standardSectionsAsk, msgs)
}

// channelLabel is the channel's display name, for the prompt only.
//
// A direct or group message has no display name; rather than compose one
// out of member names -- which would put people's names into the prompt
// for no benefit -- it stays blank and the transcript simply omits it.
func (p *Plugin) channelLabel(channelID string) string {
	ch, err := p.client.Channel.Get(channelID)
	if err != nil || ch == nil {
		return ""
	}
	if ch.Type == model.ChannelTypeDirect || ch.Type == model.ChannelTypeGroup {
		return ""
	}
	return ch.DisplayName
}

// sendSummaryPost delivers the result to the requester alone.
func (p *Plugin) sendSummaryPost(userID, channelID, rootID, message string) {
	post := &model.Post{
		UserId:    p.botID,
		ChannelId: channelID,
		RootId:    rootID,
		Message:   message,
	}
	p.client.Post.SendEphemeralPost(userID, post)
}

// ephemeral is the immediate reply to the command itself.
func ephemeral(text string) *model.CommandResponse {
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         text,
	}
}
