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
	routePrefixes         = "/api/v1/prefixes"
	routeTeams            = "/api/v1/teams"             // list teams for the approval-channel picker
	routeChannels         = "/api/v1/channels"          // list channels in a team, ?team_id=...
	routeUserAutocomplete = "/api/v1/user_autocomplete" // ?q=... for the request-modal member picker

	routeChannelAdminDialog = "/api/v1/channel_admin_dialog"
	routeTeamAdminDialog    = "/api/v1/team_admin_dialog"
	routeTeamDialog         = "/api/v1/team_dialog"
	routeApproveTeamAdmin   = "/api/v1/approve_team_admin"
	routeDenyTeamAdmin      = "/api/v1/deny_team_admin"
	routeApproveTeam        = "/api/v1/approve_team"
	routeDenyTeam           = "/api/v1/deny_team"
	routeBotDialog          = "/api/v1/bot_dialog"
	routeApproveBot         = "/api/v1/approve_bot"
	routeDenyBot            = "/api/v1/deny_bot"

	routeIncomingWebhookDialog = "/api/v1/incoming_webhook_dialog"
	routeOutgoingWebhookDialog = "/api/v1/outgoing_webhook_dialog"
	routeApproveWebhook        = "/api/v1/approve_webhook"
	routeDenyWebhook           = "/api/v1/deny_webhook"

	// Webapp-driven REST endpoints — no triggerID required. Used by React
	// modals that collect form data and POST directly to the plugin.
	routeSubmitTeamCreationWebapp       = "/api/v1/submit_team_creation"
	routeSubmitTeamAdminWebapp          = "/api/v1/submit_team_admin_request"
	routeSubmitBotWebapp                = "/api/v1/submit_bot_request"
	routeSubmitIncomingWebhookWebapp    = "/api/v1/submit_incoming_webhook_request"
	routeSubmitOutgoingWebhookWebapp    = "/api/v1/submit_outgoing_webhook_request"
	routeSidebarCategories              = "/api/v1/sidebar_categories" // ?team_id=... — list caller's sidebar categories

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
	case routeRequestAdmin:
		p.handleRequestAdmin(w, r)
	case routeApproveAdmin:
		p.handleAdminAction(w, r, true)
	case routeDenyAdmin:
		p.handleAdminAction(w, r, false)
	case routePrefixes:
		p.handlePrefixes(w, r)
	case routeTeams:
		p.handleListTeams(w, r)
	case routeChannels:
		p.handleListChannels(w, r)
	case routeUserAutocomplete:
		p.handleUserAutocomplete(w, r)
	case routeChannelAdminDialog:
		p.handleChannelAdminDialogSubmit(w, r)
	case routeTeamAdminDialog:
		p.handleTeamAdminDialogSubmit(w, r)
	case routeTeamDialog:
		p.handleTeamCreationDialogSubmit(w, r)
	case routeApproveTeamAdmin:
		p.handleTeamAdminAction(w, r, true)
	case routeDenyTeamAdmin:
		p.handleTeamAdminAction(w, r, false)
	case routeApproveTeam:
		p.handleTeamAction(w, r, true)
	case routeDenyTeam:
		p.handleTeamAction(w, r, false)
	case routeBotDialog:
		p.handleBotDialogSubmit(w, r)
	case routeApproveBot:
		p.handleBotAction(w, r, true)
	case routeDenyBot:
		p.handleBotAction(w, r, false)
	case routeIncomingWebhookDialog:
		p.handleIncomingWebhookDialogSubmit(w, r)
	case routeOutgoingWebhookDialog:
		p.handleOutgoingWebhookDialogSubmit(w, r)
	case routeApproveWebhook:
		p.handleWebhookAction(w, r, true)
	case routeDenyWebhook:
		p.handleWebhookAction(w, r, false)
	case routeSubmitTeamCreationWebapp:
		p.handleSubmitTeamCreationWebapp(w, r)
	case routeSubmitTeamAdminWebapp:
		p.handleSubmitTeamAdminWebapp(w, r)
	case routeSubmitBotWebapp:
		p.handleSubmitBotWebapp(w, r)
	case routeSubmitIncomingWebhookWebapp:
		p.handleSubmitIncomingWebhookWebapp(w, r)
	case routeSubmitOutgoingWebhookWebapp:
		p.handleSubmitOutgoingWebhookWebapp(w, r)
	case routeSidebarCategories:
		p.handleListSidebarCategories(w, r)
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
			DisplayName: "Sidebar category",
			Name:        fieldCategory,
			Type:        "text",
			Optional:    true,
			HelpText:    "Existing sidebar category to place the channel in on approval (case-insensitive match). Leave blank to skip.",
			MaxLength:   64,
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
		CategoryName:   submissionString(submission.Submission, fieldCategory),
	}

	message, err := p.submitRequest(in)
	if err != nil {
		writeJSON(w, model.SubmitDialogResponse{Error: err.Error()})
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
	// Category is the optional sidebar category name to place the new channel
	// in on approval. Matched case-insensitively against existing categories.
	Category string `json:"category"`
}

