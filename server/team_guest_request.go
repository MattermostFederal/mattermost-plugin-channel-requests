package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	kvTeamGuestRequestPrefix  = "team_guest_request_"
	teamGuestDialogCallbackID = "team_guest_request"
)

// teamGuestRequest is a pending request for a guest invite to a team.
// Routes to mattermost-team-requests; only System Admins may approve.
// On approve: always return a shareable team invite link; optionally email
// guests via InviteGuestsToTeam when Email is set.
type teamGuestRequest struct {
	ID          string   `json:"id"`
	RequesterID string   `json:"requester_id"`
	TeamID      string   `json:"team_id"`
	Email       string   `json:"email,omitempty"`
	ChannelIDs  []string `json:"channel_ids"`
	Reason      string   `json:"reason,omitempty"`

	ApprovalPostID    string `json:"approval_post_id,omitempty"`
	ApprovalChannelID string `json:"approval_channel_id,omitempty"`
	DMRootPostID      string `json:"dm_root_post_id,omitempty"`
	DMChannelID       string `json:"dm_channel_id,omitempty"`
}

func (p *Plugin) submitTeamGuestRequest(requesterID, teamID, email string, channelIDs []string, reason string, forceQueue bool) (string, error) {
	if !p.getConfiguration().RequestEnabled(requestTypeTeamGuest) {
		return "", errors.New(teamGuestRequestsDisabledMsg)
	}
	if strings.TrimSpace(teamID) == "" {
		return "", errors.New("a team is required")
	}
	channels := dedupeNonEmpty(channelIDs)
	if len(channels) == 0 {
		return "", errors.New("pick at least one channel the guest should access")
	}

	email = strings.TrimSpace(strings.ToLower(email))
	if email != "" && !strings.Contains(email, "@") {
		return "", errors.New("guest email looks invalid")
	}

	team, appErr := p.API.GetTeam(teamID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load team")
	}

	requester, appErr := p.API.GetUser(requesterID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load requesting user")
	}

	// Guest invites are scoped to a team the requester already belongs to
	// (System Admins may invite for any team). Channels must belong to that team.
	if !requester.IsSystemAdmin() {
		member, memberErr := p.API.GetTeamMember(teamID, requesterID)
		if memberErr != nil || member == nil || member.DeleteAt != 0 {
			return "", errors.New("you must be a member of the team to request a guest invite")
		}
	}
	for _, chID := range channels {
		ch, chErr := p.API.GetChannel(chID)
		if chErr != nil || ch == nil || ch.TeamId != teamID {
			return "", errors.New("all guest channels must belong to the selected team")
		}
	}

	req := &teamGuestRequest{
		ID:          model.NewId(),
		RequesterID: requesterID,
		TeamID:      teamID,
		Email:       email,
		ChannelIDs:  channels,
		Reason:      strings.TrimSpace(reason),
	}

	config := p.getConfiguration()
	if !forceQueue && requester.IsSystemAdmin() {
		link, inviteErr := p.fulfillTeamGuestRequest(req, requester.Id)
		if inviteErr != nil {
			return "", inviteErr
		}
		p.logAudit(config, fmt.Sprintf("TEAM GUEST: @%s invited guest to team %s (email=%q)",
			requester.Username, team.DisplayName, email))
		return fmt.Sprintf("Guest invite ready for **%s**.\n\nShareable link: %s", team.DisplayName, link), nil
	}

	if err := p.storeTeamGuestRequest(req); err != nil {
		return "", err
	}

	approvalPostID, approvalChannelID, err := p.postTeamGuestApprovalRequest(req, requester, team)
	if err != nil {
		_ = p.API.KVDelete(kvTeamGuestRequestPrefix + req.ID)
		return "", err
	}
	req.ApprovalPostID = approvalPostID
	req.ApprovalChannelID = approvalChannelID

	dmRootPostID, dmChannelID := p.sendTeamGuestTicketCreatedDM(req, requester, team)
	req.DMRootPostID = dmRootPostID
	req.DMChannelID = dmChannelID

	if storeErr := p.storeTeamGuestRequest(req); storeErr != nil {
		p.API.LogWarn("failed to re-store team guest request with post IDs", "request_id", req.ID, "error", storeErr.Error())
	} else {
		p.storeTicketLookups(threadAnchor{
			ApprovalPostID:    approvalPostID,
			ApprovalChannelID: approvalChannelID,
			DMRootPostID:      dmRootPostID,
			DMChannelID:       dmChannelID,
			Kind:              requestTypeTeamGuest,
		})
	}

	return "Your guest invite request has been submitted for approval. You'll be notified once a System Admin responds.", nil
}

