# Mattermost Requests Branch QA Report

Branch: `kyledoliente_requests_plugin_additions` (vs `origin/main`)
Reviewer: senior QA / adversarial engineering pass
Method: branch diff review + executed Go/webapp suites + **live end-to-end exercise against a real Mattermost server** (driving the real plugin HTTP endpoints as three real users and the real Approve/Deny action API) + targeted failure-injection analysis and one reproduced failing regression test.

## Executive Summary

The branch adds a general `/request` command (`channel`, `team`, `team-admin`, `webhook`, `bot-token`) with the legacy `/channel-request` alias, per-type enable toggles, team-creation requests (+ React modal), Team Admin promotion requests, and a **two-step (security + system) approval engine** for incoming-webhook and bot-token requests, all backed by KV-persisted pending state and approval cards.

**The core approval machinery is sound.** The two-step engine enforces two distinct approvers, works in both approval orders, and is race-safe for the single privileged side effect (verified live with delta assertions, and by reading the `KVCompareAndSet`/`KVCompareAndDelete` claim logic). Secrets (webhook URLs, bot tokens) are never persisted, logged, or posted to the approval/audit channels — only DM'd to the requester (verified live + code audit). Feature toggles are enforced server-side on every *submission* path. The Go suite, webapp `tsc`, lint, and production build all pass.

**The failure-recovery and partial-success paths were not production-ready when first reviewed.** The most serious issues were all in the "what happens after the irreversible side effect" space the happy-path tests never touch. **All six have since been fixed on this branch — see the Resolution Update below.**

- ~~**A bot-token request can orphan a privileged bot account and become permanently unrecoverable** if token issuance fails after bot creation (High).~~ **FIXED**
- ~~**A freshly minted secret (bot token / webhook URL) can be irretrievably lost** if the DM delivery fails, because the request is already resolved (High).~~ **FIXED**
- ~~**Team Admin promotion reports complete success — including in the audit log — even when some or all promotions failed** (High).~~ **FIXED**
- ~~Team creation tells the requester they are a Team Admin even if that promotion failed (Medium).~~ **FIXED**
- ~~Channel-admin requests have **no feature toggle** and no server-side enable gate (Medium).~~ **FIXED**
- ~~Webhook creation can **duplicate** on an ambiguous-success retry (Medium).~~ **FIXED**

Original recommendation: not ready for production until the High items are fixed and partial-success reporting is made truthful. **Current status: those fixes have landed (with regression tests); remaining open items are Low (a stale docs label) — see Resolution Update.**

## Resolution Update

All six confirmed bugs were fixed on this branch after the initial review, each in its own commit with a regression test. The Go suite (`go test -count=1 ./...`), `go vet`, and the webapp `tsc`/lint/production build all pass after the changes.

| Bug | Severity | Fix commit | Fix summary | Regression test |
|---|---|---|---|---|
| 1 Bot-token orphan/unrecoverable | High | `fec6f56` | On token-issuance failure the orphaned bot is `PermanentDeleteBot`-ed, freeing the username so a retry succeeds | `TestCreateBotTokenForRequest_DeletesOrphanBotOnTokenFailure` |
| 2 Secret lost on delivery failure | High | `fec6f56` | DM delivery now returns an error; on failure the bot/webhook is removed and the failure is reported (no false success) | `TestHandleBotTokenAction_DeliveryFailureRemovesBotAndReportsFailure` |
| 3 Team-Admin promotion overclaim | High | `6f8282d` | `promoteTeamAdmins` returns `(promoted, failed)`; card/DM/audit report only real promotions and flag failures | `TestHandleTeamAdminAction_PartialPromotionReportedTruthfully` |
| 4 Team-creation overclaim | Medium | `fe6cba8` | `createTeamForRequest` reports whether the requester was actually promoted + trims members to real adds | `TestHandleTeamAction_RequesterNotToldTeamAdminWhenPromotionFails` |
| 5 Channel-admin had no toggle | Medium | `bf8dcce` | New `AllowChannelAdminRequests` toggle (default on) gating `handleRequestAdmin`, `handleConfig`, and the webapp menu item | `TestHandleRequestAdmin_DisabledToggleRejects` |
| 6 Webhook duplicate on retry | Medium | `68f8479` | Per-request marker embedded in the hook description; a retry finds and reuses the existing hook instead of duplicating | `TestWebhookRequestIdempotencyMatching` |

Remedy principle for the secret bugs (1, 2): **fail closed on the secret** — if a bot token or webhook URL cannot be delivered to the right person, the credential is destroyed (`PermanentDeleteBot` / `DeleteIncomingWebhook`) rather than left live and unowned; resubmission is the clean recovery.

