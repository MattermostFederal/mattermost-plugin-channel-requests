package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pkg/errors"
)

// resolveApprovalChannel returns the channel where approval requests for the
// given teamID should be posted, plus an optional header prefix to include
// when the request is routed to the root channel on behalf of a specific team.
//
// Priority order:
//
//  1. Per-team auto-channel (when PerTeamApprovalChannels is enabled AND the
//     team has at least one active Team Admin monitoring it).
//  2. Root approval channel with a "For Team: X — " prefix label, used when
//     PerTeamApprovalChannels is enabled but the team has no active Team Admins
//     — routing to a per-team channel nobody monitors would silently bury requests.
//  3. Root approval channel with no label (PerTeamApprovalChannels is disabled).
//  4. nil — caller falls back to DMing every System Admin.
//
// Failures at each tier are logged and the next tier is tried so a
// misconfiguration at one level doesn't silently drop requests.
func (p *Plugin) resolveApprovalChannel(teamID string) (*model.Channel, string) {
	config := p.getConfiguration()

	if config.PerTeamApprovalChannels && teamID != "" {
		if !p.hasActiveTeamAdmins(teamID) {
			// No active Team Admins — a per-team channel would be unmonitored.
			// Fall back to the root channel and label the card so System Admins
			// know which team the request belongs to (Option C routing).
			teamName := teamID
			if team, appErr := p.API.GetTeam(teamID); appErr == nil {
				teamName = team.DisplayName
			}
			p.API.LogInfo("channel-requests: no active team admins; routing to root channel",
				"team_id", teamID, "team_name", teamName)
			ch := p.ensureRootApprovalChannel()
			return ch, fmt.Sprintf("For Team: %s — ", teamName)
		}

		ch, err := p.ensureTeamApprovalChannel(teamID)
		if err == nil {
			return ch, ""
		}
		p.API.LogWarn("failed to resolve per-team approval channel; falling back to root channel",
			"team_id", teamID, "error", err.Error())
	}

	ch := p.ensureRootApprovalChannel()
	if ch == nil {
		p.API.LogWarn("channel-requests: no approval channel available — falling back to System Admin DMs. " +
			"Configure ApprovalTeam in System Console, or enable PerTeamApprovalChannels.")
	}
	return ch, ""
}

// hasActiveTeamAdmins returns true when teamID has at least one Team Admin
// (via SchemeAdmin flag or team_admin role string). Used to decide whether a
// per-team approval channel is actually monitored before routing requests there.
// Returns true on lookup failure so the caller defaults to trying the per-team
// channel rather than silently bypassing it.
func (p *Plugin) hasActiveTeamAdmins(teamID string) bool {
	const perPage = 200
	for page := range 10 {
		members, appErr := p.API.GetTeamMembers(teamID, page, perPage)
		if appErr != nil {
			return true // safe default: assume monitored
		}
		for _, m := range members {
			if m.SchemeAdmin || slices.Contains(strings.Fields(m.Roles), model.TeamAdminRoleId) {
				return true
			}
		}
		if len(members) < perPage {
			return false
		}
	}
	return false
}

