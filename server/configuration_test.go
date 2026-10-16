package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestParseAutoApproveList_AcceptsOnlyIDs(t *testing.T) {
	id1, id2 := model.NewId(), model.NewId()
	var skipped []string
	logWarn := func(_ string, kv ...any) { skipped = append(skipped, kv[1].(string)) }

	got := parseAutoApproveList(id1+", alice\n"+id2+" "+id1, logWarn)

	require.Equal(t, []string{id1, id2}, got)
	// Usernames are skipped, not resolved.
	require.Equal(t, []string{"alice"}, skipped)
}

func TestCompileSuffixRule_FailsClosed(t *testing.T) {
	noopLog := func(string, ...any) {}

	// A regex that doesn't compile rejects every suffix instead of lifting
	// the rule.
	prefixes := parsePrefixList("proj-|Projects|[0-9", noopLog)
	_, err := resolvePrefixedName(prefixes, requestInput{Prefix: "proj-", Name: "anything-goes"})
	require.Error(t, err)

	// An unbalanced pattern is rejected too.
	prefixes = parsePrefixList("proj-|Projects|[0-9]{4})|(.*", noopLog)
	_, err = resolvePrefixedName(prefixes, requestInput{Prefix: "proj-", Name: "anything-goes"})
	require.Error(t, err)
}

func TestMigrateLegacySettings_ConvertsNamesToIDs(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	teamID, channelID, aliceID, bobID := model.NewId(), model.NewId(), model.NewId(), model.NewId()
	api.On("GetPluginConfig").Return(map[string]any{
		"ApprovalTeam":       "ops",
		"approvalchannel":    "channel-requests",
		"autoapproveuserids": "alice, " + bobID,
		"auditchannelid":     "keep-me",
	})
	api.On("GetTeamByName", "ops").Return(&model.Team{Id: teamID}, nil)
	api.On("GetTeam", teamID).Return(&model.Team{Id: teamID}, nil)
	api.On("GetChannel", "channel-requests").Return(nil, testAppErr("not found"))
	api.On("GetChannelByName", teamID, "channel-requests", false).Return(&model.Channel{Id: channelID}, nil)
	api.On("GetUserByUsername", "alice").Return(&model.User{Id: aliceID}, nil)
	api.On("GetUser", bobID).Return(&model.User{Id: bobID}, nil)

	var saved map[string]any
	api.On("SavePluginConfig", mock.Anything).Run(func(args mock.Arguments) {
		saved = args.Get(0).(map[string]any)
	}).Return(nil)
	// The migrated values must take effect immediately: Mattermost doesn't
	// send OnConfigurationChange to a plugin that is still activating.
	api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*configuration)
		dest.ApprovalTeam, _ = saved["approvalteam"].(string)
		dest.AutoApproveUserIDs, _ = saved["autoapproveuserids"].(string)
	}).Return(nil)

	require.NoError(t, p.migrateLegacySettings())
	require.True(t, p.getConfiguration().AutoApproveContains(aliceID), "migrated auto-approve list is live without a restart")
	require.Equal(t, teamID, p.getConfiguration().ApprovalTeam)

	require.Equal(t, teamID, saved["approvalteam"])
	require.Equal(t, channelID, saved["approvalchannel"])
	require.Equal(t, aliceID+","+bobID, saved["autoapproveuserids"])
	require.Equal(t, "keep-me", saved["auditchannelid"])
	require.NotContains(t, saved, "ApprovalTeam")
}

func TestMigrateLegacySettings_IDsAreLeftAlone(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	teamID, channelID, userID := model.NewId(), model.NewId(), model.NewId()
	api.On("GetPluginConfig").Return(map[string]any{
		"approvalteam":       teamID,
		"approvalchannel":    channelID,
		"autoapproveuserids": userID,
	})
	api.On("GetTeam", teamID).Return(&model.Team{Id: teamID}, nil)
	api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID}, nil)
	api.On("GetUser", userID).Return(&model.User{Id: userID}, nil)

	require.NoError(t, p.migrateLegacySettings())

	// Already migrated: nothing is resolved by name and nothing is saved.
	api.AssertNotCalled(t, "SavePluginConfig", mock.Anything)
	api.AssertNotCalled(t, "GetTeamByName", mock.Anything)
	api.AssertNotCalled(t, "GetUserByUsername", mock.Anything)
}

