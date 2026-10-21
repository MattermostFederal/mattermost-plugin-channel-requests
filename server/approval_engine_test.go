package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTwoStepState_DoneAndComplete(t *testing.T) {
	var s twoStepState
	require.False(t, s.securityDone())
	require.False(t, s.systemDone())
	require.False(t, s.complete())

	s = s.withApproval(stepSecurity, "u_sec", 100)
	require.True(t, s.securityDone())
	require.False(t, s.complete())
	require.Equal(t, "u_sec", s.approverID(stepSecurity))

	s = s.withApproval(stepSystem, "u_sys", 200)
	require.True(t, s.complete())
	require.Equal(t, "u_sys", s.approverID(stepSystem))
}

func TestPlanApproval(t *testing.T) {
	t.Run("fresh request, security-only approver fills security", func(t *testing.T) {
		step, fill, _ := planApproval(twoStepState{}, true, false, "u1", "u_req")
		require.True(t, fill)
		require.Equal(t, stepSecurity, step)
	})

	t.Run("fresh request, system-only must wait for security first", func(t *testing.T) {
		_, fill, reason := planApproval(twoStepState{}, false, true, "u1", "u_req")
		require.False(t, fill)
		require.Contains(t, reason, "Security review must happen first")
	})

	t.Run("dual-role on a fresh request fills security first", func(t *testing.T) {
		step, fill, _ := planApproval(twoStepState{}, true, true, "u1", "u_req")
		require.True(t, fill)
		require.Equal(t, stepSecurity, step)
	})

	t.Run("second distinct system-only approver completes the system step", func(t *testing.T) {
		state := twoStepState{SecurityApproverID: "u_sec", SecurityApprovedAt: 1}
		step, fill, _ := planApproval(state, false, true, "u_sys", "u_req")
		require.True(t, fill)
		require.Equal(t, stepSystem, step)
	})

	t.Run("second dual-role approver takes system after security", func(t *testing.T) {
		// Both users are security+system eligible; first took security, second
		// must take system (not block as "security already done").
		state := twoStepState{SecurityApproverID: "u_sec", SecurityApprovedAt: 1}
		step, fill, _ := planApproval(state, true, true, "u_dual", "u_req")
		require.True(t, fill)
		require.Equal(t, stepSystem, step)
	})

	t.Run("same user cannot fill both steps", func(t *testing.T) {
		state := twoStepState{SecurityApproverID: "u1", SecurityApprovedAt: 1}
		_, fill, reason := planApproval(state, true, true, "u1", "u_req")
		require.False(t, fill)
		require.Contains(t, reason, "already approved")
	})

	t.Run("requester cannot approve their own request", func(t *testing.T) {
		_, fill, reason := planApproval(twoStepState{}, true, true, "u_req", "u_req")
		require.False(t, fill)
		require.Contains(t, reason, "cannot approve it")
	})

	t.Run("requester cannot approve system step either", func(t *testing.T) {
		state := twoStepState{SecurityApproverID: "u_sec", SecurityApprovedAt: 1}
		_, fill, reason := planApproval(state, true, true, "u_req", "u_req")
		require.False(t, fill)
		require.Contains(t, reason, "cannot approve it")
	})

	t.Run("non-approver is rejected", func(t *testing.T) {
		_, fill, reason := planApproval(twoStepState{}, false, false, "u1", "u_req")
		require.False(t, fill)
		require.Contains(t, reason, "permission")
	})

	t.Run("security-only cannot take system after security is done", func(t *testing.T) {
		state := twoStepState{SecurityApproverID: "u_other", SecurityApprovedAt: 1}
		_, fill, reason := planApproval(state, true, false, "u1", "u_req")
		require.False(t, fill)
		require.Contains(t, reason, "system approver")
	})

	t.Run("already complete", func(t *testing.T) {
		state := twoStepState{SecurityApproverID: "a", SystemApproverID: "b"}
		_, fill, reason := planApproval(state, true, true, "c", "u_req")
		require.False(t, fill)
		require.Contains(t, reason, "already been fully approved")
	})
}

func TestApproveButtonLabel(t *testing.T) {
	// The Approve button is always just "Approve" — the per-step status lives
	// in the card's Approvals field, and the button is shared by all viewers.
	require.Equal(t, "Approve", approveButtonLabel)
}

func TestTwoStepApproveBlock(t *testing.T) {
	t.Run("self-approve paints card and system ephemeral", func(t *testing.T) {
		title, card, eph, paint := twoStepApproveBlock("alice", "You created this request — you cannot approve it. Another security or system approver must review this ticket.")
		require.True(t, paint)
		require.Contains(t, title, "Requester cannot approve")
		require.Contains(t, card, "@alice")
		require.Contains(t, eph, "You created this request — you cannot approve it")
		require.Contains(t, eph, "ticket card now shows that reminder")
	})
	t.Run("security first paints card and system ephemeral", func(t *testing.T) {
		title, card, eph, paint := twoStepApproveBlock("bob", "Security review must happen first. A security approver has to click Approve before a system approver can finish this ticket.")
		require.True(t, paint)
		require.Contains(t, title, "Security review required first")
		require.Contains(t, card, "@bob")
		require.Contains(t, eph, "Security review required first")
		require.Contains(t, eph, "ticket card now shows that reminder")
	})
	t.Run("other reasons do not paint card", func(t *testing.T) {
		_, _, eph, paint := twoStepApproveBlock("bob", "You don't have permission to approve this request.")
		require.False(t, paint)
		require.Contains(t, eph, "permission")
	})
}