// ensureRootApprovalChannel returns the root approval channel for channel and
// admin promotion requests, creating it if it doesn't already exist. The channel
// is always named "mattermost-channel-requests" in the configured ApprovalTeam.
// Returns nil when ApprovalTeam is blank (plugin not yet configured), so callers
// can fall back to DMing System Admins.
func (p *Plugin) ensureRootApprovalChannel() *model.Channel {
	config := p.getConfiguration()
	teamName := strings.TrimSpace(config.ApprovalTeam)
	if teamName == "" {
		return nil
	}
	channelName := "mattermost-channel-requests"

	// Fast path: channel already exists.
	if ch, appErr := p.API.GetChannelByNameForTeamName(teamName, channelName, false); appErr == nil {
		return ch
	}

	// Channel not found — look up the team so we can create the channel.
	team, appErr := p.API.GetTeamByName(teamName)
	if appErr != nil {
		p.API.LogWarn("root approval team not found; falling back to System Admin DMs",
			"team", teamName, "error", appErr.Error())
		return nil
	}

	ch, appErr := p.API.CreateChannel(&model.Channel{
		TeamId:      team.Id,
		Name:        channelName,
		DisplayName: "Mattermost: Channel Requests",
		Type:        model.ChannelTypeOpen,
		Purpose: fmt.Sprintf(
			"Global channel and governance requests for %s. Reviewed by System and Team Admins.",
			team.DisplayName,
		),
		CreatorId: p.botUserID,
	})
	if appErr != nil {
		// A concurrent call may have beaten us — retry the lookup before surfacing the error.
		if existing, getErr := p.API.GetChannelByNameForTeamName(teamName, channelName, false); getErr == nil {
			return existing
		}
		p.API.LogWarn("failed to create root approval channel; falling back to System Admin DMs",
			"team", teamName, "channel", channelName, "error", appErr.Error())
		return nil
	}

	if _, postErr := p.API.CreatePost(&model.Post{
		UserId:    p.botUserID,
		ChannelId: ch.Id,
		Message: "This channel was created automatically by the Mattermost Tickets plugin. " +
			"It is the global approval channel for channel creation, Channel Admin, Team Admin, and team creation requests. " +
			"Configure team-specific routing in System Console → Plugins → Mattermost Permissions.",
	}); postErr != nil {
		p.API.LogWarn("failed to post welcome message in root approval channel",
			"channel_id", ch.Id, "error", postErr.Error())
	}

	p.addSystemAdminsToChannel(ch.Id)

	return ch
}

// sidebarCategoryName is the display name of the sidebar category the plugin
// creates for every admin who has access to one or more approval channels.
const sidebarCategoryName = "Mattermost: Permissions"

// addChannelToPermissionsCategory ensures the given channel appears at the top
// of the "Mattermost: Permissions" sidebar category for userID in teamID.
// The category is created on first use. Failures are logged but never fatal —
// the channel still works correctly without the sidebar grouping.
func (p *Plugin) addChannelToPermissionsCategory(userID, teamID, channelID string) {
	if userID == "" || teamID == "" || channelID == "" {
		return
	}
	cats, appErr := p.API.GetChannelSidebarCategories(userID, teamID)
	if appErr != nil {
		p.API.LogWarn("sidebar: failed to get categories",
			"user_id", userID, "team_id", teamID, "error", appErr.Error())
		return
	}

	for _, cat := range cats.Categories {
		if cat.DisplayName != sidebarCategoryName {
			continue
		}
		// Already in category — nothing to do.
		for _, id := range cat.Channels {
			if id == channelID {
				return
			}
		}
		// Prepend so the new channel appears at the top.
		cat.Channels = append([]string{channelID}, cat.Channels...)
		if _, updateErr := p.API.UpdateChannelSidebarCategories(userID, teamID,
			[]*model.SidebarCategoryWithChannels{cat}); updateErr != nil {
			p.API.LogWarn("sidebar: failed to update category",
				"user_id", userID, "category_id", cat.Id, "error", updateErr.Error())
		}
		return
	}

	// Category doesn't exist yet — create it.
	if _, createErr := p.API.CreateChannelSidebarCategory(userID, teamID, &model.SidebarCategoryWithChannels{
		SidebarCategory: model.SidebarCategory{
			UserId:      userID,
			TeamId:      teamID,
			DisplayName: sidebarCategoryName,
			Type:        model.SidebarCategoryCustom,
		},
		Channels: []string{channelID},
	}); createErr != nil {
		p.API.LogWarn("sidebar: failed to create category",
			"user_id", userID, "team_id", teamID, "error", createErr.Error())
	}
}

