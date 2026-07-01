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
}

func (p *Plugin) OnActivate() error {
	botUserID, err := p.API.EnsureBotUser(&model.Bot{
		Username:    "channel-request",
		DisplayName: "Channel Request",
		Description: "Handles channel-creation requests and approvals.",
	})
	if err != nil {
		return errors.Wrap(err, "failed to ensure bot user")
	}
	p.botUserID = botUserID

	return p.API.RegisterCommand(getCommand())
}
