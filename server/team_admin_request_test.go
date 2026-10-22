package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSubmitTeamAdminRequest_RequiresTeam(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	_, err := p.submitTeamAdminRequest("u_req", "", []string{testNomineeID})
	require.Error(t, err)
	require.Contains(t, err.Error(), "team is required")
}

func TestSubmitTeamAdminRequest_RequiresNominees(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	_, err := p.submitTeamAdminRequest("u_req", "team1", []string{"", "  "})
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least one person")
}

func TestSubmitTeamAdminRequest_RequesterMustBeTeamMember(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetTeam", "team1").Return(&model.Team{Id: "team1", DisplayName: "Sales"}, nil)
	// Non-admin requester who is not a team member.
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "reqer", Roles: "system_user"}, nil)
	api.On("GetTeamMember", "team1", "u_req").Return(nil, testAppErr("not a member"))

	_, err := p.submitTeamAdminRequest("u_req", "team1", []string{testNomineeID})

	require.Error(t, err)
	require.Contains(t, err.Error(), "member of the team")
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
}

func TestSubmitTeamAdminRequest_StoresAndPostsRequest(t *testing.T) {
	api := &plugintest.API{}
	stubPendingLists(api)
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{ApprovalTeam: "appteam", AllowTeamAdminRequests: true})

	api.On("GetTeam", "team1").Return(&model.Team{Id: "team1", DisplayName: "Sales"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "reqer", Roles: "system_user"}, nil)
	api.On("GetTeamMember", "team1", "u_req").Return(&model.TeamMember{UserId: "u_req"}, nil)
	// mentionList calls GetUser for each nominee (in the attachment Text field).
	api.On("GetUser", testNomineeID).Return(&model.User{Id: testNomineeID, Username: "nommy"}, nil)
	api.On("GetTeamMember", "team1", testNomineeID).Return(&model.TeamMember{UserId: testNomineeID}, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)

	// Team Admin requests go to the dedicated team-requests channel via
	// ensureTeamCreationApprovalChannel → ensureNamedApprovalChannel.
	api.On("GetTeamByName", "appteam").Return(&model.Team{Id: "appteam-id"}, nil)
	stubApprovalTeamRecord(api)
	api.On("GetChannelByName", "appteam-id", "mattermost-team-requests", false).
		Return(&model.Channel{Type: model.ChannelTypePrivate, CreatorId: "bot-user-id", Id: "team-req-ch", TeamId: "appteam-id"}, nil)
	// systemAdminMentions + forceTicketChannelCategory share GetUsers.
	api.On("GetUsers", mock.Anything).Return([]*model.User{{Id: "sys1", Username: "root"}}, nil)
	api.On("GetUser", "sys1").Return(&model.User{Id: "sys1", Username: "root"}, nil)
	api.On("GetChannelSidebarCategories", "sys1", "appteam-id").Return(&model.OrderedSidebarCategories{
		Categories: []*model.SidebarCategoryWithChannels{{
			SidebarCategory: model.SidebarCategory{DisplayName: "Mattermost: Request Hub"},
			Channels:        []string{"team-req-ch"},
		}},
	}, nil).Maybe()

	// Approval post captured to verify destination.
	var approvalPost *model.Post
	api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool {
		return post.ChannelId == "team-req-ch"
	})).Run(func(args mock.Arguments) {
		approvalPost = args.Get(0).(*model.Post)
	}).Return(&model.Post{Id: "approval-post-1"}, nil)

	// DM notification to requester.
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "req-dm"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{Id: "dm-post-1"}, nil)

	msg, err := p.submitTeamAdminRequest("u_req", "team1", []string{testNomineeID})

	require.NoError(t, err)
	require.Contains(t, msg, "submitted for approval")
	require.NotNil(t, approvalPost)
	require.Equal(t, "team-req-ch", approvalPost.ChannelId)
	require.Contains(t, approvalPost.Message, "@root")
	api.AssertCalled(t, "KVSet", mock.Anything, mock.Anything)
	// No promotion yet — pending approval.
	api.AssertNotCalled(t, "UpdateTeamMemberRoles", mock.Anything, mock.Anything, mock.Anything)
}

