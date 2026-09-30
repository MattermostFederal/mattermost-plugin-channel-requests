package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	// kvBotTokenRequestPrefix namespaces pending bot-token requests in the KV store.
	kvBotTokenRequestPrefix = "bot_token_request_"

	// botTokenDialogCallbackID identifies submissions from the bot-token dialog.
	botTokenDialogCallbackID = "bot_token_request"

	// fieldUsername / fieldBotDescription are the bot-token dialog elements.
	fieldUsername       = "username"
	fieldBotDescription = "bot_description"

	// maxBotDescriptionLen bounds the description server-side (bots allow 1024).
	maxBotDescriptionLen = 1024
)

// botTokenRequest is a pending request for a bot account + access token. It uses
// the two-step approval engine (security + system) — the token is only issued
// once both pools sign off, and is DM'd privately to the requester, never posted
// in the approval channel.
type botTokenRequest struct {
	ID          string `json:"id"`
	RequesterID string `json:"requester_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	twoStepState
}

// botTokenRequestInput is the normalized input from either entry point.
type botTokenRequestInput struct {
	RequesterID string
	Username    string
	DisplayName string
	Description string
}

// validateBotTokenInput checks the caller-supplied fields (no API calls).
func validateBotTokenInput(in botTokenRequestInput) error {
	username := strings.ToLower(strings.TrimSpace(in.Username))
	if username == "" {
		return errors.New("a bot username is required")
	}
	if !model.IsValidUsername(username) {
		return errors.New("bot username must be lowercase letters, numbers, and . - _ (3-22 characters)")
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.DisplayName)) > maxDisplayNameLen {
		return errors.Errorf("display name must be %d characters or fewer", maxDisplayNameLen)
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Description)) > maxBotDescriptionLen {
		return errors.Errorf("description must be %d characters or fewer", maxBotDescriptionLen)
	}
	return nil
}

// submitBotTokenRequest validates and either issues the bot+token immediately
// (System Admins only — they can create bots directly anyway, so gating them
// adds no security) or stores a pending two-step request and posts it for
// approval. The auto-approve list is deliberately NOT honored here: two-step
// requests must always get a real security sign-off.
func (p *Plugin) submitBotTokenRequest(in botTokenRequestInput) (string, error) {
	config := p.getConfiguration()

	if err := validateBotTokenInput(in); err != nil {
		return "", err
	}

	requester, appErr := p.API.GetUser(in.RequesterID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load requesting user")
	}

	req := &botTokenRequest{
		ID:          model.NewId(),
		RequesterID: in.RequesterID,
		Username:    strings.ToLower(strings.TrimSpace(in.Username)),
		DisplayName: strings.TrimSpace(in.DisplayName),
		Description: strings.TrimSpace(in.Description),
	}

	if requester.IsSystemAdmin() {
		token, botUsername, err := p.createBotTokenForRequest(req)
		if err != nil {
			return "", err
		}
		p.deliverBotToken(req.RequesterID, botUsername, token)
		p.logAudit(config, fmt.Sprintf("BOT TOKEN CREATED: @%s created bot @%s directly (System Admin)",
			requester.Username, botUsername))
		return "Bot created. The access token has been sent to you in a direct message.", nil
	}

	if err := p.storeBotTokenRequest(req); err != nil {
		return "", err
	}

	if err := p.postBotTokenApprovalRequest(req, requester); err != nil {
		_ = p.API.KVDelete(kvBotTokenRequestPrefix + req.ID)
		return "", err
	}

	return "Your bot token request has been submitted. It requires approval from a security approver and a system approver; you'll be notified once it's issued.", nil
}

// createBotTokenForRequest creates the bot account and issues an access token,
// returning the raw token and the created bot's username.
func (p *Plugin) createBotTokenForRequest(req *botTokenRequest) (token, botUsername string, err error) {
	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Username
	}
	bot, appErr := p.API.CreateBot(&model.Bot{
		Username:    req.Username,
		DisplayName: displayName,
		Description: req.Description,
	})
	if appErr != nil {
		return "", "", errors.Wrap(appErr, "failed to create bot")
	}

	accessToken, appErr := p.API.CreateUserAccessToken(&model.UserAccessToken{
		UserId:      bot.UserId,
		Description: fmt.Sprintf("Requested via channel-requests by user %s", req.RequesterID),
	})
	if appErr != nil {
		return "", "", errors.Wrap(appErr, "failed to create access token")
	}

	return accessToken.Token, bot.Username, nil
}

// deliverBotToken DMs the freshly-created token to the requester ONLY. The token
// is never posted to the approval channel.
func (p *Plugin) deliverBotToken(requesterID, botUsername, token string) {
	p.notifyRequester(requesterID, fmt.Sprintf(
		"✅ Your bot token request was approved. Bot **@%s** was created.\n\n**Access token:** `%s`\n\n⚠️ Store this now — it won't be shown again.",
		botUsername, token,
	))
}

func (p *Plugin) storeBotTokenRequest(req *botTokenRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return errors.Wrap(err, "failed to marshal bot-token request")
	}
	if appErr := p.API.KVSet(kvBotTokenRequestPrefix+req.ID, data); appErr != nil {
		return errors.Wrap(appErr, "failed to store bot-token request")
	}
	return nil
}

func (p *Plugin) loadBotTokenRequest(id string) (*botTokenRequest, []byte, error) {
	data, appErr := p.API.KVGet(kvBotTokenRequestPrefix + id)
	if appErr != nil {
		return nil, nil, errors.Wrap(appErr, "failed to load bot-token request")
	}
	if data == nil {
		return nil, nil, nil
	}
	var req botTokenRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, nil, errors.Wrap(err, "failed to unmarshal bot-token request")
	}
	return &req, data, nil
}

func (p *Plugin) postBotTokenApprovalRequest(req *botTokenRequest, requester *model.User) error {
	return p.postApprovalAttachment(
		p.botTokenApprovalAttachment(req, requester),
		"@channel — a bot token request needs review (requires a security approval and a system approval).",
	)
}

// botTokenApprovalAttachment builds the approval card, including the two-step
// status and a single Approve button labeled with the step still needed.
func (p *Plugin) botTokenApprovalAttachment(req *botTokenRequest, requester *model.User) *model.MessageAttachment {
	fields := []*model.MessageAttachmentField{
		{Title: "Requested by", Value: fmt.Sprintf("@%s", requester.Username), Short: true},
		{Title: "Bot username", Value: "@" + req.Username, Short: true},
	}
	if req.DisplayName != "" {
		fields = append(fields, &model.MessageAttachmentField{Title: "Display name", Value: req.DisplayName, Short: true})
	}
	if req.Description != "" {
		fields = append(fields, &model.MessageAttachmentField{Title: "Description", Value: req.Description, Short: false})
	}
	fields = append(fields, &model.MessageAttachmentField{Title: "Approvals", Value: p.twoStepStatusValue(req.twoStepState), Short: false})

	siteURL := "/plugins/" + manifest.Id
	return &model.MessageAttachment{
		Title:   "Bot token request",
		Color:   "#0058CC",
		Fields:  fields,
		Actions: p.botTokenApprovalActions(req.ID, siteURL, req.twoStepState),
	}
}

func (p *Plugin) botTokenApprovalActions(requestID, siteURL string, state twoStepState) []*model.PostAction {
	return []*model.PostAction{
		{
			Id:    "approve",
			Name:  stepButtonName(state),
			Type:  model.PostActionTypeButton,
			Style: "primary",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeApproveBotToken,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
		{
			Id:    "deny",
			Name:  "Deny",
			Type:  model.PostActionTypeButton,
			Style: "danger",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeDenyBotToken,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
	}
}
