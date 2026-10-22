package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/external/pluginmcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pkg/errors"
)

// The tools are a third entry point beside the slash command and the
// webapp modal, and go through the same submitRequest and
// submitAdminRequest, which own validation and membership checks. Every
// request, a System Admin's included, is queued for approval. Each tool
// honors the same per-type setting as the slash command and the modal.
// list_my_requests and get_request_status use the same listOwnRequests and
// requestStatus as the list subcommand. Approving and denying are
// deliberately not tools: they are the human checkpoint, on the approval
// card and the slash command.

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
	Message string `json:"message" jsonschema:"What happened: the request was submitted for approval"`
}

type ListMyRequestsArgs struct{}

type GetRequestStatusArgs struct {
	RequestID string `json:"request_id" jsonschema:"The request ID, from list_my_requests or the approval card"`
}

type MCPRequest struct {
	ID        string `json:"id" jsonschema:"The request ID"`
	Kind      string `json:"kind" jsonschema:"channel for a new channel, channel_admin for Channel Admins on an existing channel"`
	Summary   string `json:"summary" jsonschema:"What was requested and where, in Markdown"`
	State     string `json:"state" jsonschema:"pending while it waits for an approver, or expired when it was not reviewed in time and an approver can close it"`
	CreatedAt string `json:"created_at,omitempty" jsonschema:"When it was submitted, in RFC 3339"`
	ExpiresAt string `json:"expires_at,omitempty" jsonschema:"When it expires, in RFC 3339. Absent when requests don't expire"`
}

type ListMyRequestsOutput struct {
	Requests []MCPRequest `json:"requests" jsonschema:"The user's pending requests, oldest first, at most 50. Approved and denied requests are not kept"`
	More     int          `json:"more,omitempty" jsonschema:"How many more pending requests there are beyond the ones listed"`
}

// The kind values the tools report.
const (
	mcpKindChannel      = "channel"
	mcpKindChannelAdmin = "channel_admin"
)

var errNoMCPUser = errors.New("no user is associated with this request")

// errMCPInternal is what a tool returns when its handler panicked.
var errMCPInternal = errors.New("internal error")

// addTool registers a tool with guardTool around its handler, so every
// tool gets the same recovery.
func addTool[In, Out any](p *Plugin, server *pluginmcp.Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	pluginmcp.AddTool(server, tool, guardTool(p, tool.Name, handler))
}

// guardTool turns a panic in a tool handler into an "internal error"
// result and a log line, instead of a failed call to the Agents plugin.
func guardTool[In, Out any](p *Plugin, name string, handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (result *mcp.CallToolResult, out Out, err error) {
		defer func() {
			if r := recover(); r != nil {
				p.API.LogError("MCP tool failed", "tool", name, "panic", fmt.Sprint(r))
				var zero Out
				result, out, err = nil, zero, errMCPInternal
			}
		}()
		return handler(ctx, req, in)
	}
}

func (p *Plugin) registerMCPTools(server *pluginmcp.Server) {
	addTool(p, server, &mcp.Tool{
		Name:        "list_prefixes",
		Description: "List the channel prefixes a channel request can use, with what each is for and any rule for the rest of the name.",
	}, p.listPrefixesTool)

	addTool(p, server, &mcp.Tool{
		Name:        "request_channel",
		Description: "Request a new channel on the user's behalf. The request is posted for admin approval. Call list_prefixes first for the prefix.",
	}, p.requestChannelTool)

	addTool(p, server, &mcp.Tool{
		Name:        "request_channel_admin",
		Description: "Request that existing members of a channel be made Channel Admins, on the user's behalf. The request is posted for admin approval.",
	}, p.requestChannelAdminTool)

	addTool(p, server, &mcp.Tool{
		Name:        "list_my_requests",
		Description: "List the user's own pending channel and Channel Admin requests, with each one's ID, state and expiry. Approved and denied requests are not listed; the user gets a direct message with each outcome.",
	}, p.listMyRequestsTool)

	addTool(p, server, &mcp.Tool{
		Name:        "get_request_status",
		Description: "Look up one pending channel or Channel Admin request by ID. Works for the user's own requests and for requests the user can approve. A request that is no longer pending, or that the user can't see, is reported as not found.",
	}, p.getRequestStatusTool)
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

	message, err := p.submitAdminRequest(userID, in.ChannelID, nomineeIDs)
	if err != nil {
		return nil, MCPMessageOutput{}, err
	}
	return nil, MCPMessageOutput{Message: message}, nil
}

func (p *Plugin) listMyRequestsTool(ctx context.Context, _ *mcp.CallToolRequest, _ ListMyRequestsArgs) (*mcp.CallToolResult, ListMyRequestsOutput, error) {
	userID := pluginmcp.GetUserID(ctx)
	if userID == "" {
		return nil, ListMyRequestsOutput{}, errNoMCPUser
	}
	config := p.getConfiguration()
	if !config.RequestEnabled(requestTypeChannel) && !config.RequestEnabled(requestTypeChannelAdmin) {
		return nil, ListMyRequestsOutput{}, errors.New(listRequestsDisabledMsg)
	}

	requests, more, err := p.listOwnRequests(userID, maxListedRequests)
	if err != nil {
		p.API.LogError("failed to list pending requests", "user_id", userID, "error", err.Error())
		return nil, ListMyRequestsOutput{}, errors.New("could not load your requests; try again")
	}
	out := ListMyRequestsOutput{Requests: make([]MCPRequest, 0, len(requests)), More: more}
	for _, req := range requests {
		out.Requests = append(out.Requests, mcpRequest(req))
	}
	return nil, out, nil
}

func (p *Plugin) getRequestStatusTool(ctx context.Context, _ *mcp.CallToolRequest, in GetRequestStatusArgs) (*mcp.CallToolResult, MCPRequest, error) {
	userID := pluginmcp.GetUserID(ctx)
	if userID == "" {
		return nil, MCPRequest{}, errNoMCPUser
	}

	req, err := p.requestStatus(userID, in.RequestID)
	if errors.Is(err, errNoVisibleRequest) {
		return nil, MCPRequest{}, err
	}
	if err != nil {
		p.API.LogError("failed to load request status", "user_id", userID, "request_id", in.RequestID, "error", err.Error())
		return nil, MCPRequest{}, errors.New("could not load that request; try again")
	}
	return nil, mcpRequest(*req), nil
}

// mcpRequest converts a summary for the tools.
func mcpRequest(s requestSummary) MCPRequest {
	kind, state := mcpKindChannel, "pending"
	if s.Kind == pendingKindAdmin {
		kind = mcpKindChannelAdmin
	}
	if s.Expired {
		state = "expired"
	}
	return MCPRequest{
		ID:        s.ID,
		Kind:      kind,
		Summary:   s.Summary,
		State:     state,
		CreatedAt: rfc3339(s.CreatedAt),
		ExpiresAt: rfc3339(s.ExpiresAt),
	}
}

// rfc3339 renders a millisecond time, or "" for 0.
func rfc3339(millis int64) string {
	if millis <= 0 {
		return ""
	}
	return model.GetTimeForMillis(millis).UTC().Format(time.RFC3339)
}
