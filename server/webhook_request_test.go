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

func TestValidateWebhookInput(t *testing.T) {
	require.Error(t, validateWebhookInput(webhookRequestInput{ChannelID: "", DisplayName: "X"}))
	require.Error(t, validateWebhookInput(webhookRequestInput{ChannelID: "c1", DisplayName: ""}))
	require.NoError(t, validateWebhookInput(webhookRequestInput{ChannelID: "c1", DisplayName: "Deploy hook"}))
}

func TestSubmitWebhookRequest_PendingStoresAndPosts(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{ApprovalTeam: "ops", ApprovalChannel: "approvals"})

	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("GetChannel", "c1").Return(&model.Channel{Id: "c1", Name: "deploys"}, nil)
	api.On("GetChannelMember", "c1", "u_req").Return(&model.ChannelMember{}, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)
	api.On("GetChannelByNameForTeamName", "ops", "approvals", false).Return(&model.Channel{Id: "ch_approvals"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	msg, err := p.submitWebhookRequest(webhookRequestInput{RequesterID: "u_req", ChannelID: "c1", DisplayName: "Deploy hook"})
	require.NoError(t, err)
	require.Contains(t, msg, "requires approval")
}

func TestSubmitWebhookRequest_NonMemberRejected(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("GetChannel", "c1").Return(&model.Channel{Id: "c1", Name: "deploys"}, nil)
	api.On("GetChannelMember", "c1", "u_req").Return(nil, testAppErr("not a member"))

	_, err := p.submitWebhookRequest(webhookRequestInput{RequesterID: "u_req", ChannelID: "c1", DisplayName: "Deploy hook"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "member of the channel")
}

func TestHandleWebhookAction_FirstApprovalIsPartial(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	req := &webhookRequest{ID: "w1", RequesterID: "u_req", ChannelID: "c1", ChannelName: "deploys", DisplayName: "Deploy hook"}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Username: "admin", Roles: model.SystemAdminRoleId}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("KVGet", kvWebhookRequestPrefix+"w1").Return(raw, nil)
	api.On("KVCompareAndSet", kvWebhookRequestPrefix+"w1", raw, mock.Anything).Return(true, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(&model.Post{Id: "post1"}, nil)

	r := httptest.NewRequest(http.MethodPost, routeApproveWebhook, strings.NewReader(actionBody(t, "w1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleWebhookAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	api.AssertCalled(t, "KVCompareAndSet", kvWebhookRequestPrefix+"w1", raw, mock.Anything)
	api.AssertNotCalled(t, "KVCompareAndDelete", mock.Anything, mock.Anything)
}

func TestHandleWebhookAction_NonApproverRejected(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	req := &webhookRequest{ID: "w1", RequesterID: "u_req", ChannelID: "c1", ChannelName: "deploys", DisplayName: "Deploy hook"}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "u_nobody").Return(&model.User{Id: "u_nobody", Username: "nobody"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("KVGet", kvWebhookRequestPrefix+"w1").Return(raw, nil)

	r := httptest.NewRequest(http.MethodPost, routeApproveWebhook, strings.NewReader(actionBody(t, "w1")))
	r.Header.Set(headerUserID, "u_nobody")
	w := httptest.NewRecorder()
	p.handleWebhookAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	var resp model.PostActionIntegrationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(t, resp.EphemeralText, "permission")
}
