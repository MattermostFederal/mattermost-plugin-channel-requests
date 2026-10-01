package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pkg/errors"
)

const (
	routeDialog           = "/api/v1/dialog"
	routeCreate           = "/api/v1/create"
	routeApprove          = "/api/v1/approve"
	routeDeny             = "/api/v1/deny"
	routeRequestAdmin     = "/api/v1/request_admin" // request Channel Admin promotion on an existing channel
	routeApproveAdmin     = "/api/v1/approve_admin"
	routeDenyAdmin        = "/api/v1/deny_admin"
	routeDialogTeam       = "/api/v1/dialog_team" // team request dialog (slash command) submissions
	routeCreateTeam       = "/api/v1/create_team" // team request (webapp modal) submissions
	routeApproveTeam      = "/api/v1/approve_team"
	routeDenyTeam         = "/api/v1/deny_team"
	routeDialogTeamAdmin  = "/api/v1/dialog_team_admin"  // team-admin request dialog (slash command)
	routeRequestTeamAdmin = "/api/v1/request_team_admin" // team-admin request (webapp) submissions
	routeApproveTeamAdmin = "/api/v1/approve_team_admin"
	routeDenyTeamAdmin    = "/api/v1/deny_team_admin"
	routeDialogBotToken   = "/api/v1/dialog_bot_token" // bot-token request dialog (slash command)
	routeApproveBotToken  = "/api/v1/approve_bot_token"
	routeDenyBotToken     = "/api/v1/deny_bot_token"
	routeDialogWebhook    = "/api/v1/dialog_webhook" // webhook request dialog (slash command)
	routeApproveWebhook   = "/api/v1/approve_webhook"
	routeDenyWebhook      = "/api/v1/deny_webhook"
	routePrefixes         = "/api/v1/prefixes"
	routeTeams            = "/api/v1/teams"             // list teams for the approval-channel picker
	routeChannels         = "/api/v1/channels"          // list channels in a team, ?team_id=...
	routeUserAutocomplete = "/api/v1/user_autocomplete" // ?q=... for the request-modal member picker
	routeConfig           = "/api/v1/config"            // per-type request toggles for the webapp

	// fieldPrefix is the dialog element name for the domain-prefix
	// dropdown. Kept alongside the other field* constants in request.go.
	fieldPrefix = "prefix"

	// channelRequestsDisabledMsg is shown when a user tries to submit a
	// channel request while an admin has the feature turned off.
	channelRequestsDisabledMsg = "Channel requests are currently disabled by an administrator."

	// channelAdminRequestsDisabledMsg is the channel-admin-request counterpart.
	channelAdminRequestsDisabledMsg = "Channel admin requests are currently disabled by an administrator."

	// teamRequestsDisabledMsg is the team-request counterpart.
	teamRequestsDisabledMsg = "Team requests are currently disabled by an administrator."

	// teamAdminRequestsDisabledMsg is the team-admin-request counterpart.
	teamAdminRequestsDisabledMsg = "Team admin requests are currently disabled by an administrator."

	// botTokenRequestsDisabledMsg is the bot-token-request counterpart.
	botTokenRequestsDisabledMsg = "Bot token requests are currently disabled by an administrator."

	// webhookRequestsDisabledMsg is the webhook-request counterpart.
	webhookRequestsDisabledMsg = "Webhook requests are currently disabled by an administrator."
)

func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case routeDialog:
		p.handleDialogSubmit(w, r)
	case routeCreate:
		p.handleWebappCreate(w, r)
	case routeApprove:
		p.handleAction(w, r, true)
	case routeDeny:
		p.handleAction(w, r, false)
	case routeRequestAdmin:
		p.handleRequestAdmin(w, r)
	case routeApproveAdmin:
		p.handleAdminAction(w, r, true)
	case routeDenyAdmin:
		p.handleAdminAction(w, r, false)
	case routeDialogTeam:
		p.handleTeamDialogSubmit(w, r)
	case routeCreateTeam:
		p.handleWebappCreateTeam(w, r)
	case routeApproveTeam:
		p.handleTeamAction(w, r, true)
	case routeDenyTeam:
		p.handleTeamAction(w, r, false)
	case routeDialogTeamAdmin:
		p.handleTeamAdminDialogSubmit(w, r)
	case routeRequestTeamAdmin:
		p.handleRequestTeamAdmin(w, r)
	case routeApproveTeamAdmin:
		p.handleTeamAdminAction(w, r, true)
	case routeDenyTeamAdmin:
		p.handleTeamAdminAction(w, r, false)
	case routeDialogBotToken:
		p.handleBotTokenDialogSubmit(w, r)
	case routeApproveBotToken:
		p.handleBotTokenAction(w, r, true)
	case routeDenyBotToken:
		p.handleBotTokenAction(w, r, false)
	case routeDialogWebhook:
		p.handleWebhookDialogSubmit(w, r)
	case routeApproveWebhook:
		p.handleWebhookAction(w, r, true)
	case routeDenyWebhook:
		p.handleWebhookAction(w, r, false)
	case routePrefixes:
		p.handlePrefixes(w, r)
	case routeTeams:
		p.handleListTeams(w, r)
	case routeChannels:
		p.handleListChannels(w, r)
	case routeUserAutocomplete:
		p.handleUserAutocomplete(w, r)
	case routeConfig:
		p.handleConfig(w, r)
	default:
		http.NotFound(w, r)
	}
}

// requireUserID returns the authenticated caller's ID from the
// Mattermost-User-Id header, which the Mattermost server populates from
// the session — a client cannot forge it. Every handler that acts on
// behalf of a user MUST derive identity this way rather than trusting an
// ID in the request body. When the header is absent it writes a 401 and
// returns ("", false); callers should return immediately on !ok.
func requireUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		http.Error(w, "not authorized", http.StatusUnauthorized)
		return "", false
	}
	return userID, true
}

// handleListTeams returns the list of teams the current user can see.
// Used by the ApprovalChannelPicker component to populate the team
// dropdown. Any logged-in user gets the list (only sysadmins reach the
// admin console anyway; MM enforces that at the UI level).
func (p *Plugin) handleListTeams(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	teams, appErr := p.API.GetTeamsForUser(userID)
	if appErr != nil {
		p.API.LogWarn("list-teams failed", "user_id", userID, "error", appErr.Error())
		http.Error(w, "failed to list teams", http.StatusInternalServerError)
		return
	}
	type teamDTO struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
	}
	out := make([]teamDTO, 0, len(teams))
	for _, t := range teams {
		out = append(out, teamDTO{ID: t.Id, Name: t.Name, DisplayName: t.DisplayName})
	}
	writeJSON(w, out)
}

// handleListChannels returns the channels in a team the current user
// can see. Query param: team_id. Filters to public + private channels
// (excludes DMs/GMs — a DM channel isn't a valid approval destination).
func (p *Plugin) handleListChannels(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	teamID := r.URL.Query().Get("team_id")
	if teamID == "" {
		http.Error(w, "team_id required", http.StatusBadRequest)
		return
	}
	channels, appErr := p.API.GetChannelsForTeamForUser(teamID, userID, false)
	if appErr != nil {
		p.API.LogWarn("list-channels failed", "team_id", teamID, "user_id", userID, "error", appErr.Error())
		http.Error(w, "failed to list channels", http.StatusInternalServerError)
		return
	}
	type channelDTO struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		Type        string `json:"type"`
	}
	out := make([]channelDTO, 0, len(channels))
	for _, c := range channels {
		// Only public + private — DMs/GMs make no sense as an approval channel.
		if c.Type != model.ChannelTypeOpen && c.Type != model.ChannelTypePrivate {
			continue
		}
		out = append(out, channelDTO{
			ID:          c.Id,
			Name:        c.Name,
			DisplayName: c.DisplayName,
			Type:        string(c.Type),
		})
	}
	writeJSON(w, out)
}

