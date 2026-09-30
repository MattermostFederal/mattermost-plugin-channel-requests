package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestExecuteCommand_NotConfiguredDoesNotOpenDialog(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	// Channel requests enabled, but no prefixes configured — the
	// not-configured message must win over opening a doomed dialog.
	p.setConfiguration(&configuration{AllowChannelRequests: true})

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: "/request channel", TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	require.Contains(t, resp.Text, "aren't configured")
	// A doomed dialog must not be opened when no prefix is configured.
	api.AssertNotCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_OpensDialogWhenConfigured(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowChannelRequests: true, prefixes: []channelPrefix{{Prefix: "team-"}}})

	api.On("OpenInteractiveDialog", mock.Anything).Return(nil)

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: "/request channel", TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	api.AssertCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_DisabledDoesNotOpenDialog(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	// Prefixes are configured, but channel requests are toggled off.
	p.setConfiguration(&configuration{AllowChannelRequests: false, prefixes: []channelPrefix{{Prefix: "team-"}}})

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: "/request channel", TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	require.Contains(t, resp.Text, "disabled")
	// The feature gate short-circuits before any dialog is opened.
	api.AssertNotCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_LegacyAliasOpensChannelDialog(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowChannelRequests: true, prefixes: []channelPrefix{{Prefix: "team-"}}})

	api.On("OpenInteractiveDialog", mock.Anything).Return(nil)

	// The deprecated `/channel-request` trigger must behave like `/request channel`.
	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: "/channel-request", TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	api.AssertCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_NoSubcommandShowsUsage(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowChannelRequests: true, prefixes: []channelPrefix{{Prefix: "team-"}}})

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: "/request", TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	require.Contains(t, resp.Text, "Usage")
	// Usage help must never open a dialog.
	api.AssertNotCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_TeamDisabledReportsDisabled(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	// Team requests are off by default.
	p.setConfiguration(&configuration{AllowChannelRequests: true})

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: "/request team", TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	require.Contains(t, resp.Text, "disabled")
	api.AssertNotCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_TeamEnabledOpensDialog(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowTeamRequests: true})

	api.On("OpenInteractiveDialog", mock.Anything).Return(nil)

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: "/request team", TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	api.AssertCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_TeamAdminDisabledReportsDisabled(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowChannelRequests: true})

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: "/request team-admin", TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	require.Contains(t, resp.Text, "disabled")
	api.AssertNotCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_TeamAdminEnabledOpensDialog(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowTeamAdminRequests: true})

	api.On("OpenInteractiveDialog", mock.Anything).Return(nil)

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: "/request team-admin", TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	api.AssertCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_TeamAdminNoTeamContext(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowTeamAdminRequests: true})

	// No team context (e.g. run from a DM) — can't scope the request.
	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: "/request team-admin", TriggerId: "trig", TeamId: ""})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	require.Contains(t, resp.Text, "within the team")
	api.AssertNotCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_BotTokenDisabledReportsDisabled(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowChannelRequests: true})

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: "/request bot-token", TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	require.Contains(t, resp.Text, "disabled")
	api.AssertNotCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_BotTokenEnabledOpensDialog(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowBotTokenRequests: true})

	api.On("OpenInteractiveDialog", mock.Anything).Return(nil)

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: "/request bot-token", TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	api.AssertCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_WebhookDisabledReportsDisabled(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowChannelRequests: true})

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: "/request webhook", TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	require.Contains(t, resp.Text, "disabled")
	api.AssertNotCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_WebhookEnabledOpensDialog(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowWebhookRequests: true})

	api.On("OpenInteractiveDialog", mock.Anything).Return(nil)

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: "/request webhook", TriggerId: "trig", TeamId: "team1", ChannelId: "c1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	api.AssertCalled(t, "OpenInteractiveDialog", mock.Anything)
}
