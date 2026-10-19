package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pkg/errors"
)

const commandTrigger = "channel-request"

const (
	subcommandCreate   = "create"
	subcommandAdmin    = "admin"
	subcommandPrefixes = "prefixes"
	subcommandList     = "list"
	subcommandApprove  = "approve"
	subcommandDeny     = "deny"
	subcommandHelp     = "help"
)

// subcommandHint lists the subcommands for the autocomplete hint.
const subcommandHint = "[create|admin|prefixes|list|approve|deny|help]"

// notConfiguredMessage is shown by every entry point that needs a prefix
// when none is configured.
const notConfiguredMessage = "Channel requests aren't configured yet. Ask a System Admin to define at least one channel prefix in System Console → Plugins → Channel Requests."

const commandUsage = "Usage:\n" +
	"- `/channel-request` opens the channel request form.\n" +
	"- `/channel-request create <prefix> <Channel name> [--suffix <suffix>] [--purpose <text>] [--private] [--members @user ...] [--admins @user ...]` requests a channel without the form.\n" +
	"- `/channel-request admin @user [@user ...]` asks for those channel members to be made Channel Admins of this channel.\n" +
	"- `/channel-request prefixes` lists the channel prefixes you can use.\n" +
	"- `/channel-request list` lists your pending requests and their IDs.\n" +
	"- `/channel-request approve <request ID>` approves a request, as its Approve button would.\n" +
	"- `/channel-request deny <request ID>` denies a request, as its Deny button would.\n" +
	"- `/channel-request help` shows this message."

func getCommand() *model.Command {
	return &model.Command{
		Trigger:          commandTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "Request the creation of a new channel for admin approval",
		AutoCompleteHint: subcommandHint,
		AutocompleteData: getAutocompleteData(),
		DisplayName:      "Channel Request",
	}
}

// getAutocompleteData drives the inline `/` suggestion UI. The bare
// command opens the request form; the subcommands cover the same
// operations as the plugin's REST API.
func getAutocompleteData() *model.AutocompleteData {
	autocomplete := model.NewAutocompleteData(
		commandTrigger,
		subcommandHint,
		"Request the creation of a new channel for admin approval",
	)

	create := model.NewAutocompleteData(subcommandCreate, "<prefix> <Channel name> [--suffix] [--purpose] [--private] [--members] [--admins]", "Request a channel without opening the form")
	create.AddTextArgument("Prefix, channel name and options", "<prefix> <Channel name> [--suffix <suffix>] [--purpose <text>] [--private] [--members @user ...] [--admins @user ...]", "")
	autocomplete.AddCommand(create)

	admin := model.NewAutocompleteData(subcommandAdmin, "@user [@user ...]", "Request Channel Admin for members of this channel")
	admin.AddTextArgument("Channel members to make Channel Admin", "@user [@user ...]", "")
	autocomplete.AddCommand(admin)

	prefixes := model.NewAutocompleteData(subcommandPrefixes, "", "List the channel prefixes you can use")
	autocomplete.AddCommand(prefixes)

	list := model.NewAutocompleteData(subcommandList, "", "List your pending requests and their IDs")
	autocomplete.AddCommand(list)

	approve := model.NewAutocompleteData(subcommandApprove, "<request ID>", "Approve a request by the ID on its approval card")
	approve.AddTextArgument("The request ID from the approval card", "<request ID>", "")
	autocomplete.AddCommand(approve)

	deny := model.NewAutocompleteData(subcommandDeny, "<request ID>", "Deny a request by the ID on its approval card")
	deny.AddTextArgument("The request ID from the approval card", "<request ID>", "")
	autocomplete.AddCommand(deny)

	help := model.NewAutocompleteData(subcommandHelp, "", "Show how to use /channel-request")
	autocomplete.AddCommand(help)

	return autocomplete
}