**Post-fix live re-verification** (real server, three real users, real action API): after redeploying the fixed build, the full happy-path suite was re-run — channel/team/team-admin/channel-admin + both two-step flows. The two-step delta guards were exact in both approval orders (no creation after one approval; no creation when the same user clicks twice; creation only on the second distinct approval), secrets were DM'd to the requester and not leaked to the approval channel, and the new per-request idempotency marker (`[channel-requests:<id>]`) was confirmed present in every created hook's description. No regression from the fixes. The forced delivery-failure and duplicate-retry edges were not reproduced live (hard to trigger safely) — bot-token delivery failure is covered by a Go test; the webhook delivery-failure and true duplicate-retry reuse paths remain live-untested (see checklist).

Still open (lower priority, not yet addressed): the **Low** docs mismatch (`approvals.html` still describes the old "Approve (security)" button label), and the benign `handleConfig` key inconsistency.

## Extended test coverage (follow-up pass)

After the bug fixes, a second pass closed most of the non-browser coverage gaps and added real UI tests:

- **Slash-command routing (live)** via `/api/v4/commands/execute` as a real user: bare `/request`, `help`, unknown subcommand → usage; `channel`/`team`/`bot-token` route to the dialog; `/channel-request` alias; case-insensitive subcommand. Plus Go unit tests for the same (incl. extra-args ignored) and the existing disabled/enabled matrix.
- **Input-validation matrix (live)** through the endpoints: blank/overlong names, overlong purpose/description, missing team, missing/unknown prefix, >100 members, team URL min-length, Unicode-only name without a URL (correctly rejected — slug is empty), Unicode name *with* an ASCII URL (accepted), no-nominees, unknown nominee, requester-not-on-team, blank webhook name, blank/invalid/overlong bot username. **26/26 pass.**
- **`handleConfig` toggle matrix (Go)**: asserts the webapp-facing flags for all-on / all-off / channel-admin-off.
- **Request Team modal (Playwright component tests, real chromium)**: renders when open, renders nothing when closed, blank-name client validation, Cancel closes, live URL-slug preview, successful submit (network mocked) shows success + Close, and a server error is surfaced with the form left open. **8/8 pass** (incl. the pre-existing `HeaderIcon` test).

**New finding from this pass → fixed:** `validateBotTokenInput` used `model.IsValidUsername` (1–64 chars), so bot usernames up to 64 chars passed submit validation even though the dialog/help advertise **3–22**. Tightened to enforce 3–22 (commit `c43181f`), with unit + live tests. Low severity (pre-existing; `CreateBot` would likely have accepted the longer name).

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

> **All six bugs below are now RESOLVED** (see the Resolution Update section for the fix commits and regression tests). They are retained here as the original findings with reproductions, evidence, and the fixes that were applied.

### [High] [RESOLVED — `fec6f56`] Bot-token request orphans a privileged bot and becomes permanently unrecoverable when token issuance fails after bot creation

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

Added during the fix pass (all in `server/qa_regression_test.go`, passing):

1. ✅ **Bot-token orphan on token failure** — `TestCreateBotTokenForRequest_DeletesOrphanBotOnTokenFailure`.
2. ✅ **Secret-delivery failure (bot-token)** — `TestHandleBotTokenAction_DeliveryFailureRemovesBotAndReportsFailure`.
3. ✅ **Partial Team-Admin promotion** — `TestHandleTeamAdminAction_PartialPromotionReportedTruthfully`.
4. ✅ **Partial team creation** — `TestHandleTeamAction_RequesterNotToldTeamAdminWhenPromotionFails`.
5. ✅ **Channel-admin toggle** — `TestHandleRequestAdmin_DisabledToggleRejects`.
6. ✅ **Webhook idempotency match rule** — `TestWebhookRequestIdempotencyMatching`.

Still worth adding:

7. **Secret-delivery failure (webhook)** — the webhook completion path mirrors bot-token but calls Client4 REST (`restClient`), which isn't mockable via `plugintest.API`; cover with a live/integration test.
8. **Cross-type request id** — POST a team request id to `approve_webhook` → assert "already handled", no mutation.
9. **Stale CAS** — `KVCompareAndSet`/`KVCompareAndDelete` returns false → assert "already handled/try again", no side effect, no duplicate notification. (Partially covered for bot-token partial path.)
10. **Malformed stored JSON** — `load*Request` on corrupt bytes → assert graceful ephemeral, no panic.

## Remaining Manual QA

Now covered by the follow-up pass (no longer purely manual): Team-modal core UX (render/validation/cancel/submit success+error/live preview) via Playwright CT; slash-command routing and the input-validation matrix via live tests; toggle exposure via `handleConfig` tests.

Still requires a running server/browser (not executed here):

