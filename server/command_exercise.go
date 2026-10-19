package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	exercisePopulate = "populate"
	exerciseTeardown = "teardown"
	exerciseStatus   = "status"
)

const exerciseUsage = "Usage: `/channel-request exercise populate|teardown|status`"

// executeExercise routes `/channel-request exercise`. The setting is
// checked first, then that the caller is a System Admin, so nobody learns
// more than that exercise mode is off.
func (p *Plugin) executeExercise(args *model.CommandArgs, params []string) *model.CommandResponse {
	if !p.getConfiguration().EnableExercise {
		return ephemeralResponse("Exercise mode is off. A System Admin can turn on **Enable exercise mode** in System Console → Plugins → Channel Requests.")
	}
	user, appErr := p.API.GetUser(args.UserId)
	if appErr != nil && !isNotFound(appErr) {
		p.API.LogWarn("failed to load the user running an exercise command", "user_id", args.UserId, "error", appErr.Error())
		return ephemeralResponse("Could not check your account. Please try again.")
	}
	if appErr != nil || !user.IsSystemAdmin() {
		return ephemeralResponse("Only System Admins can run `/channel-request exercise`.")
	}
	if args.TeamId == "" {
		return ephemeralResponse("Run `/channel-request exercise` from a channel in the team to seed.")
	}
	if len(params) != 1 {
		return ephemeralResponse(exerciseUsage)
	}

	var (
		reply string
		err   error
	)
	switch params[0] {
	case exercisePopulate:
		reply, err = p.populateExercise(args.TeamId, args.UserId)
	case exerciseTeardown:
		reply, err = p.teardownExercise(args.TeamId)
	case exerciseStatus:
		reply, err = p.exerciseStatus(args.TeamId)
	default:
		return ephemeralResponse(fmt.Sprintf("Unknown exercise subcommand `%s`. %s", params[0], exerciseUsage))
	}
	if err != nil {
		p.API.LogWarn("exercise command failed", "subcommand", params[0], "team_id", args.TeamId, "error", err.Error())
		return ephemeralResponse(fmt.Sprintf("Exercise %s failed: %s.", params[0], strings.TrimRight(err.Error(), ".")))
	}
	return ephemeralResponse(reply)
}
