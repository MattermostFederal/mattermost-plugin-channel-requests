package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSubmitBotRequest_RequiresUsername(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	_, err := p.submitBotRequest("u1", botRequestInput{DisplayName: "My Bot"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "username is required")
}

// TestSubmitBotRequest_StripsAtPrefix verifies that "@  " (at-sign plus
// whitespace) reduces to an empty username and returns the username error
// rather than proceeding to a GetUser call.
func TestSubmitBotRequest_StripsAtPrefix(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	_, err := p.submitBotRequest("u1", botRequestInput{Username: "@  ", DisplayName: "My Bot"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "username is required")
}

func TestSubmitBotRequest_RequiresDisplayName(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	_, err := p.submitBotRequest("u1", botRequestInput{Username: "mybot", DisplayName: "  "})
	require.Error(t, err)
	require.Contains(t, err.Error(), "display name is required")
}

func TestSubmitBotRequest_SystemAdminCreatesDirectly(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetUser", "u_admin").Return(&model.User{Id: "u_admin", Username: "admin", Roles: "system_user system_admin"}, nil)
	api.On("CreateBot", mock.Anything).Return(&model.Bot{Username: "mybot"}, nil)

	msg, err := p.submitBotRequest("u_admin", botRequestInput{Username: "mybot", DisplayName: "My Bot"})
	require.NoError(t, err)
	require.Contains(t, msg, "mybot")
	// No approval queue — request not stored.
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
	api.AssertCalled(t, "CreateBot", mock.Anything)
}

// TestSubmitBotRequest_SystemAdminWithOwner verifies that when an explicit
// owner user ID is provided, CreateBot is called with that owner rather than
// the requester.
func TestSubmitBotRequest_SystemAdminWithOwner(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetUser", "u_admin").Return(&model.User{Id: "u_admin", Username: "admin", Roles: "system_user system_admin"}, nil)
	api.On("CreateBot", mock.MatchedBy(func(b *model.Bot) bool {
		return b.OwnerId == "explicit-owner"
	})).Return(&model.Bot{Username: "svc-bot"}, nil)

	msg, err := p.submitBotRequest("u_admin", botRequestInput{Username: "svc-bot", DisplayName: "Service Bot", Description: "does stuff", OwnerUserID: "explicit-owner"})
	require.NoError(t, err)
	require.Contains(t, msg, "svc-bot")
}

func TestSubmitBotRequest_RegularUserQueuesInBotChannel(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{ApprovalTeam: "appteam"})

	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "reqer", Roles: "system_user"}, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)

	// ensureBotApprovalChannel
	api.On("GetTeamByName", "appteam").Return(&model.Team{Id: "appteam-id", DisplayName: "App Team"}, nil)
	api.On("GetChannelByName", "appteam-id", "mattermost-bot-requests", false).
		Return(&model.Channel{Id: "bots-ch"}, nil)

	// systemAdminMentions
	api.On("GetUsers", mock.Anything).Return([]*model.User{{Id: "sys1", Username: "root"}}, nil)
	api.On("GetUser", "sys1").Return(&model.User{Id: "sys1", Username: "root"}, nil)

	var approvalPost *model.Post
	api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool {
		return post.ChannelId == "bots-ch"
	})).Run(func(args mock.Arguments) {
		approvalPost = args.Get(0).(*model.Post)
	}).Return(&model.Post{Id: "approval-1"}, nil)

	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm-ch"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{Id: "dm-1"}, nil)

	msg, err := p.submitBotRequest("u_req", botRequestInput{Username: "monitoring-bot", DisplayName: "Monitoring Bot", Description: "watches things"})

	require.NoError(t, err)
	require.Contains(t, msg, "submitted for approval")
	require.NotNil(t, approvalPost)
	require.Equal(t, "bots-ch", approvalPost.ChannelId)
	api.AssertCalled(t, "KVSet", mock.Anything, mock.Anything)
}

// --- createBotForRequest tests ---

func TestCreateBotForRequest_DefaultsOwnerToRequester(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("CreateBot", mock.MatchedBy(func(b *model.Bot) bool {
		return b.OwnerId == "u_req" && b.Username == "mybot"
	})).Return(&model.Bot{Username: "mybot"}, nil)

	bot, err := p.createBotForRequest(&botRequest{
		RequesterID: "u_req",
		Username:    "mybot",
		DisplayName: "My Bot",
	})

	require.NoError(t, err)
	require.Equal(t, "mybot", bot.Username)
}

func TestCreateBotForRequest_UsesExplicitOwner(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("CreateBot", mock.MatchedBy(func(b *model.Bot) bool {
		return b.OwnerId == "owner-id"
	})).Return(&model.Bot{Username: "svc-bot"}, nil)

	bot, err := p.createBotForRequest(&botRequest{
		RequesterID: "u_req",
		OwnerUserID: "owner-id",
		Username:    "svc-bot",
		DisplayName: "Service Bot",
	})

	require.NoError(t, err)
	require.Equal(t, "svc-bot", bot.Username)
}
