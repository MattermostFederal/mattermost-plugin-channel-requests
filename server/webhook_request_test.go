package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mmAPIStub builds a minimal httptest.Server that responds to the Mattermost
// REST API endpoints used by createWebhookForRequest. The server writes back
// a synthetic webhook object so callers can verify the success message.
func mmAPIStub(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v4/hooks/incoming":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var hook model.IncomingWebhook
			_ = json.NewDecoder(r.Body).Decode(&hook)
			hook.Id = "stub-incoming-id"
			if hook.DisplayName == "" {
				hook.DisplayName = "Test Incoming"
			}
			_ = json.NewEncoder(w).Encode(hook)
		case "/api/v4/hooks/outgoing":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var hook model.OutgoingWebhook
			_ = json.NewDecoder(r.Body).Decode(&hook)
			hook.Id = "stub-outgoing-id"
			if hook.DisplayName == "" {
				hook.DisplayName = "Test Outgoing"
			}
			_ = json.NewEncoder(w).Encode(hook)
		default:
			http.NotFound(w, r)
		}
	}))
}

// stubWebhookChannel sets up the API mock expectations for
// ensureWebhookApprovalChannel (called by submitWebhookRequest).
func stubWebhookChannel(api *plugintest.API, teamName, channelID string) {
	api.On("GetTeamByName", teamName).Return(&model.Team{Id: "team-id", DisplayName: "App Team"}, nil)
	api.On("GetChannelByName", "team-id", "mattermost-webhook-requests", false).
		Return(&model.Channel{Id: channelID}, nil)
}

// --- submitWebhookRequest tests -------------------------------------------------

func TestSubmitWebhookRequest_RequiresChannel(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	_, err := p.submitWebhookRequest("u1", webhookTypeIncoming, "", "My Hook", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "target channel is required")
}

func TestSubmitWebhookRequest_RequiresDisplayName(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	_, err := p.submitWebhookRequest("u1", webhookTypeIncoming, "ch1", "  ", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "display name is required")
}

func TestSubmitWebhookRequest_OutgoingRequiresCallbackURL(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	_, err := p.submitWebhookRequest("u1", webhookTypeOutgoing, "ch1", "Hook", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "callback URL")
}

func TestSubmitWebhookRequest_RejectsUnknownType(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	_, err := p.submitWebhookRequest("u1", "magic", "ch1", "Hook", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown webhook type")
}

// TestSubmitWebhookRequest_SystemAdminQueuesNotBypasses verifies that unlike
// other request types, System Admins are NOT given a creation bypass for
// webhooks (the admin session token is only available at approve-time).
func TestSubmitWebhookRequest_SystemAdminQueuesNotBypasses(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{ApprovalTeam: "appteam"})

	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", TeamId: "team-id", Name: "alerts"}, nil)
	api.On("GetUser", "u_admin").Return(&model.User{Id: "u_admin", Username: "admin", Roles: "system_user system_admin"}, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)

	stubWebhookChannel(api, "appteam", "wh-ch")

	api.On("GetUsers", mock.Anything).Return([]*model.User{{Id: "sys1", Username: "root"}}, nil)
	api.On("GetUser", "sys1").Return(&model.User{Id: "sys1", Username: "root"}, nil)
	api.On("CreatePost", mock.MatchedBy(func(p *model.Post) bool { return p.ChannelId == "wh-ch" })).
		Return(&model.Post{Id: "approval-1"}, nil)
	api.On("GetDirectChannel", "u_admin", "bot-user-id").Return(&model.Channel{Id: "dm-ch"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{Id: "dm-1"}, nil)

	msg, err := p.submitWebhookRequest("u_admin", webhookTypeIncoming, "ch1", "Monitoring Alerts", "", "")

	require.NoError(t, err)
	// The message should point the admin to the approval channel, not confirm creation.
	require.Contains(t, msg, "webhook approval channel")
	// No REST API call made — webhook not created yet.
	api.AssertNotCalled(t, "CreateIncomingWebhook", mock.Anything)
}

func TestSubmitWebhookRequest_RegularUserQueuesInWebhookChannel(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{ApprovalTeam: "appteam"})

	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", TeamId: "team-id", Name: "alerts"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "reqer", Roles: "system_user"}, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)

	stubWebhookChannel(api, "appteam", "wh-ch")

	api.On("GetUsers", mock.Anything).Return([]*model.User{{Id: "sys1", Username: "root"}}, nil)
	api.On("GetUser", "sys1").Return(&model.User{Id: "sys1", Username: "root"}, nil)

	var approvalPost *model.Post
	api.On("CreatePost", mock.MatchedBy(func(p *model.Post) bool { return p.ChannelId == "wh-ch" })).
		Run(func(args mock.Arguments) { approvalPost = args.Get(0).(*model.Post) }).
		Return(&model.Post{Id: "approval-1"}, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm-ch"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{Id: "dm-1"}, nil)

	msg, err := p.submitWebhookRequest("u_req", webhookTypeIncoming, "ch1", "Monitoring Alerts", "watch for alerts", "")

	require.NoError(t, err)
	require.Contains(t, msg, "submitted for approval")
	require.NotNil(t, approvalPost)
	require.Equal(t, "wh-ch", approvalPost.ChannelId)
	api.AssertCalled(t, "KVSet", mock.Anything, mock.Anything)
}

func TestSubmitWebhookRequest_OutgoingQueuesWithCallbackURL(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{ApprovalTeam: "appteam"})

	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", TeamId: "team-id", Name: "ops"}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "reqer", Roles: "system_user"}, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)

	stubWebhookChannel(api, "appteam", "wh-ch")

	api.On("GetUsers", mock.Anything).Return([]*model.User{{Id: "sys1", Username: "root"}}, nil)
	api.On("GetUser", "sys1").Return(&model.User{Id: "sys1", Username: "root"}, nil)
	api.On("CreatePost", mock.MatchedBy(func(p *model.Post) bool { return p.ChannelId == "wh-ch" })).
		Return(&model.Post{Id: "approval-1"}, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm-ch"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{Id: "dm-1"}, nil)

	msg, err := p.submitWebhookRequest("u_req", webhookTypeOutgoing, "ch1", "Incident Relay", "sends to PagerDuty", "https://pd.example.com/hook")

	require.NoError(t, err)
	require.Contains(t, msg, "submitted for approval")
}

// --- createWebhookForRequest tests ---------------------------------------------

func TestCreateWebhookForRequest_IncomingReturnsURL(t *testing.T) {
	ts := mmAPIStub(t)
	defer ts.Close()

	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetConfig").Return(&model.Config{
		ServiceSettings: model.ServiceSettings{SiteURL: strPtr(ts.URL)},
	})
	// createWebhookForRequest creates a short-lived session for the admin and
	// revokes it immediately after the REST API call.
	api.On("CreateSession", mock.Anything).Return(&model.Session{Id: "sess1", Token: "tok1"}, nil)
	api.On("RevokeSession", "sess1").Return(nil)

	msg, err := p.createWebhookForRequest(&webhookRequest{
		WebhookType: webhookTypeIncoming,
		ChannelID:   "ch1",
		TeamID:      "team1",
		DisplayName: "Monitoring Alerts",
	}, "test-admin-user-id")

	require.NoError(t, err)
	require.Contains(t, msg, "stub-incoming-id")
	require.Contains(t, msg, "/hooks/")
}

