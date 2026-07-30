package main

import (
	"net/http"
	"strings"
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

func TestCreateChannelForRequest_TrimsListsToActualAdds(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("CreateChannel", mock.Anything).Return(&model.Channel{Id: "ch1"}, nil)
	// u_bad can't be added; register before the catch-all so testify
	// matches the specific expectation first.
	api.On("AddChannelMember", "ch1", "u_bad").Return(nil, testAppErr("cannot add"))
	api.On("AddChannelMember", "ch1", mock.Anything).Return(&model.ChannelMember{}, nil)
	api.On("UpdateChannelMemberRoles", "ch1", mock.Anything, mock.Anything).Return(&model.ChannelMember{}, nil)

	req := &channelRequest{
		RequesterID:    "u_req",
		TeamID:         "team1",
		Name:           "team-x",
		ChannelType:    "O",
		MemberIDs:      []string{"u_bad", "u_good"},
		AdminMemberIDs: []string{"u_admin"},
	}

	_, err := p.createChannelForRequest(req)
	require.NoError(t, err)

	// The lists are trimmed to who was actually added/promoted so the
	// welcome message can't claim a user was added when the API call
	// failed (e.g. a non-team-member).
	require.Equal(t, []string{"u_good"}, req.MemberIDs)
	require.Equal(t, []string{"u_admin"}, req.AdminMemberIDs)
}

func TestResolvePrefixedName_AnchoredSuffixMatchesWholeSuffix(t *testing.T) {
	noopLog := func(string, ...any) {}
	// Third pipe-field is the suffix regex "dev|development".
	prefixes := parsePrefixList("proj-|Projects|dev|development", noopLog)
	require.Len(t, prefixes, 1)

	// The longer alternative must be accepted — the old leftmost-first
	// FindStringIndex check wrongly rejected "development" because it
	// matched only the "dev" alternative.
	name, err := resolvePrefixedName(prefixes, requestInput{Prefix: "proj-", Name: "development"})
	require.NoError(t, err)
	require.Equal(t, "proj-development", name)

	// The short alternative still works.
	name, err = resolvePrefixedName(prefixes, requestInput{Prefix: "proj-", Name: "dev"})
	require.NoError(t, err)
	require.Equal(t, "proj-dev", name)

	// A suffix matching neither alternative is rejected.
	_, err = resolvePrefixedName(prefixes, requestInput{Prefix: "proj-", Name: "prod"})
	require.Error(t, err)
}

func TestResolvePrefixedName_NumericSuffixRuleIsLengthBounded(t *testing.T) {
	noopLog := func(string, ...any) {}
	// Numeric third field -> max suffix length (compiled as ^[a-z0-9-]{2,8}$).
	prefixes := parsePrefixList("team-|Teams|8", noopLog)
	require.Len(t, prefixes, 1)

	name, err := resolvePrefixedName(prefixes, requestInput{Prefix: "team-", Name: "sales"})
	require.NoError(t, err)
	require.Equal(t, "team-sales", name)

	// Over the length bound -> rejected (anchored, so no partial match).
	_, err = resolvePrefixedName(prefixes, requestInput{Prefix: "team-", Name: "supercalifragilistic"})
	require.Error(t, err)
}

func TestSubmitRequest_NonMemberRejected(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetUser", "u1").Return(&model.User{Id: "u1", Roles: "system_user"}, nil)
	api.On("GetTeamMember", "team1", "u1").Return(nil, testAppErr("not a member"))

	_, err := p.submitRequest(requestInput{
		RequesterID: "u1",
		TeamID:      "team1",
		DisplayName: "Marketing",
		Prefix:      "team-",
		Name:        "marketing",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "member of the team")
	// Rejected before any channel work happens.
	api.AssertNotCalled(t, "CreateChannel", mock.Anything)
}

func TestSubmitRequest_SystemAdminExemptFromMembership(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{prefixes: []channelPrefix{{Prefix: "team-"}}})

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Roles: "system_user system_admin"}, nil)
	api.On("CreateChannel", mock.MatchedBy(func(c *model.Channel) bool {
		return c.TeamId == "team1" && c.Name == "team-marketing"
	})).Return(&model.Channel{Id: "ch1", Name: "team-marketing"}, nil)
	api.On("AddChannelMember", "ch1", "admin1").Return(&model.ChannelMember{}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	msg, err := p.submitRequest(requestInput{
		RequesterID: "admin1",
		TeamID:      "team1",
		DisplayName: "Marketing",
		Prefix:      "team-",
		Name:        "marketing",
	})

	require.NoError(t, err)
	require.Contains(t, msg, "team-marketing")
	// A System Admin can create cross-team, so membership is never checked.
	api.AssertNotCalled(t, "GetTeamMember", mock.Anything, mock.Anything)
}

