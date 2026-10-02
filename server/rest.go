package main

import (
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// kvBotAccessTokenKey stores the plugin bot's personal access token, created
// lazily and reused. It's needed because some actions (creating incoming
// webhooks) have no plugin API and must go through the REST API as an
// authenticated user.
const kvBotAccessTokenKey = "bot_access_token"

// siteURL returns the configured server Site URL, or "" if unset.
func (p *Plugin) siteURL() string {
	cfg := p.API.GetConfig()
	if cfg != nil && cfg.ServiceSettings.SiteURL != nil {
		return *cfg.ServiceSettings.SiteURL
	}
	return ""
}

// ensureBotAccessToken returns the plugin bot's access token, creating and
// caching it on first use.
func (p *Plugin) ensureBotAccessToken() (string, error) {
	if data, appErr := p.API.KVGet(kvBotAccessTokenKey); appErr == nil && len(data) > 0 {
		return string(data), nil
	}
	tok, appErr := p.API.CreateUserAccessToken(&model.UserAccessToken{
		UserId:      p.botUserID,
		Description: "channel-requests: create incoming webhooks",
	})
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to create plugin bot access token")
	}
	if appErr := p.API.KVSet(kvBotAccessTokenKey, []byte(tok.Token)); appErr != nil {
		p.API.LogWarn("failed to cache bot access token; will recreate next time", "error", appErr.Error())
	}
	return tok.Token, nil
}

// restClient returns a Client4 authenticated as the plugin bot, for the few
// operations with no plugin API. Callers should surface its error to the user,
// since it depends on Site URL being set and access tokens being enabled.
func (p *Plugin) restClient() (*model.Client4, error) {
	site := p.siteURL()
	if site == "" {
		return nil, errors.New("Site URL is not configured (System Console → Environment → Web Server)")
	}
	token, err := p.ensureBotAccessToken()
	if err != nil {
		return nil, err
	}
	client := model.NewAPIv4Client(site)
	client.SetToken(token)
	return client, nil
}
