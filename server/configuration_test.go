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
