package main

import (
	"context"

	"github.com/mattermost/mattermost-plugin-agents/v2/external/pluginmcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pkg/errors"
)

// The tools are a third entry point beside the slash command and the
// webapp modal, and go through the same submitRequest and
// submitAdminRequest, which own validation, membership checks and the
// System Admin shortcut. Like the webapp modal, a System Admin's request
// takes effect at once. Each tool honors the same per-type setting as the
// slash command and the modal. Approving and denying are deliberately not
// tools: they are the human checkpoint.

type ListPrefixesArgs struct{}

type MCPPrefix struct {
	Prefix      string `json:"prefix" jsonschema:"The prefix exactly as it must be passed to request_channel, such as team-"`
	Description string `json:"description,omitempty" jsonschema:"What channels under this prefix are for"`
	SuffixRule  string `json:"suffix_rule,omitempty" jsonschema:"The rule for the part after the prefix: a maximum length, or a pattern it must match. Empty means no rule"`
}

type ListPrefixesOutput struct {
	Prefixes []MCPPrefix `json:"prefixes" jsonschema:"The channel prefixes a request can use"`
}

type RequestChannelArgs struct {
	TeamID       string   `json:"team_id" jsonschema:"The ID of the team to create the channel in"`
	Prefix       string   `json:"prefix" jsonschema:"One of the prefixes from list_prefixes, exactly as listed"`
	DisplayName  string   `json:"display_name" jsonschema:"The channel's display name, at most 64 characters"`
	Name         string   `json:"name,omitempty" jsonschema:"The URL suffix after the prefix; generated from display_name when blank"`
	Purpose      string   `json:"purpose,omitempty" jsonschema:"The channel purpose, at most 250 characters"`
	ChannelType  string   `json:"channel_type,omitempty" jsonschema:"O for a public channel (the default) or P for a private one"`
	Members      []string `json:"members,omitempty" jsonschema:"Usernames to add as members, at most 100"`
	AdminMembers []string `json:"admin_members,omitempty" jsonschema:"Usernames to make Channel Admins, at most 100"`
}

type RequestChannelAdminArgs struct {
	ChannelID string   `json:"channel_id" jsonschema:"The ID of an existing public or private channel the user is a member of"`
	Nominees  []string `json:"nominees" jsonschema:"Usernames of channel members to make Channel Admins, at most 100"`
}

type MCPMessageOutput struct {
	Message string `json:"message" jsonschema:"What happened: the request is pending approval, or it took effect at once"`
}

var errNoMCPUser = errors.New("no user is associated with this request")

func (p *Plugin) registerMCPTools(server *pluginmcp.Server) {
	pluginmcp.AddTool(server, &mcp.Tool{
		Name:        "list_prefixes",
		Description: "List the channel prefixes a channel request can use, with what each is for and any rule for the rest of the name.",
	}, p.listPrefixesTool)

	pluginmcp.AddTool(server, &mcp.Tool{
		Name:        "request_channel",
		Description: "Request a new channel on the user's behalf. The request is posted for admin approval, unless the user is a System Admin, in which case the channel is created at once. Call list_prefixes first for the prefix.",
	}, p.requestChannelTool)

	pluginmcp.AddTool(server, &mcp.Tool{
		Name:        "request_channel_admin",
		Description: "Request that existing members of a channel be made Channel Admins, on the user's behalf. The request is posted for admin approval, unless the user is a System Admin, in which case they are promoted at once.",
	}, p.requestChannelAdminTool)
}

func (p *Plugin) listPrefixesTool(ctx context.Context, _ *mcp.CallToolRequest, _ ListPrefixesArgs) (*mcp.CallToolResult, ListPrefixesOutput, error) {
	if pluginmcp.GetUserID(ctx) == "" {
		return nil, ListPrefixesOutput{}, errNoMCPUser
	}

	config := p.getConfiguration()
	if !config.RequestEnabled(requestTypeChannel) {
		return nil, ListPrefixesOutput{}, errors.New(channelRequestsDisabledMsg)
	}
	if !config.UsesPrefixList() {
		return nil, ListPrefixesOutput{}, errors.New(notConfiguredMessage)
	}

	out := ListPrefixesOutput{Prefixes: make([]MCPPrefix, 0, len(config.prefixes))}
	for _, pf := range config.prefixes {
		out.Prefixes = append(out.Prefixes, MCPPrefix{
			Prefix:      pf.Prefix,
			Description: pf.Description,
			SuffixRule:  pf.SuffixPatternRaw,
		})
	}
	return nil, out, nil
}

func (p *Plugin) requestChannelTool(ctx context.Context, _ *mcp.CallToolRequest, in RequestChannelArgs) (*mcp.CallToolResult, MCPMessageOutput, error) {
	userID := pluginmcp.GetUserID(ctx)
	if userID == "" {
		return nil, MCPMessageOutput{}, errNoMCPUser
	}
	if !p.getConfiguration().RequestEnabled(requestTypeChannel) {
		return nil, MCPMessageOutput{}, errors.New(channelRequestsDisabledMsg)
	}

	memberIDs, err := p.resolveUsernameList(in.Members)
	if err != nil {
		return nil, MCPMessageOutput{}, err
	}
	adminMemberIDs, err := p.resolveUsernameList(in.AdminMembers)
	if err != nil {
		return nil, MCPMessageOutput{}, err
	}

	message, err := p.submitRequest(requestInput{
		RequesterID:    userID,
		TeamID:         in.TeamID,
		DisplayName:    in.DisplayName,
		Name:           in.Name,
		Prefix:         in.Prefix,
		Purpose:        in.Purpose,
		ChannelType:    in.ChannelType,
		MemberIDs:      memberIDs,
		AdminMemberIDs: adminMemberIDs,
	})
	if err != nil {
		return nil, MCPMessageOutput{}, err
	}
	return nil, MCPMessageOutput{Message: message}, nil
}

func (p *Plugin) requestChannelAdminTool(ctx context.Context, _ *mcp.CallToolRequest, in RequestChannelAdminArgs) (*mcp.CallToolResult, MCPMessageOutput, error) {
	userID := pluginmcp.GetUserID(ctx)
	if userID == "" {
		return nil, MCPMessageOutput{}, errNoMCPUser
	}
	if !p.getConfiguration().RequestEnabled(requestTypeChannelAdmin) {
		return nil, MCPMessageOutput{}, errors.New(channelAdminRequestsDisabledMsg)
	}

	nomineeIDs, err := p.resolveUsernameList(in.Nominees)
	if err != nil {
		return nil, MCPMessageOutput{}, err
	}

	message, err := p.submitAdminRequest(userID, in.ChannelID, nomineeIDs, false)
	if err != nil {
		return nil, MCPMessageOutput{}, err
	}
	return nil, MCPMessageOutput{Message: message}, nil
}
