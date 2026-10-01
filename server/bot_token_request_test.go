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

func TestValidateBotTokenInput(t *testing.T) {
	require.Error(t, validateBotTokenInput(botTokenRequestInput{Username: ""}))
	require.Error(t, validateBotTokenInput(botTokenRequestInput{Username: "Bad Name!"}))
	require.NoError(t, validateBotTokenInput(botTokenRequestInput{Username: "deploy-bot"}))

	// Length must be the advertised 3-22, not model.IsValidUsername's 1-64.
	require.Error(t, validateBotTokenInput(botTokenRequestInput{Username: "ab"}), "2 chars should be rejected")
	require.Error(t, validateBotTokenInput(botTokenRequestInput{Username: strings.Repeat("b", 23)}), "23 chars should be rejected")
	require.NoError(t, validateBotTokenInput(botTokenRequestInput{Username: strings.Repeat("b", 22)}), "22 chars is the max")
}

func TestSubmitBotTokenRequest_SysAdminBypassCreatesBotAndDMsToken(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	api.On("GetUser", "u_admin").Return(&model.User{Id: "u_admin", Username: "admin", Roles: model.SystemAdminRoleId}, nil)
	api.On("CreateBot", mock.MatchedBy(func(b *model.Bot) bool { return b.Username == "deploy-bot" })).
		Return(&model.Bot{UserId: "botid", Username: "deploy-bot"}, nil)
	api.On("CreateUserAccessToken", mock.Anything).Return(&model.UserAccessToken{Token: "tok-123"}, nil)
	api.On("GetDirectChannel", "u_admin", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	msg, err := p.submitBotTokenRequest(botTokenRequestInput{RequesterID: "u_admin", Username: "deploy-bot"})
	require.NoError(t, err)
	require.Contains(t, msg, "sent to you")
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
}

func TestSubmitBotTokenRequest_PendingStoresAndPosts(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{ApprovalTeam: "ops", ApprovalChannel: "approvals"})

	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)
	api.On("GetChannelByNameForTeamName", "ops", "approvals", false).Return(&model.Channel{Id: "ch_approvals"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	msg, err := p.submitBotTokenRequest(botTokenRequestInput{RequesterID: "u_req", Username: "deploy-bot"})
	require.NoError(t, err)
	require.Contains(t, msg, "requires approval")
	api.AssertNotCalled(t, "CreateBot", mock.Anything)
}

func TestHandleBotTokenAction_SecondApprovalCompletesAndIssuesToken(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	// Empty attribute names: the system step is satisfied by the System Admin
	// role, and the security step is already done in the stored request.
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
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	r := httptest.NewRequest(http.MethodPost, routeApproveBotToken, strings.NewReader(actionBody(t, "b1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleBotTokenAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	api.AssertCalled(t, "CreateBot", mock.Anything)
	api.AssertCalled(t, "CreateUserAccessToken", mock.Anything)
}

func TestHandleBotTokenAction_FirstApprovalIsPartial(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	req := &botTokenRequest{ID: "b1", RequesterID: "u_req", Username: "deploy-bot"}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	// System Admin gives the SYSTEM approval first; the security step remains.
	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Username: "admin", Roles: model.SystemAdminRoleId}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("KVGet", kvBotTokenRequestPrefix+"b1").Return(raw, nil)
	api.On("KVCompareAndSet", kvBotTokenRequestPrefix+"b1", raw, mock.Anything).Return(true, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(&model.Post{Id: "post1"}, nil)

	r := httptest.NewRequest(http.MethodPost, routeApproveBotToken, strings.NewReader(actionBody(t, "b1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleBotTokenAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	// Partial approval must NOT create the bot yet, and must persist via CAS.
	api.AssertCalled(t, "KVCompareAndSet", kvBotTokenRequestPrefix+"b1", raw, mock.Anything)
	api.AssertNotCalled(t, "CreateBot", mock.Anything)
	api.AssertNotCalled(t, "KVCompareAndDelete", mock.Anything, mock.Anything)
}

func TestHandleBotTokenAction_NonApproverRejected(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	req := &botTokenRequest{ID: "b1", RequesterID: "u_req", Username: "deploy-bot"}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "u_nobody").Return(&model.User{Id: "u_nobody", Username: "nobody"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("KVGet", kvBotTokenRequestPrefix+"b1").Return(raw, nil)

	r := httptest.NewRequest(http.MethodPost, routeApproveBotToken, strings.NewReader(actionBody(t, "b1")))
	r.Header.Set(headerUserID, "u_nobody")
	w := httptest.NewRecorder()
	p.handleBotTokenAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(t, resp.EphemeralText, "permission")
	api.AssertNotCalled(t, "KVCompareAndDelete", mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything)
}
