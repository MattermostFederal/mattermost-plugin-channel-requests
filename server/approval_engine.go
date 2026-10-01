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
//   - A user may fill only a step they qualify for that isn't already done.
//   - The two approvals must come from two DISTINCT users, so a user who already
//     approved one step can't fill the other.
//   - When a user qualifies for both outstanding steps, they fill security first;
//     the system step still needs a different, qualified user.
func planApproval(state twoStepState, canSecurity, canSystem bool, userID string) (step approverStep, fill bool, reason string) {
	if state.complete() {
		return 0, false, "This request has already been fully approved."
	}

	// A user can't fill a step if they already approved the other one.
	canFillSecurity := !state.securityDone() && canSecurity && state.SystemApproverID != userID
	canFillSystem := !state.systemDone() && canSystem && state.SecurityApproverID != userID

	if canFillSecurity {
		return stepSecurity, true, ""
	}
	if canFillSystem {
		return stepSystem, true, ""
	}

	// Nothing to fill — explain why.
	if state.approverID(stepSecurity) == userID || state.approverID(stepSystem) == userID {
		return 0, false, "You've already approved your step. This request needs a different approver to complete it."
	}
	if !canSecurity && !canSystem {
		return 0, false, "You don't have permission to approve this request."
	}
	// Qualifies for a step, but it's already done and they can't fill the
	// outstanding one.
	outstanding := stepSystem
	if !state.securityDone() {
		outstanding = stepSecurity
	}
	return 0, false, fmt.Sprintf("This request now needs a %s approver, which you're not eligible for.", outstanding)
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
