package main

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const commandTrigger = "mattermost-request-hub"

// fieldNominees is the dialog element name for the nominees selector in the
// channel-admin slash-command dialog (distinct from fieldMembers used by the
// channel-creation dialog).
const fieldNominees = "nominees"

// fieldChannelID is the dialog element name for the channel selector in the
// channel-admin slash-command dialog.
const fieldChannelID = "channel_id"

// channelAdminDialogCallbackID identifies submissions from the channel-admin
// slash-command dialog (distinct from the webapp path which uses JSON directly).
const channelAdminDialogCallbackID = "channel_admin_request"

func getCommand() *model.Command {
	return &model.Command{
		Trigger:          commandTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "Request admin-gated permissions (channels, teams, admin roles) or open docs",
		AutoCompleteHint: "[docs|channel|channel-admin|team-admin|team|team-member|team-guest|bot|webhook-incoming|webhook-outgoing]",
		AutocompleteData: getAutocompleteData(),
		DisplayName:      "Mattermost Request Hub",
	}
}

func getAutocompleteData() *model.AutocompleteData {
	root := model.NewAutocompleteData(commandTrigger, "[subcommand]", "Request admin-gated access via Request Hub")
	root.AddCommand(model.NewAutocompleteData("docs", "", "Open the full documentation guide in your browser"))
	root.AddCommand(model.NewAutocompleteData("channel", "", "Request creation of a new channel"))
	root.AddCommand(model.NewAutocompleteData("channel-admin", "", "Request Channel Admin role on an existing channel"))
	root.AddCommand(model.NewAutocompleteData("team-admin", "", "Request Team Admin role in the current team"))
	root.AddCommand(model.NewAutocompleteData("team", "", "Request creation of a new team"))
	root.AddCommand(model.NewAutocompleteData("team-member", "", "Request adding users to the current team"))
	root.AddCommand(model.NewAutocompleteData("team-guest", "", "Request a guest invite to the current team"))
	root.AddCommand(model.NewAutocompleteData("bot", "", "Request creation of a bot account"))
	root.AddCommand(model.NewAutocompleteData("webhook-incoming", "", "Request an incoming webhook (external → Mattermost)"))
	root.AddCommand(model.NewAutocompleteData("webhook-outgoing", "", "Request an outgoing webhook (Mattermost → external)"))
	return root
}

func (p *Plugin) ExecuteCommand(_ *plugin.Context, args *model.CommandArgs) (resp *model.CommandResponse, appErr *model.AppError) {
	// Never let a panic deactivate the whole plugin — Mattermost surfaces
	// "command crashed the … plugin" / "Plugin for /… is not working" after that.
	defer func() {
		if r := recover(); r != nil {
			p.API.LogError("channel-requests: ExecuteCommand panic recovered",
				"panic", fmt.Sprint(r),
				"stack", string(debug.Stack()),
			)
			resp = ephemeralResponse("Something went wrong running that command. The error was logged — please try again or contact a System Admin.")
			appErr = nil
		}
	}()

	if args == nil {
		return ephemeralResponse("Could not run that command (missing arguments)."), nil
	}

	fields := strings.Fields(args.Command)
	subcommand := ""
	if len(fields) >= 2 {
		subcommand = strings.ToLower(fields[1])
	}

	switch subcommand {
	case "docs", "help", "documentation":
		return p.executeDocsCommand(args)
	case "channel", "":
		return p.executeChannelRequestCommand(args)
	case "channel-admin":
		return p.executeChannelAdminRequestCommand(args)
	case "team-admin":
		return p.executeTeamAdminRequestCommand(args)
	case "team":
		return p.executeTeamCreationRequestCommand(args)
	case "team-member":
		return p.executeTeamMemberRequestCommand(args)
	case "team-guest":
		return p.executeTeamGuestRequestCommand(args)
	case "bot":
		return p.executeBotRequestCommand(args)
	case "webhook-incoming":
		return p.executeIncomingWebhookRequestCommand(args)
	case "webhook-outgoing":
		return p.executeOutgoingWebhookRequestCommand(args)
	default:
		return ephemeralResponse(commandHelpText()), nil
	}
}

// executeDocsCommand posts an ephemeral markdown link to the bundled HTML
// guide (public/help/docs.html). Do NOT set GotoLocation: Mattermost treats
// same-origin /plugins/... URLs as in-app React routes, which leaves the
// channel view stuck on "Loading..." forever. Markdown links to /plugins/
// open in a new tab as a full document load instead.
func (p *Plugin) executeDocsCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	path := fmt.Sprintf("/plugins/%s/public/help/docs.html", manifest.Id)
	location := path
	if args != nil && args.SiteURL != "" {
		location = strings.TrimRight(args.SiteURL, "/") + path
	}
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text: "**Documentation**\n\n" +
			"[Open the full guide](" + location + ") (opens in a new tab).\n\n" +
			"Covers channels, Channel Admin, teams, bots, webhooks, messaging threads, and slash commands.",
	}, nil
}

