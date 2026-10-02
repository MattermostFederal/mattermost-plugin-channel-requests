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
		return errors.New("A channel is required.")
	}
	if strings.TrimSpace(in.DisplayName) == "" {
		return newFieldError(fieldWebhookName, "A webhook name is required.")
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.DisplayName)) > maxDisplayNameLen {
		return newFieldError(fieldWebhookName, fmt.Sprintf("Webhook name must be %d characters or fewer.", maxDisplayNameLen))
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Description)) > maxPurposeLen {
		return newFieldError(fieldWebhookDescription, fmt.Sprintf("Description must be %d characters or fewer.", maxPurposeLen))
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
			return "", errors.New("You must be a member of the channel to request a webhook for it.")
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
		url, hookID, err := p.createIncomingWebhookForRequest(req)
		if err != nil {
			return "", err
		}
		if deliverErr := p.deliverWebhookURL(req.RequesterID, channel.Name, url); deliverErr != nil {
			// An undelivered hook URL is an unowned secret endpoint — remove it
			// and tell the requester to retry.
			if delErr := p.deleteIncomingWebhook(hookID); delErr != nil {
				p.API.LogError("failed to delete webhook after URL delivery failure", "hook_id", hookID, "error", delErr.Error())
				return "", errors.Wrap(deliverErr, "webhook created but the URL could not be delivered to you, and the webhook could not be removed automatically — ask an admin to delete it, then try again")
			}
			return "", errors.Wrap(deliverErr, "webhook created but the URL could not be delivered to you; it has been removed — please try again")
		}
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
// URL and the hook ID (so the caller can delete it if the URL can't be
// delivered). The bot is added to the channel first so it may create a hook there.
func (p *Plugin) createIncomingWebhookForRequest(req *webhookRequest) (url, hookID string, err error) {
	// Test seam: lets the delivery-failure cleanup path be unit-tested without
	// a live REST API. nil in production.
	if p.createWebhookFn != nil {
		return p.createWebhookFn(req)
	}

	// The bot must be a member of the channel to own a hook there — and it
	// can't be added to the channel until it's on the channel's TEAM. Without
	// team membership the add fails ("no team member found") and the hook
	// creation is denied (manage_own_incoming_webhooks is a team-scoped
	// permission), which is the usual cause of a webhook approval that
	// "does nothing". Join the team first, then the channel.
	var teamID string
	if channel, appErr := p.API.GetChannel(req.ChannelID); appErr != nil {
		p.API.LogWarn("failed to load channel for webhook creation", "channel_id", req.ChannelID, "error", appErr.Error())
	} else {
		teamID = channel.TeamId
		if _, appErr := p.API.CreateTeamMember(teamID, p.botUserID); appErr != nil {
			p.API.LogWarn("failed to add bot to team for webhook creation", "team_id", teamID, "error", appErr.Error())
		}
		// Promote the bot to Team Admin on this team so it holds
		// manage_own_incoming_webhooks there. Incoming-webhook creation is a
		// team-scoped permission that a plain member lacks on servers where
		// integrations are restricted to admins (the common default) — which
		// otherwise 403s the REST call and makes the approval "do nothing".
		// Bounded to teams where a webhook was actually approved.
		if _, appErr := p.API.UpdateTeamMemberRoles(teamID, p.botUserID, teamAdminRoleString); appErr != nil {
			p.API.LogWarn("failed to grant bot team-admin for webhook creation", "team_id", teamID, "error", appErr.Error())
		}
	}

	if _, appErr := p.API.AddChannelMember(req.ChannelID, p.botUserID); appErr != nil {
		p.API.LogWarn("failed to add bot to channel for webhook creation", "channel_id", req.ChannelID, "error", appErr.Error())
	}

	client, err := p.restClient()
	if err != nil {
		return "", "", err
	}

	// Idempotency guard: a prior approval attempt for THIS request may have
	// created the hook server-side even though the client observed an
	// error/timeout. Retrying would otherwise create a duplicate hook. A stable
	// per-request marker is embedded in the hook description so a retry finds and
	// reuses the existing hook instead of creating another.
	marker := webhookRequestMarker(req.ID)
	if teamID != "" {
		if existing := p.findWebhookByMarker(client, teamID, req.ChannelID, marker); existing != nil {
			return p.webhookURL(existing.Id), existing.Id, nil
		}
	}

	description := marker
	if req.Description != "" {
		description = req.Description + " " + marker
	}
	hook, _, err := client.CreateIncomingWebhook(context.Background(), &model.IncomingWebhook{
		ChannelId:   req.ChannelID,
		DisplayName: req.DisplayName,
		Description: description,
	})
	if err != nil {
		return "", "", errors.Wrap(err, "failed to create incoming webhook (check that incoming webhooks are enabled and the channel-request bot may manage them)")
	}

	return p.webhookURL(hook.Id), hook.Id, nil
}

// webhookRequestMarker is a stable token embedded in a created hook's
// description so a retry of the same request can find the hook it already
// created (idempotency) rather than making a duplicate.
func webhookRequestMarker(requestID string) string {
	return fmt.Sprintf("[channel-requests:%s]", requestID)
}

// webhookMatchesRequest reports whether an existing hook is the one created for
// this request (same channel + carrying this request's marker). Pure, so the
// matching rule is unit-testable without a live server.
func webhookMatchesRequest(hook *model.IncomingWebhook, channelID, marker string) bool {
	return hook != nil && hook.ChannelId == channelID && strings.Contains(hook.Description, marker)
}

func (p *Plugin) webhookURL(hookID string) string {
	return fmt.Sprintf("%s/hooks/%s", strings.TrimRight(p.siteURL(), "/"), hookID)
}

// findWebhookByMarker scans the team's incoming webhooks for one matching this
// request's marker. Returns nil if none is found or the listing fails (in which
// case the caller falls back to creating a new hook). Bounded to a fixed number
// of pages so a team with a huge number of hooks can't make this unbounded.
func (p *Plugin) findWebhookByMarker(client *model.Client4, teamID, channelID, marker string) *model.IncomingWebhook {
	const perPage = 100
	const maxPages = 20
	for page := 0; page < maxPages; page++ {
		hooks, _, err := client.GetIncomingWebhooksForTeam(context.Background(), teamID, page, perPage, "")
		if err != nil {
			p.API.LogWarn("failed to list incoming webhooks for idempotency check", "team_id", teamID, "error", err.Error())
			return nil
		}
		for _, h := range hooks {
			if webhookMatchesRequest(h, channelID, marker) {
				return h
			}
		}
		if len(hooks) < perPage {
			return nil // scanned every hook on the team
		}
	}
	// Hit the page cap without scanning all hooks: a prior attempt's hook could
	// lie beyond the scan window, so a duplicate is possible. Surface it rather
	// than failing silently.
	p.API.LogWarn("incoming-webhook idempotency scan hit the page cap; a duplicate hook is possible",
		"team_id", teamID, "pages_scanned", maxPages)
	return nil
}

// deleteIncomingWebhook removes a hook created by createIncomingWebhookForRequest.
// Used to clean up a hook whose URL could not be delivered to the requester, so
// no unowned secret endpoint is left live.
func (p *Plugin) deleteIncomingWebhook(hookID string) error {
	// Test seam (see createIncomingWebhookForRequest). nil in production.
	if p.deleteWebhookFn != nil {
		return p.deleteWebhookFn(hookID)
	}

	client, err := p.restClient()
	if err != nil {
		return err
	}
	if _, err := client.DeleteIncomingWebhook(context.Background(), hookID); err != nil {
		return errors.Wrap(err, "failed to delete incoming webhook")
	}
	return nil
}

// deliverWebhookURL DMs the webhook URL to the requester ONLY — never to the
// approval channel — and returns an error if delivery fails. The caller MUST
// handle a delivery failure (an undelivered URL is an unowned secret endpoint).
func (p *Plugin) deliverWebhookURL(requesterID, channelName, url string) error {
	return p.dmRequester(requesterID, fmt.Sprintf(
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
