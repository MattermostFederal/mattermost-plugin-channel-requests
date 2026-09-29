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
	p.setConfiguration(&configuration{AllowChannelRequests: true, prefixes: []channelPrefix{{Prefix: "team-"}}})

	api.On("OpenInteractiveDialog", mock.Anything).Return(nil)

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	api.AssertCalled(t, "OpenInteractiveDialog", mock.Anything)
}

func TestExecuteCommand_DisabledDoesNotOpenDialog(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)
	// Prefixes are configured, but channel requests are toggled off.
	p.setConfiguration(&configuration{AllowChannelRequests: false, prefixes: []channelPrefix{{Prefix: "team-"}}})

	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{TriggerId: "trig", TeamId: "team1"})

	require.Nil(t, appErr)
	require.NotNil(t, resp)
	require.Contains(t, resp.Text, "disabled")
	// The feature gate short-circuits before any dialog is opened.
	api.AssertNotCalled(t, "OpenInteractiveDialog", mock.Anything)
}