// addSystemAdminsToChannel adds all System Admins to the given channel so they
// can see and act on root-channel approval requests immediately after creation.
// Per-user failures are logged but don't abort — a partial add is better than none.
func (p *Plugin) addSystemAdminsToChannel(channelID string) {
	// Resolve the channel's team so we can place it in the sidebar category.
	channel, chErr := p.API.GetChannel(channelID)
	var teamID string
	if chErr == nil {
		teamID = channel.TeamId
	} else {
		p.API.LogWarn("sidebar: failed to resolve channel team; sidebar categorization skipped",
			"channel_id", channelID, "error", chErr.Error())
	}

	admins, appErr := p.API.GetUsers(&model.UserGetOptions{Role: model.SystemAdminRoleId, Page: 0, PerPage: 100})
	if appErr != nil {
		p.API.LogWarn("failed to list System Admins for root channel seeding", "channel_id", channelID, "error", appErr.Error())
		return
	}
	for _, admin := range admins {
		if admin.Id == p.botUserID {
			continue
		}
		if _, addErr := p.API.AddChannelMember(channelID, admin.Id); addErr != nil {
			p.API.LogWarn("failed to add System Admin to root approval channel",
				"channel_id", channelID, "user_id", admin.Id, "error", addErr.Error())
			continue
		}
		if teamID != "" {
			p.addChannelToPermissionsCategory(admin.Id, teamID, channelID)
		}
	}
}

// ensureNamedApprovalChannel looks up or creates a private approval channel
// with the given name in the globally-configured approval team. It seeds all
// System Admins and posts a one-time orientation message on first creation.
// Returns nil when ApprovalTeam is not configured (plugin not yet set up).
func (p *Plugin) ensureNamedApprovalChannel(name, displayName, purpose string) *model.Channel {
	config := p.getConfiguration()
	teamName := strings.TrimSpace(config.ApprovalTeam)
	if teamName == "" {
		return nil
	}

	team, appErr := p.API.GetTeamByName(teamName)
	if appErr != nil {
		p.API.LogWarn("cannot ensure named approval channel — team not found",
			"team", teamName, "channel", name, "error", appErr.Error())
		return nil
	}

	if ch, appErr := p.API.GetChannelByName(team.Id, name, false); appErr == nil {
		return ch
	}

	ch, appErr := p.API.CreateChannel(&model.Channel{
		TeamId:      team.Id,
		Name:        name,
		DisplayName: displayName,
		Purpose:     purpose,
		Type:        model.ChannelTypePrivate,
		CreatorId:   p.botUserID,
	})
	if appErr != nil {
		// Concurrent activation may have beaten us — retry lookup before failing.
		if existing, getErr := p.API.GetChannelByName(team.Id, name, false); getErr == nil {
			return existing
		}
		p.API.LogWarn("failed to create dedicated approval channel",
			"name", name, "error", appErr.Error())
		return nil
	}

	_, _ = p.API.CreatePost(&model.Post{
		UserId:    p.botUserID,
		ChannelId: ch.Id,
		Message: fmt.Sprintf(
			"This channel was created automatically by the Mattermost Tickets plugin. "+
				"**%s** requests are posted here for System Admin review. "+
				"Reply to any request thread to ask the requester for more information.",
			displayName,
		),
	})

	// Seed all System Admins immediately so they can see and act on requests
	// without waiting for the next activation cycle.
	p.addSystemAdminsToChannel(ch.Id)

	return ch
}

// ensureBotApprovalChannel returns the dedicated channel for bot account
// creation requests. Created automatically in the root approval team.
func (p *Plugin) ensureBotApprovalChannel() *model.Channel {
	return p.ensureNamedApprovalChannel(
		"mattermost-bot-requests",
		"Mattermost: Bot Requests",
		"Bot account creation requests — System Admin review required.",
	)
}

