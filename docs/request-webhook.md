# Requesting Webhooks (Standalone)

Use this guide if you need a webhook but don't need a new bot account. If you're setting up a full integration from scratch — bot account, token, and webhooks together — see [Requesting a Bot Account](request-bot.md) instead, which bundles all three into a single approval ticket.

**Incoming webhook** — an external system POSTs messages into a Mattermost channel. The webhook URL is the endpoint your system calls.

**Outgoing webhook** — Mattermost forwards messages from a channel to your external URL. Useful for triggering pipelines when keywords appear.

This guide covers three roles:

- [Requesting a webhook](#requesting-a-webhook) — any member
- [Approving requests](#approving-requests) — approvers
- [Admin setup](#admin-setup) — system admins

---

## Requesting a webhook

### Entry points for incoming webhooks

**Channel-name menu:** Click the channel name at the top-left, then **More actions → Request Incoming Webhook**.

[Screenshot: channel-name More actions menu with "Request Incoming Webhook" highlighted]

**Integrations page:** Navigate to **Main Menu → Integrations → Incoming Webhooks**. If you don't have System Admin access, clicking that link opens the request modal instead of the native management page.

[Screenshot: Mattermost Integrations page showing Incoming Webhooks tile]

### Entry points for outgoing webhooks

**Channel-name menu:** Click the channel name at the top-left, then **More actions → Request Outgoing Webhook**.

[Screenshot: channel-name More actions menu with "Request Outgoing Webhook" highlighted]

**Integrations page:** Navigate to **Main Menu → Integrations → Outgoing Webhooks**. Same intercept behavior as above.

### Filling out an incoming webhook request

[Screenshot: The Request an Incoming Webhook form]

| Field | What it does |
|-------|--------------|
| **Target channel** | Pre-filled with the channel you're currently in (read-only). Navigate to the intended channel before opening the form if you need a different target. |
| **Display name** | A label for the webhook shown in the Integrations page. |
| **Description** | Optional. What system sends to this webhook and why. |

The webhook item is only shown in public and private channels — it's hidden in DMs and group messages, which can't be webhook targets.

### Filling out an outgoing webhook request

[Screenshot: The Request an Outgoing Webhook form]

| Field | What it does |
|-------|--------------|
| **Source channel** | Pre-filled with the channel you're currently in (read-only). Navigate to the intended channel first if needed. |
| **Display name** | A label for the webhook shown in the Integrations page. |
| **Callback URL** | Required. The `https://` endpoint that Mattermost will POST to. Must start with `https://` or `http://`. |
| **Description** | Optional. |

### After submitting

Click **Submit request**. You'll get a DM from the bot confirming the request was received. A second DM arrives when an approver acts on it.

---

## Approving requests

All webhook requests — incoming and outgoing — are posted to the **`mattermost-permissions-webhooks`** private channel. They use **two-step approval** (security + system, two different people). The card shows both status lines.

[Screenshot: A webhook request approval card with Approve/Deny buttons and webhook type and channel displayed]

When both steps are filled, the final approval creates the webhook using that approver's Mattermost session. On success, the requester receives a DM containing:
- The webhook URL (for incoming webhooks)
- A confirmation that the outgoing webhook is live (for outgoing webhooks)

If an incoming webhook URL cannot be delivered to the requester, the hook is deleted automatically so no unowned secret endpoint is left live.

[Screenshot: The DM the requester receives after a webhook approval, showing the webhook URL]

**Deny** closes the ticket and sends the requester a DM.

### A note on System Admin bypass

Webhooks have **no single-click System Admin bypass**. System Admins still go through the queue; they can fill the system step, but a security approver must fill the other.

### Approve failures

If webhook creation fails after both steps, the card stays pending with an error notice so the final approver can retry.

---

## Admin setup

Webhook requests use the plugin-managed `mattermost-permissions-webhooks` private channel, created automatically in the configured approval team when the plugin activates. All System Admins are added as members on creation.

Enable **Allow webhook requests** and configure the security/system approver User Attributes. See [Admin Guide](admin-guide.md).
