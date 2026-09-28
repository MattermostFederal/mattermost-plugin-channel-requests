package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	kvWebhookRequestPrefix = "webhook_request_"

	webhookTypeIncoming = "incoming"
	webhookTypeOutgoing = "outgoing"

	fieldCallbackURL = "callback_url"
)

// webhookRequest is a pending request to create an incoming or outgoing webhook.
// Webhooks cross the network boundary, so all requests — including those from
// System Admins — go through the dedicated webhook approval channel and require
// an explicit Approve click. This ensures the approving admin's session token
// is available at creation time (no stored credentials needed).
type webhookRequest struct {
	ID          string `json:"id"`
	RequesterID string `json:"requester_id"`
	WebhookType string `json:"webhook_type"` // "incoming" or "outgoing"
	ChannelID   string `json:"channel_id"`
	TeamID      string `json:"team_id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	CallbackURL string `json:"callback_url,omitempty"` // outgoing only

	ApprovalPostID    string `json:"approval_post_id,omitempty"`
	ApprovalChannelID string `json:"approval_channel_id,omitempty"`
	DMRootPostID      string `json:"dm_root_post_id,omitempty"`
	DMChannelID       string `json:"dm_channel_id,omitempty"`
}

// submitWebhookRequest validates input and queues a webhook request in the
// dedicated webhook approval channel. Unlike other request types, System Admin
// callers are NOT given a creation bypass — the webhook creation call requires
// the approving admin's session token, which is only available at approve-time.
func (p *Plugin) submitWebhookRequest(requesterID, webhookType, channelID, displayName, description, callbackURL string) (string, error) {
	if webhookType != webhookTypeIncoming && webhookType != webhookTypeOutgoing {
		return "", errors.Errorf("unknown webhook type %q", webhookType)
	}
	if strings.TrimSpace(channelID) == "" {
		return "", errors.New("a target channel is required")
	}
	if strings.TrimSpace(displayName) == "" {
		return "", errors.New("a display name is required")
	}
	if webhookType == webhookTypeOutgoing && strings.TrimSpace(callbackURL) == "" {
		return "", errors.New("a callback URL is required for outgoing webhooks")
	}

	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load target channel")
	}

	requester, appErr := p.API.GetUser(requesterID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load requesting user")
	}

	req := &webhookRequest{
		ID:          model.NewId(),
		RequesterID: requesterID,
		WebhookType: webhookType,
		ChannelID:   channelID,
		TeamID:      channel.TeamId,
		DisplayName: strings.TrimSpace(displayName),
		Description: strings.TrimSpace(description),
		CallbackURL: strings.TrimSpace(callbackURL),
	}

	if err := p.storeWebhookRequest(req); err != nil {
		return "", err
	}

	approvalPostID, approvalChannelID, err := p.postWebhookApprovalRequest(req, requester, channel)
	if err != nil {
		_ = p.API.KVDelete(kvWebhookRequestPrefix + req.ID)
		return "", err
	}
	req.ApprovalPostID = approvalPostID
	req.ApprovalChannelID = approvalChannelID

	dmRootPostID, dmChannelID := p.sendWebhookTicketCreatedDM(req, requester, channel)
	req.DMRootPostID = dmRootPostID
	req.DMChannelID = dmChannelID

	if storeErr := p.storeWebhookRequest(req); storeErr != nil {
		p.API.LogWarn("failed to re-store webhook request with post IDs", "request_id", req.ID, "error", storeErr.Error())
	} else {
		p.storeTicketLookups(threadAnchor{
			ApprovalPostID:    approvalPostID,
			ApprovalChannelID: approvalChannelID,
			DMRootPostID:      dmRootPostID,
			DMChannelID:       dmChannelID,
		})
	}

	msg := "Your webhook request has been submitted for approval. You'll be notified once a System Admin responds."
	if requester.IsSystemAdmin() {
		msg = "Your webhook request has been posted to the webhook approval channel. As a System Admin you can approve it immediately."
	}
	return msg, nil
}

// sendWebhookTicketCreatedDM opens a DM and posts a card mirroring the
// webhook approval post (same fields, no buttons) as the root of the ticket thread.
func (p *Plugin) sendWebhookTicketCreatedDM(req *webhookRequest, requester *model.User, channel *model.Channel) (dmRootPostID, dmChannelID string) {
	dm, appErr := p.API.GetDirectChannel(req.RequesterID, p.botUserID)
	if appErr != nil {
		p.API.LogWarn("failed to open DM for webhook ticket notification", "user_id", req.RequesterID, "error", appErr.Error())
		return "", ""
	}

	attachment := p.webhookApprovalAttachment(req, requester, channel)
	attachment.Actions = nil
	attachment.Color = colorPending

	post := &model.Post{
		UserId:    p.botUserID,
		ChannelId: dm.Id,
		Message:   "Your webhook request has been submitted for approval. Reply to this thread to add context — your reply will be forwarded to the reviewers.",
	}
	model.ParseMessageAttachment(post, []*model.MessageAttachment{attachment})

	created, appErr := p.API.CreatePost(post)
	if appErr != nil {
		p.API.LogWarn("failed to send webhook ticket-created DM", "user_id", req.RequesterID, "error", appErr.Error())
		return "", ""
	}
	return created.Id, dm.Id
}

// createWebhookForRequest creates the webhook via the Mattermost REST API using
// a short-lived session for the approving admin. Mattermost relays interactive
// button clicks server-side, so the admin's browser token is never present in
// the Authorization header — instead we create a session here and revoke it
// immediately after the API call. No credentials are stored anywhere.
func (p *Plugin) createWebhookForRequest(req *webhookRequest, adminUserID string) (string, error) {
	if strings.TrimSpace(adminUserID) == "" {
		return "", errors.New("admin session token is missing — cannot create webhook")
	}

	internalURL := p.internalBaseURL()
	publicURL := p.siteURL()
	if internalURL == "" || publicURL == "" {
		return "", errors.New("site URL is not configured — cannot create webhook")
	}

	session, appErr := p.API.CreateSession(&model.Session{UserId: adminUserID})
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to create admin session for webhook creation")
	}
	defer p.API.RevokeSession(session.Id)

	// Use the internal localhost URL to avoid the loopback routing issue in
	// cloud deployments where the public SiteURL routes through a load balancer
	// that may block traffic originating from inside the server process.
	client := model.NewAPIv4Client(internalURL)
	client.SetToken(session.Token)
	ctx := context.Background()

	switch req.WebhookType {
	case webhookTypeIncoming:
		hook, _, apiErr := client.CreateIncomingWebhook(ctx, &model.IncomingWebhook{
			ChannelId:   req.ChannelID,
			TeamId:      req.TeamID,
			DisplayName: req.DisplayName,
			Description: req.Description,
		})
		if apiErr != nil {
			return "", errors.Wrap(apiErr, "failed to create incoming webhook")
		}
		return fmt.Sprintf("Your incoming webhook **%s** was approved. Your webhook URL:\n`%s/hooks/%s`",
			hook.DisplayName, publicURL, hook.Id), nil

	case webhookTypeOutgoing:
		hook, _, apiErr := client.CreateOutgoingWebhook(ctx, &model.OutgoingWebhook{
			TeamId:       req.TeamID,
			ChannelId:    req.ChannelID,
			DisplayName:  req.DisplayName,
			Description:  req.Description,
			CallbackURLs: []string{req.CallbackURL},
			ContentType:  "application/json",
		})
		if apiErr != nil {
			return "", errors.Wrap(apiErr, "failed to create outgoing webhook")
		}
		return fmt.Sprintf("Your outgoing webhook **%s** was approved and is now active.", hook.DisplayName), nil

	default:
		return "", errors.Errorf("unknown webhook type: %s", req.WebhookType)
	}
}

// siteURL returns the Mattermost instance's public site URL, used for
// user-facing links (e.g. webhook URLs sent in DMs).
func (p *Plugin) siteURL() string {
	cfg := p.API.GetConfig()
	if cfg == nil || cfg.ServiceSettings.SiteURL == nil {
		return ""
	}
	return *cfg.ServiceSettings.SiteURL
}

// internalBaseURL returns an HTTP base URL that reaches the Mattermost server
// via localhost, bypassing the public SiteURL. Cloud deployments route the
// public hostname through a load balancer; a plugin calling its own public IP
// from inside the server process often times out due to egress filtering.
// Using http://localhost:<port> avoids that routing entirely.
// Falls back to the public SiteURL if the listen address cannot be parsed.
func (p *Plugin) internalBaseURL() string {
	cfg := p.API.GetConfig()
	if cfg == nil || cfg.ServiceSettings.ListenAddress == nil {
		return p.siteURL()
	}
	_, port, err := net.SplitHostPort(*cfg.ServiceSettings.ListenAddress)
	if err != nil || port == "" {
		return p.siteURL()
	}
	return "http://localhost:" + port
}

func (p *Plugin) postWebhookApprovalRequest(req *webhookRequest, requester *model.User, channel *model.Channel) (postID, channelID string, err error) {
	webhookCh := p.ensureWebhookApprovalChannel()
	header := p.webhookApprovalHeader()
	return p.postApprovalAttachment(
		p.webhookApprovalAttachment(req, requester, channel),
		header,
		webhookCh,
	)
}

// webhookApprovalHeader builds the @-mention header for webhook requests.
// Reuses BotReviewGroup (security team) since webhooks have the same
// network-boundary concerns as bot accounts.
func (p *Plugin) webhookApprovalHeader() string {
	config := p.getConfiguration()
	group := strings.TrimSpace(config.BotReviewGroup)
	adminMentions := p.systemAdminMentions()

	suffix := " — a webhook request needs security and System Admin review."
	if group == "" {
		return adminMentions + suffix
	}
	groupMention := "@" + strings.TrimPrefix(group, "@")
	if adminMentions == "@channel" {
		return groupMention + " @channel" + suffix
	}
	return groupMention + " " + adminMentions + suffix
}

func (p *Plugin) webhookApprovalAttachment(req *webhookRequest, requester *model.User, channel *model.Channel) *model.MessageAttachment {
	typeLabel := "Incoming webhook"
	if req.WebhookType == webhookTypeOutgoing {
		typeLabel = "Outgoing webhook"
	}

	fields := []*model.MessageAttachmentField{
		{Title: "Requested by", Value: fmt.Sprintf("@%s", requester.Username), Short: true},
		{Title: "Type", Value: typeLabel, Short: true},
		{Title: "Target channel", Value: fmt.Sprintf("~%s", channel.Name), Short: true},
		{Title: "Display name", Value: req.DisplayName, Short: true},
	}

	var details []string
	if req.Description != "" {
		details = append(details, fmt.Sprintf("**Description:** %s", req.Description))
	}
	if req.CallbackURL != "" {
		details = append(details, fmt.Sprintf("**Callback URL:** `%s`", req.CallbackURL))
	}

	siteURL := "/plugins/" + manifest.Id
	return &model.MessageAttachment{
		Title:   "Webhook request",
		Color:   "#FF6B35",
		Fields:  fields,
		Text:    strings.Join(details, "\n"),
		Actions: p.webhookApprovalActions(req.ID, siteURL),
	}
}

func (p *Plugin) webhookApprovalActions(requestID, siteURL string) []*model.PostAction {
	return []*model.PostAction{
		{
			Id: "approve", Name: "Approve", Type: model.PostActionTypeButton, Style: "primary",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeApproveWebhook,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
		{
			Id: "deny", Name: "Deny", Type: model.PostActionTypeButton, Style: "danger",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeDenyWebhook,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
	}
}

func (p *Plugin) storeWebhookRequest(req *webhookRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return errors.Wrap(err, "failed to marshal webhook request")
	}
	if appErr := p.API.KVSet(kvWebhookRequestPrefix+req.ID, data); appErr != nil {
		return errors.Wrap(appErr, "failed to store webhook request")
	}
	return nil
}

func (p *Plugin) loadWebhookRequest(id string) (*webhookRequest, []byte, error) {
	data, appErr := p.API.KVGet(kvWebhookRequestPrefix + id)
	if appErr != nil {
		return nil, nil, errors.Wrap(appErr, "failed to load webhook request")
	}
	if data == nil {
		return nil, nil, nil
	}
	var req webhookRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, nil, errors.Wrap(err, "failed to unmarshal webhook request")
	}
	return &req, data, nil
}

// openIncomingWebhookDialog opens the request dialog for an incoming webhook.
func (p *Plugin) openIncomingWebhookDialog(triggerID string) error {
	dialog := model.Dialog{
		CallbackId:       "incoming_webhook_request",
		Title:            "Request an Incoming Webhook",
		IntroductionText: "Incoming webhooks let external services post messages into a Mattermost channel. Requires System Admin approval.",
		SubmitLabel:      "Submit request",
		Elements: []model.DialogElement{
			{
				DisplayName: "Target channel",
				Name:        fieldChannelID,
				Type:        "select",
				DataSource:  "channels",
				HelpText:    "The channel this webhook will post messages into.",
			},
			{
				DisplayName: "Display name",
				Name:        fieldDisplayName,
				Type:        "text",
				Placeholder: "e.g. Monitoring Alerts",
				MaxLength:   64,
			},
			{
				DisplayName: "Description / purpose",
				Name:        fieldPurpose,
				Type:        "textarea",
				Optional:    true,
				Placeholder: "What service is this for? What events does it send?",
				MaxLength:   500,
			},
		},
	}
	return p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeIncomingWebhookDialog),
		Dialog:    dialog,
	})
}

// openOutgoingWebhookDialog opens the request dialog for an outgoing webhook.
func (p *Plugin) openOutgoingWebhookDialog(triggerID string) error {
	dialog := model.Dialog{
		CallbackId:       "outgoing_webhook_request",
		Title:            "Request an Outgoing Webhook",
		IntroductionText: "Outgoing webhooks send channel messages to an external service. Requires System Admin approval.",
		SubmitLabel:      "Submit request",
		Elements: []model.DialogElement{
			{
				DisplayName: "Target channel",
				Name:        fieldChannelID,
				Type:        "select",
				DataSource:  "channels",
				HelpText:    "The channel whose messages trigger this webhook.",
			},
			{
				DisplayName: "Display name",
				Name:        fieldDisplayName,
				Type:        "text",
				Placeholder: "e.g. Incident Relay",
				MaxLength:   64,
			},
			{
				DisplayName: "Description / purpose",
				Name:        fieldPurpose,
				Type:        "textarea",
				Optional:    true,
				Placeholder: "What does this webhook do? What service does it relay to?",
				MaxLength:   500,
			},
			{
				DisplayName: "Callback URL",
				Name:        fieldCallbackURL,
				Type:        "text",
				Placeholder: "https://example.com/mattermost-hook",
				HelpText:    "The external URL Mattermost will POST to when messages are sent in the target channel.",
				MaxLength:   1024,
			},
		},
	}
	return p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeOutgoingWebhookDialog),
		Dialog:    dialog,
	})
}
