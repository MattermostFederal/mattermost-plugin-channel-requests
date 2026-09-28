package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const commandTrigger = "mattermost-permissions"

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
		AutoCompleteDesc: "Request admin-gated permissions (channels, teams, admin roles)",
		AutoCompleteHint: "[channel|channel-admin|team-admin|team|bot|webhook-incoming|webhook-outgoing]",
		AutocompleteData: getAutocompleteData(),
		DisplayName:      "Mattermost Permissions",
	}
}

func getAutocompleteData() *model.AutocompleteData {
	root := model.NewAutocompleteData(commandTrigger, "[subcommand]", "Request admin-gated permissions")
	root.AddCommand(model.NewAutocompleteData("channel", "", "Request creation of a new channel"))
	root.AddCommand(model.NewAutocompleteData("channel-admin", "", "Request Channel Admin role on an existing channel"))
	root.AddCommand(model.NewAutocompleteData("team-admin", "", "Request Team Admin role in the current team"))
	root.AddCommand(model.NewAutocompleteData("team", "", "Request creation of a new team"))
	root.AddCommand(model.NewAutocompleteData("bot", "", "Request creation of a bot account"))
	root.AddCommand(model.NewAutocompleteData("webhook-incoming", "", "Request an incoming webhook (external → Mattermost)"))
	root.AddCommand(model.NewAutocompleteData("webhook-outgoing", "", "Request an outgoing webhook (Mattermost → external)"))
	return root
}

func (p *Plugin) ExecuteCommand(_ *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	fields := strings.Fields(args.Command)
	subcommand := ""
	if len(fields) >= 2 {
		subcommand = strings.ToLower(fields[1])
	}

	switch subcommand {
	case "channel", "":
		return p.executeChannelRequestCommand(args)
	case "channel-admin":
		return p.executeChannelAdminRequestCommand(args)
	case "team-admin":
		return p.executeTeamAdminRequestCommand(args)
	case "team":
		return p.executeTeamCreationRequestCommand(args)
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

func (p *Plugin) executeChannelRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().UsesPrefixList() {
		return ephemeralResponse("Channel requests aren't configured yet. Ask a System Admin to define at least one channel prefix in System Console → Plugins → Mattermost Permissions."), nil
	}
	if err := p.openRequestDialog(args.TriggerId, args.TeamId); err != nil {
		p.API.LogError("failed to open channel request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the channel request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

func (p *Plugin) executeChannelAdminRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if err := p.openChannelAdminRequestDialog(args.TriggerId); err != nil {
		p.API.LogError("failed to open channel-admin request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the Channel Admin request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

func (p *Plugin) executeTeamAdminRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if err := p.openTeamAdminRequestDialog(args.TriggerId, args.TeamId); err != nil {
		p.API.LogError("failed to open team-admin request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the Team Admin request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

func (p *Plugin) executeTeamCreationRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if err := p.openTeamCreationRequestDialog(args.TriggerId); err != nil {
		p.API.LogError("failed to open team creation request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the team creation request form. Please try again."), nil
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
	return p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeChannelAdminDialog),
		Dialog:    dialog,
	})
}

func (p *Plugin) executeBotRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if err := p.openBotRequestDialog(args.TriggerId); err != nil {
		p.API.LogError("failed to open bot request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the bot request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

func (p *Plugin) executeIncomingWebhookRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if err := p.openIncomingWebhookDialog(args.TriggerId); err != nil {
		p.API.LogError("failed to open incoming webhook request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the incoming webhook request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

func (p *Plugin) executeOutgoingWebhookRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if err := p.openOutgoingWebhookDialog(args.TriggerId); err != nil {
		p.API.LogError("failed to open outgoing webhook request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the outgoing webhook request form. Please try again."), nil
	}
	return &model.CommandResponse{}, nil
}

func commandHelpText() string {
	return "**Mattermost Permissions** — request admin-gated access.\n\n" +
		"| Subcommand | What it does |\n|---|---|\n" +
		"| `/mattermost-permissions channel` | Request a new channel |\n" +
		"| `/mattermost-permissions channel-admin` | Request Channel Admin role |\n" +
		"| `/mattermost-permissions team-admin` | Request Team Admin role in this team |\n" +
		"| `/mattermost-permissions team` | Request a new team |\n" +
		"| `/mattermost-permissions bot` | Request a bot account |\n" +
		"| `/mattermost-permissions webhook-incoming` | Request an incoming webhook |\n" +
		"| `/mattermost-permissions webhook-outgoing` | Request an outgoing webhook |"
}

func ephemeralResponse(text string) *model.CommandResponse {
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         text,
	}
}