func (p *Plugin) handleWebappCreate(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
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
		CategoryName:   body.Category,
	})
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, map[string]string{"message": message})
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

	// Refresh the thread anchors before claiming. If the initial storeTicketLookups
	// call failed silently at request submission time, the KV entries for the relay
	// are missing and post-approval replies won't be forwarded. Re-storing here
	// guarantees the anchors exist regardless of any prior failure.
	p.storeTicketLookups(threadAnchor{
		ApprovalPostID:    req.ApprovalPostID,
		ApprovalChannelID: req.ApprovalChannelID,
		DMRootPostID:      req.DMRootPostID,
		DMChannelID:       req.DMChannelID,
	})

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
		p.addChannelToSidebarCategory(channel.Id, channel.TeamId, req)
		outcome = fmt.Sprintf("✅ Approved by @%s. Channel ~%s created.", actingUser.Username, channel.Name)
		if requester != nil {
			p.postWelcomeMessage(channel, req, requester, actingUser)
		}
		p.notifyRequesterInThreadCard(req.RequesterID, req.DMRootPostID, req.DMChannelID, &model.MessageAttachment{
			Color: colorApproved,
			Title: "Channel Request Approved",
			Text:  fmt.Sprintf("Approved by @%s. Your request for channel **%s** is now available at ~%s.", actingUser.Username, req.DisplayName, channel.Name),
		})
		p.postOutcomeThreadReply(req.DMChannelID, req.DMRootPostID, true, actingUser.Username)
		p.logAudit(config, fmt.Sprintf("APPROVED: @%s approved channel request `%s` (%s) from @%s",
			actingUser.Username, req.DisplayName, channel.Name, requesterUsername(requester, req.RequesterID)))
	} else {
		outcome = fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequesterInThreadCard(req.RequesterID, req.DMRootPostID, req.DMChannelID, &model.MessageAttachment{
			Color: colorDenied,
			Title: "Channel Request Denied",
			Text:  fmt.Sprintf("Denied by @%s. Your request for channel **%s** was not approved. Reply to this thread if you'd like more information.", actingUser.Username, req.DisplayName),
		})
		p.postOutcomeThreadReply(req.DMChannelID, req.DMRootPostID, false, actingUser.Username)
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

	// Refresh thread anchors before claiming — same defensive pattern as handleAction.
	p.storeTicketLookups(threadAnchor{
		ApprovalPostID:    req.ApprovalPostID,
		ApprovalChannelID: req.ApprovalChannelID,
		DMRootPostID:      req.DMRootPostID,
		DMChannelID:       req.DMChannelID,
	})

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
		p.notifyRequesterInThreadCard(req.RequesterID, req.DMRootPostID, req.DMChannelID, &model.MessageAttachment{
			Color: colorApproved,
			Title: "Channel Admin Request Approved",
			Text:  fmt.Sprintf("Approved by @%s. %s is now Channel Admin in %s.", actingUser.Username, nominees, channelRef),
		})
		p.postOutcomeThreadReply(req.DMChannelID, req.DMRootPostID, true, actingUser.Username)
		p.logAudit(config, fmt.Sprintf("CHANNEL ADMIN APPROVED: @%s promoted %s to Channel Admin in %s (requested by @%s)",
			actingUser.Username, nominees, channelRef, requesterUsername(requester, req.RequesterID)))
	} else {
		outcome = fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequesterInThreadCard(req.RequesterID, req.DMRootPostID, req.DMChannelID, &model.MessageAttachment{
			Color: colorDenied,
			Title: "Channel Admin Request Denied",
			Text:  fmt.Sprintf("Denied by @%s. Your request to make %s Channel Admin in %s was not approved. Reply to this thread if you'd like more information.", actingUser.Username, nominees, channelRef),
		})
		p.postOutcomeThreadReply(req.DMChannelID, req.DMRootPostID, false, actingUser.Username)
		p.logAudit(config, fmt.Sprintf("CHANNEL ADMIN DENIED: @%s denied a Channel Admin request for %s in %s",
			actingUser.Username, nominees, channelRef))
	}

	// The KV key was already removed by the atomic claim above.
	writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
}

