package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	kvBotRequestPrefix  = "bot_request_"
	botDialogCallbackID = "bot_request"

	fieldBotUsername    = "bot_username"
	fieldBotDisplayName = "bot_display_name"
	fieldBotDescription = "bot_description"
	fieldBotOwner       = "bot_owner"
)

// botRequest is a pending request to create a new bot account. Because bots
// carry integration credentials and system-wide access, these requests always
// route to the root approval channel, ping the configured security review
// group, and can only be approved by System Admins.
type botRequest struct {
	ID          string `json:"id"`
	RequesterID string `json:"requester_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	OwnerUserID string `json:"owner_user_id,omitempty"`

	// Optional extras — requested alongside the bot account in the same ticket.
	// All three are created at approve-time using the approver's session token.
	RequestToken               bool   `json:"request_token,omitempty"`
	IncomingWebhookChannelID   string `json:"incoming_webhook_channel_id,omitempty"`
	IncomingWebhookDisplayName string `json:"incoming_webhook_display_name,omitempty"`
	OutgoingWebhookChannelID   string `json:"outgoing_webhook_channel_id,omitempty"`
	OutgoingWebhookDisplayName string `json:"outgoing_webhook_display_name,omitempty"`
	OutgoingWebhookCallbackURL string `json:"outgoing_webhook_callback_url,omitempty"`

	// Ticket messaging fields — set after the approval post and DM are created.
	ApprovalPostID    string `json:"approval_post_id,omitempty"`
	ApprovalChannelID string `json:"approval_channel_id,omitempty"`
	DMRootPostID      string `json:"dm_root_post_id,omitempty"`
	DMChannelID       string `json:"dm_channel_id,omitempty"`
}

// botRequestInput carries the caller-supplied fields for a new bot request.
// The slash-command dialog path populates only the base fields; the webapp
// path additionally sets the optional token/webhook fields.
type botRequestInput struct {
	Username                   string
	DisplayName                string
	Description                string
	OwnerUserID                string
	RequestToken               bool
	IncomingWebhookChannelID   string
	IncomingWebhookDisplayName string
	OutgoingWebhookChannelID   string
	OutgoingWebhookDisplayName string
	OutgoingWebhookCallbackURL string
}

// submitBotRequest validates the input and either creates the bot immediately
// (System Admins) or stores a pending request routed to the root approval
// channel with a security group ping.
func (p *Plugin) submitBotRequest(requesterID string, input botRequestInput) (string, error) {
	username := strings.TrimSpace(strings.TrimPrefix(input.Username, "@"))
	if username == "" {
		return "", errors.New("a bot username is required")
	}
	if strings.TrimSpace(input.DisplayName) == "" {
		return "", errors.New("a display name is required")
	}

	requester, appErr := p.API.GetUser(requesterID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load requesting user")
	}

	req := &botRequest{
		ID:                         model.NewId(),
		RequesterID:                requesterID,
		Username:                   username,
		DisplayName:                strings.TrimSpace(input.DisplayName),
		Description:                strings.TrimSpace(input.Description),
		OwnerUserID:                strings.TrimSpace(input.OwnerUserID),
		RequestToken:               input.RequestToken,
		IncomingWebhookChannelID:   strings.TrimSpace(input.IncomingWebhookChannelID),
		IncomingWebhookDisplayName: strings.TrimSpace(input.IncomingWebhookDisplayName),
		OutgoingWebhookChannelID:   strings.TrimSpace(input.OutgoingWebhookChannelID),
		OutgoingWebhookDisplayName: strings.TrimSpace(input.OutgoingWebhookDisplayName),
		OutgoingWebhookCallbackURL: strings.TrimSpace(input.OutgoingWebhookCallbackURL),
	}

	config := p.getConfiguration()

	// System Admins can create bots without going through approval.
	if requester.IsSystemAdmin() {
		bot, err := p.createBotForRequest(req)
		if err != nil {
			return "", err
		}
		p.logAudit(config, fmt.Sprintf("BOT CREATED: @%s created bot @%s directly", requester.Username, bot.Username))
		return fmt.Sprintf("Bot **@%s** created.", bot.Username), nil
	}

	if err := p.storeBotRequest(req); err != nil {
		return "", err
	}

	approvalPostID, approvalChannelID, err := p.postBotApprovalRequest(req, requester)
	if err != nil {
		_ = p.API.KVDelete(kvBotRequestPrefix + req.ID)
		return "", err
	}
	req.ApprovalPostID = approvalPostID
	req.ApprovalChannelID = approvalChannelID

	dmRootPostID, dmChannelID := p.sendBotTicketCreatedDM(req, requester)
	req.DMRootPostID = dmRootPostID
	req.DMChannelID = dmChannelID

	if storeErr := p.storeBotRequest(req); storeErr != nil {
		p.API.LogWarn("failed to re-store bot request with post IDs", "request_id", req.ID, "error", storeErr.Error())
	} else {
		p.storeTicketLookups(threadAnchor{
			ApprovalPostID:    approvalPostID,
			ApprovalChannelID: approvalChannelID,
			DMRootPostID:      dmRootPostID,
			DMChannelID:       dmChannelID,
		})
	}

	return "Your bot account request has been submitted for approval. You'll be notified once a System Admin responds.", nil
}

// sendBotTicketCreatedDM opens a DM and posts a card mirroring the bot
// approval post (same fields, no buttons) as the root of the ticket thread.
func (p *Plugin) sendBotTicketCreatedDM(req *botRequest, requester *model.User) (dmRootPostID, dmChannelID string) {
	dm, appErr := p.API.GetDirectChannel(req.RequesterID, p.botUserID)
	if appErr != nil {
		p.API.LogWarn("failed to open DM for bot ticket notification", "user_id", req.RequesterID, "error", appErr.Error())
		return "", ""
	}

	attachment := p.botApprovalAttachment(req, requester)
	attachment.Actions = nil
	attachment.Color = colorPending

	post := &model.Post{
		UserId:    p.botUserID,
		ChannelId: dm.Id,
		Message:   "Your bot account request has been submitted for approval. Reply to this thread to add context — your reply will be forwarded to the reviewers.",
	}
	model.ParseMessageAttachment(post, []*model.MessageAttachment{attachment})

	created, appErr := p.API.CreatePost(post)
	if appErr != nil {
		p.API.LogWarn("failed to send bot ticket-created DM", "user_id", req.RequesterID, "error", appErr.Error())
		return "", ""
	}
	return created.Id, dm.Id
}

// createBotForRequest creates the Mattermost bot account. OwnerId is set to
// the selected owner (if provided) or the requester.
func (p *Plugin) createBotForRequest(req *botRequest) (*model.Bot, error) {
	ownerID := req.OwnerUserID
	if ownerID == "" {
		ownerID = req.RequesterID
	}
	bot, appErr := p.API.CreateBot(&model.Bot{
		Username:    req.Username,
		DisplayName: req.DisplayName,
		Description: req.Description,
		OwnerId:     ownerID,
	})
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to create bot")
	}
	return bot, nil
}

// postBotApprovalRequest posts to the dedicated bot-requests approval channel.
// Only System Admins may approve.
func (p *Plugin) postBotApprovalRequest(req *botRequest, requester *model.User) (postID, channelID string, err error) {
	ch := p.ensureBotApprovalChannel()
	return p.postApprovalAttachment(
		p.botApprovalAttachment(req, requester),
		p.botApprovalHeader(),
		ch,
	)
}

// botApprovalHeader builds the @-mention header for a bot approval post.
// When BotReviewGroup is configured, that group is mentioned first so its
// members are notified alongside System Admins.
func (p *Plugin) botApprovalHeader() string {
	config := p.getConfiguration()
	group := strings.TrimSpace(config.BotReviewGroup)
	adminMentions := p.systemAdminMentions()

	suffix := " — a bot account request needs security and System Admin review."
	if group == "" {
		return adminMentions + suffix
	}
	groupMention := "@" + strings.TrimPrefix(group, "@")
	if adminMentions == "@channel" {
		return groupMention + " @channel" + suffix
	}
	return groupMention + " " + adminMentions + suffix
}

func (p *Plugin) botApprovalAttachment(req *botRequest, requester *model.User) *model.MessageAttachment {
	fields := []*model.MessageAttachmentField{
		{Title: "Requested by", Value: fmt.Sprintf("@%s", requester.Username), Short: true},
		{Title: "Bot username", Value: fmt.Sprintf("@%s", req.Username), Short: true},
	}

	var details []string
	details = append(details, fmt.Sprintf("**Display name:** %s", req.DisplayName))
	if req.Description != "" {
		details = append(details, fmt.Sprintf("**Description:** %s", req.Description))
	}
	if req.OwnerUserID != "" {
		details = append(details, fmt.Sprintf("**Owner:** %s", p.mentionList([]string{req.OwnerUserID})))
	}
	if req.RequestToken {
		details = append(details, "**Token:** requested (generated at approve-time, sent in DM)")
	}
	if req.IncomingWebhookChannelID != "" {
		name := req.IncomingWebhookDisplayName
		if name == "" {
			name = "incoming webhook"
		}
		details = append(details, fmt.Sprintf("**Incoming webhook:** `%s` → %s", name, p.channelDisplayRef(req.IncomingWebhookChannelID)))
	}
	if req.OutgoingWebhookChannelID != "" {
		name := req.OutgoingWebhookDisplayName
		if name == "" {
			name = "outgoing webhook"
		}
		details = append(details, fmt.Sprintf("**Outgoing webhook:** `%s` → %s (callback: `%s`)", name, p.channelDisplayRef(req.OutgoingWebhookChannelID), req.OutgoingWebhookCallbackURL))
	}

	siteURL := "/plugins/" + manifest.Id
	return &model.MessageAttachment{
		Title:   "Bot account request",
		Color:   "#8B008B",
		Fields:  fields,
		Text:    strings.Join(details, "\n"),
		Actions: p.botApprovalActions(req.ID, siteURL),
	}
}

func (p *Plugin) botApprovalActions(requestID, siteURL string) []*model.PostAction {
	return []*model.PostAction{
		{
			Id: "approve", Name: "Approve", Type: model.PostActionTypeButton, Style: "primary",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeApproveBot,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
		{
			Id: "deny", Name: "Deny", Type: model.PostActionTypeButton, Style: "danger",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeDenyBot,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
	}
}

// channelDisplayRef returns "#channel-name (Team: DisplayName)" for display
// in approval cards. Falls back gracefully when either lookup fails.
func (p *Plugin) channelDisplayRef(channelID string) string {
	ch, appErr := p.API.GetChannel(channelID)
	if appErr != nil {
		return channelID
	}
	team, appErr := p.API.GetTeam(ch.TeamId)
	if appErr != nil {
		return "#" + ch.Name
	}
	return fmt.Sprintf("#%s (Team: %s)", ch.Name, team.DisplayName)
}

func (p *Plugin) storeBotRequest(req *botRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return errors.Wrap(err, "failed to marshal bot request")
	}
	if appErr := p.API.KVSet(kvBotRequestPrefix+req.ID, data); appErr != nil {
		return errors.Wrap(appErr, "failed to store bot request")
	}
	return nil
}

func (p *Plugin) loadBotRequest(id string) (*botRequest, []byte, error) {
	data, appErr := p.API.KVGet(kvBotRequestPrefix + id)
	if appErr != nil {
		return nil, nil, errors.Wrap(appErr, "failed to load bot request")
	}
	if data == nil {
		return nil, nil, nil
	}
	var req botRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, nil, errors.Wrap(err, "failed to unmarshal bot request")
	}
	return &req, data, nil
}

// openBotRequestDialog opens the interactive dialog for submitting a bot
// account request.
func (p *Plugin) openBotRequestDialog(triggerID string) error {
	dialog := model.Dialog{
		CallbackId:       botDialogCallbackID,
		Title:            "Request a Bot Account",
		IntroductionText: "Bot account requests are reviewed by System Admins and the security team.",
		SubmitLabel:      "Submit request",
		Elements: []model.DialogElement{
			{
				DisplayName: "Bot username",
				Name:        fieldBotUsername,
				Type:        "text",
				Placeholder: "my-integration-bot",
				HelpText:    "The bot's @handle. Lowercase letters, numbers, and hyphens only.",
				MaxLength:   64,
			},
			{
				DisplayName: "Display name",
				Name:        fieldBotDisplayName,
				Type:        "text",
				Placeholder: "My Integration Bot",
				MaxLength:   64,
			},
			{
				DisplayName: "Description",
				Name:        fieldBotDescription,
				Type:        "textarea",
				Optional:    true,
				Placeholder: "What does this bot do? What systems will it integrate with?",
				MaxLength:   500,
			},
			{
				DisplayName: "Bot owner",
				Name:        fieldBotOwner,
				Type:        "select",
				DataSource:  "users",
				Optional:    true,
				HelpText:    "The user responsible for this bot. Defaults to you if blank.",
			},
		},
	}
	return p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeBotDialog),
		Dialog:    dialog,
	})
}