func (p *Plugin) ExecuteCommand(_ *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	fields := strings.Fields(args.Command)
	if len(fields) < 2 {
		return p.executeOpenForm(args), nil
	}

	switch fields[1] {
	case subcommandCreate:
		return p.executeCreate(args, fields[2:]), nil
	case subcommandAdmin:
		return p.executeAdmin(args, fields[2:]), nil
	case subcommandPrefixes:
		return p.executePrefixes(), nil
	case subcommandList:
		return p.executeList(args), nil
	case subcommandApprove:
		return p.executeDecision(args, fields[2:], true), nil
	case subcommandDeny:
		return p.executeDecision(args, fields[2:], false), nil
	case subcommandHelp:
		return ephemeralResponse(commandUsage), nil
	default:
		return ephemeralResponse(fmt.Sprintf("Unknown subcommand `%s`.\n%s", fields[1], commandUsage)), nil
	}
}

func (p *Plugin) executeOpenForm(args *model.CommandArgs) *model.CommandResponse {
	// Requests require at least one configured prefix; without one the
	// dialog could only ever fail on submit. Tell the user instead of
	// opening a doomed form.
	if !p.getConfiguration().UsesPrefixList() {
		return ephemeralResponse(notConfiguredMessage)
	}

	if err := p.openRequestDialog(args.TriggerId, args.TeamId); err != nil {
		p.API.LogError("failed to open channel request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the channel request form. Please try again.")
	}

	return &model.CommandResponse{}
}

// createCommandInput is the parsed form of `/channel-request create`.
type createCommandInput struct {
	Prefix      string
	DisplayName string
	Suffix      string
	Purpose     string
	Private     bool
	Members     []string
	Admins      []string
}

// parseCreateArgs parses `<prefix> <Channel name...> [--flag values...]`.
// Each flag takes the words up to the next flag.
func parseCreateArgs(params []string) (createCommandInput, error) {
	var in createCommandInput
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
		default:
			return in, errors.Errorf("unknown option `%s`", flag)
		}
	}
	return in, nil
}

// executeCreate submits a channel request through the same path as the
// webapp modal.
func (p *Plugin) executeCreate(args *model.CommandArgs, params []string) *model.CommandResponse {
	if !p.getConfiguration().UsesPrefixList() {
		return ephemeralResponse(notConfiguredMessage)
	}

	parsed, err := parseCreateArgs(params)
	if err != nil {
		return ephemeralResponse(fmt.Sprintf("Could not read the request: %s.\n%s", err.Error(), commandUsage))
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
	})
	if err != nil {
		return ephemeralResponse(err.Error())
	}
	return ephemeralResponse(message)
}

// executeAdmin requests Channel Admin for the named users in the channel
// the command was run from, through the same path as the webapp modal.
func (p *Plugin) executeAdmin(args *model.CommandArgs, params []string) *model.CommandResponse {
	if len(params) == 0 {
		return ephemeralResponse("Name at least one channel member: `/channel-request admin @user [@user ...]`")
	}

	nomineeIDs, err := p.resolveUsernameList(params)
	if err != nil {
		return ephemeralResponse(err.Error())
	}

	message, err := p.submitAdminRequest(args.UserId, args.ChannelId, nomineeIDs)
	if err != nil {
		return ephemeralResponse(err.Error())
	}
	return ephemeralResponse(message)
}

// executePrefixes lists the configured prefixes, the same data the
// prefixes endpoint serves to the webapp.
func (p *Plugin) executePrefixes() *model.CommandResponse {
	config := p.getConfiguration()
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

// executeList lists the user's pending requests.
func (p *Plugin) executeList(args *model.CommandArgs) *model.CommandResponse {
	requests, more, err := p.listOwnRequests(args.UserId, maxListedRequests)
	if err != nil {
		p.API.LogError("failed to list pending requests", "user_id", args.UserId, "error", err.Error())
		return ephemeralResponse("Could not load your requests. Please try again.")
	}
	if len(requests) == 0 {
		return ephemeralResponse("You have no pending requests.")
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

// executeDecision approves or denies a request by ID, through the same
// checks as the approval card buttons.
func (p *Plugin) executeDecision(args *model.CommandArgs, params []string, approve bool) *model.CommandResponse {
	sub := subcommandDeny
	if approve {
		sub = subcommandApprove
	}
	if len(params) != 1 || !model.IsValidId(params[0]) {
		return ephemeralResponse(fmt.Sprintf("Give the request ID from the approval card: `/channel-request %s <request ID>`", sub))
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
