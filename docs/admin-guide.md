# Admin Guide — Mattermost Permissions Plugin

This guide covers plugin configuration, the approval workflow, and how each request type is handled. For end-user guides, see the linked request-type docs at the bottom.

---

## Initial setup

Configure the plugin in **System Console → Plugins → Mattermost Permissions**.

### Per-type toggles

Each request type can be enabled independently. Defaults after install:

| Toggle | Default |
|--------|---------|
| Channel requests | On |
| Channel admin requests | On |
| Team requests | Off |
| Team admin requests | Off |
| Bot account requests | Off |
| Webhook requests | Off |

Disabled types are hidden from the Request Hub and slash commands, and the server rejects submissions.

### Required settings

| Setting | Purpose |
|---------|---------|
| **Approval team** | The team that owns the plugin-managed approval channels. |

### Two-step approver attributes (bot + webhook)

Bot and webhook requests require **two distinct approvals**: one security step and one system step.

| Setting | Purpose |
|---------|---------|
| **Security approver attribute** | User Attribute name (access_control group) that marks security-step approvers. System Admins do **not** auto-qualify for this step. |
| **System approver attribute** | User Attribute name for system-step approvers. System Admins auto-qualify for this step. |

Create and assign these attributes in **System Console → User Management → Attributes**. If they are unset or no users hold them, bot/webhook cards cannot complete.

### Optional settings

| Setting | Purpose |
|---------|---------|
| **Channel domain prefixes** | Restrict channel URL naming to a defined prefix list. Requesters pick a prefix from a dropdown; the final URL is `<prefix><suffix>`. |
| **Team Admins can approve requests** | When on, Team Admins of the approval team can approve or deny channel creation and channel admin requests (not team/bot/webhook). |
| **Auto-approve requests from these users** | Named users bypass approval for channel creation only. |
| **Per-team approval channels** | When on, channel and channel-admin requests route to a per-team approval channel instead of the root channel. |

### Locking down native channel creation

The plugin intercepts the sidebar UI for non-admins, but it can't veto the native Mattermost channel creation API. To fully route members through the request flow, remove the **Create Public/Private Channel** permission from the System User role in **System Console → User Management → Permissions → System Scheme**.

---

## Approval channels

The plugin creates and manages four dedicated private channels in the configured approval team. All System Admins are added as members when the plugin activates (and when each channel is first created).

| Channel | What routes here |
|---------|-----------------|
| Configured approval channel | Channel creation requests, channel admin requests, team admin requests |
| `mattermost-permissions-teams` | Team creation requests |
| `mattermost-permissions-bots` | Bot account requests (including associated tokens and webhooks) |
| `mattermost-permissions-webhooks` | Standalone incoming and outgoing webhook requests |

If a channel is accidentally archived, the plugin recreates it on next activation.

---

## Approving and denying requests

Every request posts a card to the appropriate approval channel. The card includes the request details, the requester's username, and **Approve** and **Deny** buttons.

[Screenshot: A representative request card showing request details and Approve/Deny buttons]

Channel, channel-admin, team, and team-admin cards use **single-step** approval (System Admins, plus Team Admins when enabled for channel types).

Bot and webhook cards use **two-step** approval. The card shows security and system status lines. Each eligible approver clicks **Approve** once; two different people must fill the two steps before the resource is created. Any eligible approver may **Deny** at any time.

### What each approval does

| Request type | On Approve |
|--------------|-----------|
| **Channel creation** | Creates the channel, adds members, promotes Channel Admins, places in sidebar category if requested |
| **Channel Admin** | Adds nominees to the channel (if not already members) and promotes them to Channel Admin |
| **Team Admin** | Promotes the requester to Team Admin on their current team |
| **Team creation** | Creates the team, adds the requester, optionally promotes to Team Admin |
| **Bot account** | After both steps: creates the bot, optional token/webhooks; secrets go to a DM that auto-deletes in 72h |
| **Incoming webhook** | After both steps: creates the webhook; URL is DM'd privately. If delivery fails, the hook is deleted |
| **Outgoing webhook** | After both steps: creates the webhook and confirms via DM |

