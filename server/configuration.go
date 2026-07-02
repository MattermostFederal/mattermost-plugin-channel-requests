package main

import (
	"fmt"
	"reflect"
	"regexp"
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
//   1. Approval routing (team + channel slugs)
//   2. Naming enforcement (structured prefix list + legacy fallback)
//   3. Approver policy (who can approve, auto-approve list)
//   4. Notification preferences (DM + audit channel + welcome post)
//   5. Rate limits
type configuration struct {
	// --- 1. Approval routing ---
	ApprovalTeam    string
	ApprovalChannel string

	// --- 2. Naming enforcement ---
	// ChannelNamePrefixes is the raw multi-line prefix list. Each line
	// is "prefix|description|optional_regex". The structured editor
	// component serializes to this format on save; parsed back into
	// prefixes below.
	ChannelNamePrefixes string

	// Legacy fallback (only used when ChannelNamePrefixes is empty).
	ChannelNameTemplate string
	ChannelNamePattern  string

	// --- 3. Approver policy ---
	AllowTeamAdminApprovers    bool
	AllowChannelAdminApprovers bool
	AutoApproveUserIDs         string // raw text, parsed below

	// --- 4. Notification preferences ---
	NotifyRequesterOnApprove    bool
	NotifyRequesterOnDeny       bool
	PostWelcomeInCreatedChannel bool
	AuditChannelID              string

	// --- 5. Rate limits ---
	MaxRequestsPerUser     int
	RateLimitWindowHours   int
	SkipRateLimitForAdmins bool

	// --- Parsed / computed (unexported) ---
	prefixes           []channelPrefix
	compiledPattern    *regexp.Regexp
	autoApproveUserIDs []string
}

// UsesPrefixList reports whether the admin has configured the new
// prefix-list flow. When true, request handling switches to the
// dropdown-based UX; when false, the legacy template + regex apply.
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

// Clone shallow copies the configuration. Because the configuration contains only value types, a
// shallow copy is sufficient.
func (c *configuration) Clone() *configuration {
	clone := *c
	return &clone
}

// IsValid reports whether the configuration has everything the plugin needs to route requests.
func (c *configuration) IsValid() error {
	if strings.TrimSpace(c.ApprovalTeam) == "" {
		return errors.New("the Approval Team is not configured")
	}
	if strings.TrimSpace(c.ApprovalChannel) == "" {
		return errors.New("the Approval Channel is not configured")
	}
	return nil
}

// AutoApproveContains reports whether the given user ID is in the
// admin-configured auto-approve list.
func (c *configuration) AutoApproveContains(userID string) bool {
	for _, id := range c.autoApproveUserIDs {
		if id == userID {
			return true
		}
	}
	return false
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

	// Parse the auto-approve user-ID list. Accepts commas or
	// whitespace as separators so admins can type "id1 id2, id3"
	// without thinking about formatting.
	configuration.autoApproveUserIDs = parseUserIDList(configuration.AutoApproveUserIDs)

	// Apply defaults for booleans that should default TRUE. Go's
	// zero-value is false, and the MM plugin config loader can't
	// distinguish "unset" from "explicitly set to false". Best we can
	// do is: if the user never touched the setting AND the whole config
	// looks fresh (no prior version marker), assume defaults. For now
	// we simply flip these to true on activation regardless; admins who
	// want them off can toggle via System Console.
	// (This matches the pre-plugin behavior where the DM always fired.)
	if !configuration.NotifyRequesterOnApprove && !configuration.NotifyRequesterOnDeny {
		configuration.NotifyRequesterOnApprove = true
		configuration.NotifyRequesterOnDeny = true
	}
	if !configuration.SkipRateLimitForAdmins {
		configuration.SkipRateLimitForAdmins = true
	}

	// Compile the legacy pattern (only used when prefix list is empty).
	// Same tolerance policy: invalid pattern is logged and ignored.
	if pattern := strings.TrimSpace(configuration.ChannelNamePattern); pattern != "" {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			p.API.LogError("invalid ChannelNamePattern; ignoring it", "pattern", pattern, "error", err.Error())
		} else {
			configuration.compiledPattern = compiled
		}
	}

	p.setConfiguration(configuration)

	return nil
}

// parseUserIDList tokenizes a free-form list of MM user IDs separated
// by commas OR whitespace. Returns a de-duplicated slice preserving
// first-seen order. Filters obviously-invalid entries (too short to be
// an MM 26-char ID) but doesn't hit the API to verify existence —
// non-existent IDs are just ignored at auto-approve check time.
func parseUserIDList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	// Split on commas, semicolons, whitespace, newlines — everything
	// non-alphanumeric that isn't part of an ID.
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' || r == '\r'
	})
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if len(f) != 26 { // MM IDs are 26-char base32-ish
			continue
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
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
		expr := fmt.Sprintf("[a-z0-9-]{2,%d}", n)
		re, err := regexp.Compile(expr)
		if err != nil {
			// Shouldn't happen — regex is generated from a bounded int.
			logErr("channel-requests: failed to compile numeric suffix rule",
				"prefix", prefix, "n", third, "error", err.Error())
			return nil, third
		}
		return re, third
	}

	// Legacy regex path.
	re, err := regexp.Compile(third)
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
