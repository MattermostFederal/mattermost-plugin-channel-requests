package main

import (
	"net/http"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// channelAdminRoles is the role string createChannelForRequest promotes
// designated admins to. Kept here so the test breaks loudly if the
// production string changes.
const channelAdminRoles = "channel_user channel_admin"

// newTestPlugin wires a Plugin up to a mock API with a known bot user.
func newTestPlugin(api *plugintest.API) *Plugin {
	p := &Plugin{}
	p.SetAPI(api)
	p.botUserID = "bot-user-id"
	return p
}

// stubLogs registers permissive log expectations so production code can
// log at any level/arity without the test caring about the exact calls.
func stubLogs(api *plugintest.API) {
	for _, method := range []string{"LogDebug", "LogInfo", "LogWarn", "LogError"} {
		for arity := 1; arity <= 16; arity++ {
			args := make([]any, arity)
			for i := range args {
				args[i] = mock.Anything
			}
			api.On(method, args...).Maybe()
		}
	}
}

// testAppErr builds a throwaway *model.AppError for failure-path mocks.
func testAppErr(msg string) *model.AppError {
	return model.NewAppError("test", "test.error", nil, msg, http.StatusInternalServerError)
}

func TestCreateChannelForRequest_AddsMembersAndPromotesAdmins(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	created := &model.Channel{Id: "ch1", Name: "team-marketing"}

	// Assert the channel is created from the request's fields verbatim.
	api.On("CreateChannel", mock.MatchedBy(func(c *model.Channel) bool {
		return c.TeamId == "team1" &&
			c.Name == "team-marketing" &&
			c.DisplayName == "Marketing" &&
			c.Type == model.ChannelTypeOpen &&
			c.Purpose == "Coordination" &&
			c.CreatorId == "u_req"
	})).Return(created, nil)
	api.On("AddChannelMember", "ch1", mock.Anything).Return(&model.ChannelMember{}, nil)
	api.On("UpdateChannelMemberRoles", "ch1", "u_a1", channelAdminRoles).Return(&model.ChannelMember{}, nil)

	req := &channelRequest{
		RequesterID:    "u_req",
		TeamID:         "team1",
		Name:           "team-marketing",
		DisplayName:    "Marketing",
		Purpose:        "Coordination",
		ChannelType:    "O",
		MemberIDs:      []string{"u_m1", "u_m2"},
		AdminMemberIDs: []string{"u_a1"},
	}

	channel, err := p.createChannelForRequest(req)

	require.NoError(t, err)
	require.Equal(t, "ch1", channel.Id)

	// Requester + two members + one admin, each added exactly once.
	api.AssertNumberOfCalls(t, "AddChannelMember", 4)
	api.AssertCalled(t, "AddChannelMember", "ch1", "u_req")
	api.AssertCalled(t, "AddChannelMember", "ch1", "u_m1")
	api.AssertCalled(t, "AddChannelMember", "ch1", "u_m2")
	api.AssertCalled(t, "AddChannelMember", "ch1", "u_a1")

	// Only the designated admin is promoted, and only once.
	api.AssertNumberOfCalls(t, "UpdateChannelMemberRoles", 1)
	api.AssertCalled(t, "UpdateChannelMemberRoles", "ch1", "u_a1", channelAdminRoles)
}

func TestCreateChannelForRequest_DeduplicatesUsers(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("CreateChannel", mock.Anything).Return(&model.Channel{Id: "ch1"}, nil)
	api.On("AddChannelMember", "ch1", mock.Anything).Return(&model.ChannelMember{}, nil)
	api.On("UpdateChannelMemberRoles", "ch1", "u_dup", channelAdminRoles).Return(&model.ChannelMember{}, nil)

	// Requester repeated as a member, and one user listed as both a member
	// and an admin. Neither should be added twice.
	req := &channelRequest{
		RequesterID:    "u_req",
		TeamID:         "team1",
		Name:           "team-x",
		ChannelType:    "O",
		MemberIDs:      []string{"u_req", "u_dup"},
		AdminMemberIDs: []string{"u_dup"},
	}

	_, err := p.createChannelForRequest(req)

	require.NoError(t, err)
	// Unique users only: u_req, u_dup.
	api.AssertNumberOfCalls(t, "AddChannelMember", 2)
	// u_dup is an admin -> promoted exactly once despite appearing twice.
	api.AssertNumberOfCalls(t, "UpdateChannelMemberRoles", 1)
	api.AssertCalled(t, "UpdateChannelMemberRoles", "ch1", "u_dup", channelAdminRoles)
}

func TestCreateChannelForRequest_SkipsEmptyIDs(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("CreateChannel", mock.Anything).Return(&model.Channel{Id: "ch1"}, nil)
	api.On("AddChannelMember", "ch1", mock.Anything).Return(&model.ChannelMember{}, nil)

	req := &channelRequest{
		RequesterID:    "u_req",
		TeamID:         "team1",
		Name:           "team-x",
		ChannelType:    "O",
		MemberIDs:      []string{"", "u_m1"},
		AdminMemberIDs: []string{""},
	}

	_, err := p.createChannelForRequest(req)

	require.NoError(t, err)
	// Empty IDs are skipped: only u_req and u_m1 are added.
	api.AssertNumberOfCalls(t, "AddChannelMember", 2)
	// The empty admin entry must not trigger any promotion.
	api.AssertNotCalled(t, "UpdateChannelMemberRoles", mock.Anything, mock.Anything, mock.Anything)
}

func TestCreateChannelForRequest_CreateChannelErrorAbortsEarly(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("CreateChannel", mock.Anything).Return(nil, testAppErr("boom"))

	req := &channelRequest{
		RequesterID: "u_req",
		TeamID:      "team1",
		Name:        "team-x",
		ChannelType: "O",
		MemberIDs:   []string{"u_m1"},
	}

	channel, err := p.createChannelForRequest(req)

	require.Error(t, err)
	require.Nil(t, channel)
	// No member work happens if the channel itself can't be created.
	api.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
}

func TestCreateChannelForRequest_AddMemberFailureIsNonFatalAndSkipsPromotion(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("CreateChannel", mock.Anything).Return(&model.Channel{Id: "ch1"}, nil)
	// u_bad can't be added; register it before the catch-all so testify
	// matches the specific expectation first.
	api.On("AddChannelMember", "ch1", "u_bad").Return(nil, testAppErr("cannot add"))
	api.On("AddChannelMember", "ch1", mock.Anything).Return(&model.ChannelMember{}, nil)
	api.On("UpdateChannelMemberRoles", "ch1", mock.Anything, mock.Anything).Return(&model.ChannelMember{}, nil)

	// Both u_bad and u_good are admins; only u_good is added successfully.
	req := &channelRequest{
		RequesterID:    "u_req",
		TeamID:         "team1",
		Name:           "team-x",
		ChannelType:    "O",
		AdminMemberIDs: []string{"u_bad", "u_good"},
	}

	channel, err := p.createChannelForRequest(req)

	// A member that can't be added is logged, not fatal.
	require.NoError(t, err)
	require.Equal(t, "ch1", channel.Id)
	api.AssertCalled(t, "AddChannelMember", "ch1", "u_bad")
	// A user that failed to be added must not be promoted...
	api.AssertNotCalled(t, "UpdateChannelMemberRoles", "ch1", "u_bad", channelAdminRoles)
	// ...but a successfully-added admin still is.
	api.AssertCalled(t, "UpdateChannelMemberRoles", "ch1", "u_good", channelAdminRoles)
}

func TestResolveUsernameList(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)

	api.On("GetUserByUsername", "alice").Return(&model.User{Id: "id_alice"}, nil)
	api.On("GetUserByUsername", "bob").Return(&model.User{Id: "id_bob"}, nil)

	// Leading @ and surrounding whitespace are stripped; blank entries dropped.
	ids, err := p.resolveUsernameList([]string{"@alice", " bob ", ""})

	require.NoError(t, err)
	require.Equal(t, []string{"id_alice", "id_bob"}, ids)
}

func TestResolveUsernameList_UnknownUserErrors(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)

	api.On("GetUserByUsername", "ghost").Return(nil, testAppErr("not found"))

	ids, err := p.resolveUsernameList([]string{"ghost"})

	require.Error(t, err)
	require.Nil(t, ids)
	require.Contains(t, err.Error(), "ghost")
}
