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

	// nameTemplatePlaceholder is replaced with the slugified base name in ChannelNameTemplate.
	nameTemplatePlaceholder = "{{name}}"

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
	AdminIDs    []string `json:"admin_ids"`
}

// requestInput is the normalized set of values gathered from either entry point (slash command
// dialog or webapp modal) before a request is created.
type requestInput struct {
	RequesterID string
	TeamID      string
	DisplayName string
	Name        string
	Purpose     string
	ChannelType string
	MemberIDs   []string
	AdminIDs    []string
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

// resolveChannelName derives the final channel URL name from the request, applying the configured
// slug template and validating it against the configured pattern.
func (p *Plugin) resolveChannelName(config *configuration, in requestInput) (string, error) {
	base := in.Name
	if strings.TrimSpace(base) == "" {
		base = in.DisplayName
	}
	base = slugify(base)

	name := base
	if tmpl := strings.TrimSpace(config.ChannelNameTemplate); tmpl != "" {
		name = slugify(strings.ReplaceAll(tmpl, nameTemplatePlaceholder, base))
	}

	if !model.IsValidChannelIdentifier(name) {
		return "", errors.Errorf("%q is not a valid channel URL name; use 2-64 lowercase letters, numbers, or hyphens", name)
	}

	if config.compiledPattern != nil && !config.compiledPattern.MatchString(name) {
		return "", errors.Errorf("the channel URL %q doesn't match the required naming pattern %q", name, config.ChannelNamePattern)
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
		ID:          model.NewId(),
		RequesterID: in.RequesterID,
		TeamID:      in.TeamID,
		Name:        name,
		DisplayName: strings.TrimSpace(in.DisplayName),
		Purpose:     strings.TrimSpace(in.Purpose),
		ChannelType: in.ChannelType,
		MemberIDs:   in.MemberIDs,
		AdminIDs:    in.AdminIDs,
	}

	// System Admins skip the approval step and create the channel directly.
	requester, appErr := p.API.GetUser(in.RequesterID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load requesting user")
	}
	if requester.IsSystemAdmin() {
		channel, err := p.createChannelForRequest(req)
		if err != nil {
			return "", err
		}
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

// createChannelForRequest creates the channel described by req, adds the requester and any
// designated members and channel admins, promotes the admins, and returns the created channel.
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

	admins := map[string]bool{}
	for _, id := range req.AdminIDs {
		admins[id] = true
	}

	// Always add the requester, then the designated members and channel admins (admins are added as
	// members too). Skip duplicates and don't fail the whole operation if an individual user can't
	// be added or promoted.
	adminRoles := fmt.Sprintf("%s %s", model.ChannelUserRoleId, model.ChannelAdminRoleId)
	added := map[string]bool{}
	var addedAll, addedAdmins []string
	userIDs := append([]string{req.RequesterID}, req.MemberIDs...)
	userIDs = append(userIDs, req.AdminIDs...)
	for _, userID := range userIDs {
		if userID == "" || added[userID] {
			continue
		}
		added[userID] = true
		if _, appErr := p.API.AddChannelMember(channel.Id, userID); appErr != nil {
			p.API.LogWarn("failed to add member to created channel", "channel_id", channel.Id, "user_id", userID, "error", appErr.Error())
			continue
		}
		if admins[userID] {
			if _, appErr := p.API.UpdateChannelMemberRoles(channel.Id, userID, adminRoles); appErr != nil {
				p.API.LogWarn("failed to promote channel admin", "channel_id", channel.Id, "user_id", userID, "error", appErr.Error())
			}
		}

		// Collect every user actually added so they can all be mentioned in the welcome message.
		addedAll = append(addedAll, userID)
		if admins[userID] {
			addedAdmins = append(addedAdmins, userID)
		}
	}

	p.postWelcomeMessage(channel, addedAll, addedAdmins)

	return channel, nil
}

// postWelcomeMessage posts a message from the bot in the newly created channel that mentions every
// user who was added, so they're all notified they've been added.
func (p *Plugin) postWelcomeMessage(channel *model.Channel, addedIDs, adminIDs []string) {
	if len(addedIDs) == 0 {
		return
	}

	message := fmt.Sprintf("👋 %s — welcome! You've been added to this channel.", p.mentionList(addedIDs))
	if len(adminIDs) > 0 {
		message += fmt.Sprintf("\nChannel admins: %s", p.mentionList(adminIDs))
	}

	if _, appErr := p.API.CreatePost(&model.Post{
		UserId:    p.botUserID,
		ChannelId: channel.Id,
		Message:   message,
	}); appErr != nil {
		p.API.LogWarn("failed to post welcome message", "channel_id", channel.Id, "error", appErr.Error())
	}
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
	if len(req.AdminIDs) > 0 {
		fields = append(fields, &model.SlackAttachmentField{Title: "Channel admins", Value: p.mentionList(req.AdminIDs), Short: false})
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