func TestSetConfiguration_SamePointerPanics(t *testing.T) {
	p := newTestPlugin(&plugintest.API{})
	cfg := &configuration{AuditChannelID: "audit"}
	p.setConfiguration(cfg)

	require.Panics(t, func() { p.setConfiguration(cfg) })
}

func TestOnConfigurationChange_LoadFailure(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	api.On("LoadPluginConfiguration", mock.Anything).Return(testAppErr("bad config"))

	require.ErrorContains(t, p.OnConfigurationChange(), "failed to load plugin configuration")
	require.Nil(t, p.configuration)
}

func TestMigrateLegacySettings_UnknownTeamNameLeftAlone(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("GetPluginConfig").Return(map[string]any{"approvalteam": "nosuch", "approvalchannel": "requests"})
	api.On("GetTeamByName", "nosuch").Return(nil, testAppErr("not found"))

	require.NoError(t, p.migrateLegacySettings())
	// With no resolvable team, the channel name isn't looked up either.
	api.AssertNotCalled(t, "GetChannelByName", mock.Anything, mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "SavePluginConfig", mock.Anything)
}

func TestMigrateLegacySettings_UnresolvableEntriesAndSaveFailure(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	teamID := model.NewId()
	api.On("GetPluginConfig").Return(map[string]any{
		"approvalteam":       teamID,
		"approvalchannel":    "nochan",
		"autoapproveuserids": "ghost, @",
	})
	api.On("GetTeam", teamID).Return(&model.Team{Id: teamID}, nil)
	api.On("GetChannel", "nochan").Return(nil, testAppErr("not found"))
	api.On("GetChannelByName", teamID, "nochan", false).Return(nil, testAppErr("not found"))
	api.On("GetUserByUsername", "ghost").Return(nil, testAppErr("not found"))
	var saved map[string]any
	api.On("SavePluginConfig", mock.Anything).Run(func(args mock.Arguments) {
		saved = args.Get(0).(map[string]any)
	}).Return(testAppErr("save failed"))

	err := p.migrateLegacySettings()

	require.ErrorContains(t, err, "failed to save migrated settings")
	// The unknown channel name is kept; the unknown username is dropped.
	require.Equal(t, "nochan", saved["approvalchannel"])
	require.Equal(t, "", saved["autoapproveuserids"])
}

func TestParsePrefixList_SkipsEmptyPrefix(t *testing.T) {
	var logged []string
	got := parsePrefixList("|No prefix\nteam-|Teams", func(msg string, _ ...any) { logged = append(logged, msg) })

	require.Len(t, got, 1)
	require.Equal(t, "team-", got[0].Prefix)
	require.Equal(t, []string{"channel-requests: skipping prefix entry with empty prefix"}, logged)
}

func TestCompileSuffixRule_ClampsNumericLength(t *testing.T) {
	noopLog := func(string, ...any) {}
	cases := []struct{ in, want string }{
		{"1", "^(?:[a-z0-9-]{2,2})$"},
		{"16", "^(?:[a-z0-9-]{2,16})$"},
		{"99", "^(?:[a-z0-9-]{2,32})$"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			re, raw := compileSuffixRule(tc.in, "team-", noopLog)
			require.Equal(t, tc.want, re.String())
			require.Equal(t, tc.in, raw)
		})
	}
	require.False(t, isPositiveInt(""))
}

func TestParseNonNegativeInt(t *testing.T) {
	cases := []struct {
		raw    string
		want   int
		warned bool
	}{
		{raw: "", want: 7},
		{raw: "  ", want: 7},
		{raw: "12", want: 12},
		{raw: " 3 ", want: 3},
		{raw: "0", want: 0},
		{raw: "-1", want: 7, warned: true},
		{raw: "five", want: 7, warned: true},
		{raw: "2.5", want: 7, warned: true},
		{raw: "+5", want: 7, warned: true},
		{raw: "100", want: 100},
		{raw: "101", want: 100, warned: true},
		{raw: "99999999999999", want: 100, warned: true},
		{raw: "999999999999999999999", want: 7, warned: true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			warned := false
			got := parseNonNegativeInt(tc.raw, 7, 100, "Setting", func(string, ...any) { warned = true })
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.warned, warned)
		})
	}
}