func (p *Plugin) sendTeamGuestTicketCreatedDM(req *teamGuestRequest, requester *model.User, team *model.Team) (dmRootPostID, dmChannelID string) {
	dm, appErr := p.API.GetDirectChannel(req.RequesterID, p.botUserID)
	if appErr != nil {
		p.API.LogWarn("failed to open DM for team guest ticket", "user_id", req.RequesterID, "error", appErr.Error())
		return "", ""
	}

	attachment := p.teamGuestApprovalAttachment(req, requester, team)
	attachment.Actions = nil
	attachment.Color = colorPending

	post := &model.Post{
		UserId:    p.botUserID,
		ChannelId: dm.Id,
		Message:   "Your guest invite request has been submitted for approval. Reply to this thread to add context — your reply will be forwarded to the reviewers.",
	}
	model.ParseMessageAttachment(post, []*model.MessageAttachment{attachment})

	created, appErr := p.API.CreatePost(post)
	if appErr != nil {
		p.API.LogWarn("failed to send team guest ticket-created DM", "user_id", req.RequesterID, "error", appErr.Error())
		return "", ""
	}
	return created.Id, dm.Id
}

// teamInviteLink builds the shareable signup URL from the team's InviteId.
func (p *Plugin) teamInviteLink(team *model.Team) (string, error) {
	if team == nil || team.InviteId == "" {
		return "", errors.New("team invite ID is not available")
	}
	base := strings.TrimRight(p.siteURL(), "/")
	if base == "" {
		return "", errors.New("SiteURL is not configured")
	}
	return fmt.Sprintf("%s/signup_user_complete/?id=%s", base, team.InviteId), nil
}

// fulfillTeamGuestRequest returns the shareable invite link and optionally
// emails a guest invitation using the acting admin's session.
func (p *Plugin) fulfillTeamGuestRequest(req *teamGuestRequest, actingAdminID string) (inviteLink string, err error) {
	team, appErr := p.API.GetTeam(req.TeamID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load team")
	}
	link, linkErr := p.teamInviteLink(team)
	if linkErr != nil {
		return "", linkErr
	}

	if req.Email == "" {
		return link, nil
	}

	adminSession, sessErr := p.API.CreateSession(&model.Session{UserId: actingAdminID})
	if sessErr != nil {
		return link, fmt.Errorf("invite link ready, but could not send guest email (%s)", sessErr.Error())
	}
	defer p.API.RevokeSession(adminSession.Id)

	client := model.NewAPIv4Client(p.internalBaseURL())
	client.SetToken(adminSession.Token)
	if _, inviteErr := client.InviteGuestsToTeam(context.Background(), req.TeamID, []string{req.Email}, req.ChannelIDs, req.Reason); inviteErr != nil {
		return link, fmt.Errorf("invite link ready, but guest email invite failed (%s). Guest Accounts may be disabled", inviteErr.Error())
	}
	return link, nil
}

func (p *Plugin) postTeamGuestApprovalRequest(req *teamGuestRequest, requester *model.User, team *model.Team) (postID, channelID string, err error) {
	ch := p.ensureTeamCreationApprovalChannel()
	header := p.systemAdminMentions() + " — a guest invite request needs System Admin review."
	return p.postApprovalAttachment(
		p.teamGuestApprovalAttachment(req, requester, team),
		header,
		ch,
	)
}

