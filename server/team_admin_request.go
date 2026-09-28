package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	kvTeamAdminRequestPrefix  = "team_admin_request_"
	teamAdminDialogCallbackID = "team_admin_request"
	// teamAdminRoles is the roles string granting Team Admin on a team.
	teamAdminRoles = "team_user team_admin"
)

// teamAdminRequest is a pending request to promote one or more users to Team
// Admin on a specific team. Because Team Admin grants broad permissions across
// the whole team, these requests always route to the root approval channel and
// can only be approved by System Admins.
type teamAdminRequest struct {
	ID          string   `json:"id"`
	RequesterID string   `json:"requester_id"`
	TeamID      string   `json:"team_id"`
	NomineeIDs  []string `json:"nominee_ids"`

	// Ticket messaging fields — set after the approval post and DM are created.
	ApprovalPostID    string `json:"approval_post_id,omitempty"`
	ApprovalChannelID string `json:"approval_channel_id,omitempty"`
	DMRootPostID      string `json:"dm_root_post_id,omitempty"`
	DMChannelID       string `json:"dm_channel_id,omitempty"`
}

// submitTeamAdminRequest validates the input and either promotes immediately
// (System Admins + auto-approve users) or stores a pending request and posts
// it to the root approval channel.
func (p *Plugin) submitTeamAdminRequest(requesterID, teamID string, nomineeIDs []string) (string, error) {
	if strings.TrimSpace(teamID) == "" {
		return "", errors.New("a team is required")
	}
	nominees := dedupeNonEmpty(nomineeIDs)
	if len(nominees) == 0 {
		return "", errors.New("pick at least one person to make a Team Admin")
	}

	team, appErr := p.API.GetTeam(teamID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load team")
	}

	requester, appErr := p.API.GetUser(requesterID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load requesting user")
	}

	// Requester must be an active member of the team they're nominating into.
	// System Admins are exempt — they govern all teams.
	if !requester.IsSystemAdmin() {
		member, appErr := p.API.GetTeamMember(teamID, requesterID)
		if appErr != nil || member == nil || member.DeleteAt != 0 {
			return "", errors.New("you must be a member of the team to request Team Admin access")
		}
	}

	req := &teamAdminRequest{
		ID:          model.NewId(),
		RequesterID: requesterID,
		TeamID:      teamID,
		NomineeIDs:  nominees,
	}

	config := p.getConfiguration()
	if requester.IsSystemAdmin() || config.AutoApproveContains(requester.Id) {
		p.promoteTeamAdmins(req)
		p.logAudit(config, fmt.Sprintf("TEAM ADMIN: @%s promoted %s to Team Admin in %s",
			requester.Username, p.mentionList(req.NomineeIDs), team.DisplayName))
		return fmt.Sprintf("Promoted %s to Team Admin in **%s**.", p.mentionList(req.NomineeIDs), team.DisplayName), nil
	}

	if err := p.storeTeamAdminRequest(req); err != nil {
		return "", err
	}

	approvalPostID, approvalChannelID, err := p.postTeamAdminApprovalRequest(req, requester, team)
	if err != nil {
		_ = p.API.KVDelete(kvTeamAdminRequestPrefix + req.ID)
		return "", err
	}
	req.ApprovalPostID = approvalPostID
	req.ApprovalChannelID = approvalChannelID

	dmRootPostID, dmChannelID := p.sendTeamAdminTicketCreatedDM(req, requester, team)
	req.DMRootPostID = dmRootPostID
	req.DMChannelID = dmChannelID

	if storeErr := p.storeTeamAdminRequest(req); storeErr != nil {
		p.API.LogWarn("failed to re-store team admin request with post IDs", "request_id", req.ID, "error", storeErr.Error())
	} else {
		p.storeTicketLookups(threadAnchor{
			ApprovalPostID:    approvalPostID,
			ApprovalChannelID: approvalChannelID,
			DMRootPostID:      dmRootPostID,
			DMChannelID:       dmChannelID,
		})
	}

	return "Your Team Admin request has been submitted for approval. You'll be notified once a System Admin responds.", nil
}

// sendTeamAdminTicketCreatedDM opens a DM and posts a card mirroring the
// Team Admin approval post (same fields, no buttons) as the root of the ticket thread.
func (p *Plugin) sendTeamAdminTicketCreatedDM(req *teamAdminRequest, requester *model.User, team *model.Team) (dmRootPostID, dmChannelID string) {
	dm, appErr := p.API.GetDirectChannel(req.RequesterID, p.botUserID)
	if appErr != nil {
		p.API.LogWarn("failed to open DM for team admin ticket notification", "user_id", req.RequesterID, "error", appErr.Error())
		return "", ""
	}

	attachment := p.teamAdminApprovalAttachment(req, requester, team)
	attachment.Actions = nil
	attachment.Color = colorPending

	post := &model.Post{
		UserId:    p.botUserID,
		ChannelId: dm.Id,
		Message:   "Your Team Admin request has been submitted for approval. Reply to this thread to add context — your reply will be forwarded to the reviewers.",
	}
	model.ParseMessageAttachment(post, []*model.MessageAttachment{attachment})

	created, appErr := p.API.CreatePost(post)
	if appErr != nil {
		p.API.LogWarn("failed to send team admin ticket-created DM", "user_id", req.RequesterID, "error", appErr.Error())
		return "", ""
	}
	return created.Id, dm.Id
}