func (p *Plugin) handleChannelAdminDialogSubmit(w http.ResponseWriter, r *http.Request) {
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

	channelID := submissionString(submission.Submission, fieldChannelID)
	nomineeIDs := splitIDs(submissionString(submission.Submission, fieldNominees))

	message, err := p.submitAdminRequest(userID, channelID, nomineeIDs)
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

	teamID := submission.State
	if teamID == "" {
		teamID = submission.TeamId
	}
	nomineeIDs := splitIDs(submissionString(submission.Submission, fieldMembers))

	message, err := p.submitTeamAdminRequest(userID, teamID, nomineeIDs)
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

func (p *Plugin) handleTeamCreationDialogSubmit(w http.ResponseWriter, r *http.Request) {
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

	requestTeamAdmin := false
	if v, ok := submission.Submission[fieldRequestTeamAdmin].(bool); ok {
		requestTeamAdmin = v
	}

	message, err := p.submitTeamCreationRequest(teamCreationInput{
		RequesterID:      userID,
		DisplayName:      submissionString(submission.Submission, fieldDisplayName),
		Name:             submissionString(submission.Submission, fieldName),
		Description:      submissionString(submission.Submission, fieldPurpose),
		Type:             submissionString(submission.Submission, fieldType),
		RequestTeamAdmin: requestTeamAdmin,
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

// handleTeamAdminAction handles Approve/Deny on a team admin promotion request.
// Only System Admins may approve — team admin grants broad permissions.
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
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not verify your identity."})
		return
	}
	if !actingUser.IsSystemAdmin() {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Only System Admins can approve or deny Team Admin requests."})
		return
	}

	requestID, _ := request.Context[actionContextRequestID].(string)
	req, rawReq, err := p.loadTeamAdminRequest(requestID)
	if err != nil {
		p.API.LogError("failed to load team admin request", "error", err.Error())
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not load that request."})
		return
	}
	if req == nil {
		writeJSON(w, model.PostActionIntegrationResponse{
			Update: p.resolvedPost(request.PostId, "This request has already been handled."),
		})
		return
	}

	team, teamErr := p.API.GetTeam(req.TeamID)
	if approve && teamErr != nil {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not load the team; it may have been deleted."})
		return
	}

	p.storeTicketLookups(threadAnchor{
		ApprovalPostID:    req.ApprovalPostID,
		ApprovalChannelID: req.ApprovalChannelID,
		DMRootPostID:      req.DMRootPostID,
		DMChannelID:       req.DMChannelID,
	})

	claimed, claimErr := p.API.KVCompareAndDelete(kvTeamAdminRequestPrefix+req.ID, rawReq)
	if claimErr != nil {
		p.API.LogError("failed to claim team admin request", "error", claimErr.Error())
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
	requester, _ := p.API.GetUser(req.RequesterID)
	nominees := p.mentionList(req.NomineeIDs)
	teamRef := req.TeamID
	if teamErr == nil {
		teamRef = "**" + team.DisplayName + "**"
	}

	var outcome string
	if approve {
		p.promoteTeamAdmins(req)
		outcome = fmt.Sprintf("✅ Approved by @%s. %s promoted to Team Admin in %s.", actingUser.Username, nominees, teamRef)
		p.notifyRequesterInThreadCard(req.RequesterID, req.DMRootPostID, req.DMChannelID, &model.MessageAttachment{
			Color: colorApproved,
			Title: "Team Admin Request Approved",
			Text:  fmt.Sprintf("Approved by @%s. %s is now Team Admin in %s.", actingUser.Username, nominees, teamRef),
		})
		p.postOutcomeThreadReply(req.DMChannelID, req.DMRootPostID, true, actingUser.Username)
		p.logAudit(config, fmt.Sprintf("TEAM ADMIN APPROVED: @%s promoted %s to Team Admin in %s (requested by @%s)",
			actingUser.Username, nominees, teamRef, requesterUsername(requester, req.RequesterID)))
	} else {
		outcome = fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequesterInThreadCard(req.RequesterID, req.DMRootPostID, req.DMChannelID, &model.MessageAttachment{
			Color: colorDenied,
			Title: "Team Admin Request Denied",
			Text:  fmt.Sprintf("Denied by @%s. Your request to make %s Team Admin in %s was not approved. Reply to this thread if you'd like more information.", actingUser.Username, nominees, teamRef),
		})
		p.postOutcomeThreadReply(req.DMChannelID, req.DMRootPostID, false, actingUser.Username)
		p.logAudit(config, fmt.Sprintf("TEAM ADMIN DENIED: @%s denied a Team Admin request for %s in %s",
			actingUser.Username, nominees, teamRef))
	}
	writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
}

// handleTeamAction handles Approve/Deny on a team creation request.
// Only System Admins may approve.
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

	actingUser, appErr := p.API.GetUser(userID)
	if appErr != nil {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not verify your identity."})
		return
	}
	if !actingUser.IsSystemAdmin() {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Only System Admins can approve or deny team creation requests."})
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

	p.storeTicketLookups(threadAnchor{
		ApprovalPostID:    req.ApprovalPostID,
		ApprovalChannelID: req.ApprovalChannelID,
		DMRootPostID:      req.DMRootPostID,
		DMChannelID:       req.DMChannelID,
	})

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
	requester, _ := p.API.GetUser(req.RequesterID)

	var outcome string
	if approve {
		team, createErr := p.createTeamForRequest(req)
		if createErr != nil {
			p.API.LogError("failed to create team on approval", "error", createErr.Error())
			if restoreErr := p.storeTeamRequest(req); restoreErr != nil {
				p.API.LogError("failed to restore team request after create failure", "error", restoreErr.Error())
				writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: fmt.Sprintf("Could not create the team (%s), and the pending request could not be saved — ask the requester to resubmit.", createErr.Error())})
				return
			}
			writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: fmt.Sprintf("Could not create the team: %s. The request is still pending.", createErr.Error())})
			return
		}
		adminNote := ""
		if req.RequestTeamAdmin {
			adminNote = " Requester promoted to Team Admin."
		}
		outcome = fmt.Sprintf("✅ Approved by @%s. Team **%s** created.%s", actingUser.Username, team.DisplayName, adminNote)
		p.notifyRequesterInThreadCard(req.RequesterID, req.DMRootPostID, req.DMChannelID, &model.MessageAttachment{
			Color: colorApproved,
			Title: "Team Creation Request Approved",
			Text:  fmt.Sprintf("Approved by @%s. Team **%s** has been created.%s", actingUser.Username, req.DisplayName, adminNote),
		})
		p.postOutcomeThreadReply(req.DMChannelID, req.DMRootPostID, true, actingUser.Username)
		p.logAudit(config, fmt.Sprintf("TEAM CREATED: @%s approved team %s (%s) requested by @%s",
			actingUser.Username, req.DisplayName, team.Name, requesterUsername(requester, req.RequesterID)))
	} else {
		outcome = fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequesterInThreadCard(req.RequesterID, req.DMRootPostID, req.DMChannelID, &model.MessageAttachment{
			Color: colorDenied,
			Title: "Team Creation Request Denied",
			Text:  fmt.Sprintf("Denied by @%s. Your request to create team **%s** was not approved. Reply to this thread if you'd like more information.", actingUser.Username, req.DisplayName),
		})
		p.postOutcomeThreadReply(req.DMChannelID, req.DMRootPostID, false, actingUser.Username)
		p.logAudit(config, fmt.Sprintf("TEAM CREATION DENIED: @%s denied a team creation request for %s",
			actingUser.Username, req.DisplayName))
	}
	writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
}

