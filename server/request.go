package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	// kvRequestPrefix namespaces pending requests in the KV store.
	kvRequestPrefix = "request_"

	// dialogCallbackID identifies submissions from the channel request dialog.
	dialogCallbackID = "channel_request"

	// Dialog element / action context field names.
	fieldDisplayName = "display_name"
	fieldName        = "name"
	fieldPurpose     = "purpose"
	fieldType        = "type"
	fieldMembers     = "members"
	fieldAdmins      = "admins"

	// actionContextRequestID carries the pending request ID on the approve/deny buttons.
	actionContextRequestID = "request_id"

	// Channel type values as plain strings, for use in dialog options, comparisons, and storage.
	channelTypeOpen    = string(model.ChannelTypeOpen)
	channelTypePrivate = string(model.ChannelTypePrivate)

	// Server-side input bounds. The dialog enforces these client-side via
	// MaxLength, but the webapp JSON endpoint bypasses the dialog, so the
	// server must enforce them too.
	maxDisplayNameLen = 64
	maxPurposeLen     = 250

	// maxMembersPerList caps the regular-member and channel-admin lists
	// independently so a single request can't fan out into thousands of
	// synchronous user-lookup and add-member API calls.
	maxMembersPerList = 100

	// channelAdminRoleString grants a channel member the Channel Admin
	// role. MM maps this classic role string to the appropriate scheme
	// role internally. Used both when creating a channel and when
	// promoting members on an existing one.
	channelAdminRoleString = "channel_user channel_admin"
)

// channelRequest is a pending request to create a channel, persisted in the KV store until a System
// Admin approves or denies it.
type channelRequest struct {
	ID          string   `json:"id"`
	RequesterID string   `json:"requester_id"`
	TeamID      string   `json:"team_id"`
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Purpose     string   `json:"purpose"`
	ChannelType string   `json:"channel_type"`
	MemberIDs   []string `json:"member_ids"`
	// AdminMemberIDs are the requester-designated Channel Admins. On
	// approval, these users get channel_admin scheme roles in the newly
	// created channel (in addition to being members).
	AdminMemberIDs []string `json:"admin_member_ids"`

	// Ticket messaging fields — set after the approval post and DM are created.
	// Used by the MessageHasBeenPosted hook to route thread replies in both directions.
	ApprovalPostID    string `json:"approval_post_id,omitempty"`
	ApprovalChannelID string `json:"approval_channel_id,omitempty"`
	DMRootPostID      string `json:"dm_root_post_id,omitempty"`
	DMChannelID       string `json:"dm_channel_id,omitempty"`
}

// requestInput is the normalized set of values gathered from either entry point (slash command
// dialog or webapp modal) before a request is created.
type requestInput struct {
	RequesterID string
	TeamID      string
	DisplayName string
	// Name holds ONLY THE SUFFIX (the part after the selected prefix);
	// server code prepends the prefix. May be blank, in which case the
	// suffix is generated from DisplayName.
	Name string
	// Prefix is the selected domain prefix (e.g., "team-", "project-").
	// Required — a request with no prefix is rejected by resolveChannelName.
	Prefix         string
	Purpose        string
	ChannelType    string
	MemberIDs      []string
	AdminMemberIDs []string
}

// promoteSoleMember applies the orphaned-channel guard: when no Channel
// Admins were designated and exactly one regular member was added, that
// member becomes the Channel Admin (returned as the admin list, with the
// member list emptied). In every other case the inputs pass through
// unchanged. A single-member channel with no admin has nobody who can
// manage it, which is the "No Channel Admin" orphan case.
func promoteSoleMember(memberIDs, adminIDs []string) (members, admins []string) {
	if len(adminIDs) == 0 && len(memberIDs) == 1 {
		return nil, memberIDs
	}
	return memberIDs, adminIDs
}

var invalidNameChars = regexp.MustCompile(`[^a-z0-9]+`)

// slugify converts a free-form string into a valid channel URL name.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = invalidNameChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > model.ChannelNameMaxLength {
		s = s[:model.ChannelNameMaxLength]
	}
	return s
}

