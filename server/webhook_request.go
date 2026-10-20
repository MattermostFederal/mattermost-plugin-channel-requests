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
// System Admins — go through two-step approval. Creation uses the final
// approver's session (no stored credentials).
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

	twoStepState
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
	rawBefore, snapErr := json.Marshal(req)
	if snapErr != nil {
		_ = p.API.KVDelete(kvWebhookRequestPrefix + req.ID)
		return "", errors.Wrap(snapErr, "failed to snapshot webhook request")
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

	if ok, casErr := p.kvUpdateIfUnchanged(kvWebhookRequestPrefix+req.ID, rawBefore, req); casErr != nil {
		p.API.LogWarn("failed to re-store webhook request with post IDs", "request_id", req.ID, "error", casErr.Error())
	} else if !ok {
		p.API.LogWarn("webhook request already claimed before post IDs were written", "request_id", req.ID)
	} else {
		p.storeTicketLookups(threadAnchor{
			ApprovalPostID:    approvalPostID,
			ApprovalChannelID: approvalChannelID,
			DMRootPostID:      dmRootPostID,
			DMChannelID:       dmChannelID,
			Kind:              requestTypeWebhook,
		})
	}

	msg := "Your webhook request has been submitted for approval. It needs a security approval and a system approval."
	if requester.IsSystemAdmin() {
		msg = "Your webhook request has been posted for two-step approval. As a System Admin you can fill the system step; a security approver must fill the other."
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
// a short-lived session for the approving admin. Returns a user-facing DM
// message and the created hook ID (empty for outgoing) so callers can delete
// an undelivered incoming-hook secret.
func (p *Plugin) createWebhookForRequest(req *webhookRequest, adminUserID string) (dmMsg, hookID string, err error) {
	if strings.TrimSpace(adminUserID) == "" {
		return "", "", errors.New("admin session token is missing — cannot create webhook")
	}

	internalURL := p.internalBaseURL()
	publicURL := p.siteURL()
	if internalURL == "" || publicURL == "" {
		return "", "", errors.New("site URL is not configured — cannot create webhook")
	}

	if p.createWebhookFn != nil {
		return p.createWebhookFn(req, adminUserID)
	}

	session, appErr := p.API.CreateSession(&model.Session{UserId: adminUserID})
	if appErr != nil {
		return "", "", errors.Wrap(appErr, "failed to create admin session for webhook creation")
	}
	defer func() {
		if revErr := p.API.RevokeSession(session.Id); revErr != nil {
			p.API.LogWarn("failed to revoke admin session after webhook op", "error", revErr.Error())
		}
	}()

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
			return "", "", errors.Wrap(apiErr, "failed to create incoming webhook")
		}
		return fmt.Sprintf("Your incoming webhook **%s** was approved. Your webhook URL:\n`%s/hooks/%s`",
			hook.DisplayName, publicURL, hook.Id), hook.Id, nil

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
			return "", "", errors.Wrap(apiErr, "failed to create outgoing webhook")
		}
		return fmt.Sprintf("Your outgoing webhook **%s** was approved and is now active.", hook.DisplayName), hook.Id, nil

	default:
		return "", "", errors.Errorf("unknown webhook type: %s", req.WebhookType)
	}
}

