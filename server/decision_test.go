package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Request IDs that pass model.IsValidId.
var (
	testChannelRequestID = model.NewId()
	testAdminRequestID   = model.NewId()
)

// runCommandAs runs a slash command as userID.
func runCommandAs(p *Plugin, userID, command string) *model.CommandResponse {
	resp, appErr := p.ExecuteCommand(nil, &model.CommandArgs{Command: command, UserId: userID, TeamId: "team1", ChannelId: "chX"})
	if appErr != nil {
		panic(appErr)
	}
	return resp
}

// approvalCard is a bot-authored approval post with buttons.
func approvalCard(id string) *model.Post {
	post := &model.Post{Id: id, UserId: "bot-user-id", Message: "header"}
	model.ParseMessageAttachment(post, []*model.MessageAttachment{{
		Title:   "Channel creation request",
		Footer:  requestIDFooter("x"),
		Actions: []*model.PostAction{{Id: "approve", Name: "Approve"}},
	}})
	return post
}

// decisionFixture is a plugin backed by fakeKV with a stored channel
// request from u_req, recorded on cards post1 and post2.
func decisionFixture(t *testing.T) (*plugintest.API, *Plugin, map[string][]byte) {
	t.Helper()
	api := &plugintest.API{}
	stubLogs(api)
	t.Cleanup(func() { api.AssertExpectations(t) })
	p := newTestPlugin(api)
	p.setConfiguration(approvableConfig())
	store := fakeKV(api)

	req := &channelRequest{ID: testChannelRequestID, RequesterID: "u_req", TeamID: "team1", Name: "team-x", DisplayName: "X", ChannelType: "O", CreatedAt: model.GetMillis()}
	store[kvRequestPrefix+req.ID] = recordJSON(t, req)
	store[pendingListKey(pendingKindChannel, "u_req")] = recordJSON(t, []pendingEntry{{ID: req.ID, CreatedAt: req.CreatedAt, Key: channelRequestKey(req)}})
	store[kvApprovalPostsPrefix+req.ID] = recordJSON(t, []string{"post1", "post2"})

	api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil).Maybe()
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil).Maybe()
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil).Maybe()
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil).Maybe()
	return api, p, store
}

func TestExecuteDecision_NeedsOneValidID(t *testing.T) {
	for _, command := range []string{
		"/channel-request approve",
		"/channel-request approve not-an-id",
		"/channel-request deny " + testChannelRequestID + " extra",
	} {
		t.Run(command, func(t *testing.T) {
			api := &plugintest.API{}
			defer api.AssertExpectations(t)
			p := newTestPlugin(api)

			resp := runCommandAs(p, "admin1", command)

			require.Equal(t, model.CommandResponseTypeEphemeral, resp.ResponseType)
			// Nothing is mocked, so any KV read would have panicked.
			require.Contains(t, resp.Text, "Give the request ID from the approval card")
		})
	}
}

func TestExecuteDecision_UnknownIDIsAlreadyHandled(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	fakeKV(api)
	api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)

	resp := runCommandAs(p, "admin1", "/channel-request deny "+model.NewId())

	require.Equal(t, alreadyHandledText, resp.Text)
}

func TestExecuteDecision_LookupFailure(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	api.On("KVGet", kvRequestPrefix+testChannelRequestID).Return(nil, testAppErr("kv down"))

	resp := runCommandAs(p, "admin1", "/channel-request approve "+testChannelRequestID)

	require.Equal(t, "Could not load that request.", resp.Text)
}

