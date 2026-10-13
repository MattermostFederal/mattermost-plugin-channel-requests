package main

import (
	"sync"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pkg/errors"
)

type Plugin struct {
	plugin.MattermostPlugin

	configurationLock sync.RWMutex
	configuration     *configuration

	// botUserID is the user ID of the bot account used to post approval requests and notify
	// requesters of the outcome.
	botUserID string

	// Test seams for the incoming-webhook side effects. These go through the
	// REST API (Client4), which the plugin-API mock can't exercise, so the
	// delivery-failure cleanup path can't otherwise be unit-tested. Both are
	// nil in production, where createIncomingWebhookForRequest and
	// deleteIncomingWebhook run their real Client4 implementations.
	createWebhookFn func(req *webhookRequest) (url, hookID string, err error)
	deleteWebhookFn func(hookID string) error
}

func (p *Plugin) OnActivate() error {
	// Username is kept stable ("channel-request") so existing @-mentions and
	// DMs keep resolving; only the display name/description broaden to reflect
	// that the bot now handles more than channel requests.
	botUserID, err := p.API.EnsureBotUser(&model.Bot{
		Username:    "channel-request",
		DisplayName: "Requests",
		Description: "Handles user requests and approvals.",
	})
	if err != nil {
		return errors.Wrap(err, "failed to ensure bot user")
	}
	p.botUserID = botUserID

	if err := p.API.RegisterCommand(getCommand()); err != nil {
		return errors.Wrap(err, "failed to register /request command")
	}
	if err := p.API.RegisterCommand(getLegacyCommand()); err != nil {
		return errors.Wrap(err, "failed to register /channel-request command")
	}
	return nil
}