// promoteTeamAdmins adds each nominee to the team (ensuring membership) then
// grants the team_admin scheme role.
func (p *Plugin) promoteTeamAdmins(req *teamAdminRequest) {
	for _, userID := range req.NomineeIDs {
		if userID == "" {
			continue
		}
		if _, appErr := p.API.CreateTeamMember(req.TeamID, userID); appErr != nil {
			p.API.LogWarn("failed to add nominee to team", "team_id", req.TeamID, "user_id", userID, "error", appErr.Error())
		}
		if _, appErr := p.API.UpdateTeamMemberRoles(req.TeamID, userID, teamAdminRoles); appErr != nil {
			p.API.LogWarn("failed to promote nominee to team admin", "team_id", req.TeamID, "user_id", userID, "error", appErr.Error())
		}
	}
}

// postTeamAdminApprovalRequest posts to the team requests approval channel.
// Only System Admins can approve team admin requests. Uses the same channel as
// team creation requests so all team-scope actions appear together.
func (p *Plugin) postTeamAdminApprovalRequest(req *teamAdminRequest, requester *model.User, team *model.Team) (postID, channelID string, err error) {
	ch := p.ensureTeamCreationApprovalChannel()
	header := p.systemAdminMentions() + " — a Team Admin request needs System Admin review."
	return p.postApprovalAttachment(
		p.teamAdminApprovalAttachment(req, requester, team),
		header,
		ch,
	)
}

func (p *Plugin) teamAdminApprovalAttachment(req *teamAdminRequest, requester *model.User, team *model.Team) *model.MessageAttachment {
	nominees := p.mentionList(req.NomineeIDs)
	fields := []*model.MessageAttachmentField{
		{Title: "Requested by", Value: fmt.Sprintf("@%s", requester.Username), Short: true},
		{Title: "Team", Value: team.DisplayName, Short: true},
		{Title: "Proposed Team Admins", Value: nominees, Short: false},
		{
			Title: "⚠️ Privilege Escalation — Team Admin",
			Value: "Team Admin grants broad permissions across the entire team: " +
				"manage members, create and archive channels, edit team settings, " +
				"and view all private channels. Only approve if this level of access is appropriate.",
			Short: false,
		},
	}
	siteURL := "/plugins/" + manifest.Id
	return &model.MessageAttachment{
		Title:   "Team Admin request",
		Color:   "#F4A221",
		Fields:  fields,
		Actions: p.teamAdminApprovalActions(req.ID, siteURL),
	}
}

func (p *Plugin) teamAdminApprovalActions(requestID, siteURL string) []*model.PostAction {
	return []*model.PostAction{
		{
			Id: "approve", Name: "Approve", Type: model.PostActionTypeButton, Style: "primary",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeApproveTeamAdmin,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
		{
			Id: "deny", Name: "Deny", Type: model.PostActionTypeButton, Style: "danger",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeDenyTeamAdmin,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
	}
}

// systemAdminMentions returns a mention string for all System Admins,
// used in root-channel approval post headers so the right people are notified.
func (p *Plugin) systemAdminMentions() string {
	admins, appErr := p.API.GetUsers(&model.UserGetOptions{Role: model.SystemAdminRoleId, Page: 0, PerPage: 100})
	if appErr != nil || len(admins) == 0 {
		return "@channel"
	}
	ids := make([]string, 0, len(admins))
	for _, a := range admins {
		if a.Id != p.botUserID {
			ids = append(ids, a.Id)
		}
	}
	mentions := p.mentionList(ids)
	if mentions == "" {
		return "@channel"
	}
	return mentions
}

func (p *Plugin) storeTeamAdminRequest(req *teamAdminRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return errors.Wrap(err, "failed to marshal team admin request")
	}
	if appErr := p.API.KVSet(kvTeamAdminRequestPrefix+req.ID, data); appErr != nil {
		return errors.Wrap(appErr, "failed to store team admin request")
	}
	return nil
}

func (p *Plugin) loadTeamAdminRequest(id string) (*teamAdminRequest, []byte, error) {
	data, appErr := p.API.KVGet(kvTeamAdminRequestPrefix + id)
	if appErr != nil {
		return nil, nil, errors.Wrap(appErr, "failed to load team admin request")
	}
	if data == nil {
		return nil, nil, nil
	}
	var req teamAdminRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, nil, errors.Wrap(err, "failed to unmarshal team admin request")
	}
	return &req, data, nil
}

// openTeamAdminRequestDialog opens the interactive dialog for submitting a
// Team Admin promotion request for the user's current team.
func (p *Plugin) openTeamAdminRequestDialog(triggerID, teamID string) error {
	teamName := teamID
	if t, appErr := p.API.GetTeam(teamID); appErr == nil {
		teamName = t.DisplayName
	}

	dialog := model.Dialog{
		CallbackId: teamAdminDialogCallbackID,
		Title:      "Request Team Admin Promotion",
		IntroductionText: fmt.Sprintf(
			"Nominate users to become Team Admins in **%s**. System Admin approval is required because Team Admin grants broad permissions across the whole team.",
			teamName,
		),
		SubmitLabel: "Submit request",
		State:       teamID,
		Elements: []model.DialogElement{
			{
				DisplayName: "Users to promote",
				Name:        fieldMembers,
				Type:        "select",
				DataSource:  "users",
				MultiSelect: true,
				HelpText:    "Select one or more users to nominate for Team Admin. They will be added to the team if not already members.",
			},
		},
	}
	return p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeTeamAdminDialog),
		Dialog:    dialog,
	})
}
