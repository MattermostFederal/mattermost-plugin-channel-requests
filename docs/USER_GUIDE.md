# Channel Requests — User Guide

The Channel Requests plugin lets members **request** a new channel instead of
creating one directly. Requests go to an approver, who approves or denies them.
On approval the channel is created, members and channel admins are added, and
everyone added gets an @-mention welcome message.

This guide covers three roles:

- [Requesting a channel](#requesting-a-channel) — anyone
- [Approving requests](#approving-requests) — approvers
- [Admin setup](#admin-setup) — system admins

> **Screenshots:** placeholders below point at `docs/images/<name>.png`. Drop your
> screenshots in `docs/images/` using those exact names and they'll render.

---

## Requesting a channel

There are three ways to open the request form. All open the same modal.

### 1. From the "+" in the sidebar

Click the **+** next to a category (or the main **+** in the sidebar header) and
choose **Request new channel**. For members who can't create channels directly,
the native "Create new channel" action is automatically relabeled to this.

<!-- Screenshot: the sidebar "+" menu open, with "Request new channel" highlighted -->
![Request new channel from the + menu](images/request-plus-menu.png)

### 2. From the channel header

Click the **Request Channel** icon in the channel header toolbar.

<!-- Screenshot: the channel header with the Request Channel button highlighted -->
![Request Channel header button](images/request-header-button.png)

### 3. From the slash command

Type `/channel-request` in any message box and press Enter.

<!-- Screenshot: typing /channel-request in the message box -->
![Slash command](images/request-slash-command.png)

### Filling out the request

<!-- Screenshot: the full Request a Channel modal filled in -->
![Request a Channel modal](images/request-modal.png)

| Field | What it does |
|-------|--------------|
| **Domain prefix** | Appears only if your admin configured prefixes. Pick the category — the final URL is `<prefix><suffix>`. |
| **Channel name** | The display name people see (e.g. *Marketing Team*). |
| **URL name / suffix** | Optional. The URL part after the prefix. Leave blank to auto-generate from the channel name. A live **Preview** shows the final URL. |
| **Purpose** | Optional short description of the channel. |
| **Visibility** | **Public** (anyone can join) or **Private** (invite only). |
| **Members to add** | Search and add users who become regular members on approval. |
| **Channel Admins to add** | Search and add users who become channel admins on approval. |

**Members vs Channel Admins:** a user can be in only one of the two lists. If you
add someone to Channel Admins who's already a Member (or vice versa), they
**move** to the new list automatically. A badge in the dropdown shows where a
user is currently assigned.

<!-- Screenshot: the two pickers with a user showing a "Channel Admin" / "Member" badge -->
![Member and admin pickers](images/request-pickers.png)

Click **Submit request**. You'll see a confirmation and get a direct message when
an approver responds.

<!-- Screenshot: submission confirmation message -->
![Request submitted](images/request-submitted.png)

---

## Approving requests

Approvers (System Admins, plus Team Admins if your admin enabled it) receive
each request in the **approval channel** with **Approve** and **Deny** buttons.

<!-- Screenshot: an approval request card in the approval channel with Approve/Deny buttons -->
![Approval request card](images/approval-card.png)

- **Approve** — creates the channel, adds the requested members, promotes the
  channel admins, and posts a welcome message that @-mentions everyone added.
- **Deny** — the request is closed and the requester is notified by DM.

<!-- Screenshot: the welcome message in a newly created channel mentioning added users -->
![Welcome message](images/welcome-message.png)

Requests from users on the **auto-approve list** (and from System Admins) skip
this step — the channel is created immediately.

---

## Admin setup

Configure the plugin in **System Console → Plugins → Channel Requests**.

<!-- Screenshot: the Channel Requests settings page in System Console -->
![Plugin settings](images/admin-settings.png)

| Setting | Purpose |
|---------|---------|
| **Approval team** | The team whose channel receives requests. Required. |
| **Approval channel** | The channel where Approve/Deny cards are posted. Required. |
| **Channel domain prefixes** | The prefix list requesters pick from (see below). Leave empty for free-form URLs. |
| **Team Admins can approve requests** | When on, Team Admins of the approval team can approve/deny, not just System Admins. |
| **Auto-approve requests from these users** | Requests from these users skip approval. |
| **Audit channel ID** | Optional. Posts an audit line for every approve/deny. |

### Domain prefixes

Each prefix has a **name**, a **description**, and a **max length** for the suffix
(2–32 characters). Use **Load preset** to populate the table with a starter pack
(SRE / Ops, Product org, Support / Success, or General org), then edit as needed.
Loading a preset updates prefixes you already have and adds any that are missing.

<!-- Screenshot: the prefix editor table with the Load preset dropdown open -->
![Prefix editor](images/admin-prefixes.png)

The **Live preview** at the bottom shows exactly what URL a request will produce
for a given prefix and typed name.

<!-- Screenshot: the prefix editor live preview -->
![Prefix live preview](images/admin-prefix-preview.png)
