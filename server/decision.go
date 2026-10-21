package main

import (
	"encoding/json"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// kvApprovalPostsPrefix namespaces the IDs of the direct-message copies of
// a request's approval card, posted to each System Admin when there is no
// approval channel, so every copy can be resolved together.
const kvApprovalPostsPrefix = "approval_dm_posts_"

// decisionResult is what an approve or deny produced.
type decisionResult struct {
	// Reply is the private reply to the acting user; "" when the cards
	// say it all.
	Reply string
	// Outcome is the status line for the approval cards; "" when nothing
	// was resolved.
	Outcome string
	// Update is the clicked approval card, resolved; nil when there was no
	// click or nothing was resolved.
	Update *model.Post
}

// replyText is the private reply for the slash command, which has no
// card to update.
func (d decisionResult) replyText() string {
	if d.Reply != "" {
		return d.Reply
	}
	return d.Outcome
}

// requestUpdatedText is the private reply when a decision lost the claim
// while the request is still pending.
func requestUpdatedText(clicked bool) string {
	if clicked {
		return "This request was just updated. Please click Approve or Deny again."
	}
	return "This request was just updated. Please run the command again."
}

// claimLost answers a decision that lost the claim on a channel or Channel
// Admin request. It never touches the cards: the decision that won the
// claim already did, and overwriting them would erase that outcome.
func claimLost(stillPending, clicked bool) decisionResult {
	if stillPending {
		return decisionResult{Reply: requestUpdatedText(clicked)}
	}
	return decisionResult{Reply: alreadyHandledText}
}

// decisionCards names the approval cards a decision resolves.
type decisionCards struct {
	requestID string
	// clickedPostID is the card that was clicked; "" for the slash command.
	clickedPostID string
	// cardPostID is the card the decision closes through closeTicket: the
	// clicked card, or else the stored approval-channel card ("" when the
	// request went to System Admin direct messages).
	cardPostID string
}

func newDecisionCards(requestID, clickedPostID, approvalPostID string) decisionCards {
	cardPostID := clickedPostID
	if cardPostID == "" {
		cardPostID = approvalPostID
	}
	return decisionCards{requestID: requestID, clickedPostID: clickedPostID, cardPostID: cardPostID}
}

// finish applies a resolved card: a click gets it back as its Update; with
// no click the stored card is updated in place. Every other recorded copy
// is resolved with outcome too.
func (p *Plugin) finish(c decisionCards, reply, outcome string, resolved *model.Post) decisionResult {
	result := decisionResult{Reply: reply, Outcome: outcome}
	if c.clickedPostID != "" {
		result.Update = resolved
	} else if resolved != nil && resolved.Id != "" {
		if _, appErr := p.API.UpdatePost(resolved); appErr != nil {
			p.API.LogWarn("failed to resolve approval card", "request_id", c.requestID, "post_id", resolved.Id, "error", appErr.Error())
		}
	}
	p.resolveApprovalCards(c.requestID, outcome, c.cardPostID)
	return result
}

// decideChannelRequest approves or denies a channel request on behalf of
// actingUserID. The approval card buttons and the slash command both come
// through here, so they make the same checks in the same order.
// clickedPostID is the card that was clicked, which the caller updates
// from the result; "" when there is none.
func (p *Plugin) decideChannelRequest(actingUserID, requestID, clickedPostID string, approve bool) decisionResult {
	actingUser, appErr := p.API.GetUser(actingUserID)
	if appErr != nil {
		return decisionResult{Reply: "Could not verify your identity to approve/deny."}
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

	// System Admin, primary ApprovalTeam Team Admin (opt-in), or Team Admin of
	// the team this channel will be created in (per-team approval cards).
	if !p.canApproveChannelRequest(actingUser, req.TeamID) {
		return decisionResult{Reply: "You don't have permission to approve or deny this channel request. Team Admins of this team or a System Admin can approve it."}
	}
	if approve && req.RequesterID == actingUser.Id && !actingUser.IsSystemAdmin() {
		return decisionResult{Reply: ownRequestText}
	}

	// Refresh lookups before claim so a failed register at submit still relays.
	p.refreshTicketThread(ticketThread{
		ApprovalPostID:    req.ApprovalPostID,
		ApprovalChannelID: req.ApprovalChannelID,
		DMRootPostID:      req.DMRootPostID,
		DMChannelID:       req.DMChannelID,
		Kind:              requestTypeChannel,
		ScopeID:           req.TeamID,
	})

	cards := newDecisionCards(req.ID, clickedPostID, req.ApprovalPostID)
	config := p.getConfiguration()
	// Best-effort for the welcome post and audit; approve checks requesterErr.
	requester, requesterErr := p.API.GetUser(req.RequesterID)

	if requestExpired(req.CreatedAt, config.ExpiryDays(), model.GetMillis()) {
		return p.expireRequest(cards, expiredRequest{
			kvKey:       kvRequestPrefix + req.ID,
			raw:         rawReq,
			kind:        pendingKindChannel,
			requestID:   req.ID,
			requesterID: req.RequesterID,
			dmRootID:    req.DMRootPostID,
			dmChannelID: req.DMChannelID,
			days:        config.ExpiryDays(),
			cardTitle:   exerciseLabel(req.Exercise) + "Channel Request Expired",
			cardText:    fmt.Sprintf("Your request for channel **%s** expired before it was reviewed. Submit it again if you still need it.", escapeMarkdown(req.DisplayName)),
			audit: exerciseLabel(req.Exercise) + fmt.Sprintf("@%s closed channel request \"%s\" from %s, which was not reviewed within %d days",
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
		still, _, loadErr := p.loadRequest(req.ID)
		return claimLost(loadErr != nil || still != nil, clickedPostID != "")
	}

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
				return p.finish(cards,
					fmt.Sprintf("Could not create the channel (%s), and the pending request could not be saved — ask the requester to submit it again.", createErr.Error()),
					lost, p.resolvedPost(cards.cardPostID, lost))
			}
			return decisionResult{Reply: fmt.Sprintf("Could not create the channel: %s. The request is still pending. If this keeps failing, the channel name may already be taken — deny it and ask the requester to resubmit with a different name.", createErr.Error())}
		}
		if req.Exercise {
			p.recordExerciseChannel(channel)
		}
		// The KV key was already removed by the atomic claim above.
		p.releasePending(pendingKindChannel, req.RequesterID, req.ID)
		p.addChannelToSidebarCategory(channel.Id, channel.TeamId, req)
		outcome := fmt.Sprintf("✅ Approved by @%s for %s. Channel ~%s created.", actingUser.Username, requesterUsername(requester, req.RequesterID), channel.Name)
		if requester != nil {
			p.postWelcomeMessage(channel, req, requester, actingUser)
		}
		p.logAudit(auditApproved, exerciseLabel(req.Exercise)+fmt.Sprintf("@%s approved channel request \"%s\" (~%s) from %s",
			actingUser.Username, escapeMarkdown(req.DisplayName), channel.Name, requesterUsername(requester, req.RequesterID)), cards.cardPostID)
		return p.finish(cards, "", outcome, p.closeTicket(ticketClose{
			RequesterID: req.RequesterID, DMRootPostID: req.DMRootPostID, DMChannelID: req.DMChannelID,
			ApprovalPostID: cards.cardPostID, Approved: true, ApproverUsername: actingUser.Username,
			CardTitle:  exerciseLabel(req.Exercise) + "Channel Request Approved",
			CardText:   fmt.Sprintf("Approved by @%s. Your request for channel **%s** is now available at ~%s.", actingUser.Username, escapeMarkdown(req.DisplayName), channel.Name),
			StatusLine: outcome,
		}))
	}

	p.releasePending(pendingKindChannel, req.RequesterID, req.ID)
	outcome := fmt.Sprintf("❌ Denied by @%s for %s. Channel request **%s** (`%s`).", actingUser.Username, requesterUsername(requester, req.RequesterID), escapeMarkdown(req.DisplayName), req.Name)
	p.logAudit(auditDenied, exerciseLabel(req.Exercise)+fmt.Sprintf("@%s denied channel request \"%s\" from %s",
		actingUser.Username, escapeMarkdown(req.DisplayName), requesterUsername(requester, req.RequesterID)), cards.cardPostID)
	return p.finish(cards, "", outcome, p.closeTicket(ticketClose{
		RequesterID: req.RequesterID, DMRootPostID: req.DMRootPostID, DMChannelID: req.DMChannelID,
		ApprovalPostID: cards.cardPostID, Approved: false, ApproverUsername: actingUser.Username,
		CardTitle:  exerciseLabel(req.Exercise) + "Channel Request Denied",
		CardText:   fmt.Sprintf("Denied by @%s. Your request for channel **%s** was not approved. Reply to this thread if you'd like more information.", actingUser.Username, escapeMarkdown(req.DisplayName)),
		StatusLine: outcome,
	}))
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
		return decisionResult{Reply: ownRequestText}
	}

	// Best-effort for notify and audit; approve checks requesterErr.
	requester, requesterErr := p.API.GetUser(req.RequesterID)
	channel, channelErr := p.API.GetChannel(req.ChannelID)

	channelRef := "the channel"
	if channelErr == nil {
		channelRef = "~" + channel.Name
	}
	nominees := p.mentionList(req.NomineeIDs)

	p.refreshTicketThread(ticketThread{
		ApprovalPostID:    req.ApprovalPostID,
		ApprovalChannelID: req.ApprovalChannelID,
		DMRootPostID:      req.DMRootPostID,
		DMChannelID:       req.DMChannelID,
		Kind:              requestTypeChannelAdmin,
		ScopeID:           req.ChannelID,
	})

	cards := newDecisionCards(req.ID, clickedPostID, req.ApprovalPostID)

	if days := p.getConfiguration().ExpiryDays(); requestExpired(req.CreatedAt, days, model.GetMillis()) {
		return p.expireRequest(cards, expiredRequest{
			kvKey:       kvAdminRequestPrefix + req.ID,
			raw:         rawReq,
			kind:        pendingKindAdmin,
			requestID:   req.ID,
			requesterID: req.RequesterID,
			dmRootID:    req.DMRootPostID,
			dmChannelID: req.DMChannelID,
			days:        days,
			cardTitle:   exerciseLabel(req.Exercise) + "Channel Admin Request Expired",
			cardText:    fmt.Sprintf("Your request to make %s Channel Admin in %s expired before it was reviewed. Submit it again if you still need it.", nominees, channelRef),
			audit: exerciseLabel(req.Exercise) + fmt.Sprintf("@%s closed the Channel Admin request for %s in %s from %s, which was not reviewed within %d days",
				actingUser.Username, nominees, channelRef, requesterUsername(requester, req.RequesterID), days),
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

	// Atomically claim the request before acting. Mirrors
	// decideChannelRequest.
	claimed, claimErr := p.API.KVCompareAndDelete(kvAdminRequestPrefix+req.ID, rawReq)
	if claimErr != nil {
		p.API.LogError("failed to claim channel-admin request", "error", claimErr.Error())
		return decisionResult{Reply: "Could not process that request."}
	}
	if !claimed {
		still, _, loadErr := p.loadAdminRequest(req.ID)
		return claimLost(loadErr != nil || still != nil, clickedPostID != "")
	}

	// The KV key was already removed by the atomic claim above.
	p.releasePending(pendingKindAdmin, req.RequesterID, req.ID)

	if approve {
		// Report only who was actually promoted; nominees who have since
		// left the channel are skipped.
		req.NomineeIDs = p.promoteChannelAdmins(req)
		promoted := p.mentionList(req.NomineeIDs)
		var outcome, cardText string
		if promoted == "" {
			outcome = fmt.Sprintf("⚠️ Approved by @%s for %s, but nobody was promoted: the nominees are no longer members of %s.", actingUser.Username, requesterUsername(requester, req.RequesterID), channelRef)
			cardText = fmt.Sprintf("Approved by @%s, but nobody was promoted: %s are no longer members of %s.", actingUser.Username, nominees, channelRef)
			p.logAudit(auditApproved, exerciseLabel(req.Exercise)+fmt.Sprintf("@%s approved %s for Channel Admin in %s, but none were still members (requested by %s)",
				actingUser.Username, nominees, channelRef, requesterUsername(requester, req.RequesterID)), cards.cardPostID)
		} else {
			p.postAdminPromotionMessage(channel, req, actingUser)
			outcome = fmt.Sprintf("✅ Approved by @%s for %s. %s promoted to Channel Admin in %s.", actingUser.Username, requesterUsername(requester, req.RequesterID), promoted, channelRef)
			cardText = fmt.Sprintf("Approved by @%s. %s is now Channel Admin in %s.", actingUser.Username, promoted, channelRef)
			p.logAudit(auditApproved, exerciseLabel(req.Exercise)+fmt.Sprintf("@%s promoted %s to Channel Admin in %s (requested by %s)",
				actingUser.Username, promoted, channelRef, requesterUsername(requester, req.RequesterID)), cards.cardPostID)
		}
		return p.finish(cards, "", outcome, p.closeTicket(ticketClose{
			RequesterID: req.RequesterID, DMRootPostID: req.DMRootPostID, DMChannelID: req.DMChannelID,
			ApprovalPostID: cards.cardPostID, Approved: true, ApproverUsername: actingUser.Username,
			CardTitle:  exerciseLabel(req.Exercise) + "Channel Admin Request Approved",
			CardText:   cardText,
			StatusLine: outcome,
		}))
	}

	outcome := fmt.Sprintf("❌ Denied by @%s for %s. Channel Admin for %s in %s.", actingUser.Username, requesterUsername(requester, req.RequesterID), nominees, channelRef)
	p.logAudit(auditDenied, exerciseLabel(req.Exercise)+fmt.Sprintf("@%s denied a Channel Admin request for %s in %s",
		actingUser.Username, nominees, channelRef), cards.cardPostID)
	return p.finish(cards, "", outcome, p.closeTicket(ticketClose{
		RequesterID: req.RequesterID, DMRootPostID: req.DMRootPostID, DMChannelID: req.DMChannelID,
		ApprovalPostID: cards.cardPostID, Approved: false, ApproverUsername: actingUser.Username,
		CardTitle:  exerciseLabel(req.Exercise) + "Channel Admin Request Denied",
		CardText:   fmt.Sprintf("Denied by @%s. Your request to make %s Channel Admin in %s was not approved. Reply to this thread if you'd like more information.", actingUser.Username, nominees, channelRef),
		StatusLine: outcome,
	}))
}

// expiredRequest describes a pending channel or Channel Admin request
// that was approved or denied after it expired.
type expiredRequest struct {
	kvKey       string
	raw         []byte
	kind        string // pendingKind*
	requestID   string
	requesterID string
	dmRootID    string // requester's ticket DM root
	dmChannelID string
	days        int
	cardTitle   string // requester's ticket card
	cardText    string
	audit       string // audit-channel line
}

// expireRequest closes an expired request: it claims the record like an
// approve or deny would, closes the requester's ticket as expired, and
// marks the approval cards expired.
func (p *Plugin) expireRequest(cards decisionCards, e expiredRequest) decisionResult {
	claimed, claimErr := p.API.KVCompareAndDelete(e.kvKey, e.raw)
	if claimErr != nil {
		p.API.LogError("failed to claim expired request", "error", claimErr.Error())
		return decisionResult{Reply: "Could not process that request."}
	}
	if !claimed {
		still, loadErr := p.API.KVGet(e.kvKey)
		return claimLost(loadErr != nil || still != nil, cards.clickedPostID != "")
	}
	p.releasePending(e.kind, e.requesterID, e.requestID)
	p.logAudit(auditExpired, e.audit, cards.cardPostID)
	outcome := fmt.Sprintf("⌛ Expired: not reviewed within %d days.", e.days)
	return p.finish(cards, expiredText, outcome, p.closeTicket(ticketClose{
		RequesterID: e.requesterID, DMRootPostID: e.dmRootID, DMChannelID: e.dmChannelID,
		ApprovalPostID: cards.cardPostID, Expired: true,
		CardTitle:   e.cardTitle,
		CardText:    e.cardText,
		ThreadReply: fmt.Sprintf("⌛ **Expired**: not reviewed within %d days.", e.days),
		StatusLine:  outcome,
	}))
}

// saveApprovalPosts records the direct-message copies of a request's
// approval card. A failure is logged: the copies then update only when
// clicked.
func (p *Plugin) saveApprovalPosts(kind, requestID string, postIDs []string) {
	if len(postIDs) == 0 {
		return
	}
	data, _ := json.Marshal(postIDs) // a []string always marshals
	if appErr := p.API.KVSet(kvApprovalPostsPrefix+requestID, data); appErr != nil {
		p.API.LogWarn("failed to record approval posts", "request_id", requestID, "error", appErr.Error())
		return
	}
	// An approver can act on the first copy while the rest are still being
	// posted, before they were recorded here. If the request is already
	// gone, resolve the copies now, since nothing else will.
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
// caller resolves itself. Failures are logged; a card left behind answers
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
		if _, appErr := p.API.UpdatePost(p.resolvedCard(post, outcome)); appErr != nil {
			p.API.LogWarn("failed to resolve approval card", "request_id", requestID, "post_id", postID, "error", appErr.Error())
		}
	}
	p.deleteApprovalPosts(requestID)
}
