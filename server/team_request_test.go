package main

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// teamAdminRoles is the role string createTeamForRequest promotes the requester
// to. Kept here so the test breaks loudly if the production string changes.
const teamAdminRoles = "team_user team_admin"

func TestValidateTeamRequestInput(t *testing.T) {
	require.Error(t, validateTeamRequestInput(teamRequestInput{DisplayName: "  "}))
	require.Error(t, validateTeamRequestInput(teamRequestInput{DisplayName: strings.Repeat("a", maxDisplayNameLen+1)}))
	require.Error(t, validateTeamRequestInput(teamRequestInput{DisplayName: "OK", Description: strings.Repeat("d", maxPurposeLen+1)}))

	tooMany := make([]string, maxMembersPerList+1)
	require.Error(t, validateTeamRequestInput(teamRequestInput{DisplayName: "OK", MemberIDs: tooMany}))

	require.NoError(t, validateTeamRequestInput(teamRequestInput{DisplayName: "Marketing", Description: "Team stuff"}))
}

func TestResolveTeamName(t *testing.T) {
	// Blank URL name falls back to a slug of the display name.
	name, err := resolveTeamName(teamRequestInput{DisplayName: "Marketing Team"})
	require.NoError(t, err)
	require.Equal(t, "marketing-team", name)

	// Provided name is slugified.
	name, err = resolveTeamName(teamRequestInput{DisplayName: "X", Name: "My Ops"})
	require.NoError(t, err)
	require.Equal(t, "my-ops", name)

	// Too short after slugifying is rejected.
	_, err = resolveTeamName(teamRequestInput{DisplayName: "A"})
	require.Error(t, err)
}

func TestCreateTeamForRequest_CreatesTeamPromotesRequesterAddsMembers(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	created := &model.Team{Id: "team1", Name: "marketing", DisplayName: "Marketing"}
	api.On("CreateTeam", mock.MatchedBy(func(tm *model.Team) bool {
		return tm.Name == "marketing" &&
			tm.DisplayName == "Marketing" &&
			tm.Type == model.TeamOpen &&
			tm.AllowOpenInvite &&
			tm.Email == "req@example.com"
	})).Return(created, nil)
	api.On("CreateTeamMember", "team1", "u_req").Return(&model.TeamMember{}, nil)
	api.On("UpdateTeamMemberRoles", "team1", "u_req", teamAdminRoles).Return(&model.TeamMember{}, nil)
	api.On("CreateTeamMember", "team1", "u_m1").Return(&model.TeamMember{}, nil)

	req := &teamRequest{
		RequesterID: "u_req",
		Name:        "marketing",
		DisplayName: "Marketing",
		TeamType:    model.TeamOpen,
		MemberIDs:   []string{"u_m1", "u_req"}, // requester in list is skipped (already added)
	}
	requester := &model.User{Id: "u_req", Username: "req", Email: "req@example.com"}

	team, err := p.createTeamForRequest(req, requester)
	require.NoError(t, err)
	require.Equal(t, "team1", team.Id)
}

func TestSubmitTeamRequest_SysAdminBypassCreatesTeam(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowTeamRequests: true})

	sysadmin := &model.User{Id: "u_admin", Username: "admin", Email: "a@example.com", Roles: model.SystemAdminRoleId}
	api.On("GetUser", "u_admin").Return(sysadmin, nil)
	api.On("GetTeamByName", "ops").Return(nil, model.NewAppError("GetTeamByName", "not_found", nil, "", 404))
	api.On("CreateTeam", mock.Anything).Return(&model.Team{Id: "team1", Name: "ops", DisplayName: "Ops"}, nil)
	api.On("CreateTeamMember", "team1", "u_admin").Return(&model.TeamMember{}, nil)
	api.On("UpdateTeamMemberRoles", "team1", "u_admin", teamAdminRoles).Return(&model.TeamMember{}, nil)

	msg, err := p.submitTeamRequest(teamRequestInput{RequesterID: "u_admin", DisplayName: "Ops"})
	require.NoError(t, err)
	require.Contains(t, msg, "Created team")
	// A bypass must not store a pending request.
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
}

func TestSubmitTeamRequest_PendingStoresAndPosts(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	// Configure an approval channel so the approval post has a fixed destination.
	p.setConfiguration(&configuration{AllowTeamRequests: true, ApprovalTeam: "ops", ApprovalChannel: "approvals"})

	requester := &model.User{Id: "u_req", Username: "req", Email: "req@example.com"}
	api.On("GetUser", "u_req").Return(requester, nil)
	api.On("GetTeamByName", "marketing").Return(nil, model.NewAppError("GetTeamByName", "not_found", nil, "", 404))
	api.On("KVSet", mock.MatchedBy(func(key string) bool {
		return strings.HasPrefix(key, kvTeamRequestPrefix)
	}), mock.Anything).Return(nil)
	api.On("GetChannelByNameForTeamName", "ops", "approvals", false).Return(&model.Channel{Id: "ch_approvals"}, nil)
	api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool {
		return post.ChannelId == "ch_approvals"
	})).Return(&model.Post{}, nil)

	msg, err := p.submitTeamRequest(teamRequestInput{RequesterID: "u_req", DisplayName: "Marketing"})
	require.NoError(t, err)
	require.Contains(t, msg, "submitted for approval")
	// A non-admin must NOT trigger team creation.
	api.AssertNotCalled(t, "CreateTeam", mock.Anything)
}

func TestTeamApprovalAttachmentWithNotice_PrependsWarningKeepsButtons(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	req := &teamRequest{ID: "r1", RequesterID: "u_req", Name: "ops", DisplayName: "Ops", TeamType: teamTypeOpen}
	requester := &model.User{Id: "u_req", Username: "req"}

	att := p.teamApprovalAttachmentWithNotice(req, requester, "Couldn't create the team: taken.")

	// The warning is the first field so it reads as a banner above the details.
	require.NotEmpty(t, att.Fields)
	require.Contains(t, att.Fields[0].Title, "Action needed")
	require.Contains(t, att.Fields[0].Value, "Couldn't create the team")
	// Buttons are preserved so the approver can retry or deny.
	require.Len(t, att.Actions, 2)
	// Colored red to signal the failed attempt (vs. the normal blue card).
	require.Equal(t, "#D24B4E", att.Color)
}

func TestSubmitTeamRequest_DuplicateURLNameRejected(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowTeamRequests: true})

	requester := &model.User{Id: "u_req", Username: "req", Email: "req@example.com"}
	api.On("GetUser", "u_req").Return(requester, nil)
	// A team already owns the slugified URL name "marketing".
	api.On("GetTeamByName", "marketing").Return(&model.Team{Id: "existing", Name: "marketing"}, nil)

	_, err := p.submitTeamRequest(teamRequestInput{RequesterID: "u_req", DisplayName: "Marketing"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "already exists")
	// The collision is caught before anything is stored or posted.
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "CreateTeam", mock.Anything)
}
