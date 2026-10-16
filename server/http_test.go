package main

import (
	"encoding/json"
	"fmt"
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
	api.On("GetUser", "u1").Return(&model.User{Id: "u1", Roles: "system_user"}, nil)
	api.On("GetConfig").Return(&model.Config{})
	// The search MUST be scoped to the requested team — assert the TeamId
	// (and term) reach SearchUsers, so team-scoping can't regress silently.
	api.On("SearchUsers", mock.MatchedBy(func(s *model.UserSearch) bool {
		return s.TeamId == "t1" && s.Term == "alice"
	})).Return([]*model.User{{Id: "id_alice", Username: "alice", FirstName: "Alice", LastName: "Smith"}}, nil)

	req := httptest.NewRequest(http.MethodGet, routeUserAutocomplete+"?q=alice&team_id=t1", nil)
	req.Header.Set(headerUserID, "u1")
	w := httptest.NewRecorder()
	p.handleUserAutocomplete(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)
	require.Equal(t, "alice", out[0]["username"])
	// ShowFullName is unset (off): real names are withheld.
	require.NotContains(t, out[0], "first_name")
	require.NotContains(t, out[0], "last_name")
}

func TestHandleUserAutocomplete_ShowFullNameReturnsNames(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetTeamMember", "t1", "u1").Return(&model.TeamMember{TeamId: "t1", UserId: "u1"}, nil)
	api.On("GetUser", "u1").Return(&model.User{Id: "u1", Roles: "system_user"}, nil)
	cfg := &model.Config{}
	cfg.SetDefaults()
	*cfg.PrivacySettings.ShowFullName = true
	api.On("GetConfig").Return(cfg)
	api.On("SearchUsers", mock.Anything).Return([]*model.User{{Id: "id_alice", Username: "alice", FirstName: "Alice", LastName: "Smith"}}, nil)

	req := httptest.NewRequest(http.MethodGet, routeUserAutocomplete+"?q=alice&team_id=t1", nil)
	req.Header.Set(headerUserID, "u1")
	w := httptest.NewRecorder()
	p.handleUserAutocomplete(w, req)

	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)
	require.Equal(t, "Alice", out[0]["first_name"])
}

func TestHandleUserAutocomplete_GuestGetsEmpty(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetTeamMember", "t1", "g1").Return(&model.TeamMember{TeamId: "t1", UserId: "g1"}, nil)
	api.On("GetUser", "g1").Return(&model.User{Id: "g1", Roles: model.SystemGuestRoleId}, nil)

	req := httptest.NewRequest(http.MethodGet, routeUserAutocomplete+"?q=a&team_id=t1", nil)
	req.Header.Set(headerUserID, "g1")
	w := httptest.NewRecorder()
	p.handleUserAutocomplete(w, req)

	require.JSONEq(t, "[]", strings.TrimSpace(w.Body.String()))
	api.AssertNotCalled(t, "SearchUsers", mock.Anything)
}

func TestServeHTTP_RejectsOversizedBody(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	huge := `{"members":["` + strings.Repeat("a", maxRequestBodyBytes) + `"]}`
	r := httptest.NewRequest(http.MethodPost, routeCreate, strings.NewReader(huge))
	r.Header.Set(headerUserID, "u1")
	w := httptest.NewRecorder()
	p.ServeHTTP(nil, w, r)

	require.Equal(t, http.StatusBadRequest, w.Code)
	api.AssertNotCalled(t, "GetUserByUsername", mock.Anything)
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
	stubPendingLists(api)
	stubActiveTeamMember(api, "team1", "u_req")
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(approvableConfig())

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
	stubPendingLists(api)
	stubActiveTeamMember(api, "team1", "u_req")
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(approvableConfig())

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
	stubPendingLists(api)
	stubActiveTeamMember(api, "team1", "u_req")
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(approvableConfig())

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
		p.setConfiguration(&configuration{AllowTeamAdminApprovers: true, ApprovalTeam: "team1"})
		api.On("GetTeamMember", "team1", "u1").Return(&model.TeamMember{Roles: "team_user team_admin"}, nil)
		require.True(t, p.canApprove(&model.User{Id: "u1", Roles: "system_user"}))
		// The approval team is an ID; it is never resolved by name.
		api.AssertNotCalled(t, "GetTeamByName", mock.Anything)
	})

	t.Run("team admin denied when flag off", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{AllowTeamAdminApprovers: false, ApprovalTeam: "team1"})
		// Team membership must not even be consulted when the flag is off.
		require.False(t, p.canApprove(&model.User{Id: "u1", Roles: "system_user"}))
		api.AssertNotCalled(t, "GetTeamMember", mock.Anything, mock.Anything)
	})

	t.Run("regular user denied", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{AllowTeamAdminApprovers: true, ApprovalTeam: "team1"})
		api.On("GetTeamMember", "team1", "u1").Return(&model.TeamMember{Roles: "team_user"}, nil)
		require.False(t, p.canApprove(&model.User{Id: "u1", Roles: "system_user"}))
	})

	t.Run("former team admin denied", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{AllowTeamAdminApprovers: true, ApprovalTeam: "team1"})
		// Leaving the team keeps SchemeAdmin on the soft-deleted row.
		api.On("GetTeamMember", "team1", "u1").Return(&model.TeamMember{SchemeAdmin: true, DeleteAt: 12345}, nil)
		require.False(t, p.canApprove(&model.User{Id: "u1", Roles: "system_user"}))
	})
}

func TestHandleAction_TeamAdminCannotApproveOwnRequest(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowTeamAdminApprovers: true, ApprovalTeam: "appteam"})

	req := &channelRequest{ID: "req1", RequesterID: "u_ta", TeamID: "team1", Name: "team-x", DisplayName: "X", ChannelType: "O"}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "u_ta").Return(&model.User{Id: "u_ta", Username: "lead", Roles: "system_user"}, nil)
	api.On("GetTeamMember", "appteam", "u_ta").Return(&model.TeamMember{Roles: "team_user team_admin"}, nil)
	api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)

	r := httptest.NewRequest(http.MethodPost, routeApprove, strings.NewReader(actionBody(t, "req1")))
	r.Header.Set(headerUserID, "u_ta")
	w := httptest.NewRecorder()
	p.handleAction(w, r, true)

	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(t, resp.EphemeralText, "your own request")
	api.AssertNotCalled(t, "KVCompareAndDelete", mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "CreateChannel", mock.Anything)
}

