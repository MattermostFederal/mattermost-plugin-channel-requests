package main

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pkg/errors"
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

// Arguments that `channel` and `channel-admin` accept in place of the form,
// and the `prefixes` subcommand. Shared by the autocomplete hints and the
// usage table so they can't drift apart.
const (
	channelArgsHint      = "<prefix> <Channel name> [--suffix <suffix>] [--purpose <text>] [--private] [--members @user ...] [--admins @user ...] [--category <name>]"
	channelAdminArgsHint = "@user [@user ...]"
	subcommandPrefixes   = "prefixes"
	subcommandList       = "list"
	subcommandApprove    = "approve"
	subcommandDeny       = "deny"
	requestIDArgHint     = "<request ID>"
)

// notConfiguredMessage is shown by every channel request entry point when
// no prefix is configured.
const notConfiguredMessage = "Channel requests aren't configured yet. Ask a System Admin to define at least one channel prefix in System Console → Plugins → Mattermost Request Hub."

func getCommand() *model.Command {
	return &model.Command{
		Trigger:          commandTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "Request admin-gated permissions (channels, teams, admin roles) or open docs",
		AutoCompleteHint: "[docs|help|channel|channel-admin|prefixes|list|approve|deny|team-admin|team|team-member|team-guest|bot|webhook-incoming|webhook-outgoing]",
		AutocompleteData: getAutocompleteData(),
		DisplayName:      "Mattermost Request Hub",
	}
}

func getAutocompleteData() *model.AutocompleteData {
	root := model.NewAutocompleteData(commandTrigger, "[subcommand]", "Request admin-gated access via Request Hub")
	root.AddCommand(model.NewAutocompleteData("docs", "", "Open the full documentation guide in your browser"))
	root.AddCommand(model.NewAutocompleteData("help", "", "Show the subcommands and their arguments"))
	root.AddCommand(model.NewAutocompleteData("channel", "["+channelArgsHint+"]", "Request creation of a new channel (no arguments opens the form)"))
	root.AddCommand(model.NewAutocompleteData("channel-admin", "["+channelAdminArgsHint+"]", "Request Channel Admin role (no arguments opens the form; with users, for this channel)"))
	root.AddCommand(model.NewAutocompleteData(subcommandPrefixes, "", "List the channel prefixes you can use"))
	root.AddCommand(model.NewAutocompleteData(subcommandList, "", "List your pending channel and Channel Admin requests and their IDs"))
	root.AddCommand(model.NewAutocompleteData(subcommandApprove, requestIDArgHint, "Approve a channel or Channel Admin request by the ID on its approval card"))
	root.AddCommand(model.NewAutocompleteData(subcommandDeny, requestIDArgHint, "Deny a channel or Channel Admin request by the ID on its approval card"))
	root.AddCommand(model.NewAutocompleteData("team-admin", "", "Request Team Admin role in the current team"))
	root.AddCommand(model.NewAutocompleteData("team", "", "Request creation of a new team"))
	root.AddCommand(model.NewAutocompleteData("team-member", "", "Request adding users or bots to the current team"))
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

	var params []string
	if len(fields) > 2 {
		params = fields[2:]
	}

	switch subcommand {
	case "docs", "documentation":
		return p.executeDocsCommand(args)
	case "help":
		return ephemeralResponse(commandHelpText() + "\n\n" + p.docsLinkText(args)), nil
	case "channel", "":
		if len(params) > 0 {
			return p.executeChannelCreateCommand(args, params), nil
		}
		return p.executeChannelRequestCommand(args)
	case "channel-admin":
		if len(params) > 0 {
			return p.executeChannelAdminNominateCommand(args, params), nil
		}
		return p.executeChannelAdminRequestCommand(args)
	case subcommandPrefixes:
		return p.executePrefixesCommand(), nil
	case subcommandList:
		return p.executeList(args), nil
	case subcommandApprove:
		return p.executeDecision(args, params, true), nil
	case subcommandDeny:
		return p.executeDecision(args, params, false), nil
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
		return ephemeralResponse(fmt.Sprintf("Unknown subcommand `%s`.\n\n%s", fields[1], commandHelpText())), nil
	}
}