func TestPromoteTeamAdmins_AddsMemberAndRole(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("CreateTeamMember", "team1", mock.Anything).Return(&model.TeamMember{}, nil)
	api.On("UpdateTeamMemberRoles", "team1", mock.Anything, teamAdminRoles).Return(&model.TeamMember{}, nil)

	p.promoteTeamAdmins(&teamAdminRequest{TeamID: "team1", NomineeIDs: []string{"u1", "u2"}})

	api.AssertNumberOfCalls(t, "CreateTeamMember", 2)
	api.AssertCalled(t, "CreateTeamMember", "team1", "u1")
	api.AssertCalled(t, "CreateTeamMember", "team1", "u2")
	api.AssertNumberOfCalls(t, "UpdateTeamMemberRoles", 2)
	api.AssertCalled(t, "UpdateTeamMemberRoles", "team1", "u1", teamAdminRoles)
	api.AssertCalled(t, "UpdateTeamMemberRoles", "team1", "u2", teamAdminRoles)
}

func TestPromoteTeamAdmins_ReseedsPerTeamApprovalChannel(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{PerTeamApprovalChannels: true})

	api.On("CreateTeamMember", "team1", "u1").Return(&model.TeamMember{}, nil)
	api.On("UpdateTeamMemberRoles", "team1", "u1", teamAdminRoles).Return(&model.TeamMember{}, nil)

	// hasActiveTeamApprovers → true via new Team Admin
	api.On("GetUsers", mock.Anything).Return([]*model.User{}, nil).Maybe()
	api.On("GetTeamMembers", "team1", 0, 200).Return([]*model.TeamMember{
		{TeamId: "team1", UserId: "u1", Roles: "team_user team_admin", SchemeAdmin: true},
	}, nil)

	existing := &model.Channel{Type: model.ChannelTypePrivate, CreatorId: "bot-user-id", Id: "ch-req", TeamId: "team1", Name: "mattermost-channel-requests"}
	api.On("GetChannelByName", "team1", "mattermost-channel-requests", false).Return(existing, nil)
	api.On("AddChannelMember", "ch-req", "u1").Return(&model.ChannelMember{}, nil)
	api.On("GetChannelSidebarCategories", "u1", "team1").Return(&model.OrderedSidebarCategories{
		Categories: []*model.SidebarCategoryWithChannels{{
			SidebarCategory: model.SidebarCategory{DisplayName: "Mattermost: Request Hub"},
			Channels:        []string{"ch-req"},
		}},
	}, nil)
	api.On("GetUsersInChannel", "ch-req", "username", 0, 200).Return([]*model.User{{Id: "u1"}}, nil).Maybe()
	api.On("KVGet", mock.Anything).Return(nil, nil).Maybe()
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()

	p.promoteTeamAdmins(&teamAdminRequest{TeamID: "team1", NomineeIDs: []string{"u1"}})

	api.AssertCalled(t, "AddChannelMember", "ch-req", "u1")
}

func TestPromoteTeamAdmins_MemberAddFailureStillAttemptsRoleGrant(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	// CreateTeamMember fails (already a member), but role grant must still run.
	api.On("CreateTeamMember", "team1", "u1").Return(nil, testAppErr("already a member"))
	api.On("UpdateTeamMemberRoles", "team1", "u1", teamAdminRoles).Return(&model.TeamMember{}, nil)

	p.promoteTeamAdmins(&teamAdminRequest{TeamID: "team1", NomineeIDs: []string{"u1"}})

	api.AssertCalled(t, "UpdateTeamMemberRoles", "team1", "u1", teamAdminRoles)
}