func TestHandleAction_AuditRendersDisplayNameAsText(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	stubPendingLists(api)
	stubActiveTeamMember(api, "team1", "u_req")
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AuditChannelID: "audit1"})
	stubAuditChannel(api, "audit1")

	req := &channelRequest{ID: "req1", RequesterID: "u_req", TeamID: "team1", Name: "team-x", DisplayName: "a`b @here [c](https://example.com)", ChannelType: "O"}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Username: "admin", Roles: "system_user system_admin"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)
	api.On("KVCompareAndDelete", kvRequestPrefix+"req1", raw).Return(true, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))
	var audit *model.Post
	api.On("CreatePost", mock.Anything).Run(func(args mock.Arguments) {
		if post := args.Get(0).(*model.Post); post.ChannelId == "audit1" {
			audit = post
		}
	}).Return(&model.Post{}, nil)

	r := httptest.NewRequest(http.MethodPost, routeDeny, strings.NewReader(actionBody(t, "req1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleAction(w, r, false)

	require.NotNil(t, audit)
	require.NotContains(t, audit.Message, "@here")
	require.NotContains(t, audit.Message, "](https")
	require.NotContains(t, audit.Message, "@@")
	require.Contains(t, audit.Message, "from @req")
}

func TestResolvedPost_OnlyRebuildsBotPosts(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	other := &model.Post{Id: "p1", UserId: "someone", ChannelId: "ch2", Message: "hello"}
	other.AddProp("custom", "value")
	api.On("GetPost", "p1").Return(other, nil)

	got := p.resolvedPost("p1", "done")

	require.Equal(t, "done", got.Message)
	require.Empty(t, got.ChannelId)
	require.Nil(t, got.GetProp("custom"))
}

func TestHandleAction_LostClaimReportsAlreadyHandled(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(approvableConfig())

	req := &channelRequest{ID: "req1", RequesterID: "u_req", TeamID: "team1", Name: "team-x", DisplayName: "X", ChannelType: "O"}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Roles: "system_user system_admin"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	stubActiveTeamMember(api, "team1", "u_req")
	api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)
	// Another approver already claimed it — compare-and-delete finds no match.
	api.On("KVCompareAndDelete", kvRequestPrefix+"req1", raw).Return(false, nil)

	r := httptest.NewRequest(http.MethodPost, routeApprove, strings.NewReader(actionBody(t, "req1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// The loser replies privately and leaves the winner's card alone.
	require.Nil(t, resp.Update)
	require.Contains(t, resp.EphemeralText, "already been handled")
	// The loser of the race must NOT create a channel.
	api.AssertNotCalled(t, "CreateChannel", mock.Anything)
	api.AssertNotCalled(t, "GetPost", mock.Anything)
}

func TestHandleAction_DenyDoesNotCreateChannel(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	stubPendingLists(api)
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
	stubPendingLists(api)
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
	api.On("GetChannelMember", "chX", "u_nom").Return(&model.ChannelMember{}, nil)
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

	r := httptest.NewRequest(http.MethodPost, routeApproveAdmin, strings.NewReader(actionBody(t, "areq1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleAdminAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Nil(t, resp.Update)
	require.Contains(t, resp.EphemeralText, "already been handled")
	// The loser must not promote anyone.
	api.AssertNotCalled(t, "UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything)
}

func TestHandleAdminAction_UnknownRequestRepliesPrivately(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	// An unknown request gets a private reply; the post isn't loaded.
	api.On("GetUser", "u1").Return(&model.User{Id: "u1", Roles: "system_user"}, nil)
	api.On("KVGet", kvAdminRequestPrefix+"bogus").Return(nil, nil)

	r := httptest.NewRequest(http.MethodPost, routeDenyAdmin, strings.NewReader(actionBody(t, "bogus")))
	r.Header.Set(headerUserID, "u1")
	w := httptest.NewRecorder()
	p.handleAdminAction(w, r, false)

	require.Equal(t, http.StatusOK, w.Code)
	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Nil(t, resp.Update)
	api.AssertNotCalled(t, "GetPost", mock.Anything)
}

func TestHandleAdminAction_RequesterCannotApproveOwnRequest(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	areq := &adminRequest{ID: "areq1", RequesterID: "u_ta", ChannelID: "chX", NomineeIDs: []string{"u_ta"}}
	raw, err := json.Marshal(areq)
	require.NoError(t, err)

	api.On("GetUser", "u_ta").Return(&model.User{Id: "u_ta", Username: "lead", Roles: "system_user"}, nil)
	api.On("KVGet", kvAdminRequestPrefix+"areq1").Return(raw, nil)
	api.On("GetChannel", "chX").Return(&model.Channel{Id: "chX", TeamId: "team1"}, nil)
	api.On("GetTeamMember", "team1", "u_ta").Return(&model.TeamMember{Roles: "team_user team_admin"}, nil)

	r := httptest.NewRequest(http.MethodPost, routeApproveAdmin, strings.NewReader(actionBody(t, "areq1")))
	r.Header.Set(headerUserID, "u_ta")
	w := httptest.NewRecorder()
	p.handleAdminAction(w, r, true)

	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(t, resp.EphemeralText, "your own request")
	api.AssertNotCalled(t, "KVCompareAndDelete", mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything)
}

func TestHandleAdminAction_ArchivedChannelLeavesRequestPending(t *testing.T) {
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
	api.On("GetChannel", "chX").Return(&model.Channel{Id: "chX", Name: "old", TeamId: "team1", DeleteAt: 1}, nil)

	r := httptest.NewRequest(http.MethodPost, routeApproveAdmin, strings.NewReader(actionBody(t, "areq1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleAdminAction(w, r, true)

	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(t, resp.EphemeralText, "deleted")
	api.AssertNotCalled(t, "KVCompareAndDelete", mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything)
}

// sysadmin is an acting user who passes every approver check.
func sysadmin(id string) *model.User {
	return &model.User{Id: id, Username: "admin", Roles: "system_user system_admin"}
}

// callHandler sends body to handler as userID (no header when empty) and
// returns the recorder.
func callHandler(handler http.HandlerFunc, method, target, userID, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if userID != "" {
		r.Header.Set(headerUserID, userID)
	}
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

// actionResponse decodes a PostAction integration response.
func actionResponse(t *testing.T, w *httptest.ResponseRecorder) model.PostActionIntegrationResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code)
	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

// jsonMap decodes a {"message"} / {"error"} style response.
func jsonMap(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return out
}

func TestServeHTTP_RoutesEveryPathToAnAuthenticatedHandler(t *testing.T) {
	routes := []string{
		routeDialog, routeCreate, routeApprove, routeDeny, routeRequestAdmin,
		routeApproveAdmin, routeDenyAdmin, routePrefixes, routeTeams, routeChannels,
		routeUserAutocomplete,
	}
	for _, route := range routes {
		t.Run(route, func(t *testing.T) {
			api := &plugintest.API{}
			p := newTestPlugin(api)

			r := httptest.NewRequest(http.MethodPost, route, strings.NewReader("{}"))
			w := httptest.NewRecorder()
			p.ServeHTTP(nil, w, r)

			// Each handler rejects a request with no session user.
			require.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}

	t.Run("unknown path", func(t *testing.T) {
		p := newTestPlugin(&plugintest.API{})
		r := httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil)
		r.Header.Set(headerUserID, "u1")
		w := httptest.NewRecorder()
		p.ServeHTTP(nil, w, r)
		require.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestHandleListTeams(t *testing.T) {
	t.Run("missing header", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		w := callHandler(p.handleListTeams, http.MethodGet, routeTeams, "", "")
		require.Equal(t, http.StatusUnauthorized, w.Code)
		api.AssertNotCalled(t, "GetTeamsForUser", mock.Anything)
	})

	t.Run("api error", func(t *testing.T) {
		api := &plugintest.API{}
		stubLogs(api)
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		api.On("GetTeamsForUser", "u1").Return(nil, testAppErr("db down"))

		w := callHandler(p.handleListTeams, http.MethodGet, routeTeams, "u1", "")
		require.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("lists the caller's teams", func(t *testing.T) {
		api := &plugintest.API{}
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		api.On("GetTeamsForUser", "u1").Return([]*model.Team{{Id: "t1", Name: "ops", DisplayName: "Ops"}}, nil)

		w := callHandler(p.handleListTeams, http.MethodGet, routeTeams, "u1", "")

		require.Equal(t, http.StatusOK, w.Code)
		require.JSONEq(t, `[{"id":"t1","name":"ops","display_name":"Ops"}]`, w.Body.String())
	})
}

func TestHandleListChannels(t *testing.T) {
	t.Run("missing header", func(t *testing.T) {
		p := newTestPlugin(&plugintest.API{})
		w := callHandler(p.handleListChannels, http.MethodGet, routeChannels+"?team_id=t1", "", "")
		require.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("requires team_id", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		w := callHandler(p.handleListChannels, http.MethodGet, routeChannels, "u1", "")
		require.Equal(t, http.StatusBadRequest, w.Code)
		api.AssertNotCalled(t, "GetChannelsForTeamForUser", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("api error", func(t *testing.T) {
		api := &plugintest.API{}
		stubLogs(api)
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		api.On("GetChannelsForTeamForUser", "t1", "u1", false).Return(nil, testAppErr("db down"))

		w := callHandler(p.handleListChannels, http.MethodGet, routeChannels+"?team_id=t1", "u1", "")
		require.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("keeps only public and private channels", func(t *testing.T) {
		api := &plugintest.API{}
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		api.On("GetChannelsForTeamForUser", "t1", "u1", false).Return([]*model.Channel{
			{Id: "c1", Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen},
			{Id: "c2", Name: "secret", DisplayName: "Secret", Type: model.ChannelTypePrivate},
			{Id: "c3", Name: "dm", Type: model.ChannelTypeDirect},
			{Id: "c4", Name: "gm", Type: model.ChannelTypeGroup},
		}, nil)

		w := callHandler(p.handleListChannels, http.MethodGet, routeChannels+"?team_id=t1", "u1", "")

		require.Equal(t, http.StatusOK, w.Code)
		require.JSONEq(t, `[
			{"id":"c1","name":"town-square","display_name":"Town Square","type":"O"},
			{"id":"c2","name":"secret","display_name":"Secret","type":"P"}
		]`, w.Body.String())
	})
}

func TestHandlePrefixes(t *testing.T) {
	t.Run("missing header", func(t *testing.T) {
		p := newTestPlugin(&plugintest.API{})
		w := callHandler(p.handlePrefixes, http.MethodGet, routePrefixes, "", "")
		require.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("lists configured prefixes", func(t *testing.T) {
		p := newTestPlugin(&plugintest.API{})
		p.setConfiguration(&configuration{prefixes: parsePrefixList("team-|Teams|8\nops-", func(string, ...any) {})})

		w := callHandler(p.handlePrefixes, http.MethodGet, routePrefixes, "u1", "")

		require.Equal(t, http.StatusOK, w.Code)
		require.JSONEq(t, `[
			{"prefix":"team-","description":"Teams","suffix_regex":"8"},
			{"prefix":"ops-","description":"","suffix_regex":""}
		]`, w.Body.String())
	})
}

func TestHandleRequestAdmin(t *testing.T) {
	t.Run("missing header", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		w := callHandler(p.handleRequestAdmin, http.MethodPost, routeRequestAdmin, "", `{}`)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		api.AssertNotCalled(t, "GetUser", mock.Anything)
	})

	t.Run("invalid body", func(t *testing.T) {
		p := newTestPlugin(&plugintest.API{})
		w := callHandler(p.handleRequestAdmin, http.MethodPost, routeRequestAdmin, "u1", `not json`)
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("unknown nominee", func(t *testing.T) {
		api := &plugintest.API{}
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		api.On("GetUserByUsername", "ghost").Return(nil, testAppErr("not found"))

		w := callHandler(p.handleRequestAdmin, http.MethodPost, routeRequestAdmin, "u1", `{"channel_id":"chX","nominees":["@ghost"]}`)

		require.Equal(t, "unknown user: ghost", jsonMap(t, w)["error"])
	})

	t.Run("validation error", func(t *testing.T) {
		api := &plugintest.API{}
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		api.On("GetUserByUsername", "nom").Return(&model.User{Id: "u_nom"}, nil)

		w := callHandler(p.handleRequestAdmin, http.MethodPost, routeRequestAdmin, "u1", `{"channel_id":"","nominees":["nom"]}`)

		require.Equal(t, "a channel is required", jsonMap(t, w)["error"])
	})

	t.Run("system admin promotes directly", func(t *testing.T) {
		api := &plugintest.API{}
		stubLogs(api)
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{})

		api.On("GetUserByUsername", "nom").Return(&model.User{Id: "u_nom"}, nil)
		api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
		api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nom"}, nil)
		api.On("GetChannel", "chX").Return(&model.Channel{Id: "chX", Name: "marketing", Type: model.ChannelTypeOpen}, nil)
		api.On("GetChannelMember", "chX", "u_nom").Return(&model.ChannelMember{}, nil)
		api.On("UpdateChannelMemberRoles", "chX", "u_nom", channelAdminRoles).Return(&model.ChannelMember{}, nil)
		api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

		w := callHandler(p.handleRequestAdmin, http.MethodPost, routeRequestAdmin, "admin1", `{"channel_id":"chX","nominees":["nom"]}`)

		require.Equal(t, "Promoted @nom to Channel Admin in ~marketing.", jsonMap(t, w)["message"])
	})
}

func TestHandleWebappCreate(t *testing.T) {
	t.Run("missing header", func(t *testing.T) {
		p := newTestPlugin(&plugintest.API{})
		w := callHandler(p.handleWebappCreate, http.MethodPost, routeCreate, "", `{}`)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("invalid body", func(t *testing.T) {
		p := newTestPlugin(&plugintest.API{})
		w := callHandler(p.handleWebappCreate, http.MethodPost, routeCreate, "u1", `[`)
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("unknown member", func(t *testing.T) {
		api := &plugintest.API{}
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		api.On("GetUserByUsername", "ghost").Return(nil, testAppErr("not found"))

		w := callHandler(p.handleWebappCreate, http.MethodPost, routeCreate, "u1", `{"members":["ghost"]}`)

		require.Equal(t, "unknown user: ghost", jsonMap(t, w)["error"])
	})

	t.Run("unknown channel admin", func(t *testing.T) {
		api := &plugintest.API{}
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		api.On("GetUserByUsername", "ghost").Return(nil, testAppErr("not found"))

		w := callHandler(p.handleWebappCreate, http.MethodPost, routeCreate, "u1", `{"admin_members":["ghost"]}`)

		require.Equal(t, "unknown user: ghost", jsonMap(t, w)["error"])
	})

	t.Run("submit error", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)

		w := callHandler(p.handleWebappCreate, http.MethodPost, routeCreate, "u1", `{"team_id":"team1","display_name":"  "}`)

		require.Equal(t, "a channel name is required", jsonMap(t, w)["error"])
		api.AssertNotCalled(t, "GetUser", mock.Anything)
	})

	t.Run("system admin creates directly", func(t *testing.T) {
		api := &plugintest.API{}
		stubLogs(api)
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{prefixes: []channelPrefix{{Prefix: "team-"}}})

		aliceID := model.NewId()
		api.On("GetUserByUsername", "alice").Return(&model.User{Id: aliceID}, nil)
		api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
		api.On("GetUser", aliceID).Return(&model.User{Id: aliceID, Username: "alice"}, nil)
		api.On("CreateChannel", mock.MatchedBy(func(c *model.Channel) bool {
			return c.TeamId == "team1" && c.Name == "team-marketing" && c.Type == model.ChannelTypePrivate
		})).Return(&model.Channel{Id: "ch1", Name: "team-marketing"}, nil)
		api.On("AddChannelMember", "ch1", "admin1").Return(&model.ChannelMember{}, nil)
		api.On("AddChannelMember", "ch1", aliceID).Return(&model.ChannelMember{}, nil)
		// A lone member with no named admins is promoted.
		api.On("UpdateChannelMemberRoles", "ch1", aliceID, channelAdminRoles).Return(&model.ChannelMember{}, nil)
		api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

		w := callHandler(p.handleWebappCreate, http.MethodPost, routeCreate, "admin1",
			`{"team_id":"team1","display_name":"Marketing","prefix":"team-","channel_type":"P","members":["@alice"]}`)

		require.Equal(t, "Created channel ~team-marketing.", jsonMap(t, w)["message"])
	})
}

func TestHandleDialogSubmit(t *testing.T) {
	dialogBody := func(t *testing.T, req model.SubmitDialogRequest) string {
		t.Helper()
		b, err := json.Marshal(req)
		require.NoError(t, err)
		return string(b)
	}

	t.Run("missing header", func(t *testing.T) {
		p := newTestPlugin(&plugintest.API{})
		w := callHandler(p.handleDialogSubmit, http.MethodPost, routeDialog, "", `{}`)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("invalid body", func(t *testing.T) {
		p := newTestPlugin(&plugintest.API{})
		w := callHandler(p.handleDialogSubmit, http.MethodPost, routeDialog, "u1", `{`)
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("cancelled does nothing", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)

		w := callHandler(p.handleDialogSubmit, http.MethodPost, routeDialog, "u1", dialogBody(t, model.SubmitDialogRequest{Cancelled: true}))

		require.Equal(t, http.StatusOK, w.Code)
		require.Empty(t, w.Body.String())
		api.AssertNotCalled(t, "GetUser", mock.Anything)
	})

	t.Run("submit error is returned to the dialog", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)

		w := callHandler(p.handleDialogSubmit, http.MethodPost, routeDialog, "u1", dialogBody(t, model.SubmitDialogRequest{
			State:      "team1",
			Submission: map[string]any{fieldDisplayName: ""},
		}))

		require.Equal(t, http.StatusOK, w.Code)
		var resp model.SubmitDialogResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Equal(t, "a channel name is required", resp.Error)
		api.AssertNotCalled(t, "SendEphemeralPost", mock.Anything, mock.Anything)
	})

	t.Run("falls back to TeamId and splits member lists", func(t *testing.T) {
		api := &plugintest.API{}
		stubLogs(api)
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{prefixes: []channelPrefix{{Prefix: "team-"}}})

		m1, m2 := model.NewId(), model.NewId()
		api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
		api.On("GetUser", m1).Return(&model.User{Id: m1, Username: "one"}, nil)
		api.On("GetUser", m2).Return(&model.User{Id: m2, Username: "two"}, nil)
		api.On("CreateChannel", mock.MatchedBy(func(c *model.Channel) bool {
			return c.TeamId == "team1" && c.Name == "team-ops"
		})).Return(&model.Channel{Id: "ch1", Name: "team-ops"}, nil)
		api.On("AddChannelMember", "ch1", "admin1").Return(&model.ChannelMember{}, nil)
		api.On("AddChannelMember", "ch1", m1).Return(&model.ChannelMember{}, nil)
		api.On("AddChannelMember", "ch1", m2).Return(&model.ChannelMember{}, nil)
		api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)
		api.On("SendEphemeralPost", "admin1", mock.MatchedBy(func(post *model.Post) bool {
			return post.ChannelId == "here" && post.Message == "Created channel ~team-ops."
		})).Return(&model.Post{})

		w := callHandler(p.handleDialogSubmit, http.MethodPost, routeDialog, "admin1", dialogBody(t, model.SubmitDialogRequest{
			TeamId:    "team1",
			ChannelId: "here",
			Submission: map[string]any{
				fieldDisplayName: "Ops",
				fieldPrefix:      "team-",
				fieldMembers:     " " + m1 + ", ," + m2 + " ",
				fieldAdmins:      "   ",
			},
		}))

		require.Equal(t, http.StatusOK, w.Code)
	})
}

func TestHandleAction_ErrorPaths(t *testing.T) {
	pending := &channelRequest{ID: "req1", RequesterID: "u_req", TeamID: "team1", Name: "team-x", DisplayName: "X", ChannelType: "O"}
	raw, err := json.Marshal(pending)
	require.NoError(t, err)

	t.Run("invalid body", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAction(w, r, true) }, http.MethodPost, routeApprove, "admin1", `{`)
		require.Equal(t, http.StatusBadRequest, w.Code)
		api.AssertNotCalled(t, "GetUser", mock.Anything)
	})

	cases := []struct {
		name  string
		setup func(api *plugintest.API)
		want  string
	}{
		{
			name: "acting user lookup fails",
			setup: func(api *plugintest.API) {
				api.On("GetUser", "admin1").Return(nil, testAppErr("gone"))
			},
			want: "Could not verify your identity",
		},
		{
			name: "store read fails",
			setup: func(api *plugintest.API) {
				api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
				api.On("KVGet", kvRequestPrefix+"req1").Return(nil, testAppErr("kv down"))
			},
			want: "Could not load that request.",
		},
		{
			name: "request no longer pending",
			setup: func(api *plugintest.API) {
				api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
				api.On("KVGet", kvRequestPrefix+"req1").Return(nil, nil)
			},
			want: alreadyHandledText,
		},
		{
			name: "claim fails",
			setup: func(api *plugintest.API) {
				api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
				api.On("GetUser", "u_req").Return(&model.User{Id: "u_req"}, nil)
				stubActiveTeamMember(api, "team1", "u_req")
				api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)
				api.On("KVCompareAndDelete", kvRequestPrefix+"req1", raw).Return(false, testAppErr("kv down"))
			},
			want: "Could not process that request.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &plugintest.API{}
			stubLogs(api)
			stubPendingLists(api)
			defer api.AssertExpectations(t)
			p := newTestPlugin(api)
			p.setConfiguration(approvableConfig())
			tc.setup(api)

			w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAction(w, r, true) },
				http.MethodPost, routeApprove, "admin1", actionBody(t, "req1"))

			resp := actionResponse(t, w)
			require.Contains(t, resp.EphemeralText, tc.want)
			require.Nil(t, resp.Update)
			api.AssertNotCalled(t, "CreateChannel", mock.Anything)
		})
	}
}

func TestHandleAction_WelcomeMentionsAddedMembersAndAdmins(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	stubPendingLists(api)
	stubActiveTeamMember(api, "team1", "u_req")
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(approvableConfig())

	pending := &channelRequest{
		ID: "req1", RequesterID: "u_req", TeamID: "team1", Name: "team-x", DisplayName: "X", ChannelType: "O",
		MemberIDs: []string{"u_mem"}, AdminMemberIDs: []string{"u_adm"},
	}
	raw, err := json.Marshal(pending)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("GetUser", "u_mem").Return(&model.User{Id: "u_mem", Username: "mem"}, nil)
	api.On("GetUser", "u_adm").Return(&model.User{Id: "u_adm", Username: "adm"}, nil)
	api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)
	api.On("KVCompareAndDelete", kvRequestPrefix+"req1", raw).Return(true, nil)
	api.On("CreateChannel", mock.Anything).Return(&model.Channel{Id: "ch1", Name: "team-x"}, nil)
	for _, id := range []string{"u_req", "u_mem", "u_adm"} {
		api.On("AddChannelMember", "ch1", id).Return(&model.ChannelMember{}, nil)
	}
	api.On("UpdateChannelMemberRoles", "ch1", "u_adm", channelAdminRoles).Return(&model.ChannelMember{}, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	var welcome *model.Post
	api.On("CreatePost", mock.Anything).Run(func(args mock.Arguments) {
		if post := args.Get(0).(*model.Post); post.ChannelId == "ch1" {
			welcome = post
		}
	}).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAction(w, r, true) },
		http.MethodPost, routeApprove, "admin1", actionBody(t, "req1"))

	resp := actionResponse(t, w)
	require.Contains(t, resp.Update.Message, "Approved by @admin")
	require.NotNil(t, welcome)
	require.Contains(t, welcome.Message, "requested by @req and approved by @admin")
	require.Contains(t, welcome.Message, "\nAdded: @mem")
	require.Contains(t, welcome.Message, "\nChannel admins: @adm")
}

func TestHandleAction_DenyWithUnknownRequesterAuditsByID(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	stubPendingLists(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AuditChannelID: "audit"})
	stubAuditChannel(api, "audit")

	pending := &channelRequest{ID: "req1", RequesterID: "u_req", TeamID: "team1", Name: "team-x", DisplayName: "X", ChannelType: "O"}
	raw, err := json.Marshal(pending)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
	api.On("GetUser", "u_req").Return(nil, testAppErr("deleted"))
	api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)
	api.On("KVCompareAndDelete", kvRequestPrefix+"req1", raw).Return(true, nil)
	// The requester's DM can't be opened, and the audit post fails; both
	// are logged and the denial still completes.
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(nil, testAppErr("no dm"))
	var audit *model.Post
	api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool { return post.ChannelId == "audit" })).
		Run(func(args mock.Arguments) { audit = args.Get(0).(*model.Post) }).
		Return(nil, testAppErr("audit down"))
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAction(w, r, false) },
		http.MethodPost, routeDeny, "admin1", actionBody(t, "req1"))

	resp := actionResponse(t, w)
	require.Contains(t, resp.Update.Message, "Denied by @")
	require.NotNil(t, audit)
	require.Contains(t, audit.Message, "from user u_req")
	api.AssertNotCalled(t, "CreateChannel", mock.Anything)
	api.AssertCalled(t, "LogWarn", "failed to open DM with requester", "user_id", "u_req", "error", mock.Anything)
	api.AssertCalled(t, "LogWarn", "audit post failed", "channel_id", "audit", "error", mock.Anything)
}

func TestHandleAdminAction_ErrorPaths(t *testing.T) {
	pending := &adminRequest{ID: "areq1", RequesterID: "u_req", ChannelID: "chX", NomineeIDs: []string{"u_nom"}}
	raw, err := json.Marshal(pending)
	require.NoError(t, err)

	t.Run("missing header", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAdminAction(w, r, true) }, http.MethodPost, routeApproveAdmin, "", `{}`)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		api.AssertNotCalled(t, "GetUser", mock.Anything)
	})

	t.Run("invalid body", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAdminAction(w, r, true) }, http.MethodPost, routeApproveAdmin, "admin1", `{`)
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	cases := []struct {
		name  string
		setup func(api *plugintest.API)
		want  string
	}{
		{
			name: "acting user lookup fails",
			setup: func(api *plugintest.API) {
				api.On("GetUser", "u1").Return(nil, testAppErr("gone"))
			},
			want: "Could not verify your identity",
		},
		{
			name: "store read fails",
			setup: func(api *plugintest.API) {
				api.On("GetUser", "u1").Return(sysadmin("u1"), nil)
				api.On("KVGet", kvAdminRequestPrefix+"areq1").Return(nil, testAppErr("kv down"))
			},
			want: "Could not load that request.",
		},
		{
			name: "not a Team Admin of the channel's team",
			setup: func(api *plugintest.API) {
				api.On("GetUser", "u1").Return(&model.User{Id: "u1", Username: "plain", Roles: "system_user"}, nil)
				api.On("KVGet", kvAdminRequestPrefix+"areq1").Return(raw, nil)
				api.On("GetChannel", "chX").Return(&model.Channel{Id: "chX", TeamId: "team1"}, nil)
				api.On("GetTeamMember", "team1", "u1").Return(&model.TeamMember{TeamId: "team1", UserId: "u1", Roles: "team_user"}, nil)
			},
			want: "You don't have permission",
		},
		{
			name: "claim fails",
			setup: func(api *plugintest.API) {
				api.On("GetUser", "u1").Return(sysadmin("u1"), nil)
				api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
				api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nom"}, nil)
				api.On("KVGet", kvAdminRequestPrefix+"areq1").Return(raw, nil)
				api.On("GetChannel", "chX").Return(&model.Channel{Id: "chX", Name: "marketing", TeamId: "team1"}, nil)
				api.On("KVCompareAndDelete", kvAdminRequestPrefix+"areq1", raw).Return(false, testAppErr("kv down"))
			},
			want: "Could not process that request.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &plugintest.API{}
			stubLogs(api)
			defer api.AssertExpectations(t)
			p := newTestPlugin(api)
			p.setConfiguration(&configuration{})
			tc.setup(api)

			w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAdminAction(w, r, true) },
				http.MethodPost, routeApproveAdmin, "u1", actionBody(t, "areq1"))

			resp := actionResponse(t, w)
			require.Contains(t, resp.EphemeralText, tc.want)
			require.Nil(t, resp.Update)
			api.AssertNotCalled(t, "UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestHandleAdminAction_DenyAfterChannelDeleted(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	stubPendingLists(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	pending := &adminRequest{ID: "areq1", RequesterID: "u_req", ChannelID: "chX", NomineeIDs: []string{"u_nom"}}
	raw, err := json.Marshal(pending)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nom"}, nil)
	api.On("KVGet", kvAdminRequestPrefix+"areq1").Return(raw, nil)
	api.On("GetChannel", "chX").Return(nil, testAppErr("deleted"))
	api.On("KVCompareAndDelete", kvAdminRequestPrefix+"areq1", raw).Return(true, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	var dm *model.Post
	api.On("CreatePost", mock.Anything).Run(func(args mock.Arguments) { dm = args.Get(0).(*model.Post) }).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAdminAction(w, r, false) },
		http.MethodPost, routeDenyAdmin, "admin1", actionBody(t, "areq1"))

	resp := actionResponse(t, w)
	require.Equal(t, "❌ Denied by @admin.", resp.Update.Message)
	require.NotNil(t, dm)
	require.Equal(t, "Your request to make @nom Channel Admin in the channel was denied.", dm.Message)
}

func TestHandleAdminAction_ApproveWithNoRemainingNominees(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	stubPendingLists(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	pending := &adminRequest{ID: "areq1", RequesterID: "u_req", ChannelID: "chX", NomineeIDs: []string{"u_nom"}}
	raw, err := json.Marshal(pending)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nom"}, nil)
	api.On("KVGet", kvAdminRequestPrefix+"areq1").Return(raw, nil)
	api.On("GetChannel", "chX").Return(&model.Channel{Id: "chX", Name: "marketing", TeamId: "team1"}, nil)
	api.On("KVCompareAndDelete", kvAdminRequestPrefix+"areq1", raw).Return(true, nil)
	// The nominee left the channel while the request was pending.
	api.On("GetChannelMember", "chX", "u_nom").Return(nil, testAppErr("not a member"))
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool { return post.ChannelId == "dm1" })).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAdminAction(w, r, true) },
		http.MethodPost, routeApproveAdmin, "admin1", actionBody(t, "areq1"))

	resp := actionResponse(t, w)
	require.Contains(t, resp.Update.Message, "nobody was promoted")
	api.AssertNotCalled(t, "UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything)
	// No promotion announcement in the channel.
	api.AssertNotCalled(t, "CreatePost", mock.MatchedBy(func(post *model.Post) bool { return post.ChannelId == "chX" }))
}

func TestResolvedPost_StripsButtonsFromBotPost(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	post := &model.Post{Id: "p1", UserId: "bot-user-id", ChannelId: "appch", Message: "@channel — review"}
	model.ParseMessageAttachment(post, []*model.MessageAttachment{{
		Title:   "Channel creation request",
		Fields:  []*model.MessageAttachmentField{{Title: "URL", Value: "team-x"}},
		Actions: p.approvalActions("req1", "/plugins/x"),
	}})
	api.On("GetPost", "p1").Return(post, nil)

	got := p.resolvedPost("p1", "done")

	require.Equal(t, "done", got.Message)
	require.Equal(t, "appch", got.ChannelId)
	attachments := got.Attachments()
	require.Len(t, attachments, 1)
	require.Empty(t, attachments[0].Actions)
	require.Equal(t, "done", attachments[0].Footer)
	require.Equal(t, "team-x", attachments[0].Fields[0].Value)
}

func TestCanApproveAdminRequest_Guards(t *testing.T) {
	plain := &model.User{Id: "u1", Roles: "system_user"}
	cases := []struct {
		name      string
		user      *model.User
		channelID string
		setup     func(api *plugintest.API)
		want      bool
	}{
		{name: "nil user", user: nil, channelID: "chX"},
		{name: "no channel", user: plain, channelID: ""},
		{
			name: "channel lookup fails", user: plain, channelID: "chX",
			setup: func(api *plugintest.API) { api.On("GetChannel", "chX").Return(nil, testAppErr("gone")) },
		},
		{
			name: "channel with no team", user: plain, channelID: "chX",
			setup: func(api *plugintest.API) { api.On("GetChannel", "chX").Return(&model.Channel{Id: "chX"}, nil) },
		},
		{
			name: "scheme Team Admin", user: plain, channelID: "chX", want: true,
			setup: func(api *plugintest.API) {
				api.On("GetChannel", "chX").Return(&model.Channel{Id: "chX", TeamId: "team1"}, nil)
				api.On("GetTeamMember", "team1", "u1").Return(&model.TeamMember{TeamId: "team1", UserId: "u1", SchemeAdmin: true}, nil)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &plugintest.API{}
			defer api.AssertExpectations(t)
			p := newTestPlugin(api)
			if tc.setup != nil {
				tc.setup(api)
			}
			require.Equal(t, tc.want, p.canApproveAdminRequest(tc.user, tc.channelID))
		})
	}
}

func TestHandleUserAutocomplete_EmptyQueryReturnsNothing(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)

	w := callHandler(p.handleUserAutocomplete, http.MethodGet, routeUserAutocomplete+"?q=%20%20&team_id=t1", "u1", "")

	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `[]`, w.Body.String())
	api.AssertNotCalled(t, "GetTeamMember", mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "SearchUsers", mock.Anything)
}

