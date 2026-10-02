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

	req := &botTokenRequest{
		ID: "b1", RequesterID: "u_req", Username: "deploy-bot",
		twoStepState: twoStepState{SecurityApproverID: "u_sec", SecurityApprovedAt: 1},
	}
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
	require.Contains(t, body, "nom1")              // promoted
	require.Contains(t, body, "Could not promote") // failure surfaced
	require.Contains(t, body, "nom2")              // the one that failed
}

// TestHandleTeamAction_RequesterNotToldTeamAdminWhenPromotionFails covers Bug 4:
// if the team is created but the requester's Team Admin promotion fails, the
// requester must not be told they're a Team Admin.
func TestHandleTeamAction_RequesterNotToldTeamAdminWhenPromotionFails(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	req := &teamRequest{ID: "t1", RequesterID: "u_req", Name: "mktg", DisplayName: "Mktg", TeamType: teamTypeOpen}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Username: "admin", Roles: model.SystemAdminRoleId}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req", Email: "req@example.com"}, nil)
	api.On("KVGet", kvTeamRequestPrefix+"t1").Return(raw, nil)
	api.On("KVCompareAndDelete", kvTeamRequestPrefix+"t1", raw).Return(true, nil)
	api.On("CreateTeam", mock.Anything).Return(&model.Team{Id: "team1", Name: "mktg", DisplayName: "Mktg"}, nil)
	api.On("CreateTeamMember", "team1", "u_req").Return(&model.TeamMember{}, nil)
	// Promotion of the requester fails.
	api.On("UpdateTeamMemberRoles", "team1", "u_req", teamAdminRoleString).Return(nil, testAppErr("role update failed"))
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	r := httptest.NewRequest(http.MethodPost, routeApproveTeam, strings.NewReader(actionBody(t, "t1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleTeamAction(w, r, true)

	require.Equal(t, http.StatusOK, w.Code)
	// The requester DM must NOT claim Team Admin; it should say it couldn't be set.
	api.AssertCalled(t, "CreatePost", mock.MatchedBy(func(post *model.Post) bool {
		return strings.Contains(post.Message, "couldn't set you as a Team Admin")
	}))
	api.AssertNotCalled(t, "CreatePost", mock.MatchedBy(func(post *model.Post) bool {
		return strings.Contains(post.Message, "You're now a Team Admin")
	}))
}

// TestHandleRequestAdmin_DisabledToggleRejects covers Bug 5: channel-admin
// requests must be gated by their own toggle. When disabled, the submission is
// rejected server-side and nothing is stored.
func TestHandleRequestAdmin_DisabledToggleRejects(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{AllowChannelAdminRequests: false})

	r := httptest.NewRequest(http.MethodPost, routeRequestAdmin, strings.NewReader(`{"channel_id":"ch1","nominees":["bob"]}`))
	r.Header.Set(headerUserID, "u_req")
	w := httptest.NewRecorder()
	p.handleRequestAdmin(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "disabled")
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
}

// TestHandleConfig_ReflectsToggles verifies the webapp-facing /config endpoint
// returns the correct per-type flags the webapp uses to show/hide entry points,
// for several toggle combinations. The webapp hides buttons/menus based purely
// on these flags, so they must be accurate.
func TestHandleConfig_ReflectsToggles(t *testing.T) {
	cases := []struct {
		name string
		cfg  *configuration
		want map[string]bool
	}{
		{
			name: "all on",
			cfg:  &configuration{AllowChannelRequests: true, AllowChannelAdminRequests: true, AllowTeamRequests: true, AllowWebhookRequests: true},
			want: map[string]bool{"channel": true, "channel_admin": true, "team": true, "webhook": true},
		},
		{
			name: "all off",
			cfg:  &configuration{},
			want: map[string]bool{"channel": false, "channel_admin": false, "team": false, "webhook": false},
		},
		{
			name: "channel-admin off only",
			cfg:  &configuration{AllowChannelRequests: true, AllowChannelAdminRequests: false, AllowTeamRequests: true, AllowWebhookRequests: true},
			want: map[string]bool{"channel": true, "channel_admin": false, "team": true, "webhook": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &plugintest.API{}
			stubLogs(api)
			defer api.AssertExpectations(t)
			p := newTestPlugin(api)
			p.setConfiguration(tc.cfg)

			r := httptest.NewRequest(http.MethodGet, routeConfig, nil)
			r.Header.Set(headerUserID, "u1")
			w := httptest.NewRecorder()
			p.handleConfig(w, r)

			require.Equal(t, http.StatusOK, w.Code)
			var got map[string]bool
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			require.Equal(t, tc.want, got)
		})
	}
}

// TestWebhookRequestIdempotencyMatching covers Bug 6's matching rule: the
// per-request marker lets a retry recognize the hook it already created (same
// channel + same marker) so it reuses it instead of creating a duplicate, while
// not matching a different request's hook or a same-marker hook in another
// channel.
func TestWebhookRequestIdempotencyMatching(t *testing.T) {
	markerA := webhookRequestMarker("reqA")
	markerB := webhookRequestMarker("reqB")
	require.NotEqual(t, markerA, markerB)

	// Same channel + carries this request's marker -> match (the retry case).
	require.True(t, webhookMatchesRequest(&model.IncomingWebhook{ChannelId: "ch1", Description: "deploy " + markerA}, "ch1", markerA))
	// A different request's hook -> no match (no cross-request reuse).
	require.False(t, webhookMatchesRequest(&model.IncomingWebhook{ChannelId: "ch1", Description: "deploy " + markerB}, "ch1", markerA))
	// Same marker but a different channel -> no match.
	require.False(t, webhookMatchesRequest(&model.IncomingWebhook{ChannelId: "ch2", Description: "deploy " + markerA}, "ch1", markerA))
	// Hook with no marker (created outside the plugin) -> no match.
	require.False(t, webhookMatchesRequest(&model.IncomingWebhook{ChannelId: "ch1", Description: "manual hook"}, "ch1", markerA))
	require.False(t, webhookMatchesRequest(nil, "ch1", markerA))
}

// webhookDeliveryFailureHookID is the hook id the create seam hands back in the
// delivery-failure fixture, so tests can assert the right hook is cleaned up.
const webhookDeliveryFailureHookID = "hook-abc"

// setupWebhookDeliveryFailure brings a webhook request to the point of final
// (system) approval and makes the secret-delivery DM fail. The acting user is a
// System Admin (fills the system step; the security step is already recorded),
// and the created webhook is faked via the createWebhookFn test seam so the
// Client4 path isn't needed. The caller sets deleteWebhookFn to control whether
// removal succeeds, then calls handleWebhookAction with acting user "admin1"
// and request id "w1".
func setupWebhookDeliveryFailure(t *testing.T, api *plugintest.API, p *Plugin) {
	t.Helper()

	req := &webhookRequest{
		ID: "w1", RequesterID: "u_req", ChannelID: "chan1", ChannelName: "town", DisplayName: "Deploy Hook",
		twoStepState: twoStepState{SecurityApproverID: "u_sec", SecurityApprovedAt: 1},
	}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Username: "admin", Roles: model.SystemAdminRoleId}, nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("KVGet", kvWebhookRequestPrefix+"w1").Return(raw, nil)
	api.On("KVCompareAndDelete", kvWebhookRequestPrefix+"w1", raw).Return(true, nil)
	// DM delivery of the secret URL fails.
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(nil, testAppErr("dm blocked"))
	api.On("GetPost", "post1").Return(nil, testAppErr("no post"))

	// Fake the REST-backed hook creation so the delivery-failure branch runs
	// with a real url+hookID without a live server.
	p.createWebhookFn = func(_ *webhookRequest) (string, string, error) {
		return "https://mm.example.com/hooks/abc", webhookDeliveryFailureHookID, nil
	}
}

