package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const headerUserID = "Mattermost-User-Id"

func TestHandleUserAutocomplete_MissingHeaderUnauthorized(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)

	req := httptest.NewRequest(http.MethodGet, routeUserAutocomplete+"?q=a&team_id=t1", nil)
	w := httptest.NewRecorder()
	p.handleUserAutocomplete(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	// No API work without an authenticated caller.
	api.AssertNotCalled(t, "SearchUsers", mock.Anything)
}

func TestHandleUserAutocomplete_NonMemberGetsEmpty(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	// Caller is not a member of the requested team.
	api.On("GetTeamMember", "t1", "u1").Return(nil, testAppErr("not a member"))

	req := httptest.NewRequest(http.MethodGet, routeUserAutocomplete+"?q=alice&team_id=t1", nil)
	req.Header.Set(headerUserID, "u1")
	w := httptest.NewRecorder()
	p.handleUserAutocomplete(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, "[]", strings.TrimSpace(w.Body.String()))
	// The user directory is never searched for a non-member — no enumeration.
	api.AssertNotCalled(t, "SearchUsers", mock.Anything)
}

func TestHandleUserAutocomplete_TeamScopedHappyPath(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetTeamMember", "t1", "u1").Return(&model.TeamMember{TeamId: "t1", UserId: "u1"}, nil)
	// The search MUST be scoped to the requested team — assert the TeamId
	// (and term) reach SearchUsers, so team-scoping can't regress silently.
	api.On("SearchUsers", mock.MatchedBy(func(s *model.UserSearch) bool {
		return s.TeamId == "t1" && s.Term == "alice"
	})).Return([]*model.User{{Id: "id_alice", Username: "alice"}}, nil)

	req := httptest.NewRequest(http.MethodGet, routeUserAutocomplete+"?q=alice&team_id=t1", nil)
	req.Header.Set(headerUserID, "u1")
	w := httptest.NewRecorder()
	p.handleUserAutocomplete(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)
	require.Equal(t, "alice", out[0]["username"])
}

func TestHandleUserAutocomplete_SoftDeletedMemberGetsEmpty(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	// Former member: GetTeamMember returns a row, but DeleteAt != 0.
	api.On("GetTeamMember", "t1", "u1").Return(&model.TeamMember{TeamId: "t1", UserId: "u1", DeleteAt: 123}, nil)

	req := httptest.NewRequest(http.MethodGet, routeUserAutocomplete+"?q=alice&team_id=t1", nil)
	req.Header.Set(headerUserID, "u1")
	w := httptest.NewRecorder()
	p.handleUserAutocomplete(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, "[]", strings.TrimSpace(w.Body.String()))
	// A former member must not be able to enumerate the team's users.
	api.AssertNotCalled(t, "SearchUsers", mock.Anything)
}

func TestHandleUserAutocomplete_RequiresTeamID(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	req := httptest.NewRequest(http.MethodGet, routeUserAutocomplete+"?q=alice", nil)
	req.Header.Set(headerUserID, "u1")
	w := httptest.NewRecorder()
	p.handleUserAutocomplete(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, "[]", strings.TrimSpace(w.Body.String()))
	api.AssertNotCalled(t, "SearchUsers", mock.Anything)
}

func TestHandleAction_MissingHeaderUnauthorized(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)

	req := httptest.NewRequest(http.MethodPost, routeApprove, strings.NewReader("{}"))
	w := httptest.NewRecorder()
	p.handleAction(w, req, true)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	// Identity is never looked up from a forged/absent header.
	api.AssertNotCalled(t, "GetUser", mock.Anything)
}

// actionBody builds a PostAction integration request body carrying the
// given pending request id in its context.
func actionBody(t *testing.T, requestID string) string {
	t.Helper()
	body, err := json.Marshal(model.PostActionIntegrationRequest{
		PostId:  "post1",
		Context: map[string]any{actionContextRequestID: requestID},
	})
	require.NoError(t, err)
	return string(body)
}