// mockAutocompleteCaller sets up u1 as an active, non-guest member of t1.
func mockAutocompleteCaller(api *plugintest.API) {
	api.On("GetTeamMember", "t1", "u1").Return(&model.TeamMember{TeamId: "t1", UserId: "u1"}, nil)
	api.On("GetUser", "u1").Return(&model.User{Id: "u1", Roles: "system_user"}, nil)
	api.On("GetConfig").Return(&model.Config{})
}

func TestHandleUserAutocomplete_SearchFailure(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	mockAutocompleteCaller(api)
	api.On("SearchUsers", mock.Anything).Return(nil, testAppErr("search down"))

	w := callHandler(p.handleUserAutocomplete, http.MethodGet, routeUserAutocomplete+"?q=al&team_id=t1", "u1", "")

	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleUserAutocomplete_CapsResultsAtTwenty(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	mockAutocompleteCaller(api)
	users := make([]*model.User, 30)
	for i := range users {
		users[i] = &model.User{Id: model.NewId(), Username: fmt.Sprintf("user%02d", i)}
	}
	api.On("SearchUsers", mock.MatchedBy(func(s *model.UserSearch) bool { return s.Limit == 50 })).Return(users, nil)

	w := callHandler(p.handleUserAutocomplete, http.MethodGet, routeUserAutocomplete+"?q=user&team_id=t1", "u1", "")

	require.Equal(t, http.StatusOK, w.Code)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 20)
	require.Equal(t, "user00", out[0]["username"])
	require.Equal(t, "user19", out[19]["username"])
}

