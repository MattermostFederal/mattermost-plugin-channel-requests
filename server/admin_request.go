package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	// kvAdminRequestPrefix namespaces pending channel-admin requests in the
	// KV store, kept distinct from kvRequestPrefix (channel-creation
	// requests) so the two request types never collide.
	kvAdminRequestPrefix = "admin_request_"
)

// adminRequest is a pending request to promote one or more existing users to
// Channel Admin on an already-created channel. Persisted in the KV store
// until a reviewer approves or denies it. This is the counterpart to
// channelRequest, which is about creating brand-new channels.
type adminRequest struct {
	ID          string `json:"id"`
	RequesterID string `json:"requester_id"`
	ChannelID   string `json:"channel_id"`
	// NomineeIDs are the users the requester wants promoted to Channel
	// Admin. On approval each is added to the channel (if not already a
	// member) and granted the channel_admin scheme role.
	NomineeIDs []string `json:"nominee_ids"`
}

// submitAdminRequest validates the input and either promotes the nominees
// immediately (System Admins + auto-approve users) or stores a pending
// request and posts it to the approval channel. Returns a message suitable
// for showing to the requester.
func (p *Plugin) submitAdminRequest(requesterID, channelID string, nomineeIDs []string) (string, error) {
	if strings.TrimSpace(channelID) == "" {
		return "", errors.New("a channel is required")
	}

	nominees := dedupeNonEmpty(nomineeIDs)
	if len(nominees) == 0 {
		return "", errors.New("pick at least one person to make a Channel Admin")
	}

	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load channel")
	}

	requester, appErr := p.API.GetUser(requesterID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to load requesting user")
	}

	req := &adminRequest{
		ID:          model.NewId(),
		RequesterID: requesterID,
		ChannelID:   channelID,
		NomineeIDs:  nominees,
	}

	config := p.getConfiguration()

	// Bypass approval for System Admins and delegated managers on the
	// auto-approve list, mirroring the channel-creation flow.
	if requester.IsSystemAdmin() || config.AutoApproveContains(requester.Id) {
		p.promoteChannelAdmins(req)
		p.postAdminPromotionMessage(channel, req, requester)
		p.logAudit(config, fmt.Sprintf("CHANNEL ADMIN: @%s promoted %s to Channel Admin in ~%s",
			requester.Username, p.mentionList(req.NomineeIDs), channel.Name))
		return fmt.Sprintf("Promoted %s to Channel Admin in ~%s.", p.mentionList(req.NomineeIDs), channel.Name), nil
	}

	if err := p.storeAdminRequest(req); err != nil {
		return "", err
	}

	if err := p.postAdminApprovalRequest(req, requester, channel); err != nil {
		// Roll back so the stored request isn't orphaned without an approval message.
		_ = p.API.KVDelete(kvAdminRequestPrefix + req.ID)
		return "", err
	}

	return "Your Channel Admin request has been submitted for approval. You'll be notified once an admin responds.", nil
}

// promoteChannelAdmins adds each nominee to the channel (a no-op for existing
// members) and grants them the channel_admin role. Per-user failures are
// logged but don't abort the others — a partial promotion is better than
// none.
func (p *Plugin) promoteChannelAdmins(req *adminRequest) {
	for _, userID := range req.NomineeIDs {
		if userID == "" {
			continue
		}
		// Ensure membership first — a user can't be a Channel Admin without
		// being in the channel. AddChannelMember is idempotent for existing
		// members; a failure here is logged but we still attempt the
		// promotion in case they're already a member.
		if _, appErr := p.API.AddChannelMember(req.ChannelID, userID); appErr != nil {
			p.API.LogWarn("failed to add nominee to channel", "channel_id", req.ChannelID, "user_id", userID, "error", appErr.Error())
		}
		if _, appErr := p.API.UpdateChannelMemberRoles(req.ChannelID, userID, channelAdminRoleString); appErr != nil {
			p.API.LogWarn("failed to promote nominee to channel admin", "channel_id", req.ChannelID, "user_id", userID, "error", appErr.Error())
		}
	}
}

// postAdminPromotionMessage posts a bot message in the channel announcing the
// newly-promoted Channel Admins so the channel sees who can now manage it.
func (p *Plugin) postAdminPromotionMessage(channel *model.Channel, req *adminRequest, approver *model.User) {
	nominees := p.mentionList(req.NomineeIDs)
	if nominees == "" {
		return
	}
	body := nominees + " is now a Channel Admin for this channel."
	if approver != nil {
		body += fmt.Sprintf(" (Approved by @%s.)", approver.Username)
	}
	if _, appErr := p.API.CreatePost(&model.Post{
		UserId:    p.botUserID,
		ChannelId: channel.Id,
		Message:   body,
	}); appErr != nil {
		p.API.LogWarn("failed to post channel-admin promotion message", "channel_id", channel.Id, "error", appErr.Error())
	}
}

func (p *Plugin) storeAdminRequest(req *adminRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return errors.Wrap(err, "failed to marshal admin request")
	}
	if appErr := p.API.KVSet(kvAdminRequestPrefix+req.ID, data); appErr != nil {
		return errors.Wrap(appErr, "failed to store admin request")
	}
	return nil
}