// handleUserAutocomplete returns up to 20 users matching the query
// string. Used by the request modal's member picker. Proxies to MM's
// SearchUsers API. When ?team_id=... is set, filters to users who are
// members of that team — critical for the "which people can I add to
// a channel in this team" flow.
func (p *Plugin) handleUserAutocomplete(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, []any{})
		return
	}
	teamID := strings.TrimSpace(r.URL.Query().Get("team_id"))

	// Scope enumeration to a team the caller actually belongs to. The
	// plugin SearchUsers API runs with admin visibility and no viewer
	// restriction, so forwarding an arbitrary team_id would let any
	// logged-in user harvest the usernames/real-names of teams they
	// aren't in. Requiring caller membership limits results to rosters
	// the caller can already see, which is exactly the set they'd add to
	// a channel in that team.
	if teamID == "" {
		writeJSON(w, []any{})
		return
	}
	// GetTeamMember returns soft-deleted rows too, so a former member (who
	// left the team) would still pass a bare error check. Require an active
	// membership before scoping the search to this team.
	member, appErr := p.API.GetTeamMember(teamID, userID)
	if appErr != nil || member == nil || member.DeleteAt != 0 {
		writeJSON(w, []any{})
		return
	}

	// Limit MUST be non-zero — MM's UserSearch treats Limit=0 as
	// "return no results" (not "unlimited"). Set Limit to a
	// slightly-larger cap than our display cap of 20 so we still
	// have headroom after post-filtering already-selected users.
	users, appErr := p.API.SearchUsers(&model.UserSearch{
		Term:          q,
		TeamId:        teamID,
		AllowInactive: false,
		Limit:         50,
	})
	if appErr != nil {
		p.API.LogWarn("user autocomplete failed", "q", q, "team_id", teamID, "error", appErr.Error())
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}

	type userDTO struct {
		ID        string `json:"id"`
		Username  string `json:"username"`
		Nickname  string `json:"nickname"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	}
	if len(users) > 20 {
		users = users[:20]
	}
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		out = append(out, userDTO{
			ID:        u.Id,
			Username:  u.Username,
			Nickname:  u.Nickname,
			FirstName: u.FirstName,
			LastName:  u.LastName,
		})
	}
	writeJSON(w, out)
}

// handlePrefixes serves the current admin-configured prefix list so the
// webapp modal can populate its dropdown. Read-only, authenticated
// (any logged-in user can see it — same visibility as the plugin
// settings page shows anyway).
func (p *Plugin) handlePrefixes(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	config := p.getConfiguration()
	type prefixDTO struct {
		Prefix      string `json:"prefix"`
		Description string `json:"description"`
		SuffixRegex string `json:"suffix_regex"`
	}
	out := make([]prefixDTO, 0, len(config.prefixes))
	for _, p := range config.prefixes {
		out = append(out, prefixDTO{
			Prefix:      p.Prefix,
			Description: p.Description,
			SuffixRegex: p.SuffixPatternRaw,
		})
	}
	writeJSON(w, out)
}

// handleConfig serves the per-type request toggles so the webapp can hide
// entry points for disabled request types. Read-only and authenticated;
// any logged-in user may read it (the flags aren't sensitive, and the
// server re-checks them authoritatively on every submission). The webapp
// treats a load failure as fail-open so a transient error never hides a
// working feature.
func (p *Plugin) handleConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	config := p.getConfiguration()
	writeJSON(w, map[string]bool{
		requestTypeChannel:      config.RequestEnabled(requestTypeChannel),
		requestTypeChannelAdmin: config.RequestEnabled(requestTypeChannelAdmin),
		requestTypeTeam:         config.RequestEnabled(requestTypeTeam),
		requestTypeWebhook:      config.RequestEnabled(requestTypeWebhook),
	})
}

// openRequestDialog opens the interactive channel request dialog for the
// slash command entry point. Callers must ensure a prefix list is
// configured (ExecuteCommand guards this) — the dialog always presents a
// "Domain prefix" dropdown and frames the URL field as a "URL suffix".
func (p *Plugin) openRequestDialog(triggerID, teamID string) error {
	config := p.getConfiguration()

	prefixOptions := make([]*model.PostActionOptions, 0, len(config.prefixes))
	for _, pf := range config.prefixes {
		label := pf.Prefix
		if pf.Description != "" {
			label = fmt.Sprintf("%s  (%s)", pf.Prefix, pf.Description)
		}
		prefixOptions = append(prefixOptions, &model.PostActionOptions{Text: label, Value: pf.Prefix})
	}

	elements := []model.DialogElement{
		{
			DisplayName: "Domain prefix",
			Name:        fieldPrefix,
			Type:        "select",
			Options:     prefixOptions,
			HelpText:    "Pick the category for this channel. The final URL is <prefix><suffix>.",
		},
		{
			DisplayName: "Channel name",
			Name:        fieldDisplayName,
			Type:        "text",
			Placeholder: "e.g. Marketing Team",
			MaxLength:   64,
		},
		{
			DisplayName: "URL suffix",
			Name:        fieldName,
			Type:        "text",
			Optional:    true,
			HelpText:    "The part AFTER the prefix. Lowercase letters, numbers, and hyphens. Leave blank to generate from the channel name.",
			MaxLength:   64,
		},
		{
			DisplayName: "Purpose",
			Name:        fieldPurpose,
			Type:        "textarea",
			Optional:    true,
			MaxLength:   250,
		},
		{
			DisplayName: "Visibility",
			Name:        fieldType,
			Type:        "radio",
			Default:     channelTypeOpen,
			Options: []*model.PostActionOptions{
				{Text: "Public", Value: channelTypeOpen},
				{Text: "Private", Value: channelTypePrivate},
			},
		},
		{
			DisplayName: "Members to add",
			Name:        fieldMembers,
			Type:        "select",
			DataSource:  "users",
			MultiSelect: true,
			Optional:    true,
			HelpText:    "These users are added to the channel once it's approved.",
		},
		{
			DisplayName: "Channel admins",
			Name:        fieldAdmins,
			Type:        "select",
			DataSource:  "users",
			MultiSelect: true,
			Optional:    true,
			HelpText:    "These users are made channel admins once the channel is approved.",
		},
	}

	dialog := model.Dialog{
		CallbackId:       dialogCallbackID,
		Title:            "Request a Channel",
		IntroductionText: "This request will be sent to an admin for approval.",
		SubmitLabel:      "Submit request",
		State:            teamID,
		Elements:         elements,
	}

	if appErr := p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeDialog),
		Dialog:    dialog,
	}); appErr != nil {
		return appErr
	}
	return nil
}

func (p *Plugin) handleDialogSubmit(w http.ResponseWriter, r *http.Request) {
	// Identity comes from the authenticated header, NOT submission.UserId
	// in the body — trusting the body would let anyone impersonate a
	// System Admin (or auto-approve user) and bypass approval.
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var submission model.SubmitDialogRequest
	if err := json.NewDecoder(r.Body).Decode(&submission); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if submission.Cancelled {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Gate at the entry point: the modal/dialog may still be open from before
	// an admin disabled the feature, so reject the submission rather than
	// trusting the client to have hidden the form.
	if !p.getConfiguration().RequestEnabled(requestTypeChannel) {
		writeJSON(w, model.SubmitDialogResponse{Error: channelRequestsDisabledMsg})
		return
	}

	teamID := submission.State
	if teamID == "" {
		teamID = submission.TeamId
	}

	in := requestInput{
		RequesterID: userID,
		TeamID:      teamID,
		DisplayName: submissionString(submission.Submission, fieldDisplayName),
		Name:        submissionString(submission.Submission, fieldName),
		// Prefix is present in submission only when the dialog was
		// opened with the prefix-list feature active.
		Prefix:         submissionString(submission.Submission, fieldPrefix),
		Purpose:        submissionString(submission.Submission, fieldPurpose),
		ChannelType:    submissionString(submission.Submission, fieldType),
		MemberIDs:      splitIDs(submissionString(submission.Submission, fieldMembers)),
		AdminMemberIDs: splitIDs(submissionString(submission.Submission, fieldAdmins)),
	}

	message, err := p.submitRequest(in)
	if err != nil {
		writeJSON(w, dialogErrorResponse(err))
		return
	}

	// Acknowledge success with an ephemeral message in the channel the dialog was opened from.
	p.API.SendEphemeralPost(userID, &model.Post{
		ChannelId: submission.ChannelId,
		Message:   message,
	})
	w.WriteHeader(http.StatusOK)
}

// webappCreateRequest is the JSON payload sent by the webapp modal.
type webappCreateRequest struct {
	TeamID      string `json:"team_id"`
	DisplayName string `json:"display_name"`
	Name        string `json:"name"`
	// Prefix is the selected domain prefix from the modal's dropdown
	// (e.g., "team-"). Required — the server rejects requests without a
	// configured prefix.
	Prefix       string   `json:"prefix"`
	Purpose      string   `json:"purpose"`
	ChannelType  string   `json:"channel_type"`
	Members      []string `json:"members"`       // usernames — regular members
	AdminMembers []string `json:"admin_members"` // usernames — Channel Admins
}

func (p *Plugin) handleWebappCreate(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	if !p.getConfiguration().RequestEnabled(requestTypeChannel) {
		writeJSON(w, map[string]string{"error": channelRequestsDisabledMsg})
		return
	}

	var body webappCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	memberIDs, err := p.resolveUsernameList(body.Members)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	adminMemberIDs, err := p.resolveUsernameList(body.AdminMembers)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	message, err := p.submitRequest(requestInput{
		RequesterID:    userID,
		TeamID:         body.TeamID,
		DisplayName:    body.DisplayName,
		Name:           body.Name,
		Prefix:         body.Prefix,
		Purpose:        body.Purpose,
		ChannelType:    body.ChannelType,
		MemberIDs:      memberIDs,
		AdminMemberIDs: adminMemberIDs,
	})
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, map[string]string{"message": message})
}

// openTeamRequestDialog opens the interactive team request dialog for the slash
// command entry point. Teams use no prefix list, so the dialog presents a plain
// "Team name" + optional "URL name" pair.
func (p *Plugin) openTeamRequestDialog(triggerID string) error {
	elements := []model.DialogElement{
		{
			DisplayName: "Team name",
			Name:        fieldDisplayName,
			Type:        "text",
			Placeholder: "e.g. Marketing",
			MaxLength:   maxDisplayNameLen,
		},
		{
			DisplayName: "URL name",
			Name:        fieldName,
			Type:        "text",
			Optional:    true,
			HelpText:    "The team's URL. Lowercase letters, numbers, and hyphens. Leave blank to generate from the team name.",
			MaxLength:   model.TeamNameMaxLength,
		},
		{
			DisplayName: "Description",
			Name:        fieldDescription,
			Type:        "textarea",
			Optional:    true,
			MaxLength:   maxPurposeLen,
		},
		{
			DisplayName: "Visibility",
			Name:        fieldTeamType,
			Type:        "radio",
			Default:     teamTypeOpen,
			Options: []*model.PostActionOptions{
				{Text: "Open (anyone on the server can join)", Value: teamTypeOpen},
				{Text: "Invite only", Value: teamTypeInvite},
			},
		},
		{
			DisplayName: "Members to add",
			Name:        fieldMembers,
			Type:        "select",
			DataSource:  "users",
			MultiSelect: true,
			Optional:    true,
			HelpText:    "These users are added to the team once it's approved.",
		},
	}

	dialog := model.Dialog{
		CallbackId:       teamDialogCallbackID,
		Title:            "Request a Team",
		IntroductionText: "This request will be sent to an admin for approval.",
		SubmitLabel:      "Submit request",
		Elements:         elements,
	}

	if appErr := p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeDialogTeam),
		Dialog:    dialog,
	}); appErr != nil {
		return appErr
	}
	return nil
}

func (p *Plugin) handleTeamDialogSubmit(w http.ResponseWriter, r *http.Request) {
	// Identity comes from the authenticated header, NOT the submission body.
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var submission model.SubmitDialogRequest
	if err := json.NewDecoder(r.Body).Decode(&submission); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if submission.Cancelled {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Gate at the entry point: the dialog may still be open from before an
	// admin disabled the feature, so reject the submission server-side.
	if !p.getConfiguration().RequestEnabled(requestTypeTeam) {
		writeJSON(w, model.SubmitDialogResponse{Error: teamRequestsDisabledMsg})
		return
	}

	in := teamRequestInput{
		RequesterID: userID,
		DisplayName: submissionString(submission.Submission, fieldDisplayName),
		Name:        submissionString(submission.Submission, fieldName),
		Description: submissionString(submission.Submission, fieldDescription),
		TeamType:    submissionString(submission.Submission, fieldTeamType),
		MemberIDs:   splitIDs(submissionString(submission.Submission, fieldMembers)),
	}

	message, err := p.submitTeamRequest(in)
	if err != nil {
		writeJSON(w, dialogErrorResponse(err))
		return
	}

	p.API.SendEphemeralPost(userID, &model.Post{
		ChannelId: submission.ChannelId,
		Message:   message,
	})
	w.WriteHeader(http.StatusOK)
}

// webappCreateTeamRequest is the JSON payload sent by the webapp team modal.
type webappCreateTeamRequest struct {
	DisplayName string   `json:"display_name"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	TeamType    string   `json:"team_type"`
	Members     []string `json:"members"` // usernames
}