// ensureWebhookApprovalChannel returns the dedicated channel for incoming and
// outgoing webhook requests. Created automatically in the root approval team.
func (p *Plugin) ensureWebhookApprovalChannel() *model.Channel {
	return p.ensureNamedApprovalChannel(
		"mattermost-webhook-requests",
		"Mattermost: Webhook Requests",
		"Incoming and outgoing webhook requests — System Admin review required.",
	)
}

// ensureTeamCreationApprovalChannel returns the dedicated channel for team
// creation requests. Created automatically in the root approval team.
func (p *Plugin) ensureTeamCreationApprovalChannel() *model.Channel {
	return p.ensureNamedApprovalChannel(
		"mattermost-team-requests",
		"Mattermost: Team Requests",
		"Team creation requests — System Admin review required.",
	)
}

// ensureAuditChannel returns the dedicated channel for audit log entries.
// Created automatically in the root approval team. All approve/deny actions
// post a line here so admins have a chronological record outside of request threads.
func (p *Plugin) ensureAuditChannel() *model.Channel {
	return p.ensureNamedApprovalChannel(
		"mattermost-audit-requests",
		"Mattermost: Audit Requests",
		"Audit log for all approval actions — channel creation, bot, webhook, team, and admin requests.",
	)
}

// ensureTeamApprovalChannel returns the per-team approval channel, creating it
// (and adding all current Team Admins as members) if it doesn't already exist.
// Safe to call concurrently — if two calls race to create the same channel, the
// loser retries the lookup and returns the already-created channel.
func (p *Plugin) ensureTeamApprovalChannel(teamID string) (*model.Channel, error) {
	channelName := p.getConfiguration().TeamChannelName()

	// Fast path: channel already exists.
	if ch, appErr := p.API.GetChannelByName(teamID, channelName, false); appErr == nil {
		return ch, nil
	}

	team, appErr := p.API.GetTeam(teamID)
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to load team")
	}

	// Ensure the bot is a team member before creating the channel. The plugin
	// API bypasses most permission checks, but the bot must be in the team to
	// reliably post messages in team channels later. CreateTeamMember is
	// idempotent — MM returns the existing membership if the bot is already in
	// the team, so this is safe to call unconditionally.
	if _, addErr := p.API.CreateTeamMember(teamID, p.botUserID); addErr != nil {
		p.API.LogWarn("failed to add bot to team before creating approval channel",
			"team_id", teamID, "bot_user_id", p.botUserID, "error", addErr.Error())
	}

	ch, appErr := p.API.CreateChannel(&model.Channel{
		TeamId:      teamID,
		Name:        channelName,
		DisplayName: "Mattermost: Channel Requests",
		Type:        model.ChannelTypeOpen,
		Purpose: fmt.Sprintf(
			"Channel and admin promotion requests for %s. Team Admins review and approve requests here.",
			team.DisplayName,
		),
		CreatorId: p.botUserID,
	})
	if appErr != nil {
		// A concurrent call may have beaten us to creation — retry the lookup
		// before surfacing the error.
		if existing, getErr := p.API.GetChannelByName(teamID, channelName, false); getErr == nil {
			return existing, nil
		}
		return nil, errors.Wrap(appErr, "failed to create team approval channel")
	}

	p.addTeamAdminsToChannel(teamID, ch.Id)
	p.postChannelWelcomeMessage(ch, team)

	return ch, nil
}