// expiredMillis is a CreatedAt older than 30 days.
func expiredMillis() int64 {
	return model.GetMillis() - 31*millisPerDay
}

func TestHandleActionExpired(t *testing.T) {
	for _, approve := range []bool{true, false} {
		t.Run(fmt.Sprintf("approve=%v", approve), func(t *testing.T) {
			api := &plugintest.API{}
			stubLogs(api)
			defer api.AssertExpectations(t)
			p := newTestPlugin(api)
			config := approvableConfig()
			config.expiryDays = 30
			config.AuditChannelID = "audit"
			p.setConfiguration(config)
			stubAuditChannel(api, "audit")

			req := &channelRequest{ID: "req1", RequesterID: "u_req", TeamID: "team1", Name: "team-x", DisplayName: "X", ChannelType: "O", CreatedAt: expiredMillis()}
			raw, err := json.Marshal(req)
			require.NoError(t, err)
			lists := fakePendingLists(api)
			lists[pendingListKey(pendingKindChannel, "u_req")] = recordJSON(t, []pendingEntry{{ID: "req1", CreatedAt: req.CreatedAt, Key: "team1/team-x"}})

			api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
			api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
			api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)
			api.On("KVCompareAndDelete", kvRequestPrefix+"req1", raw).Return(true, nil)
			api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
			var dm, audit *model.Post
			api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool { return post.ChannelId == "dm1" })).
				Run(func(args mock.Arguments) { dm = args.Get(0).(*model.Post) }).Return(&model.Post{}, nil)
			api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool { return post.ChannelId == "audit" })).
				Run(func(args mock.Arguments) { audit = args.Get(0).(*model.Post) }).Return(&model.Post{}, nil)
			api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

			route := routeDeny
			if approve {
				route = routeApprove
			}
			w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAction(w, r, approve) },
				http.MethodPost, route, "admin1", actionBody(t, "req1"))

			resp := actionResponse(t, w)
			require.Equal(t, expiredText, resp.EphemeralText)
			require.Equal(t, "⌛ Expired: not reviewed within 30 days.", resp.Update.Message)
			require.Contains(t, dm.Message, "expired before it was reviewed")
			require.Equal(t, `EXPIRED: @admin closed channel request "X" from @req, which was not reviewed within 30 days`, audit.Message)
			require.Empty(t, lists)
			api.AssertNotCalled(t, "CreateChannel", mock.Anything)
			api.AssertNotCalled(t, "GetTeamMember", mock.Anything, mock.Anything)
		})
	}
}