// resolveChannelName derives the final channel URL name from the
// request using the admin's configured prefix list. Requires at least
// one prefix to be defined; returns an error otherwise so the admin
// notices the misconfiguration instead of silently permitting free-form
// channel names.
func (p *Plugin) resolveChannelName(config *configuration, in requestInput) (string, error) {
	if !config.UsesPrefixList() {
		return "", errors.New("plugin is not configured: an admin must define at least one channel prefix in System Console -> Plugins -> Mattermost Permissions")
	}
	return resolvePrefixedName(config.Prefixes(), in)
}

// resolvePrefixedName joins the selected prefix with a slugified suffix.
// The suffix is slugified BEFORE joining so users can type "My Team" and
// get "team-my-team".
func resolvePrefixedName(prefixes []channelPrefix, in requestInput) (string, error) {
	selected := strings.TrimSpace(in.Prefix)
	if selected == "" {
		return "", errors.New("please pick a channel prefix (e.g., team-, project-, ops-) from the dropdown")
	}
	var entry *channelPrefix
	for i := range prefixes {
		if prefixes[i].Prefix == selected {
			entry = &prefixes[i]
			break
		}
	}
	if entry == nil {
		return "", errors.Errorf("prefix %q is not in the list of allowed prefixes", selected)
	}

	suffix := strings.TrimSpace(in.Name)
	if suffix == "" {
		// Fall back to slugifying the display name so users who leave
		// the URL field blank still get a sensible suffix.
		suffix = in.DisplayName
	}
	suffix = slugify(suffix)
	// Requesters sometimes double-type the prefix; strip it so
	// "team-marketing" under prefix "team-" doesn't become "team-team-marketing".
	suffix = strings.TrimPrefix(suffix, strings.TrimSuffix(entry.Prefix, "-")+"-")
	suffix = strings.Trim(suffix, "-")

	if suffix == "" {
		return "", errors.New("channel name suffix is required (letters/numbers, becomes the part after the prefix)")
	}

	// The compiled pattern is anchored (^(?:...)$) so it must match the
	// whole suffix. Error messages surface the raw pattern text so users
	// see what the admin wrote, not our anchored rewrite.
	if entry.SuffixPattern != nil && !entry.SuffixPattern.MatchString(suffix) {
		return "", errors.Errorf("suffix %q doesn't match the required pattern for prefix %q (%s)", suffix, entry.Prefix, entry.SuffixPatternRaw)
	}

	name := entry.Prefix + suffix
	if !model.IsValidChannelIdentifier(name) {
		return "", errors.Errorf("%q is not a valid channel URL name; combined prefix + suffix must be 2-64 lowercase letters, numbers, or hyphens", name)
	}
	return name, nil
}

// validateRequestInput checks the caller-supplied fields that require no
// API calls, returning a user-facing error for the first problem. It is
// pure so it can be unit-tested without mocks. Side-effecting checks —
// team membership (authorization) and prefix/name resolution (needs
// config) — happen in submitRequest after this passes.
func validateRequestInput(in requestInput) error {
	if strings.TrimSpace(in.DisplayName) == "" {
		return errors.New("a channel name is required")
	}
	// Count runes on the trimmed value to match the dialog's MaxLength
	// (which counts characters), so a multibyte name the UI accepts isn't
	// rejected server-side by a byte-length check.
	if utf8.RuneCountInString(strings.TrimSpace(in.DisplayName)) > maxDisplayNameLen {
		return errors.Errorf("channel name must be %d characters or fewer", maxDisplayNameLen)
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Purpose)) > maxPurposeLen {
		return errors.Errorf("purpose must be %d characters or fewer", maxPurposeLen)
	}
	if len(in.MemberIDs) > maxMembersPerList || len(in.AdminMemberIDs) > maxMembersPerList {
		return errors.Errorf("too many members: at most %d members and %d channel admins per request", maxMembersPerList, maxMembersPerList)
	}
	if strings.TrimSpace(in.TeamID) == "" {
		return errors.New("a team is required")
	}
	return nil
}