func (p *Plugin) handleBotDialogSubmit(w http.ResponseWriter, r *http.Request) {
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

	message, err := p.submitBotRequest(userID, botRequestInput{
		Username:    submissionString(submission.Submission, fieldBotUsername),
		DisplayName: submissionString(submission.Submission, fieldBotDisplayName),
		Description: submissionString(submission.Submission, fieldBotDescription),
		OwnerUserID: submissionString(submission.Submission, fieldBotOwner),
	})
	if err != nil {
		writeJSON(w, model.SubmitDialogResponse{Error: err.Error()})
		return
	}
	p.API.SendEphemeralPost(userID, &model.Post{ChannelId: submission.ChannelId, Message: message})
	w.WriteHeader(http.StatusOK)
}

// handleBotAction handles Approve/Deny on a bot account request.
// Only System Admins may approve — bots carry integration credentials.
func (p *Plugin) handleBotAction(w http.ResponseWriter, r *http.Request, approve bool) {
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
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not verify your identity."})
		return
	}
	if !actingUser.IsSystemAdmin() {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Only System Admins can approve or deny bot account requests."})
		return
	}

	requestID, _ := request.Context[actionContextRequestID].(string)
	req, rawReq, err := p.loadBotRequest(requestID)
	if err != nil {
		p.API.LogError("failed to load bot request", "error", err.Error())
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not load that request."})
		return
	}
	if req == nil {
		writeJSON(w, model.PostActionIntegrationResponse{
			Update: p.resolvedPost(request.PostId, "This request has already been handled."),
		})
		return
	}

	p.storeTicketLookups(threadAnchor{
		ApprovalPostID:    req.ApprovalPostID,
		ApprovalChannelID: req.ApprovalChannelID,
		DMRootPostID:      req.DMRootPostID,
		DMChannelID:       req.DMChannelID,
	})

	claimed, claimErr := p.API.KVCompareAndDelete(kvBotRequestPrefix+req.ID, rawReq)
	if claimErr != nil {
		p.API.LogError("failed to claim bot request", "error", claimErr.Error())
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
	requester, _ := p.API.GetUser(req.RequesterID)

	var outcome string
	if approve {
		bot, createErr := p.createBotForRequest(req)
		if createErr != nil {
			p.API.LogError("failed to create bot on approval", "error", createErr.Error())
			// Restore so the request can be retried.
			if restoreErr := p.storeBotRequest(req); restoreErr != nil {
				p.API.LogError("failed to restore bot request after create failure", "error", restoreErr.Error())
				writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: fmt.Sprintf("Could not create the bot (%s), and the pending request could not be saved — ask the requester to resubmit.", createErr.Error())})
				return
			}
			writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: fmt.Sprintf("Could not create the bot: %s. The request is still pending.", createErr.Error())})
			return
		}

		// Mattermost relays interactive button clicks server-side, so the admin's
		// browser token is never in the Authorization header. Create a short-lived
		// session for the approving admin to authenticate the REST API calls below,
		// then revoke it immediately when this block exits. No credentials stored.
		authToken := ""
		siteURL := p.siteURL()
		if adminSession, sessErr := p.API.CreateSession(&model.Session{UserId: actingUser.Id}); sessErr != nil {
			p.API.LogWarn("failed to create admin session — token/webhook extras will be skipped", "error", sessErr.Error())
		} else {
			authToken = adminSession.Token
			defer p.API.RevokeSession(adminSession.Id)
		}

		var tokenValue string
		var incomingWebhookURL string
		var outgoingWebhookCreated bool
		var partialErrors []string

		if req.RequestToken && authToken != "" {
			tok, tokErr := p.createBotToken(authToken, bot.UserId, "API token for @"+req.Username)
			if tokErr != nil {
				partialErrors = append(partialErrors, fmt.Sprintf("token generation failed: %s", tokErr.Error()))
				p.API.LogError("failed to create bot token", "bot_user_id", bot.UserId, "error", tokErr.Error())
			} else {
				tokenValue = tok
			}
		}

		if req.IncomingWebhookChannelID != "" && authToken != "" && siteURL != "" {
			incomingWebhookURL, err = p.createIncomingWebhookForBot(authToken, siteURL, req)
			if err != nil {
				partialErrors = append(partialErrors, fmt.Sprintf("incoming webhook creation failed: %s", err.Error()))
				p.API.LogError("failed to create incoming webhook for bot", "error", err.Error())
			}
		}

		if req.OutgoingWebhookChannelID != "" && authToken != "" && siteURL != "" {
			if outErr := p.createOutgoingWebhookForBot(authToken, siteURL, req); outErr != nil {
				partialErrors = append(partialErrors, fmt.Sprintf("outgoing webhook creation failed: %s", outErr.Error()))
				p.API.LogError("failed to create outgoing webhook for bot", "error", outErr.Error())
			} else {
				outgoingWebhookCreated = true
			}
		}

		p.notifyRequesterInThreadCard(req.RequesterID, req.DMRootPostID, req.DMChannelID, &model.MessageAttachment{
			Color: colorApproved,
			Title: "Bot Account Request Approved",
			Text:  fmt.Sprintf("Approved by @%s. Bot **@%s** has been created.", actingUser.Username, bot.Username),
		})
		p.postOutcomeThreadReply(req.DMChannelID, req.DMRootPostID, true, actingUser.Username)
		// Token, webhook URLs, and partial errors go in a separate thread reply so
		// the token post can be scheduled for auto-deletion without affecting the card.
		p.sendBotExtrasReply(req, tokenValue, incomingWebhookURL, outgoingWebhookCreated, partialErrors)

		outcome = fmt.Sprintf("✅ Approved by @%s. Bot **@%s** created.", actingUser.Username, bot.Username)
		p.logAudit(config, fmt.Sprintf("BOT CREATED: @%s approved bot @%s requested by @%s",
			actingUser.Username, bot.Username, requesterUsername(requester, req.RequesterID)))
	} else {
		outcome = fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequesterInThreadCard(req.RequesterID, req.DMRootPostID, req.DMChannelID, &model.MessageAttachment{
			Color: colorDenied,
			Title: "Bot Account Request Denied",
			Text:  fmt.Sprintf("Denied by @%s. Your request for bot **@%s** was not approved. Reply to this thread if you'd like more information.", actingUser.Username, req.Username),
		})
		p.postOutcomeThreadReply(req.DMChannelID, req.DMRootPostID, false, actingUser.Username)
		p.logAudit(config, fmt.Sprintf("BOT DENIED: @%s denied a bot request for @%s",
			actingUser.Username, req.Username))
	}
	writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
}

