package main

import (
	"reflect"
	"regexp"
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
type configuration struct {
	// ApprovalTeam is the URL name (slug) of the team containing the approval channel.
	ApprovalTeam string

	// ApprovalChannel is the URL name (slug) of the channel where channel-creation requests are
	// posted for a System Admin to approve or deny.
	ApprovalChannel string

	// ChannelNamePrefixes is the raw multi-line text the admin entered in
	// the System Console. Each line is "prefix|description|optional_regex".
	// Parsed into prefixes below in OnConfigurationChange.
	ChannelNamePrefixes string

	// ChannelNameTemplate is the LEGACY template setting. Only used when
	// ChannelNamePrefixes is empty (backward compat with pre-prefix-picker
	// deployments). Placeholder "{{name}}" is replaced with the slugified
	// requester name.
	ChannelNameTemplate string

	// ChannelNamePattern is the LEGACY regex setting. Only used when
	// ChannelNamePrefixes is empty. Applied to the final full name.
	ChannelNamePattern string

	// prefixes is the parsed form of ChannelNamePrefixes. Nil/empty when
	// the admin left the field blank (legacy fallback path).
	prefixes []channelPrefix

	// compiledPattern is the compiled form of ChannelNamePattern, computed in OnConfigurationChange.
	// It is nil when no (valid) pattern is configured.
	compiledPattern *regexp.Regexp
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

// parsePrefixList parses the admin's multi-line prefix-list setting.
// Line format: "prefix|description|optional_regex". Empty lines and
// leading/trailing whitespace are ignored. A malformed regex causes the
// entry's SuffixPattern to be nil (permissive) with an error logged;
// the entry itself still enters the list so the prefix stays selectable.
//
// logErr is the plugin's structured logger (p.API.LogError). Passed as
// an arg so the function is testable without a live Plugin.
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
			patternRaw := strings.TrimSpace(parts[2])
			if patternRaw != "" {
				compiled, err := regexp.Compile(patternRaw)
				if err != nil {
					// Log but keep the prefix — better to have a
					// permissive prefix than to drop it entirely and
					// confuse the admin about why it disappeared.
					logErr("channel-requests: invalid suffix regex for prefix; falling back to no-pattern",
						"prefix", prefix, "pattern", patternRaw, "error", err.Error())
				} else {
					entry.SuffixPattern = compiled
					entry.SuffixPatternRaw = patternRaw
				}
			}
		}
		out = append(out, entry)
	}
	return out
}