// deleteWebhook removes a hook created by createWebhookForRequest. Used when
// the requester DM fails after creation so we don't leave an unowned secret.
// adminUserID is the approver whose session is used for the REST delete.
func (p *Plugin) deleteWebhook(webhookType, hookID, adminUserID string) error {
	if strings.TrimSpace(hookID) == "" {
		return nil
	}
	if p.deleteWebhookFn != nil {
		return p.deleteWebhookFn(webhookType, hookID)
	}

	internalURL := p.internalBaseURL()
	if internalURL == "" {
		return errors.New("site URL is not configured — cannot delete webhook")
	}
	session, appErr := p.API.CreateSession(&model.Session{UserId: adminUserID})
	if appErr != nil {
		return errors.Wrap(appErr, "failed to create admin session for webhook deletion")
	}
	defer func() {
		if revErr := p.API.RevokeSession(session.Id); revErr != nil {
			p.API.LogWarn("failed to revoke admin session after webhook op", "error", revErr.Error())
		}
	}()

	client := model.NewAPIv4Client(internalURL)
	client.SetToken(session.Token)
	ctx := context.Background()

	switch webhookType {
	case webhookTypeIncoming:
		if _, err := client.DeleteIncomingWebhook(ctx, hookID); err != nil {
			return errors.Wrap(err, "failed to delete incoming webhook")
		}
	case webhookTypeOutgoing:
		if _, err := client.DeleteOutgoingWebhook(ctx, hookID); err != nil {
			return errors.Wrap(err, "failed to delete outgoing webhook")
		}
	}
	return nil
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
// on its listen address, bypassing the public SiteURL. Cloud deployments route
// the public hostname through a load balancer; a plugin calling its own public
// IP from inside the server process often times out due to egress filtering.
// Falls back to the public SiteURL when the listen address cannot be parsed,
// or when TLS is enabled on a loopback host (local cert verification fails).
func (p *Plugin) internalBaseURL() string {
	cfg := p.API.GetConfig()
	if cfg == nil || cfg.ServiceSettings.ListenAddress == nil {
		return p.siteURL()
	}
	host, port, err := net.SplitHostPort(*cfg.ServiceSettings.ListenAddress)
	if err != nil || port == "" {
		return p.siteURL()
	}
	scheme := "http"
	if cfg.ServiceSettings.ConnectionSecurity != nil &&
		*cfg.ServiceSettings.ConnectionSecurity == model.ConnSecurityTLS {
		scheme = "https"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "localhost"
	}
	// TLS certs usually match SiteURL's hostname, not a raw listen IP / loopback.
	// Prefer SiteURL so webhook/guest/bot-extra API clients pass cert checks.
	if scheme == "https" {
		if host == "localhost" || host == "127.0.0.1" || host == "::1" || net.ParseIP(host) != nil {
			return p.siteURL()
		}
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

func (p *Plugin) postWebhookApprovalRequest(req *webhookRequest, requester *model.User, channel *model.Channel) (postID, channelID string, err error) {
	webhookCh := p.ensureWebhookApprovalChannel()
	return p.postApprovalAttachment(
		p.webhookApprovalAttachment(req, requester, channel),
		p.webhookApprovalHeader(),
		webhookCh,
	)
}

func (p *Plugin) webhookApprovalHeader() string {
	return "@channel — a webhook request needs review (requires a security approval and a system approval)."
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
		{Title: "Approvals", Value: p.twoStepStatusValue(req.twoStepState), Short: false},
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

func (p *Plugin) webhookApprovalAttachmentWithNotice(req *webhookRequest, requester *model.User, channel *model.Channel, notice string) *model.MessageAttachment {
	att := p.webhookApprovalAttachment(req, requester, channel)
	att.Color = "#D24B4E"
	att.Fields = append([]*model.MessageAttachmentField{
		{Title: "⚠️ Action needed", Value: notice, Short: false},
	}, att.Fields...)
	return att
}

func (p *Plugin) webhookApprovalActions(requestID, siteURL string) []*model.PostAction {
	return []*model.PostAction{
		{
			Id: "approve", Name: approveButtonLabel, Type: model.PostActionTypeButton, Style: "primary",
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
		IntroductionText: "Incoming webhooks let external services post messages into a Mattermost channel. Requires a security approval and a system approval.",
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
	if appErr := p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeIncomingWebhookDialog),
		Dialog:    dialog,
	}); appErr != nil {
		return appErr
	}
	return nil
}

// openOutgoingWebhookDialog opens the request dialog for an outgoing webhook.
func (p *Plugin) openOutgoingWebhookDialog(triggerID string) error {
	dialog := model.Dialog{
		CallbackId:       "outgoing_webhook_request",
		Title:            "Request an Outgoing Webhook",
		IntroductionText: "Outgoing webhooks send channel messages to an external service. Requires a security approval and a system approval.",
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
	if appErr := p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeOutgoingWebhookDialog),
		Dialog:    dialog,
	}); appErr != nil {
		return appErr
	}
	return nil
}