func (p *Plugin) handleIncomingWebhookDialogSubmit(w http.ResponseWriter, r *http.Request) {
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

	message, err := p.submitWebhookRequest(
		userID,
		webhookTypeIncoming,
		submissionString(submission.Submission, fieldChannelID),
		submissionString(submission.Submission, fieldDisplayName),
		submissionString(submission.Submission, fieldPurpose),
		"",
	)
	if err != nil {
		writeJSON(w, model.SubmitDialogResponse{Error: err.Error()})
		return
	}
	p.API.SendEphemeralPost(userID, &model.Post{ChannelId: submission.ChannelId, Message: message})
	w.WriteHeader(http.StatusOK)
}

func (p *Plugin) handleOutgoingWebhookDialogSubmit(w http.ResponseWriter, r *http.Request) {
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

	message, err := p.submitWebhookRequest(
		userID,
		webhookTypeOutgoing,
		submissionString(submission.Submission, fieldChannelID),
		submissionString(submission.Submission, fieldDisplayName),
		submissionString(submission.Submission, fieldPurpose),
		submissionString(submission.Submission, fieldCallbackURL),
	)
	if err != nil {
		writeJSON(w, model.SubmitDialogResponse{Error: err.Error()})
		return
	}
	p.API.SendEphemeralPost(userID, &model.Post{ChannelId: submission.ChannelId, Message: message})
	w.WriteHeader(http.StatusOK)
}

