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
	p.setConfiguration(&configuration{ApprovalTeam: "appteam"})

	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Name: "marketing", TeamId: "team1"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "reqer", Roles: "system_user"}, nil)
	api.On("GetChannelMember", "ch1", "u_req").Return(&model.ChannelMember{}, nil)
	api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nommy"}, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)

	// Approval destination + approver set: one System Admin + one Team Admin
	// of the target channel's team.
	api.On("GetChannelByNameForTeamName", "appteam", "mattermost-channel-requests", false).Return(&model.Channel{Id: "appch"}, nil)
	api.On("GetUsers", mock.Anything).Return([]*model.User{{Id: "sys1"}}, nil)
	api.On("GetTeamMembers", "team1", mock.Anything, mock.Anything).Return([]*model.TeamMember{
		{TeamId: "team1", UserId: "ta1", Roles: "team_user team_admin"},
		{TeamId: "team1", UserId: "tu1", Roles: "team_user"},
	}, nil)
	api.On("GetUser", "sys1").Return(&model.User{Id: "sys1", Username: "root"}, nil)
	api.On("GetUser", "ta1").Return(&model.User{Id: "ta1", Username: "lead"}, nil)

	// Capture only the post going to the approval channel so the requester DM
	// (a second CreatePost call) doesn't overwrite the assertion target.
	var posted *model.Post
	api.On("CreatePost", mock.MatchedBy(func(p *model.Post) bool {
		return p.ChannelId == "appch"
	})).Run(func(args mock.Arguments) {
		posted = args.Get(0).(*model.Post)
	}).Return(&model.Post{Id: "appch-post"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	// sendTicketCreatedDM opens a DM with the requester to start their ticket thread.
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "req-dm"}, nil)

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

func TestSubmitAdminRequest_NoApprovalChannelFallsBackToSystemAdmins(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	// No approval channel configured -> DM every System Admin, mirroring the
	// channel-creation flow, rather than dropping the request.
	p.setConfiguration(&configuration{})

	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Name: "marketing", TeamId: "team1"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "reqer", Roles: "system_user"}, nil)
	api.On("GetChannelMember", "ch1", "u_req").Return(&model.ChannelMember{}, nil)
	api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nommy"}, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)

	// adminApprovalHeader also lists approvers; supply one System Admin who is
	// both an approver mention and a DM recipient.
	api.On("GetUsers", mock.Anything).Return([]*model.User{{Id: "sys1", Username: "root"}}, nil)
	api.On("GetTeamMembers", "team1", mock.Anything, mock.Anything).Return([]*model.TeamMember{}, nil)
	api.On("GetUser", "sys1").Return(&model.User{Id: "sys1", Username: "root"}, nil)

	// Capture only the post going to the system-admin DM so the requester DM
	// (a second CreatePost call) doesn't overwrite the assertion target.
	var dmPost *model.Post
	api.On("GetDirectChannel", "sys1", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "req-dm"}, nil)
	api.On("CreatePost", mock.MatchedBy(func(p *model.Post) bool {
		return p.ChannelId == "dm1"
	})).Run(func(args mock.Arguments) {
		dmPost = args.Get(0).(*model.Post)
	}).Return(&model.Post{}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	msg, err := p.submitAdminRequest("u_req", "ch1", []string{"u_nom"})

	require.NoError(t, err)
	require.Contains(t, msg, "submitted for approval")
	// The request was DMed to the System Admin, not dropped.
	require.NotNil(t, dmPost)
	require.Equal(t, "dm1", dmPost.ChannelId)
	api.AssertCalled(t, "GetDirectChannel", "sys1", "bot-user-id")
}

func TestSubmitAdminRequest_RejectsNonChannelMember(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Name: "marketing", TeamId: "team1"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "reqer", Roles: "system_user"}, nil)
	// The requester is not a member of the channel.
	api.On("GetChannelMember", "ch1", "u_req").Return(nil, testAppErr("not a member"))

	_, err := p.submitAdminRequest("u_req", "ch1", []string{"u_nom"})

	require.Error(t, err)
	require.Contains(t, err.Error(), "member of the channel")
	// No promotion and no stored request when the membership gate fails.
	api.AssertNotCalled(t, "UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
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
