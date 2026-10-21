package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const mcpToolPrefix = "com_mattermostfederal_mattermost-plugin-request-hub__"

// callMCPTool calls a tool over a real MCP session acting for userID.
func callMCPTool(t *testing.T, p *Plugin, userID, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := mcpSession(t, p, userID).CallTool(context.Background(), &mcp.CallToolParams{
		Name:      mcpToolPrefix + tool,
		Arguments: args,
	})
	require.NoError(t, err)
	return result
}

// mcpToolError returns the error text of a failed tool call.
func mcpToolError(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.True(t, result.IsError, "the tool call should have failed")
	require.NotEmpty(t, result.Content)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return text.Text
}

// mcpToolOutput decodes a successful tool call's structured output.
func mcpToolOutput[Out any](t *testing.T, result *mcp.CallToolResult) Out {
	t.Helper()
	require.False(t, result.IsError, "the tool call failed: %v", result.Content)
	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var out Out
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func TestListPrefixesTool_RefusesWithoutUserID(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	p.setConfiguration(&configuration{prefixes: []channelPrefix{{Prefix: "team-"}}})

	_, _, err := p.listPrefixesTool(context.Background(), nil, ListPrefixesArgs{})

	require.ErrorIs(t, err, errNoMCPUser)
}

func TestListPrefixesTool_NotConfigured(t *testing.T) {
	stubMCP(t)
	p := newMCPTestPlugin(t, &plugintest.API{}, &configuration{})

	result := callMCPTool(t, p, "u1", "list_prefixes", nil)

	require.Equal(t, notConfiguredMessage, mcpToolError(t, result))
}

func TestListPrefixesTool_ListsPrefixes(t *testing.T) {
	stubMCP(t)
	p := newMCPTestPlugin(t, &plugintest.API{}, &configuration{prefixes: []channelPrefix{
		{Prefix: "team-", Description: "Team channels", SuffixPatternRaw: "16"},
		{Prefix: "proj-"},
	}})

	out := mcpToolOutput[ListPrefixesOutput](t, callMCPTool(t, p, "u1", "list_prefixes", nil))

	require.Equal(t, []MCPPrefix{
		{Prefix: "team-", Description: "Team channels", SuffixRule: "16"},
		{Prefix: "proj-"},
	}, out.Prefixes)
}

func TestRequestChannelTool_RefusesWithoutUserID(t *testing.T) {
	stubMCP(t)
	api := &plugintest.API{}
	p := newMCPTestPlugin(t, api, &configuration{prefixes: []channelPrefix{{Prefix: "team-"}}})

	result := callMCPTool(t, p, "", "request_channel", map[string]any{
		"team_id": "team1", "prefix": "team-", "display_name": "Marketing", "members": []string{"alice"},
	})

	require.Equal(t, errNoMCPUser.Error(), mcpToolError(t, result))
	api.AssertNotCalled(t, "GetUserByUsername", mock.Anything)
	api.AssertNotCalled(t, "GetUser", mock.Anything)
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
}

func TestRequestChannelTool_UsesContextUserNotArgs(t *testing.T) {
	stubMCP(t)
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newMCPTestPlugin(t, api, &configuration{prefixes: []channelPrefix{{Prefix: "team-"}}})

	api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
	api.On("CreateChannel", mock.MatchedBy(func(c *model.Channel) bool {
		return c.CreatorId == "admin1" && c.TeamId == "team1" && c.Name == "team-marketing"
	})).Return(&model.Channel{Id: "ch1", Name: "team-marketing"}, nil)
	api.On("AddChannelMember", "ch1", "admin1").Return(&model.ChannelMember{}, nil)
	// With no Channel Admins named, the requester becomes one.
	api.On("UpdateChannelMemberRoles", "ch1", "admin1", channelAdminRoles).Return(&model.ChannelMember{}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	result := callMCPTool(t, p, "admin1", "request_channel", map[string]any{
		"team_id": "team1", "prefix": "team-", "display_name": "Marketing",
	})

	out := mcpToolOutput[MCPMessageOutput](t, result)
	require.Equal(t, "Created channel ~team-marketing.", out.Message)
}

func TestRequestChannelTool_NonMemberIsRefused(t *testing.T) {
	stubMCP(t)
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newMCPTestPlugin(t, api, &configuration{prefixes: []channelPrefix{{Prefix: "team-"}}})

	api.On("GetUser", "u1").Return(&model.User{Id: "u1", Username: "reqer", Roles: "system_user"}, nil)
	api.On("GetTeamMember", "team1", "u1").Return(nil, testAppErr("not a member"))

	result := callMCPTool(t, p, "u1", "request_channel", map[string]any{
		"team_id": "team1", "prefix": "team-", "display_name": "Marketing",
	})

	require.Equal(t, "you must be a member of the team to request a channel in it", mcpToolError(t, result))
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "CreateChannel", mock.Anything)
}

func TestRequestChannelTool_UnknownUsersAreRefused(t *testing.T) {
	for _, field := range []string{"members", "admin_members"} {
		t.Run(field, func(t *testing.T) {
			stubMCP(t)
			api := &plugintest.API{}
			defer api.AssertExpectations(t)
			p := newMCPTestPlugin(t, api, &configuration{prefixes: []channelPrefix{{Prefix: "team-"}}})

			api.On("GetUserByUsername", "ghost").Return(nil, testAppErr("not found"))

			result := callMCPTool(t, p, "u1", "request_channel", map[string]any{
				"team_id": "team1", "prefix": "team-", "display_name": "Marketing", field: []string{"@ghost"},
			})

			require.Equal(t, "unknown user: ghost", mcpToolError(t, result))
			api.AssertNotCalled(t, "GetUser", mock.Anything)
		})
	}
}

func TestRequestChannelAdminTool_RefusesWithoutUserID(t *testing.T) {
	stubMCP(t)
	api := &plugintest.API{}
	p := newMCPTestPlugin(t, api, &configuration{})

	result := callMCPTool(t, p, "", "request_channel_admin", map[string]any{
		"channel_id": "ch1", "nominees": []string{"nommy"},
	})

	require.Equal(t, errNoMCPUser.Error(), mcpToolError(t, result))
	api.AssertNotCalled(t, "GetUserByUsername", mock.Anything)
	api.AssertNotCalled(t, "GetUser", mock.Anything)
}

func TestRequestChannelAdminTool_PromotesForSysadmin(t *testing.T) {
	stubMCP(t)
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newMCPTestPlugin(t, api, &configuration{})

	api.On("GetUserByUsername", "nommy").Return(&model.User{Id: "u_nom", Username: "nommy"}, nil)
	api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
	api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nommy"}, nil)
	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Name: "marketing", Type: model.ChannelTypeOpen}, nil)
	api.On("GetChannelMember", "ch1", "u_nom").Return(&model.ChannelMember{}, nil)
	api.On("UpdateChannelMemberRoles", "ch1", "u_nom", channelAdminRoles).Return(&model.ChannelMember{}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	result := callMCPTool(t, p, "admin1", "request_channel_admin", map[string]any{
		"channel_id": "ch1", "nominees": []string{"@nommy"},
	})

	out := mcpToolOutput[MCPMessageOutput](t, result)
	require.Equal(t, "Promoted @nommy to Channel Admin in ~marketing.", out.Message)
}

