# Mattermost Requests Branch QA Report

Branch: `kyledoliente_requests_plugin_additions` (vs `origin/main`)
Reviewer: senior QA / adversarial engineering pass
Method: branch diff review + executed Go/webapp suites + **live end-to-end exercise against a real Mattermost server** (driving the real plugin HTTP endpoints as three real users and the real Approve/Deny action API) + targeted failure-injection analysis and one reproduced failing regression test.

## Executive Summary

The branch adds a general `/request` command (`channel`, `team`, `team-admin`, `webhook`, `bot-token`) with the legacy `/channel-request` alias, per-type enable toggles, team-creation requests (+ React modal), Team Admin promotion requests, and a **two-step (security + system) approval engine** for incoming-webhook and bot-token requests, all backed by KV-persisted pending state and approval cards.

**The core approval machinery is sound.** The two-step engine enforces two distinct approvers, works in both approval orders, and is race-safe for the single privileged side effect (verified live with delta assertions, and by reading the `KVCompareAndSet`/`KVCompareAndDelete` claim logic). Secrets (webhook URLs, bot tokens) are never persisted, logged, or posted to the approval/audit channels — only DM'd to the requester (verified live + code audit). Feature toggles are enforced server-side on every *submission* path. The Go suite, webapp `tsc`, lint, and production build all pass.

**However, the failure-recovery and partial-success paths are not production-ready.** The most serious issues are all in the "what happens after the irreversible side effect" space the happy-path tests never touch:

- **A bot-token request can orphan a privileged bot account and become permanently unrecoverable** if token issuance fails after bot creation (High).
- **A freshly minted secret (bot token / webhook URL) can be irretrievably lost** if the DM delivery fails, because the request is already resolved (High).
- **Team Admin promotion reports complete success — including in the audit log — even when some or all promotions failed** (High), with no retry and no durable record of partial completion.
- Team creation tells the requester they are a Team Admin even if that promotion failed (Medium).
- Channel-admin requests have **no feature toggle** and no server-side enable gate (Medium).
- Webhook creation can **duplicate** on an ambiguous-success retry (Medium).

Recommendation: **not ready for production** until the High items are fixed and the partial-success reporting is made truthful.

## Commands Executed

| Command | Result |
|---|---|
| `git log --oneline origin/main..HEAD` | 5 feature commits (toggles, `/request`, team requests, team modal, team-admin + two-step engine) + uncommitted hardening changes in working tree |
| `git diff --stat origin/main...HEAD` | 25 files, +3929/−35 |
| `go test ./...` | **PASS** (server + build/pluginctl) |
| `go test ./server/ -run Test_BUG_OrphanBotOnTokenFailure` (skip removed) | **FAIL as designed** — reproduces the orphan-bot bug (`DeleteBot` never called) |
| `go test ./server/` (skip restored) | **PASS** (suite green) |
| webapp `npx tsc` | **PASS** (exit 0) |
| webapp `npx eslint --quiet .` | **PASS** (no errors) |
| webapp `npx webpack --mode=production` | **PASS** (compiled successfully) |
| webapp `playwright` (CT + E2E) | **NOT RUN** (require running server/browser) |
| Live E2E vs real server (earlier this session) | 40 assertions incl. two-step delta/order checks — all passed; see Concurrency & Failure-Recovery sections |

## Request-Type Matrix