func (p *Plugin) loadAdminRequest(id string) (*adminRequest, error) {
	data, appErr := p.API.KVGet(kvAdminRequestPrefix + id)
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to load admin request")
	}
	if data == nil {
		return nil, nil
	}
	var req adminRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal admin request")
	}
	return &req, nil
}

// postAdminApprovalRequest posts a channel-admin request (with Approve/Deny
// buttons) to the configured approval channel, where the guaranteed set of
// approvers — System Admins and the target channel's Team Admins — can act on
// it. The post @-mentions those approvers so they're pulled into the review.
//
// The approval channel is REQUIRED: if none is configured (or it can't be
// found) an error is returned so the requester is told, rather than the
// request being silently dropped or landing somewhere with no one to approve
// it. The requester sees this error in the request modal.
func (p *Plugin) postAdminApprovalRequest(req *adminRequest, requester *model.User, channel *model.Channel) error {
	config := p.getConfiguration()

	if strings.TrimSpace(config.ApprovalTeam) == "" || strings.TrimSpace(config.ApprovalChannel) == "" {
		return errors.New("No approval channel is configured for Channel Admin requests. Ask a System Admin to set the approval team and channel in the plugin settings.")
	}

	approvalChannel, appErr := p.API.GetChannelByNameForTeamName(config.ApprovalTeam, config.ApprovalChannel, false)
	if appErr != nil || approvalChannel == nil {
		return errors.New("The approval channel configured for Channel Admin requests could not be found. Ask a System Admin to check the plugin settings.")
	}

	post := &model.Post{
		UserId:    p.botUserID,
		ChannelId: approvalChannel.Id,
		Message:   p.adminApprovalHeader(channel),
	}
	model.ParseMessageAttachment(post, []*model.MessageAttachment{p.adminApprovalAttachment(req, requester, channel)})
	if _, appErr := p.API.CreatePost(post); appErr != nil {
		return errors.Wrap(appErr, "failed to post channel-admin request to the approval channel")
	}
	return nil
}

// adminApprovalHeader builds the message that leads the approval post,
// @-mentioning the users who can approve it so they're notified.
func (p *Plugin) adminApprovalHeader(channel *model.Channel) string {
	mentions := p.mentionList(p.adminApproverIDs(channel.TeamId))
	if mentions == "" {
		return "A Channel Admin request needs your review."
	}
	return mentions + " — a Channel Admin request needs your review."
}

// adminApproverIDs returns the user IDs authorized to approve a channel-admin
// request: every System Admin plus the Team Admins of the target channel's
// team. Deduped, bot excluded. Best-effort — lookup failures are logged and
// skipped rather than aborting the request.
func (p *Plugin) adminApproverIDs(teamID string) []string {
	seen := make(map[string]bool)
	var ids []string
	add := func(id string) {
		if id == "" || id == p.botUserID || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}

	sysAdmins, appErr := p.API.GetUsers(&model.UserGetOptions{Role: model.SystemAdminRoleId, Page: 0, PerPage: 100})
	if appErr != nil {
		p.API.LogWarn("failed to list System Admins for admin-request approvers", "error", appErr.Error())
	} else {
		for _, u := range sysAdmins {
			add(u.Id)
		}
	}

	if teamID != "" {
		const perPage = 200
		for page := 0; page < 50; page++ {
			members, appErr := p.API.GetTeamMembers(teamID, page, perPage)
			if appErr != nil {
				p.API.LogWarn("failed to list Team Admins for admin-request approvers", "team_id", teamID, "error", appErr.Error())
				break
			}
			if len(members) == 0 {
				break
			}
			for _, m := range members {
				if m.SchemeAdmin || slices.Contains(strings.Fields(m.Roles), model.TeamAdminRoleId) {
					add(m.UserId)
				}
			}
			if len(members) < perPage {
				break
			}
		}
	}
	return ids
}

// adminApprovalAttachment builds the Slack attachment (with Approve/Deny
// buttons) describing a channel-admin request.
func (p *Plugin) adminApprovalAttachment(req *adminRequest, requester *model.User, channel *model.Channel) *model.MessageAttachment {
	fields := []*model.MessageAttachmentField{
		{Title: "Requested by", Value: fmt.Sprintf("@%s", requester.Username), Short: true},
		{Title: "Channel", Value: fmt.Sprintf("~%s", channel.Name), Short: true},
		{Title: "Proposed Channel Admins", Value: p.mentionList(req.NomineeIDs), Short: false},
	}

	siteURL := "/plugins/" + manifest.Id
	return &model.MessageAttachment{
		Title:   "Channel Admin request",
		Color:   "#0058CC",
		Fields:  fields,
		Actions: p.adminApprovalActions(req.ID, siteURL),
	}
}

func (p *Plugin) adminApprovalActions(requestID, siteURL string) []*model.PostAction {
	return []*model.PostAction{
		{
			Id:    "approve",
			Name:  "Approve",
			Type:  model.PostActionTypeButton,
			Style: "primary",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeApproveAdmin,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
		{
			Id:    "deny",
			Name:  "Deny",
			Type:  model.PostActionTypeButton,
			Style: "danger",
			Integration: &model.PostActionIntegration{
				URL:     siteURL + routeDenyAdmin,
				Context: map[string]any{actionContextRequestID: requestID},
			},
		},
	}
}

// dedupeNonEmpty returns the input with empty strings and duplicates removed,
// preserving first-seen order.
func dedupeNonEmpty(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