// submitRequest validates the input and either creates the channel immediately (if the requester is
// a System Admin) or stores a pending request and posts it to the approval channel. It returns a
// message suitable for showing to the requester.
func (p *Plugin) submitRequest(in requestInput) (string, error) {
	config := p.getConfiguration()

	if err := validateRequestInput(in); err != nil {
		return "", err
	}

	requester, appErr := p.API.GetUser(in.RequesterID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load requesting user")
	}

	// System Admins can create channels in any team (they administer all
	// of them). Everyone else — including delegated auto-approve users —
	// must belong to the target team, so a client-supplied TeamID can't be
	// used to plant channels in a team the requester has no access to.
	if !requester.IsSystemAdmin() {
		// GetTeamMember returns soft-deleted rows (a user who LEFT the team
		// still has a TeamMembers row with DeleteAt != 0), so an error isn't
		// enough — require an active membership.
		member, appErr := p.API.GetTeamMember(in.TeamID, in.RequesterID)
		if appErr != nil || member == nil || member.DeleteAt != 0 {
			return "", errors.New("you must be a member of the team to request a channel in it")
		}
	}

	if in.ChannelType != channelTypeOpen && in.ChannelType != channelTypePrivate {
		in.ChannelType = channelTypeOpen
	}

	// Guard against orphaned channels: when the requester adds exactly one
	// member and names no Channel Admins, that lone member is promoted to
	// Channel Admin so the channel always has someone who can manage it.
	// Applied here (before the request is built) so the approval card and
	// welcome message reflect the promotion too — not just the created
	// channel.
	in.MemberIDs, in.AdminMemberIDs = promoteSoleMember(in.MemberIDs, in.AdminMemberIDs)

	name, err := p.resolveChannelName(config, in)
	if err != nil {
		return "", err
	}

	req := &channelRequest{
		ID:             model.NewId(),
		RequesterID:    in.RequesterID,
		TeamID:         in.TeamID,
		Name:           name,
		DisplayName:    strings.TrimSpace(in.DisplayName),
		Purpose:        strings.TrimSpace(in.Purpose),
		ChannelType:    in.ChannelType,
		MemberIDs:      in.MemberIDs,
		AdminMemberIDs: in.AdminMemberIDs,
	}

	// Bypass approval for:
	//   - System Admins (always)
	//   - Users on the admin-configured auto-approve list (delegated managers)
	if requester.IsSystemAdmin() || config.AutoApproveContains(requester.Id) {
		channel, err := p.createChannelForRequest(req)
		if err != nil {
			return "", err
		}
		p.postWelcomeMessage(channel, req, requester, requester)
		// Audit the bypass-path creations too — these are the
		// highest-privilege (System Admin / delegated auto-approve)
		// creations and the ones an audit trail most needs to record.
		p.logAudit(config, fmt.Sprintf("CREATED: @%s created channel `%s` (%s) directly (System Admin or auto-approve)",
			requester.Username, req.DisplayName, channel.Name))
		return fmt.Sprintf("Created channel ~%s.", channel.Name), nil
	}

	// Store the request before posting the approval message so the approve/deny
	// handlers can find it by ID the moment the post is live.
	if err := p.storeRequest(req); err != nil {
		return "", err
	}

	approvalPostID, approvalChannelID, err := p.postApprovalRequest(req, requester)
	if err != nil {
		// Roll back the stored request so it isn't orphaned without an approval message.
		_ = p.API.KVDelete(kvRequestPrefix + req.ID)
		return "", err
	}

	req.ApprovalPostID = approvalPostID
	req.ApprovalChannelID = approvalChannelID

	dmRootPostID, dmChannelID := p.sendTicketCreatedDM(req, requester)
	req.DMRootPostID = dmRootPostID
	req.DMChannelID = dmChannelID

	// Re-store with the post IDs filled in. Best-effort: if this fails the ticket
	// still works (approve/deny still finds the request), but thread forwarding and
	// threaded outcome notifications won't work for this ticket.
	if storeErr := p.storeRequest(req); storeErr != nil {
		p.API.LogWarn("failed to re-store request with post IDs", "request_id", req.ID, "error", storeErr.Error())
	} else {
		p.storeTicketLookups(threadAnchor{
			ApprovalPostID:    approvalPostID,
			ApprovalChannelID: approvalChannelID,
			DMRootPostID:      dmRootPostID,
			DMChannelID:       dmChannelID,
		})
	}

	return "Your channel request has been submitted for approval. You'll be notified once an admin responds.", nil
}

