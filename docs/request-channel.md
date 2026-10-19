# Requesting a New Channel

The Mattermost Permissions plugin routes channel creation through an approval workflow for users who don't have the native create-channel permission. Submit a request, an approver reviews it, and on approval the channel is created with the members and admins you specified.

> System Admins bypass the approval queue — they create channels directly with the same modal.

This guide covers three roles:

- [Requesting a channel](#requesting-a-channel) — any member
- [Approving requests](#approving-requests) — approvers
- [Admin setup](#admin-setup) — system admins

---

## Requesting a channel

There are two entry points, both opening the same form.

### 1. From the sidebar "+" menu

Click the **+** at the top of the channel sidebar and choose **Request new channel**. For members who can't create channels directly, Mattermost's native "Create new channel" action is automatically relabeled to this.

![The + sidebar menu showing "Request new channel"](images/mattermost-creation-plus-menu.png)

### 2. From the channel header toolbar

Click the **Request Channel** icon in the channel header. This is the plugin's own button and always opens the request form regardless of your permissions.

### 3. From the channel-name menu

Click the channel name at the top-left, then **More actions → Request a Channel**.

### Filling out the request

![The Request a Channel form](images/mattermost-creation-modal.png)

| Field | What it does |
|-------|--------------|
| **Domain prefix** | Only shown if your admin configured prefixes. Pick the category — the final URL becomes `<prefix><suffix>`. |
| **Channel name** | The display name people see (e.g. *SRE Team*). Required. |
| **URL suffix** | Leave blank to auto-generate from the channel name. A live **Preview** shows the final URL (e.g. `ops-sre-team`). |
| **Purpose** | Optional short description shown in the channel header. |
| **Visibility** | **Public** (anyone can join and search) or **Private** (invite only). |
| **Members to add** | Users who become regular members on approval. |
| **Channel Admins to add** | Users who become Channel Admins on approval. |

A user can be in either Members or Channel Admins, not both. Adding someone to one list removes them from the other automatically.

Click **Submit request**. You'll receive a DM confirmation from the bot, and another DM when an approver responds.

---

## Approving requests

Each request is posted to the configured **approval channel**. System Admins (and Team Admins if your admin enabled it) see an **Approve** and **Deny** button on the card.

![A channel creation request card with Approve/Deny buttons](images/mattermost-creation-approval-card.png)

- **Approve** — creates the channel, adds the requested members, promotes the Channel Admins, and posts a welcome message that @-mentions everyone added.

![The welcome message the bot posts in the new channel](images/mattermost-creation-welcome.png)

- **Deny** — closes the request. The requester is notified by DM.

Requests from users on the **auto-approve list** skip this step — the channel is created immediately.

---

## Admin setup

Configure in **System Console → Plugins → Channel Requests**.

![The Channel Requests settings page in System Console](images/mattermost-admin-settings.png)

| Setting | Purpose |
|---------|---------|
| **Approval team** | The team that owns the approval channel. Required. |
| **Approval channel** | Where Approve/Deny cards are posted. Required. |
| **Channel domain prefixes** | The prefix list requesters pick from. Leave empty for free-form URLs. |
| **Team Admins can approve requests** | Lets Team Admins of the approval team approve/deny, not just System Admins. |
| **Auto-approve requests from these users** | Requests from these users skip approval. |

To fully route non-admins through the request flow, remove the built-in **Create Public/Private Channel** permission from the System User role in **System Console → User Management → Permissions**.

### Domain prefixes

Each row has a **prefix**, a **description**, and a **max suffix length** (2–32 characters). Use **Load preset** to populate from a starter pack.

![The domain-prefix editor](images/mattermost-admin-prefixes.png)

The **Live preview** at the bottom shows exactly what URL a given prefix and suffix produce.

![Domain prefix editor with preview](images/mattermost-admin-prefix-preview.png)
