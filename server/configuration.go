package main

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

// channelPrefix is one entry in the admin-configured list of allowed
// domain prefixes. The requester picks a prefix by ID from a dropdown;
// the server enforces that the final channel name starts with the
// prefix and (if a SuffixPattern is set) that the suffix matches it.
type channelPrefix struct {
	// Prefix is the literal string prepended to the suffix (e.g., "team-").
	Prefix string

	// Description is the human-readable label shown next to the prefix
	// in the dropdown ("Team collaboration channels"). Empty is fine.
	Description string

	// SuffixPattern is an optional regex applied to just the suffix
	// portion (not the full name). Nil when the admin didn't configure one.
	SuffixPattern *regexp.Regexp

	// SuffixPatternRaw is the un-compiled pattern string, retained for
	// UI display + error messages so users see the original regex text
	// rather than Go's re-serialized form.
	SuffixPatternRaw string
}

// configuration captures the plugin's external configuration as exposed in the Mattermost server
// configuration, as well as values computed from the configuration. Any public fields will be
// deserialized from the Mattermost server configuration in OnConfigurationChange.
//
// Field grouping (matches the System Console layout):
//  1. Approval routing (team + channel slugs)
//  2. Naming enforcement (structured prefix list — required)
//  3. Approver policy (who can approve, auto-approve list)
//  4. Notification preferences (DM + audit channel + welcome post)
//  5. Rate limits
type configuration struct {
	// --- 1. Approval routing ---

	// ApprovalTeam is the team where all named approval channels are created
	// (bot, webhook, team, audit, and the root channel/admin channel requests).
	// The channel URL is always "mattermost-channel-requests" — no separate
	// setting is needed.
	ApprovalTeam string

	// PerTeamApprovalChannels enables automatic creation of a per-team
	// approval channel (named by PerTeamChannelName) in every team. When
	// true, channel and admin-promotion requests route to the requesting
	// team's dedicated channel instead of the global ApprovalTeam/Channel.
	PerTeamApprovalChannels bool

	// --- 2. Naming enforcement ---
	// ChannelNamePrefixes is the raw multi-line prefix list. Each line
	// is "prefix|description|max_length". The structured editor
	// component serializes to this format on save; parsed back into
	// prefixes below. Prefix list is REQUIRED — there is no legacy
	// fallback.
	ChannelNamePrefixes string

	// --- 3. Approver policy ---
	AllowTeamAdminApprovers bool

	// AutoApproveUserIDs is the raw comma-separated USERNAMES the admin
	// entered via the AutoApprovePicker. Parsed + resolved to user IDs
	// in OnConfigurationChange and stored in autoApproveUserIDs.
	//
	// (Field kept named AutoApproveUserIDs for plugin.json setting-key
	// stability. Value shape is usernames now — the picker resolves to
	// IDs on the server side, since usernames are what admins recognize.)
	AutoApproveUserIDs string

	// --- 4. Security ---

	// BotReviewGroup is the name of a Mattermost User Group (without @)
	// that is @-mentioned on every bot account request alongside System
	// Admins. Leave blank to notify System Admins only.
	BotReviewGroup string

	// --- Parsed / computed (unexported) ---
	prefixes           []channelPrefix
	autoApproveUserIDs []string
}

// TeamChannelName returns the fixed per-team approval channel URL name.
// Follows the mattermost-*-requests pattern used by all plugin-managed channels.
func (c *configuration) TeamChannelName() string {
	return "mattermost-channel-requests"
}

// UsesPrefixList reports whether the admin has configured at least one
// channel prefix. A prefix is REQUIRED to submit a request: when false,
// the slash command and modal show a "not configured" message and
// resolveChannelName returns an error.
func (c *configuration) UsesPrefixList() bool {
	return len(c.prefixes) > 0
}

// Prefixes returns the parsed prefix list (copy to avoid callers
// mutating shared state).
func (c *configuration) Prefixes() []channelPrefix {
	out := make([]channelPrefix, len(c.prefixes))
	copy(out, c.prefixes)
	return out
}

// AutoApproveContains reports whether the given user ID is in the
// admin-configured auto-approve list.
func (c *configuration) AutoApproveContains(userID string) bool {
	return slices.Contains(c.autoApproveUserIDs, userID)
}

func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return &configuration{}
	}

	return p.configuration
}

func (p *Plugin) setConfiguration(configuration *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()

	if configuration != nil && p.configuration == configuration {
		if reflect.ValueOf(*configuration).NumField() == 0 {
			return
		}

		panic("setConfiguration called with the existing configuration")
	}

	p.configuration = configuration
}

func (p *Plugin) OnConfigurationChange() error {
	configuration := new(configuration)

	if err := p.API.LoadPluginConfiguration(configuration); err != nil {
		return errors.Wrap(err, "failed to load plugin configuration")
	}

	// Parse the new prefix-list setting. Each non-blank line is a
	// pipe-delimited entry: "prefix|description|optional_regex". Bad
	// entries are logged and skipped rather than failing the whole
	// activation — a typo in one line should not wedge the plugin.
	configuration.prefixes = parsePrefixList(configuration.ChannelNamePrefixes, p.API.LogError)

	// Parse the auto-approve list. The picker component serializes
	// selected users as comma-separated USERNAMES (matching MemberPicker
	// serialization). Resolve each to a user ID here so runtime
	// AutoApproveContains checks are O(len(list)) string compares
	// instead of hitting the API for every request.
	configuration.autoApproveUserIDs = p.resolveAutoApproveList(configuration.AutoApproveUserIDs)

	// Detect PerTeamApprovalChannels being enabled for the first time so we can
	// backfill channels for teams that existed before the setting was toggled on.
	// Only triggers on the false→true transition to avoid an unnecessary team-list
	// scan on every unrelated config save while the setting is already enabled.
	prevPerTeamEnabled := p.getConfiguration().PerTeamApprovalChannels

	p.setConfiguration(configuration)

	if configuration.PerTeamApprovalChannels && !prevPerTeamEnabled {
		go p.ensureAllTeamApprovalChannels()
	}

	return nil
}