// sendTicketCreatedDM opens a DM between the bot and the requester and posts a
// card mirroring the approval post (same fields, no buttons) as the root of
// the ticket thread. The requester can reply here to add context; replies are
// forwarded to the approval thread by MessageHasBeenPosted. Returns the root
// post ID and DM channel ID; both empty on failure (non-fatal).
func (p *Plugin) sendTicketCreatedDM(req *channelRequest, requester *model.User) (dmRootPostID, dmChannelID string) {
	dm, appErr := p.API.GetDirectChannel(req.RequesterID, p.botUserID)
	if appErr != nil {
		p.API.LogWarn("failed to open DM for ticket notification", "user_id", req.RequesterID, "error", appErr.Error())
		return "", ""
	}

	attachment := p.approvalAttachment(req, requester)
	attachment.Actions = nil
	attachment.Color = colorPending

	post := &model.Post{
		UserId:    p.botUserID,
		ChannelId: dm.Id,
		Message:   "Your channel request has been submitted for approval. Reply to this thread to add context — your reply will be forwarded to the reviewers.",
	}
	model.ParseMessageAttachment(post, []*model.MessageAttachment{attachment})

	created, appErr := p.API.CreatePost(post)
	if appErr != nil {
		p.API.LogWarn("failed to send ticket-created DM", "user_id", req.RequesterID, "error", appErr.Error())
		return "", ""
	}
	return created.Id, dm.Id
}

// postWelcomeMessage posts a bot message in the newly-created channel announcing who requested it
// and who approved, then @-mentions the added members and channel admins so everyone who was
// added is notified they're in the channel.
func (p *Plugin) postWelcomeMessage(channel *model.Channel, req *channelRequest, requester, approver *model.User) {
	body := fmt.Sprintf("Welcome — this channel was requested by @%s", requester.Username)
	if approver != nil && approver.Id != requester.Id {
		body += fmt.Sprintf(" and approved by @%s", approver.Username)
	}
	body += ". Adjust the header + purpose to fit."

	// Mention the added users so they get notified. The requester is the
	// creator and already here, so only call out the designated members
	// and admins. mentionList returns "" for an empty list.
	if members := p.mentionList(req.MemberIDs); members != "" {
		body += "\nAdded: " + members
	}
	if admins := p.mentionList(req.AdminMemberIDs); admins != "" {
		body += "\nChannel admins: " + admins
	}

	if _, appErr := p.API.CreatePost(&model.Post{
		UserId:    p.botUserID,
		ChannelId: channel.Id,
		Message:   body,
	}); appErr != nil {
		p.API.LogWarn("failed to post welcome message", "channel_id", channel.Id, "error", appErr.Error())
	}
}

// logAudit posts an audit-trail line to the auto-created audit channel.
// No-op when ApprovalTeam is not configured (ensureAuditChannel returns nil).
func (p *Plugin) logAudit(_ *configuration, message string) {
	ch := p.ensureAuditChannel()
	if ch == nil {
		return
	}
	if _, appErr := p.API.CreatePost(&model.Post{
		UserId:    p.botUserID,
		ChannelId: ch.Id,
		Message:   message,
	}); appErr != nil {
		p.API.LogWarn("audit post failed", "channel_id", ch.Id, "error", appErr.Error())
	}
}

