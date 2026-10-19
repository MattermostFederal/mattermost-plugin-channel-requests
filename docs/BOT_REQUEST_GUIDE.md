# Bot Account Requests — User Guide

Mattermost Request Hub lets members **request a bot account** (optionally
with an access token and incoming/outgoing webhooks in the same ticket) instead
of creating bots directly. Bot requests use **two-step approval** (security +
system). On final approval the bot is created and secrets are delivered by DM
(the DM auto-deletes after 72 hours).

> If you only need a webhook and already have a bot (or don't need one), use the
> [standalone webhook guide](WEBHOOK_REQUEST_GUIDE.md) instead.
>
> Companion guides: [new channels](CHANNEL_CREATION_GUIDE.md) ·
> [Channel Admin](CHANNEL_ADMIN_GUIDE.md) · [teams](TEAM_REQUEST_GUIDE.md) ·
> [webhooks](WEBHOOK_REQUEST_GUIDE.md).
>
> In Mattermost: **`/mattermost-request-hub docs`** opens the full HTML guide.

This guide covers:

- [Requesting a bot](#requesting-a-bot) — any member
- [Messaging while a request is pending](#messaging-while-a-request-is-pending) — two-way threads
- [Approving requests](#approving-requests) — security + system approvers
- [Admin setup](#admin-setup) — system admins

---

## Requesting a bot

There are several ways to open the same **Request a Bot Account** form.

### 1. Native: Integrations page

Open **Main Menu (≡) → Integrations**. Click **Bot Accounts**. If you aren't a
System Admin, the plugin opens the request form instead of the native bot
management page.

![Integrations page — Bot Accounts opens the request form for non-admins](images/bot-request/mattermost-bot-integrations.png)

System Admins still get the native Bot Accounts management UI.

### 2. Dropdown: channel More actions

Click the **channel name** at the top-left, then **More actions → Request a Bot**.

![Request a Bot under More actions](images/bot-request/mattermost-bot-more-actions.png)

### 3. Right-side plug icon (Requests hub)

On the **far right** of the window (the App Bar / plugin strip), click the
**plug** icon. Open **Requests**, then in the **Request Hub** choose
**Request a Bot**.

If several plugins share the strip, the plug may open a short menu first —
pick **Requests**, then the hub tile.

![Plug icon on the right App Bar opening Requests](images/shared/mattermost-requests-plug-appbar.png)

![Request Hub with Request a Bot highlighted](images/bot-request/mattermost-bot-hub.png)

### Filling out the request

![The Request a Bot Account form](images/bot-request/mattermost-bot-modal.png)

#### Required fields

| Field | What it does |
|-------|--------------|
| **Bot username** | Username the bot posts as. Lowercase letters, numbers, and hyphens only. 3–22 characters. Example: `deploy-notifier`. |
| **Display name** | Friendly name shown in messages. Example: `Deploy Notifier`. |
| **Description** | Optional. What the bot does and which systems it connects to. |

#### Optional: Access token

**Request an access token** is checked by default. External systems use this
token to authenticate to Mattermost as the bot.

> The token is sent in a DM when the request is fully approved. That DM
> **auto-deletes after 72 hours**. Save the token in a secrets manager or CI
> vault before it disappears. The token itself does not expire until you revoke it.

Uncheck this if the bot only receives messages via an incoming webhook and never
calls the API.

#### Optional: Incoming webhook

Check **Request an incoming webhook** to also create a webhook that posts
*into* Mattermost on behalf of this bot.

| Field | What it does |
|-------|--------------|
| **Webhook display name** | Pre-filled with `{username} incoming`. Edit if needed. |
| **Target channel** | Pre-filled with the channel you're currently in (read-only). Switch channels before opening the form to change it. |

#### Optional: Outgoing webhook

Check **Request an outgoing webhook** to forward messages from a channel to an
external URL (for example, to trigger a pipeline on a keyword).

| Field | What it does |
|-------|--------------|
| **Webhook display name** | Pre-filled with `{username} outgoing`. |
| **Source channel** | Pre-filled with your current channel (read-only). |
| **Callback URL** | Required. The `https://` endpoint that receives the payload. |

### Submitting

Click **Submit request**. One approval ticket covers the bot, token, and any
webhooks you checked. You'll get a DM when both approvers have acted.

---

## Messaging while a request is pending

Bot tickets use the same **two-way thread relay** as other request types.
Reviewers (security or system) can ask clarifying questions in the approval
thread — for example, *What systems will this bot call?* or *Why do you need an
access token?* — without adding you to `mattermost-bot-requests`.

Open the **Thread** RHS from the card (Reply / reply count) — not a new root post.

1. Approver replies in the approval **Thread**:

![Approver asks a clarifying question in the Thread RHS](images/shared/mattermost-thread-approver-ask.png)

2. You see `Message from @admin:` in your **DM Thread** with the bot:

![Requester DM Thread RHS showing the relayed question](images/shared/mattermost-thread-requester-dm.png)

3. You **reply in that Thread**. Your answer appears on the approval Thread as
   `Reply from @you:`:

![Approval Thread RHS showing the requester's relayed reply](images/shared/mattermost-thread-relay-back.png)

Reply inside the ticket Thread only. A brand-new DM to the bot gets a nudge to
use the specific request thread. Threads remain open after approve/deny (useful
if a token DM needs follow-up).

---

## Approving requests

Bot requests post to **`mattermost-bot-requests`**. They need **two different
people**: one security-step approver and one system-step approver. The card shows
both status lines.

![A bot approval card with two-step Approve/Deny](images/bot-request/mattermost-bot-approval-card.png)

Each eligible approver clicks **Approve** once for their step. When both steps
are complete, the plugin:

1. Creates the bot account.
2. Generates an access token if requested.
3. Creates any requested incoming/outgoing webhooks.
4. DMs the requester with results (tokens/URLs auto-delete after 72 hours).

Any eligible approver may **Deny** at any time — that closes the ticket and
notifies the requester.

### DM to the requester

![DM after bot approval with token and webhook URLs](images/bot-request/mattermost-bot-token-dm.png)

The DM includes:

- Bot account confirmation
- Access token (if requested) — **message auto-deletes in 72 hours**
- Incoming webhook URL (if requested)
- Outgoing webhook confirmation (if requested)

If something failed, the DM says what couldn't be created and what to do next.

Slash-command submissions always go through both approval steps. System Admins
who submit from the **webapp Request Hub / modal** (not ForceQueue) create the
bot immediately and skip token/webhook extras — there is no independent security
approval on that path. For queued tickets, System Admins can fill the system
step, but a security approver must still fill the other.

---

## Admin setup

| Setting | Purpose |
|---------|---------|
| **Approval team** | Owns `mattermost-bot-requests`. Required. |
| **Allow bot account requests** | Enables bot entry points. Default **off**. |
| **Security approver attribute** | User Attribute (access_control) for the security step. System Admins do **not** auto-qualify. |
| **System approver attribute** | User Attribute for the system step. System Admins auto-qualify. |

Create and assign attributes in **System Console → User Management → Attributes**.
See the [Admin Guide](admin-guide.md).

### Token security

Tokens are delivered by DM and not stored by the plugin. If a token is
compromised, rotate it under **System Console → User Management → Bot Accounts**
and revoke the old one.