func TestConfigurationAllowsChannelName(t *testing.T) {
	noop := func(string, ...any) {}
	opsRule, opsRaw := compileSuffixRule("[a-z]+", "team-ops-", noop)
	config := &configuration{prefixes: []channelPrefix{
		{Prefix: "team-"},
		{Prefix: "team-ops-", SuffixPattern: opsRule, SuffixPatternRaw: opsRaw},
		{Prefix: "proj-", SuffixPattern: matchNothing},
	}}

	require.True(t, config.allowsChannelName("team-marketing"))
	require.True(t, config.allowsChannelName("team-ops-oncall"))
	// The longest matching prefix decides which rule applies.
	require.False(t, config.allowsChannelName("team-ops-42"))
	require.False(t, config.allowsChannelName("team-"))
	require.False(t, config.allowsChannelName("proj-alpha"))
	require.False(t, config.allowsChannelName("eng-alpha"))
	require.False(t, (&configuration{}).allowsChannelName("team-marketing"))
}

func TestOnConfigurationChange_ParsesRequestLimits(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)

	api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*configuration)
		dest.MaxPendingRequestsPerUser = "2"
		dest.RequestExpiryDays = "soon"
	}).Return(nil)

	require.NoError(t, p.OnConfigurationChange())
	require.Equal(t, 2, p.getConfiguration().MaxPending())
	require.Equal(t, defaultRequestExpiryDays, p.getConfiguration().ExpiryDays())
	api.AssertCalled(t, "LogWarn", "channel-requests: setting must be a whole number 0 or greater; using the default",
		"setting", "RequestExpiryDays", "value", "soon", "default", defaultRequestExpiryDays)
}

func TestOnConfigurationChange_ChecksAuditChannel(t *testing.T) {
	cases := []struct {
		name    string
		channel *model.Channel
		appErr  *model.AppError
		warned  bool
	}{
		{name: "private channel", channel: &model.Channel{Id: "audit", Type: model.ChannelTypePrivate}},
		{name: "public channel", channel: &model.Channel{Id: "audit", Type: model.ChannelTypeOpen}, warned: true},
		{name: "archived channel", channel: &model.Channel{Id: "audit", Type: model.ChannelTypePrivate, DeleteAt: 1}, warned: true},
		{name: "missing channel", appErr: testAppErr("not found"), warned: true},
		{name: "lookup returns nothing", warned: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &plugintest.API{}
			stubLogs(api)
			defer api.AssertExpectations(t)
			p := newTestPlugin(api)

			api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
				args.Get(0).(*configuration).AuditChannelID = "  audit "
			}).Return(nil)
			api.On("GetChannel", "audit").Return(tc.channel, tc.appErr)

			require.NoError(t, p.OnConfigurationChange())
			require.Equal(t, "audit", p.getConfiguration().AuditChannelID, "the ID is stored trimmed")
			if tc.warned {
				api.AssertCalled(t, "LogWarn", auditChannelWarning, "channel_id", "audit", "error", mock.Anything)
			} else {
				api.AssertNotCalled(t, "LogWarn", auditChannelWarning, "channel_id", "audit", "error", mock.Anything)
			}
		})
	}
}

func TestOnConfigurationChange_BlankAuditChannelIsNotChecked(t *testing.T) {
	for _, id := range []string{"", "   "} {
		api := &plugintest.API{}
		stubLogs(api)
		defer api.AssertExpectations(t)
		p := newTestPlugin(api)
		api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
			args.Get(0).(*configuration).AuditChannelID = id
		}).Return(nil)

		require.NoError(t, p.OnConfigurationChange())
		require.Empty(t, p.getConfiguration().AuditChannelID)
		api.AssertNotCalled(t, "GetChannel", mock.Anything)
	}
}

func TestOnConfigurationChange_CapsRequestLimits(t *testing.T) {
	api := &plugintest.API{}
	stubLogs(api)
	defer api.AssertExpectations(t)
	p := newTestPlugin(api)
	api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*configuration)
		dest.MaxPendingRequestsPerUser = "5000"
		dest.RequestExpiryDays = "99999999999999"
	}).Return(nil)

	require.NoError(t, p.OnConfigurationChange())
	require.Equal(t, maxPendingRequestsLimit, p.getConfiguration().MaxPending())
	require.Equal(t, maxRequestExpiryDays, p.getConfiguration().ExpiryDays())
	require.False(t, requestExpired(model.GetMillis()-day, p.getConfiguration().ExpiryDays(), model.GetMillis()))
}