func TestHandleAction_ApproveClaimsAndCreates(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	req := &channelRequest{ID: "req1", RequesterID: "u_req", TeamID: "team1", Name: "team-x", DisplayName: "X", ChannelType: "O"}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Username: "admin", Roles: "system_user system_admin"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)
	// The claim compares against the exact bytes loadRequest returned.
	api.On("KVCompareAndDelete", kvRequestPrefix+"req1", raw).Return(true, nil)
	api.On("CreateChannel", mock.Anything).Return(&model.Channel{Id: "ch1", Name: "team-x"}, nil)
	api.On("AddChannelMember", "ch1", "u_req").Return(&model.ChannelMember{}, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post")) // resolvedPost falls back

	r := httptest.NewRequest(http.MethodPost, routeApprove, strings.NewReader(actionBody(t, "req1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Update)
	api.AssertCalled(t, "KVCompareAndDelete", kvRequestPrefix+"req1", raw)
	api.AssertCalled(t, "CreateChannel", mock.Anything)
}

func TestHandleAction_CreateFailureRestoresRequest(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	req := &channelRequest{ID: "req1", RequesterID: "u_req", TeamID: "team1", Name: "team-x", DisplayName: "X", ChannelType: "O"}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Roles: "system_user system_admin"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)
	api.On("KVCompareAndDelete", kvRequestPrefix+"req1", raw).Return(true, nil)
	// Channel creation fails after the claim already deleted the request.
	api.On("CreateChannel", mock.Anything).Return(nil, testAppErr("boom"))
	// The request must be restored so it isn't lost.
	api.On("KVSet", kvRequestPrefix+"req1", mock.Anything).Return(nil)

	r := httptest.NewRequest(http.MethodPost, routeApprove, strings.NewReader(actionBody(t, "req1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(t, resp.EphemeralText, "still pending")
	// The claimed request was written back to the KV store.
	api.AssertCalled(t, "KVSet", kvRequestPrefix+"req1", mock.Anything)
}

func TestHandleAction_CreateAndRestoreBothFailWarnsResubmit(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	req := &channelRequest{ID: "req1", RequesterID: "u_req", TeamID: "team1", Name: "team-x", DisplayName: "X", ChannelType: "O"}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Roles: "system_user system_admin"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)
	api.On("KVCompareAndDelete", kvRequestPrefix+"req1", raw).Return(true, nil)
	api.On("CreateChannel", mock.Anything).Return(nil, testAppErr("boom"))
	// The restore write also fails -> the request is genuinely lost.
	api.On("KVSet", kvRequestPrefix+"req1", mock.Anything).Return(testAppErr("kv down"))

	r := httptest.NewRequest(http.MethodPost, routeApprove, strings.NewReader(actionBody(t, "req1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(t, resp.EphemeralText, "could not be saved")
}

func TestHandleDialogSubmit_UsesHeaderIdentityNotBody(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{prefixes: []channelPrefix{{Prefix: "team-"}}})

	// The header says "realuser"; the body tries to forge a sysadmin.
	api.On("GetUser", "realuser").Return(&model.User{Id: "realuser", Roles: "system_user"}, nil)
	api.On("GetTeamMember", "team1", "realuser").Return(nil, testAppErr("not a member"))

	body, err := json.Marshal(model.SubmitDialogRequest{
		UserId: "forged-admin",
		State:  "team1",
		Submission: map[string]any{
			fieldDisplayName: "X",
			fieldPrefix:      "team-",
			fieldName:        "marketing",
		},
	})
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodPost, routeDialog, strings.NewReader(string(body)))
	r.Header.Set(headerUserID, "realuser")
	w := httptest.NewRecorder()
	p.handleDialogSubmit(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	// Identity is the header user, never the body's forged id.
	api.AssertCalled(t, "GetUser", "realuser")
	api.AssertNotCalled(t, "GetUser", "forged-admin")
	api.AssertCalled(t, "GetTeamMember", "team1", "realuser")
}

func TestCanApprove(t *testing.T) {
	t.Run("nil user", func(t *testing.T) {
		p := newTestPlugin(&plugintest.API{})
		p.setConfiguration(&configuration{})
		require.False(t, p.canApprove(nil))
	})

	t.Run("system admin always", func(t *testing.T) {
		p := newTestPlugin(&plugintest.API{})
		p.setConfiguration(&configuration{})
		require.True(t, p.canApprove(&model.User{Id: "a", Roles: "system_user system_admin"}))
	})

	t.Run("team admin approves when flag on", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{AllowTeamAdminApprovers: true, ApprovalTeam: "eng"})
		api.On("GetTeamByName", "eng").Return(&model.Team{Id: "team1"}, nil)
		api.On("GetTeamMember", "team1", "u1").Return(&model.TeamMember{Roles: "team_user team_admin"}, nil)
		require.True(t, p.canApprove(&model.User{Id: "u1", Roles: "system_user"}))
	})

	t.Run("team admin denied when flag off", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{AllowTeamAdminApprovers: false, ApprovalTeam: "eng"})
		// isTeamAdmin must not even be consulted when the flag is off.
		require.False(t, p.canApprove(&model.User{Id: "u1", Roles: "system_user"}))
		api.AssertNotCalled(t, "GetTeamByName", mock.Anything)
	})

	t.Run("regular user denied", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{AllowTeamAdminApprovers: true, ApprovalTeam: "eng"})
		api.On("GetTeamByName", "eng").Return(&model.Team{Id: "team1"}, nil)
		api.On("GetTeamMember", "team1", "u1").Return(&model.TeamMember{Roles: "team_user"}, nil)
		require.False(t, p.canApprove(&model.User{Id: "u1", Roles: "system_user"}))
	})
}

