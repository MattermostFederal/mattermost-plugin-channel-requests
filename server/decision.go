package main

import (
	"encoding/json"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// kvApprovalPostsPrefix namespaces the IDs of the posts that carry a
// request's approval card, so every copy can be resolved together.
const kvApprovalPostsPrefix = "approval_posts_"

// decisionResult is what an approve or deny produced.
type decisionResult struct {
	// Reply is the private reply to the acting user; "" when the cards
	// say it all.
	Reply string
	// Outcome is the status line for the approval cards; "" when nothing
	// was resolved.
	Outcome string
}

// replyText is the private reply for the slash command, which has no
// card to update.
func (d decisionResult) replyText() string {
	if d.Reply != "" {
		return d.Reply
	}
	return d.Outcome
}

// decideChannelRequest approves or denies a channel request on behalf of
// actingUserID. The approval card buttons and the slash command both come
// through here, so they make the same checks in the same order.
// clickedPostID is the card that was clicked, which the caller updates
// itself; "" when there is none.
func (p *Plugin) decideChannelRequest(actingUserID, requestID, clickedPostID string, approve bool) decisionResult {
	// Approvers: System Admins always. Team Admins of the approval team
	// too, if the admin has opted in via AllowTeamAdminApprovers.
	actingUser, appErr := p.API.GetUser(actingUserID)
	if appErr != nil {
		return decisionResult{Reply: "Could not verify your identity to approve/deny."}
	}
	if !p.canApprove(actingUser) {
		return decisionResult{Reply: "You don't have permission to approve or deny channel requests. Contact a System Admin."}
	}

	req, rawReq, err := p.loadRequest(requestID)
	if err != nil {
		p.API.LogError("failed to load channel request", "error", err.Error())
		return decisionResult{Reply: "Could not load that request."}
	}
	if req == nil {
		// Only the decision that wins the claim below updates the cards.
		return decisionResult{Reply: alreadyHandledText}
	}

	if approve && req.RequesterID == actingUser.Id && !actingUser.IsSystemAdmin() {
		return decisionResult{Reply: "You can't approve your own request. Another approver has to review it."}
	}

	config := p.getConfiguration()
	// Best-effort for the welcome post and audit; approve checks requesterErr.
	requester, requesterErr := p.API.GetUser(req.RequesterID)

	if requestExpired(req.CreatedAt, config.ExpiryDays(), model.GetMillis()) {
		return p.expireRequest(config, expiredRequest{
			kvKey:         kvRequestPrefix + req.ID,
			raw:           rawReq,
			kind:          pendingKindChannel,
			requestID:     req.ID,
			requesterID:   req.RequesterID,
			clickedPostID: clickedPostID,
			notice:        fmt.Sprintf("Your request for channel **%s** expired before it was reviewed. Submit it again if you still need it.", escapeMarkdown(req.DisplayName)),
			audit: fmt.Sprintf("EXPIRED: @%s closed channel request \"%s\" from %s, which was not reviewed within %d days",
				actingUser.Username, escapeMarkdown(req.DisplayName), requesterUsername(requester, req.RequesterID), config.ExpiryDays()),
		})
	}

	// Re-check what submitRequest checked, since the request may have been
	// pending a while. A failed check leaves it pending so it can be denied.
	if approve {
		if msg := p.approvalBlocker(config, req, requester, requesterErr); msg != "" {
			return decisionResult{Reply: msg}
		}
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
		return decisionResult{Reply: "Could not process that request."}
	}
	if !claimed {
		// The winner updates the cards; overwriting them here would erase
		// their outcome.
		return decisionResult{Reply: alreadyHandledText}
	}

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
				p.releasePending(pendingKindChannel, req.RequesterID, req.ID)
				// The request is gone, so no card should offer it any more.
				lost := fmt.Sprintf("⚠️ Approval by @%s failed and the request could not be kept. Ask the requester to submit it again.", actingUser.Username)
				p.resolveApprovalCards(req.ID, lost, clickedPostID)
				return decisionResult{
					Reply:   fmt.Sprintf("Could not create the channel (%s), and the pending request could not be saved — ask the requester to submit it again.", createErr.Error()),
					Outcome: lost,
				}
			}
			return decisionResult{Reply: fmt.Sprintf("Could not create the channel: %s. The request is still pending. If this keeps failing, the channel name may already be taken — deny it and ask the requester to resubmit with a different name.", createErr.Error())}
		}
		outcome = fmt.Sprintf("✅ Approved by @%s. Channel ~%s created.", actingUser.Username, channel.Name)
		if requester != nil {
			p.postWelcomeMessage(channel, req, requester, actingUser)
		}
		p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request for channel **%s** was approved. It's now available at ~%s.", escapeMarkdown(req.DisplayName), channel.Name))
		p.logAudit(config, fmt.Sprintf("APPROVED: @%s approved channel request \"%s\" (~%s) from %s",
			actingUser.Username, escapeMarkdown(req.DisplayName), channel.Name, requesterUsername(requester, req.RequesterID)))
	} else {
		outcome = fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request for channel **%s** was denied.", escapeMarkdown(req.DisplayName)))
		p.logAudit(config, fmt.Sprintf("DENIED: @%s denied channel request \"%s\" from %s",
			actingUser.Username, escapeMarkdown(req.DisplayName), requesterUsername(requester, req.RequesterID)))
	}

	// The KV key was already removed by the atomic claim above.
	p.releasePending(pendingKindChannel, req.RequesterID, req.ID)
	p.resolveApprovalCards(req.ID, outcome, clickedPostID)
	return decisionResult{Outcome: outcome}
}

