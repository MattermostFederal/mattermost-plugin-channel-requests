package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestExecuteCommand_NotConfiguredDoesNotOpenDialog(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{}) // no prefixes configured

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	require.Contains(t, resp.Text, "aren't configured")
	// A doomed dialog must not be opened when no prefix is configured.
	api.AssertNotCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_OpensDialogWhenConfigured(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{prefixes: []channelPrefix{{Prefix: "team-"}}})

	api.On("OpenInteractiveDialog", mock.Anything).Return(nil)

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	api.AssertCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_DialogFailureRepliesPrivately(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{prefixes: []channelPrefix{{Prefix: "team-", Description: "Teams"}}})

	api.On("OpenInteractiveDialog", mock.Anything).Return(testAppErr("expired trigger"))

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.Equal(t, model.CommandResponseTypeEphemeral, resp.ResponseType)
	require.Contains(t, resp.Text, "Could not open")
}

func runCommand(p *Plugin, command string) *model.CommandResponse {
	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{
		Command:   command,
		UserId:    "u1",
		TeamId:    "team1",
		ChannelId: "chX",
		TriggerId: "trig",
	})
	if appErr != nil {
		panic(appErr)
	}
	return resp
}

func TestExecuteCommand_HelpAndUnknownSubcommand(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	resp := runCommand(p, "/channel-request help")
	require.Equal(t, model.CommandResponseTypeEphemeral, resp.ResponseType)
	require.Equal(t, commandUsage, resp.Text)

	resp = runCommand(p, "/channel-request bogus")
	require.Equal(t, model.CommandResponseTypeEphemeral, resp.ResponseType)
	require.Contains(t, resp.Text, "Unknown subcommand `bogus`")
	require.Contains(t, resp.Text, commandUsage)
}

func TestExecuteCommand_BareCommandWithTrailingSpaceOpensDialog(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{prefixes: []channelPrefix{{Prefix: "team-"}}})

	api.On("OpenInteractiveDialog", mock.Anything).Return(nil)

	resp := runCommand(p, "/channel-request   ")

	require.Empty(t, resp.Text)
}

func TestExecuteCommand_Prefixes(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)

	p.setConfiguration(&configuration{})
	resp := runCommand(p, "/channel-request prefixes")
	require.Equal(t, notConfiguredMessage, resp.Text)

	p.setConfiguration(&configuration{prefixes: []channelPrefix{
		{Prefix: "team-", Description: "Team *channels*"},
		{Prefix: "proj-", SuffixPatternRaw: "^[0-9]{4}$"},
	}})
	resp = runCommand(p, "/channel-request prefixes")
	require.Equal(t, model.CommandResponseTypeEphemeral, resp.ResponseType)
	require.Equal(t, "Channel prefixes you can use:\n"+
		"- `team-` Team \\*channels\\*\n"+
		"- `proj-` (suffix rule: `^[0-9]{4}$`)", resp.Text)
}

func TestParseCreateArgs(t *testing.T) {
	in, err := parseCreateArgs(strings.Fields("team- Marketing Team --suffix mkt --purpose Plan the launch --private --members @alice bob --admins @carol"))
	require.NoError(t, err)
	require.Equal(t, createCommandInput{
		Prefix:      "team-",
		DisplayName: "Marketing Team",
		Suffix:      "mkt",
		Purpose:     "Plan the launch",
		Private:     true,
		Members:     []string{"@alice", "bob"},
		Admins:      []string{"@carol"},
	}, in)

	in, err = parseCreateArgs([]string{"team-", "Marketing"})
	require.NoError(t, err)
	require.Equal(t, createCommandInput{Prefix: "team-", DisplayName: "Marketing"}, in)

	for args, want := range map[string]string{
		"":                                    "a prefix and a channel name are required",
		"team-":                               "a prefix and a channel name are required",
		"--private Marketing":                 "a prefix and a channel name are required",
		"team- --private":                     "a prefix and a channel name are required",
		"team- Marketing --suffix":            "`--suffix` takes exactly one value",
		"team- Marketing --suffix a b":        "`--suffix` takes exactly one value",
		"team- Marketing --purpose":           "`--purpose` needs some text",
		"team- Marketing --private yes":       "`--private` takes no value",
		"team- Marketing --color blue":        "unknown option `--color`",
		"team- Marketing --private --private": "`--private` is given more than once",
	} {
		_, err := parseCreateArgs(strings.Fields(args))
		require.EqualError(t, err, want, args)
	}
}

func TestExecuteCommand_CreateNotConfigured(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	resp := runCommand(p, "/channel-request create team- Marketing")

	require.Equal(t, notConfiguredMessage, resp.Text)
}

func TestExecuteCommand_CreateBadArguments(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(approvalConfig())

	resp := runCommand(p, "/channel-request create team-")

	require.Equal(t, model.CommandResponseTypeEphemeral, resp.ResponseType)
	require.Contains(t, resp.Text, "Could not read the request: a prefix and a channel name are required.")
	require.Contains(t, resp.Text, commandUsage)
}

func TestExecuteCommand_CreateUnknownUsers(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(approvalConfig())

	api.On("GetUserByUsername", "ghost").Return(nil, testAppErr("not found"))
	api.On("GetUserByUsername", "alice").Return(&model.User{Id: "id_alice"}, nil)

	resp := runCommand(p, "/channel-request create team- Marketing --members @ghost")
	require.Equal(t, "unknown user: ghost", resp.Text)

	resp = runCommand(p, "/channel-request create team- Marketing --members @alice --admins @ghost")
	require.Equal(t, "unknown user: ghost", resp.Text)

	api.AssertNotCalled(t, "GetUser", mock.Anything)
}

