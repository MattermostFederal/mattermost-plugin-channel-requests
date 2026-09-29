package main

import (
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const commandTrigger = "channel-request"

func getCommand() *model.Command {
	return &model.Command{
		Trigger:          commandTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "Request the creation of a new channel for admin approval",
		AutoCompleteHint: "",
		AutocompleteData: getAutocompleteData(),
		DisplayName:      "Channel Request",
	}
}

// getAutocompleteData drives the inline `/` suggestion UI. The command
// takes no arguments — it just opens the request form — so this is a
// single leaf with no subcommands. Registering it (rather than relying on
// AutoComplete alone) is what makes `/channel-request` surface in the
// autocomplete suggestion list as a user types, matching the other
// federal plugins.
func getAutocompleteData() *model.AutocompleteData {
	return model.NewAutocompleteData(
		commandTrigger,
		"",
		"Request the creation of a new channel for admin approval",
	)
}

func (p *Plugin) ExecuteCommand(_ *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	// Feature gate first: if an admin has disabled channel requests, say so
	// rather than opening a form that would be rejected on submit.
	if !p.getConfiguration().RequestEnabled(requestTypeChannel) {
		return ephemeralResponse(channelRequestsDisabledMsg), nil
	}

	// Requests require at least one configured prefix; without one the
	// dialog could only ever fail on submit. Tell the user instead of
	// opening a doomed form.
	if !p.getConfiguration().UsesPrefixList() {
		return ephemeralResponse("Channel requests aren't configured yet. Ask a System Admin to define at least one channel prefix in System Console → Plugins → Channel Requests."), nil
	}

	if err := p.openRequestDialog(args.TriggerId, args.TeamId); err != nil {
		p.API.LogError("failed to open channel request dialog", "error", err.Error())
		return ephemeralResponse("Could not open the channel request form. Please try again."), nil
	}

	return &model.CommandResponse{}, nil
}

func ephemeralResponse(text string) *model.CommandResponse {
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         text,
	}
}
