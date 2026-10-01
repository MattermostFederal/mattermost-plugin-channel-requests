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

// Test_BUG_OrphanBotOnTokenFailure documents a confirmed defect:
// createBotTokenForRequest creates the bot FIRST, then issues the access token.
// If CreateBot succeeds but CreateUserAccessToken fails, the just-created bot is
// left orphaned — there is no cleanup. Because the bot now owns the requested
// username, every retry of the (restored) pending request fails at CreateBot
// with "username already taken", so the request becomes permanently
// unrecoverable and a privileged bot account is leaked.
//
// It asserts that the orphaned bot is permanently deleted on token-issuance
// failure so the username is freed and a retry can succeed.
func TestCreateBotTokenForRequest_DeletesOrphanBotOnTokenFailure(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	api.On("CreateBot", mock.Anything).Return(&model.Bot{UserId: "orphan-bot-id", Username: "deploy-bot"}, nil)
	api.On("CreateUserAccessToken", mock.Anything).Return(nil, testAppErr("token service down"))
	// The orphaned bot must be cleaned up so a retry can re-create it.
	api.On("PermanentDeleteBot", "orphan-bot-id").Return(nil)

	req := &botTokenRequest{ID: "b1", RequesterID: "u_req", Username: "deploy-bot"}
	token, bot, err := p.createBotTokenForRequest(req)
	require.Error(t, err)
	require.Empty(t, token)
	require.Nil(t, bot)
	api.AssertCalled(t, "PermanentDeleteBot", "orphan-bot-id")
}

// TestHandleBotTokenAction_DeliveryFailureRemovesBotAndReportsFailure covers the
// secret-delivery failure path: the bot + token are created, but the DM to the
// requester fails. The only copy of the token would otherwise be lost while the
// card claimed success, leaving a live bot nobody can use. The handler must
// remove the bot and report the failure instead of a false success.
func TestHandleBotTokenAction_DeliveryFailureRemovesBotAndReportsFailure(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	req := &botTokenRequest{ID: "b1", RequesterID: "u_req", Username: "deploy-bot",
		twoStepState: twoStepState{SecurityApproverID: "u_sec", SecurityApprovedAt: 1}}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Username: "admin", Roles: model.SystemAdminRoleId}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("KVGet", kvBotTokenRequestPrefix+"b1").Return(raw, nil)
	api.On("KVCompareAndDelete", kvBotTokenRequestPrefix+"b1", raw).Return(true, nil)
	api.On("CreateBot", mock.Anything).Return(&model.Bot{UserId: "botid", Username: "deploy-bot"}, nil)
	api.On("CreateUserAccessToken", mock.Anything).Return(&model.UserAccessToken{Token: "tok-123"}, nil)
	// DM delivery fails.
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(nil, testAppErr("dm blocked"))
	// The undeliverable bot must be removed.
	api.On("PermanentDeleteBot", "botid").Return(nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	r := httptest.NewRequest(http.MethodPost, routeApproveBotToken, strings.NewReader(actionBody(t, "b1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleBotTokenAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	api.AssertCalled(t, "PermanentDeleteBot", "botid")
	// The card must report the failure (and removal), not a clean success.
	require.Contains(t, w.Body.String(), "removed")
	require.NotContains(t, w.Body.String(), "sent privately")
}

// TestHandleTeamAdminAction_PartialPromotionReportedTruthfully covers Bug 3:
// when one of two nominees cannot be promoted, the card must name only the
// nominee actually promoted and flag the failure — it must not claim both were
// promoted.
func TestHandleTeamAdminAction_PartialPromotionReportedTruthfully(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	req := &teamAdminRequest{ID: "ta1", RequesterID: "u_req", TeamID: "team1", NomineeIDs: []string{"n1", "n2"}}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Username: "admin", Roles: model.SystemAdminRoleId}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("GetUser", "n1").Return(&model.User{Id: "n1", Username: "nom1"}, nil)
	api.On("GetUser", "n2").Return(&model.User{Id: "n2", Username: "nom2"}, nil)
	api.On("GetTeam", "team1").Return(&model.Team{Id: "team1", Name: "ops", DisplayName: "Ops"}, nil)
	api.On("KVGet", kvTeamAdminRequestPrefix+"ta1").Return(raw, nil)
	api.On("KVCompareAndDelete", kvTeamAdminRequestPrefix+"ta1", raw).Return(true, nil)
	// n1 promotes cleanly; n2's role update fails.
	api.On("CreateTeamMember", "team1", "n1").Return(&model.TeamMember{}, nil)
	api.On("UpdateTeamMemberRoles", "team1", "n1", teamAdminRoleString).Return(&model.TeamMember{}, nil)
	api.On("CreateTeamMember", "team1", "n2").Return(&model.TeamMember{}, nil)
	api.On("UpdateTeamMemberRoles", "team1", "n2", teamAdminRoleString).Return(nil, testAppErr("deactivated"))
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	r := httptest.NewRequest(http.MethodPost, routeApproveTeamAdmin, strings.NewReader(actionBody(t, "ta1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleTeamAdminAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "nom1")                 // promoted
	require.Contains(t, body, "Could not promote")    // failure surfaced
	require.Contains(t, body, "nom2")                 // the one that failed
}