func (p *Plugin) handleWebappCreateTeam(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	if !p.getConfiguration().RequestEnabled(requestTypeTeam) {
		writeJSON(w, map[string]string{"error": teamRequestsDisabledMsg})
		return
	}

	var body webappCreateTeamRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	memberIDs, err := p.resolveUsernameList(body.Members)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	message, err := p.submitTeamRequest(teamRequestInput{
		RequesterID: userID,
		DisplayName: body.DisplayName,
		Name:        body.Name,
		Description: body.Description,
		TeamType:    body.TeamType,
		MemberIDs:   memberIDs,
	})
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, map[string]string{"message": message})
}

// handleTeamAction handles Approve/Deny on a team-creation request. Mirrors
// handleAction but operates on teamRequest records and creates a team.
func (p *Plugin) handleTeamAction(w http.ResponseWriter, r *http.Request, approve bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var request model.PostActionIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	// Team-creation approvers are the global approvers (System Admins, plus
	// opted-in approval-team admins) — there's no target team to scope to.
	actingUser, appErr := p.API.GetUser(userID)
	if appErr != nil {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not verify your identity to approve/deny."})
		return
	}
	if !p.canApprove(actingUser) {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "You don't have permission to approve or deny team requests. Contact a System Admin."})
		return
	}

	requestID, _ := request.Context[actionContextRequestID].(string)
	req, rawReq, err := p.loadTeamRequest(requestID)
	if err != nil {
		p.API.LogError("failed to load team request", "error", err.Error())
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not load that request."})
		return
	}
	if req == nil {
		writeJSON(w, model.PostActionIntegrationResponse{
			Update: p.resolvedPost(request.PostId, "This request has already been handled."),
		})
		return
	}

	// Atomically claim the request before acting — same concurrency guard as
	// handleAction/handleAdminAction.
	claimed, claimErr := p.API.KVCompareAndDelete(kvTeamRequestPrefix+req.ID, rawReq)
	if claimErr != nil {
		p.API.LogError("failed to claim team request", "error", claimErr.Error())
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not process that request."})
		return
	}
	if !claimed {
		writeJSON(w, model.PostActionIntegrationResponse{
			Update: p.resolvedPost(request.PostId, "This request has already been handled."),
		})
		return
	}

	config := p.getConfiguration()
	requester, _ := p.API.GetUser(req.RequesterID) // best-effort for create + notify + audit

	var outcome string
	if approve {
		if requester == nil {
			// The requester is needed as the team's owner/contact email; if we
			// can't load them, restore the request rather than dropping it.
			if restoreErr := p.storeTeamRequest(req); restoreErr != nil {
				p.API.LogError("failed to restore team request after requester-load failure", "error", restoreErr.Error())
			}
			writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not load the requester; the request is still pending."})
			return
		}
		team, requesterPromoted, createErr := p.createTeamForRequest(req, requester)
		if createErr != nil {
			p.API.LogError("failed to create team on approval", "error", createErr.Error())
			// Restore so a transient failure doesn't silently drop the request.
			if restoreErr := p.storeTeamRequest(req); restoreErr != nil {
				p.API.LogError("failed to restore team request after create failure", "error", restoreErr.Error())
				writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: fmt.Sprintf("Could not create the team (%s), and the pending request could not be saved — ask the requester to submit it again.", createErr.Error())})
				return
			}
			// Repaint the card with a visible warning banner (keeping the
			// Approve/Deny buttons) so the failure isn't only an
			// easy-to-miss ephemeral. The request stays pending: the
			// approver can retry Approve or Deny it.
			notice := fmt.Sprintf("Couldn't create the team: %s. The request is still pending — retry **Approve**, or **Deny** it. If this keeps failing, the team URL is likely already taken; ask the requester to resubmit with a different name.", createErr.Error())
			writeJSON(w, model.PostActionIntegrationResponse{
				EphemeralText: notice,
				Update: p.repaintedApprovalPost(
					request.PostId,
					"@channel — a team request needs your review (a previous approval attempt failed).",
					p.teamApprovalAttachmentWithNotice(req, requester, notice),
				),
			})
			return
		}
		outcome = fmt.Sprintf("✅ Approved by @%s. Team **%s** created.", actingUser.Username, team.DisplayName)
		// Only tell the requester they're a Team Admin if the promotion actually
		// succeeded; otherwise they may be a plain member (or not added).
		if requesterPromoted {
			p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request for team **%s** was approved. You're now a Team Admin of it.", req.DisplayName))
		} else {
			p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request for team **%s** was approved and the team was created. We couldn't set you as a Team Admin automatically — ask an admin to grant it.", req.DisplayName))
		}
		p.logAudit(config, fmt.Sprintf("TEAM APPROVED: @%s approved team request `%s` (%s) from @%s",
			actingUser.Username, req.DisplayName, team.Name, requesterUsername(requester, req.RequesterID)))
	} else {
		outcome = fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request for team **%s** was denied.", req.DisplayName))
		p.logAudit(config, fmt.Sprintf("TEAM DENIED: @%s denied team request `%s` from @%s",
			actingUser.Username, req.DisplayName, requesterUsername(requester, req.RequesterID)))
	}

	writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
}