func TestHandleActionExpiredClaim(t *testing.T) {
	req := &channelRequest{ID: "req1", RequesterID: "u_req", TeamID: "team1", Name: "team-x", CreatedAt: expiredMillis()}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		claimed bool
		appErr  *model.AppError
		want    string
	}{
		"lost":   {want: alreadyHandledText},
		"failed": {appErr: testAppErr("kv down"), want: "Could not process that request."},
	} {
		t.Run(name, func(t *testing.T) {
			api := &plugintest.API{}
			stubLogs(api)
			defer api.AssertExpectations(t)
			p := newTestPlugin(api)
			p.setConfiguration(&configuration{expiryDays: 30})

			api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
			api.On("GetUser", "u_req").Return(&model.User{Id: "u_req"}, nil)
			api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)
			api.On("KVCompareAndDelete", kvRequestPrefix+"req1", raw).Return(tc.claimed, tc.appErr)

			w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAction(w, r, true) },
				http.MethodPost, routeApprove, "admin1", actionBody(t, "req1"))

			resp := actionResponse(t, w)
			require.Equal(t, tc.want, resp.EphemeralText)
			require.Nil(t, resp.Update)
			api.AssertNotCalled(t, "CreatePost", mock.Anything)
		})
	}
}

func TestHandleActionApproveRechecks(t *testing.T) {
	notFound := model.NewAppError("test", "test.not_found", nil, "", http.StatusNotFound)
	cases := []struct {
		name         string
		requester    *model.User
		requesterErr *model.AppError
		member       *model.TeamMember
		memberErr    *model.AppError
		memberCalled bool
		reqName      string
		want         string
	}{
		{name: "requester not found", requesterErr: notFound, reqName: "team-x", want: "The requester's account is no longer active."},
		{name: "requester lookup fails", requesterErr: testAppErr("db down"), reqName: "team-x", want: approvalRetryText},
		{name: "requester deactivated", requester: &model.User{Id: "u_req", DeleteAt: 1}, reqName: "team-x", want: "The requester's account is no longer active."},
		{name: "deactivated System Admin requester", requester: &model.User{Id: "u_req", Roles: "system_user system_admin", DeleteAt: 1}, reqName: "team-x", want: "The requester's account is no longer active."},
		{name: "requester left the team", requester: &model.User{Id: "u_req"}, member: &model.TeamMember{DeleteAt: 1}, memberCalled: true, reqName: "team-x", want: "The requester is no longer a member of the team."},
		{name: "requester was never on the team", requester: &model.User{Id: "u_req"}, memberErr: notFound, memberCalled: true, reqName: "team-x", want: "The requester is no longer a member of the team."},
		{name: "membership lookup returns nothing", requester: &model.User{Id: "u_req"}, memberCalled: true, reqName: "team-x", want: "The requester is no longer a member of the team."},
		{name: "membership lookup fails", requester: &model.User{Id: "u_req"}, memberErr: testAppErr("db down"), memberCalled: true, reqName: "team-x", want: approvalRetryText},
		{name: "prefix no longer configured", requester: &model.User{Id: "u_req"}, member: &model.TeamMember{}, memberCalled: true, reqName: "proj-x", want: "The channel name no longer matches the configured prefixes."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &plugintest.API{}
			stubLogs(api)
			defer api.AssertExpectations(t)
			p := newTestPlugin(api)
			p.setConfiguration(approvableConfig())

			req := &channelRequest{ID: "req1", RequesterID: "u_req", TeamID: "team1", Name: tc.reqName, ChannelType: "O"}
			raw, err := json.Marshal(req)
			require.NoError(t, err)

			api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
			api.On("GetUser", "u_req").Return(tc.requester, tc.requesterErr)
			if tc.memberCalled {
				api.On("GetTeamMember", "team1", "u_req").Return(tc.member, tc.memberErr)
			}
			api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)

			w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAction(w, r, true) },
				http.MethodPost, routeApprove, "admin1", actionBody(t, "req1"))

			resp := actionResponse(t, w)
			require.Contains(t, resp.EphemeralText, tc.want)
			require.Nil(t, resp.Update, "the card keeps its buttons")
			api.AssertNotCalled(t, "KVCompareAndDelete", mock.Anything, mock.Anything)
		})
	}
}

