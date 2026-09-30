package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	// kvTeamAdminRequestPrefix namespaces pending team-admin requests in the KV
	// store, kept distinct from the other request-type prefixes.
	kvTeamAdminRequestPrefix = "team_admin_request_"

	// teamAdminDialogCallbackID identifies submissions from the team-admin dialog.
	teamAdminDialogCallbackID = "team_admin_request"

	// fieldNominees is the dialog element naming the users to promote.
	fieldNominees = "nominees"
)

// teamAdminRequest is a pending request to promote one or more users to Team
// Admin on an existing team. The team analogue of adminRequest (channel admin).
type teamAdminRequest struct {
	ID          string `json:"id"`
	RequesterID string `json:"requester_id"`
	TeamID      string `json:"team_id"`
	// NomineeIDs are the users to promote to Team Admin. On approval each is
	// added to the team (if not already a member) and granted the team_admin
	// scheme role.
	NomineeIDs []string `json:"nominee_ids"`
}

// submitTeamAdminRequest validates the input and either promotes the nominees
// immediately (System Admins + auto-approve users) or stores a pending request
// and posts it to the approval channel. Returns a message for the requester.
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

	// The requester must belong to the team they're nominating admins for.
	// System Admins manage every team and are exempt. GetTeamMember returns
	// soft-deleted rows, so require an active membership.
	if !requester.IsSystemAdmin() {
		member, appErr := p.API.GetTeamMember(teamID, requesterID)
		if appErr != nil || member == nil || member.DeleteAt != 0 {
			return "", errors.New("you must be a member of the team to request Team Admins for it")
		}
	}

	req := &teamAdminRequest{
		ID:          model.NewId(),
		RequesterID: requesterID,
		TeamID:      teamID,
		NomineeIDs:  nominees,
	}

	config := p.getConfiguration()

	// Bypass approval for System Admins and delegated auto-approve users,
	// mirroring the channel-admin flow.
	if requester.IsSystemAdmin() || config.AutoApproveContains(requester.Id) {
		p.promoteTeamAdmins(req)
		p.logAudit(config, fmt.Sprintf("TEAM ADMIN: @%s promoted %s to Team Admin in team `%s`",
			requester.Username, p.mentionList(req.NomineeIDs), team.Name))
		return fmt.Sprintf("Promoted %s to Team Admin in **%s**.", p.mentionList(req.NomineeIDs), team.DisplayName), nil
	}

	if err := p.storeTeamAdminRequest(req); err != nil {
		return "", err
	}

	if err := p.postTeamAdminApprovalRequest(req, requester, team); err != nil {
		_ = p.API.KVDelete(kvTeamAdminRequestPrefix + req.ID)
		return "", err
	}

	return "Your Team Admin request has been submitted for approval. You'll be notified once an admin responds.", nil
}

// promoteTeamAdmins adds each nominee to the team (a no-op for existing members)
// and grants them the team_admin role. Per-user failures are logged but don't
// abort the others — a partial promotion is better than none.
func (p *Plugin) promoteTeamAdmins(req *teamAdminRequest) {
	for _, userID := range req.NomineeIDs {
		if userID == "" {
			continue
		}
		// Ensure membership first — a user can't be a Team Admin without being
		// on the team. CreateTeamMember is idempotent for existing members.
		if _, appErr := p.API.CreateTeamMember(req.TeamID, userID); appErr != nil {
			p.API.LogWarn("failed to add nominee to team", "team_id", req.TeamID, "user_id", userID, "error", appErr.Error())
		}
		if _, appErr := p.API.UpdateTeamMemberRoles(req.TeamID, userID, teamAdminRoleString); appErr != nil {
			p.API.LogWarn("failed to promote nominee to team admin", "team_id", req.TeamID, "user_id", userID, "error", appErr.Error())
		}
	}
}

func (p *Plugin) storeTeamAdminRequest(req *teamAdminRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return errors.Wrap(err, "failed to marshal team-admin request")
	}
	if appErr := p.API.KVSet(kvTeamAdminRequestPrefix+req.ID, data); appErr != nil {
		return errors.Wrap(appErr, "failed to store team-admin request")
	}
	return nil
}

// loadTeamAdminRequest returns the stored request and its raw KV bytes for an
// atomic KVCompareAndDelete — mirrors loadAdminRequest.
func (p *Plugin) loadTeamAdminRequest(id string) (*teamAdminRequest, []byte, error) {
	data, appErr := p.API.KVGet(kvTeamAdminRequestPrefix + id)
	if appErr != nil {
		return nil, nil, errors.Wrap(appErr, "failed to load team-admin request")
	}
	if data == nil {
		return nil, nil, nil
	}
	var req teamAdminRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, nil, errors.Wrap(err, "failed to unmarshal team-admin request")
	}
	return &req, data, nil
}

// postTeamAdminApprovalRequest delivers a team-admin request (with Approve/Deny
// buttons) to the reviewers, @-mentioning the guaranteed approvers (System
// Admins + the team's Team Admins).
func (p *Plugin) postTeamAdminApprovalRequest(req *teamAdminRequest, requester *model.User, team *model.Team) error {
	return p.postApprovalAttachment(
		p.teamAdminApprovalAttachment(req, requester, team),
		p.teamAdminApprovalHeader(team.Id),
	)
}

// teamAdminApprovalHeader leads the approval post, @-mentioning who can approve.
// adminApproverIDs(teamID) already returns System Admins + the team's Team
// Admins, so it's reused directly.
func (p *Plugin) teamAdminApprovalHeader(teamID string) string {
	mentions := p.mentionList(p.adminApproverIDs(teamID))
	if mentions == "" {
		return "A Team Admin request needs your review."
	}
	return mentions + " — a Team Admin request needs your review."
}

func (p *Plugin) teamAdminApprovalAttachment(req *teamAdminRequest, requester *model.User, team *model.Team) *model.MessageAttachment {
	fields := []*model.MessageAttachmentField{
		{Title: "Requested by", Value: fmt.Sprintf("@%s", requester.Username), Short: true},
		{Title: "Team", Value: fmt.Sprintf("%s (/%s)", team.DisplayName, team.Name), Short: true},
		{Title: "Proposed Team Admins", Value: p.mentionList(req.NomineeIDs), Short: false},
	}

	siteURL := "/plugins/" + manifest.Id
	return &model.MessageAttachment{
		Title:   "Team Admin request",
		Color:   "#0058CC",
		Fields:  fields,
		Actions: p.teamAdminApprovalActions(req.ID, siteURL),
	}
}

func (p *Plugin) teamAdminApprovalActions(requestID, siteURL string) []*model.PostAction {
	return []*model.PostAction{
		{
			Id:    "approve",
			Name:  "Approve",
			Type:  model.PostActionTypeButton,
			Style: "primary",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeApproveTeamAdmin,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
		{
			Id:    "deny",
			Name:  "Deny",
			Type:  model.PostActionTypeButton,
			Style: "danger",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeDenyTeamAdmin,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
	}
}

// canApproveTeamAdminRequest reports whether the acting user may approve or deny
// a Team Admin request for the given team: the global approvers (via canApprove)
// plus the target team's own Team Admins.
func (p *Plugin) canApproveTeamAdminRequest(user *model.User, teamID string) bool {
	if p.canApprove(user) {
		return true
	}
	if user == nil || teamID == "" {
		return false
	}
	return p.isTeamAdminOfTeamID(user.Id, teamID)
}
