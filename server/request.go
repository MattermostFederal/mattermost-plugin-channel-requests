package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	// kvRequestPrefix namespaces pending requests in the KV store.
	kvRequestPrefix = "request_"

	// dialogCallbackID identifies submissions from the channel request dialog.
	dialogCallbackID = "channel_request"

	// Dialog element / action context field names.
	fieldDisplayName = "display_name"
	fieldName        = "name"
	fieldPurpose     = "purpose"
	fieldType        = "type"
	fieldMembers     = "members"
	fieldAdmins      = "admins"

	// actionContextRequestID carries the pending request ID on the approve/deny buttons.
	actionContextRequestID = "request_id"

	// Channel type values as plain strings, for use in dialog options, comparisons, and storage.
	channelTypeOpen    = string(model.ChannelTypeOpen)
	channelTypePrivate = string(model.ChannelTypePrivate)
)

// channelRequest is a pending request to create a channel, persisted in the KV store until a System
// Admin approves or denies it.
type channelRequest struct {
	ID          string   `json:"id"`
	RequesterID string   `json:"requester_id"`
	TeamID      string   `json:"team_id"`
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Purpose     string   `json:"purpose"`
	ChannelType string   `json:"channel_type"`
	MemberIDs   []string `json:"member_ids"`
	// AdminMemberIDs are the requester-designated Channel Admins. On
	// approval, these users get channel_admin scheme roles in the newly
	// created channel (in addition to being members).
	AdminMemberIDs []string `json:"admin_member_ids"`
}

// requestInput is the normalized set of values gathered from either entry point (slash command
// dialog or webapp modal) before a request is created.
type requestInput struct {
	RequesterID string
	TeamID      string
	DisplayName string
	// Name is the free-form channel URL portion. When the prefix-list
	// feature is active, this holds ONLY THE SUFFIX (the part after the
	// prefix); server code prepends the selected prefix. When empty
	// prefix list, this behaves as before — the full channel name.
	Name string
	// Prefix is the selected domain prefix (e.g., "team-", "project-").
	// Non-empty only when the prefix-list feature is active AND the
	// requester picked one. Ignored when the plugin is in legacy mode.
	Prefix         string
	Purpose        string
	ChannelType    string
	MemberIDs      []string
	AdminMemberIDs []string
}

var invalidNameChars = regexp.MustCompile(`[^a-z0-9]+`)

// slugify converts a free-form string into a valid channel URL name.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = invalidNameChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > model.ChannelNameMaxLength {
		s = s[:model.ChannelNameMaxLength]
	}
	return s
}

// resolveChannelName derives the final channel URL name from the
// request using the admin's configured prefix list. Requires at least
// one prefix to be defined; returns an error otherwise so the admin
// notices the misconfiguration instead of silently permitting free-form
// channel names.
func (p *Plugin) resolveChannelName(config *configuration, in requestInput) (string, error) {
	if !config.UsesPrefixList() {
		return "", errors.New("plugin is not configured: an admin must define at least one channel prefix in System Console -> Plugins -> Channel Requests")
	}
	return resolvePrefixedName(config.Prefixes(), in)
}

// resolvePrefixedName handles the prefix-list flow. The suffix is
// slugified BEFORE joining so users can type "My Team" and get
// "team-my-team" — same forgiving normalization as the legacy path.
func resolvePrefixedName(prefixes []channelPrefix, in requestInput) (string, error) {
	selected := strings.TrimSpace(in.Prefix)
	if selected == "" {
		return "", errors.New("please pick a channel prefix (e.g., team-, project-, ops-) from the dropdown")
	}
	var entry *channelPrefix
	for i := range prefixes {
		if prefixes[i].Prefix == selected {
			entry = &prefixes[i]
			break
		}
	}
	if entry == nil {
		return "", errors.Errorf("prefix %q is not in the list of allowed prefixes", selected)
	}

	suffix := strings.TrimSpace(in.Name)
	if suffix == "" {
		// Fall back to slugifying the display name so users who leave
		// the URL field blank still get a sensible suggestion. This
		// matches the legacy path's forgiveness.
		suffix = in.DisplayName
	}
	suffix = slugify(suffix)
	// Requesters sometimes double-type the prefix; strip it so
	// "team-marketing" under prefix "team-" doesn't become "team-team-marketing".
	suffix = strings.TrimPrefix(suffix, strings.TrimSuffix(entry.Prefix, "-")+"-")
	suffix = strings.Trim(suffix, "-")

	if suffix == "" {
		return "", errors.New("channel name suffix is required (letters/numbers, becomes the part after the prefix)")
	}

	// Match the whole suffix, not any substring. Admins wrote patterns
	// like [a-z0-9-]{2,16} expecting "the suffix must be exactly this
	// shape" — surprise-anchor them instead of requiring every admin to
	// remember ^ and $. Preserves the display text of the raw pattern
	// in error messages so users see what they wrote, not our anchored
	// rewrite.
	if entry.SuffixPattern != nil {
		match := entry.SuffixPattern.FindStringIndex(suffix)
		if match == nil || match[0] != 0 || match[1] != len(suffix) {
			return "", errors.Errorf("suffix %q doesn't match the required pattern for prefix %q (%s)", suffix, entry.Prefix, entry.SuffixPatternRaw)
		}
	}

	name := entry.Prefix + suffix
	if !model.IsValidChannelIdentifier(name) {
		return "", errors.Errorf("%q is not a valid channel URL name; combined prefix + suffix must be 2-64 lowercase letters, numbers, or hyphens", name)
	}
	return name, nil
}