func TestRequestChannelAdminTool_NonMemberIsRefused(t *testing.T) {
	stubMCP(t)
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newMCPTestPlugin(t, api, &configuration{})

	api.On("GetUserByUsername", "nommy").Return(&model.User{Id: "u_nom", Username: "nommy"}, nil)
	api.On("GetUser", "u1").Return(&model.User{Id: "u1", Username: "reqer", Roles: "system_user"}, nil)
	api.On("GetChannelMember", "ch1", "u1").Return(nil, testAppErr("not a member"))

	result := callMCPTool(t, p, "u1", "request_channel_admin", map[string]any{
		"channel_id": "ch1", "nominees": []string{"nommy"},
	})

	require.Equal(t, "channel not found, or you are not a member of it", mcpToolError(t, result))
	api.AssertNotCalled(t, "UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
}

func TestRequestChannelAdminTool_UnknownNomineeIsRefused(t *testing.T) {
	stubMCP(t)
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newMCPTestPlugin(t, api, &configuration{})

	api.On("GetUserByUsername", "ghost").Return(nil, testAppErr("not found"))

	result := callMCPTool(t, p, "u1", "request_channel_admin", map[string]any{
		"channel_id": "ch1", "nominees": []string{"ghost"},
	})

	require.Equal(t, "unknown user: ghost", mcpToolError(t, result))
	api.AssertNotCalled(t, "GetUser", mock.Anything)
}

func TestMCPTools_HonorRequestTypeSettings(t *testing.T) {
	for _, tc := range []struct {
		tool string
		args map[string]any
		off  func(c *configuration)
		want string
	}{
		{"list_prefixes", nil, func(c *configuration) { c.AllowChannelRequests = false }, channelRequestsDisabledMsg},
		{"request_channel", map[string]any{
			"team_id": "team1", "prefix": "team-", "display_name": "Marketing", "members": []string{"alice"},
		}, func(c *configuration) { c.AllowChannelRequests = false }, channelRequestsDisabledMsg},
		{"request_channel_admin", map[string]any{
			"channel_id": "ch1", "nominees": []string{"nommy"},
		}, func(c *configuration) { c.AllowChannelAdminRequests = false }, channelAdminRequestsDisabledMsg},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			stubMCP(t)
			api := &plugintest.API{}
			p := newMCPTestPlugin(t, api, &configuration{prefixes: []channelPrefix{{Prefix: "team-"}}})
			config := *p.getConfiguration()
			tc.off(&config)
			p.setConfiguration(&config)

			result := callMCPTool(t, p, "u1", tc.tool, tc.args)

			require.Equal(t, tc.want, mcpToolError(t, result))
			api.AssertNotCalled(t, "GetUserByUsername", mock.Anything)
			api.AssertNotCalled(t, "GetUser", mock.Anything)
		})
	}
}

func TestRequestChannelTool_QueuesForRegularMember(t *testing.T) {
	stubMCP(t)
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newMCPTestPlugin(t, api, approvalConfig())

	mockMemberRequester(api)
	var stored channelRequest
	api.On("KVSet", pendingKey, mock.Anything).Run(func(args mock.Arguments) {
		require.NoError(t, json.Unmarshal(args.Get(1).([]byte), &stored))
	}).Return(nil)
	api.On("GetTeam", "team1").Return(&model.Team{Id: "team1", Name: "eng", DisplayName: "Engineering"}, nil)
	mockApprovalChannel(api)
	api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool { return post.ChannelId == "appch" })).Return(&model.Post{Id: "card1"}, nil)
	api.On("GetDirectChannel", "u1", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool { return post.ChannelId == "dm1" })).Return(&model.Post{Id: "dmroot"}, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()

	out := mcpToolOutput[MCPMessageOutput](t, callMCPTool(t, p, "u1", "request_channel", map[string]any{
		"team_id": "team1", "prefix": "team-", "display_name": "Marketing",
	}))

	require.Contains(t, out.Message, "submitted for approval")
	require.Equal(t, "u1", stored.RequesterID)
	require.Equal(t, "team-marketing", stored.Name)
	api.AssertNotCalled(t, "CreateChannel", mock.Anything)
}