// createChannelForRequest creates the channel described by req, adds the requester and any
// designated members, promotes anyone in AdminMemberIDs to channel_admin, and returns the
// created channel. Add-and-promote failures on individual users are logged but don't fail
// the overall operation — a partially-populated channel is better than nothing.
func (p *Plugin) createChannelForRequest(req *channelRequest) (*model.Channel, error) {
	channel, appErr := p.API.CreateChannel(&model.Channel{
		TeamId:      req.TeamID,
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Type:        model.ChannelType(req.ChannelType),
		Purpose:     req.Purpose,
		CreatorId:   req.RequesterID,
	})
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to create channel")
	}

	// Union of everyone who should be a member: requester + regular + admin sets.
	// If no explicit Channel Admins were designated, the requester is promoted
	// automatically — a channel with no admin has nobody who can manage it.
	adminIDs := req.AdminMemberIDs
	if len(adminIDs) == 0 {
		adminIDs = []string{req.RequesterID}
	}
	adminSet := map[string]bool{}
	for _, uid := range adminIDs {
		if uid != "" {
			adminSet[uid] = true
		}
	}

	seen := map[string]bool{}
	addedOK := map[string]bool{}
	promotedOK := map[string]bool{}
	for _, userID := range append(append([]string{req.RequesterID}, req.MemberIDs...), req.AdminMemberIDs...) {
		if userID == "" || seen[userID] {
			continue
		}
		seen[userID] = true
		if _, appErr := p.API.AddChannelMember(channel.Id, userID); appErr != nil {
			p.API.LogWarn("failed to add member to created channel", "channel_id", channel.Id, "user_id", userID, "error", appErr.Error())
			continue
		}
		addedOK[userID] = true
		// Promote to channel admin if they were designated as such.
		// "channel_user channel_admin" is the classic role string;
		// MM handles the scheme-role mapping internally.
		if adminSet[userID] {
			if _, appErr := p.API.UpdateChannelMemberRoles(channel.Id, userID, channelAdminRoleString); appErr != nil {
				p.API.LogWarn("failed to promote member to channel admin",
					"channel_id", channel.Id, "user_id", userID, "error", appErr.Error())
				continue
			}
			promotedOK[userID] = true
		}
	}

	// Trim the request's lists to who was actually added (and, for admins,
	// actually promoted) so the welcome message doesn't claim users were
	// added when the API call failed — e.g. a user who isn't a team member
	// can't be added to the channel.
	req.MemberIDs = filterIDs(req.MemberIDs, addedOK)
	req.AdminMemberIDs = filterIDs(req.AdminMemberIDs, promotedOK)

	return channel, nil
}

// filterIDs returns the elements of ids present in keep, preserving order.
func filterIDs(ids []string, keep map[string]bool) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if keep[id] {
			out = append(out, id)
		}
	}
	return out
}

func (p *Plugin) storeRequest(req *channelRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return errors.Wrap(err, "failed to marshal request")
	}
	if appErr := p.API.KVSet(kvRequestPrefix+req.ID, data); appErr != nil {
		return errors.Wrap(appErr, "failed to store request")
	}
	return nil
}

// loadRequest returns the stored request and its raw KV bytes. The raw
// bytes let callers do an atomic KVCompareAndDelete against exactly what
// was read, rather than re-marshaling (which would silently stop matching
// if the struct ever gained a map field or the on-disk schema evolved).
func (p *Plugin) loadRequest(id string) (*channelRequest, []byte, error) {
	data, appErr := p.API.KVGet(kvRequestPrefix + id)
	if appErr != nil {
		return nil, nil, errors.Wrap(appErr, "failed to load request")
	}
	if data == nil {
		return nil, nil, nil
	}
	var req channelRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, nil, errors.Wrap(err, "failed to unmarshal request")
	}
	return &req, data, nil
}

// postApprovalRequest posts a channel-creation request (with Approve/Deny buttons) for an admin to
// act on. Returns the created post's ID and channel ID, which are empty in fallback mode (when the
// request is DM'd to system admins instead of posted to a channel).
func (p *Plugin) postApprovalRequest(req *channelRequest, requester *model.User) (postID, channelID string, err error) {
	approvalChannel, teamLabel := p.resolveApprovalChannel(req.TeamID)
	return p.postApprovalAttachment(
		p.approvalAttachment(req, requester),
		teamLabel+"@channel — a new channel request needs your review.",
		approvalChannel,
	)
}

// postApprovalAttachment delivers an approval attachment to the reviewers. When approvalChannel is
// non-nil, the post goes there; otherwise it falls back to DMing every System Admin so requests are
// never silently dropped. Shared by the channel-creation and channel-admin request flows.
//
// Returns (postID, channelID, error). postID and channelID are empty in fallback mode — in that case
// the ticket messaging features (thread reply forwarding) are unavailable, but approve/deny notifications
// still reach the requester via a new DM root post.
func (p *Plugin) postApprovalAttachment(attachment *model.MessageAttachment, headerMessage string, approvalChannel *model.Channel) (postID, channelID string, err error) {
	if approvalChannel != nil {
		post := &model.Post{
			UserId:    p.botUserID,
			ChannelId: approvalChannel.Id,
			Message:   headerMessage,
		}
		model.ParseMessageAttachment(post, []*model.MessageAttachment{attachment})
		created, postErr := p.API.CreatePost(post)
		if postErr != nil {
			return "", "", errors.Wrap(postErr, "failed to post approval request")
		}
		return created.Id, approvalChannel.Id, nil
	}

	return "", "", p.postApprovalToSystemAdmins(attachment)
}