// submitRequest validates the input and either creates the channel immediately (if the requester is
// a System Admin) or stores a pending request and posts it to the approval channel. It returns a
// message suitable for showing to the requester.
func (p *Plugin) submitRequest(in requestInput) (string, error) {
	config := p.getConfiguration()

	if strings.TrimSpace(in.DisplayName) == "" {
		return "", errors.New("a channel name is required")
	}

	if in.ChannelType != channelTypeOpen && in.ChannelType != channelTypePrivate {
		in.ChannelType = channelTypeOpen
	}

	name, err := p.resolveChannelName(config, in)
	if err != nil {
		return "", err
	}

	req := &channelRequest{
		ID:             model.NewId(),
		RequesterID:    in.RequesterID,
		TeamID:         in.TeamID,
		Name:           name,
		DisplayName:    strings.TrimSpace(in.DisplayName),
		Purpose:        strings.TrimSpace(in.Purpose),
		ChannelType:    in.ChannelType,
		MemberIDs:      in.MemberIDs,
		AdminMemberIDs: in.AdminMemberIDs,
	}

	requester, appErr := p.API.GetUser(in.RequesterID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load requesting user")
	}

	// Bypass approval for:
	//   - System Admins (always)
	//   - Users on the admin-configured auto-approve list (delegated managers)
	if requester.IsSystemAdmin() || config.AutoApproveContains(requester.Id) {
		channel, err := p.createChannelForRequest(req)
		if err != nil {
			return "", err
		}
		p.postWelcomeMessage(channel, req, requester, requester)
		return fmt.Sprintf("Created channel ~%s.", channel.Name), nil
	}

	if err := p.storeRequest(req); err != nil {
		return "", err
	}

	if err := p.postApprovalRequest(req, requester); err != nil {
		// Roll back the stored request so it isn't orphaned without an approval message.
		_ = p.API.KVDelete(kvRequestPrefix + req.ID)
		return "", err
	}

	return "Your channel request has been submitted for approval. You'll be notified once an admin responds.", nil
}

// postWelcomeMessage posts a bot message in the newly-created channel announcing who requested it
// and who approved, then @-mentions the added members and channel admins so everyone who was
// added is notified they're in the channel.
func (p *Plugin) postWelcomeMessage(channel *model.Channel, req *channelRequest, requester, approver *model.User) {
	body := fmt.Sprintf("Welcome — this channel was requested by @%s", requester.Username)
	if approver != nil && approver.Id != requester.Id {
		body += fmt.Sprintf(" and approved by @%s", approver.Username)
	}
	body += ". Adjust the header + purpose to fit."

	// Mention the added users so they get notified. The requester is the
	// creator and already here, so only call out the designated members
	// and admins. mentionList returns "" for an empty list.
	if members := p.mentionList(req.MemberIDs); members != "" {
		body += "\nAdded: " + members
	}
	if admins := p.mentionList(req.AdminMemberIDs); admins != "" {
		body += "\nChannel admins: " + admins
	}

	if _, appErr := p.API.CreatePost(&model.Post{
		UserId:    p.botUserID,
		ChannelId: channel.Id,
		Message:   body,
	}); appErr != nil {
		p.API.LogWarn("failed to post welcome message", "channel_id", channel.Id, "error", appErr.Error())
	}
}