// openTeamAdminRequestDialog opens the interactive team-admin request dialog for
// the slash-command entry point, scoped to the team the command was run in
// (carried in State).
func (p *Plugin) openTeamAdminRequestDialog(triggerID, teamID string) error {
	elements := []model.DialogElement{
		{
			DisplayName: "People to make Team Admin",
			Name:        fieldNominees,
			Type:        "select",
			DataSource:  "users",
			MultiSelect: true,
			HelpText:    "These users are promoted to Team Admin of this team once approved.",
		},
	}

	dialog := model.Dialog{
		CallbackId:       teamAdminDialogCallbackID,
		Title:            "Request Team Admin",
		IntroductionText: "This request will be sent to an admin for approval.",
		SubmitLabel:      "Submit request",
		State:            teamID,
		Elements:         elements,
	}

	if appErr := p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeDialogTeamAdmin),
		Dialog:    dialog,
	}); appErr != nil {
		return appErr
	}
	return nil
}

func (p *Plugin) handleTeamAdminDialogSubmit(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var submission model.SubmitDialogRequest
	if err := json.NewDecoder(r.Body).Decode(&submission); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if submission.Cancelled {
		w.WriteHeader(http.StatusOK)
		return
	}

	if !p.getConfiguration().RequestEnabled(requestTypeTeamAdmin) {
		writeJSON(w, model.SubmitDialogResponse{Error: teamAdminRequestsDisabledMsg})
		return
	}

	teamID := submission.State
	if teamID == "" {
		teamID = submission.TeamId
	}

	message, err := p.submitTeamAdminRequest(userID, teamID, splitIDs(submissionString(submission.Submission, fieldNominees)))
	if err != nil {
		writeJSON(w, model.SubmitDialogResponse{Error: err.Error()})
		return
	}

	p.API.SendEphemeralPost(userID, &model.Post{
		ChannelId: submission.ChannelId,
		Message:   message,
	})
	w.WriteHeader(http.StatusOK)
}

// teamAdminRequestBody is the JSON payload sent by a webapp "Request Team Admin"
// entry point (usernames to promote).
type teamAdminRequestBody struct {
	TeamID   string   `json:"team_id"`
	Nominees []string `json:"nominees"`
}

func (p *Plugin) handleRequestTeamAdmin(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	if !p.getConfiguration().RequestEnabled(requestTypeTeamAdmin) {
		writeJSON(w, map[string]string{"error": teamAdminRequestsDisabledMsg})
		return
	}

	var body teamAdminRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	nomineeIDs, err := p.resolveUsernameList(body.Nominees)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	message, err := p.submitTeamAdminRequest(userID, body.TeamID, nomineeIDs)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, map[string]string{"message": message})
}

// handleTeamAdminAction handles Approve/Deny on a team-admin request. Mirrors
// handleAdminAction but operates on teamAdminRequest records and promotes Team
// Admins.
func (p *Plugin) handleTeamAdminAction(w http.ResponseWriter, r *http.Request, approve bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var request model.PostActionIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	actingUser, appErr := p.API.GetUser(userID)
	if appErr != nil {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not verify your identity to approve/deny."})
		return
	}

	requestID, _ := request.Context[actionContextRequestID].(string)
	req, rawReq, err := p.loadTeamAdminRequest(requestID)
	if err != nil {
		p.API.LogError("failed to load team-admin request", "error", err.Error())
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not load that request."})
		return
	}
	if req == nil {
		writeJSON(w, model.PostActionIntegrationResponse{
			Update: p.resolvedPost(request.PostId, "This request has already been handled."),
		})
		return
	}

	// A team-admin request is scoped to a team, so that team's own Team Admins
	// may act on it (in addition to the global approvers). Needs the loaded
	// request's TeamID, so the check happens here.
	if !p.canApproveTeamAdminRequest(actingUser, req.TeamID) {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "You don't have permission to approve or deny this Team Admin request. Contact a Team Admin or System Admin."})
		return
	}

	config := p.getConfiguration()
	requester, _ := p.API.GetUser(req.RequesterID) // best-effort for notify + audit
	team, teamErr := p.API.GetTeam(req.TeamID)

	teamRef := "the team"
	if teamErr == nil {
		teamRef = "**" + team.DisplayName + "**"
	}
	nominees := p.mentionList(req.NomineeIDs)

	// Atomically claim the request before acting — same concurrency guard as
	// the other action handlers.
	claimed, claimErr := p.API.KVCompareAndDelete(kvTeamAdminRequestPrefix+req.ID, rawReq)
	if claimErr != nil {
		p.API.LogError("failed to claim team-admin request", "error", claimErr.Error())
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not process that request."})
		return
	}
	if !claimed {
		writeJSON(w, model.PostActionIntegrationResponse{
			Update: p.resolvedPost(request.PostId, "This request has already been handled."),
		})
		return
	}

	var outcome string
	if approve {
		// Report only who was actually promoted — a privileged grant (and its
		// audit line) must not claim promotions that failed.
		promoted, failed := p.promoteTeamAdmins(req)
		promotedList := p.mentionList(promoted)
		switch {
		case len(promoted) == 0:
			outcome = fmt.Sprintf("⚠️ Approved by @%s, but no one could be promoted to Team Admin in %s (%s). Ask the requester to resubmit.", actingUser.Username, teamRef, p.mentionList(failed))
			p.notifyRequester(req.RequesterID, fmt.Sprintf("Your Team Admin request in %s was approved, but %s could not be promoted. Please resubmit.", teamRef, p.mentionList(failed)))
			p.logAudit(config, fmt.Sprintf("TEAM ADMIN APPROVED (FAILED): @%s approved a Team Admin request in %s but no promotions succeeded (%s); requested by @%s",
				actingUser.Username, teamRef, p.mentionList(failed), requesterUsername(requester, req.RequesterID)))
		case len(failed) > 0:
			outcome = fmt.Sprintf("✅ Approved by @%s. %s promoted to Team Admin in %s. ⚠️ Could not promote %s.", actingUser.Username, promotedList, teamRef, p.mentionList(failed))
			p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request made %s Team Admin in %s. %s could not be promoted — check they're active members.", promotedList, teamRef, p.mentionList(failed)))
			p.logAudit(config, fmt.Sprintf("TEAM ADMIN APPROVED (PARTIAL): @%s promoted %s to Team Admin in %s; failed: %s (requested by @%s)",
				actingUser.Username, promotedList, teamRef, p.mentionList(failed), requesterUsername(requester, req.RequesterID)))
		default:
			outcome = fmt.Sprintf("✅ Approved by @%s. %s promoted to Team Admin in %s.", actingUser.Username, promotedList, teamRef)
			p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request to make %s Team Admin in %s was approved.", promotedList, teamRef))
			p.logAudit(config, fmt.Sprintf("TEAM ADMIN APPROVED: @%s promoted %s to Team Admin in %s (requested by @%s)",
				actingUser.Username, promotedList, teamRef, requesterUsername(requester, req.RequesterID)))
		}
	} else {
		outcome = fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request to make %s Team Admin in %s was denied.", nominees, teamRef))
		p.logAudit(config, fmt.Sprintf("TEAM ADMIN DENIED: @%s denied a Team Admin request for %s in %s",
			actingUser.Username, nominees, teamRef))
	}

	writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
}