func TestExecuteDecision_ApproveResolvesEveryCard(t *testing.T) {
	api, p, store := decisionFixture(t)
	stubActiveTeamMember(api, "team1", "u_req")
	api.On("CreateChannel", mock.Anything).Return(&model.Channel{Id: "ch1", Name: "team-x"}, nil)
	api.On("AddChannelMember", "ch1", "u_req").Return(&model.ChannelMember{}, nil)
	api.On("GetPost", "post1").Return(approvalCard("post1"), nil)
	api.On("GetPost", "post2").Return(approvalCard("post2"), nil)
	var updated []*model.Post
	api.On("UpdatePost", mock.Anything).Run(func(args mock.Arguments) {
		updated = append(updated, args.Get(0).(*model.Post))
	}).Return(&model.Post{}, nil)

	resp := runCommandAs(p, "admin1", "/channel-request approve "+testChannelRequestID)

	require.Equal(t, "✅ Approved by @admin. Channel ~team-x created.", resp.Text)
	require.Len(t, updated, 2)
	for _, post := range updated {
		require.Equal(t, resp.Text, post.Message)
		attachments := post.Attachments()
		require.Len(t, attachments, 1)
		require.Empty(t, attachments[0].Actions)
		require.Equal(t, resp.Text, attachments[0].Footer)
	}
	require.NotContains(t, store, kvRequestPrefix+testChannelRequestID)
	require.NotContains(t, store, kvApprovalPostsPrefix+testChannelRequestID)
	require.NotContains(t, store, pendingListKey(pendingKindChannel, "u_req"))
}

func TestExecuteDecision_RefusalsLeaveTheRequestPending(t *testing.T) {
	t.Run("not an approver", func(t *testing.T) {
		api, p, store := decisionFixture(t)
		api.On("GetUser", "u_other").Return(&model.User{Id: "u_other", Roles: "system_user"}, nil)

		resp := runCommandAs(p, "u_other", "/channel-request approve "+testChannelRequestID)

		require.Contains(t, resp.Text, "You don't have permission to approve or deny channel requests")
		require.Contains(t, store, kvRequestPrefix+testChannelRequestID)
		require.Contains(t, store, kvApprovalPostsPrefix+testChannelRequestID)
	})

	t.Run("own request", func(t *testing.T) {
		// A Team Admin approver who filed the request.
		api, p, store := decisionFixture(t)
		config := approvableConfig()
		config.AllowTeamAdminApprovers = true
		config.ApprovalTeam = "appteam"
		p.setConfiguration(config)
		api.On("GetTeamMember", "appteam", "u_req").Return(&model.TeamMember{TeamId: "appteam", UserId: "u_req", SchemeAdmin: true}, nil)

		resp := runCommandAs(p, "u_req", "/channel-request approve "+testChannelRequestID)

		require.Equal(t, "You can't approve your own request. Another approver has to review it.", resp.Text)
		require.Contains(t, store, kvRequestPrefix+testChannelRequestID)
	})
}

func TestExecuteDecision_DenyAdminRequest(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(approvableConfig())
	store := fakeKV(api)

	req := &adminRequest{ID: testAdminRequestID, RequesterID: "u_req", ChannelID: "ch1", NomineeIDs: []string{"u_nom"}, CreatedAt: model.GetMillis()}
	store[kvAdminRequestPrefix+req.ID] = recordJSON(t, req)
	store[kvApprovalPostsPrefix+req.ID] = recordJSON(t, []string{"post1"})

	api.On("GetUser", "admin1").Return(sysadmin("admin1"), nil)
	api.On("GetUser", "u_req").Return(&model.User{Id: "u_req", Username: "req"}, nil)
	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Name: "town", TeamId: "team1"}, nil)
	api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nom"}, nil)
	api.On("GetDirectChannel", "u_req", "bot-user-id").Return(&model.Channel{Id: "dm1"}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)
	api.On("GetPost", "post1").Return(approvalCard("post1"), nil)
	api.On("UpdatePost", mock.Anything).Return(&model.Post{}, nil)

	resp := runCommandAs(p, "admin1", "/channel-request deny "+testAdminRequestID)

	require.Equal(t, "❌ Denied by @admin.", resp.Text)
	api.AssertNumberOfCalls(t, "UpdatePost", 1)
	api.AssertNotCalled(t, "UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything)
	require.NotContains(t, store, kvAdminRequestPrefix+testAdminRequestID)
	require.NotContains(t, store, kvApprovalPostsPrefix+testAdminRequestID)
}

