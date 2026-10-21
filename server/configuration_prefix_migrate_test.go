package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBumpLegacyDefaultSuffixMax(t *testing.T) {
	t.Run("rewrites 24 to 48, leaves custom lengths", func(t *testing.T) {
		in := "team-|Teams|24\nops-|Ops|16\nproject-|Projects|24\n"
		out, changed := bumpLegacyDefaultSuffixMax(in)
		require.True(t, changed)
		require.Equal(t, "team-|Teams|48\nops-|Ops|16\nproject-|Projects|48\n", out)
	})

	t.Run("no-op when nothing to bump", func(t *testing.T) {
		in := "team-|Teams|48\nops-|Ops|16"
		out, changed := bumpLegacyDefaultSuffixMax(in)
		require.False(t, changed)
		require.Equal(t, in, out)
	})

	t.Run("preserves comments and blanks", func(t *testing.T) {
		in := "# prefixes\n\nteam-|Teams|24\n"
		out, changed := bumpLegacyDefaultSuffixMax(in)
		require.True(t, changed)
		require.Equal(t, "# prefixes\n\nteam-|Teams|48\n", out)
	})

	t.Run("empty", func(t *testing.T) {
		out, changed := bumpLegacyDefaultSuffixMax("")
		require.False(t, changed)
		require.Equal(t, "", out)
	})
}

func TestBumpLegacyDefaultSuffixMax_ParsedAs48(t *testing.T) {
	noopLog := func(string, ...any) {}
	bumped, changed := bumpLegacyDefaultSuffixMax("team-|Teams|24")
	require.True(t, changed)
	prefixes := parsePrefixList(bumped, noopLog)
	require.Len(t, prefixes, 1)
	require.Equal(t, "48", prefixes[0].SuffixPatternRaw)
	require.True(t, prefixes[0].SuffixPattern.MatchString(strings.Repeat("a", 48)))
	require.False(t, prefixes[0].SuffixPattern.MatchString(strings.Repeat("a", 49)))
}