// handleWebhookAction handles Approve/Deny on a webhook request.
// Only System Admins may approve — webhooks cross the network boundary.
// On approval, the admin's session token (from the Authorization header) is
// used directly to call the Mattermost REST API for webhook creation.
// No credentials are stored anywhere.
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
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Could not verify your identity."})
		return
	}
	if !actingUser.IsSystemAdmin() {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Only System Admins can approve or deny webhook requests."})
		return
	}

	requestID, _ := request.Context[actionContextRequestID].(string)
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

	p.storeTicketLookups(threadAnchor{
		ApprovalPostID:    req.ApprovalPostID,
		ApprovalChannelID: req.ApprovalChannelID,
		DMRootPostID:      req.DMRootPostID,
		DMChannelID:       req.DMChannelID,
	})

	claimed, claimErr := p.API.KVCompareAndDelete(kvWebhookRequestPrefix+req.ID, rawReq)
	if claimErr != nil {
		p.API.LogError("failed to claim webhook request", "error", claimErr.Error())
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
	requester, _ := p.API.GetUser(req.RequesterID)

	var outcome string
	if approve {
		dmMsg, createErr := p.createWebhookForRequest(req, actingUser.Id)
		if createErr != nil {
			p.API.LogError("failed to create webhook on approval", "error", createErr.Error())
			if restoreErr := p.storeWebhookRequest(req); restoreErr != nil {
				p.API.LogError("failed to restore webhook request after create failure", "error", restoreErr.Error())
				writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: fmt.Sprintf("Could not create the webhook (%s), and the pending request could not be saved — ask the requester to resubmit.", createErr.Error())})
				return
			}
			if req.ApprovalPostID != "" && req.ApprovalChannelID != "" {
				_, _ = p.API.CreatePost(&model.Post{
					UserId:    p.botUserID,
					ChannelId: req.ApprovalChannelID,
					RootId:    req.ApprovalPostID,
					Message:   fmt.Sprintf("⚠️ Webhook creation failed: %s. The request is still pending — click **Approve** to retry.", createErr.Error()),
				})
			}
			writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: fmt.Sprintf("Could not create the webhook: %s. The request is still pending.", createErr.Error())})
			return
		}
		outcome = fmt.Sprintf("✅ Approved by @%s. %s webhook **%s** created.", actingUser.Username, req.WebhookType, req.DisplayName)
		p.notifyRequesterInThreadCard(req.RequesterID, req.DMRootPostID, req.DMChannelID, &model.MessageAttachment{
			Color: colorApproved,
			Title: "Webhook Request Approved",
			Text:  fmt.Sprintf("Approved by @%s. %s", actingUser.Username, dmMsg),
		})
		p.postOutcomeThreadReply(req.DMChannelID, req.DMRootPostID, true, actingUser.Username)
		p.logAudit(config, fmt.Sprintf("WEBHOOK CREATED: @%s approved %s webhook %q requested by @%s",
			actingUser.Username, req.WebhookType, req.DisplayName, requesterUsername(requester, req.RequesterID)))
	} else {
		outcome = fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequesterInThreadCard(req.RequesterID, req.DMRootPostID, req.DMChannelID, &model.MessageAttachment{
			Color: colorDenied,
			Title: "Webhook Request Denied",
			Text:  fmt.Sprintf("Denied by @%s. Your request for a %s webhook **%s** was not approved. Reply to this thread if you'd like more information.", actingUser.Username, req.WebhookType, req.DisplayName),
		})
		p.postOutcomeThreadReply(req.DMChannelID, req.DMRootPostID, false, actingUser.Username)
		p.logAudit(config, fmt.Sprintf("WEBHOOK DENIED: @%s denied a %s webhook request for %s",
			actingUser.Username, req.WebhookType, req.DisplayName))
	}
	writeJSON(w, model.PostActionIntegrationResponse{Update: p.resolvedPost(request.PostId, outcome)})
}

