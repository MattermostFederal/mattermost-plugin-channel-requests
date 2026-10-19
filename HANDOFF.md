# Local Testing Handoff — Mattermost Request Hub

**Updated:** 2026-10-19  
**Branch:** `feature/full-request-hub` (pushed, in sync with origin)  
**PR:** https://github.com/MattermostFederal/mattermost-plugin-request-hub/pull/71  
**Version:** `0.5.2`  
**Plugin ID:** `com.mattermostfederal.mattermost-plugin-request-hub`  
**Slash:** `/mattermost-request-hub`  
**Bot:** `mattermost-request-hub` (Display: Mattermost Request Hub)  
**Bundle:** `dist/com.mattermostfederal.mattermost-plugin-request-hub-0.5.2.tar.gz`

---

## Where we left off

1. **Product rename + hub expansion shipped on the PR** — channel, channel-admin, team, team-admin, team-member, team-guest, bot, webhook; help docs under `public/help/`; team rail `+` injection; security hardening (no auto-approve for guest/membership shortcuts that were wrong; relay gated by ticket Kind; guest submit requires team membership).
2. **Lint + Copilot findings fixed and pushed** (`8397ab6`) — CAS re-store for ticket post IDs, select-attribute option resolution, guest email-only invites (no regular team InviteId link), hub tile O/P guards, PrefixEditor lossless parse, dialog focus, override disposers, `.spec.ts` renames. All 12 Copilot threads replied + resolved.
3. **Approval close UX pushed** (`3e94481`) — squashed one-liner includes requester; original card archived into a thread reply on close.
4. **Audit channel UX** (`0131464` + `1c48df9`) — approve/deny posts are colored cards (green ✅ / red ❌ / blue ⚡ Direct) with `[Open ticket thread]` permalinks.
5. **Local dist rebuilt** at `dist/com.mattermostfederal.mattermost-plugin-request-hub-0.5.2.tar.gz` (includes audit colors). Bot icon is the pasted Mattermost mark.
6. **SBOM/Grype/govulncheck** were green earlier; no CVE bump required. Dependabot #72–#74 optional.
7. **Terraform GitHub rename** already done earlier; not part of current local testing.

### Still local / not in git

- `HANDOFF.md` itself (this file) — intentionally untracked.

### Watch on CI after `3e94481`

- Lint was reporting **FAILURE** right after push (may still be the new run). Re-check PR checks if red; prior lint issues were cleared in `8397ab6`.

---

## Deploy for local test

1. Upload  
   `dist/com.mattermostfederal.mattermost-plugin-request-hub-0.5.2.tar.gz`  
   via **System Console → Plugins → Upload Plugin**.
2. Enable the plugin. Confirm bot `mattermost-request-hub` and profile image.
3. If an older channel-requests / request-hub ID is still installed, uninstall it first (0.5.0+ ID change — settings/KV do not migrate).

### Config to verify

| Setting | Notes |
|---------|--------|
| Approval team | Required — owns auto-created approval channels |
| Channel name prefixes | At least one prefix or channel-create modal blocks |
| Allow* toggles | Team guest defaults off until enabled |
| Security / System approver attributes | Bot + webhook two-step; select fields must use a **Yes**-named option |

### Approval channels (auto-created in the approval team)

| Request types | Channel |
|---------------|---------|
| Channel create, Channel Admin | Root / per-team channel requests |
| Team create, Team Admin, team member, guest | `mattermost-team-requests` |
| Bot | `mattermost-bot-requests` |
| Webhooks | `mattermost-webhook-requests` |
| Audit | `mattermost-audit-requests` |

---

## What to test next (focus: close UX)

1. Submit any request as a non-admin → approve or deny.
2. **Approval channel root** should become a one-liner like:  
   `✅ Approved by @you for @requester. Channel ~foo created.`
3. Expand the **thread** under that post → should see  
   `Original request details (preserved on close):`  
   with the full card fields and **no** Approve/Deny buttons.
4. Requester DM ticket still gets outcome card / thread reply as before.
5. Guest invite: email **required**; approve emails via `InviteGuestsToTeam` only (no `/signup_user_complete/?id=` link).
6. Hub from a **DM**: Channel Admin / Guest / webhook tiles hidden; from O/P channel they show.

### Quick slash reference

```
/mattermost-request-hub docs
/mattermost-request-hub channel | channel-admin | team | team-admin
/mattermost-request-hub team-member | team-guest | bot
/mattermost-request-hub webhook-incoming | webhook-outgoing
```

---

## Recent commits on this branch (newest first)

| SHA | Summary |
|-----|---------|
| `3e94481` | Squash line includes requester; archive original card into thread on close |
| `8397ab6` | Lint + Copilot fixes (CAS, approvers, guest email, hub guards, a11y, disposers) |
| `28fda84` | Drop `.gitkeep` placeholders from docs images |
| `bbdff3a` | Bot avatar = pasted Mattermost icon verbatim |

---

## Known gaps / not done

| Item | Notes |
|------|--------|
| 30-day KV TTL for thread anchors | Still not implemented |
| Dependabot #72–#74 | Optional; merge after this PR if desired |
| Docs screenshots | Some may still show older branding — re-capture if needed |
| HANDOFF.md | Local only — do not commit unless you want it in-repo |

---

## Debug

```bash
# Plugin logs
docker logs -f <mattermost-container> 2>&1 | grep -i 'request-hub\|mattermost-request-hub'
```

Search Mattermost logs for `"plugin_id":"com.mattermostfederal.mattermost-plugin-request-hub"`.