func TestExecuteDecision_ExpiredResolvesCards(t *testing.T) {
	api, p, store := decisionFixture(t)
	config := approvableConfig()
	config.expiryDays = 30
	p.setConfiguration(config)
	req := &channelRequest{ID: testChannelRequestID, RequesterID: "u_req", TeamID: "team1", Name: "team-x", DisplayName: "X", CreatedAt: expiredMillis()}
	store[kvRequestPrefix+req.ID] = recordJSON(t, req)
	api.On("GetPost", mock.Anything).Return(approvalCard("p"), nil)
	api.On("UpdatePost", mock.MatchedBy(func(post *model.Post) bool {
		return post.Message == "⌛ Expired: not reviewed within 30 days."
	})).Return(&model.Post{}, nil)

	resp := runCommandAs(p, "admin1", "/channel-request approve "+testChannelRequestID)

	require.Equal(t, expiredText, resp.Text)
	api.AssertNumberOfCalls(t, "UpdatePost", 2)
	require.NotContains(t, store, kvApprovalPostsPrefix+testChannelRequestID)
	api.AssertNotCalled(t, "CreateChannel", mock.Anything)
}

func TestHandleAction_ResolvesTheOtherCards(t *testing.T) {
	api, p, store := decisionFixture(t)
	api.On("GetPost", "post1").Return(approvalCard("post1"), nil)
	api.On("GetPost", "post2").Return(approvalCard("post2"), nil)
	var updatedIDs []string
	api.On("UpdatePost", mock.Anything).Run(func(args mock.Arguments) {
		updatedIDs = append(updatedIDs, args.Get(0).(*model.Post).Id)
	}).Return(&model.Post{}, nil)

	// actionBody clicks post1, which the response updates.
	w := callHandler(func(w http.ResponseWriter, r *http.Request) { p.handleAction(w, r, false) },
		http.MethodPost, routeDeny, "admin1", actionBody(t, testChannelRequestID))

	resp := actionResponse(t, w)
	require.Equal(t, "❌ Denied by @admin.", resp.Update.Message)
	require.Equal(t, []string{"post2"}, updatedIDs)
	require.NotContains(t, store, kvApprovalPostsPrefix+testChannelRequestID)
}

func TestResolveApprovalCards(t *testing.T) {
	t.Run("skips missing, foreign and failing posts", func(t *testing.T) {
		api := &plugintest.API{}
		stubLogs(api)
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		store := fakeKV(api)
		store[kvApprovalPostsPrefix+"r1"] = recordJSON(t, []string{"gone", "foreign", "fails", "ok"})

		api.On("GetPost", "gone").Return(nil, testAppErr("not found"))
		api.On("GetPost", "foreign").Return(&model.Post{Id: "foreign", UserId: "someone"}, nil)
		api.On("GetPost", "fails").Return(approvalCard("fails"), nil)
		api.On("GetPost", "ok").Return(approvalCard("ok"), nil)
		api.On("UpdatePost", mock.MatchedBy(func(post *model.Post) bool { return post.Id == "fails" })).Return(nil, testAppErr("update failed"))
		api.On("UpdatePost", mock.MatchedBy(func(post *model.Post) bool { return post.Id == "ok" })).Return(&model.Post{}, nil)

		p.resolveApprovalCards("r1", "done", "")

		api.AssertNumberOfCalls(t, "UpdatePost", 2)
		api.AssertCalled(t, "LogWarn", "failed to resolve approval card", "request_id", "r1", "post_id", "fails", "error", mock.Anything)
		api.AssertCalled(t, "LogWarn", "failed to load approval card", "request_id", "r1", "post_id", "gone", "error", mock.Anything)
		require.NotContains(t, store, kvApprovalPostsPrefix+"r1")
	})

	t.Run("nothing recorded", func(t *testing.T) {
		api := &plugintest.API{}
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		api.On("KVGet", kvApprovalPostsPrefix+"r1").Return(nil, nil)

		// KVDelete isn't mocked, so deleting would have panicked.
		p.resolveApprovalCards("r1", "done", "")
	})

	for name, kv := range map[string]func(api *plugintest.API){
		"load fails": func(api *plugintest.API) {
			api.On("KVGet", kvApprovalPostsPrefix+"r1").Return(nil, testAppErr("kv down"))
		},
		"unreadable": func(api *plugintest.API) { api.On("KVGet", kvApprovalPostsPrefix+"r1").Return([]byte("not json"), nil) },
	} {
		t.Run(name, func(t *testing.T) {
			api := &plugintest.API{}
			stubLogs(api)
			defer api.AssertExpectations(t)
			p := newTestPlugin(api)
			kv(api)

			p.resolveApprovalCards("r1", "done", "")

			api.AssertCalled(t, "LogWarn", "failed to resolve approval cards", "request_id", "r1", "error", mock.Anything)
			api.AssertNotCalled(t, "GetPost", mock.Anything)
		})
	}

	t.Run("delete fails", func(t *testing.T) {
		api := &plugintest.API{}
		stubLogs(api)
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		api.On("KVGet", kvApprovalPostsPrefix+"r1").Return(recordJSON(t, []string{}), nil)
		api.On("KVDelete", kvApprovalPostsPrefix+"r1").Return(testAppErr("kv down"))

		p.resolveApprovalCards("r1", "done", "")

		api.AssertCalled(t, "LogWarn", "failed to delete approval posts record", "request_id", "r1", "error", mock.Anything)
	})
}