// postApprovalToSystemAdmins DMs the approval request to every System Admin. Any admin can act on
// it; once one does, the others' copies resolve to an "already handled" message.
func (p *Plugin) postApprovalToSystemAdmins(attachment *model.MessageAttachment) error {
	admins, appErr := p.API.GetUsers(&model.UserGetOptions{Role: model.SystemAdminRoleId, Page: 0, PerPage: 100})
	if appErr != nil {
		return errors.Wrap(appErr, "failed to list System Admins")
	}

	posted := 0
	for _, admin := range admins {
		if admin.Id == p.botUserID {
			continue
		}
		dm, appErr := p.API.GetDirectChannel(admin.Id, p.botUserID)
		if appErr != nil {
			p.API.LogWarn("failed to open DM with System Admin", "user_id", admin.Id, "error", appErr.Error())
			continue
		}
		post := &model.Post{UserId: p.botUserID, ChannelId: dm.Id}
		model.ParseMessageAttachment(post, []*model.MessageAttachment{attachment})
		if _, appErr := p.API.CreatePost(post); appErr != nil {
			p.API.LogWarn("failed to DM approval request to System Admin", "user_id", admin.Id, "error", appErr.Error())
			continue
		}
		posted++
	}

	if posted == 0 {
		return errors.New("no System Admins are available to receive the approval request")
	}
	return nil
}

// approvalAttachment builds the Slack attachment (with Approve/Deny buttons) describing a request.
// Core identifying fields (requester, visibility, channel name, URL) are always visible.
// Verbose details (purpose, members, admins) go in Text so Mattermost's native "Show more"
// collapses them on long requests — the approval channel stays scannable.
func (p *Plugin) approvalAttachment(req *channelRequest, requester *model.User) *model.MessageAttachment {
	visibility := "Public"
	if req.ChannelType == channelTypePrivate {
		visibility = "Private"
	}

	fields := []*model.MessageAttachmentField{
		{Title: "Requested by", Value: fmt.Sprintf("@%s", requester.Username), Short: true},
		{Title: "Visibility", Value: visibility, Short: true},
		{Title: "Channel name", Value: req.DisplayName, Short: true},
		{Title: "URL", Value: fmt.Sprintf("~%s", req.Name), Short: true},
	}

	var details []string
	if req.Purpose != "" {
		details = append(details, fmt.Sprintf("**Purpose:** %s", req.Purpose))
	}
	if len(req.MemberIDs) > 0 {
		details = append(details, fmt.Sprintf("**Members to add:** %s", p.mentionList(req.MemberIDs)))
	}
	if len(req.AdminMemberIDs) > 0 {
		details = append(details, fmt.Sprintf("**Channel Admins to add:** %s", p.mentionList(req.AdminMemberIDs)))
	}

	siteURL := "/plugins/" + manifest.Id
	return &model.MessageAttachment{
		Title:   "Channel creation request",
		Color:   "#0058CC",
		Fields:  fields,
		Text:    strings.Join(details, "\n"),
		Actions: p.approvalActions(req.ID, siteURL),
	}
}

func (p *Plugin) approvalActions(requestID, siteURL string) []*model.PostAction {
	return []*model.PostAction{
		{
			Id:    "approve",
			Name:  "Approve",
			Type:  model.PostActionTypeButton,
			Style: "primary",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeApprove,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
		{
			Id:    "deny",
			Name:  "Deny",
			Type:  model.PostActionTypeButton,
			Style: "danger",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeDeny,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
	}
}

// mentionList renders a list of user IDs as @mentions for display.
func (p *Plugin) mentionList(userIDs []string) string {
	mentions := make([]string, 0, len(userIDs))
	for _, id := range userIDs {
		if user, appErr := p.API.GetUser(id); appErr == nil {
			mentions = append(mentions, "@"+user.Username)
		}
	}
	return strings.Join(mentions, ", ")
}

