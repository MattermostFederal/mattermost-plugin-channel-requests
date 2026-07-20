package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// prefixConfig returns a minimal configuration with a single "team-" prefix so
// submitRequest can resolve a channel name during these tests.
func prefixConfig() *configuration {
	return &configuration{prefixes: []channelPrefix{{Prefix: "team-"}}}
}

func TestIsActiveTeamMember(t *testing.T) {
	t.Run("active member passes", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		api.On("GetTeamMember", "team1", "u1").Return(&model.TeamMember{TeamId: "team1", UserId: "u1", DeleteAt: 0}, nil)
		require.True(t, p.isActiveTeamMember("u1", "team1"))
	})

	t.Run("soft-deleted member is rejected", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		// A user who left the team still has a row, but with DeleteAt != 0.
		api.On("GetTeamMember", "team1", "u1").Return(&model.TeamMember{TeamId: "team1", UserId: "u1", DeleteAt: 123}, nil)
		require.False(t, p.isActiveTeamMember("u1", "team1"))
	})

	t.Run("non-member (API error) is rejected", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		api.On("GetTeamMember", "team1", "u1").Return(nil, testAppErr("not found"))
		require.False(t, p.isActiveTeamMember("u1", "team1"))
	})

	t.Run("empty inputs are rejected without an API call", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		require.False(t, p.isActiveTeamMember("", "team1"))
		require.False(t, p.isActiveTeamMember("u1", ""))
		api.AssertNotCalled(t, "GetTeamMember", mock.Anything, mock.Anything)
	})
}

func TestSubmitRequest_RejectsNonTeamMember(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(prefixConfig())

	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Roles: "system_user"}, nil)
	api.On("GetTeamMember", "team1", "u_req").Return(nil, testAppErr("not a member"))

	_, err := p.submitRequest(requestInput{
		RequesterID: "u_req",
		TeamID:      "team1",
		DisplayName: "Marketing",
		Prefix:      "team-",
		Name:        "marketing",
		ChannelType: channelTypeOpen,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "member of the team")
	// Nothing is created or stored when the gate fails.
	api.AssertNotCalled(t, "CreateChannel", mock.Anything)
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
}

func TestSubmitRequest_RejectsSoftDeletedMember(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(prefixConfig())

	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Roles: "system_user"}, nil)
	// Former member: row exists but DeleteAt != 0.
	api.On("GetTeamMember", "team1", "u_req").Return(&model.TeamMember{TeamId: "team1", UserId: "u_req", DeleteAt: 999}, nil)

	_, err := p.submitRequest(requestInput{
		RequesterID: "u_req",
		TeamID:      "team1",
		DisplayName: "Marketing",
		Prefix:      "team-",
		Name:        "marketing",
		ChannelType: channelTypeOpen,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "member of the team")
	api.AssertNotCalled(t, "CreateChannel", mock.Anything)
}

func TestSubmitRequest_SystemAdminExemptFromMembership(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(prefixConfig())

	// System Admin who is NOT a member of the target team still succeeds
	// (they manage all teams), and GetTeamMember is never consulted.
	api.On("GetUser", "u_admin").Return(&model.User{Id: "u_admin", Username: "root", Roles: "system_user system_admin"}, nil)
	api.On("CreateChannel", mock.Anything).Return(&model.Channel{Id: "ch1", Name: "team-marketing"}, nil)
	api.On("AddChannelMember", "ch1", mock.Anything).Return(&model.ChannelMember{}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	msg, err := p.submitRequest(requestInput{
		RequesterID: "u_admin",
		TeamID:      "team1",
		DisplayName: "Marketing",
		Prefix:      "team-",
		Name:        "marketing",
		ChannelType: channelTypeOpen,
	})

	require.NoError(t, err)
	require.Contains(t, msg, "team-marketing")
	api.AssertNotCalled(t, "GetTeamMember", mock.Anything, mock.Anything)
}

func TestSubmitRequest_ActiveMemberProceeds(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	// Auto-approve the requester so the success path goes straight to channel
	// creation (keeps the test focused on the membership gate, not approval
	// routing).
	cfg := prefixConfig()
	cfg.autoApproveUserIDs = []string{"u_req"}
	p.setConfiguration(cfg)

	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "reqer", Roles: "system_user"}, nil)
	api.On("GetTeamMember", "team1", "u_req").Return(&model.TeamMember{TeamId: "team1", UserId: "u_req", DeleteAt: 0}, nil)
	api.On("CreateChannel", mock.Anything).Return(&model.Channel{Id: "ch1", Name: "team-marketing"}, nil)
	api.On("AddChannelMember", "ch1", mock.Anything).Return(&model.ChannelMember{}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	msg, err := p.submitRequest(requestInput{
		RequesterID: "u_req",
		TeamID:      "team1",
		DisplayName: "Marketing",
		Prefix:      "team-",
		Name:        "marketing",
		ChannelType: channelTypeOpen,
	})

	require.NoError(t, err)
	require.Contains(t, msg, "team-marketing")
	api.AssertCalled(t, "CreateChannel", mock.Anything)
}

func TestHandleUserAutocomplete_RejectsNonMemberTeamScope(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	// Caller is not a member of team1 and is not a System Admin.
	api.On("GetTeamMember", "team1", "u_caller").Return(nil, testAppErr("not a member"))
	api.On("GetUser", "u_caller").Return(&model.User{Id: "u_caller", Roles: "system_user"}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user_autocomplete?q=jo&team_id=team1", nil)
	req.Header.Set("Mattermost-User-Id", "u_caller")
	rec := httptest.NewRecorder()

	p.handleUserAutocomplete(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	// The proxied search must never run for an unauthorized team scope.
	api.AssertNotCalled(t, "SearchUsers", mock.Anything)
}

func TestHandleUserAutocomplete_MemberMayScopeToTeam(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetTeamMember", "team1", "u_caller").Return(&model.TeamMember{TeamId: "team1", UserId: "u_caller", DeleteAt: 0}, nil)
	api.On("SearchUsers", mock.MatchedBy(func(s *model.UserSearch) bool {
		return s.TeamId == "team1" && s.Term == "jo"
	})).Return([]*model.User{{Id: "u1", Username: "jo"}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user_autocomplete?q=jo&team_id=team1", nil)
	req.Header.Set("Mattermost-User-Id", "u_caller")
	rec := httptest.NewRecorder()

	p.handleUserAutocomplete(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	api.AssertCalled(t, "SearchUsers", mock.Anything)
}

func TestHandleUserAutocomplete_RequiresAuth(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)

	// No Mattermost-User-Id header -> 401, and no downstream work.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/user_autocomplete?q=jo&team_id=team1", nil)
	rec := httptest.NewRecorder()

	p.handleUserAutocomplete(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	api.AssertNotCalled(t, "GetTeamMember", mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "SearchUsers", mock.Anything)
}
