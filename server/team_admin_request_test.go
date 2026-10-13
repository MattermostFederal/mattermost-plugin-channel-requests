package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSubmitTeamAdminRequest_RequiresTeamAndNominees(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowTeamAdminRequests: true})

	_, err := p.submitTeamAdminRequest("u_req", "", []string{"u_n1"})
	require.Error(t, err)

	// A team with no nominees is rejected before any team/user lookup.
	_, err = p.submitTeamAdminRequest("u_req", "team1", nil)
	require.Error(t, err)
}

func TestPromoteTeamAdmins_AddsAndPromotes(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("CreateTeamMember", "team1", "u_n1").Return(&model.TeamMember{}, nil)
	api.On("UpdateTeamMemberRoles", "team1", "u_n1", teamAdminRoles).Return(&model.TeamMember{}, nil)

	p.promoteTeamAdmins(&teamAdminRequest{TeamID: "team1", NomineeIDs: []string{"u_n1"}})
}

func TestSubmitTeamAdminRequest_SysAdminBypassPromotes(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowTeamAdminRequests: true})

	api.On("GetTeam", "team1").Return(&model.Team{Id: "team1", Name: "ops", DisplayName: "Ops"}, nil)
	api.On("GetUser", "u_admin").Return(&model.User{Id: "u_admin", Username: "admin", Roles: model.SystemAdminRoleId}, nil)
	api.On("GetUser", "u_n1").Return(&model.User{Id: "u_n1", Username: "nom1"}, nil)
	api.On("CreateTeamMember", "team1", "u_n1").Return(&model.TeamMember{}, nil)
	api.On("UpdateTeamMemberRoles", "team1", "u_n1", teamAdminRoles).Return(&model.TeamMember{}, nil)

	msg, err := p.submitTeamAdminRequest("u_admin", "team1", []string{"u_n1"})
	require.NoError(t, err)
	require.Contains(t, msg, "Promoted")
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
}

func TestSubmitTeamAdminRequest_PendingStoresAndPosts(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowTeamAdminRequests: true, ApprovalTeam: "ops", ApprovalChannel: "approvals"})

	api.On("GetTeam", "team1").Return(&model.Team{Id: "team1", Name: "ops", DisplayName: "Ops"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("GetUser", "u_n1").Return(&model.User{Id: "u_n1", Username: "nom1"}, nil)
	api.On("GetTeamMember", "team1", "u_req").Return(&model.TeamMember{UserId: "u_req"}, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)
	// adminApproverIDs enumerates System Admins + the team's Team Admins.
	api.On("GetUsers", mock.Anything).Return([]*model.User{}, nil)
	api.On("GetTeamMembers", "team1", 0, mock.Anything).Return([]*model.TeamMember{}, nil)
	api.On("GetChannelByNameForTeamName", "ops", "approvals", false).Return(&model.Channel{Id: "ch_approvals"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	msg, err := p.submitTeamAdminRequest("u_req", "team1", []string{"u_n1"})
	require.NoError(t, err)
	require.Contains(t, msg, "submitted for approval")
	api.AssertNotCalled(t, "UpdateTeamMemberRoles", mock.Anything, mock.Anything, mock.Anything)
}

func TestCanApproveTeamAdminRequest(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	// System Admins always may.
	require.True(t, p.canApproveTeamAdminRequest(&model.User{Roles: model.SystemAdminRoleId}, "team1"))

	// A Team Admin of the target team may.
	api.On("GetTeamMember", "team1", "u_ta").Return(&model.TeamMember{UserId: "u_ta", SchemeAdmin: true}, nil)
	require.True(t, p.canApproveTeamAdminRequest(&model.User{Id: "u_ta"}, "team1"))

	// A regular member may not.
	api.On("GetTeamMember", "team1", "u_reg").Return(&model.TeamMember{UserId: "u_reg"}, nil)
	require.False(t, p.canApproveTeamAdminRequest(&model.User{Id: "u_reg"}, "team1"))
}