func (p *Plugin) executeChannelRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().RequestEnabled(requestTypeChannel) {
		return ephemeralResponse(channelRequestsDisabledMsg), nil
	}
	if !p.getConfiguration().UsesPrefixList() {
		return ephemeralResponse("Channel requests aren't configured yet. Ask a System Admin to define at least one channel prefix in System Console → Plugins → Mattermost Request Hub."), nil
	}
	if err := p.openRequestDialog(args.TriggerId, args.TeamId); err != nil {
		p.API.LogError("failed to open channel request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the channel request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

func (p *Plugin) executeChannelAdminRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().RequestEnabled(requestTypeChannelAdmin) {
		return ephemeralResponse(channelAdminRequestsDisabledMsg), nil
	}
	if err := p.openChannelAdminRequestDialog(args.TriggerId); err != nil {
		p.API.LogError("failed to open channel-admin request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the Channel Admin request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

func (p *Plugin) executeTeamAdminRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().RequestEnabled(requestTypeTeamAdmin) {
		return ephemeralResponse(teamAdminRequestsDisabledMsg), nil
	}
	if err := p.openTeamAdminRequestDialog(args.TriggerId, args.TeamId); err != nil {
		p.API.LogError("failed to open team-admin request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the Team Admin request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

func (p *Plugin) executeTeamCreationRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().RequestEnabled(requestTypeTeam) {
		return ephemeralResponse(teamRequestsDisabledMsg), nil
	}
	if err := p.openTeamCreationRequestDialog(args.TriggerId); err != nil {
		p.API.LogError("failed to open team creation request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the team creation request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

func (p *Plugin) executeTeamMemberRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().RequestEnabled(requestTypeTeamMember) {
		return ephemeralResponse(teamMemberRequestsDisabledMsg), nil
	}
	if err := p.openTeamMemberRequestDialog(args.TriggerId, args.TeamId); err != nil {
		p.API.LogError("failed to open team-member request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the team member request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

func (p *Plugin) executeTeamGuestRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().RequestEnabled(requestTypeTeamGuest) {
		return ephemeralResponse(teamGuestRequestsDisabledMsg), nil
	}
	if err := p.openTeamGuestRequestDialog(args.TriggerId, args.TeamId); err != nil {
		p.API.LogError("failed to open team-guest request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the guest invite request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

// openChannelAdminRequestDialog opens a slash-command-driven dialog for
// requesting Channel Admin promotion. Uses the existing submitAdminRequest
// backend — the dialog submission handler adapts the dialog format.
func (p *Plugin) openChannelAdminRequestDialog(triggerID string) error {
	dialog := model.Dialog{
		CallbackId:       channelAdminDialogCallbackID,
		Title:            "Request Channel Admin Promotion",
		IntroductionText: "Nominate users for Channel Admin on an existing channel. Requires admin approval.",
		SubmitLabel:      "Submit request",
		Elements: []model.DialogElement{
			{
				DisplayName: "Channel",
				Name:        fieldChannelID,
				Type:        "select",
				DataSource:  "channels",
				HelpText:    "Select the channel you want to request Channel Admin access for.",
			},
			{
				DisplayName: "Users to promote",
				Name:        fieldNominees,
				Type:        "select",
				DataSource:  "users",
				MultiSelect: true,
			},
		},
	}
	if appErr := p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeChannelAdminDialog),
		Dialog:    dialog,
	}); appErr != nil {
		return appErr
	}
	return nil
}

func (p *Plugin) executeBotRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().RequestEnabled(requestTypeBot) {
		return ephemeralResponse(botRequestsDisabledMsg), nil
	}
	if err := p.openBotRequestDialog(args.TriggerId); err != nil {
		p.API.LogError("failed to open bot request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the bot request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

func (p *Plugin) executeIncomingWebhookRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().RequestEnabled(requestTypeWebhook) {
		return ephemeralResponse(webhookRequestsDisabledMsg), nil
	}
	if err := p.openIncomingWebhookDialog(args.TriggerId); err != nil {
		p.API.LogError("failed to open incoming webhook request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the incoming webhook request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

func (p *Plugin) executeOutgoingWebhookRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().RequestEnabled(requestTypeWebhook) {
		return ephemeralResponse(webhookRequestsDisabledMsg), nil
	}
	if err := p.openOutgoingWebhookDialog(args.TriggerId); err != nil {
		p.API.LogError("failed to open outgoing webhook request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the outgoing webhook request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

func commandHelpText() string {
	return "**Mattermost Request Hub** — request admin-gated access.\n\n" +
		"| Subcommand | What it does |\n|---|---|\n" +
		"| `/mattermost-request-hub docs` | Open the full documentation guide |\n" +
		"| `/mattermost-request-hub channel` | Request a new channel |\n" +
		"| `/mattermost-request-hub channel-admin` | Request Channel Admin role |\n" +
		"| `/mattermost-request-hub team-admin` | Request Team Admin role in this team |\n" +
		"| `/mattermost-request-hub team` | Request a new team |\n" +
		"| `/mattermost-request-hub team-member` | Request adding users to this team |\n" +
		"| `/mattermost-request-hub team-guest` | Request a guest invite to this team |\n" +
		"| `/mattermost-request-hub bot` | Request a bot account |\n" +
		"| `/mattermost-request-hub webhook-incoming` | Request an incoming webhook |\n" +
		"| `/mattermost-request-hub webhook-outgoing` | Request an outgoing webhook |"
}

func ephemeralResponse(text string) *model.CommandResponse {
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         text,
	}
}
