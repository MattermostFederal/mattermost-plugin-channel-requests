package main

import (
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// createUserSession creates a short-lived session for REST API calls on behalf
// of userID. Roles must be copied from the user — PluginAPI CreateSession does
// not populate them, and Mattermost permission checks then fail (e.g. webhook
// create with manage_own_incoming_webhooks).
func (p *Plugin) createUserSession(userID string) (*model.Session, error) {
	if userID == "" {
		return nil, errors.New("user id is required for session")
	}
	user, appErr := p.API.GetUser(userID)
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to load user for session")
	}
	session, appErr := p.API.CreateSession(&model.Session{
		UserId: userID,
		Roles:  user.Roles,
	})
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to create session")
	}
	return session, nil
}
