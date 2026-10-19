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
//     team has at least one Team Admin or System Admin as a member).
//  2. Root approval channel with a "For Team: X — " prefix label, used when
//     PerTeamApprovalChannels is enabled but the team has no local approvers
//     — routing to a per-team channel nobody monitors would silently bury requests.
//  3. Root approval channel with no label (PerTeamApprovalChannels is disabled).
//  4. nil — caller falls back to DMing every System Admin.
//
// Failures at each tier are logged and the next tier is tried so a
// misconfiguration at one level doesn't silently drop requests.
func (p *Plugin) resolveApprovalChannel(teamID string) (*model.Channel, string) {
	config := p.getConfiguration()

	if config.PerTeamApprovalChannels && teamID != "" {
		if !p.hasActiveTeamApprovers(teamID) {
			// No Team Admin and no System Admin on this team — fall back to root
			// and label the card so System Admins know which team it belongs to.
			teamName := teamID
			if team, appErr := p.API.GetTeam(teamID); appErr == nil {
				teamName = team.DisplayName
			}
			p.API.LogInfo("channel-requests: no team/system admins on team; routing to root channel",
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

// hasActiveTeamApprovers returns true when teamID has at least one member who
// can monitor a per-team approval channel: a Team Admin (SchemeAdmin /
// team_admin) or a System Admin who belongs to the team.
//
// Returns true on team-member lookup failure so the caller defaults to trying
// the per-team channel rather than silently bypassing it.
func (p *Plugin) hasActiveTeamApprovers(teamID string) bool {
	sysAdmins := p.systemAdminIDSet()

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
			if sysAdmins[m.UserId] {
				return true
			}
		}
		if len(members) < perPage {
			return false
		}
	}
	return false
}

// systemAdminIDSet returns the set of System Admin user IDs (best-effort).
func (p *Plugin) systemAdminIDSet() map[string]bool {
	out := map[string]bool{}
	admins, appErr := p.API.GetUsers(&model.UserGetOptions{Role: model.SystemAdminRoleId, Page: 0, PerPage: 200})
	if appErr != nil {
		p.API.LogWarn("failed to list System Admins for approver check", "error", appErr.Error())
		return out
	}
	for _, admin := range admins {
		if admin != nil && admin.Id != "" && admin.Id != p.botUserID {
			out[admin.Id] = true
		}
	}
	return out
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
		Type:        model.ChannelTypePrivate,
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
		Message: "This channel was created automatically by the Mattermost Request Hub plugin. " +
			"It is the global approval channel for channel creation and Channel Admin requests. " +
			"Team Admin and team creation requests go to mattermost-team-requests. " +
			"Configure team-specific routing in System Console → Plugins → Mattermost Request Hub.",
	}); postErr != nil {
		p.API.LogWarn("failed to post welcome message in root approval channel",
			"channel_id", ch.Id, "error", postErr.Error())
	}

	p.addSystemAdminsToChannel(ch.Id)

	return ch
}

// Managed sidebar category for every plugin ticket / approval channel.
// Prior labels are renamed in-place so upgrades don't leave duplicate empty categories.
const (
	sidebarCategoryName       = "Mattermost: Request Hub"
	kvSidebarCategoryMigrated = "migration_sidebar_category_v3"
)

var sidebarCategoryNameLegacy = []string{
	"Mattermost: Permissions",
	"Mattermost Requests",
}

func isManagedSidebarCategory(name string) bool {
	return name == sidebarCategoryName || slices.Contains(sidebarCategoryNameLegacy, name)
}

