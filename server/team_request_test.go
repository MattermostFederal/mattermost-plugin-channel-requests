package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSubmitTeamCreationRequest_DisabledWhenToggleOff(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	p.setConfiguration(&configuration{AllowTeamRequests: false})
	_, err := p.submitTeamCreationRequest(teamCreationInput{RequesterID: "u_req", DisplayName: "Marketing"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "disabled")
}

func TestSubmitTeamCreationRequest_RequiresDisplayName(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	p.setConfiguration(&configuration{AllowTeamRequests: true})
	_, err := p.submitTeamCreationRequest(teamCreationInput{RequesterID: "u_req", DisplayName: "   "})
	require.Error(t, err)
	require.Contains(t, err.Error(), "team name is required")
}

func TestSubmitTeamCreationRequest_InvalidURLName(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	p.setConfiguration(&configuration{AllowTeamRequests: true})
	// "!!!" slugifies to "" (all special chars stripped), which fails IsValidTeamName.
	_, err := p.submitTeamCreationRequest(teamCreationInput{
		RequesterID: "u_req",
		DisplayName: "!!!",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a valid team URL name")
}

func TestSubmitTeamCreationRequest_StoresAndPostsRequest(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{ApprovalTeam: "appteam", AllowTeamRequests: true})

	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "reqer", Roles: "system_user"}, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)

	// ensureTeamCreationApprovalChannel routes team creation to its own channel.
	api.On("GetTeamByName", "appteam").Return(&model.Team{Id: "appteam-id", DisplayName: "App Team"}, nil)
	stubApprovalTeamRecord(api)
	api.On("GetChannelByName", "appteam-id", "mattermost-team-requests", false).
		Return(&model.Channel{Type: model.ChannelTypePrivate, CreatorId: "bot-user-id", Id: "teams-ch", TeamId: "appteam-id"}, nil)

	// systemAdminMentions + forceTicketChannelCategory share GetUsers.
	api.On("GetUsers", mock.Anything).Return([]*model.User{{Id: "sys1", Username: "root"}}, nil)
	api.On("GetUser", "sys1").Return(&model.User{Id: "sys1", Username: "root"}, nil)
	api.On("GetChannelSidebarCategories", "sys1", "appteam-id").Return(&model.OrderedSidebarCategories{
		Categories: []*model.SidebarCategoryWithChannels{{
			SidebarCategory: model.SidebarCategory{DisplayName: "Mattermost: Request Hub"},
			Channels:        []string{"teams-ch"},
		}},
	}, nil).Maybe()

	var approvalPost *model.Post
	api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool {
		return post.ChannelId == "teams-ch"
	})).Run(func(args mock.Arguments) {
		approvalPost = args.Get(0).(*model.Post)
	}).Return(&model.Post{Id: "approval-post-1"}, nil)

	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "req-dm"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{Id: "dm-post-1"}, nil)

	msg, err := p.submitTeamCreationRequest(teamCreationInput{
		RequesterID: "u_req",
		DisplayName: "Marketing",
		Type:        "O",
	})

	require.NoError(t, err)
	require.Contains(t, msg, "submitted for approval")
	require.NotNil(t, approvalPost)
	require.Equal(t, "teams-ch", approvalPost.ChannelId)
	require.Contains(t, approvalPost.Message, "@root")
	api.AssertCalled(t, "KVSet", mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "CreateTeam", mock.Anything)
}

func TestCreateTeamForRequest_AddsRequesterAsMember(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("CreateTeam", mock.Anything).Return(&model.Team{Id: "new-team", Name: "eng", DisplayName: "Engineering"}, nil)
	api.On("CreateTeamMember", "new-team", "u_req").Return(&model.TeamMember{}, nil)

	team, err := p.createTeamForRequest(&teamCreationRequest{
		RequesterID: "u_req",
		Name:        "eng",
		DisplayName: "Engineering",
		Type:        "O",
	})

	require.NoError(t, err)
	require.Equal(t, "new-team", team.Id)
	api.AssertCalled(t, "CreateTeamMember", "new-team", "u_req")
	// No role promotion when RequestTeamAdmin is false.
	api.AssertNotCalled(t, "UpdateTeamMemberRoles", mock.Anything, mock.Anything, mock.Anything)
}

func TestCreateTeamForRequest_PromotesRequesterWhenFlagSet(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("CreateTeam", mock.Anything).Return(&model.Team{Id: "new-team", Name: "eng", DisplayName: "Engineering"}, nil)
	api.On("CreateTeamMember", "new-team", "u_req").Return(&model.TeamMember{}, nil)
	api.On("UpdateTeamMemberRoles", "new-team", "u_req", teamAdminRoles).Return(&model.TeamMember{}, nil)

	team, err := p.createTeamForRequest(&teamCreationRequest{
		RequesterID:      "u_req",
		Name:             "eng",
		DisplayName:      "Engineering",
		Type:             "O",
		RequestTeamAdmin: true,
	})

	require.NoError(t, err)
	require.Equal(t, "new-team", team.Id)
	api.AssertCalled(t, "UpdateTeamMemberRoles", "new-team", "u_req", teamAdminRoles)
}