// logAudit posts an audit-trail line to the configured audit channel.
// No-op when AuditChannelID is empty.
func (p *Plugin) logAudit(config *configuration, message string) {
	if strings.TrimSpace(config.AuditChannelID) == "" {
		return
	}
	if _, appErr := p.API.CreatePost(&model.Post{
		UserId:    p.botUserID,
		ChannelId: config.AuditChannelID,
		Message:   message,
	}); appErr != nil {
		p.API.LogWarn("audit post failed", "channel_id", config.AuditChannelID, "error", appErr.Error())
	}
}

// createChannelForRequest creates the channel described by req, adds the requester and any
// designated members, promotes anyone in AdminMemberIDs to channel_admin, and returns the
// created channel. Add-and-promote failures on individual users are logged but don't fail
// the overall operation — a partially-populated channel is better than nothing.
func (p *Plugin) createChannelForRequest(req *channelRequest) (*model.Channel, error) {
	channel, appErr := p.API.CreateChannel(&model.Channel{
		TeamId:      req.TeamID,
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Type:        model.ChannelType(req.ChannelType),
		Purpose:     req.Purpose,
		CreatorId:   req.RequesterID,
	})
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to create channel")
	}

	// Union of everyone who should be a member: requester + regular + admin sets.
	adminSet := map[string]bool{}
	for _, uid := range req.AdminMemberIDs {
		if uid != "" {
			adminSet[uid] = true
		}
	}

	added := map[string]bool{}
	for _, userID := range append(append([]string{req.RequesterID}, req.MemberIDs...), req.AdminMemberIDs...) {
		if userID == "" || added[userID] {
			continue
		}
		added[userID] = true
		if _, appErr := p.API.AddChannelMember(channel.Id, userID); appErr != nil {
			p.API.LogWarn("failed to add member to created channel", "channel_id", channel.Id, "user_id", userID, "error", appErr.Error())
			continue
		}
		// Promote to channel admin if they were designated as such.
		// "channel_user channel_admin" is the classic role string;
		// MM handles the scheme-role mapping internally.
		if adminSet[userID] {
			if _, appErr := p.API.UpdateChannelMemberRoles(channel.Id, userID, "channel_user channel_admin"); appErr != nil {
				p.API.LogWarn("failed to promote member to channel admin",
					"channel_id", channel.Id, "user_id", userID, "error", appErr.Error())
			}
		}
	}

	return channel, nil
}

func (p *Plugin) storeRequest(req *channelRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return errors.Wrap(err, "failed to marshal request")
	}
	if appErr := p.API.KVSet(kvRequestPrefix+req.ID, data); appErr != nil {
		return errors.Wrap(appErr, "failed to store request")
	}
	return nil
}

func (p *Plugin) loadRequest(id string) (*channelRequest, error) {
	data, appErr := p.API.KVGet(kvRequestPrefix + id)
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to load request")
	}
	if data == nil {
		return nil, nil
	}
	var req channelRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal request")
	}
	return &req, nil
}

// postApprovalRequest posts a message with Approve/Deny buttons for an admin to act on. It prefers
// the configured approval channel, but when that isn't configured (or can't be found) it falls back
// to DMing every System Admin so requests are never silently dropped.
func (p *Plugin) postApprovalRequest(req *channelRequest, requester *model.User) error {
	config := p.getConfiguration()
	attachment := p.approvalAttachment(req, requester)

	if strings.TrimSpace(config.ApprovalTeam) != "" && strings.TrimSpace(config.ApprovalChannel) != "" {
		channel, appErr := p.API.GetChannelByNameForTeamName(config.ApprovalTeam, config.ApprovalChannel, false)
		if appErr == nil {
			post := &model.Post{
				UserId:    p.botUserID,
				ChannelId: channel.Id,
				Message:   "@channel — a new channel request needs your review.",
			}
			model.ParseSlackAttachment(post, []*model.SlackAttachment{attachment})
			if _, appErr := p.API.CreatePost(post); appErr != nil {
				return errors.Wrap(appErr, "failed to post approval request")
			}
			return nil
		}
		p.API.LogWarn("configured approval channel not found; falling back to System Admins", "team", config.ApprovalTeam, "channel", config.ApprovalChannel, "error", appErr.Error())
	}

	return p.postApprovalToSystemAdmins(attachment)
}