// addChannelToPermissionsCategory ensures the given channel appears at the top
// of the "Mattermost: Request Hub" sidebar category for userID in teamID.
// Renames a legacy category label if found. The category is created on first
// use. Failures are logged but never fatal.
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
		if !isManagedSidebarCategory(cat.DisplayName) {
			continue
		}
		changed := false
		if cat.DisplayName != sidebarCategoryName {
			cat.DisplayName = sidebarCategoryName
			changed = true
		}
		already := slices.Contains(cat.Channels, channelID)
		if !already {
			cat.Channels = append([]string{channelID}, cat.Channels...)
			changed = true
		}
		if !changed {
			return
		}
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

	if existing, lookupErr := p.API.GetChannelByName(team.Id, name, false); lookupErr == nil {
		return existing
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
			"This channel was created automatically by the Mattermost Request Hub plugin. "+
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
// creation requests. Created automatically in the root approval team. Always
// forces the channel into the managed "Mattermost: Request Hub" sidebar
// category for System Admins (same treatment as other ticket channels),
// including when the channel already existed from an older install.
func (p *Plugin) ensureTeamCreationApprovalChannel() *model.Channel {
	ch := p.ensureNamedApprovalChannel(
		"mattermost-team-requests",
		"Mattermost: Team Requests",
		"Team creation requests — System Admin review required.",
	)
	if ch != nil {
		p.forceTicketChannelCategory(ch)
	}
	return ch
}

// forceTicketChannelCategory places ch at the top of each System Admin's
// "Mattermost: Request Hub" sidebar category for ch's team. Does not add
// membership — callers that need seeding should use addSystemAdminsToChannel.
func (p *Plugin) forceTicketChannelCategory(ch *model.Channel) {
	if ch == nil || ch.TeamId == "" {
		return
	}
	for adminID := range p.systemAdminIDSet() {
		p.addChannelToPermissionsCategory(adminID, ch.TeamId, ch.Id)
	}
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
		Type:        model.ChannelTypePrivate,
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

	p.addTeamApproversToChannel(teamID, ch.Id)
	p.postChannelWelcomeMessage(ch, team)

	return ch, nil
}

// addTeamApproversToChannel adds every Team Admin of teamID, plus every System
// Admin who is a member of that team, to channelID. Places each in the managed
// "Mattermost: Request Hub" sidebar category. Called when the per-team channel
// is first created (and from the one-time sidebar migration). Per-member
// failures are logged but don't abort the rest.
func (p *Plugin) addTeamApproversToChannel(teamID, channelID string) {
	sysAdmins := p.systemAdminIDSet()
	const perPage = 200
	for page := range 50 {
		members, appErr := p.API.GetTeamMembers(teamID, page, perPage)
		if appErr != nil {
			p.API.LogWarn("failed to list team members when seeding approval channel",
				"team_id", teamID, "channel_id", channelID, "error", appErr.Error())
			return
		}
		for _, m := range members {
			isTeamAdmin := m.SchemeAdmin || slices.Contains(strings.Fields(m.Roles), model.TeamAdminRoleId)
			if !isTeamAdmin && !sysAdmins[m.UserId] {
				continue
			}
			if m.UserId == p.botUserID {
				continue
			}
			if _, appErr := p.API.AddChannelMember(channelID, m.UserId); appErr != nil {
				p.API.LogWarn("failed to add approver to team approval channel",
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
		"This channel was created automatically by the Mattermost Request Hub plugin. "+
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
// team that has at least one Team Admin or System Admin member. Teams with no
// local approvers are skipped (their channel requests fall back to the root
// approval channel). Called from OnActivate / config enable when
// PerTeamApprovalChannels is on. Best-effort: failures for individual teams
// are logged but don't abort the sweep.
func (p *Plugin) ensureAllTeamApprovalChannels() {
	teams, appErr := p.API.GetTeams()
	if appErr != nil {
		p.API.LogWarn("failed to list teams for approval-channel sweep", "error", appErr.Error())
		return
	}
	created := 0
	skipped := 0
	for _, team := range teams {
		if !p.hasActiveTeamApprovers(team.Id) {
			skipped++
			continue
		}
		if _, err := p.ensureTeamApprovalChannel(team.Id); err != nil {
			p.API.LogWarn("failed to ensure approval channel for team",
				"team_id", team.Id, "team_name", team.Name, "error", err.Error())
			continue
		}
		created++
	}
	p.API.LogInfo("per-team approval channel sweep complete",
		"teams", len(teams), "ensured", created, "skipped_no_approvers", skipped)
}

// migrateTicketChannelsToManagedCategory is a one-shot sweep: for every plugin
// ticket/approval channel (including mattermost-team-requests), ensure each
// member has that channel in the managed "Mattermost: Request Hub" sidebar
// category (renaming a short-lived "Mattermost Requests" category if present).
// Idempotent via KV flag (v2 re-runs after the temporary rename).
func (p *Plugin) migrateTicketChannelsToManagedCategory() {
	if migrated, _ := p.API.KVGet(kvSidebarCategoryMigrated); migrated != nil {
		return
	}

	channelName := p.getConfiguration().TeamChannelName()
	var channels []*model.Channel

	for _, ensure := range []func() *model.Channel{
		p.ensureRootApprovalChannel,
		p.ensureBotApprovalChannel,
		p.ensureWebhookApprovalChannel,
		p.ensureTeamCreationApprovalChannel,
		p.ensureAuditChannel,
	} {
		if ch := ensure(); ch != nil {
			channels = append(channels, ch)
		}
	}

	if teams, appErr := p.API.GetTeams(); appErr == nil {
		for _, team := range teams {
			if ch, getErr := p.API.GetChannelByName(team.Id, channelName, false); getErr == nil && ch != nil {
				channels = append(channels, ch)
			}
		}
	} else {
		p.API.LogWarn("sidebar migration: failed to list teams", "error", appErr.Error())
	}

	seen := map[string]bool{}
	placed := 0
	for _, ch := range channels {
		if ch == nil || ch.Id == "" || seen[ch.Id] {
			continue
		}
		seen[ch.Id] = true
		placed += p.placeChannelMembersInManagedCategory(ch)
	}

	_ = p.API.KVSet(kvSidebarCategoryMigrated, []byte("1"))
	p.API.LogInfo("sidebar migration: ticket channels forced into Mattermost: Request Hub category",
		"channels", len(seen), "member_placements", placed)
}

// placeChannelMembersInManagedCategory walks channel members and ensures each
// is in the managed sidebar category for that channel's team. Returns how many
// members were processed (best-effort).
func (p *Plugin) placeChannelMembersInManagedCategory(ch *model.Channel) int {
	if ch == nil || ch.TeamId == "" {
		return 0
	}
	count := 0
	const perPage = 200
	for page := range 50 {
		members, appErr := p.API.GetUsersInChannel(ch.Id, "username", page, perPage)
		if appErr != nil {
			p.API.LogWarn("sidebar migration: failed to list channel members",
				"channel_id", ch.Id, "error", appErr.Error())
			return count
		}
		for _, u := range members {
			if u.Id == p.botUserID {
				continue
			}
			p.addChannelToPermissionsCategory(u.Id, ch.TeamId, ch.Id)
			count++
		}
		if len(members) < perPage {
			return count
		}
	}
	return count
}

// UserHasJoinedTeam ensures that a Team Admin or System Admin who joins (or is
// added to) a team is automatically added to the team's approval channel. This
// handles new team creation and explicit admin additions. It does NOT handle
// promotion of an existing member to team admin.
func (p *Plugin) UserHasJoinedTeam(_ *plugin.Context, teamMember *model.TeamMember, actor *model.User) {
	if !p.getConfiguration().PerTeamApprovalChannels {
		return
	}

	isTeamAdmin := teamMember.SchemeAdmin ||
		slices.Contains(strings.Fields(teamMember.Roles), model.TeamAdminRoleId)
	isSysAdmin := false
	if actor != nil && actor.Id == teamMember.UserId {
		isSysAdmin = actor.IsSystemAdmin()
	} else if u, appErr := p.API.GetUser(teamMember.UserId); appErr == nil {
		isSysAdmin = u.IsSystemAdmin()
	}
	if !isTeamAdmin && !isSysAdmin {
		return
	}

	ch, err := p.ensureTeamApprovalChannel(teamMember.TeamId)
	if err != nil {
		p.API.LogWarn("failed to ensure team approval channel on team join",
			"team_id", teamMember.TeamId, "user_id", teamMember.UserId, "error", err.Error())
		return
	}

	if _, appErr := p.API.AddChannelMember(ch.Id, teamMember.UserId); appErr != nil {
		p.API.LogWarn("failed to add joining approver to approval channel",
			"channel_id", ch.Id, "user_id", teamMember.UserId, "error", appErr.Error())
		return
	}
	p.addChannelToPermissionsCategory(teamMember.UserId, teamMember.TeamId, ch.Id)
}