// resolveAutoApproveList parses the auto-approve setting (comma-
// separated usernames or IDs) and resolves everything to MM user IDs.
// Deduplicates. Skips unresolvable entries with a warn log so the
// admin can spot typos in the plugin log.
//
// Accepts BOTH usernames (what the picker produces) AND raw 26-char
// user IDs (for backward compat with anyone who typed IDs directly).
func (p *Plugin) resolveAutoApproveList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	// Split on any punctuation/whitespace so admins can paste in any
	// reasonable format ("alice, bob", "alice bob", "alice\nbob").
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' || r == '\r'
	})

	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimPrefix(strings.TrimSpace(f), "@")
		if f == "" {
			continue
		}

		var userID string
		if len(f) == 26 {
			// Looks like an MM ID. Trust it — a wrong ID just means the
			// user never matches at runtime; no harm at parse time.
			userID = f
		} else {
			// Username -> resolve via API.
			user, appErr := p.API.GetUserByUsername(f)
			if appErr != nil || user == nil {
				p.API.LogWarn("channel-requests: auto-approve entry did not resolve to a user; skipping",
					"entry", f, "error", func() string {
						if appErr != nil {
							return appErr.Error()
						}
						return "nil user"
					}())
				continue
			}
			userID = user.Id
		}
		if seen[userID] {
			continue
		}
		seen[userID] = true
		out = append(out, userID)
	}
	return out
}

// parsePrefixList parses the admin's multi-line prefix-list setting.
// Line format: "prefix|description|third". The THIRD field accepts
// two shapes:
//
//	numeric ("16", "24")         -> treated as a max suffix length.
//	                                Internally compiled as
//	                                [a-z0-9-]{2,N}. This is the format
//	                                the new PrefixEditor UI emits.
//	regex string ("[a-z]+", ...) -> compiled as-is. Backward compat
//	                                with hand-edited configs from
//	                                the pre-editor days.
//
// Empty third field -> no constraint on the suffix (other than MM's
// channel identifier validity check).
//
// Empty lines and comments (# prefix) are skipped. Malformed regex is
// logged and the prefix stays selectable with a no-op rule so a typo
// can't wedge the plugin.
func parsePrefixList(raw string, logErr func(msg string, keyValuePairs ...any)) []channelPrefix {
	var out []channelPrefix
	for lineNo, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		prefix := strings.TrimSpace(parts[0])
		if prefix == "" {
			logErr("channel-requests: skipping prefix entry with empty prefix",
				"line", lineNo+1, "text", line)
			continue
		}
		entry := channelPrefix{Prefix: prefix}
		if len(parts) >= 2 {
			entry.Description = strings.TrimSpace(parts[1])
		}
		if len(parts) >= 3 {
			third := strings.TrimSpace(parts[2])
			if third != "" {
				entry.SuffixPattern, entry.SuffixPatternRaw = compileSuffixRule(third, prefix, logErr)
			}
		}
		out = append(out, entry)
	}
	return out
}

// compileSuffixRule converts the third pipe-field into a compiled regex.
// Returns (nil, third) when the field can't be compiled — the caller
// stores the raw text so error messages reference what the admin wrote.
func compileSuffixRule(third, prefix string, logErr func(msg string, keyValuePairs ...any)) (*regexp.Regexp, string) {
	// Numeric shape: treat as max length. Build [a-z0-9-]{2,N}. Any
	// N > 32 is silently capped since MM channel identifiers max out
	// at 64 total and we want room for the prefix.
	if isPositiveInt(third) {
		n, _ := strconv.Atoi(third)
		if n < 2 {
			n = 2
		}
		if n > 32 {
			n = 32
		}
		// Anchor the pattern so it matches the whole suffix. This lets
		// callers use MatchString directly and avoids leftmost-first
		// surprises (e.g. an alternation like "dev|development" wrongly
		// matching only the "dev" prefix of "development").
		expr := fmt.Sprintf("^(?:[a-z0-9-]{2,%d})$", n)
		re, err := regexp.Compile(expr)
		if err != nil {
			// Shouldn't happen — regex is generated from a bounded int.
			logErr("channel-requests: failed to compile numeric suffix rule",
				"prefix", prefix, "n", third, "error", err.Error())
			return nil, third
		}
		return re, third
	}

	// Legacy regex path. Anchor the admin-supplied pattern so it must
	// match the entire suffix — admins write patterns like [a-z0-9-]{2,16}
	// expecting "the suffix must be exactly this shape". Wrapping in a
	// non-capturing group keeps top-level alternations intact.
	re, err := regexp.Compile("^(?:" + third + ")$")
	if err != nil {
		logErr("channel-requests: invalid suffix regex for prefix; falling back to no-pattern",
			"prefix", prefix, "pattern", third, "error", err.Error())
		return nil, third
	}
	return re, third
}

func isPositiveInt(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
