package main

import (
	"reflect"
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

// configuration captures the plugin's external configuration as exposed in the Mattermost server
// configuration, as well as values computed from the configuration. Any public fields will be
// deserialized from the Mattermost server configuration in OnConfigurationChange.
type configuration struct {
	// ApprovalTeam is the URL name (slug) of the team containing the approval channel.
	ApprovalTeam string

	// ApprovalChannel is the URL name (slug) of the channel where channel-creation requests are
	// posted for a System Admin to approve or deny.
	ApprovalChannel string

	// ChannelNameTemplate optionally forces every approved channel URL into a standard shape. The
	// placeholder "{{name}}" is replaced with the requester's slugified channel name.
	ChannelNameTemplate string

	// ChannelNamePattern is an optional regular expression that the final channel URL must match.
	ChannelNamePattern string

	// compiledPattern is the compiled form of ChannelNamePattern, computed in OnConfigurationChange.
	// It is nil when no (valid) pattern is configured.
	compiledPattern *regexp.Regexp
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

	// Compile the optional channel name pattern once, here, rather than on every request. An
	// invalid pattern is logged and ignored (treated as no pattern) so a typo can't wedge the
	// whole request flow.
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