func TestHandleActionApproveSystemAdminRequesterSkipsTeamCheck(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	stubPendingLists(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(approvableConfig())

	req := &channelRequest{ID: "req1", RequesterID: "admin2", TeamID: "team1", Name: "team-x", DisplayName: "X", ChannelType: "O"}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
	api.On("GetUser", "admin2").Return(sysadmin("admin2"), nil)
	api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)
	api.On("KVCompareAndDelete", kvRequestPrefix+"req1", raw).Return(true, nil)
	api.On("CreateChannel", mock.Anything).Return(&model.Channel{Id: "ch1", Name: "team-x"}, nil)
	api.On("AddChannelMember", "ch1", "admin2").Return(&model.ChannelMember{}, nil)
	api.On("GetDirectChannel", "admin2", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAction(w, r, true) },
		http.MethodPost, routeApprove, "admin1", actionBody(t, "req1"))

	resp := actionResponse(t, w)
	require.Contains(t, resp.Update.Message, "Channel ~team-x created")
	api.AssertNotCalled(t, "GetTeamMember", mock.Anything, mock.Anything)
}

func TestHandleActionFreesPendingSlot(t *testing.T) {
	entry := pendingEntry{ID: "req1", CreatedAt: 1, Key: "team1/team-x"}
	listKey := pendingListKey(pendingKindChannel, "u_req")

	for name, tc := range map[string]struct {
		approve    bool
		createErr  *model.AppError
		restoreErr *model.AppError
		freed      bool
	}{
		"approved":                       {approve: true, freed: true},
		"denied":                         {freed: true},
		"create fails, kept":             {approve: true, createErr: testAppErr("boom")},
		"create and restore fail, freed": {approve: true, createErr: testAppErr("boom"), restoreErr: testAppErr("kv down"), freed: true},
	} {
		t.Run(name, func(t *testing.T) {
			api := &plugintest.API{}
			stubLogs(api)
			defer api.AssertExpectations(t)
			p := newTestPlugin(api)
			p.setConfiguration(approvableConfig())

			req := &channelRequest{ID: "req1", RequesterID: "u_req", TeamID: "team1", Name: "team-x", DisplayName: "X", ChannelType: "O"}
			raw, err := json.Marshal(req)
			require.NoError(t, err)
			lists := fakePendingLists(api)
			lists[listKey] = recordJSON(t, []pendingEntry{entry})

			api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
			api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
			stubActiveTeamMember(api, "team1", "u_req")
			api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)
			api.On("KVCompareAndDelete", kvRequestPrefix+"req1", raw).Return(true, nil)
			if tc.approve {
				if tc.createErr != nil {
					api.On("CreateChannel", mock.Anything).Return(nil, tc.createErr)
					api.On("KVSet", kvRequestPrefix+"req1", mock.Anything).Return(tc.restoreErr)
				} else {
					api.On("CreateChannel", mock.Anything).Return(&model.Channel{Id: "ch1", Name: "team-x"}, nil)
					api.On("AddChannelMember", "ch1", "u_req").Return(&model.ChannelMember{}, nil)
				}
			}
			api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil).Maybe()
			api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil).Maybe()
			api.On("GetPost", "post1").Return(nil, testAppErr("no post")).Maybe()

			w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAction(w, r, tc.approve) },
				http.MethodPost, routeApprove, "admin1", actionBody(t, "req1"))

			resp := actionResponse(t, w)
			switch {
			case tc.restoreErr != nil:
				require.Contains(t, resp.EphemeralText, "ask the requester to submit it again")
			case tc.createErr != nil:
				require.Contains(t, resp.EphemeralText, "still pending")
			case tc.approve:
				require.Contains(t, resp.Update.Message, "Approved by @admin")
			default:
				require.Contains(t, resp.Update.Message, "Denied by @admin")
			}
			if tc.freed {
				require.Empty(t, lists)
			} else {
				require.Equal(t, []pendingEntry{entry}, decodeEntries(t, lists[listKey]))
			}
		})
	}
}