// handleListSidebarCategories returns the caller's sidebar categories for a
// team. The webapp modal uses this to populate the "Sidebar category" dropdown.
// Query param: team_id (required).
func (p *Plugin) handleListSidebarCategories(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	teamID := r.URL.Query().Get("team_id")
	if teamID == "" {
		http.Error(w, "team_id required", http.StatusBadRequest)
		return
	}
	cats, appErr := p.API.GetChannelSidebarCategories(userID, teamID)
	if appErr != nil {
		p.API.LogWarn("sidebar-categories list failed", "user_id", userID, "team_id", teamID, "error", appErr.Error())
		http.Error(w, "failed to list categories", http.StatusInternalServerError)
		return
	}
	type catDTO struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Type        string `json:"type"`
	}
	out := make([]catDTO, 0, len(cats.Categories))
	for _, c := range cats.Categories {
		out = append(out, catDTO{ID: c.Id, DisplayName: c.DisplayName, Type: string(c.Type)})
	}
	writeJSON(w, out)
}

// resolvedPost returns an updated version of the approval post that collapses
// the attachment to a single outcome line. All previously-visible fields are
// moved into the attachment's Text block so reviewers can still expand and read
// the original details via Mattermost's native "Show more" link. The Approve/
// Deny buttons are removed and the attachment is recolored by outcome.
func (p *Plugin) resolvedPost(postID, status string) *model.Post {
	post, appErr := p.API.GetPost(postID)
	if appErr != nil {
		return &model.Post{Message: status}
	}

	attachments := post.Attachments()
	for _, attachment := range attachments {
		// Archive the original fields into the expandable Text block.
		var archived strings.Builder
		for _, f := range attachment.Fields {
			if archived.Len() > 0 {
				archived.WriteByte('\n')
			}
			archived.WriteString(fmt.Sprintf("**%s:** %s", f.Title, f.Value))
		}
		if attachment.Text != "" {
			if archived.Len() > 0 {
				archived.WriteByte('\n')
			}
			archived.WriteString(attachment.Text)
		}
		attachment.Fields = nil
		attachment.Actions = nil
		// Pad to exceed Mattermost's ~300px attachment-text collapse threshold
		// so resolved cards always render compact (title + "▸" expander) rather
		// than showing all archived fields expanded. Each \n is ~22px rendered.
		if archived.Len() > 0 {
			archived.WriteString(strings.Repeat("\n", 12))
		}
		attachment.Text = archived.String()
		attachment.Color = resolvedColor(status)
	}
	post.DelProp("attachments")
	model.ParseMessageAttachment(post, attachments)
	post.Message = status
	return post
}