**Deny** always: closes the ticket, notifies the requester by DM, records who denied and when.

---

## Two-way DM relay

When a request is submitted, the bot sends the requester a DM with a thread for that request. Admins can communicate with the requester through the approval thread:

- A reply in the **approval thread** is relayed to the requester's **DM thread**.
- A reply in the requester's **DM thread** is relayed to the **approval thread**.

This lets reviewers ask clarifying questions and get responses without leaving the approval channel, and without the requester needing access to the approval channel.

[Screenshot: An approval thread with a reviewer reply, showing the relay indicator]

---

## Bot approvals in detail

A single bot approval ticket can cover three operations: creating the bot account, generating an access token, and creating incoming and/or outgoing webhooks.

Bot requests need a **security** approval and a **system** approval from two different people. When both steps are filled, the server creates the bot and optional extras. If creation fails, the card stays pending so the final approver can retry.

### Token delivery

Access tokens are generated at approve-time using the plugin's bot account, not a stored credential. The token value is sent to the requester in a direct message that **auto-deletes after 72 hours**. The token itself does not expire unless revoked.

If a token is suspected compromised, revoke it in **System Console → User Management → Bot Accounts → {bot name}** and issue a new one.

---

## Webhook approvals in detail

Webhook requests use the same two-step engine as bots. The final approver's session is used for a single create call — never stored.

**System Admins are not single-step bypassed for webhooks.** They can fill the system step; a security approver must still approve. If creation fails after both steps, the card stays pending for retry. If an incoming webhook URL cannot be DM'd to the requester, the hook is deleted.

---

## Domain prefix configuration

If you set channel domain prefixes, requesters must pick a prefix from a dropdown. The final channel URL is `<prefix><suffix>`.

[Screenshot: The domain-prefix editor with several rows filled in]

Each row has:
- **Prefix** — the fixed URL segment (e.g. `ops-`, `prod-`)
- **Description** — shown in the dropdown to help requesters choose
- **Max suffix length** — caps how many characters the requester can append (2–32)

Use **Load preset** to populate from a starter pack. The **Live preview** at the bottom shows what a given prefix and suffix combination produces.

[Screenshot: Domain prefix editor with preset loaded and live preview showing example URL]

---

## Per-team approval channels

When **Per-team approval channels** is enabled, each new team created through the plugin gets a private `{team-url}-approvals` channel. Channel creation and channel admin requests from members of that team route to that team's channel instead of the root approval channel.

This is useful when you have multiple independent teams with their own admin pools and don't want all requests landing in one shared channel.

System Admins are seeded into each per-team channel when it's created.

---

## Request type reference

| Request type | Entry points | Approval channel | Bypass? |
|---|---|---|---|
| Channel creation | Sidebar +, channel header button, More actions, slash cmd | Root channel or per-team channel | System Admins + auto-approve list |
| Channel Admin | More actions, Members panel button, slash cmd | Root channel or per-team channel | System Admins + auto-approve list |
| Team Admin | More actions, slash cmd | Root channel | System Admins |
| Team creation | More actions, team sidebar menu | `mattermost-permissions-teams` | System Admins |
| Bot account (+ token + webhooks) | More actions, Integrations page | `mattermost-permissions-bots` | Two-step: security + system attributes |
| Incoming webhook (standalone) | More actions, Integrations page | `mattermost-permissions-webhooks` | **None** — always queued |
| Outgoing webhook (standalone) | More actions, Integrations page | `mattermost-permissions-webhooks` | **None** — always queued |

---

## Related guides

- [Requesting a new channel](request-channel.md)
- [Requesting channel admin privileges](request-channel-admin.md)
- [Requesting a bot account](request-bot.md)
- [Requesting webhooks (standalone)](request-webhook.md)