// openWebhookRequestDialog opens the interactive webhook request dialog, scoped
// to the channel the command was run in (carried in State).
func (p *Plugin) openWebhookRequestDialog(triggerID, channelID string) error {
	elements := []model.DialogElement{
		{
			DisplayName: "Webhook name",
			Name:        fieldWebhookName,
			Type:        "text",
			Placeholder: "e.g. Deploy notifications",
			MaxLength:   maxDisplayNameLen,
		},
		{
			DisplayName: "What's it for?",
			Name:        fieldWebhookDescription,
			Type:        "textarea",
			Optional:    true,
			HelpText:    "Helps approvers decide. Webhook requests need a security and a system approval.",
			MaxLength:   maxPurposeLen,
		},
	}

	dialog := model.Dialog{
		CallbackId:       webhookDialogCallbackID,
		Title:            "Request an Incoming Webhook",
		IntroductionText: "This request needs approval from a security approver and a system approver. The URL will be sent to you privately.",
		SubmitLabel:      "Submit request",
		State:            channelID,
		Elements:         elements,
	}

	if appErr := p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeDialogWebhook),
		Dialog:    dialog,
	}); appErr != nil {
		return appErr
	}
	return nil
}

func (p *Plugin) handleWebhookDialogSubmit(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var submission model.SubmitDialogRequest
	if err := json.NewDecoder(r.Body).Decode(&submission); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if submission.Cancelled {
		w.WriteHeader(http.StatusOK)
		return
	}

	if !p.getConfiguration().RequestEnabled(requestTypeWebhook) {
		writeJSON(w, model.SubmitDialogResponse{Error: webhookRequestsDisabledMsg})
		return
	}

	channelID := submission.State
	if channelID == "" {
		channelID = submission.ChannelId
	}

	message, err := p.submitWebhookRequest(webhookRequestInput{
		RequesterID: userID,
		ChannelID:   channelID,
		DisplayName: submissionString(submission.Submission, fieldWebhookName),
		Description: submissionString(submission.Submission, fieldWebhookDescription),
	})
	if err != nil {
		writeJSON(w, model.SubmitDialogResponse{Error: err.Error()})
		return
	}

	p.API.SendEphemeralPost(userID, &model.Post{
		ChannelId: submission.ChannelId,
		Message:   message,
	})
	w.WriteHeader(http.StatusOK)
}

// handleWebhookAction handles Approve/Deny on a webhook request via the two-step
// engine. Parallels handleBotTokenAction; on final approval it creates the
// incoming webhook (REST) and DMs the URL to the requester.
func (p *Plugin) handleWebhookAction(w http.ResponseWriter, r *http.Request, approve bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var request model.PostActionIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	actingUser, appErr := p.API.GetUser(userID)
	if appErr != nil {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not verify your identity to approve/deny."})
		return
	}

	requestID, _ := request.Context[actionContextRequestID].(string)
	key := kvWebhookRequestPrefix + requestID
	req, rawReq, err := p.loadWebhookRequest(requestID)
	if err != nil {
		p.API.LogError("failed to load webhook request", "error", err.Error())
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not load that request."})
		return
	}
	if req == nil {
		writeJSON(w, model.PostActionIntegrationResponse{
			Update: p.resolvedPost(request.PostId, "This request has already been handled."),
		})
		return
	}

	canSec, errSec := p.canApproveStep(actingUser, stepSecurity)
	canSys, errSys := p.canApproveStep(actingUser, stepSystem)
	if errSec != nil || errSys != nil {
		p.API.LogError("failed to check webhook approver eligibility", "user_id", userID)
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not check your approver permissions. Please try again."})
		return
	}

	config := p.getConfiguration()
	requester, _ := p.API.GetUser(req.RequesterID)

	if !approve {
		if !canSec && !canSys {
			writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "You don't have permission to deny this request."})
			return
		}
		claimed, claimErr := p.API.KVCompareAndDelete(key, rawReq)
		if claimErr != nil {
			writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not process that request."})
			return
		}
		if !claimed {
			writeJSON(w, model.PostActionIntegrationResponse{
				Update: p.resolvedPost(request.PostId, "This request has already been handled."),
			})
			return
		}
		outcome := fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequester(req.RequesterID, fmt.Sprintf("Your incoming webhook request for ~%s was denied.", req.ChannelName))
		p.logAudit(config, fmt.Sprintf("WEBHOOK DENIED: @%s denied a webhook request for ~%s from @%s",
			actingUser.Username, req.ChannelName, requesterUsername(requester, req.RequesterID)))
		writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
		return
	}

	step, fill, reason := planApproval(req.twoStepState, canSec, canSys, userID)
	if !fill {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: reason})
		return
	}

	newState := req.twoStepState.withApproval(step, userID, model.GetMillis())

	if newState.complete() {
		claimed, claimErr := p.API.KVCompareAndDelete(key, rawReq)
		if claimErr != nil {
			writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not process that request."})
			return
		}
		if !claimed {
			writeJSON(w, model.PostActionIntegrationResponse{
				Update: p.resolvedPost(request.PostId, "This request has already been handled."),
			})
			return
		}
		url, hookID, createErr := p.createIncomingWebhookForRequest(req)
		if createErr != nil {
			p.API.LogError("failed to create webhook on approval", "error", createErr.Error())
			if restoreErr := p.API.KVSet(key, rawReq); restoreErr != nil {
				p.API.LogError("failed to restore webhook request after create failure", "error", restoreErr.Error())
				writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: fmt.Sprintf("Could not create the webhook (%s), and the pending request could not be saved — ask the requester to submit it again.", createErr.Error())})
				return
			}
			notice := fmt.Sprintf("Couldn't create the webhook: %s. The request is still pending — click **Approve** again to retry, or **Deny** it.", createErr.Error())
			cardRequester := requester
			if cardRequester == nil {
				cardRequester = &model.User{Username: requesterUsername(nil, req.RequesterID)}
			}
			writeJSON(w, model.PostActionIntegrationResponse{
				EphemeralText: notice,
				Update: p.repaintedApprovalPost(
					request.PostId,
					"@channel — a webhook request needs review (a previous approval attempt failed).",
					p.webhookApprovalAttachmentWithNotice(req, cardRequester, notice),
				),
			})
			return
		}
		if deliverErr := p.deliverWebhookURL(req.RequesterID, req.ChannelName, url); deliverErr != nil {
			// The request is already resolved and the URL is a secret, so an
			// undelivered hook is an unowned secret endpoint. Delete it and
			// report the failure instead of a false success.
			p.API.LogError("failed to deliver webhook URL to requester", "user_id", req.RequesterID, "error", deliverErr.Error())
			removed := true
			if delErr := p.deleteIncomingWebhook(hookID); delErr != nil {
				p.API.LogError("failed to delete webhook after URL delivery failure", "hook_id", hookID, "error", delErr.Error())
				removed = false
			}
			var outcome string
			if removed {
				p.logAudit(config, fmt.Sprintf("WEBHOOK DELIVERY FAILED: an incoming webhook for ~%s was created for @%s but the URL could not be delivered; the webhook was removed",
					req.ChannelName, requesterUsername(requester, req.RequesterID)))
				outcome = fmt.Sprintf("⚠️ Approved, but the URL could not be delivered to the requester, so the incoming webhook for ~%s was removed. Ask them to submit the request again.", req.ChannelName)
			} else {
				p.logAudit(config, fmt.Sprintf("WEBHOOK DELIVERY FAILED: an incoming webhook for ~%s was created for @%s but the URL could not be delivered AND the webhook could not be removed — manual cleanup needed",
					req.ChannelName, requesterUsername(requester, req.RequesterID)))
				outcome = fmt.Sprintf("⚠️ Approved, but the URL could not be delivered to the requester and the incoming webhook for ~%s could not be removed automatically — an admin should delete it. Ask the requester to submit again.", req.ChannelName)
			}
			writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
			return
		}
		outcome := fmt.Sprintf("✅ Fully approved (security + system). Incoming webhook for ~%s created; the URL was sent privately to the requester.", req.ChannelName)
		p.logAudit(config, fmt.Sprintf("WEBHOOK APPROVED: @%s gave final approval; incoming webhook for ~%s created for @%s",
			actingUser.Username, req.ChannelName, requesterUsername(requester, req.RequesterID)))
		writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
		return
	}

	newReq := *req
	newReq.twoStepState = newState
	newBytes, marshalErr := json.Marshal(&newReq)
	if marshalErr != nil {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not process that request."})
		return
	}
	set, setErr := p.API.KVCompareAndSet(key, rawReq, newBytes)
	if setErr != nil || !set {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "This request was just updated by someone else. Please try again."})
		return
	}
	p.notifyRequester(req.RequesterID, fmt.Sprintf("Your webhook request for ~%s received the %s approval; awaiting the other approval.", req.ChannelName, step))
	cardRequester := requester
	if cardRequester == nil {
		cardRequester = &model.User{Username: requesterUsername(nil, req.RequesterID)}
	}
	writeJSON(w, model.PostActionIntegrationResponse{
		Update: p.repaintedApprovalPost(
			request.PostId,
			"@channel — an incoming webhook request needs review (requires a security approval and a system approval).",
			p.webhookApprovalAttachment(&newReq, cardRequester),
		),
	})
}