- Team-modal UX details not in the CT tests: member-autocomplete interaction, keyboard navigation, focus trap, screen-reader/a11y semantics, Escape-to-close.
- **System Console** admin settings UI (the custom pickers: Team/ApprovalChannel/Prefix/Member/AutoApprove).
- **Visual rendering** of approval cards, the repaint-on-failure banner, and ephemeral messages in a real client; websocket real-time card updates.
- True concurrency stress (parallel approve/deny clicks) against a real store.
- Ambiguous-success network-fault injection for webhook creation (Bug 6) and the webhook delivery-failure path end-to-end.
- Negative authorization at the action endpoints against a live server (unauthorized user calling `/api/v1/approve_*` directly).
- In-place-upgrade check that the channel-admin manifest default applies.

## Production Readiness Checklist

- Two-step engine enforces two distinct approvers: **VERIFIED** (live)
- Both approval orders (security-first / system-first) reach COMPLETE: **VERIFIED** (live)
- System Admin cannot satisfy the security step: **VERIFIED** (live)
- No premature creation before 2nd approval; single side effect on completion: **VERIFIED** (live delta)
- Secrets never leak to approval/audit/logs/ephemeral; DM-only delivery: **VERIFIED** (code audit + live)
- Feature toggles enforced on every submission path (incl. channel-admin): **VERIFIED** (code + regression test; channel-admin gate added in `bf8dcce`)
- Go unit suite / webapp tsc / lint / build: **VERIFIED** (executed, including after all fixes)
- Bot-token recoverable after token-issuance failure: **VERIFIED** (regression test; orphan bot deleted — `fec6f56`)
- Secret guaranteed-or-flagged on delivery failure: **VERIFIED** for bot-token (regression test — `fec6f56`); webhook path mirrors it but is **NOT TESTED** in Go (needs live/integration — Client4 not mockable)
- Team Admin promotion status/audit accurate on partial failure: **VERIFIED** (regression test — `6f8282d`)
- Team creation status accurate on partial failure: **VERIFIED** (regression test — `fe6cba8`)
- Channel-admin request can be disabled by admins: **VERIFIED** (regression test — `bf8dcce`)
- Webhook creation idempotent on ambiguous-success retry: match rule **VERIFIED** (unit test — `68f8479`); per-request marker now **VERIFIED live** (embedded in the created hook's description). True ambiguous-success *reuse* still **NOT TESTED** end-to-end (can't force a lost-response retry without fault injection).
- Channel-admin stays enabled after in-place upgrade (manifest default applied): **VERIFIED** (live — the daily-insights config predates the `allowchanneladminrequests` key, yet `/api/v1/config` returns `channel_admin:true`, confirming the plugin.json default applies on upgrade).
- Concurrency: no regression to pending, no duplicate side effect under races: **VERIFIED** (live — 5× concurrent completing approvals produced exactly one webhook, one marker, and the pending KV entry consumed once; CAS claim-before-execute holds).
- Negative authorization at action endpoints (live): **VERIFIED** (live — requester cannot approve or deny own webhook/bot-token request; the same approver cannot fill both steps; no side effect on blocked attempts).
- Full two-step webhook flow end-to-end (live): **VERIFIED** (partial→complete, secret DM'd to requester only, not in approvals channel, and posting to the delivered URL returns 200).
- Webhook delivery-failure live (undeliverable secret → cleanup + flag): **NOT TESTED** (needs DM-send fault injection; bot-token equivalent is regression-tested and the webhook path mirrors it).
- Webapp Team modal UX / a11y: **NOT TESTED** (needs browser; logic covered by Playwright CT).
- Playwright component suite: **VERIFIED** (8/8 for the Request Team modal); broader E2E/a11y **NOT TESTED**.

## Overall verdict

**Initial review: not production-ready** — the privileged-side-effect failure paths were unsafe (bot-token could brick a request and leak a bot, secrets could be silently lost, admin-promotion workflows overclaimed success including in the audit log).

**After the fix pass: the six confirmed bugs are resolved**, each with a regression test, and the Go suite + webapp build are green. The design and concurrency/secret-confidentiality fundamentals remain sound.

**After the live verification pass (2026-10-01):** the two-step webhook flow is confirmed end-to-end against a running server (partial→complete, secret DM-only, delivered URL usable), negative authorization at the action endpoints holds, the CAS survives a 5× concurrent completion with a single side effect, the idempotency marker is embedded in the created hook, and the channel-admin default correctly applies on in-place upgrade. The remaining gaps are narrow and require fault injection or a browser: forcing an undeliverable-secret cleanup (webhook path mirrors the regression-tested bot-token path), forcing a true ambiguous-success reuse, and a webapp modal a11y/keyboard review. **For an internal or limited rollout this is ship-ready; the remaining items are fast-follow confidence checks rather than known defects.**