// postApprovalToSystemAdmins DMs the approval request to every System Admin. Any admin can act on
// it; once one does, the others' copies resolve to an "already handled" message.
func (p *Plugin) postApprovalToSystemAdmins(attachment *model.SlackAttachment) error {
	admins, appErr := p.API.GetUsers(&model.UserGetOptions{Role: model.SystemAdminRoleId, Page: 0, PerPage: 100})
	if appErr != nil {
		return errors.Wrap(appErr, "failed to list System Admins")
	}

	posted := 0
	for _, admin := range admins {
		if admin.Id == p.botUserID {
			continue
		}
		dm, appErr := p.API.GetDirectChannel(admin.Id, p.botUserID)
		if appErr != nil {
			p.API.LogWarn("failed to open DM with System Admin", "user_id", admin.Id, "error", appErr.Error())
			continue
		}
		post := &model.Post{UserId: p.botUserID, ChannelId: dm.Id}
		model.ParseSlackAttachment(post, []*model.SlackAttachment{attachment})
		if _, appErr := p.API.CreatePost(post); appErr != nil {
			p.API.LogWarn("failed to DM approval request to System Admin", "user_id", admin.Id, "error", appErr.Error())
			continue
		}
		posted++
	}

	if posted == 0 {
		return errors.New("no System Admins are available to receive the approval request")
	}
	return nil
}

// approvalAttachment builds the Slack attachment (with Approve/Deny buttons) describing a request.
func (p *Plugin) approvalAttachment(req *channelRequest, requester *model.User) *model.SlackAttachment {
	visibility := "Public"
	if req.ChannelType == channelTypePrivate {
		visibility = "Private"
	}

	fields := []*model.SlackAttachmentField{
		{Title: "Requested by", Value: fmt.Sprintf("@%s", requester.Username), Short: true},
		{Title: "Visibility", Value: visibility, Short: true},
		{Title: "Channel name", Value: req.DisplayName, Short: true},
		{Title: "URL", Value: fmt.Sprintf("~%s", req.Name), Short: true},
	}
	if req.Purpose != "" {
		fields = append(fields, &model.SlackAttachmentField{Title: "Purpose", Value: req.Purpose, Short: false})
	}
	if len(req.MemberIDs) > 0 {
		fields = append(fields, &model.SlackAttachmentField{Title: "Members to add", Value: p.mentionList(req.MemberIDs), Short: false})
	}
	if len(req.AdminMemberIDs) > 0 {
		fields = append(fields, &model.SlackAttachmentField{
			Title: "Channel Admins to add",
			Value: p.mentionList(req.AdminMemberIDs),
			Short: false,
		})
	}

	siteURL := "/plugins/" + manifest.Id
	return &model.SlackAttachment{
		Title:   "Channel creation request",
		Color:   "#0058CC",
		Fields:  fields,
		Actions: p.approvalActions(req.ID, siteURL),
	}
}

func (p *Plugin) approvalActions(requestID, siteURL string) []*model.PostAction {
	return []*model.PostAction{
		{
			Id:    "approve",
			Name:  "Approve",
			Type:  model.PostActionTypeButton,
			Style: "primary",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeApprove,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
		{
			Id:    "deny",
			Name:  "Deny",
			Type:  model.PostActionTypeButton,
			Style: "danger",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeDeny,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
	}
}

// mentionList renders a list of user IDs as @mentions for display.
func (p *Plugin) mentionList(userIDs []string) string {
	mentions := make([]string, 0, len(userIDs))
	for _, id := range userIDs {
		if user, appErr := p.API.GetUser(id); appErr == nil {
			mentions = append(mentions, "@"+user.Username)
		}
	}
	return strings.Join(mentions, ", ")
}

// notifyRequester sends a DM from the bot to the requester about the outcome of their request.
func (p *Plugin) notifyRequester(requesterID, message string) {
	channel, appErr := p.API.GetDirectChannel(requesterID, p.botUserID)
	if appErr != nil {
		p.API.LogWarn("failed to open DM with requester", "user_id", requesterID, "error", appErr.Error())
		return
	}
	if _, appErr := p.API.CreatePost(&model.Post{
		UserId:    p.botUserID,
		ChannelId: channel.Id,
		Message:   message,
	}); appErr != nil {
		p.API.LogWarn("failed to notify requester", "user_id", requesterID, "error", appErr.Error())
	}
}
