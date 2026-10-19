package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	kvTeamMemberRequestPrefix  = "team_member_request_"
	teamMemberDialogCallbackID = "team_member_request"
)

// teamMemberRequest is a pending request to add one or more existing users to a
// team. Routes to mattermost-team-requests; only System Admins may approve.
type teamMemberRequest struct {
	ID          string   `json:"id"`
	RequesterID string   `json:"requester_id"`
	TeamID      string   `json:"team_id"`
	NomineeIDs  []string `json:"nominee_ids"`
	Reason      string   `json:"reason,omitempty"`

	ApprovalPostID    string `json:"approval_post_id,omitempty"`
	ApprovalChannelID string `json:"approval_channel_id,omitempty"`
	DMRootPostID      string `json:"dm_root_post_id,omitempty"`
	DMChannelID       string `json:"dm_channel_id,omitempty"`
}

func (p *Plugin) submitTeamMemberRequest(requesterID, teamID string, nomineeIDs []string, reason string, forceQueue bool) (string, error) {
	if !p.getConfiguration().RequestEnabled(requestTypeTeamMember) {
		return "", errors.New(teamMemberRequestsDisabledMsg)
	}
	if strings.TrimSpace(teamID) == "" {
		return "", errors.New("a team is required")
	}
	nominees := dedupeNonEmpty(nomineeIDs)
	if len(nominees) == 0 {
		return "", errors.New("pick at least one person to add to the team")
	}

	team, appErr := p.API.GetTeam(teamID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load team")
	}

	requester, appErr := p.API.GetUser(requesterID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load requesting user")
	}

	req := &teamMemberRequest{
		ID:          model.NewId(),
		RequesterID: requesterID,
		TeamID:      teamID,
		NomineeIDs:  nominees,
		Reason:      strings.TrimSpace(reason),
	}

	config := p.getConfiguration()
	// Any authenticated user may nominate people onto any team; System Admins
	// approve. Membership of the requester on the target team is intentionally
	// not required — governance is the approval card, not submit-side RBAC.
	if !forceQueue && requester.IsSystemAdmin() {
		p.addTeamMembers(req)
		p.logAudit(config, fmt.Sprintf("TEAM MEMBER: @%s added %s to team %s",
			requester.Username, p.mentionList(req.NomineeIDs), team.DisplayName))
		return fmt.Sprintf("Added %s to **%s**.", p.mentionList(req.NomineeIDs), team.DisplayName), nil
	}

	if err := p.storeTeamMemberRequest(req); err != nil {
		return "", err
	}

	approvalPostID, approvalChannelID, err := p.postTeamMemberApprovalRequest(req, requester, team)
	if err != nil {
		_ = p.API.KVDelete(kvTeamMemberRequestPrefix + req.ID)
		return "", err
	}
	req.ApprovalPostID = approvalPostID
	req.ApprovalChannelID = approvalChannelID

	dmRootPostID, dmChannelID := p.sendTeamMemberTicketCreatedDM(req, requester, team)
	req.DMRootPostID = dmRootPostID
	req.DMChannelID = dmChannelID

	if storeErr := p.storeTeamMemberRequest(req); storeErr != nil {
		p.API.LogWarn("failed to re-store team member request with post IDs", "request_id", req.ID, "error", storeErr.Error())
	} else {
		p.storeTicketLookups(threadAnchor{
			ApprovalPostID:    approvalPostID,
			ApprovalChannelID: approvalChannelID,
			DMRootPostID:      dmRootPostID,
			DMChannelID:       dmChannelID,
			Kind:              requestTypeTeamMember,
		})
	}

	return "Your team member request has been submitted for approval. You'll be notified once a System Admin responds.", nil
}

func (p *Plugin) sendTeamMemberTicketCreatedDM(req *teamMemberRequest, requester *model.User, team *model.Team) (dmRootPostID, dmChannelID string) {
	dm, appErr := p.API.GetDirectChannel(req.RequesterID, p.botUserID)
	if appErr != nil {
		p.API.LogWarn("failed to open DM for team member ticket", "user_id", req.RequesterID, "error", appErr.Error())
		return "", ""
	}

	attachment := p.teamMemberApprovalAttachment(req, requester, team)
	attachment.Actions = nil
	attachment.Color = colorPending

	post := &model.Post{
		UserId:    p.botUserID,
		ChannelId: dm.Id,
		Message:   "Your team member request has been submitted for approval. Reply to this thread to add context — your reply will be forwarded to the reviewers.",
	}
	model.ParseMessageAttachment(post, []*model.MessageAttachment{attachment})

	created, appErr := p.API.CreatePost(post)
	if appErr != nil {
		p.API.LogWarn("failed to send team member ticket-created DM", "user_id", req.RequesterID, "error", appErr.Error())
		return "", ""
	}
	return created.Id, dm.Id
}

