package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const (
	// commandTrigger is the current top-level command. It takes a subcommand
	// naming the resource to request (channel/team/webhook).
	commandTrigger = "request"

	// legacyCommandTrigger is the original command, kept as a deprecated alias
	// that behaves exactly like `/request channel` so existing muscle memory
	// and documentation keep working.
	legacyCommandTrigger = "channel-request"
)

// Subcommands of `/request`. These mirror the per-type request toggles in
// configuration (requestType* constants) one-for-one.
const (
	subCommandChannel   = "channel"
	subCommandTeam      = "team"
	subCommandTeamAdmin = "team-admin"
	subCommandWebhook   = "webhook"
	subCommandBotToken  = "bot-token"
)

func getCommand() *model.Command {
	return &model.Command{
		Trigger:          commandTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "Request the creation of a resource for admin approval",
		AutoCompleteHint: "[channel|team|webhook]",
		AutocompleteData: getAutocompleteData(),
		DisplayName:      "Request",
	}
}

// getLegacyCommand registers `/channel-request` as a deprecated alias for
// `/request channel`. It is still fully functional; the autocomplete text
// points users at the new command.
func getLegacyCommand() *model.Command {
	return &model.Command{
		Trigger:          legacyCommandTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "(Deprecated) Use /request channel instead",
		AutoCompleteHint: "",
		AutocompleteData: model.NewAutocompleteData(
			legacyCommandTrigger,
			"",
			"(Deprecated) Request a new channel — use /request channel",
		),
		DisplayName: "Channel Request (deprecated)",
	}
}

// getAutocompleteData drives the inline `/` suggestion UI: `/request` plus a
// leaf per requestable resource type. Phases that implement team/webhook fill
// in their handlers; the subcommands are surfaced now so the command shape is
// stable.
func getAutocompleteData() *model.AutocompleteData {
	cmd := model.NewAutocompleteData(
		commandTrigger,
		"[channel|team|webhook]",
		"Request the creation of a resource for admin approval",
	)
	cmd.AddCommand(model.NewAutocompleteData(subCommandChannel, "", "Request the creation of a new channel"))
	cmd.AddCommand(model.NewAutocompleteData(subCommandTeam, "", "Request the creation of a new team"))
	cmd.AddCommand(model.NewAutocompleteData(subCommandTeamAdmin, "", "Request Team Admin on the current team"))
	cmd.AddCommand(model.NewAutocompleteData(subCommandWebhook, "", "Request an incoming webhook for a channel"))
	cmd.AddCommand(model.NewAutocompleteData(subCommandBotToken, "", "Request a bot token (needs security + system approval)"))
	return cmd
}

func (p *Plugin) ExecuteCommand(_ *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	switch subcommandFromArgs(args) {
	case subCommandChannel:
		return p.executeChannelRequest(args)
	case subCommandTeam:
		return p.executeTeamRequest(args)
	case subCommandTeamAdmin:
		return p.executeTeamAdminRequest(args)
	case subCommandBotToken:
		return p.executeBotTokenRequest(args)
	case subCommandWebhook:
		return p.executeWebhookRequest(args)
	default:
		return ephemeralResponse(requestUsage()), nil
	}
}

// subcommandFromArgs resolves the requested resource type from the raw command
// text. The legacy `/channel-request` trigger maps to the channel subcommand;
// otherwise the first argument after `/request` selects the type.
func subcommandFromArgs(args *model.CommandArgs) string {
	fields := strings.Fields(args.Command)
	if len(fields) == 0 {
		return ""
	}
	trigger := strings.TrimPrefix(fields[0], "/")
	if trigger == legacyCommandTrigger {
		return subCommandChannel
	}
	if len(fields) < 2 {
		return ""
	}
	return strings.ToLower(fields[1])
}