func TestHandleAdminActionExpired(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{expiryDays: 30})

	req := &adminRequest{ID: "areq1", RequesterID: "u_req", ChannelID: "ch1", NomineeIDs: []string{"u_nom"}, CreatedAt: expiredMillis()}
	raw, err := json.Marshal(req)
	require.NoError(t, err)
	lists := fakePendingLists(api)
	lists[pendingListKey(pendingKindAdmin, "u_req")] = recordJSON(t, []pendingEntry{{ID: "areq1", CreatedAt: req.CreatedAt, Key: "ch1/u_nom"}})

	api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nom"}, nil)
	api.On("KVGet", kvAdminRequestPrefix+"areq1").Return(raw, nil)
	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Name: "marketing", TeamId: "team1"}, nil)
	api.On("KVCompareAndDelete", kvAdminRequestPrefix+"areq1", raw).Return(true, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	var dm *model.Post
	api.On("CreatePost", mock.Anything).Run(func(args mock.Arguments) { dm = args.Get(0).(*model.Post) }).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAdminAction(w, r, true) },
		http.MethodPost, routeApproveAdmin, "admin1", actionBody(t, "areq1"))

	resp := actionResponse(t, w)
	require.Equal(t, expiredText, resp.EphemeralText)
	require.Contains(t, resp.Update.Message, "Expired")
	require.Equal(t, "Your request to make @nom Channel Admin in ~marketing expired before it was reviewed. Submit it again if you still need it.", dm.Message)
	require.Empty(t, lists)
	api.AssertNotCalled(t, "UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything)
}

func TestHandleAdminActionFreesPendingSlot(t *testing.T) {
	for _, approve := range []bool{true, false} {
		t.Run(fmt.Sprintf("approve=%v", approve), func(t *testing.T) {
			api := &plugintest.API{}
			stubLogs(api)
			defer api.AssertExpectations(t)
			p := newTestPlugin(api)
			p.setConfiguration(&configuration{})

			req := &adminRequest{ID: "areq1", RequesterID: "u_req", ChannelID: "ch1", NomineeIDs: []string{"u_nom"}}
			raw, err := json.Marshal(req)
			require.NoError(t, err)
			lists := fakePendingLists(api)
			lists[pendingListKey(pendingKindAdmin, "u_req")] = recordJSON(t, []pendingEntry{{ID: "areq1", CreatedAt: 1, Key: "ch1/u_nom"}})

			api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
			api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
			api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nom"}, nil)
			api.On("KVGet", kvAdminRequestPrefix+"areq1").Return(raw, nil)
			api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Name: "marketing", TeamId: "team1"}, nil)
			api.On("KVCompareAndDelete", kvAdminRequestPrefix+"areq1", raw).Return(true, nil)
			api.On("GetChannelMember", "ch1", "u_nom").Return(&model.ChannelMember{}, nil).Maybe()
			api.On("UpdateChannelMemberRoles", "ch1", "u_nom", channelAdminRoles).Return(&model.ChannelMember{}, nil).Maybe()
			api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
			api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)
			api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

			callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAdminAction(w, r, approve) },
				http.MethodPost, routeApproveAdmin, "admin1", actionBody(t, "areq1"))

			require.Empty(t, lists)
		})
	}
}

