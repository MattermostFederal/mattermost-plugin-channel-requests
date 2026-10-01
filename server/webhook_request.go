package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	// kvWebhookRequestPrefix namespaces pending webhook requests in the KV store.
	kvWebhookRequestPrefix = "webhook_request_"

	// webhookDialogCallbackID identifies submissions from the webhook dialog.
	webhookDialogCallbackID = "webhook_request"

	// fieldWebhookName / fieldWebhookDescription are the webhook dialog elements.
	fieldWebhookName        = "webhook_name"
	fieldWebhookDescription = "webhook_description"
)

// webhookRequest is a pending request for an incoming webhook on a channel. Like
// bot-token requests, it uses the two-step approval engine, and the resulting
// URL is DM'd privately to the requester — never posted in the approval channel.
type webhookRequest struct {
	ID          string `json:"id"`
	RequesterID string `json:"requester_id"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	twoStepState
}

// webhookRequestInput is the normalized input from the entry point.
type webhookRequestInput struct {
	RequesterID string
	ChannelID   string
	DisplayName string
	Description string
}

func validateWebhookInput(in webhookRequestInput) error {
	if strings.TrimSpace(in.ChannelID) == "" {
		return errors.New("a channel is required")
	}
	if strings.TrimSpace(in.DisplayName) == "" {
		return errors.New("a webhook name is required")
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.DisplayName)) > maxDisplayNameLen {
		return errors.Errorf("webhook name must be %d characters or fewer", maxDisplayNameLen)
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Description)) > maxPurposeLen {
		return errors.Errorf("description must be %d characters or fewer", maxPurposeLen)
	}
	return nil
}

// submitWebhookRequest validates and either creates the webhook immediately
// (System Admins only) or stores a pending two-step request and posts it for
// approval. Like bot tokens, the auto-approve list is NOT honored — two-step
// requests always require a real security sign-off.
func (p *Plugin) submitWebhookRequest(in webhookRequestInput) (string, error) {
	config := p.getConfiguration()

	if err := validateWebhookInput(in); err != nil {
		return "", err
	}

	requester, appErr := p.API.GetUser(in.RequesterID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load requesting user")
	}

	channel, appErr := p.API.GetChannel(in.ChannelID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load channel")
	}

	// The requester must belong to the channel they want a webhook for
	// (System Admins are exempt).
	if !requester.IsSystemAdmin() {
		if _, appErr := p.API.GetChannelMember(in.ChannelID, in.RequesterID); appErr != nil {
			return "", errors.New("you must be a member of the channel to request a webhook for it")
		}
	}

	req := &webhookRequest{
		ID:          model.NewId(),
		RequesterID: in.RequesterID,
		ChannelID:   in.ChannelID,
		ChannelName: channel.Name,
		DisplayName: strings.TrimSpace(in.DisplayName),
		Description: strings.TrimSpace(in.Description),
	}

	if requester.IsSystemAdmin() {
		url, err := p.createIncomingWebhookForRequest(req)
		if err != nil {
			return "", err
		}
		p.deliverWebhookURL(req.RequesterID, channel.Name, url)
		p.logAudit(config, fmt.Sprintf("WEBHOOK CREATED: @%s created an incoming webhook for ~%s directly (System Admin)",
			requester.Username, channel.Name))
		return "Incoming webhook created. The URL has been sent to you in a direct message.", nil
	}

	if err := p.storeWebhookRequest(req); err != nil {
		return "", err
	}

	if err := p.postWebhookApprovalRequest(req, requester); err != nil {
		_ = p.API.KVDelete(kvWebhookRequestPrefix + req.ID)
		return "", err
	}

	return "Your webhook request has been submitted. It requires approval from a security approver and a system approver; the URL will be sent to you privately once issued.", nil
}

// createIncomingWebhookForRequest creates the incoming webhook via the REST API
// (there is no plugin API for it) as the plugin bot, and returns the full hook
// URL. The bot is added to the channel first so it may create a hook there.
func (p *Plugin) createIncomingWebhookForRequest(req *webhookRequest) (string, error) {
	// The bot must be a member of the channel to own a hook there — and it
	// can't be added to the channel until it's on the channel's TEAM. Without
	// team membership the add fails ("no team member found") and the hook
	// creation is denied (manage_own_incoming_webhooks is a team-scoped
	// permission), which is the usual cause of a webhook approval that
	// "does nothing". Join the team first, then the channel.
	if channel, appErr := p.API.GetChannel(req.ChannelID); appErr != nil {
		p.API.LogWarn("failed to load channel for webhook creation", "channel_id", req.ChannelID, "error", appErr.Error())
	} else {
		if _, appErr := p.API.CreateTeamMember(channel.TeamId, p.botUserID); appErr != nil {
			p.API.LogWarn("failed to add bot to team for webhook creation", "team_id", channel.TeamId, "error", appErr.Error())
		}
		// Promote the bot to Team Admin on this team so it holds
		// manage_own_incoming_webhooks there. Incoming-webhook creation is a
		// team-scoped permission that a plain member lacks on servers where
		// integrations are restricted to admins (the common default) — which
		// otherwise 403s the REST call and makes the approval "do nothing".
		// Bounded to teams where a webhook was actually approved.
		if _, appErr := p.API.UpdateTeamMemberRoles(channel.TeamId, p.botUserID, teamAdminRoleString); appErr != nil {
			p.API.LogWarn("failed to grant bot team-admin for webhook creation", "team_id", channel.TeamId, "error", appErr.Error())
		}
	}

	if _, appErr := p.API.AddChannelMember(req.ChannelID, p.botUserID); appErr != nil {
		p.API.LogWarn("failed to add bot to channel for webhook creation", "channel_id", req.ChannelID, "error", appErr.Error())
	}

	client, err := p.restClient()
	if err != nil {
		return "", err
	}

	hook, _, err := client.CreateIncomingWebhook(context.Background(), &model.IncomingWebhook{
		ChannelId:   req.ChannelID,
		DisplayName: req.DisplayName,
		Description: req.Description,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to create incoming webhook (check that incoming webhooks are enabled and the channel-request bot may manage them)")
	}

	return fmt.Sprintf("%s/hooks/%s", strings.TrimRight(p.siteURL(), "/"), hook.Id), nil
}

// deliverWebhookURL DMs the webhook URL to the requester ONLY — never to the
// approval channel.
func (p *Plugin) deliverWebhookURL(requesterID, channelName, url string) {
	p.notifyRequester(requesterID, fmt.Sprintf(
		"✅ Your incoming webhook for ~%s was approved.\n\n**Webhook URL:** `%s`\n\n⚠️ Treat this URL like a secret — anyone with it can post to the channel.",
		channelName, url,
	))
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

func (p *Plugin) postWebhookApprovalRequest(req *webhookRequest, requester *model.User) error {
	return p.postApprovalAttachment(
		p.webhookApprovalAttachment(req, requester),
		"@channel — an incoming webhook request needs review (requires a security approval and a system approval).",
	)
}

func (p *Plugin) webhookApprovalAttachment(req *webhookRequest, requester *model.User) *model.MessageAttachment {
	fields := []*model.MessageAttachmentField{
		{Title: "Requested by", Value: fmt.Sprintf("@%s", requester.Username), Short: true},
		{Title: "Channel", Value: "~" + req.ChannelName, Short: true},
		{Title: "Webhook name", Value: req.DisplayName, Short: true},
	}
	if req.Description != "" {
		fields = append(fields, &model.MessageAttachmentField{Title: "Description", Value: req.Description, Short: false})
	}
	fields = append(fields, &model.MessageAttachmentField{Title: "Approvals", Value: p.twoStepStatusValue(req.twoStepState), Short: false})

	siteURL := "/plugins/" + manifest.Id
	return &model.MessageAttachment{
		Title:   "Incoming webhook request",
		Color:   "#0058CC",
		Fields:  fields,
		Actions: p.webhookApprovalActions(req.ID, siteURL, req.twoStepState),
	}
}

// webhookApprovalAttachmentWithNotice is webhookApprovalAttachment plus a
// visible warning banner, used to repaint the card when a final-approval
// attempt failed to create the webhook. Buttons are preserved so an approver
// can retry or deny.
func (p *Plugin) webhookApprovalAttachmentWithNotice(req *webhookRequest, requester *model.User, notice string) *model.MessageAttachment {
	att := p.webhookApprovalAttachment(req, requester)
	att.Color = "#D24B4E"
	att.Fields = append([]*model.MessageAttachmentField{
		{Title: "⚠️ Action needed", Value: notice, Short: false},
	}, att.Fields...)
	return att
}

func (p *Plugin) webhookApprovalActions(requestID, siteURL string, _ twoStepState) []*model.PostAction {
	return []*model.PostAction{
		{
			Id:    "approve",
			Name:  approveButtonLabel,
			Type:  model.PostActionTypeButton,
			Style: "primary",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeApproveWebhook,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
		{
			Id:    "deny",
			Name:  "Deny",
			Type:  model.PostActionTypeButton,
			Style: "danger",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeDenyWebhook,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
	}
}
