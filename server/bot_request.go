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
// carry integration credentials and system-wide access, these requests route
// to the bot approval channel and require two-step approval (security + system).
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

	// twoStepState tracks security + system approvals. Missing fields on
	// older KV records unmarshal as empty (both steps still required).
	twoStepState
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
	// ForceQueue skips System Admin bypass (slash-command dialog path).
	ForceQueue bool
}

// submitBotRequest validates the input and either creates the bot immediately
// (System Admins) or stores a pending request for two-step approval.
func (p *Plugin) submitBotRequest(requesterID string, input botRequestInput) (string, error) {
	username := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(input.Username, "@")))
	if username == "" {
		return "", errors.New("A bot username is required.")
	}
	if len(username) < 3 || len(username) > 22 {
		return "", errors.New("Bot username must be 3-22 characters.")
	}
	if !model.IsValidUsername(username) {
		return "", errors.New("Bot username must be lowercase letters, numbers, and . - _ (3-22 characters).")
	}
	if strings.TrimSpace(input.DisplayName) == "" {
		return "", errors.New("A display name is required.")
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

	// System Admins can create bots without going through approval — unless
	// ForceQueue (slash-command dialog) which always produces an approval card.
	if !input.ForceQueue && requester.IsSystemAdmin() {
		bot, err := p.createBotForRequest(req)
		if err != nil {
			return "", err
		}
		p.logAudit(auditDirect, fmt.Sprintf("@%s created bot @%s directly", requester.Username, bot.Username), "")
		return fmt.Sprintf("Bot **@%s** created.", bot.Username), nil
	}

	if err := p.storeBotRequest(req); err != nil {
		return "", err
	}
	rawBefore, snapErr := json.Marshal(req)
	if snapErr != nil {
		_ = p.API.KVDelete(kvBotRequestPrefix + req.ID)
		return "", errors.Wrap(snapErr, "failed to snapshot bot request")
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

	if ok, casErr := p.kvUpdateIfUnchanged(kvBotRequestPrefix+req.ID, rawBefore, req); casErr != nil {
		p.API.LogWarn("failed to re-store bot request with post IDs", "request_id", req.ID, "error", casErr.Error())
	} else if !ok {
		p.API.LogWarn("bot request already claimed before post IDs were written", "request_id", req.ID)
	} else {
		p.storeTicketLookups(threadAnchor{
			ApprovalPostID:    approvalPostID,
			ApprovalChannelID: approvalChannelID,
			DMRootPostID:      dmRootPostID,
			DMChannelID:       dmChannelID,
			Kind:              requestTypeBot,
		})
	}

	return "Your bot account request has been submitted for approval. It needs a security approval and a system approval. You'll be notified as each step completes.", nil
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
func (p *Plugin) postBotApprovalRequest(req *botRequest, requester *model.User) (postID, channelID string, err error) {
	ch := p.ensureBotApprovalChannel()
	return p.postApprovalAttachment(
		p.botApprovalAttachment(req, requester),
		p.botApprovalHeader(),
		ch,
	)
}

func (p *Plugin) botApprovalHeader() string {
	return "@channel — a bot account request needs review (requires a security approval and a system approval)."
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

	fields = append(fields, &model.MessageAttachmentField{
		Title: "Approvals", Value: p.twoStepStatusValue(req.twoStepState), Short: false,
	})

	siteURL := "/plugins/" + manifest.Id
	return &model.MessageAttachment{
		Title:   "Bot account request",
		Color:   "#8B008B",
		Fields:  fields,
		Text:    strings.Join(details, "\n"),
		Actions: p.botApprovalActions(req.ID, siteURL),
	}
}

func (p *Plugin) botApprovalAttachmentWithNotice(req *botRequest, requester *model.User, notice string) *model.MessageAttachment {
	att := p.botApprovalAttachment(req, requester)
	att.Color = "#D24B4E"
	att.Fields = append([]*model.MessageAttachmentField{
		{Title: "⚠️ Action needed", Value: notice, Short: false},
	}, att.Fields...)
	return att
}

func (p *Plugin) botApprovalActions(requestID, siteURL string) []*model.PostAction {
	return []*model.PostAction{
		{
			Id: "approve", Name: approveButtonLabel, Type: model.PostActionTypeButton, Style: "primary",
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
		IntroductionText: "Bot account requests need a security approval and a system approval.",
		SubmitLabel:      "Submit request",
		Elements: []model.DialogElement{
			{
				DisplayName: "Bot username",
				Name:        fieldBotUsername,
				Type:        "text",
				Placeholder: "my-integration-bot",
				HelpText:    "The bot's @handle. Lowercase letters, numbers, and . - _ (3-22 characters).",
				MaxLength:   22,
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
	if appErr := p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeBotDialog),
		Dialog:    dialog,
	}); appErr != nil {
		return appErr
	}
	return nil
}