// decideAdminRequest approves or denies a Channel Admin request on behalf
// of actingUserID. Mirrors decideChannelRequest but operates on
// adminRequest records and promotes nominees instead of creating a
// channel.
func (p *Plugin) decideAdminRequest(actingUserID, requestID, clickedPostID string, approve bool) decisionResult {
	actingUser, appErr := p.API.GetUser(actingUserID)
	if appErr != nil {
		return decisionResult{Reply: "Could not verify your identity to approve/deny."}
	}

	req, rawReq, err := p.loadAdminRequest(requestID)
	if err != nil {
		p.API.LogError("failed to load channel-admin request", "error", err.Error())
		return decisionResult{Reply: "Could not load that request."}
	}
	if req == nil {
		// See decideChannelRequest.
		return decisionResult{Reply: alreadyHandledText}
	}

	// A channel-admin request is scoped to a specific channel, so the check
	// needs the loaded request's ChannelID and happens here rather than up
	// front.
	if !p.canApproveAdminRequest(actingUser, req.ChannelID) {
		return decisionResult{Reply: "You don't have permission to approve or deny this Channel Admin request. Contact a Team Admin of the channel's team or a System Admin."}
	}
	if approve && req.RequesterID == actingUser.Id && !actingUser.IsSystemAdmin() {
		return decisionResult{Reply: "You can't approve your own request. Another approver has to review it."}
	}

	config := p.getConfiguration()
	// Best-effort for notify and audit; approve checks requesterErr.
	requester, requesterErr := p.API.GetUser(req.RequesterID)
	channel, channelErr := p.API.GetChannel(req.ChannelID)

	channelRef := "the channel"
	if channelErr == nil {
		channelRef = "~" + channel.Name
	}
	nominees := p.mentionList(req.NomineeIDs)

	if requestExpired(req.CreatedAt, config.ExpiryDays(), model.GetMillis()) {
		return p.expireRequest(config, expiredRequest{
			kvKey:         kvAdminRequestPrefix + req.ID,
			raw:           rawReq,
			kind:          pendingKindAdmin,
			requestID:     req.ID,
			requesterID:   req.RequesterID,
			clickedPostID: clickedPostID,
			notice:        fmt.Sprintf("Your request to make %s Channel Admin in %s expired before it was reviewed. Submit it again if you still need it.", nominees, channelRef),
			audit: fmt.Sprintf("CHANNEL ADMIN EXPIRED: @%s closed the request for %s in %s from %s, which was not reviewed within %d days",
				actingUser.Username, nominees, channelRef, requesterUsername(requester, req.RequesterID), config.ExpiryDays()),
		})
	}

	// On approval we need a live channel to promote into; bail before claiming
	// the request so a deleted channel leaves the pending request intact.
	// GetChannel returns archived channels too, so check DeleteAt.
	if approve && (channelErr != nil || channel.DeleteAt != 0) {
		return decisionResult{Reply: "Could not load the channel; it may have been deleted."}
	}

	// Re-check what submitAdminRequest checked about the requester. A failed
	// check leaves the request pending so it can be denied.
	if approve {
		if msg := p.adminApprovalBlocker(req, requester, requesterErr); msg != "" {
			return decisionResult{Reply: msg}
		}
	}

	// Atomically claim the request before acting. Two reviewers clicking
	// Approve/Deny at nearly the same time both load a non-nil req; without an
	// atomic claim both would promote (or one approve + one deny) the same
	// request. Compare against the exact bytes read so the claim is robust
	// regardless of marshaling. Whoever wins owns the outcome; the loser is
	// told it was already handled. Mirrors decideChannelRequest.
	claimed, claimErr := p.API.KVCompareAndDelete(kvAdminRequestPrefix+req.ID, rawReq)
	if claimErr != nil {
		p.API.LogError("failed to claim channel-admin request", "error", claimErr.Error())
		return decisionResult{Reply: "Could not process that request."}
	}
	if !claimed {
		return decisionResult{Reply: alreadyHandledText}
	}

	var outcome string
	if approve {
		// Report only who was actually promoted; nominees who have since
		// left the channel are skipped.
		req.NomineeIDs = p.promoteChannelAdmins(req)
		promoted := p.mentionList(req.NomineeIDs)
		if promoted == "" {
			outcome = fmt.Sprintf("⚠️ Approved by @%s, but nobody was promoted: the nominees are no longer members of %s.", actingUser.Username, channelRef)
			p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request to make %s Channel Admin in %s was approved, but nobody was promoted because they are no longer members of the channel.", nominees, channelRef))
			p.logAudit(config, fmt.Sprintf("CHANNEL ADMIN APPROVED (NO CHANGE): @%s approved %s for %s, but none were still members (requested by %s)",
				actingUser.Username, nominees, channelRef, requesterUsername(requester, req.RequesterID)))
		} else {
			p.postAdminPromotionMessage(channel, req, actingUser)
			outcome = fmt.Sprintf("✅ Approved by @%s. %s promoted to Channel Admin in %s.", actingUser.Username, promoted, channelRef)
			p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request to make %s Channel Admin in %s was approved. Promoted: %s.", nominees, channelRef, promoted))
			p.logAudit(config, fmt.Sprintf("CHANNEL ADMIN APPROVED: @%s promoted %s to Channel Admin in %s (requested by %s)",
				actingUser.Username, promoted, channelRef, requesterUsername(requester, req.RequesterID)))
		}
	} else {
		outcome = fmt.Sprintf("❌ Denied by @%s.", actingUser.Username)
		p.notifyRequester(req.RequesterID, fmt.Sprintf("Your request to make %s Channel Admin in %s was denied.", nominees, channelRef))
		p.logAudit(config, fmt.Sprintf("CHANNEL ADMIN DENIED: @%s denied a Channel Admin request for %s in %s",
			actingUser.Username, nominees, channelRef))
	}

	// The KV key was already removed by the atomic claim above.
	p.releasePending(pendingKindAdmin, req.RequesterID, req.ID)
	p.resolveApprovalCards(req.ID, outcome, clickedPostID)
	return decisionResult{Outcome: outcome}
}