// resolvedColor maps an outcome status prefix to a Mattermost attachment color.
func resolvedColor(status string) string {
	if strings.HasPrefix(status, "✅") {
		return "#28a745"
	}
	if strings.HasPrefix(status, "❌") {
		return "#d9534f"
	}
	return "#aaaaaa"
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

// handleSubmitTeamCreationWebapp handles team creation requests submitted from
// the plugin's React modal (no triggerID needed — identity comes from the
// Mattermost-User-Id header). The same submitTeamCreationRequest path is used
// for slash commands and interactive dialogs; this endpoint is the webapp-only
// entry point that skips the dialog machinery.
func (p *Plugin) handleSubmitTeamCreationWebapp(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var payload struct {
		DisplayName      string `json:"display_name"`
		Name             string `json:"name"`
		Type             string `json:"type"`
		Description      string `json:"description"`
		RequestTeamAdmin bool   `json:"request_team_admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	msg, err := p.submitTeamCreationRequest(teamCreationInput{
		RequesterID:      userID,
		DisplayName:      payload.DisplayName,
		Name:             payload.Name,
		Type:             payload.Type,
		Description:      payload.Description,
		RequestTeamAdmin: payload.RequestTeamAdmin,
	})
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"message": msg})
}

// handleSubmitTeamAdminWebapp handles a self-nomination Team Admin request
// from the plugin's React modal. The requester nominates themselves (nomineeIDs
// = [userID]) for the current team. All validation and routing logic is in
// submitTeamAdminRequest.
func (p *Plugin) handleSubmitTeamAdminWebapp(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var payload struct {
		TeamID string `json:"team_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(payload.TeamID) == "" {
		writeJSON(w, map[string]string{"error": "team_id is required"})
		return
	}

	msg, err := p.submitTeamAdminRequest(userID, payload.TeamID, []string{userID})
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"message": msg})
}

// handleSubmitBotWebapp handles bot account creation requests from the plugin's
// React modal. Fields match the slash-command dialog: username, display_name,
// description, and an optional bot_owner (defaults to the requester when
// empty). System Admins bypass approval and create the bot immediately.
func (p *Plugin) handleSubmitBotWebapp(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var payload struct {
		Username                   string `json:"username"`
		DisplayName                string `json:"display_name"`
		Description                string `json:"description"`
		BotOwner                   string `json:"bot_owner"`
		RequestToken               bool   `json:"request_token"`
		IncomingWebhookChannelID   string `json:"incoming_webhook_channel_id"`
		IncomingWebhookDisplayName string `json:"incoming_webhook_display_name"`
		OutgoingWebhookChannelID   string `json:"outgoing_webhook_channel_id"`
		OutgoingWebhookDisplayName string `json:"outgoing_webhook_display_name"`
		OutgoingWebhookCallbackURL string `json:"outgoing_webhook_callback_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	msg, err := p.submitBotRequest(userID, botRequestInput{
		Username:                   payload.Username,
		DisplayName:                payload.DisplayName,
		Description:                payload.Description,
		OwnerUserID:                payload.BotOwner,
		RequestToken:               payload.RequestToken,
		IncomingWebhookChannelID:   payload.IncomingWebhookChannelID,
		IncomingWebhookDisplayName: payload.IncomingWebhookDisplayName,
		OutgoingWebhookChannelID:   payload.OutgoingWebhookChannelID,
		OutgoingWebhookDisplayName: payload.OutgoingWebhookDisplayName,
		OutgoingWebhookCallbackURL: payload.OutgoingWebhookCallbackURL,
	})
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"message": msg})
}

// handleSubmitIncomingWebhookWebapp handles incoming webhook requests from the
// plugin's React modal. The channel_id field is the UUID of the target channel
// (pre-filled from the current channel in Redux state). Webhook requests never
// bypass approval — the approver's session token is required at creation time.
func (p *Plugin) handleSubmitIncomingWebhookWebapp(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var payload struct {
		ChannelID   string `json:"channel_id"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	msg, err := p.submitWebhookRequest(userID, "incoming", payload.ChannelID, payload.DisplayName, payload.Description, "")
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"message": msg})
}

// handleSubmitOutgoingWebhookWebapp handles outgoing webhook requests from the
// plugin's React modal. Adds a callback_url field on top of the incoming
// webhook payload. Like all webhook requests, there is no System Admin bypass.
func (p *Plugin) handleSubmitOutgoingWebhookWebapp(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var payload struct {
		ChannelID   string `json:"channel_id"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
		CallbackURL string `json:"callback_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	msg, err := p.submitWebhookRequest(userID, "outgoing", payload.ChannelID, payload.DisplayName, payload.Description, payload.CallbackURL)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"message": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