func approveWebhookRequest(t *testing.T, p *Plugin) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, routeApproveWebhook, strings.NewReader(actionBody(t, "w1")))
	r.Header.Set(headerUserID, "admin1")
	w := httptest.NewRecorder()
	p.handleWebhookAction(w, r, true)
	return w
}

// TestHandleWebhookAction_DeliveryFailureRemovesHookAndReportsFailure covers the
// webhook mirror of the bot-token secret-delivery failure: the hook is created
// on final approval but the URL DM to the requester fails. An undelivered hook
// URL is an unowned secret endpoint, so the handler must delete the hook and
// report the failure — never claim a clean success.
func TestHandleWebhookAction_DeliveryFailureRemovesHookAndReportsFailure(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	setupWebhookDeliveryFailure(t, api, p)

	deleted := ""
	p.deleteWebhookFn = func(id string) error {
		deleted = id
		return nil
	}

	w := approveWebhookRequest(t, p)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, webhookDeliveryFailureHookID, deleted, "the undeliverable hook must be deleted")
	body := w.Body.String()
	require.Contains(t, body, "removed")
	require.NotContains(t, body, "sent privately")
}

// TestHandleWebhookAction_DeliveryFailureWhenRemovalAlsoFails covers the worse
// case: delivery fails AND the hook can't be removed. The card must say the
// hook could not be removed automatically (so an admin cleans it up), not that
// it was removed.
func TestHandleWebhookAction_DeliveryFailureWhenRemovalAlsoFails(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	setupWebhookDeliveryFailure(t, api, p)

	p.deleteWebhookFn = func(_ string) error {
		return testAppErr("delete failed")
	}

	w := approveWebhookRequest(t, p)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "could not be removed automatically")
	require.NotContains(t, body, "sent privately")
}