func TestSubmitRequest_AuditsBypassCreation(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{
		prefixes:       []channelPrefix{{Prefix: "team-"}},
		AuditChannelID: "audit1",
	})

	api.On("GetUser", "admin1").Return(&model.User{Id: "admin1", Username: "admin", Roles: "system_user system_admin"}, nil)
	api.On("CreateChannel", mock.Anything).Return(&model.Channel{Id: "ch1", Name: "team-marketing"}, nil)
	api.On("AddChannelMember", "ch1", "admin1").Return(&model.ChannelMember{}, nil)
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil)

	_, err := p.submitRequest(requestInput{
		RequesterID: "admin1",
		TeamID:      "team1",
		DisplayName: "Marketing",
		Prefix:      "team-",
		Name:        "marketing",
	})
	require.NoError(t, err)

	// The bypass-path (sysadmin/auto-approve) creation is audited.
	api.AssertCalled(t, "CreatePost", mock.MatchedBy(func(post *model.Post) bool {
		return post.ChannelId == "audit1" && strings.Contains(post.Message, "CREATED")
	}))
}

func TestSubmitRequest_SoftDeletedMemberRejected(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetUser", "u1").Return(&model.User{Id: "u1", Roles: "system_user"}, nil)
	// A user who LEFT the team: GetTeamMember returns a row with DeleteAt set.
	api.On("GetTeamMember", "team1", "u1").Return(&model.TeamMember{TeamId: "team1", UserId: "u1", DeleteAt: 123}, nil)

	_, err := p.submitRequest(requestInput{
		RequesterID: "u1",
		TeamID:      "team1",
		DisplayName: "Marketing",
		Prefix:      "team-",
		Name:        "marketing",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "member of the team")
	api.AssertNotCalled(t, "CreateChannel", mock.Anything)
}

func TestSubmitRequest_AutoApproveUserStillNeedsMembership(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{autoApproveUserIDs: []string{"u1"}})

	api.On("GetUser", "u1").Return(&model.User{Id: "u1", Roles: "system_user"}, nil)
	api.On("GetTeamMember", "team1", "u1").Return(nil, testAppErr("not a member"))

	_, err := p.submitRequest(requestInput{
		RequesterID: "u1",
		TeamID:      "team1",
		DisplayName: "Marketing",
		Prefix:      "team-",
		Name:        "marketing",
	})

	// Auto-approve grants an approval bypass, NOT a team-membership bypass.
	require.Error(t, err)
	require.Contains(t, err.Error(), "member of the team")
	api.AssertNotCalled(t, "CreateChannel", mock.Anything)
}

func TestValidateRequestInput(t *testing.T) {
	tooManyMembers := make([]string, maxMembersPerList+1)
	for i := range tooManyMembers {
		tooManyMembers[i] = model.NewId()
	}

	cases := []struct {
		name    string
		in      requestInput
		wantErr string // "" means expect no error
	}{
		{
			name: "valid",
			in:   requestInput{DisplayName: "Marketing", TeamID: "team1"},
		},
		{
			name:    "empty display name",
			in:      requestInput{DisplayName: "   ", TeamID: "team1"},
			wantErr: "channel name is required",
		},
		{
			name:    "display name too long",
			in:      requestInput{DisplayName: strings.Repeat("x", maxDisplayNameLen+1), TeamID: "team1"},
			wantErr: "characters or fewer",
		},
		{
			name:    "purpose too long",
			in:      requestInput{DisplayName: "Marketing", TeamID: "team1", Purpose: strings.Repeat("x", maxPurposeLen+1)},
			wantErr: "purpose must be",
		},
		{
			name:    "too many members",
			in:      requestInput{DisplayName: "Marketing", TeamID: "team1", MemberIDs: tooManyMembers},
			wantErr: "too many members",
		},
		{
			name:    "too many admins",
			in:      requestInput{DisplayName: "Marketing", TeamID: "team1", AdminMemberIDs: tooManyMembers},
			wantErr: "too many members",
		},
		{
			name:    "missing team",
			in:      requestInput{DisplayName: "Marketing", TeamID: "  "},
			wantErr: "a team is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRequestInput(tc.in)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestPromoteSoleMember(t *testing.T) {
	tests := []struct {
		name        string
		members     []string
		admins      []string
		wantMembers []string
		wantAdmins  []string
	}{
		{
			name:        "sole member, no admins -> member promoted",
			members:     []string{"u1"},
			admins:      nil,
			wantMembers: nil,
			wantAdmins:  []string{"u1"},
		},
		{
			name:        "multiple members, no admins -> unchanged",
			members:     []string{"u1", "u2"},
			admins:      nil,
			wantMembers: []string{"u1", "u2"},
			wantAdmins:  nil,
		},
		{
			name:        "sole member, admins already set -> unchanged",
			members:     []string{"u1"},
			admins:      []string{"u2"},
			wantMembers: []string{"u1"},
			wantAdmins:  []string{"u2"},
		},
		{
			name:        "no members, no admins -> unchanged",
			members:     nil,
			admins:      nil,
			wantMembers: nil,
			wantAdmins:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			members, admins := promoteSoleMember(tt.members, tt.admins)
			require.Equal(t, tt.wantMembers, members)
			require.Equal(t, tt.wantAdmins, admins)
		})
	}
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

func TestResolveUsernameList_RejectsTooMany(t *testing.T) {
	api := &plugintest.API{}
	p := newTestPlugin(api)

	names := make([]string, maxMembersPerList+1)
	for i := range names {
		names[i] = "user"
	}

	ids, err := p.resolveUsernameList(names)

	// Bounded before any per-username lookup (no GetUserByUsername mocks
	// registered, so a lookup would fail the test).
	require.Error(t, err)
	require.Nil(t, ids)
	require.Contains(t, err.Error(), "too many users")
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
