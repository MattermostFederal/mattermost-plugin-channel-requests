package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestEnabled(t *testing.T) {
	c := &configuration{
		AllowChannelRequests: true,
		AllowTeamRequests:    false,
		AllowWebhookRequests: true,
	}

	require.True(t, c.RequestEnabled(requestTypeChannel))
	require.False(t, c.RequestEnabled(requestTypeTeam))
	require.True(t, c.RequestEnabled(requestTypeWebhook))

	// An unknown type is never enabled — a new entry point can't bypass the
	// gate before its toggle is wired up.
	require.False(t, c.RequestEnabled("bot_token"))
	require.False(t, c.RequestEnabled(""))
}
