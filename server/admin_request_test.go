package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPromoteChannelAdmins_AddsAndPromotesEachNominee(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("AddChannelMember", "ch1", mock.Anything).Return(&model.ChannelMember{}, nil)
	api.On("UpdateChannelMemberRoles", "ch1", mock.Anything, channelAdminRoles).Return(&model.ChannelMember{}, nil)

	p.promoteChannelAdmins(&adminRequest{ChannelID: "ch1", NomineeIDs: []string{"u1", "u2"}})

	api.AssertNumberOfCalls(t, "AddChannelMember", 2)
	api.AssertCalled(t, "AddChannelMember", "ch1", "u1")
	api.AssertCalled(t, "AddChannelMember", "ch1", "u2")
	api.AssertNumberOfCalls(t, "UpdateChannelMemberRoles", 2)
	api.AssertCalled(t, "UpdateChannelMemberRoles", "ch1", "u1", channelAdminRoles)
	api.AssertCalled(t, "UpdateChannelMemberRoles", "ch1", "u2", channelAdminRoles)
}

func TestPromoteChannelAdmins_AddFailureStillAttemptsPromotion(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	// Existing member: AddChannelMember errors (already a member), but the
	// promotion must still be attempted.
	api.On("AddChannelMember", "ch1", "u1").Return(nil, testAppErr("already a member"))
	api.On("UpdateChannelMemberRoles", "ch1", "u1", channelAdminRoles).Return(&model.ChannelMember{}, nil)

	p.promoteChannelAdmins(&adminRequest{ChannelID: "ch1", NomineeIDs: []string{"u1"}})

	api.AssertCalled(t, "UpdateChannelMemberRoles", "ch1", "u1", channelAdminRoles)
}

func TestSubmitAdminRequest_ValidationErrors(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})

	_, err := p.submitAdminRequest("u_req", "", []string{"u1"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "channel is required")

	_, err = p.submitAdminRequest("u_req", "ch1", []string{"", "  "})
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least one person")
}

func TestSubmitAdminRequest_SysadminPromotesImmediately(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Name: "marketing"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "reqer", Roles: "system_user system_admin"}, nil)
	api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nommy"}, nil)
	api.On("AddChannelMember", "ch1", "u_nom").Return(&model.ChannelMember{}, nil)
	api.On("UpdateChannelMemberRoles", "ch1", "u_nom", channelAdminRoles).Return(&model.ChannelMember{}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	msg, err := p.submitAdminRequest("u_req", "ch1", []string{"u_nom"})

	require.NoError(t, err)
	require.Contains(t, msg, "marketing")
	// Promotes immediately — no request is stored for approval.
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
	api.AssertCalled(t, "UpdateChannelMemberRoles", "ch1", "u_nom", channelAdminRoles)
}

func TestSubmitAdminRequest_PostsToApprovalChannelMentioningApprovers(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{ApprovalTeam: "appteam", ApprovalChannel: "channel-requests"})

	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Name: "marketing", TeamId: "team1"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "reqer", Roles: "system_user"}, nil)
	api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nommy"}, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)

	// Approval destination + approver set: one System Admin + one Team Admin
	// of the target channel's team.
	api.On("GetChannelByNameForTeamName", "appteam", "channel-requests", false).Return(&model.Channel{Id: "appch"}, nil)
	api.On("GetUsers", mock.Anything).Return([]*model.User{{Id: "sys1"}}, nil)
	api.On("GetTeamMembers", "team1", mock.Anything, mock.Anything).Return([]*model.TeamMember{
		{TeamId: "team1", UserId: "ta1", Roles: "team_user team_admin"},
		{TeamId: "team1", UserId: "tu1", Roles: "team_user"},
	}, nil)
	api.On("GetUser", "sys1").Return(&model.User{Id: "sys1", Username: "root"}, nil)
	api.On("GetUser", "ta1").Return(&model.User{Id: "ta1", Username: "lead"}, nil)

	var posted *model.Post
	api.On("CreatePost", mock.Anything).Run(func(args mock.Arguments) {
		posted = args.Get(0).(*model.Post)
	}).Return(&model.Post{}, nil)

	msg, err := p.submitAdminRequest("u_req", "ch1", []string{"u_nom"})

	require.NoError(t, err)
	require.Contains(t, msg, "submitted for approval")
	api.AssertCalled(t, "KVSet", mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything)

	// Posted to the approval channel, mentioning the System Admin + Team Admin
	// (not the plain team member).
	require.NotNil(t, posted)
	require.Equal(t, "appch", posted.ChannelId)
	require.Contains(t, posted.Message, "@root")
	require.Contains(t, posted.Message, "@lead")
}

func TestSubmitAdminRequest_NoApprovalChannelTellsRequester(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	// No approval channel configured.
	p.setConfiguration(&configuration{})

	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Name: "marketing", TeamId: "team1"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "reqer", Roles: "system_user"}, nil)
	// The request is stored, then rolled back when posting fails (the config
	// check returns before the nominee attachment is ever built).
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)
	api.On("KVDelete", mock.Anything).Return(nil)

	_, err := p.submitAdminRequest("u_req", "ch1", []string{"u_nom"})

	require.Error(t, err)
	require.Contains(t, err.Error(), "No approval channel is configured")
	// Nothing was posted, and the stored request was rolled back.
	api.AssertNotCalled(t, "CreatePost", mock.Anything)
	api.AssertCalled(t, "KVDelete", mock.Anything)
}

func TestCanApproveAdminRequest_TeamAdminMayApprove(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", TeamId: "team1"}, nil)

	// A Team Admin of the target channel's team may approve.
	teamAdmin := &model.User{Id: "u_ta", Username: "lead", Roles: "system_user"}
	api.On("GetTeamMember", "team1", "u_ta").Return(&model.TeamMember{TeamId: "team1", UserId: "u_ta", Roles: "team_user team_admin"}, nil)
	require.True(t, p.canApproveAdminRequest(teamAdmin, "ch1"))

	// A plain team member may not.
	plainMember := &model.User{Id: "u_member", Username: "joe", Roles: "system_user"}
	api.On("GetTeamMember", "team1", "u_member").Return(&model.TeamMember{TeamId: "team1", UserId: "u_member", Roles: "team_user"}, nil)
	require.False(t, p.canApproveAdminRequest(plainMember, "ch1"))

	// A System Admin may approve regardless of team membership.
	sysAdmin := &model.User{Id: "u_sys", Username: "root", Roles: "system_user system_admin"}
	require.True(t, p.canApproveAdminRequest(sysAdmin, "ch1"))
}
