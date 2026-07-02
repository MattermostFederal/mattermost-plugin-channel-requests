package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const (
	routeDialog   = "/api/v1/dialog"
	routeCreate   = "/api/v1/create"
	routeApprove  = "/api/v1/approve"
	routeDeny     = "/api/v1/deny"
	routePrefixes = "/api/v1/prefixes"

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
	default:
		http.NotFound(w, r)
	}
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
		Prefix       string `json:"prefix"`
		Description  string `json:"description"`
		SuffixRegex  string `json:"suffix_regex"`
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
		// opened with the prefix-list feature active; empty otherwise
		// (and safely ignored by resolveLegacyName).
		Prefix:      submissionString(submission.Submission, fieldPrefix),
		Purpose:     submissionString(submission.Submission, fieldPurpose),
		ChannelType: submissionString(submission.Submission, fieldType),
		MemberIDs:   splitIDs(submissionString(submission.Submission, fieldMembers)),
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
	TeamID      string   `json:"team_id"`
	DisplayName string   `json:"display_name"`
	Name        string   `json:"name"`
	// Prefix is the selected domain prefix from the modal's dropdown
	// (e.g., "team-"). Empty when the plugin is in legacy mode.
	Prefix      string   `json:"prefix"`
	Purpose     string   `json:"purpose"`
	ChannelType string   `json:"channel_type"`
	Members     []string `json:"members"` // usernames
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

	memberIDs := make([]string, 0, len(body.Members))
	for _, username := range body.Members {
		username = strings.TrimPrefix(strings.TrimSpace(username), "@")
		if username == "" {
			continue
		}
		user, appErr := p.API.GetUserByUsername(username)
		if appErr != nil {
			writeJSON(w, map[string]string{"error": fmt.Sprintf("unknown user: %s", username)})
			return
		}
		memberIDs = append(memberIDs, user.Id)
	}

	message, err := p.submitRequest(requestInput{
		RequesterID: userID,
		TeamID:      body.TeamID,
		DisplayName: body.DisplayName,
		Name:        body.Name,
		Prefix:      body.Prefix,
		Purpose:     body.Purpose,
		ChannelType: body.ChannelType,
		MemberIDs:   memberIDs,
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

	// Only System Admins may approve or deny requests.
	actingUser, appErr := p.API.GetUser(request.UserId)
	if appErr != nil || !actingUser.IsSystemAdmin() {
		writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: "Only a System Admin can approve or deny channel requests."})
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

	var outcome string
	if approve {
		channel, createErr := p.createChannelForRequest(req)
		if createErr != nil {
			p.API.LogError("failed to create channel on approval", "error", createErr.Error())
			writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: fmt.Sprintf("Could not create the channel: %s", createErr.Error())})
			return
		}
		outcome = fmt.Sprintf("✅ Approved by @%s. Channel ~%s created.", actingUser.Username, channel.Name)
		p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request for channel **%s** was approved. It's now available at ~%s.", req.DisplayName, channel.Name))
	} else {
		outcome = fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request for channel **%s** was denied.", req.DisplayName))
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
	model.ParseSlackAttachment(post, attachments)
	post.Message = status
	return post
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