// executeDocsCommand posts an ephemeral markdown link to the bundled HTML
// guide (public/help/docs.html). Do NOT set GotoLocation: Mattermost treats
// same-origin /plugins/... URLs as in-app React routes, which leaves the
// channel view stuck on "Loading..." forever. Markdown links to /plugins/
// open in a new tab as a full document load instead.
func (p *Plugin) executeDocsCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text: "**Documentation**\n\n" +
			p.docsLinkText(args) + "\n\n" +
			"Covers channels, Channel Admin, teams, bots, webhooks, messaging threads, and slash commands.",
	}, nil
}

// docsLinkText is the Markdown link to the bundled guide.
func (p *Plugin) docsLinkText(args *model.CommandArgs) string {
	path := fmt.Sprintf("/plugins/%s/public/help/docs.html", manifest.Id)
	location := path
	if args != nil && args.SiteURL != "" {
		location = strings.TrimRight(args.SiteURL, "/") + path
	}
	return "[Open the full guide](" + location + ") (opens in a new tab)."
}

func (p *Plugin) executeChannelRequestCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if !p.getConfiguration().RequestEnabled(requestTypeChannel) {
		return ephemeralResponse(channelRequestsDisabledMsg), nil
	}
	if !p.getConfiguration().UsesPrefixList() {
		return ephemeralResponse(notConfiguredMessage), nil
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
	return "**Mattermost Request Hub**: request admin-gated access.\n\n" +
		"| Subcommand | What it does |\n|---|---|\n" +
		"| `/mattermost-request-hub docs` | Open the full documentation guide |\n" +
		"| `/mattermost-request-hub help` | Show this table |\n" +
		"| `/mattermost-request-hub channel` | Request a new channel (opens the form) |\n" +
		"| `/mattermost-request-hub channel " + channelArgsHint + "` | Request a new channel without the form |\n" +
		"| `/mattermost-request-hub channel-admin` | Request Channel Admin role (opens the form) |\n" +
		"| `/mattermost-request-hub channel-admin " + channelAdminArgsHint + "` | Request Channel Admin for those members of this channel |\n" +
		"| `/mattermost-request-hub prefixes` | List the channel prefixes you can use |\n" +
		"| `/mattermost-request-hub list` | List your pending channel and Channel Admin requests and their IDs |\n" +
		"| `/mattermost-request-hub approve " + requestIDArgHint + "` | Approve a channel or Channel Admin request, as its Approve button would |\n" +
		"| `/mattermost-request-hub deny " + requestIDArgHint + "` | Deny a channel or Channel Admin request, as its Deny button would |\n" +
		"| `/mattermost-request-hub team-admin` | Request Team Admin role in this team |\n" +
		"| `/mattermost-request-hub team` | Request a new team |\n" +
		"| `/mattermost-request-hub team-member` | Request adding users or bots to this team |\n" +
		"| `/mattermost-request-hub team-guest` | Request a guest invite to this team |\n" +
		"| `/mattermost-request-hub bot` | Request a bot account |\n" +
		"| `/mattermost-request-hub webhook-incoming` | Request an incoming webhook |\n" +
		"| `/mattermost-request-hub webhook-outgoing` | Request an outgoing webhook |"
}

// channelCreateInput is the parsed form of `channel <prefix> <Channel name> ...`.
type channelCreateInput struct {
	Prefix      string
	DisplayName string
	Suffix      string
	Purpose     string
	Private     bool
	Members     []string
	Admins      []string
	Category    string
}