| Type | Submitter | Bypass | Approver | Approval Count | Side Effect | Result |
|---|---|---|---|---|---|---|
| Channel | Team member (server-verified active membership) | System Admin + auto-approve | `canApprove` (System Admin, or approval-team Team Admin if `AllowTeamAdminApprovers`) | 1 | Create channel, add members, promote channel admins (member list trimmed to actual adds) | Correct; partial member-add handled truthfully |
| Channel Admin | Channel member | System Admin + auto-approve | `canApproveAdminRequest` (global + channel's team admins) | 1 | Promote nominees to channel_admin | **No feature toggle** (Bug 5) |
| Team | Any logged-in user | System Admin + auto-approve | `canApprove` | 1 | Create team, add requester as Team Admin, add members | Partial-failure overclaim (Bug 4) |
| Team Admin | Team member (active) | System Admin + auto-approve | global approvers + target team's Team Admins | 1 | Add + promote each nominee to team_admin | **Partial-failure overclaim incl. audit** (Bug 3) |
| Webhook | Channel member | System Admin only (no auto-approve) | security pool **and** system pool, 2 distinct users | 2 | REST create incoming webhook; DM URL to requester | Secret-delivery + duplicate risks (Bugs 2, 6) |
| Bot token | Any logged-in user | System Admin only (no auto-approve) | security pool **and** system pool, 2 distinct users | 2 | CreateBot + CreateUserAccessToken; DM token to requester | **Orphan/unrecoverable + secret-delivery** (Bugs 1, 2) |

Security step is gated purely on the security attribute — **System Admins do not satisfy it** (`approvers.go` `canApproveStep`: System Admin auto-qualifies only for the *system* step). Verified live.

## Confirmed Bugs

### [High] Bot-token request orphans a privileged bot and becomes permanently unrecoverable when token issuance fails after bot creation

**Files:** `server/bot_token_request.go`, `server/http.go`
**Functions:** `createBotTokenForRequest` (bot_token_request.go ~117–140), `handleBotTokenAction` completion branch (http.go ~1404–1448)
**Reproduction:**
1. Approve a bot-token request to the final step.
2. `CreateBot` succeeds; `CreateUserAccessToken` fails (token service error, rate limit, policy).
3. `createBotTokenForRequest` returns an error with the bot already created.
4. The handler restores the pending request to KV "so the final approval can be retried."
5. Approver retries → `CreateBot` now fails with *username already taken* (the orphan owns it) → same error forever.

**Expected:** Either the orphaned bot is deleted on token-issuance failure so a retry is clean, or creation is idempotent (reuse the existing bot and only issue the token on retry).
**Actual:** No cleanup. A privileged bot account persists with **no token anyone holds**; the request is stuck pending and the only escape is Deny + resubmit under a *different* username, plus manual bot deletion by an admin. The ephemeral even hints at the symptom ("The username may be taken").
**Impact:** Unrecoverable privileged request + leaked bot account. Exactly the "fails immediately after the irreversible side effect" case.
**Evidence:** Reproduced by `Test_BUG_OrphanBotOnTokenFailure` (server/qa_regression_test.go) — running it with the `t.Skip` removed fails because `DeleteBot` is never called:
```
Expected "DeleteBot" to have been called with: [orphan-bot-id]
but actual calls were: [CreateBot, CreateUserAccessToken]
--- FAIL: Test_BUG_OrphanBotOnTokenFailure
```
**Recommended fix:** In `createBotTokenForRequest`, on `CreateUserAccessToken` failure, call `p.API.DeleteBot(bot.UserId)` before returning the error (best-effort, logged). Preferred: make it idempotent — if a bot with the username already exists and is owned by the plugin, reuse it and just issue the token, so retries converge.
**Regression test:** `Test_BUG_OrphanBotOnTokenFailure` (added, Skip-guarded; remove Skip on fix).

### [High] A freshly issued secret can be irretrievably lost when DM delivery fails

**Files:** `server/request.go`, `server/bot_token_request.go`, `server/webhook_request.go`, `server/http.go`
**Functions:** `notifyRequester` (request.go ~580–593), `deliverBotToken` / `deliverWebhookURL`, completion branches of `handleBotTokenAction` / `handleWebhookAction`
**Reproduction:**
1. Final approval completes. The handler **claims (deletes) the KV request** (`KVCompareAndDelete`), creates the side effect, then calls `deliverBotToken`/`deliverWebhookURL`, then posts "✅ created."
2. `notifyRequester` fails to open the DM or post (requester deactivated, DM restriction, transient error).
3. `notifyRequester` swallows the failure (`LogWarn`) and returns; the request is already gone and the card already says success.

**Expected:** If the only copy of a secret cannot be delivered, that must be a loud, recoverable failure (retry delivery, or invalidate/rotate the secret, or tell an admin), not a silent drop.
**Actual:** The bot token — shown with "Store this now — it won't be shown again" — is lost; the bot exists with a token nobody has. The webhook URL is lost; the hook is orphaned. No retry path, because the request is resolved.
**Impact:** Secret created but undeliverable → orphaned privileged credential + confused requester told (on the card) it succeeded.
**Evidence:** `notifyRequester` returns no error and logs-and-continues on both `GetDirectChannel` and `CreatePost` failure; the completion path calls it *after* `KVCompareAndDelete` and the create.
**Recommended fix:** Have `notifyRequester` (or a delivery-specific variant) return an error; on delivery failure in the completion path, surface it on the card/ephemeral ("Created but could not DM you — contact an admin"), and for bot tokens delete/rotate the undelivered token. Consider delivering before declaring success on the card.
**Regression test:** (specified in Missing Tests) handler test: completion with `CreatePost`/`GetDirectChannel` failing → assert the response signals delivery failure and does not claim unqualified success.

### [High] Team Admin promotion reports full success — including in the audit log — even on partial/total failure

**Files:** `server/team_admin_request.go`, `server/http.go`
**Functions:** `promoteTeamAdmins` (team_admin_request.go ~102–116), `submitTeamAdminRequest` bypass path (~80–84), `handleTeamAdminAction` outcome (http.go ~977–991)
**Reproduction:**
1. Request promotes nominees A and B. Approve it (or submit as System Admin / auto-approve).
2. `promoteTeamAdmins` loops nominees; `CreateTeamMember`/`UpdateTeamMemberRoles` for B fails (deactivated user, concurrent role change) — logged via `LogWarn`, loop continues.
3. The request was already claimed/deleted. The card says "@A @B promoted to Team Admin," the requester DM says approved, and the audit log records "promoted @A @B to Team Admin."

**Expected:** Status and audit must reflect only who was actually promoted and flag failures; a privileged-grant workflow must not claim grants that did not happen.
**Actual:** `promoteTeamAdmins` returns nothing; all user-facing messaging uses the full nominee list unconditionally. No retry (request gone), no durable record of partial completion.
**Impact:** False audit trail for privilege grants + silent partial completion of an admin operation. (Compare `createChannelForRequest`, which *does* trim to actual adds via `filterIDs` — the correct pattern.)
**Evidence:** `promoteTeamAdmins` has `LogWarn` on each API error and no return value; outcome strings use `p.mentionList(req.NomineeIDs)`.
**Recommended fix:** Return `(promoted, failed []string)` from `promoteTeamAdmins`; build the outcome/audit/notification from `promoted`, and surface `failed` explicitly.
**Regression test:** (specified) inject `UpdateTeamMemberRoles` failure for one of two nominees; assert outcome/audit name only the succeeded nominee.

### [Medium] Team creation tells the requester they are a Team Admin even when that promotion failed

**Files:** `server/team_request.go`, `server/http.go`
**Functions:** `createTeamForRequest` (team_request.go ~168–201), `handleTeamAction` approve branch (http.go ~783–786)
**Reproduction:** Approve a team request; `CreateTeam` succeeds but the requester's `UpdateTeamMemberRoles` (Team Admin promotion) or member adds fail (logged, non-fatal). The card says created and the requester DM says "You're now a Team Admin of it."
**Expected:** Report actual membership/admin outcome; don't claim admin rights that weren't granted.
**Actual:** Requester may be a plain member (or not added) while being told they're Team Admin; added-member failures aren't reflected (unlike the channel flow).
**Impact:** Misleading success for a privileged setup step; a team with no working admin is possible.
**Evidence:** per-user failures are `LogWarn`-only; notification string is unconditional.
**Recommended fix:** Mirror the channel flow — verify the requester promotion and trim/report member adds to what actually happened.
**Regression test:** (specified) `CreateTeam` ok but requester promotion fails → assert notification doesn't assert Team Admin.

### [Medium] Channel-admin requests have no feature toggle and no server-side enable gate

**Files:** `server/http.go`, `server/configuration.go`, `webapp/src/index.tsx`
**Functions:** `handleRequestAdmin` (http.go ~1500), `RequestEnabled` (configuration.go), webapp menu registration (index.tsx ~92–101)
**Reproduction:** There is no `AllowChannelAdminRequests` config field, no `RequestEnabled` case, and `handleRequestAdmin` performs no toggle check. The webapp "Request Channel Admin" entry point is registered unconditionally.
**Expected:** Consistent with the per-type-toggle design, admins should be able to disable channel-admin requests (or it should be documented as intentionally always-on).
**Actual:** Channel-admin requests can never be turned off; the entry point is always present.
**Impact:** An admin who disables all request features still exposes channel-admin requests. Inconsistency, not a security bypass (authorization on approval still applies).
**Evidence:** Audited across configuration.go, http.go, index.tsx — no gate exists.
**Recommended fix:** Add an `AllowChannelAdminRequests` toggle + `RequestEnabled(requestTypeChannelAdmin)` gate in `handleRequestAdmin`, and gate the webapp menu item; or document the intentional exception.
**Regression test:** (specified) disabled toggle → `handleRequestAdmin` returns disabled error and does not store a request.

### [Medium] Webhook creation can duplicate on an ambiguous-success retry

**Files:** `server/webhook_request.go`, `server/http.go`
**Functions:** `createIncomingWebhookForRequest` (webhook_request.go), `handleWebhookAction` completion/restore (http.go ~1169–1191)
**Reproduction:** Final approval claims the request, calls the REST `CreateIncomingWebhook`. The server creates the hook but the client observes a timeout/error. The handler restores the request; the approver retries → a **second** incoming webhook is created for the same channel.
**Expected:** Retry after ambiguous success should not create a duplicate privileged side effect (idempotency key, or pre-create existence check).
**Actual:** No idempotency; duplicate hooks are possible. (Bot-token does not duplicate in this scenario — the retry hits the taken username — but that is Bug 1 instead.)
**Impact:** Duplicate secret-bearing webhook; only one URL is delivered, the other is an orphan.
**Evidence:** Restore-on-error path unconditionally allows re-execution with no de-dup.
**Recommended fix:** Before creating, check for an existing hook matching (channel, display name) created by the bot; or persist an "execution attempted" marker and require manual confirmation on retry. At minimum, document the risk.
**Regression test:** Hard to unit test deterministically; document + manual network-fault test.

### [Low] Documentation says the two-step Approve button is step-labeled; code labels it just "Approve"

**Files:** `public/help/approvals.html` (~line 86), `server/approval_engine.go` (`approveButtonLabel`), used in `webhook_request.go` / `bot_token_request.go`
**Expected/Actual:** Docs describe the button as indicating which step it fills (e.g. "Approve (security)"); the implementation intentionally shows a shared "Approve" (per-step status is in the card's Approvals field). Doc is stale.
**Impact:** Minor user confusion.
**Recommended fix:** Update approvals.html to describe the single "Approve" button + the Approvals status field.

### [Low] `handleConfig` is inconsistent about which toggles it exposes (benign)

**Files:** `server/http.go` (`handleConfig` ~313–323), `webapp/src/client.ts`
**Detail:** `handleConfig` returns `{channel, team, webhook}`. The webapp only has header buttons for channel and team; `webhook`/`team_admin`/`bot_token` are slash-command-only. So `webhook` is sent but unused, and `team_admin`/`bot_token` are omitted. No functional impact (slash paths gate server-side), but the set is inconsistent.
**Recommended fix:** Return all five toggles (cheap, future-proofs any UI), or trim to exactly the two the client uses. Not blocking.

## Concurrency Results

Approval claim logic uses optimistic concurrency: the final (completing) step does `KVCompareAndDelete(key, rawReq)` **before** executing the side effect; partial steps use `KVCompareAndSet(key, rawReq, new)`. Both read the exact bytes they CAS against, so a stale/raced write loses the CAS and the loser gets "already handled" / "just updated — try again."

| Scenario | Result | How verified |
|---|---|---|
| Two completing approvers race on final step | Only one wins `KVCompareAndDelete`; single side effect; other sees "already handled" | Code (http.go) + single-side-effect observed live |
| Two first-approvers race (e.g. two system approvers) | One wins `KVCompareAndSet`; other told to retry; no overwrite, no lost-but-unrecoverable approval | Code |
| Security + system click simultaneously on fresh request | CAS serializes; one step persists, loser retries and fills the other; completes | Code |
| Same user double-clicks / dual-qualified user clicks twice | `planApproval` blocks filling a step the user already filled (distinct-approver rule) | **Live** (webhook: system approved, system clicked again → no creation; delta 0) |
| Stale card clicked after resolution | Request gone → "already handled" (resolved post) | Code |
| Approve vs Deny race | Deny also claims via `KVCompareAndDelete`; one wins | Code |
| No premature creation after 1 approval; completes only after 2 distinct | **delta 0 after 1st, delta 1 after 2nd**, both orders | **Live** (webhook + bot-token delta tests) |

Not executed as a true parallel stress test (would need a concurrency harness against the store); correctness argued from the CAS code and confirmed by live single-side-effect delta assertions. Marked NOT TESTED where only reasoned.

## Failure-Recovery Results

| Operation | Partial-failure behavior | Verdict |
|---|---|---|
| **Channel creation** | Member/admin add failures are trimmed to actual via `filterIDs`; welcome message truthful | **Correct** |
| **Team creation** | `CreateTeam` ok but requester-promotion/member-add failures are `LogWarn`-only; requester told "you're now Team Admin" regardless | **Bug 4 (Medium)** |
| **Team Admin promotion** | Per-nominee failures logged; card/DM/audit claim all promoted; request already resolved → no retry | **Bug 3 (High)** |
| **Webhook creation** | On failure: KV restored, card repainted with warning, retryable (good). But ambiguous success → duplicate; secret-delivery failure → URL lost | **Bugs 6, 2** |
| **Bot creation / token issuance** | Bot-first ordering; token failure orphans bot + bricks request; delivery failure loses token | **Bugs 1, 2 (High)** |
| **Secret delivery** | `notifyRequester` swallows DM errors after request resolved | **Bug 2 (High)** |
| **Approval-card repaint failure** | `repaintedApprovalPost` falls back to a bare message if `GetPost` fails — non-fatal | OK |
| **Post-approval-card failure at submit** | Submit paths `KVDelete` the stored request on post failure (no orphan pending) | **Correct** |

## Security Review

- **Authorization / identity:** Every action handler derives identity from `requireUserID` (the `Mattermost-User-Id` header set by the server), never from a body field. Approval `Context` carries only `request_id` (what), never who. Cross-type confusion is prevented by per-prefix `load*Request` (a foreign id returns nil → "already handled"). `handleBotTokenAction` non-approver rejection is covered by an executed unit test (`TestHandleBotTokenAction_NonApproverRejected`, passing). **Strong.** (Negative authorization not re-tested live this pass — see checklist.)
- **Security-step integrity:** System Admins do **not** satisfy the security step (`canApproveStep` grants the system step to admins only). Verified live: a request completed only once the `security_approver` attribute holder signed off, in both orders.
- **Secret handling:** Webhook URLs and bot tokens are never stored in the KV struct, never logged, never placed in approval/audit posts or ephemeral responses; delivered only via DM to the requester. Verified by full code audit + live checks (URL DM'd to requester; `/hooks/` never appears in the approval channel). The one gap is **delivery-failure handling** (Bug 2), not confidentiality.
- **Feature-gate enforcement:** All five toggled types gate every submission path (slash + dialog + webapp) server-side. Channel-admin is the exception (Bug 5). Approval handlers intentionally do **not** re-check toggles (in-flight requests remain actionable after a disable) — reasonable but undocumented.
- **Bot privilege:** Webhook creation auto-promotes the plugin bot to **Team Admin** on the target team (to obtain `manage_own_incoming_webhooks`). Functionally necessary but a real standing privilege grant and **undocumented**; worth a security note.

## Documentation Mismatches

| Claim | Docs | Code | Severity |
|---|---|---|---|
| Two-step Approve button label | approvals.html ~86 says it shows the step ("Approve (security)") | `approveButtonLabel = "Approve"` (shared button) | Low |
| Bot becomes Team Admin on webhook team | Not mentioned anywhere | `createIncomingWebhookForRequest` promotes bot to team_admin | Low→Medium (security-relevant) |
| Auto-approve applies to two-step | README/admin.html correct ("not for two-step"); approvals.html uses channel-only wording | Code: auto-approve NOT honored for webhook/bot-token | Low |
| Retry-after-failure behavior | Undocumented | Cards repaint with a retry notice | Low |

## Missing Tests

High-value automated tests that should exist (most target the integration path, not helpers):

1. **Bot-token orphan on token failure** — `createBotTokenForRequest` with `CreateBot` ok + `CreateUserAccessToken` fail → assert bot cleanup / idempotent retry. (Added, Skip-guarded: `Test_BUG_OrphanBotOnTokenFailure`.)
2. **Secret-delivery failure** — `handleWebhookAction`/`handleBotTokenAction` completion with DM `CreatePost` failing → assert the response signals delivery failure and does not claim unqualified success; for bot-token, token is rotated/deleted.
3. **Partial Team-Admin promotion** — inject `UpdateTeamMemberRoles` failure for one of two nominees → assert outcome/audit/DM name only the succeeded nominee.
4. **Partial team creation** — `CreateTeam` ok, requester promotion fails → assert the requester isn't told they're Team Admin.
5. **Channel-admin toggle** — once added, disabled toggle → `handleRequestAdmin` rejects and stores nothing.
6. **Approval after disable** — pending request + toggle disabled → document/assert intended behavior (currently approvable).
7. **Cross-type request id** — POST a team request id to `approve_webhook` → assert "already handled", no mutation.
8. **Stale CAS** — `KVCompareAndSet`/`KVCompareAndDelete` returns false → assert "already handled/try again", no side effect, no duplicate notification. (Partially covered for bot-token partial path.)
9. **Malformed stored JSON** — `load*Request` on corrupt bytes → assert graceful ephemeral, no panic.
10. **Webhook duplicate on retry** — simulate create success + restore + retry → assert de-dup (after a fix adds one).

## Remaining Manual QA

Requires a running server/browser (not executed here):

- Webapp Team modal UX (`RequestTeamModal.tsx`): open/close/Escape/cancel, double-submit guard, loading state, state reset on reopen, member autocomplete, keyboard nav, focus trap, a11y labels/screen-reader semantics, long/Unicode values. (Backend endpoints exercised live; UI rendering not.)
- Playwright component + E2E suites (`webapp test:pw`, `test:pw-ct`).
- True concurrency stress (parallel approve/deny clicks) against a real store.
- Ambiguous-success network-fault injection for webhook creation (Bug 6).
- Negative authorization at the action endpoints against a live server (unauthorized user calling `/api/v1/approve_*` directly).

## Production Readiness Checklist

- Two-step engine enforces two distinct approvers: **VERIFIED** (live)
- Both approval orders (security-first / system-first) reach COMPLETE: **VERIFIED** (live)
- System Admin cannot satisfy the security step: **VERIFIED** (live)
- No premature creation before 2nd approval; single side effect on completion: **VERIFIED** (live delta)
- Secrets never leak to approval/audit/logs/ephemeral; DM-only delivery: **VERIFIED** (code audit + live)
- Feature toggles enforced on all five submission paths: **VERIFIED** (code; channel-admin has none — **FAILED**)
- Go unit suite / webapp tsc / lint / build: **VERIFIED** (executed)
- Bot-token recoverable after token-issuance failure: **FAILED** (Bug 1, reproduced)
- Secret guaranteed-or-flagged on delivery failure: **FAILED** (Bug 2)
- Team Admin promotion status/audit accurate on partial failure: **FAILED** (Bug 3)
- Team creation status accurate on partial failure: **FAILED** (Bug 4)
- Channel-admin request can be disabled by admins: **FAILED** (Bug 5)
- Webhook creation idempotent on ambiguous-success retry: **FAILED** (Bug 6)
- Concurrency: no regression to pending, no duplicate side effect under races: **NOT TESTED** (reasoned from CAS code + single-side-effect observed; no parallel stress harness)
- Negative authorization at action endpoints (live): **NOT TESTED** (unit test covers bot-token non-approver; others code-reviewed)
- Webapp Team modal UX / a11y: **NOT TESTED** (needs browser)
- Playwright suites: **NOT TESTED**

## Overall verdict

**Not production-ready.** The design and the concurrency/secret-confidentiality fundamentals are solid, but the privileged-side-effect failure paths are not: a bot-token failure can brick a request and leak a bot (High), secrets can be silently lost (High), and admin-promotion workflows overclaim success including in the audit log (High). Fix the three High items and make partial-success reporting truthful, then re-run with the new regression tests and a live concurrency + negative-authorization pass.