func TestSaveApprovalPosts(t *testing.T) {
	t.Run("saves the IDs while the request is pending", func(t *testing.T) {
		api := &plugintest.API{}
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		store := fakeKV(api)
		store[kvAdminRequestPrefix+"r1"] = []byte("{}")

		p.saveApprovalPosts(pendingKindAdmin, "r1", []string{"a", "b"})

		var got []string
		require.NoError(t, json.Unmarshal(store[kvApprovalPostsPrefix+"r1"], &got))
		require.Equal(t, []string{"a", "b"}, got)
	})

	t.Run("resolves the cards when the request was decided meanwhile", func(t *testing.T) {
		api := &plugintest.API{}
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		store := fakeKV(api)
		api.On("GetPost", "a").Return(approvalCard("a"), nil)
		api.On("UpdatePost", mock.MatchedBy(func(post *model.Post) bool {
			return post.Id == "a" && post.Message == alreadyHandledText
		})).Return(&model.Post{}, nil)

		p.saveApprovalPosts(pendingKindChannel, "r1", []string{"a"})

		api.AssertNumberOfCalls(t, "UpdatePost", 1)
		require.NotContains(t, store, kvApprovalPostsPrefix+"r1")
	})

	t.Run("a failed check is only logged", func(t *testing.T) {
		api := &plugintest.API{}
		stubLogs(api)
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		api.On("KVSet", kvApprovalPostsPrefix+"r1", mock.Anything).Return(nil)
		api.On("KVGet", kvRequestPrefix+"r1").Return(nil, testAppErr("kv down"))

		p.saveApprovalPosts(pendingKindChannel, "r1", []string{"a"})

		api.AssertCalled(t, "LogWarn", "failed to check request after recording approval posts", "request_id", "r1", "error", mock.Anything)
	})

	t.Run("a failure is only logged", func(t *testing.T) {
		api := &plugintest.API{}
		stubLogs(api)
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		api.On("KVSet", kvApprovalPostsPrefix+"r1", mock.Anything).Return(testAppErr("kv down"))

		p.saveApprovalPosts(pendingKindChannel, "r1", []string{"a"})

		api.AssertCalled(t, "LogWarn", "failed to record approval posts", "request_id", "r1", "error", mock.Anything)
	})
}

func TestSubmitRequest_RecordsApprovalPostsAndShowsTheID(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	stubPendingLists(api)
	stubSubmittedRequestExists(api, kvRequestPrefix)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(approvalConfig())
	mockMemberRequester(api)
	api.On("GetTeam", "team1").Return(&model.Team{Id: "team1", Name: "eng"}, nil)
	api.On("GetChannel", "appch").Return(&model.Channel{Id: "appch", TeamId: "appteam"}, nil)
	var requestKey string
	api.On("KVSet", isRequestRecordKey, mock.Anything).Run(func(args mock.Arguments) { requestKey = args.String(0) }).Return(nil)
	var card *model.Post
	api.On("CreatePost", mock.Anything).Run(func(args mock.Arguments) { card = args.Get(0).(*model.Post) }).Return(&model.Post{Id: "card1"}, nil)

	_, err := p.submitRequest(requestInput{RequesterID: "u1", TeamID: "team1", DisplayName: "Marketing", Prefix: "team-"})

	require.NoError(t, err)
	id := requestKey[len(kvRequestPrefix):]
	require.Equal(t, "Request ID: "+id, card.Attachments()[0].Footer)
	api.AssertCalled(t, "KVSet", kvApprovalPostsPrefix+id, []byte(`["card1"]`))
}

