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
	routePrefixes         = "/api/v1/prefixes"
	routeTeams            = "/api/v1/teams"             // list teams for the approval-channel picker
	routeChannels         = "/api/v1/channels"          // list channels in a team, ?team_id=...
	routeUserAutocomplete = "/api/v1/user_autocomplete" // ?q=... for the request-modal member picker

	// fieldPrefix is the dialog element name for the domain-prefix
	// dropdown. Kept alongside the other field* constants in request.go.
	fieldPrefix = "prefix"
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
	case routePrefixes:
		p.handlePrefixes(w, r)
	case routeTeams:
		p.handleListTeams(w, r)
	case routeChannels:
		p.handleListChannels(w, r)
	case routeUserAutocomplete:
		p.handleUserAutocomplete(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleListTeams returns the list of teams the current user can see.
// Used by the ApprovalChannelPicker component to populate the team
// dropdown. Any logged-in user gets the list (only sysadmins reach the
// admin console anyway; MM enforces that at the UI level).
func (p *Plugin) handleListTeams(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		http.Error(w, "not authorized", http.StatusUnauthorized)
		return
	}
	teams, appErr := p.API.GetTeamsForUser(userID)
	if appErr != nil {
		p.API.LogWarn("list-teams failed", "user_id", userID, "error", appErr.Error())
		writeJSON(w, []any{})
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
	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		http.Error(w, "not authorized", http.StatusUnauthorized)
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
		writeJSON(w, []any{})
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
	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		http.Error(w, "not authorized", http.StatusUnauthorized)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, []any{})
		return
	}
	teamID := strings.TrimSpace(r.URL.Query().Get("team_id"))

	// TeamId in UserSearch scopes results to members of that team.
	// Empty TeamId means "any team the caller can see".
	// Limit MUST be non-zero — MM's UserSearch treats Limit=0 as
	// "return no results" (not "unlimited"), which was the reason
	// this endpoint used to silently return []. Set Limit to a
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
		writeJSON(w, []any{})
		return
	}

	type userDTO struct {
		ID        string `json:"id"`
		Username  string `json:"username"`
		Nickname  string `json:"nickname"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	}
	max := min(len(users), 20)
	out := make([]userDTO, 0, max)
	for i, u := range users {
		if i >= max {
			break
		}
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
	if r.Header.Get("Mattermost-User-Id") == "" {
		http.Error(w, "not authorized", http.StatusUnauthorized)
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

// openRequestDialog opens the interactive channel request dialog for the slash command entry point.
//
// When the admin has configured a prefix list, this injects a "Domain
// prefix" dropdown as the first element and reframes the URL field as
// "URL suffix" so the requester knows they're only providing the part
// after the prefix. When no prefix list is set, the dialog matches
// the legacy layout (Channel name / URL name / Purpose / Visibility /
// Members).
func (p *Plugin) openRequestDialog(triggerID, teamID string) error {
	config := p.getConfiguration()

	// Build the elements slice conditionally so the dialog shape adapts
	// to whichever naming-enforcement mode the admin picked.
	var elements []model.DialogElement

	// URL field HelpText + naming field labels differ between modes so
	// requesters get accurate guidance in the dialog itself.
	urlFieldName := "URL name"
	urlFieldHelp := "Lowercase letters, numbers, and hyphens. Leave blank to generate from the channel name."

	if config.UsesPrefixList() {
		prefixOptions := make([]*model.PostActionOptions, 0, len(config.prefixes))
		for _, p := range config.prefixes {
			label := p.Prefix
			if p.Description != "" {
				label = fmt.Sprintf("%s  (%s)", p.Prefix, p.Description)
			}
			prefixOptions = append(prefixOptions, &model.PostActionOptions{Text: label, Value: p.Prefix})
		}
		elements = append(elements, model.DialogElement{
			DisplayName: "Domain prefix",
			Name:        fieldPrefix,
			Type:        "select",
			Options:     prefixOptions,
			HelpText:    "Pick the category for this channel. The final URL is <prefix><suffix>.",
		})
		urlFieldName = "URL suffix"
		urlFieldHelp = "The part AFTER the prefix. Lowercase letters, numbers, and hyphens. Leave blank to generate from the channel name."
	}

	elements = append(elements,
		model.DialogElement{
			DisplayName: "Channel name",
			Name:        fieldDisplayName,
			Type:        "text",
			Placeholder: "e.g. Marketing Team",
			MaxLength:   64,
		},
		model.DialogElement{
			DisplayName: urlFieldName,
			Name:        fieldName,
			Type:        "text",
			Optional:    true,
			HelpText:    urlFieldHelp,
			MaxLength:   64,
		},
		model.DialogElement{
			DisplayName: "Purpose",
			Name:        fieldPurpose,
			Type:        "textarea",
			Optional:    true,
			MaxLength:   250,
		},
		model.DialogElement{
			DisplayName: "Visibility",
			Name:        fieldType,
			Type:        "radio",
			Default:     channelTypeOpen,
			Options: []*model.PostActionOptions{
				{Text: "Public", Value: channelTypeOpen},
				{Text: "Private", Value: channelTypePrivate},
			},
		},
		model.DialogElement{
			DisplayName: "Members to add",
			Name:        fieldMembers,
			Type:        "select",
			DataSource:  "users",
			MultiSelect: true,
			Optional:    true,
			HelpText:    "These users are added to the channel once it's approved.",
		},
		model.DialogElement{
			DisplayName: "Channel admins",
			Name:        fieldAdmins,
			Type:        "select",
			DataSource:  "users",
			MultiSelect: true,
			Optional:    true,
			HelpText:    "These users are made channel admins once the channel is approved.",
		},
	)

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
	var submission model.SubmitDialogRequest
	if err := json.NewDecoder(r.Body).Decode(&submission); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if submission.Cancelled {
		w.WriteHeader(http.StatusOK)
		return
	}

	teamID := submission.State
	if teamID == "" {
		teamID = submission.TeamId
	}

	in := requestInput{
		RequesterID: submission.UserId,
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
		writeJSON(w, model.SubmitDialogResponse{Error: err.Error()})
		return
	}

	// Acknowledge success with an ephemeral message in the channel the dialog was opened from.
	p.API.SendEphemeralPost(submission.UserId, &model.Post{
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
	// (e.g., "team-"). Empty when the plugin is in legacy mode.
	Prefix       string   `json:"prefix"`
	Purpose      string   `json:"purpose"`
	ChannelType  string   `json:"channel_type"`
	Members      []string `json:"members"`       // usernames — regular members
	AdminMembers []string `json:"admin_members"` // usernames — Channel Admins
}

func (p *Plugin) handleWebappCreate(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		http.Error(w, "not authorized", http.StatusUnauthorized)
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

func (p *Plugin) handleAction(w http.ResponseWriter, r *http.Request, approve bool) {
	var request model.PostActionIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	// Approvers: System Admins always. Team Admins of the approval team
	// too, if the admin has opted in via AllowTeamAdminApprovers.
	actingUser, appErr := p.API.GetUser(request.UserId)
	if appErr != nil {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not verify your identity to approve/deny."})
		return
	}
	if !p.canApprove(actingUser) {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "You don't have permission to approve or deny channel requests. Contact a System Admin."})
		return
	}

	requestID, _ := request.Context[actionContextRequestID].(string)
	req, err := p.loadRequest(requestID)
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

	config := p.getConfiguration()
	requester, _ := p.API.GetUser(req.RequesterID) // best-effort for welcome post + audit

	var outcome string
	if approve {
		channel, createErr := p.createChannelForRequest(req)
		if createErr != nil {
			p.API.LogError("failed to create channel on approval", "error", createErr.Error())
			writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: fmt.Sprintf("Could not create the channel: %s", createErr.Error())})
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

	if appErr := p.API.KVDelete(kvRequestPrefix + req.ID); appErr != nil {
		p.API.LogWarn("failed to delete handled request", "error", appErr.Error())
	}

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
// Channel admins are NOT approvers by design: the channel-admin role
// only exists on channels that already exist, whereas this plugin is
// specifically for creating NEW channels. There's no meaningful
// "channel admin" identity at approval time.
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