// openBotTokenRequestDialog opens the interactive bot-token request dialog.
func (p *Plugin) openBotTokenRequestDialog(triggerID string) error {
	elements := []model.DialogElement{
		{
			DisplayName: "Bot username",
			Name:        fieldUsername,
			Type:        "text",
			Placeholder: "e.g. deploy-bot",
			HelpText:    "Lowercase letters, numbers, and . - _ (3-22 characters).",
			MaxLength:   22,
		},
		{
			DisplayName: "Display name",
			Name:        fieldDisplayName,
			Type:        "text",
			Optional:    true,
			MaxLength:   maxDisplayNameLen,
		},
		{
			DisplayName: "What's it for?",
			Name:        fieldBotDescription,
			Type:        "textarea",
			Optional:    true,
			HelpText:    "Helps approvers decide. Bot token requests need a security and a system approval.",
			MaxLength:   1024,
		},
	}

	dialog := model.Dialog{
		CallbackId:       botTokenDialogCallbackID,
		Title:            "Request a Bot Token",
		IntroductionText: "This request needs approval from a security approver and a system approver. The token will be sent to you privately.",
		SubmitLabel:      "Submit request",
		Elements:         elements,
	}

	if appErr := p.API.OpenInteractiveDialog(model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s%s", manifest.Id, routeDialogBotToken),
		Dialog:    dialog,
	}); appErr != nil {
		return appErr
	}
	return nil
}

func (p *Plugin) handleBotTokenDialogSubmit(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var submission model.SubmitDialogRequest
	if err := json.NewDecoder(r.Body).Decode(&submission); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if submission.Cancelled {
		w.WriteHeader(http.StatusOK)
		return
	}

	if !p.getConfiguration().RequestEnabled(requestTypeBotToken) {
		writeJSON(w, model.SubmitDialogResponse{Error: botTokenRequestsDisabledMsg})
		return
	}

	message, err := p.submitBotTokenRequest(botTokenRequestInput{
		RequesterID: userID,
		Username:    submissionString(submission.Submission, fieldUsername),
		DisplayName: submissionString(submission.Submission, fieldDisplayName),
		Description: submissionString(submission.Submission, fieldBotDescription),
	})
	if err != nil {
		writeJSON(w, model.SubmitDialogResponse{Error: err.Error()})
		return
	}

	p.API.SendEphemeralPost(userID, &model.Post{
		ChannelId: submission.ChannelId,
		Message:   message,
	})
	w.WriteHeader(http.StatusOK)
}

// handleBotTokenAction handles Approve/Deny on a bot-token request — the two-step
// engine. Approve records the clicker's step; when both steps are filled by two
// distinct users the bot + token are created and the token is DM'd to the
// requester. Deny (by any eligible approver) rejects the whole request.
func (p *Plugin) handleBotTokenAction(w http.ResponseWriter, r *http.Request, approve bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var request model.PostActionIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	actingUser, appErr := p.API.GetUser(userID)
	if appErr != nil {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not verify your identity to approve/deny."})
		return
	}

	requestID, _ := request.Context[actionContextRequestID].(string)
	key := kvBotTokenRequestPrefix + requestID
	req, rawReq, err := p.loadBotTokenRequest(requestID)
	if err != nil {
		p.API.LogError("failed to load bot-token request", "error", err.Error())
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not load that request."})
		return
	}
	if req == nil {
		writeJSON(w, model.PostActionIntegrationResponse{
			Update: p.resolvedPost(request.PostId, "This request has already been handled."),
		})
		return
	}

	// Eligibility for each pool (needed for both approve and deny paths).
	canSec, errSec := p.canApproveStep(actingUser, stepSecurity)
	canSys, errSys := p.canApproveStep(actingUser, stepSystem)
	if errSec != nil || errSys != nil {
		p.API.LogError("failed to check bot-token approver eligibility", "user_id", userID)
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not check your approver permissions. Please try again."})
		return
	}

	config := p.getConfiguration()
	requester, _ := p.API.GetUser(req.RequesterID) // best-effort for notify + audit + card

	if !approve {
		if !canSec && !canSys {
			writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "You don't have permission to deny this request."})
			return
		}
		claimed, claimErr := p.API.KVCompareAndDelete(key, rawReq)
		if claimErr != nil {
			writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not process that request."})
			return
		}
		if !claimed {
			writeJSON(w, model.PostActionIntegrationResponse{
				Update: p.resolvedPost(request.PostId, "This request has already been handled."),
			})
			return
		}
		outcome := fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequester(req.RequesterID, fmt.Sprintf("Your bot token request for **@%s** was denied.", req.Username))
		p.logAudit(config, fmt.Sprintf("BOT TOKEN DENIED: @%s denied a bot token request for @%s from @%s",
			actingUser.Username, req.Username, requesterUsername(requester, req.RequesterID)))
		writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
		return
	}

	step, fill, reason := planApproval(req.twoStepState, canSec, canSys, userID)
	if !fill {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: reason})
		return
	}

	newState := req.twoStepState.withApproval(step, userID, model.GetMillis())

	if newState.complete() {
		// Claim (remove) before creating so two concurrent completing clicks
		// can't both create a bot.
		claimed, claimErr := p.API.KVCompareAndDelete(key, rawReq)
		if claimErr != nil {
			writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not process that request."})
			return
		}
		if !claimed {
			writeJSON(w, model.PostActionIntegrationResponse{
				Update: p.resolvedPost(request.PostId, "This request has already been handled."),
			})
			return
		}
		token, bot, createErr := p.createBotTokenForRequest(req)
		if createErr != nil {
			p.API.LogError("failed to create bot token on approval", "error", createErr.Error())
			// Restore the ORIGINAL (pre-final-approval) request so this final
			// approval can be retried after the cause is fixed.
			if restoreErr := p.API.KVSet(key, rawReq); restoreErr != nil {
				p.API.LogError("failed to restore bot-token request after create failure", "error", restoreErr.Error())
				writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: fmt.Sprintf("Could not create the bot/token (%s), and the pending request could not be saved — ask the requester to submit it again.", createErr.Error())})
				return
			}
			notice := fmt.Sprintf("Couldn't create the bot/token: %s. The request is still pending — click **Approve** again to retry, or **Deny** it. (The username may be taken, or bot/token creation may be disabled server-side.)", createErr.Error())
			cardRequester := requester
			if cardRequester == nil {
				cardRequester = &model.User{Username: requesterUsername(nil, req.RequesterID)}
			}
			writeJSON(w, model.PostActionIntegrationResponse{
				EphemeralText: notice,
				Update: p.repaintedApprovalPost(
					request.PostId,
					"@channel — a bot token request needs review (a previous approval attempt failed).",
					p.botTokenApprovalAttachmentWithNotice(req, cardRequester, notice),
				),
			})
			return
		}
		if deliverErr := p.deliverBotToken(req.RequesterID, bot.Username, token); deliverErr != nil {
			// The token is shown only once and the request is already resolved,
			// so an undelivered token is a dangling privileged credential. Delete
			// the bot to avoid leaving it live, and report the failure instead of
			// a false success.
			p.API.LogError("failed to deliver bot token to requester", "user_id", req.RequesterID, "error", deliverErr.Error())
			removed := true
			if delErr := p.API.PermanentDeleteBot(bot.UserId); delErr != nil {
				p.API.LogError("failed to delete bot after token delivery failure", "bot_user_id", bot.UserId, "error", delErr.Error())
				removed = false
			}
			var outcome string
			if removed {
				p.logAudit(config, fmt.Sprintf("BOT TOKEN DELIVERY FAILED: bot @%s was created for @%s but the token could not be delivered; the bot was removed",
					bot.Username, requesterUsername(requester, req.RequesterID)))
				outcome = fmt.Sprintf("⚠️ Approved, but the token could not be delivered to the requester, so bot @%s was removed. Ask them to submit the request again.", bot.Username)
			} else {
				p.logAudit(config, fmt.Sprintf("BOT TOKEN DELIVERY FAILED: bot @%s was created for @%s but the token could not be delivered AND the bot could not be removed — manual cleanup needed",
					bot.Username, requesterUsername(requester, req.RequesterID)))
				outcome = fmt.Sprintf("⚠️ Approved, but the token could not be delivered to the requester and bot @%s could not be removed automatically — an admin should delete it. Ask the requester to submit again.", bot.Username)
			}
			writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
			return
		}
		outcome := fmt.Sprintf("✅ Fully approved (security + system). Bot @%s created; the token was sent privately to the requester.", bot.Username)
		p.logAudit(config, fmt.Sprintf("BOT TOKEN APPROVED: @%s gave final approval; bot @%s created for @%s",
			actingUser.Username, bot.Username, requesterUsername(requester, req.RequesterID)))
		writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
		return
	}

	// Partial approval: persist the new state (atomic against concurrent clicks)
	// and repaint the card to show progress + the remaining step.
	newReq := *req
	newReq.twoStepState = newState
	newBytes, marshalErr := json.Marshal(&newReq)
	if marshalErr != nil {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not process that request."})
		return
	}
	set, setErr := p.API.KVCompareAndSet(key, rawReq, newBytes)
	if setErr != nil || !set {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "This request was just updated by someone else. Please try again."})
		return
	}
	p.notifyRequester(req.RequesterID, fmt.Sprintf("Your bot token request for **@%s** received the %s approval; awaiting the other approval.", req.Username, step))
	cardRequester := requester
	if cardRequester == nil {
		cardRequester = &model.User{Username: requesterUsername(nil, req.RequesterID)}
	}
	writeJSON(w, model.PostActionIntegrationResponse{
		Update: p.repaintedApprovalPost(
			request.PostId,
			"@channel — a bot token request needs review (requires a security approval and a system approval).",
			p.botTokenApprovalAttachment(&newReq, cardRequester),
		),
	})
}