func (p *Plugin) addTeamMembers(req *teamMemberRequest) {
	for _, userID := range req.NomineeIDs {
		if userID == "" {
			continue
		}
		if _, appErr := p.API.CreateTeamMember(req.TeamID, userID); appErr != nil {
			p.API.LogWarn("failed to add user to team", "team_id", req.TeamID, "user_id", userID, "error", appErr.Error())
		}
	}
}

func (p *Plugin) postTeamMemberApprovalRequest(req *teamMemberRequest, requester *model.User, team *model.Team) (postID, channelID string, err error) {
	ch := p.ensureTeamCreationApprovalChannel()
	header := p.systemAdminMentions() + " — a team member request needs System Admin review."
	return p.postApprovalAttachment(
		p.teamMemberApprovalAttachment(req, requester, team),
		header,
		ch,
	)
}

func (p *Plugin) teamMemberApprovalAttachment(req *teamMemberRequest, requester *model.User, team *model.Team) *model.MessageAttachment {
	fields := []*model.MessageAttachmentField{
		{Title: "Requested by", Value: fmt.Sprintf("@%s", requester.Username), Short: true},
		{Title: "Team", Value: team.DisplayName, Short: true},
		{Title: "Users to add", Value: p.mentionList(req.NomineeIDs), Short: false},
	}
	if req.Reason != "" {
		fields = append(fields, &model.MessageAttachmentField{Title: "Reason", Value: req.Reason, Short: false})
	}
	siteURL := "/plugins/" + manifest.Id
	return &model.MessageAttachment{
		Title:   "Team member request",
		Color:   "#F4A221",
		Fields:  fields,
		Actions: p.teamMemberApprovalActions(req.ID, siteURL),
	}
}

func (p *Plugin) teamMemberApprovalActions(requestID, siteURL string) []*model.PostAction {
	return []*model.PostAction{
		{
			Id: "approve", Name: "Approve", Type: model.PostActionTypeButton, Style: "primary",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeApproveTeamMember,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
		{
			Id: "deny", Name: "Deny", Type: model.PostActionTypeButton, Style: "danger",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeDenyTeamMember,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
	}
}

func (p *Plugin) storeTeamMemberRequest(req *teamMemberRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return errors.Wrap(err, "failed to marshal team member request")
	}
	if appErr := p.API.KVSet(kvTeamMemberRequestPrefix+req.ID, data); appErr != nil {
		return errors.Wrap(appErr, "failed to store team member request")
	}
	return nil
}

func (p *Plugin) loadTeamMemberRequest(id string) (*teamMemberRequest, []byte, error) {
	data, appErr := p.API.KVGet(kvTeamMemberRequestPrefix + id)
	if appErr != nil {
		return nil, nil, errors.Wrap(appErr, "failed to load team member request")
	}
	if data == nil {
		return nil, nil, nil
	}
	var req teamMemberRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, nil, errors.Wrap(err, "failed to unmarshal team member request")
	}
	return &req, data, nil
}

func (p *Plugin) openTeamMemberRequestDialog(triggerID, teamID string) error {
	teamName := teamID
	if t, appErr := p.API.GetTeam(teamID); appErr == nil {
		teamName = t.DisplayName
	}

	dialog := model.Dialog{
		CallbackId: teamMemberDialogCallbackID,
		Title:      "Request Team Members",
		IntroductionText: fmt.Sprintf(
			"Nominate existing Mattermost users to be added to **%s**. System Admin approval is required.",
			teamName,
		),
		SubmitLabel: "Submit request",
		State:       teamID,
		Elements: []model.DialogElement{
			{
				DisplayName: "Users to add",
				Name:        fieldMembers,
				Type:        "select",
				DataSource:  "users",
				MultiSelect: true,
				Optional:    false,
				HelpText:    "Select one or more existing users to add to this team.",
			},
			{
				DisplayName: "Reason",
				Name:        "reason",
				Type:        "textarea",
				Optional:    true,
				HelpText:    "Optional context for reviewers.",
			},
		},
	}
	return p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeTeamMemberDialog),
		Dialog:    dialog,
	})
}