// parseChannelCreateArgs parses `<prefix> <Channel name...> [--flag values...]`.
// Each flag takes the words up to the next flag.
func parseChannelCreateArgs(params []string) (channelCreateInput, error) {
	var in channelCreateInput
	if len(params) < 2 || strings.HasPrefix(params[0], "--") || strings.HasPrefix(params[1], "--") {
		return in, errors.New("a prefix and a channel name are required")
	}
	in.Prefix = params[0]

	i := 1
	var name []string
	for ; i < len(params) && !strings.HasPrefix(params[i], "--"); i++ {
		name = append(name, params[i])
	}
	in.DisplayName = strings.Join(name, " ")

	seen := map[string]bool{}
	for i < len(params) {
		flag := params[i]
		i++
		var values []string
		for ; i < len(params) && !strings.HasPrefix(params[i], "--"); i++ {
			values = append(values, params[i])
		}
		if seen[flag] {
			return in, errors.Errorf("`%s` is given more than once", flag)
		}
		seen[flag] = true

		switch flag {
		case "--suffix":
			if len(values) != 1 {
				return in, errors.New("`--suffix` takes exactly one value")
			}
			in.Suffix = values[0]
		case "--purpose":
			if len(values) == 0 {
				return in, errors.New("`--purpose` needs some text")
			}
			in.Purpose = strings.Join(values, " ")
		case "--private":
			if len(values) != 0 {
				return in, errors.New("`--private` takes no value")
			}
			in.Private = true
		case "--members":
			in.Members = values
		case "--admins":
			in.Admins = values
		case "--category":
			if len(values) == 0 {
				return in, errors.New("`--category` needs a name")
			}
			in.Category = strings.Join(values, " ")
		default:
			return in, errors.Errorf("unknown option `%s`", flag)
		}
	}
	return in, nil
}

// executeChannelCreateCommand submits a channel request without the form.
// It goes through submitRequest like the dialog does, and like every
// slash-command entry point it always queues the request for approval.
func (p *Plugin) executeChannelCreateCommand(args *model.CommandArgs, params []string) *model.CommandResponse {
	config := p.getConfiguration()
	if !config.RequestEnabled(requestTypeChannel) {
		return ephemeralResponse(channelRequestsDisabledMsg)
	}
	if !config.UsesPrefixList() {
		return ephemeralResponse(notConfiguredMessage)
	}

	parsed, err := parseChannelCreateArgs(params)
	if err != nil {
		return ephemeralResponse(fmt.Sprintf("Could not read the request: %s.\nUsage: `/%s channel %s`", err.Error(), commandTrigger, channelArgsHint))
	}

	memberIDs, err := p.resolveUsernameList(parsed.Members)
	if err != nil {
		return ephemeralResponse(err.Error())
	}
	adminMemberIDs, err := p.resolveUsernameList(parsed.Admins)
	if err != nil {
		return ephemeralResponse(err.Error())
	}

	channelType := channelTypeOpen
	if parsed.Private {
		channelType = channelTypePrivate
	}

	message, err := p.submitRequest(requestInput{
		RequesterID:    args.UserId,
		TeamID:         args.TeamId,
		DisplayName:    parsed.DisplayName,
		Name:           parsed.Suffix,
		Prefix:         parsed.Prefix,
		Purpose:        parsed.Purpose,
		ChannelType:    channelType,
		MemberIDs:      memberIDs,
		AdminMemberIDs: adminMemberIDs,
		CategoryName:   parsed.Category,
		ForceQueue:     true,
	})
	if err != nil {
		return ephemeralResponse(err.Error())
	}
	return ephemeralResponse(message)
}

