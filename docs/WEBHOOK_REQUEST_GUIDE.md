# Webhook Requests — User Guide

Use this guide when you need an **incoming** or **outgoing webhook** but don't
need a new bot account. Standalone webhook requests use **two-step approval**
(security + system). Secrets are delivered by DM.

> Setting up a full integration (bot + token + webhooks) in one ticket? Use the
> [bot account guide](BOT_REQUEST_GUIDE.md) instead.
>
> Companion guides: [new channels](CHANNEL_CREATION_GUIDE.md) ·
> [Channel Admin](CHANNEL_ADMIN_GUIDE.md) · [teams](TEAM_REQUEST_GUIDE.md) ·
> [bots](BOT_REQUEST_GUIDE.md).
>
> In Mattermost: **`/mattermost-request-hub docs`** opens the full HTML guide.

**Incoming webhook** — an external system POSTs messages into a Mattermost
channel. You get a URL to call.

**Outgoing webhook** — Mattermost forwards messages from a channel to your
external URL (useful for keyword-triggered pipelines).

This guide covers:

- [Requesting a webhook](#requesting-a-webhook) — any member
- [Messaging while a request is pending](#messaging-while-a-request-is-pending) — two-way threads
- [Approving requests](#approving-requests) — security + system approvers
- [Admin setup](#admin-setup) — system admins

---

## Requesting a webhook

Incoming and outgoing each have the same three entry-point patterns: the native
Integrations page, the channel **More actions** menu, and the **plug icon** on
the right App Bar (Requests hub).

Webhook menu items appear only in **public and private channels** — not in DMs
or group messages (those can't be webhook targets).

### Incoming webhook

#### 1. Native: Integrations page

Open **Main Menu (≡) → Integrations → Incoming Webhooks**. If you aren't a
System Admin, the plugin opens the request form instead of the native management
page.

![Integrations page — Incoming Webhooks for non-admins](images/webhook-request/mattermost-webhook-integrations-incoming.png)

#### 2. Dropdown: channel More actions

Click the **channel name**, then **More actions → Request Incoming Webhook**.

![Request Incoming Webhook under More actions](images/webhook-request/mattermost-webhook-more-actions-incoming.png)

#### 3. Right-side plug icon (Requests hub)

On the **far right** of the window (the App Bar / plugin strip), click the
**plug** icon → **Requests** → **Request Incoming Webhook**.

If several plugins share the strip, the plug may open a short menu first —
pick **Requests**, then the hub tile.

![Plug icon on the right App Bar opening Requests](images/shared/mattermost-requests-plug-appbar.png)

![Request Hub with Incoming Webhook highlighted](images/webhook-request/mattermost-webhook-hub.png)

#### Filling out an incoming webhook request

![The Request an Incoming Webhook form](images/webhook-request/mattermost-webhook-incoming-modal.png)

| Field | What it does |
|-------|--------------|
| **Target channel** | Pre-filled with your current channel (read-only). Navigate to the intended channel before opening the form. |
| **Display name** | Label shown on the Integrations page. |
| **Description** | Optional. What system posts here and why. |

### Outgoing webhook

#### 1. Native: Integrations page

Open **Main Menu (≡) → Integrations → Outgoing Webhooks**. Non–System Admins get
the request form; System Admins get the native UI.

![Integrations page — Outgoing Webhooks for non-admins](images/webhook-request/mattermost-webhook-integrations-outgoing.png)

#### 2. Dropdown: channel More actions

Click the **channel name**, then **More actions → Request Outgoing Webhook**.

![Request Outgoing Webhook under More actions](images/webhook-request/mattermost-webhook-more-actions-outgoing.png)

#### 3. Right-side plug icon (Requests hub)

Click the **plug** icon on the right App Bar → **Requests** → **Request Outgoing
Webhook** (same hub as incoming — see the plug-icon screenshot above).

#### Filling out an outgoing webhook request

![The Request an Outgoing Webhook form](images/webhook-request/mattermost-webhook-outgoing-modal.png)

| Field | What it does |
|-------|--------------|
| **Source channel** | Pre-filled with your current channel (read-only). |
| **Display name** | Label shown on the Integrations page. |
| **Callback URL** | Required. The `https://` (or `http://`) endpoint Mattermost will POST to. |
| **Description** | Optional. |

### After submitting

Click **Submit request**. You'll get a DM confirming the request was received,
then another when both approvers have acted.

---

## Messaging while a request is pending

Webhook tickets use the same **two-way thread relay**. Reviewers can ask things
like *Which system posts to this incoming webhook?* or *What does the callback
URL do?* without adding you to the webhooks approval channel.

Open the **Thread** RHS from the card — not a new top-level channel or DM message.

1. Approver asks in the **approval Thread**:

![Approver asks a clarifying question in the Thread RHS](images/shared/mattermost-thread-approver-ask.png)

2. You see `Message from @admin:` in your **DM Thread** with the bot:

![Requester DM Thread RHS showing the relayed question](images/shared/mattermost-thread-requester-dm.png)

3. You **reply in that Thread**. Your answer is relayed back as
   `Reply from @you:`:

![Approval Thread RHS showing the requester's relayed reply](images/shared/mattermost-thread-relay-back.png)

Always reply inside the ticket DM Thread — not a new root message to the bot.
Threads stay open after approve/deny for follow-ups (for example, if a webhook
URL needs clarification).

---

## Approving requests

All standalone webhook requests post to
**`mattermost-webhook-requests`**. They use **two-step approval** (security +
system, two different people). The card shows both status lines.

![A webhook approval card with two-step Approve/Deny](images/webhook-request/mattermost-webhook-approval-card.png)

When both steps are filled, the final approval creates the webhook. On success
the requester gets a DM with:

- The **webhook URL** (incoming)
- Confirmation that the outgoing webhook is live (outgoing)

![DM after webhook approval with the webhook URL](images/webhook-request/mattermost-webhook-dm.png)

If an incoming webhook URL can't be delivered to the requester, the hook is
**deleted automatically** so no unowned secret endpoint is left live.

**Deny** closes the ticket and notifies the requester by DM.

### System Admin note

Webhooks have **no single-click System Admin bypass**. System Admins still go
through the queue; they can fill the system step, but a security approver must
fill the other.

If webhook creation fails after both steps, the card stays pending with an error
so the final approver can retry.

---

## Admin setup

| Setting | Purpose |
|---------|---------|
| **Approval team** | Owns `mattermost-webhook-requests`. Required. |
| **Allow webhook requests** | Enables incoming/outgoing entry points. Default **off**. |
| **Security / System approver attributes** | Same two-step attributes as bot requests. |

See the [Admin Guide](admin-guide.md) for attribute setup and the full settings
reference.