// executeChannelRequest opens the channel-creation request form, gating on the
// channel toggle and prefix configuration first.
func (p *Plugin) executeChannelRequest(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	// Feature gate first: if an admin has disabled channel requests, say so
	// rather than opening a form that would be rejected on submit.
	if !p.getConfiguration().RequestEnabled(requestTypeChannel) {
		return ephemeralResponse(channelRequestsDisabledMsg), nil
	}

	// Requests require at least one configured prefix; without one the
	// dialog could only ever fail on submit. Tell the user instead of
	// opening a doomed form.
	if !p.getConfiguration().UsesPrefixList() {
		return ephemeralResponse("Channel requests aren't configured yet. Ask a System Admin to define at least one channel prefix in System Console → Plugins → Channel Requests."), nil
	}

	if err := p.openRequestDialog(args.TriggerId, args.TeamId); err != nil {
		p.API.LogError("failed to open channel request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the channel request form. Please try again."), nil
	}

	return &model.CommandResponse{}, nil
}

// executeTeamRequest opens the team-creation request form, gating on the team
// toggle first. Unlike channels, teams need no prefix configuration.
func (p *Plugin) executeTeamRequest(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().RequestEnabled(requestTypeTeam) {
		return ephemeralResponse(requestDisabledMsg("Team")), nil
	}

	if err := p.openTeamRequestDialog(args.TriggerId); err != nil {
		p.API.LogError("failed to open team request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the team request form. Please try again."), nil
	}

	return &model.CommandResponse{}, nil
}

// executeTeamAdminRequest opens the team-admin request form for the team the
// command was run in, gating on the team-admin toggle first.
func (p *Plugin) executeTeamAdminRequest(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().RequestEnabled(requestTypeTeamAdmin) {
		return ephemeralResponse(requestDisabledMsg("Team admin")), nil
	}

	if args.TeamId == "" {
		return ephemeralResponse("Run this from within the team you want Team Admin on."), nil
	}

	if err := p.openTeamAdminRequestDialog(args.TriggerId, args.TeamId); err != nil {
		p.API.LogError("failed to open team-admin request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the team admin request form. Please try again."), nil
	}

	return &model.CommandResponse{}, nil
}

// executeBotTokenRequest opens the bot-token request form, gating on the
// bot-token toggle first.
func (p *Plugin) executeBotTokenRequest(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().RequestEnabled(requestTypeBotToken) {
		return ephemeralResponse(requestDisabledMsg("Bot token")), nil
	}

	if err := p.openBotTokenRequestDialog(args.TriggerId); err != nil {
		p.API.LogError("failed to open bot-token request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the bot token request form. Please try again."), nil
	}

	return &model.CommandResponse{}, nil
}

// executeWebhookRequest opens the incoming-webhook request form for the channel
// the command was run in, gating on the webhook toggle first.
func (p *Plugin) executeWebhookRequest(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().RequestEnabled(requestTypeWebhook) {
		return ephemeralResponse(requestDisabledMsg("Webhook")), nil
	}

	if args.ChannelId == "" {
		return ephemeralResponse("Run this from the channel you want a webhook for."), nil
	}

	if err := p.openWebhookRequestDialog(args.TriggerId, args.ChannelId); err != nil {
		p.API.LogError("failed to open webhook request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the webhook request form. Please try again."), nil
	}

	return &model.CommandResponse{}, nil
}

func requestUsage() string {
	return "Usage: `/request [channel|team|team-admin|webhook|bot-token]`\n\n" +
		"• `/request channel` — request a new channel\n" +
		"• `/request team` — request a new team\n" +
		"• `/request team-admin` — request Team Admin on the current team\n" +
		"• `/request webhook` — request an incoming webhook for a channel\n" +
		"• `/request bot-token` — request a bot token (needs security + system approval)"
}

func requestDisabledMsg(label string) string {
	return fmt.Sprintf("%s requests are currently disabled by an administrator.", label)
}

func ephemeralResponse(text string) *model.CommandResponse {
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         text,
	}
}
