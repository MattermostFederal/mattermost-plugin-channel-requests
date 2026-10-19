# Requesting a Bot Account

A bot account is a dedicated Mattermost user identity for automated integrations — CI pipelines, monitoring agents, deployment notifiers, and anything else that needs to post messages or call the API on behalf of a service rather than a person. Bot accounts can also have access tokens and webhooks associated with them, and you can request all three in a single ticket.

This guide covers three roles:

- [Requesting a bot](#requesting-a-bot) — any member
- [Approving requests](#approving-requests) — approvers
- [Admin setup](#admin-setup) — system admins

---

## Requesting a bot

There are two entry points, both opening the same form.

### 1. From the channel-name menu

Click the channel name at the top-left, then **More actions → Request a Bot**.

[Screenshot: channel-name More actions menu with "Request a Bot" highlighted]

### 2. From the Integrations page

Navigate to **Main Menu → Integrations → Bot Accounts**. If you don't have the System Admin role, clicking **Bot Accounts** opens the request modal instead of the native bot management page.

[Screenshot: Mattermost Integrations page showing Bot Accounts tile]

### Filling out the request

[Screenshot: The Request a Bot Account form, fully expanded with all optional sections visible]

#### Required fields

| Field | What it does |
|-------|--------------|
| **Bot username** | The username the bot posts as. Lowercase letters, numbers, and hyphens only. 3–22 characters. Example: `deploy-notifier`. |
| **Display name** | The friendly name shown in messages. Example: `Deploy Notifier`. |
| **Description** | Optional. What this bot does and what systems it connects. Shown in the bot management page. |

#### Optional: Access token

**Request an access token** is checked by default. An access token is the credential your external system uses to authenticate to Mattermost as this bot — you need one to make any API call.

> **Token delivery and expiry:** the token is sent in a direct message from the bot when the request is approved. That DM auto-deletes after 72 hours. Save the token somewhere secure — your secrets manager, CI environment variables, or a password vault — before the DM disappears. The token itself does not expire; only the DM containing it does.

If your bot doesn't need API access (it only receives messages via an incoming webhook), uncheck this.

#### Optional: Incoming webhook

Check **Request an incoming webhook** to also set up a webhook that posts messages *into* Mattermost on behalf of this bot.

[Screenshot: The incoming webhook section expanded, showing display name and target channel fields]

| Field | What it does |
|-------|--------------|
| **Display name** | Pre-filled with `{username} incoming`. Edit if needed. |
| **Target channel** | Pre-filled with the channel you're currently in (read-only). Switch to that channel first if you want a different target. |
| **Description** | Optional. |

On approval, the webhook URL is included in the same DM as the bot confirmation.

#### Optional: Outgoing webhook

Check **Request an outgoing webhook** if you want Mattermost to forward messages from a channel to an external URL — for example, to trigger a pipeline when someone types a keyword.

[Screenshot: The outgoing webhook section expanded, showing display name, source channel, and callback URL fields]

| Field | What it does |
|-------|--------------|
| **Display name** | Pre-filled with `{username} outgoing`. Edit if needed. |
| **Source channel** | Pre-filled with the channel you're currently in (read-only). |
| **Callback URL** | Required. The `https://` endpoint that receives the webhook payload. |
| **Description** | Optional. |

### Submitting

Click **Submit request**. One approval ticket is created that covers the bot account, token, and any webhooks you checked. You'll get a DM confirmation from the bot, and another DM when an approver acts on it.

---

## Approving requests

Each bot request is posted to the **`mattermost-permissions-bots`** private channel in the approval team. Bot requests use **two-step approval**: one security approver and one system approver (two different people). The card shows both status lines.

[Screenshot: A bot approval card showing bot details, token/webhook options requested, and Approve/Deny buttons]

Each eligible approver clicks **Approve** once to fill their step. When both steps are complete, the plugin:

1. Creates the bot account.
2. If a token was requested: generates a personal access token for the bot.
3. If an incoming webhook was requested: creates the webhook on the target channel.
4. If an outgoing webhook was requested: creates the webhook on the source channel.
5. Sends results to the requester via DM (tokens/URLs auto-delete after 72 hours).

Any eligible approver may **Deny** at any time.

### DM to the requester

[Screenshot: The DM the requester receives after approval, showing bot username, access token, and webhook URLs]

The DM includes:
- Bot account created confirmation
- Access token value (if requested) — **this message auto-deletes after 72 hours**
- Incoming webhook URL (if requested)
- Outgoing webhook confirmation (if requested)

If anything failed, the DM describes what couldn't be created and what the requester or an admin should do next.

**Deny** closes the ticket and sends the requester a DM.

---

## Admin setup

Bot requests use the plugin-managed `mattermost-permissions-bots` private channel, which is created automatically in the configured approval team when the plugin activates. All System Admins are added as members on creation.

Enable **Allow bot account requests** and configure the security/system approver User Attributes. See [Admin Guide](admin-guide.md).

### Token security note

Access tokens generated at approve-time are passed to the requester via DM and not stored by the plugin. If a token is compromised, rotate it in **System Console → User Management → Bot Accounts → {bot name} → Create New Token** and revoke the old one. The requester's DM auto-deletes after 72 hours, but the token remains valid until explicitly revoked.
