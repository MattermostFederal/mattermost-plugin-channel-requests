package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestOnActivate_EnsuresBotAndRegistersCommand(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	stubPendingMigrated(api)
	defer api.AssertExpectations(t)
	p := &Plugin{}
	p.SetAPI(api)

	api.On("EnsureBotUser", mock.MatchedBy(func(b *model.Bot) bool {
		return b.Username == "channel-request"
	})).Return("bot-id", nil)
	api.On("GetPluginConfig").Return(map[string]any{}) // nothing to migrate
	api.On("RegisterCommand", mock.MatchedBy(func(c *model.Command) bool {
		return c.Trigger == commandTrigger && c.AutoComplete && c.AutocompleteData != nil &&
			c.AutocompleteData.Trigger == commandTrigger
	})).Return(nil)

	require.NoError(t, p.OnActivate())
	require.Equal(t, "bot-id", p.botUserID)
}

func TestOnActivate_BotFailureStopsActivation(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	stubPendingMigrated(api)
	defer api.AssertExpectations(t)
	p := &Plugin{}
	p.SetAPI(api)

	api.On("EnsureBotUser", mock.Anything).Return("", testAppErr("no bot"))

	err := p.OnActivate()

	require.ErrorContains(t, err, "failed to ensure bot user")
	require.Empty(t, p.botUserID)
	api.AssertNotCalled(t, "RegisterCommand", mock.Anything)
}

func TestOnActivate_MigrationFailureStillRegistersCommand(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	stubPendingMigrated(api)
	defer api.AssertExpectations(t)
	p := &Plugin{}
	p.SetAPI(api)

	teamID := model.NewId()
	api.On("EnsureBotUser", mock.Anything).Return("bot-id", nil)
	api.On("GetPluginConfig").Return(map[string]any{"ApprovalTeam": "ops"})
	api.On("GetTeamByName", "ops").Return(&model.Team{Id: teamID}, nil)
	api.On("SavePluginConfig", mock.Anything).Return(testAppErr("save failed"))
	api.On("RegisterCommand", mock.Anything).Return(nil)

	require.NoError(t, p.OnActivate())
	api.AssertCalled(t, "LogError", "channel-requests: failed to migrate settings to IDs", "error", mock.Anything)
}

func TestOnActivate_StoredRequestUpdateFailureIsLogged(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := &Plugin{}
	p.SetAPI(api)

	api.On("EnsureBotUser", mock.Anything).Return("bot-id", nil)
	api.On("GetPluginConfig").Return(map[string]any{})
	api.On("KVGet", kvPendingMigrated).Return(nil, testAppErr("kv down"))
	api.On("RegisterCommand", mock.Anything).Return(nil)

	require.NoError(t, p.OnActivate())
	api.AssertCalled(t, "LogError", "channel-requests: failed to update stored requests", "error", mock.Anything)
}