// repaintedApprovalPost rebuilds an approval post KEEPING its action buttons,
// used when a two-step request advances but isn't finished. (resolvedPost, by
// contrast, strips the buttons for a finished request.)
func (p *Plugin) repaintedApprovalPost(postID, message string, attachment *model.MessageAttachment) *model.Post {
	post, appErr := p.API.GetPost(postID)
	if appErr != nil {
		return &model.Post{Message: message}
	}
	post.Message = message
	post.DelProp("attachments")
	model.ParseMessageAttachment(post, []*model.MessageAttachment{attachment})
	return post
}

// adminRequestBody is the JSON payload sent by the "Request Channel Admin"
// modal.
type adminRequestBody struct {
	ChannelID string   `json:"channel_id"`
	Nominees  []string `json:"nominees"` // usernames to promote to Channel Admin
}

func (p *Plugin) handleRequestAdmin(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	if !p.getConfiguration().RequestEnabled(requestTypeChannelAdmin) {
		writeJSON(w, map[string]string{"error": channelAdminRequestsDisabledMsg})
		return
	}

	var body adminRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	nomineeIDs, err := p.resolveUsernameList(body.Nominees)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	message, err := p.submitAdminRequest(userID, body.ChannelID, nomineeIDs)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, map[string]string{"message": message})
}

func (p *Plugin) handleAction(w http.ResponseWriter, r *http.Request, approve bool) {
	// Identity comes from the authenticated header, NOT request.UserId in
	// the body — trusting the body would let anyone who can read a pending
	// request_id forge a System Admin user_id and approve/deny requests.
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var request model.PostActionIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	// Approvers: System Admins always. Team Admins of the approval team
	// too, if the admin has opted in via AllowTeamAdminApprovers.
	actingUser, appErr := p.API.GetUser(userID)
	if appErr != nil {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not verify your identity to approve/deny."})
		return
	}
	if !p.canApprove(actingUser) {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "You don't have permission to approve or deny channel requests. Contact a System Admin."})
		return
	}

	requestID, _ := request.Context[actionContextRequestID].(string)
	req, rawReq, err := p.loadRequest(requestID)
	if err != nil {
		p.API.LogError("failed to load channel request", "error", err.Error())
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not load that request."})
		return
	}
	if req == nil {
		writeJSON(w, model.PostActionIntegrationResponse{
			Update: p.resolvedPost(request.PostId, "This request has already been handled."),
		})
		return
	}

	// Atomically claim the request before acting on it. Two admins
	// clicking Approve (or one Approve + one Deny) at nearly the same
	// time both load a non-nil req; without an atomic claim both would
	// proceed, creating duplicate channels or approving-and-denying the
	// same request. Compare against the exact bytes we read so the claim
	// is robust regardless of how the request struct is marshaled.
	// Whoever wins owns the outcome; the loser is told it was already
	// handled.
	claimed, claimErr := p.API.KVCompareAndDelete(kvRequestPrefix+req.ID, rawReq)
	if claimErr != nil {
		p.API.LogError("failed to claim channel request", "error", claimErr.Error())
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not process that request."})
		return
	}
	if !claimed {
		writeJSON(w, model.PostActionIntegrationResponse{
			Update: p.resolvedPost(request.PostId, "This request has already been handled."),
		})
		return
	}

	config := p.getConfiguration()
	requester, _ := p.API.GetUser(req.RequesterID) // best-effort for welcome post + audit

	var outcome string
	if approve {
		channel, createErr := p.createChannelForRequest(req)
		if createErr != nil {
			p.API.LogError("failed to create channel on approval", "error", createErr.Error())
			// The claim already removed the request from the KV store.
			// Restore it so a transient creation failure doesn't silently
			// drop the pending request. If the restore ALSO fails the
			// request is genuinely lost, so tell the approver to have the
			// requester resubmit rather than implying a retry will work.
			if restoreErr := p.storeRequest(req); restoreErr != nil {
				p.API.LogError("failed to restore request after create failure", "error", restoreErr.Error())
				writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: fmt.Sprintf("Could not create the channel (%s), and the pending request could not be saved — ask the requester to submit it again.", createErr.Error())})
				return
			}
			writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: fmt.Sprintf("Could not create the channel: %s. The request is still pending. If this keeps failing, the channel name may already be taken — deny it and ask the requester to resubmit with a different name.", createErr.Error())})
			return
		}
		outcome = fmt.Sprintf("✅ Approved by @%s. Channel ~%s created.", actingUser.Username, channel.Name)
		if requester != nil {
			p.postWelcomeMessage(channel, req, requester, actingUser)
		}
		p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request for channel **%s** was approved. It's now available at ~%s.", req.DisplayName, channel.Name))
		p.logAudit(config, fmt.Sprintf("APPROVED: @%s approved channel request `%s` (%s) from @%s",
			actingUser.Username, req.DisplayName, channel.Name, requesterUsername(requester, req.RequesterID)))
	} else {
		outcome = fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request for channel **%s** was denied.", req.DisplayName))
		p.logAudit(config, fmt.Sprintf("DENIED: @%s denied channel request `%s` from @%s",
			actingUser.Username, req.DisplayName, requesterUsername(requester, req.RequesterID)))
	}

	// The KV key was already removed by the atomic claim above.
	writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
}

