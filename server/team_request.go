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
	// kvTeamRequestPrefix namespaces pending team-creation requests in the KV
	// store, kept distinct from the channel-creation and channel-admin
	// prefixes so the request types never collide.
	kvTeamRequestPrefix = "team_request_"

	// teamDialogCallbackID identifies submissions from the team request dialog.
	teamDialogCallbackID = "team_request"

	// fieldTeamType is the team-request dialog element for Open vs Invite-only.
	// Named distinctly from the channel dialog's fieldType because the values
	// differ (teams use O/I, channels use O/P).
	fieldTeamType = "team_type"

	// fieldDescription is the team-request dialog element for the team
	// description (the team analogue of a channel's purpose).
	fieldDescription = "description"

	// Team type values as plain strings, for dialog options and storage.
	teamTypeOpen   = model.TeamOpen
	teamTypeInvite = model.TeamInvite

	// teamAdminRoleString grants a team member the Team Admin role. MM maps
	// this classic role string to the appropriate scheme role internally. The
	// requester is promoted to Team Admin of the team they asked for so they
	// can manage it.
	teamAdminRoleString = "team_user team_admin"
)

// teamRequest is a pending request to create a team, persisted in the KV store
// until a reviewer approves or denies it. Counterpart to channelRequest.
type teamRequest struct {
	ID          string `json:"id"`
	RequesterID string `json:"requester_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	TeamType    string `json:"team_type"`
	// MemberIDs are additional users to add to the team on creation. The
	// requester is always added (as Team Admin) regardless of this list.
	MemberIDs []string `json:"member_ids"`
}

// teamRequestInput is the normalized set of values gathered from either entry
// point (slash-command dialog or webapp modal) before a team request is built.
type teamRequestInput struct {
	RequesterID string
	DisplayName string
	// Name holds the desired URL name. May be blank, in which case it's
	// generated from DisplayName.
	Name        string
	Description string
	TeamType    string
	MemberIDs   []string
}

// validateTeamRequestInput checks the caller-supplied fields that require no
// API calls. Pure, so it can be unit-tested without mocks.
func validateTeamRequestInput(in teamRequestInput) error {
	if strings.TrimSpace(in.DisplayName) == "" {
		return errors.New("a team name is required")
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.DisplayName)) > maxDisplayNameLen {
		return errors.Errorf("team name must be %d characters or fewer", maxDisplayNameLen)
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Description)) > maxPurposeLen {
		return errors.Errorf("description must be %d characters or fewer", maxPurposeLen)
	}
	if len(in.MemberIDs) > maxMembersPerList {
		return errors.Errorf("too many members: at most %d per request", maxMembersPerList)
	}
	return nil
}

// resolveTeamName derives the final team URL name from the request. Unlike
// channels, teams use no admin-configured prefix list — the name is simply a
// slug of the requested name (falling back to the display name).
func resolveTeamName(in teamRequestInput) (string, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = in.DisplayName
	}
	name = slugify(name)
	if utf8.RuneCountInString(name) < 2 {
		return "", errors.New("team URL name must be at least 2 characters (lowercase letters, numbers, or hyphens)")
	}
	if !model.IsValidTeamName(name) {
		return "", errors.Errorf("%q is not a valid team URL name; use lowercase letters, numbers, and hyphens", name)
	}
	return name, nil
}

// submitTeamRequest validates the input and either creates the team immediately
// (System Admins + auto-approve users) or stores a pending request and posts it
// to the approval channel. Returns a message suitable for showing the requester.
func (p *Plugin) submitTeamRequest(in teamRequestInput) (string, error) {
	config := p.getConfiguration()

	if err := validateTeamRequestInput(in); err != nil {
		return "", err
	}

	requester, appErr := p.API.GetUser(in.RequesterID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load requesting user")
	}

	if in.TeamType != teamTypeOpen && in.TeamType != teamTypeInvite {
		in.TeamType = teamTypeOpen
	}

	name, err := resolveTeamName(in)
	if err != nil {
		return "", err
	}

	// Reject up front if the URL name is already taken. CreateTeam enforces
	// team-URL uniqueness, but without this check the collision only surfaces
	// at approval time: the request sits pending and every Approve click fails
	// with "A team with this URL already exists", which reads to the approver
	// as "nothing happened". Catching it here gives the requester immediate,
	// actionable feedback instead.
	if existing, appErr := p.API.GetTeamByName(name); appErr == nil && existing != nil {
		return "", newFieldError(fieldName, fmt.Sprintf("A team with the URL name %q already exists. Pick a different URL name.", name))
	}

	req := &teamRequest{
		ID:          model.NewId(),
		RequesterID: in.RequesterID,
		Name:        name,
		DisplayName: strings.TrimSpace(in.DisplayName),
		Description: strings.TrimSpace(in.Description),
		TeamType:    in.TeamType,
		MemberIDs:   dedupeNonEmpty(in.MemberIDs),
	}

	// Bypass approval for System Admins and delegated auto-approve users,
	// mirroring the channel-creation flow.
	if requester.IsSystemAdmin() || config.AutoApproveContains(requester.Id) {
		team, _, err := p.createTeamForRequest(req, requester)
		if err != nil {
			return "", err
		}
		p.logAudit(config, fmt.Sprintf("TEAM CREATED: @%s created team `%s` (%s) directly (System Admin or auto-approve)",
			requester.Username, req.DisplayName, team.Name))
		return fmt.Sprintf("Created team **%s**.", team.DisplayName), nil
	}

	if err := p.storeTeamRequest(req); err != nil {
		return "", err
	}

	if err := p.postTeamApprovalRequest(req, requester); err != nil {
		// Roll back so the stored request isn't orphaned without an approval message.
		_ = p.API.KVDelete(kvTeamRequestPrefix + req.ID)
		return "", err
	}

	return "Your team request has been submitted for approval. You'll be notified once an admin responds.", nil
}

// createTeamForRequest creates the team described by req, makes the requester a
// Team Admin, and adds any additional members. Per-user membership failures are
// logged but don't abort the others — a partially-populated team is better than
// none. It returns the created team and whether the requester was actually made
// a Team Admin, and trims req.MemberIDs to those actually added, so callers can
// report the true outcome instead of claiming membership/admin that didn't happen.
func (p *Plugin) createTeamForRequest(req *teamRequest, requester *model.User) (team *model.Team, requesterPromoted bool, err error) {
	team, appErr := p.API.CreateTeam(&model.Team{
		Name:            req.Name,
		DisplayName:     req.DisplayName,
		Description:     req.Description,
		Type:            req.TeamType,
		AllowOpenInvite: req.TeamType == teamTypeOpen,
		// Email is the team's primary-contact address; set it to the
		// requester's so team validation has a valid address.
		Email: requester.Email,
	})
	if appErr != nil {
		return nil, false, errors.Wrap(appErr, "failed to create team")
	}

	// Add the requester and promote them to Team Admin so the team they asked
	// for always has an owner who can manage it.
	if _, appErr := p.API.CreateTeamMember(team.Id, req.RequesterID); appErr != nil {
		p.API.LogWarn("failed to add requester to created team", "team_id", team.Id, "user_id", req.RequesterID, "error", appErr.Error())
	} else if _, appErr := p.API.UpdateTeamMemberRoles(team.Id, req.RequesterID, teamAdminRoleString); appErr != nil {
		p.API.LogWarn("failed to promote requester to team admin", "team_id", team.Id, "user_id", req.RequesterID, "error", appErr.Error())
	} else {
		requesterPromoted = true
	}

	added := make([]string, 0, len(req.MemberIDs))
	for _, userID := range req.MemberIDs {
		if userID == "" || userID == req.RequesterID {
			continue
		}
		if _, appErr := p.API.CreateTeamMember(team.Id, userID); appErr != nil {
			p.API.LogWarn("failed to add member to created team", "team_id", team.Id, "user_id", userID, "error", appErr.Error())
			continue
		}
		added = append(added, userID)
	}
	// Reflect who was actually added so downstream messaging is truthful.
	req.MemberIDs = added

	return team, requesterPromoted, nil
}

func (p *Plugin) storeTeamRequest(req *teamRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return errors.Wrap(err, "failed to marshal team request")
	}
	if appErr := p.API.KVSet(kvTeamRequestPrefix+req.ID, data); appErr != nil {
		return errors.Wrap(appErr, "failed to store team request")
	}
	return nil
}

// loadTeamRequest returns the stored request and its raw KV bytes so
// handleTeamAction can do an atomic KVCompareAndDelete against exactly what was
// read — mirrors loadRequest / loadAdminRequest.
func (p *Plugin) loadTeamRequest(id string) (*teamRequest, []byte, error) {
	data, appErr := p.API.KVGet(kvTeamRequestPrefix + id)
	if appErr != nil {
		return nil, nil, errors.Wrap(appErr, "failed to load team request")
	}
	if data == nil {
		return nil, nil, nil
	}
	var req teamRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, nil, errors.Wrap(err, "failed to unmarshal team request")
	}
	return &req, data, nil
}

// postTeamApprovalRequest delivers a team-creation request (with Approve/Deny
// buttons) to the reviewers, reusing the shared approval-channel routing (with
// System-Admin DM fallback).
func (p *Plugin) postTeamApprovalRequest(req *teamRequest, requester *model.User) error {
	return p.postApprovalAttachment(
		p.teamApprovalAttachment(req, requester),
		"@channel — a new team request needs your review.",
	)
}

// teamApprovalAttachment builds the Slack attachment (with Approve/Deny buttons)
// describing a team-creation request.
func (p *Plugin) teamApprovalAttachment(req *teamRequest, requester *model.User) *model.MessageAttachment {
	visibility := "Open"
	if req.TeamType == teamTypeInvite {
		visibility = "Invite only"
	}

	fields := []*model.MessageAttachmentField{
		{Title: "Requested by", Value: fmt.Sprintf("@%s", requester.Username), Short: true},
		{Title: "Visibility", Value: visibility, Short: true},
		{Title: "Team name", Value: req.DisplayName, Short: true},
		{Title: "URL", Value: fmt.Sprintf("/%s", req.Name), Short: true},
	}
	if req.Description != "" {
		fields = append(fields, &model.MessageAttachmentField{Title: "Description", Value: req.Description, Short: false})
	}
	if len(req.MemberIDs) > 0 {
		fields = append(fields, &model.MessageAttachmentField{Title: "Members to add", Value: p.mentionList(req.MemberIDs), Short: false})
	}

	siteURL := "/plugins/" + manifest.Id
	return &model.MessageAttachment{
		Title:   "Team creation request",
		Color:   "#0058CC",
		Fields:  fields,
		Actions: p.teamApprovalActions(req.ID, siteURL),
	}
}

// teamApprovalAttachmentWithNotice is teamApprovalAttachment plus a visible
// warning banner, used to repaint the card when an approval attempt failed
// (e.g. the team URL was taken between submit and approval). The Approve/Deny
// buttons are preserved so the approver can retry or deny — the warning is a
// cue, not a terminal state.
func (p *Plugin) teamApprovalAttachmentWithNotice(req *teamRequest, requester *model.User, notice string) *model.MessageAttachment {
	att := p.teamApprovalAttachment(req, requester)
	att.Color = "#D24B4E" // red, to signal the failed attempt
	att.Fields = append([]*model.MessageAttachmentField{
		{Title: "⚠️ Action needed", Value: notice, Short: false},
	}, att.Fields...)
	return att
}

func (p *Plugin) teamApprovalActions(requestID, siteURL string) []*model.PostAction {
	return []*model.PostAction{
		{
			Id:    "approve",
			Name:  "Approve",
			Type:  model.PostActionTypeButton,
			Style: "primary",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeApproveTeam,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
		{
			Id:    "deny",
			Name:  "Deny",
			Type:  model.PostActionTypeButton,
			Style: "danger",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeDenyTeam,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
	}
}