func TestSubmitAdminRequest_RecordsApprovalPostsAndShowsTheID(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	stubPendingLists(api)
	stubSubmittedRequestExists(api, kvAdminRequestPrefix)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(approvalConfig())
	api.On("GetUser", "u1").Return(&model.User{Id: "u1", Username: "req", Roles: "system_user"}, nil)
	api.On("GetChannelMember", "ch1", mock.Anything).Return(&model.ChannelMember{}, nil)
	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Name: "town", TeamId: "team1", Type: model.ChannelTypeOpen}, nil)
	api.On("GetChannel", "appch").Return(&model.Channel{Id: "appch", TeamId: "appteam"}, nil)
	api.On("GetTeam", "team1").Return(&model.Team{Id: "team1", Name: "eng"}, nil)
	api.On("GetUsers", mock.Anything).Return([]*model.User{}, nil)
	api.On("GetTeamMembers", "team1", 0, 200).Return([]*model.TeamMember{}, nil)
	api.On("GetUser", "u_nom").Return(&model.User{Id: "u_nom", Username: "nom"}, nil)
	var requestKey string
	api.On("KVSet", mock.MatchedBy(func(key string) bool {
		return len(key) > len(kvAdminRequestPrefix) && key[:len(kvAdminRequestPrefix)] == kvAdminRequestPrefix
	}), mock.Anything).
		Run(func(args mock.Arguments) { requestKey = args.String(0) }).Return(nil)
	var card *model.Post
	api.On("CreatePost", mock.Anything).Run(func(args mock.Arguments) { card = args.Get(0).(*model.Post) }).Return(&model.Post{Id: "card1"}, nil)

	_, err := p.submitAdminRequest("u1", "ch1", []string{"u_nom"})

	require.NoError(t, err)
	id := requestKey[len(kvAdminRequestPrefix):]
	require.Equal(t, "Request ID: "+id, card.Attachments()[0].Footer)
	api.AssertCalled(t, "KVSet", kvApprovalPostsPrefix+id, []byte(`["card1"]`))
}

func TestDecision_CreateFailureKeepsTheCards(t *testing.T) {
	api, p, store := decisionFixture(t)
	stubActiveTeamMember(api, "team1", "u_req")
	api.On("CreateChannel", mock.Anything).Return(nil, testAppErr("name taken"))

	resp := runCommandAs(p, "admin1", "/channel-request approve "+testChannelRequestID)

	require.Contains(t, resp.Text, "The request is still pending")
	require.Contains(t, store, kvRequestPrefix+testChannelRequestID)
	require.Contains(t, store, kvApprovalPostsPrefix+testChannelRequestID)
	api.AssertNotCalled(t, "UpdatePost", mock.Anything)
}

func TestDecision_LostRequestResolvesTheCards(t *testing.T) {
	api, p, store := decisionFixture(t)
	stubActiveTeamMember(api, "team1", "u_req")
	api.On("CreateChannel", mock.Anything).Return(nil, testAppErr("name taken"))
	// The restore after the failed create fails too. fakeKV's KVSet was
	// registered first, so move this one ahead of it.
	restore := api.On("KVSet", kvRequestPrefix+testChannelRequestID, mock.Anything).Return(testAppErr("kv down")).Once()
	api.ExpectedCalls = append([]*mock.Call{restore}, api.ExpectedCalls[:len(api.ExpectedCalls)-1]...)
	api.On("GetPost", mock.Anything).Return(approvalCard("p"), nil)
	api.On("UpdatePost", mock.MatchedBy(func(post *model.Post) bool {
		return post.Message == "⚠️ Approval by @admin failed and the request could not be kept. Ask the requester to submit it again."
	})).Return(&model.Post{}, nil)

	resp := runCommandAs(p, "admin1", "/channel-request approve "+testChannelRequestID)

	require.Contains(t, resp.Text, "could not be saved")
	api.AssertNumberOfCalls(t, "UpdatePost", 2)
	require.NotContains(t, store, kvApprovalPostsPrefix+testChannelRequestID)
	require.NotContains(t, store, pendingListKey(pendingKindChannel, "u_req"))
}