// handleAdminAction handles Approve/Deny on a channel-admin request. Mirrors
// handleAction but operates on adminRequest records and promotes nominees
// instead of creating a channel.
func (p *Plugin) handleAdminAction(w http.ResponseWriter, r *http.Request, approve bool) {
	// Same rule as handleAction: the acting user comes from the session, not
	// the client-supplied body.
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var request model.PostActionIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	actingUser, appErr := p.API.GetUser(userID)
	if appErr != nil {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not verify your identity to approve/deny."})
		return
	}

	requestID, _ := request.Context[actionContextRequestID].(string)
	req, rawReq, err := p.loadAdminRequest(requestID)
	if err != nil {
		p.API.LogError("failed to load channel-admin request", "error", err.Error())
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not load that request."})
		return
	}
	if req == nil {
		writeJSON(w, model.PostActionIntegrationResponse{
			Update: p.resolvedPost(request.PostId, "This request has already been handled."),
		})
		return
	}

	// A channel-admin request is scoped to a specific channel, so its own
	// Channel Admins may act on it (in addition to the global approvers). We
	// need the loaded request's ChannelID for that check, so it happens here
	// rather than up front.
	if !p.canApproveAdminRequest(actingUser, req.ChannelID) {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "You don't have permission to approve or deny this Channel Admin request. Contact a Channel Admin or System Admin."})
		return
	}

	config := p.getConfiguration()
	requester, _ := p.API.GetUser(req.RequesterID) // best-effort for notify + audit
	channel, channelErr := p.API.GetChannel(req.ChannelID)

	channelRef := "the channel"
	if channelErr == nil {
		channelRef = "~" + channel.Name
	}
	nominees := p.mentionList(req.NomineeIDs)

	// On approval we need a live channel to promote into; bail before claiming
	// the request so a deleted channel leaves the pending request intact.
	if approve && channelErr != nil {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not load the channel; it may have been deleted."})
		return
	}

	// Atomically claim the request before acting. Two reviewers clicking
	// Approve/Deny at nearly the same time both load a non-nil req; without an
	// atomic claim both would promote (or one approve + one deny) the same
	// request. Compare against the exact bytes read so the claim is robust
	// regardless of marshaling. Whoever wins owns the outcome; the loser is
	// told it was already handled. Mirrors handleAction.
	claimed, claimErr := p.API.KVCompareAndDelete(kvAdminRequestPrefix+req.ID, rawReq)
	if claimErr != nil {
		p.API.LogError("failed to claim channel-admin request", "error", claimErr.Error())
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not process that request."})
		return
	}
	if !claimed {
		writeJSON(w, model.PostActionIntegrationResponse{
			Update: p.resolvedPost(request.PostId, "This request has already been handled."),
		})
		return
	}

	var outcome string
	if approve {
		p.promoteChannelAdmins(req)
		p.postAdminPromotionMessage(channel, req, actingUser)
		outcome = fmt.Sprintf("✅ Approved by @%s. %s promoted to Channel Admin in %s.", actingUser.Username, nominees, channelRef)
		p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request to make %s Channel Admin in %s was approved.", nominees, channelRef))
		p.logAudit(config, fmt.Sprintf("CHANNEL ADMIN APPROVED: @%s promoted %s to Channel Admin in %s (requested by @%s)",
			actingUser.Username, nominees, channelRef, requesterUsername(requester, req.RequesterID)))
	} else {
		outcome = fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request to make %s Channel Admin in %s was denied.", nominees, channelRef))
		p.logAudit(config, fmt.Sprintf("CHANNEL ADMIN DENIED: @%s denied a Channel Admin request for %s in %s",
			actingUser.Username, nominees, channelRef))
	}

	// The KV key was already removed by the atomic claim above.
	writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
}

// resolvedPost returns an updated version of the approval post with the buttons removed and a
// status line appended.
func (p *Plugin) resolvedPost(postID, status string) *model.Post {
	post, appErr := p.API.GetPost(postID)
	if appErr != nil {
		return &model.Post{Message: status}
	}

	// Rebuild the attachment without actions, preserving the informational fields.
	attachments := post.Attachments()
	for _, attachment := range attachments {
		attachment.Actions = nil
		attachment.Footer = status
	}
	post.DelProp("attachments")
	model.ParseMessageAttachment(post, attachments)
	post.Message = status
	return post
}

// resolveUsernameList takes a list of "@alice"/"alice"-style usernames
// and resolves them to MM user IDs. Returns an error for the first
// name that doesn't resolve so the requester sees a clear "unknown
// user: X" instead of a silent partial add.
func (p *Plugin) resolveUsernameList(usernames []string) ([]string, error) {
	// Bound the list before the per-username lookups so a crafted request
	// can't fan out into thousands of synchronous GetUserByUsername calls.
	if len(usernames) > maxMembersPerList {
		return nil, errors.Errorf("too many users: at most %d per list", maxMembersPerList)
	}
	out := make([]string, 0, len(usernames))
	for _, username := range usernames {
		username = strings.TrimPrefix(strings.TrimSpace(username), "@")
		if username == "" {
			continue
		}
		user, appErr := p.API.GetUserByUsername(username)
		if appErr != nil {
			return nil, errors.Errorf("unknown user: %s", username)
		}
		out = append(out, user.Id)
	}
	return out, nil
}

// canApprove reports whether the acting user is allowed to approve or
// deny a channel request. Cascading policy:
//
//	System Admin                                       -> yes (always)
//	Team Admin  of the approval team + opt-in flag on  -> yes
//	everyone else                                      -> no
//
// Channel admins are NOT approvers of channel-CREATION requests by
// design: the channel-admin role only exists on channels that already
// exist, whereas channel creation is about NEW channels. For
// channel-ADMIN requests (promotions on an existing channel) that
// reasoning is reversed — see canApproveAdminRequest.
func (p *Plugin) canApprove(user *model.User) bool {
	if user == nil {
		return false
	}
	if user.IsSystemAdmin() {
		return true
	}
	config := p.getConfiguration()
	if config.AllowTeamAdminApprovers && p.isTeamAdmin(user.Id, config.ApprovalTeam) {
		return true
	}
	return false
}

// canApproveAdminRequest reports whether the acting user may approve or deny a
// Channel Admin request for the given channel. Beyond the global approvers
// (System Admins, opted-in approval-team admins via canApprove), the Team
// Admins of the TARGET channel's team may act on it — postAdminApprovalRequest
// mentions exactly this set (System Admins + the channel's Team Admins) when it
// posts to the approval channel.
func (p *Plugin) canApproveAdminRequest(user *model.User, channelID string) bool {
	if p.canApprove(user) {
		return true
	}
	if user == nil || channelID == "" {
		return false
	}
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil || channel == nil {
		return false
	}
	return p.isTeamAdminOfTeamID(user.Id, channel.TeamId)
}

// isTeamAdminOfTeamID reports whether the user is a Team Admin of the team
// identified by teamID. Companion to isTeamAdmin, which resolves a team by its
// name/slug; this one takes the ID directly (e.g. a channel's TeamId).
func (p *Plugin) isTeamAdminOfTeamID(userID, teamID string) bool {
	if teamID == "" {
		return false
	}
	member, appErr := p.API.GetTeamMember(teamID, userID)
	if appErr != nil || member == nil {
		return false
	}
	if slices.Contains(strings.Fields(member.Roles), model.TeamAdminRoleId) {
		return true
	}
	return member.SchemeAdmin
}

// isTeamAdmin reports whether the user is a Team Admin of the team
// identified by teamSlug. Handles both classic roles-string admin and
// scheme-based admin (permissions v2).
func (p *Plugin) isTeamAdmin(userID, teamSlug string) bool {
	team, appErr := p.API.GetTeamByName(teamSlug)
	if appErr != nil || team == nil {
		return false
	}
	member, appErr := p.API.GetTeamMember(team.Id, userID)
	if appErr != nil || member == nil {
		return false
	}
	if slices.Contains(strings.Fields(member.Roles), model.TeamAdminRoleId) {
		return true
	}
	return member.SchemeAdmin
}

// requesterUsername returns "@username" for the audit log when we have
// the user object; falls back to the raw ID otherwise.
func requesterUsername(user *model.User, userID string) string {
	if user != nil {
		return "@" + user.Username
	}
	return "user " + userID
}

func submissionString(submission map[string]any, key string) string {
	if value, ok := submission[key].(string); ok {
		return value
	}
	return ""
}

func splitIDs(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	ids := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	return ids
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// dialogErrorResponse maps a submit error to an interactive-dialog response.
// A field-scoped *fieldError is surfaced inline under the offending dialog
// element (so it appears right below that field, not as detached red text at
// the bottom); anything else falls back to a dialog-level message.
func dialogErrorResponse(err error) model.SubmitDialogResponse {
	if fe, ok := err.(*fieldError); ok && fe.field != "" {
		return model.SubmitDialogResponse{Errors: map[string]string{fe.field: fe.msg}}
	}
	return model.SubmitDialogResponse{Error: err.Error()}
}