// expiredRequest describes a pending request that was approved or denied
// after it expired.
type expiredRequest struct {
	kvKey         string
	raw           []byte
	kind          string
	requestID     string
	requesterID   string
	clickedPostID string
	notice        string // DM to the requester
	audit         string // audit-channel line
}

// expireRequest closes an expired request: it claims the record like an
// approve or deny would, tells the requester, and marks the cards expired.
func (p *Plugin) expireRequest(config *configuration, e expiredRequest) decisionResult {
	claimed, claimErr := p.API.KVCompareAndDelete(e.kvKey, e.raw)
	if claimErr != nil {
		p.API.LogError("failed to claim expired request", "error", claimErr.Error())
		return decisionResult{Reply: "Could not process that request."}
	}
	if !claimed {
		return decisionResult{Reply: alreadyHandledText}
	}
	p.releasePending(e.kind, e.requesterID, e.requestID)
	p.notifyRequester(e.requesterID, e.notice)
	p.logAudit(config, e.audit)
	outcome := fmt.Sprintf("⌛ Expired: not reviewed within %d days.", config.ExpiryDays())
	p.resolveApprovalCards(e.requestID, outcome, e.clickedPostID)
	return decisionResult{Reply: expiredText, Outcome: outcome}
}

// saveApprovalPosts records the posts that carry a request's approval
// card. A failure is logged: the cards then update only when clicked.
func (p *Plugin) saveApprovalPosts(kind, requestID string, postIDs []string) {
	data, _ := json.Marshal(postIDs) // a []string always marshals
	if appErr := p.API.KVSet(kvApprovalPostsPrefix+requestID, data); appErr != nil {
		p.API.LogWarn("failed to record approval posts", "request_id", requestID, "error", appErr.Error())
		return
	}
	// An approver can act on the first card while the rest are still being
	// posted, before they were recorded here. If the request is already
	// gone, resolve the cards now, since nothing else will.
	exists, err := p.pendingRequestExists(kind, requestID)
	if err != nil {
		p.API.LogWarn("failed to check request after recording approval posts", "request_id", requestID, "error", err.Error())
		return
	}
	if !exists {
		p.resolveApprovalCards(requestID, alreadyHandledText, "")
	}
}