// addTeamAdminsToChannel adds every current Team Admin of teamID to channelID.
// Called once when the channel is first created. Per-member failures are logged
// but don't abort the rest — a partial add is better than no add.
func (p *Plugin) addTeamAdminsToChannel(teamID, channelID string) {
	const perPage = 200
	for page := range 50 { // guard against runaway pagination on huge deployments
		members, appErr := p.API.GetTeamMembers(teamID, page, perPage)
		if appErr != nil {
			p.API.LogWarn("failed to list team members when seeding approval channel",
				"team_id", teamID, "channel_id", channelID, "error", appErr.Error())
			return
		}
		for _, m := range members {
			isAdmin := m.SchemeAdmin || slices.Contains(strings.Fields(m.Roles), model.TeamAdminRoleId)
			if !isAdmin {
				continue
			}
			if _, appErr := p.API.AddChannelMember(channelID, m.UserId); appErr != nil {
				p.API.LogWarn("failed to add team admin to approval channel",
					"channel_id", channelID, "user_id", m.UserId, "error", appErr.Error())
				continue
			}
			p.addChannelToPermissionsCategory(m.UserId, teamID, channelID)
		}
		if len(members) < perPage {
			return
		}
	}
}

// postChannelWelcomeMessage posts an orientation message in the newly-created
// per-team approval channel so Team Admins know what it's for.
func (p *Plugin) postChannelWelcomeMessage(ch *model.Channel, team *model.Team) {
	msg := fmt.Sprintf(
		"This channel was created automatically by the Mattermost Tickets plugin. "+
			"Channel creation and Channel Admin promotion requests for the **%s** team are posted here for review.\n\n"+
			"Team Admins will receive new-request notifications and can use the **Approve** / **Deny** buttons. "+
			"Reply to any request thread to ask the requester for more information.",
		team.DisplayName,
	)
	if _, appErr := p.API.CreatePost(&model.Post{
		UserId:    p.botUserID,
		ChannelId: ch.Id,
		Message:   msg,
	}); appErr != nil {
		p.API.LogWarn("failed to post welcome message in team approval channel",
			"channel_id", ch.Id, "error", appErr.Error())
	}
}

// ensureAllTeamApprovalChannels creates per-team approval channels for every
// team that doesn't have one yet. Called in a background goroutine from
// OnActivate when PerTeamApprovalChannels is enabled. Best-effort: failures
// for individual teams are logged but don't abort the sweep.
func (p *Plugin) ensureAllTeamApprovalChannels() {
	teams, appErr := p.API.GetTeams()
	if appErr != nil {
		p.API.LogWarn("failed to list teams for approval-channel sweep", "error", appErr.Error())
		return
	}
	for _, team := range teams {
		if _, err := p.ensureTeamApprovalChannel(team.Id); err != nil {
			p.API.LogWarn("failed to ensure approval channel for team",
				"team_id", team.Id, "team_name", team.Name, "error", err.Error())
		}
	}
	p.API.LogInfo("per-team approval channel sweep complete", "teams", len(teams))
}

// UserHasJoinedTeam ensures that a Team Admin who joins (or is added to) a team
// is automatically added to the team's approval channel. This handles new team
// creation (the creator joins as team admin) and explicit admin additions. It
// does NOT handle promotion of an existing member to team admin — use the
// /governance sync-team-channels command to correct drift from role changes.
func (p *Plugin) UserHasJoinedTeam(_ *plugin.Context, teamMember *model.TeamMember, _ *model.User) {
	if !p.getConfiguration().PerTeamApprovalChannels {
		return
	}

	isAdmin := teamMember.SchemeAdmin ||
		slices.Contains(strings.Fields(teamMember.Roles), model.TeamAdminRoleId)
	if !isAdmin {
		return
	}

	ch, err := p.ensureTeamApprovalChannel(teamMember.TeamId)
	if err != nil {
		p.API.LogWarn("failed to ensure team approval channel on team join",
			"team_id", teamMember.TeamId, "user_id", teamMember.UserId, "error", err.Error())
		return
	}

	if _, appErr := p.API.AddChannelMember(ch.Id, teamMember.UserId); appErr != nil {
		p.API.LogWarn("failed to add joining team admin to approval channel",
			"channel_id", ch.Id, "user_id", teamMember.UserId, "error", appErr.Error())
		return
	}
	p.addChannelToPermissionsCategory(teamMember.UserId, teamMember.TeamId, ch.Id)
}
