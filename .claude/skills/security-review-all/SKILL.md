---
name: security-review-all
description: Whole-product offensive security audit of Channel Requests. Hunts for exploitable holes across the request and approval flow, the HTTP API, the slash command and dialog, the webapp, the admin-console settings and the build, proves each one against the code, and reports them ranked by impact with a fix. Use for periodic audits, before a release, or when asked to find security holes. Read-only.
user-invocable: true
---

# Security Review

You are a god tier security researcher. Find security holes in this product so we
can fix them.

Think like an attacker with a Mattermost account who wants a channel, a role, or
a look at something they should not have: a requester gaming the approval flow,
a team member enumerating users, a Team Admin reaching past their team, or a
crafted request that renders into an approver's view. The
goal is real, exploitable findings with a concrete path from attacker input to
impact, not a checklist of best practices.

This skill is read-only. Do not edit, commit or push. Do not run anything that
reaches the network or mutates the working tree.

## Usage

```
/security-review-all                  # The whole product
/security-review-all <path>           # Focus on one file or directory
/security-review-all --diff           # Only what changed on this branch since the trunk
```

## Step 1: Map the attack surface

Read `CLAUDE.md` first, then the comments on `requireUserID` in `server/http.go`
and on `channelRequest` and the input bounds in `server/request.go`. They are the
product's own statement of what must hold; every one is a hypothesis to break.
Then enumerate entry points and who controls each one:

| Surface | Attacker | Where |
|---|---|---|
| Request form JSON (`/api/v1/create`) | Any logged-in user | `handleWebappCreate` in `server/http.go`, `submitRequest` and `validateRequestInput` in `server/request.go` |
| Interactive dialog submit (`/api/v1/dialog`) | Any logged-in user, with a forged submission | `handleDialogSubmit`, `openRequestDialog` |
| Approve and deny buttons (`/api/v1/approve`, `/deny`) | Any user who can see or forge the callback | `handleAction`, `canApprove`, `isTeamAdmin` |
| Channel Admin requests (`/api/v1/request_admin`, `/approve_admin`, `/deny_admin`) | Any channel member | `server/admin_request.go`, `handleAdminAction`, `canApproveAdminRequest` |
| Pickers (`/api/v1/teams`, `/channels`, `/user_autocomplete`, `/prefixes`) | Any logged-in user | `handleListTeams`, `handleListChannels`, `handleUserAutocomplete`, `handlePrefixes` |
| Slash command | Any user in any channel | `server/command.go` |
| KV store request records | Concurrent approvers, cluster peers | `storeRequest`, `loadRequest`, `KVCompareAndDelete` claims |
| Admin-console settings | System Admins, and anything that reads them | `server/configuration.go` (`parsePrefixList`, `compileSuffixRule`, `resolveAutoApproveList`), `webapp/src/PrefixEditor.tsx`, `ApprovalChannelPicker.tsx`, `AutoApprovePicker.tsx` |
| Posts the bot writes | Requesters, via display name, purpose and usernames | `approvalAttachment`, `postWelcomeMessage`, `postAdminPromotionMessage`, `logAudit`, `notifyRequester` |
| Webapp rendering and overrides | Authors of any data the modals display | `webapp/src/**`, `channelCreationOverride.ts`, `membersPanelButton.ts` |
| Build and release | Upstream dependencies and workflow inputs | `build/`, `Makefile`, `.github/workflows/`, `go.mod`, `webapp/package.json`, `.grype.yaml` |

## Step 2: Hunt

Run the lenses below as parallel read-only `Explore` or `general-purpose`
agents, one per lens, each told the surface table above, told to cite
`file:line`, and told to return candidates with an attack path rather than
general advice. With a path argument, keep only the lenses that touch it.

1. **Authorization and identity.** Every handler that acts for a user must take
   identity from `requireUserID`, never from a body field, a dialog submission
   or `PostActionIntegrationRequest.UserId`. Can a non-approver approve or deny?
   Can a Team Admin approve a request for a team they do not administer, or when
   `AllowTeamAdminApprovers` is off? Can a member request admin rights on a
   channel they are not in, or nominate users outside it? Can a requester land
   themselves in the auto-approve path?
2. **Request tampering.** Fields the form constrains but the server must
   re-check: prefix not in the configured list, suffix that fails its rule, a
   name that collides or escapes slugify, channel type outside open/private, a
   team the requester is not on, member and admin lists over
   `maxMembersPerList`, deactivated or bot users, ids versus usernames.
3. **Approval races and replay.** Two approvers, a double click, a replayed
   callback, approve after deny. Does `KVCompareAndDelete` make every
   transition single-use, and does any path act before the claim succeeds?
   Partial failure after channel creation (members half-added, admins not
   promoted) and what state it leaves.
4. **Enumeration and disclosure.** `user_autocomplete` scoping to the requested
   team and to team membership, `channels` listing private channels or channels
   in teams the caller is not on, `teams` returning more than the caller's
   teams, and error bodies that reveal whether a user, team or channel exists.
5. **Injection into posts and UI.** Display name, purpose and usernames echoed
   into attachments, welcome messages, audit posts and ephemeral replies:
   markdown links, `@channel`/`@all`/`@here` mentions, images, or text that
   impersonates the bot. In the webapp, every `dangerouslySetInnerHTML`, `href`
   built from data, and admin-configured description rendered to requesters.
6. **Configuration abuse.** Suffix regexes compiled from settings (size,
   pathological patterns, anchoring), malformed prefix JSON, a stale approval
   channel id, an audit channel the bot can post to but approvers cannot see,
   and what happens when settings change while requests are pending.
7. **Denial of service.** Unbounded request creation per user, large bodies,
   fan-out of `GetUser`/`AddChannelMember` calls, and system-admin DMs when no
   approval channel is configured.
8. **Supply chain and release.** Unpinned Actions, `pull_request_target` or
   script injection from PR titles, secrets exposed to forks, copyleft or
   vulnerable dependencies, and anything `.grype.yaml` suppresses without a
   real reason.

Also check error paths: does any failure leak a stack, an internal id the
caller should not learn, or another user's data?

## Step 3: Prove it

A finding without proof is a guess. For each candidate:

- Trace the data flow from the attacker's input to the sink, reading the actual
  code at every hop. Drop it if a check anywhere on the path stops it.
- Name the attacker's required position (anonymous, any user, channel member,
  another plugin, admin, filesystem access). Admin-only and filesystem-only
  issues are real but rank lower.
- Where it is cheap and safe, write a failing test in the scratchpad or run the
  existing test that exercises the vulnerable path with a crafted input
  (`go test ./server/... -run 'TestName'`) to confirm. Never leave the working tree changed.
- Mark each finding CONFIRMED (proven or traced end to end) or PLAUSIBLE
  (traced, but depends on something you could not verify).

Be adversarial with yourself. Discard theoretical issues, defense-in-depth
nits with no path to impact, and anything the product's invariants already
explain as deliberate.

## Step 4: Report

Rank by severity, then by confidence. For each finding give:

- **Title** and severity (Critical, High, Medium, Low)
- **Location:** `file:line`
- **Attacker:** who can trigger it
- **Attack:** the exact input and the steps
- **Impact:** what they get
- **Fix:** the minimal change, and the regression test that should hold it

End with a short list of the surfaces you reviewed and found clean, so the next
audit knows what was covered, and anything you could not assess. Offer to fix
the confirmed findings; do not fix them unasked.
