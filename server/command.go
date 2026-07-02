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
		DisplayName:      "Channel Request",
	}
}

func (p *Plugin) ExecuteCommand(_ *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
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