func TestHandleAction_LostClaimReportsAlreadyHandled(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	req := &channelRequest{ID: "req1", RequesterID: "u_req", TeamID: "team1", Name: "team-x", DisplayName: "X", ChannelType: "O"}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Roles: "system_user system_admin"}, nil)
	api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)
	// Another approver already claimed it — compare-and-delete finds no match.
	api.On("KVCompareAndDelete", kvRequestPrefix+"req1", raw).Return(false, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	r := httptest.NewRequest(http.MethodPost, routeApprove, strings.NewReader(actionBody(t, "req1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	// The loser of the race must NOT create a channel.
	api.AssertNotCalled(t, "CreateChannel", mock.Anything)
}

func TestHandleAction_DenyDoesNotCreateChannel(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	req := &channelRequest{ID: "req1", RequesterID: "u_req", TeamID: "team1", Name: "team-x", DisplayName: "X", ChannelType: "O"}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Username: "admin", Roles: "system_user system_admin"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)
	api.On("KVCompareAndDelete", kvRequestPrefix+"req1", raw).Return(true, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	r := httptest.NewRequest(http.MethodPost, routeDeny, strings.NewReader(actionBody(t, "req1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleAction(w, r, false)

	require.Equal(t, http.StatusOK, w.Code)
	api.AssertNotCalled(t, "CreateChannel", mock.Anything)
	// Requester is still notified of the denial.
	api.AssertCalled(t, "GetDirectChannel", "u_req", "bot-user-id")
}

func TestHandleAction_NonApproverRejected(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{}) // AllowTeamAdminApprovers false

	api.On("GetUser", "u1").Return(&model.User{Id: "u1", Roles: "system_user"}, nil)

	req := httptest.NewRequest(http.MethodPost, routeApprove, strings.NewReader("{}"))
	req.Header.Set(headerUserID, "u1")
	w := httptest.NewRecorder()
	p.handleAction(w, req, true)

	require.Equal(t, http.StatusOK, w.Code)
	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(t, resp.EphemeralText, "permission")
	// A non-approver never reaches the pending request.
	api.AssertNotCalled(t, "KVGet", mock.Anything)
}

func TestHandleAdminAction_ApproveClaimsAndPromotes(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	areq := &adminRequest{ID: "areq1", RequesterID: "u_req", ChannelID: "chX", NomineeIDs: []string{"u_nom"}}
	raw, err := json.Marshal(areq)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Username: "admin", Roles: "system_user system_admin"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nom"}, nil)
	api.On("KVGet", kvAdminRequestPrefix+"areq1").Return(raw, nil)
	api.On("GetChannel", "chX").Return(&model.Channel{Id: "chX", Name: "marketing", TeamId: "team1"}, nil)
	// The claim compares against the exact bytes loadAdminRequest returned.
	api.On("KVCompareAndDelete", kvAdminRequestPrefix+"areq1", raw).Return(true, nil)
	api.On("AddChannelMember", "chX", "u_nom").Return(&model.ChannelMember{}, nil)
	api.On("UpdateChannelMemberRoles", "chX", "u_nom", channelAdminRoles).Return(&model.ChannelMember{}, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post")) // resolvedPost falls back

	r := httptest.NewRequest(http.MethodPost, routeApproveAdmin, strings.NewReader(actionBody(t, "areq1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleAdminAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Update)
	api.AssertCalled(t, "KVCompareAndDelete", kvAdminRequestPrefix+"areq1", raw)
	api.AssertCalled(t, "UpdateChannelMemberRoles", "chX", "u_nom", channelAdminRoles)
	// The claim deletes the key atomically; no separate KVDelete.
	api.AssertNotCalled(t, "KVDelete", mock.Anything)
}

func TestHandleAdminAction_LostClaimReportsAlreadyHandled(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	areq := &adminRequest{ID: "areq1", RequesterID: "u_req", ChannelID: "chX", NomineeIDs: []string{"u_nom"}}
	raw, err := json.Marshal(areq)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Username: "admin", Roles: "system_user system_admin"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nom"}, nil)
	api.On("KVGet", kvAdminRequestPrefix+"areq1").Return(raw, nil)
	api.On("GetChannel", "chX").Return(&model.Channel{Id: "chX", Name: "marketing", TeamId: "team1"}, nil)
	// Another reviewer already claimed and processed the request.
	api.On("KVCompareAndDelete", kvAdminRequestPrefix+"areq1", raw).Return(false, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	r := httptest.NewRequest(http.MethodPost, routeApproveAdmin, strings.NewReader(actionBody(t, "areq1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleAdminAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Update)
	require.Contains(t, resp.Update.Message, "already been handled")
	// The loser must not promote anyone.
	api.AssertNotCalled(t, "UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything)
}