func TestExecuteCommand_CreateSubmitsForApproval(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	stubPendingLists(api)
	stubSubmittedRequestExists(api, kvRequestPrefix)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(approvalConfig())

	mockMemberRequester(api)
	alice, bob, carol := model.NewId(), model.NewId(), model.NewId()
	for id, name := range map[string]string{alice: "alice", bob: "bob", carol: "carol"} {
		api.On("GetUserByUsername", name).Return(&model.User{Id: id, Username: name}, nil)
		api.On("GetUser", id).Return(&model.User{Id: id, Username: name}, nil)
	}
	var stored channelRequest
	api.On("KVSet", isRequestRecordKey, mock.Anything).Run(func(args mock.Arguments) {
		require.NoError(t, json.Unmarshal(args.Get(1).([]byte), &stored))
	}).Return(nil)
	api.On("GetTeam", "team1").Return(&model.Team{Id: "team1", Name: "eng", DisplayName: "Engineering"}, nil)
	api.On("GetChannel", "appch").Return(&model.Channel{Id: "appch", TeamId: "appteam"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	resp := runCommand(p, "/channel-request create team- Marketing  Team --suffix mkt --purpose Plan the launch --private --members @alice bob --admins @carol")

	require.Equal(t, model.CommandResponseTypeEphemeral, resp.ResponseType)
	require.Contains(t, resp.Text, "submitted for approval")
	require.Equal(t, channelRequest{
		ID:             stored.ID,
		RequesterID:    "u1",
		TeamID:         "team1",
		Name:           "team-mkt",
		DisplayName:    "Marketing Team",
		Purpose:        "Plan the launch",
		ChannelType:    channelTypePrivate,
		MemberIDs:      []string{alice, bob},
		AdminMemberIDs: []string{carol},
		CreatedAt:      stored.CreatedAt,
	}, stored)
	require.NotZero(t, stored.CreatedAt)
}

func TestExecuteCommand_CreateRejectedBySubmit(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(approvalConfig())

	api.On("GetUser", "u1").Return(&model.User{Id: "u1", Username: "req", Roles: "system_user"}, nil)
	api.On("GetTeamMember", "team1", "u1").Return(nil, testAppErr("not a member"))

	resp := runCommand(p, "/channel-request create team- Marketing")

	require.Equal(t, "you must be a member of the team to request a channel in it", resp.Text)
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
}

func TestExecuteCommand_AdminNeedsNominees(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	resp := runCommand(p, "/channel-request admin")

	require.Equal(t, model.CommandResponseTypeEphemeral, resp.ResponseType)
	require.Contains(t, resp.Text, "Name at least one channel member")
}

func TestExecuteCommand_AdminUnknownUser(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	api.On("GetUserByUsername", "ghost").Return(nil, testAppErr("not found"))

	resp := runCommand(p, "/channel-request admin @ghost")

	require.Equal(t, "unknown user: ghost", resp.Text)
}

func TestExecuteCommand_AdminRejectedBySubmit(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	api.On("GetUserByUsername", "nommy").Return(&model.User{Id: "u_nom"}, nil)
	api.On("GetUser", "u1").Return(&model.User{Id: "u1", Username: "req", Roles: "system_user"}, nil)
	api.On("GetChannelMember", "chX", "u1").Return(nil, testAppErr("not a member"))

	resp := runCommand(p, "/channel-request admin @nommy")

	require.Equal(t, "channel not found, or you are not a member of it", resp.Text)
}

func TestExecuteCommand_AdminPromotesInCurrentChannel(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	api.On("GetUserByUsername", "nommy").Return(&model.User{Id: "u_nom"}, nil)
	api.On("GetUser", "u1").Return(sysadmin("u1"), nil)
	api.On("GetChannel", "chX").Return(&model.Channel{Id: "chX", Name: "marketing", Type: model.ChannelTypeOpen}, nil)
	api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nommy"}, nil)
	api.On("GetChannelMember", "chX", "u_nom").Return(&model.ChannelMember{}, nil)
	api.On("UpdateChannelMemberRoles", "chX", "u_nom", channelAdminRoles).Return(&model.ChannelMember{}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	resp := runCommand(p, "/channel-request admin @nommy")

	require.Equal(t, model.CommandResponseTypeEphemeral, resp.ResponseType)
	require.Contains(t, resp.Text, "Channel Admin in ~marketing")
	api.AssertCalled(t, "UpdateChannelMemberRoles", "chX", "u_nom", channelAdminRoles)
}

func TestGetAutocompleteData_ListsEverySubcommand(t *testing.T) {
	data := getAutocompleteData()

	names := make([]string, 0, len(data.SubCommands))
	for _, sub := range data.SubCommands {
		names = append(names, sub.Trigger)
		require.NotEmpty(t, sub.HelpText, sub.Trigger)
		require.Contains(t, commandUsage, "/channel-request "+sub.Trigger, sub.Trigger)
	}
	require.Equal(t, []string{subcommandCreate, subcommandAdmin, subcommandPrefixes, subcommandList, subcommandApprove, subcommandDeny, subcommandExercise, subcommandHelp}, names)
	require.NoError(t, data.IsValid())
	// The hint names every subcommand, in the same order.
	require.Equal(t, "["+strings.Join(names, "|")+"]", subcommandHint)
	require.Equal(t, subcommandHint, getCommand().AutoCompleteHint)
}
