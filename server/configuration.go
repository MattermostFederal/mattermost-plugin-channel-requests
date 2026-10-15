package main

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
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
//  1. Approval routing (team + channel IDs)
//  2. Naming enforcement (structured prefix list — required)
//  3. Approver policy (who can approve, auto-approve list)
//  4. Notification preferences (DM + audit channel + welcome post)
//  5. Rate limits
type configuration struct {
	// --- 1. Approval routing ---
	// ApprovalTeam and ApprovalChannel hold IDs, not names, so renames
	// don't change where requests go. Older versions stored names;
	// migrateLegacySettings converts them once at activation.
	ApprovalTeam    string
	ApprovalChannel string

	// --- 2. Naming enforcement ---
	// ChannelNamePrefixes is the raw multi-line prefix list. Each line
	// is "prefix|description|max_length". The structured editor
	// component serializes to this format on save; parsed back into
	// prefixes below. Prefix list is REQUIRED — there is no legacy
	// fallback.
	ChannelNamePrefixes string

	// --- 3. Approver policy ---
	AllowTeamAdminApprovers bool

	// AutoApproveUserIDs is the comma-separated user IDs the admin picked
	// in the AutoApprovePicker. Older versions stored usernames;
	// migrateLegacySettings converts them once at activation.
	AutoApproveUserIDs string

	// --- 4. Audit ---
	AuditChannelID string

	// --- Parsed / computed (unexported) ---
	prefixes           []channelPrefix
	autoApproveUserIDs []string
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

	// Parse the auto-approve list. Only user IDs are accepted.
	configuration.autoApproveUserIDs = parseAutoApproveList(configuration.AutoApproveUserIDs, p.API.LogWarn)

	p.setConfiguration(configuration)

	return nil
}

// splitSettingList splits a free-form list setting on commas,
// semicolons and whitespace so admins can paste in any reasonable format
// ("a, b", "a b", "a\nb").
func splitSettingList(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' || r == '\r'
	})
}

// parseAutoApproveList parses the auto-approve setting into a deduplicated
// list of user IDs. Entries that aren't well-formed IDs are skipped with a
// warning.
func parseAutoApproveList(raw string, logWarn func(msg string, keyValuePairs ...any)) []string {
	fields := splitSettingList(raw)
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		if !model.IsValidId(f) {
			logWarn("channel-requests: auto-approve entry is not a user ID; skipping", "entry", f)
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// migrateLegacySettings converts settings saved by older versions as names
// (approval team slug, approval channel name, auto-approve usernames) to
// IDs and saves them back, once. Names are resolved here, at upgrade time,
// against whatever currently holds them, and each conversion is logged so
// an admin can check it. Unresolvable entries are kept as-is for
// ApprovalTeam/ApprovalChannel (requests then fall back to DMing System
// Admins, as for an unconfigured channel) and dropped from the
// auto-approve list.
func (p *Plugin) migrateLegacySettings() error {
	stored := p.API.GetPluginConfig()
	cfg := make(map[string]any, len(stored))
	for k, v := range stored {
		cfg[strings.ToLower(k)] = v
	}
	str := func(key string) string {
		v, _ := cfg[key].(string)
		return strings.TrimSpace(v)
	}

	changed := false

	teamID := str("approvalteam")
	if teamID != "" && !p.isTeamID(teamID) {
		if team, appErr := p.API.GetTeamByName(teamID); appErr == nil && team != nil {
			p.API.LogInfo("channel-requests: migrated ApprovalTeam from name to ID", "name", teamID, "team_id", team.Id)
			teamID = team.Id
			cfg["approvalteam"] = teamID
			changed = true
		} else {
			p.API.LogWarn("channel-requests: ApprovalTeam is neither a team ID nor a team name; leaving it unchanged", "value", teamID)
		}
	}

	if channelName := str("approvalchannel"); channelName != "" && p.isTeamID(teamID) {
		if channel, appErr := p.API.GetChannel(channelName); appErr != nil || channel == nil {
			if channel, appErr := p.API.GetChannelByName(teamID, channelName, false); appErr == nil && channel != nil {
				p.API.LogInfo("channel-requests: migrated ApprovalChannel from name to ID", "name", channelName, "channel_id", channel.Id)
				cfg["approvalchannel"] = channel.Id
				changed = true
			} else {
				p.API.LogWarn("channel-requests: ApprovalChannel is neither a channel ID nor a channel name in the approval team; leaving it unchanged", "value", channelName)
			}
		}
	}

	if raw := str("autoapproveuserids"); raw != "" {
		var ids []string
		legacy := false
		for _, f := range splitSettingList(raw) {
			f = strings.TrimPrefix(strings.TrimSpace(f), "@")
			if f == "" {
				continue
			}
			if model.IsValidId(f) {
				if _, appErr := p.API.GetUser(f); appErr == nil {
					ids = append(ids, f)
					continue
				}
			}
			legacy = true
			user, appErr := p.API.GetUserByUsername(f)
			if appErr != nil || user == nil {
				p.API.LogWarn("channel-requests: dropping auto-approve entry that is neither a user ID nor a username", "entry", f)
				continue
			}
			p.API.LogInfo("channel-requests: migrated auto-approve entry from username to ID", "username", f, "user_id", user.Id)
			ids = append(ids, user.Id)
		}
		if legacy {
			cfg["autoapproveuserids"] = strings.Join(dedupeNonEmpty(ids), ",")
			changed = true
		}
	}

	if !changed {
		return nil
	}
	if appErr := p.API.SavePluginConfig(cfg); appErr != nil {
		return errors.Wrap(appErr, "failed to save migrated settings")
	}
	// Mattermost only delivers OnConfigurationChange to active plugins, and
	// this runs from OnActivate, so the save above doesn't reach us. Reload
	// explicitly or the old name values stay in effect until the next save.
	return p.OnConfigurationChange()
}

// isTeamID reports whether s is the ID of an existing team.
func (p *Plugin) isTeamID(s string) bool {
	if !model.IsValidId(s) {
		return false
	}
	team, appErr := p.API.GetTeam(s)
	return appErr == nil && team != nil
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
// Empty lines and comments (# prefix) are skipped. A malformed regex is
// logged and the prefix stays listed but rejects every suffix (fail
// closed), so a typo can't silently lift the naming rule.
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

// matchNothing is the suffix rule used when an admin's pattern can't be
// compiled: requests under that prefix are rejected until it's fixed.
var matchNothing = regexp.MustCompile(`[^\s\S]`)

// compileSuffixRule converts the third pipe-field into a compiled regex.
// Returns (matchNothing, third) when the field can't be compiled — the
// caller stores the raw text so error messages reference what the admin
// wrote.
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
			return matchNothing, third
		}
		return re, third
	}

	// Legacy regex path. Anchor the admin-supplied pattern so it must
	// match the entire suffix — admins write patterns like [a-z0-9-]{2,16}
	// expecting "the suffix must be exactly this shape". Wrapping in a
	// non-capturing group keeps top-level alternations intact. The pattern
	// must also compile on its own so the wrapping group stays balanced.
	if _, err := regexp.Compile(third); err != nil {
		logErr("channel-requests: invalid suffix regex for prefix; rejecting all suffixes until it's fixed",
			"prefix", prefix, "pattern", third, "error", err.Error())
		return matchNothing, third
	}
	re, err := regexp.Compile("^(?:" + third + ")$")
	if err != nil {
		logErr("channel-requests: invalid suffix regex for prefix; rejecting all suffixes until it's fixed",
			"prefix", prefix, "pattern", third, "error", err.Error())
		return matchNothing, third
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
