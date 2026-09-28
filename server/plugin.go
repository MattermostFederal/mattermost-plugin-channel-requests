package main

import (
	"os"
	"path/filepath"
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
		Username:    "mattermost-requests",
		DisplayName: "Mattermost Requests",
		Description: "Handles channel, team, bot, and webhook requests; relays messages between requesters and reviewers.",
	})
	if err != nil {
		return errors.Wrap(err, "failed to ensure bot user")
	}
	p.botUserID = botUserID

	// Set the Mattermost icon as the bot's profile picture. Non-fatal if the
	// asset isn't present — the bot still works with its default avatar.
	if iconData, readErr := p.readBundleAsset("mattermost-icon.png"); readErr == nil {
		if setErr := p.API.SetProfileImage(p.botUserID, iconData); setErr != nil {
			p.API.LogWarn("failed to set bot profile image", "error", setErr.Error())
		}
	} else {
		p.API.LogWarn("bot profile image asset not found; place a PNG at assets/mattermost-icon.png to activate it")
	}

	if err := p.API.RegisterCommand(getCommand()); err != nil {
		return errors.Wrap(err, "failed to register slash command")
	}

	// Ensure approval channels exist in the background so activation doesn't
	// block on channel-creation API calls. ensureRootApprovalChannel is a no-op
	// when ApprovalTeam/ApprovalChannel are blank (first-time unconfigured).
	go func() {
		// Re-sync System Admins into every approval channel on activation so
		// admins promoted after initial install are added without manual
		// intervention. addSystemAdminsToChannel is idempotent.
		for _, ensure := range []func() *model.Channel{
			p.ensureRootApprovalChannel,
			p.ensureBotApprovalChannel,
			p.ensureWebhookApprovalChannel,
			p.ensureTeamCreationApprovalChannel,
			p.ensureAuditChannel,
		} {
			if ch := ensure(); ch != nil {
				p.addSystemAdminsToChannel(ch.Id)
			}
		}
		if p.getConfiguration().PerTeamApprovalChannels {
			p.ensureAllTeamApprovalChannels()
		}
	}()

	// Delete any token DM posts whose 72-hour window lapsed while the plugin
	// was not running, and re-arm in-process timers for the remainder.
	go p.cleanupExpiredTokenPosts()

	return nil
}

// readBundleAsset reads a file from the plugin's bundled assets directory.
func (p *Plugin) readBundleAsset(name string) ([]byte, error) {
	bundlePath, appErr := p.API.GetBundlePath()
	if appErr != nil {
		return nil, appErr
	}
	return os.ReadFile(filepath.Join(bundlePath, "assets", name))
}