func TestHandleActionExpiredChecksPermissionsFirst(t *testing.T) {
	expired := &channelRequest{ID: "req1", RequesterID: "u_ta", TeamID: "team1", Name: "team-x", DisplayName: "X", CreatedAt: expiredMillis()}
	raw, err := json.Marshal(expired)
	require.NoError(t, err)

	t.Run("non-approver", func(t *testing.T) {
		api := &plugintest.API{}
		stubLogs(api)
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{expiryDays: 30})
		api.On("GetUser", "u1").Return(&model.User{Id: "u1", Roles: "system_user"}, nil)

		resp := actionResponse(t, callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAction(w, r, false) },
			http.MethodPost, routeDeny, "u1", actionBody(t, "req1")))

		require.Contains(t, resp.EphemeralText, "permission")
		api.AssertNotCalled(t, "KVGet", mock.Anything)
		api.AssertNotCalled(t, "KVCompareAndDelete", mock.Anything, mock.Anything)
	})

	t.Run("requester approving their own expired request", func(t *testing.T) {
		api := &plugintest.API{}
		stubLogs(api)
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{expiryDays: 30, AllowTeamAdminApprovers: true, ApprovalTeam: "appteam"})
		api.On("GetUser", "u_ta").Return(&model.User{Id: "u_ta", Username: "lead", Roles: "system_user"}, nil)
		api.On("GetTeamMember", "appteam", "u_ta").Return(&model.TeamMember{Roles: "team_user team_admin"}, nil)
		api.On("KVGet", kvRequestPrefix+"req1").Return(raw, nil)

		resp := actionResponse(t, callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAction(w, r, true) },
			http.MethodPost, routeApprove, "u_ta", actionBody(t, "req1")))

		require.Contains(t, resp.EphemeralText, "your own request")
		api.AssertNotCalled(t, "KVCompareAndDelete", mock.Anything, mock.Anything)
	})
}

func TestHandleAdminActionExpiredChecksPermissionsFirst(t *testing.T) {
	expired := &adminRequest{ID: "areq1", RequesterID: "u_ta", ChannelID: "chX", NomineeIDs: []string{"u_ta"}, CreatedAt: expiredMillis()}
	raw, err := json.Marshal(expired)
	require.NoError(t, err)

	t.Run("non-approver", func(t *testing.T) {
		api := &plugintest.API{}
		stubLogs(api)
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{expiryDays: 30})
		lists := fakePendingLists(api)
		lists[pendingListKey(pendingKindAdmin, "u_ta")] = recordJSON(t, []pendingEntry{{ID: "areq1", CreatedAt: expired.CreatedAt, Key: "chX/u_ta"}})
		api.On("GetUser", "u1").Return(&model.User{Id: "u1", Roles: "system_user"}, nil)
		api.On("KVGet", kvAdminRequestPrefix+"areq1").Return(raw, nil)
		api.On("GetChannel", "chX").Return(&model.Channel{Id: "chX", TeamId: "team1"}, nil)
		api.On("GetTeamMember", "team1", "u1").Return(&model.TeamMember{Roles: "team_user"}, nil)

		resp := actionResponse(t, callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAdminAction(w, r, false) },
			http.MethodPost, routeDenyAdmin, "u1", actionBody(t, "areq1")))

		require.Contains(t, resp.EphemeralText, "permission")
		require.Nil(t, resp.Update)
		require.Len(t, decodeEntries(t, lists[pendingListKey(pendingKindAdmin, "u_ta")]), 1)
		api.AssertNotCalled(t, "KVCompareAndDelete", kvAdminRequestPrefix+"areq1", mock.Anything)
		api.AssertNotCalled(t, "CreatePost", mock.Anything)
	})

	t.Run("requester approving their own expired request", func(t *testing.T) {
		api := &plugintest.API{}
		stubLogs(api)
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{expiryDays: 30})
		api.On("GetUser", "u_ta").Return(&model.User{Id: "u_ta", Username: "lead", Roles: "system_user"}, nil)
		api.On("KVGet", kvAdminRequestPrefix+"areq1").Return(raw, nil)
		api.On("GetChannel", "chX").Return(&model.Channel{Id: "chX", TeamId: "team1"}, nil)
		api.On("GetTeamMember", "team1", "u_ta").Return(&model.TeamMember{Roles: "team_user team_admin"}, nil)

		resp := actionResponse(t, callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAdminAction(w, r, true) },
			http.MethodPost, routeApproveAdmin, "u_ta", actionBody(t, "areq1")))

		require.Contains(t, resp.EphemeralText, "your own request")
		api.AssertNotCalled(t, "KVCompareAndDelete", mock.Anything, mock.Anything)
	})
}

func TestHandleAdminActionExpiredDeny(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{expiryDays: 30, AuditChannelID: "audit"})
	stubAuditChannel(api, "audit")

	req := &adminRequest{ID: "areq1", RequesterID: "u_req", ChannelID: "ch1", NomineeIDs: []string{"u_nom"}, CreatedAt: expiredMillis()}
	raw, err := json.Marshal(req)
	require.NoError(t, err)
	fakePendingLists(api)

	api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nom"}, nil)
	api.On("KVGet", kvAdminRequestPrefix+"areq1").Return(raw, nil)
	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Name: "marketing", TeamId: "team1"}, nil)
	api.On("KVCompareAndDelete", kvAdminRequestPrefix+"areq1", raw).Return(true, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	var audit *model.Post
	api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool { return post.ChannelId == "audit" })).
		Run(func(args mock.Arguments) { audit = args.Get(0).(*model.Post) }).Return(&model.Post{}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	resp := actionResponse(t, callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAdminAction(w, r, false) },
		http.MethodPost, routeDenyAdmin, "admin1", actionBody(t, "areq1")))

	require.Equal(t, expiredText, resp.EphemeralText)
	require.Equal(t, "CHANNEL ADMIN EXPIRED: @admin closed the request for @nom in ~marketing from @req, which was not reviewed within 30 days", audit.Message)
}

func TestHandleAdminActionExpiredClaim(t *testing.T) {
	req := &adminRequest{ID: "areq1", RequesterID: "u_req", ChannelID: "ch1", NomineeIDs: []string{"u_nom"}, CreatedAt: expiredMillis()}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		appErr *model.AppError
		want   string
	}{
		"lost":   {want: alreadyHandledText},
		"failed": {appErr: testAppErr("kv down"), want: "Could not process that request."},
	} {
		t.Run(name, func(t *testing.T) {
			api := &plugintest.API{}
			stubLogs(api)
			defer api.AssertExpectations(t)
			p := newTestPlugin(api)
			p.setConfiguration(&configuration{expiryDays: 30})

			api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
			api.On("GetUser", "u_req").Return(&model.User{Id: "u_req"}, nil)
			api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nom"}, nil)
			api.On("KVGet", kvAdminRequestPrefix+"areq1").Return(raw, nil)
			api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Name: "marketing", TeamId: "team1"}, nil)
			api.On("KVCompareAndDelete", kvAdminRequestPrefix+"areq1", raw).Return(false, tc.appErr)

			resp := actionResponse(t, callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAdminAction(w, r, true) },
				http.MethodPost, routeApproveAdmin, "admin1", actionBody(t, "areq1")))

			require.Equal(t, tc.want, resp.EphemeralText)
			require.Nil(t, resp.Update)
			api.AssertNotCalled(t, "CreatePost", mock.Anything)
		})
	}
}