func TestCreateWebhookForRequest_OutgoingReturnsConfirmation(t *testing.T) {
	ts := mmAPIStub(t)
	defer ts.Close()

	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetConfig").Return(&model.Config{
		ServiceSettings: model.ServiceSettings{SiteURL: strPtr(ts.URL)},
	})
	api.On("CreateSession", mock.Anything).Return(&model.Session{Id: "sess2", Token: "tok2"}, nil)
	api.On("RevokeSession", "sess2").Return(nil)

	msg, err := p.createWebhookForRequest(&webhookRequest{
		WebhookType: webhookTypeOutgoing,
		ChannelID:   "ch1",
		TeamID:      "team1",
		DisplayName: "Incident Relay",
		CallbackURL: "https://pd.example.com/hook",
	}, "test-admin-user-id")

	require.NoError(t, err)
	require.Contains(t, msg, "approved and is now active")
}

func TestCreateWebhookForRequest_FailsWithEmptyToken(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	_, err := p.createWebhookForRequest(&webhookRequest{WebhookType: webhookTypeIncoming}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "admin session token is missing")
}

func TestCreateWebhookForRequest_FailsWithNoSiteURL(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetConfig").Return(&model.Config{
		ServiceSettings: model.ServiceSettings{SiteURL: nil},
	})

	_, err := p.createWebhookForRequest(&webhookRequest{WebhookType: webhookTypeIncoming}, "tok")
	require.Error(t, err)
	require.Contains(t, err.Error(), "site URL is not configured")
}

func TestCreateWebhookForRequest_APIErrorPropagates(t *testing.T) {
	// Server that always returns 403 — simulates the admin not having
	// the required webhook creation permission.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Forbidden","status_code":403}`, http.StatusForbidden)
	}))
	defer ts.Close()

	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetConfig").Return(&model.Config{
		ServiceSettings: model.ServiceSettings{SiteURL: strPtr(ts.URL)},
	})
	api.On("CreateSession", mock.Anything).Return(&model.Session{Id: "sess3", Token: "bad-token"}, nil)
	api.On("RevokeSession", "sess3").Return(nil)

	_, err := p.createWebhookForRequest(&webhookRequest{
		WebhookType: webhookTypeIncoming,
		ChannelID:   "ch1",
		TeamID:      "team1",
		DisplayName: "Alerts",
	}, "bad-user-id")

	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create incoming webhook")
}