func (p *Plugin) teamGuestApprovalAttachment(req *teamGuestRequest, requester *model.User, team *model.Team) *model.MessageAttachment {
	emailValue := "(none — shareable link only)"
	if req.Email != "" {
		emailValue = req.Email
	}
	fields := []*model.MessageAttachmentField{
		{Title: "Requested by", Value: fmt.Sprintf("@%s", requester.Username), Short: true},
		{Title: "Team", Value: team.DisplayName, Short: true},
		{Title: "Guest email", Value: emailValue, Short: true},
		{Title: "Channels", Value: strings.Join(req.ChannelIDs, ", "), Short: false},
	}
	if req.Reason != "" {
		fields = append(fields, &model.MessageAttachmentField{Title: "Message / reason", Value: req.Reason, Short: false})
	}
	siteURL := "/plugins/" + manifest.Id
	return &model.MessageAttachment{
		Title:   "Team guest invite request",
		Color:   "#F4A221",
		Fields:  fields,
		Actions: p.teamGuestApprovalActions(req.ID, siteURL),
	}
}

func (p *Plugin) teamGuestApprovalActions(requestID, siteURL string) []*model.PostAction {
	return []*model.PostAction{
		{
			Id: "approve", Name: "Approve", Type: model.PostActionTypeButton, Style: "primary",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeApproveTeamGuest,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
		{
			Id: "deny", Name: "Deny", Type: model.PostActionTypeButton, Style: "danger",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeDenyTeamGuest,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
	}
}

func (p *Plugin) storeTeamGuestRequest(req *teamGuestRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return errors.Wrap(err, "failed to marshal team guest request")
	}
	if appErr := p.API.KVSet(kvTeamGuestRequestPrefix+req.ID, data); appErr != nil {
		return errors.Wrap(appErr, "failed to store team guest request")
	}
	return nil
}

func (p *Plugin) loadTeamGuestRequest(id string) (*teamGuestRequest, []byte, error) {
	data, appErr := p.API.KVGet(kvTeamGuestRequestPrefix + id)
	if appErr != nil {
		return nil, nil, errors.Wrap(appErr, "failed to load team guest request")
	}
	if data == nil {
		return nil, nil, nil
	}
	var req teamGuestRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, nil, errors.Wrap(err, "failed to unmarshal team guest request")
	}
	return &req, data, nil
}

func (p *Plugin) openTeamGuestRequestDialog(triggerID, teamID string) error {
	teamName := teamID
	if t, appErr := p.API.GetTeam(teamID); appErr == nil {
		teamName = t.DisplayName
	}

	dialog := model.Dialog{
		CallbackId: teamGuestDialogCallbackID,
		Title:      "Request Guest Invite",
		IntroductionText: fmt.Sprintf(
			"Request a guest invite for **%s**. Provide an email to have Mattermost send the invite, or leave it blank and forward the shareable link yourself after approval. Guest Accounts must be enabled.",
			teamName,
		),
		SubmitLabel: "Submit request",
		State:       teamID,
		Elements: []model.DialogElement{
			{
				DisplayName: "Guest email (optional)",
				Name:        "email",
				Type:        "text",
				Optional:    true,
				HelpText:    "If set, Mattermost emails a guest invitation on approve. If blank, you get a shareable team invite link to send yourself.",
			},
			{
				DisplayName: "Channels",
				Name:        "guest_channels",
				Type:        "select",
				DataSource:  "channels",
				MultiSelect: true,
				Optional:    false,
				HelpText:    "Channels the guest should access (required for guest invites).",
			},
			{
				DisplayName: "Message / reason",
				Name:        "reason",
				Type:        "textarea",
				Optional:    true,
				HelpText:    "Optional note included with the invite email and shown to reviewers.",
			},
		},
	}
	return p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeTeamGuestDialog),
		Dialog:    dialog,
	})
}
