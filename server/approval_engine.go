package main

import (
	"fmt"
)

// twoStepState is the approval state carried by requests that need two
// independent approvals — one from the security pool and one from the system
// pool — before they're actioned. Embedded in webhookRequest and botTokenRequest.
// A step is "done" once its approver ID is recorded.
type twoStepState struct {
	SecurityApproverID string `json:"security_approver_id,omitempty"`
	SecurityApprovedAt int64  `json:"security_approved_at,omitempty"`
	SystemApproverID   string `json:"system_approver_id,omitempty"`
	SystemApprovedAt   int64  `json:"system_approved_at,omitempty"`
}

func (s twoStepState) securityDone() bool { return s.SecurityApproverID != "" }
func (s twoStepState) systemDone() bool   { return s.SystemApproverID != "" }
func (s twoStepState) complete() bool     { return s.securityDone() && s.systemDone() }

// approverID returns who approved the given step, or "".
func (s twoStepState) approverID(step approverStep) string {
	if step == stepSecurity {
		return s.SecurityApproverID
	}
	return s.SystemApproverID
}

// withApproval returns a copy of the state with the given step recorded as
// approved by userID at the given time.
func (s twoStepState) withApproval(step approverStep, userID string, atMillis int64) twoStepState {
	if step == stepSecurity {
		s.SecurityApproverID = userID
		s.SecurityApprovedAt = atMillis
	} else {
		s.SystemApproverID = userID
		s.SystemApprovedAt = atMillis
	}
	return s
}

// planApproval decides what a click on Approve does, given the current state and
// which pools the clicking user qualifies for. It returns the step the click
// fills (when fill is true), or a user-facing reason it can't (when fill is
// false). Rules:
//   - Security must be filled before system (ordered two-step review).
//   - A user may fill only a step they qualify for that isn't already done.
//   - The two approvals must come from two DISTINCT users, so a user who already
//     approved one step can't fill the other.
//   - Dual-role users (security + system): first click fills security; a
//     different dual-role (or system-only) user fills system next.
func planApproval(state twoStepState, canSecurity, canSystem bool, userID string) (step approverStep, fill bool, reason string) {
	if state.complete() {
		return 0, false, "This request has already been fully approved."
	}

	// Same person cannot fill both steps.
	if state.approverID(stepSecurity) == userID || state.approverID(stepSystem) == userID {
		return 0, false, "You've already approved your step. This request needs a different approver to complete it."
	}

	if !state.securityDone() {
		if canSecurity {
			return stepSecurity, true, ""
		}
		if canSystem {
			return 0, false, "This request needs a security approver first. After security signs off, a system approver can complete it."
		}
		return 0, false, "You don't have permission to approve this request."
	}

	// Security done — only the system step remains, and it must be a different user.
	if !state.systemDone() {
		if canSystem {
			return stepSystem, true, ""
		}
		if canSecurity {
			return 0, false, "Security already approved. This request now needs a system approver (a different person)."
		}
		return 0, false, "You don't have permission to approve this request."
	}

	return 0, false, "This request has already been fully approved."
}

// twoStepStatusValue renders the per-step approval status for an approval card,
// e.g. "🔒 Security: ✅ approved by @maria\n🛠 System: ⏳ awaiting approval".
func (p *Plugin) twoStepStatusValue(state twoStepState) string {
	return fmt.Sprintf("🔒 Security: %s\n🛠 System: %s",
		p.stepStatusLine(state, stepSecurity),
		p.stepStatusLine(state, stepSystem),
	)
}

func (p *Plugin) stepStatusLine(state twoStepState, step approverStep) string {
	id := state.approverID(step)
	if id == "" {
		return "⏳ awaiting approval"
	}
	username := id
	if user, appErr := p.API.GetUser(id); appErr == nil {
		username = user.Username
	}
	return fmt.Sprintf("✅ approved by @%s", username)
}

// approveButtonLabel is the label for the single Approve button on a two-step
// card. It's deliberately just "Approve" (not "Approve (security)" etc.): the
// card's "Approvals" status field already shows which step each person filled,
// and the button is shared by every viewer of the post, so a step-specific
// label would be misleading for whoever isn't eligible for that step. The
// server still routes each click to the correct outstanding step via
// planApproval.
const approveButtonLabel = "Approve"
