package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSubmitTeamGuestRequest_Disabled(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	p.setConfiguration(&configuration{AllowTeamGuestRequests: false})
	_, err := p.submitTeamGuestRequest("u_req", "team1", "", []string{"ch1"}, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "disabled")
}

func TestSubmitTeamGuestRequest_RequiresChannel(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	p.setConfiguration(&configuration{AllowTeamGuestRequests: true})
	_, err := p.submitTeamGuestRequest("u_req", "team1", "a@b.com", nil, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "channel")
}

func TestTeamInviteLink(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	api.On("GetConfig").Return(&model.Config{
		ServiceSettings: model.ServiceSettings{SiteURL: strPtr("https://mm.example.com")},
	})

	link, err := p.teamInviteLink(&model.Team{InviteId: "abc123"})
	require.NoError(t, err)
	require.Equal(t, "https://mm.example.com/signup_user_complete/?id=abc123", link)
}

func TestSubmitTeamGuestRequest_QueuesForApproval(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{ApprovalTeam: "appteam", AllowTeamGuestRequests: true})

	api.On("GetTeam", "team1").Return(&model.Team{Id: "team1", DisplayName: "Ops", InviteId: "inv1"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "reqer", Roles: "system_user"}, nil)
	api.On("GetTeamMember", "team1", "u_req").Return(&model.TeamMember{TeamId: "team1", UserId: "u_req"}, nil)
	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", TeamId: "team1"}, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)

	api.On("GetTeamByName", "appteam").Return(&model.Team{Id: "appteam-id"}, nil)
	api.On("GetChannelByName", "appteam-id", "mattermost-team-requests", false).
		Return(&model.Channel{Id: "team-req-ch", TeamId: "appteam-id"}, nil)
	api.On("GetUsers", mock.Anything).Return([]*model.User{{Id: "sys1", Username: "root"}}, nil)
	api.On("GetUser", "sys1").Return(&model.User{Id: "sys1", Username: "root"}, nil)
	api.On("GetChannelSidebarCategories", "sys1", "appteam-id").Return(&model.OrderedSidebarCategories{
		Categories: []*model.SidebarCategoryWithChannels{{
			SidebarCategory: model.SidebarCategory{DisplayName: "Mattermost: Request Hub"},
			Channels:        []string{"team-req-ch"},
		}},
	}, nil).Maybe()

	api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool {
		return post.ChannelId == "team-req-ch"
	})).Return(&model.Post{Id: "approval-post-1"}, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "req-dm"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{Id: "dm-post-1"}, nil)

	msg, err := p.submitTeamGuestRequest("u_req", "team1", "guest@example.com", []string{"ch1"}, "vendor", true)
	require.NoError(t, err)
	require.Contains(t, msg, "submitted")
}
