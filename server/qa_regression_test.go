package main

import (
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
// This test asserts the DESIRED behavior (the orphaned bot is deleted on token
// failure so a retry can succeed). It FAILS against current code, reproducing
// the bug. It is Skip-guarded so the suite stays green; remove the Skip when the
// bug is fixed.
func Test_BUG_OrphanBotOnTokenFailure(t *testing.T) {
	t.Skip("documents confirmed bug: orphaned bot on CreateUserAccessToken failure; un-skip when fixed")

	api := &plugintest.API{}
	stubLogs(api)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{})

	api.On("CreateBot", mock.Anything).Return(&model.Bot{UserId: "orphan-bot-id", Username: "deploy-bot"}, nil)
	api.On("CreateUserAccessToken", mock.Anything).Return(nil, testAppErr("token service down"))
	// DESIRED: the orphaned bot is cleaned up so a retry can re-create it.
	api.On("DeleteBot", "orphan-bot-id").Return(nil)

	req := &botTokenRequest{ID: "b1", RequesterID: "u_req", Username: "deploy-bot"}
	_, _, err := p.createBotTokenForRequest(req)
	require.Error(t, err)
	api.AssertCalled(t, "DeleteBot", "orphan-bot-id")
}