// executeChannelAdminNominateCommand requests Channel Admin for the named
// users in the channel the command was run from. Same path as the
// Channel Admin dialog, so it always queues for approval.
func (p *Plugin) executeChannelAdminNominateCommand(args *model.CommandArgs, params []string) *model.CommandResponse {
	if !p.getConfiguration().RequestEnabled(requestTypeChannelAdmin) {
		return ephemeralResponse(channelAdminRequestsDisabledMsg)
	}

	nomineeIDs, err := p.resolveUsernameList(params)
	if err != nil {
		return ephemeralResponse(err.Error())
	}
	if len(nomineeIDs) == 0 {
		return ephemeralResponse(fmt.Sprintf("Name at least one channel member: `/%s channel-admin %s`", commandTrigger, channelAdminArgsHint))
	}

	message, err := p.submitAdminRequest(args.UserId, args.ChannelId, nomineeIDs, true)
	if err != nil {
		return ephemeralResponse(err.Error())
	}
	return ephemeralResponse(message)
}

// executePrefixesCommand lists the configured prefixes, the same data the
// prefixes endpoint serves to the webapp.
func (p *Plugin) executePrefixesCommand() *model.CommandResponse {
	config := p.getConfiguration()
	if !config.RequestEnabled(requestTypeChannel) {
		return ephemeralResponse(channelRequestsDisabledMsg)
	}
	if !config.UsesPrefixList() {
		return ephemeralResponse(notConfiguredMessage)
	}

	var b strings.Builder
	b.WriteString("Channel prefixes you can use:\n")
	for _, pf := range config.prefixes {
		fmt.Fprintf(&b, "- `%s`", pf.Prefix)
		if pf.Description != "" {
			fmt.Fprintf(&b, " %s", escapeMarkdown(pf.Description))
		}
		if pf.SuffixPatternRaw != "" {
			fmt.Fprintf(&b, " (suffix rule: `%s`)", pf.SuffixPatternRaw)
		}
		b.WriteString("\n")
	}
	return ephemeralResponse(strings.TrimRight(b.String(), "\n"))
}

// executeList lists the user's pending channel and Channel Admin requests.
func (p *Plugin) executeList(args *model.CommandArgs) *model.CommandResponse {
	config := p.getConfiguration()
	if !config.RequestEnabled(requestTypeChannel) && !config.RequestEnabled(requestTypeChannelAdmin) {
		return ephemeralResponse(listRequestsDisabledMsg)
	}
	requests, more, err := p.listOwnRequests(args.UserId, maxListedRequests)
	if err != nil {
		p.API.LogError("failed to list pending requests", "user_id", args.UserId, "error", err.Error())
		return ephemeralResponse("Could not load your requests. Please try again.")
	}
	if len(requests) == 0 {
		return ephemeralResponse("You have no pending channel or Channel Admin requests.")
	}

	var b strings.Builder
	b.WriteString("Your pending requests:\n")
	for _, req := range requests {
		fmt.Fprintf(&b, "- %s\n", req.summaryLine())
	}
	if more > 0 {
		fmt.Fprintf(&b, "- ...and %d more\n", more)
	}
	return ephemeralResponse(strings.TrimRight(b.String(), "\n"))
}

// executeDecision approves or denies a channel or Channel Admin request by
// ID, through the same checks as the approval card buttons.
func (p *Plugin) executeDecision(args *model.CommandArgs, params []string, approve bool) *model.CommandResponse {
	sub := subcommandDeny
	if approve {
		sub = subcommandApprove
	}
	if len(params) != 1 || !model.IsValidId(params[0]) {
		return ephemeralResponse(fmt.Sprintf("Give the request ID from the approval card: `/%s %s %s`", commandTrigger, sub, requestIDArgHint))
	}
	requestID := params[0]

	// IDs are unique across both kinds, so whichever is stored is the one.
	isChannel, err := p.pendingRequestExists(pendingKindChannel, requestID)
	if err != nil {
		p.API.LogError("failed to look up request", "request_id", requestID, "error", err.Error())
		return ephemeralResponse("Could not load that request.")
	}
	if isChannel {
		return ephemeralResponse(p.decideChannelRequest(args.UserId, requestID, "", approve).replyText())
	}
	return ephemeralResponse(p.decideAdminRequest(args.UserId, requestID, "", approve).replyText())
}

func ephemeralResponse(text string) *model.CommandResponse {
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         text,
	}
}