func (p *Plugin) deleteApprovalPosts(requestID string) {
	if appErr := p.API.KVDelete(kvApprovalPostsPrefix + requestID); appErr != nil {
		p.API.LogWarn("failed to delete approval posts record", "request_id", requestID, "error", appErr.Error())
	}
}

// loadApprovalPosts returns the recorded approval post IDs for a request,
// or nil when none were recorded.
func (p *Plugin) loadApprovalPosts(requestID string) ([]string, error) {
	data, appErr := p.API.KVGet(kvApprovalPostsPrefix + requestID)
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to load approval posts")
	}
	if data == nil {
		return nil, nil
	}
	var postIDs []string
	if err := json.Unmarshal(data, &postIDs); err != nil {
		return nil, errors.Wrap(err, "failed to read approval posts")
	}
	return postIDs, nil
}

// resolveApprovalCards marks every recorded copy of a request's approval
// card with outcome and removes its buttons, except skipPostID, which the
// caller updates itself. Failures are logged; a card left behind answers
// a later click with "already handled".
func (p *Plugin) resolveApprovalCards(requestID, outcome, skipPostID string) {
	postIDs, err := p.loadApprovalPosts(requestID)
	if err != nil {
		p.API.LogWarn("failed to resolve approval cards", "request_id", requestID, "error", err.Error())
		return
	}
	if postIDs == nil {
		return
	}
	for _, postID := range postIDs {
		if postID == skipPostID {
			continue
		}
		post, appErr := p.API.GetPost(postID)
		if appErr != nil {
			p.API.LogWarn("failed to load approval card", "request_id", requestID, "post_id", postID, "error", appErr.Error())
			continue
		}
		if post == nil || post.UserId != p.botUserID {
			continue
		}
		if _, appErr := p.API.UpdatePost(resolvedCard(post, outcome)); appErr != nil {
			p.API.LogWarn("failed to resolve approval card", "request_id", requestID, "post_id", postID, "error", appErr.Error())
		}
	}
	p.deleteApprovalPosts(requestID)
}
