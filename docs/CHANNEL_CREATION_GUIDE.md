# Channel Requests — User Guide

Mattermost Request Hub lets members **request** a new channel instead of
creating one directly. The request goes to an approver; on approval the channel
is created, the requested members and Channel Admins are added, optionally placed
in a sidebar category, and everyone added gets an @-mention welcome message.

> Companion guides: [Channel Admin](CHANNEL_ADMIN_GUIDE.md) ·
> [teams](TEAM_REQUEST_GUIDE.md) · [bots](BOT_REQUEST_GUIDE.md) ·
> [webhooks](WEBHOOK_REQUEST_GUIDE.md) · [Admin Guide](admin-guide.md).
>
> In Mattermost, open the full in-app guide anytime with
> **`/mattermost-request-hub docs`**.

This guide covers:

- [Requesting a channel](#requesting-a-channel) — any member
- [Messaging while a request is pending](#messaging-while-a-request-is-pending) — two-way threads
- [Approving requests](#approving-requests) — approvers
- [Admin setup](#admin-setup) — system admins

---

## Requesting a channel

There are several ways to open the request form. They all open the same window.

### 1. Native: "+" in the sidebar

Click the **+** at the top of the channel sidebar and choose **Request new
channel**. For members who can't create channels directly, Mattermost's native
"Create new channel" action is automatically relabeled to this.

![Request new channel in the + menu](images/channel-request/mattermost-creation-plus-menu.png)

### 2. Right-side plug icon (Requests hub)

On the **far right** of the Mattermost window (the App Bar / plugin strip),
click the **plug** icon. That opens **Requests** (the plugin's request hub).
Choose **Request a Channel**.

If several plugins share the strip, the plug may open a short menu first —
pick **Requests**, then the hub tile. When the App Bar is off, the same control
appears as a channel-header plugin button.

![Plug icon on the right App Bar opening Requests](images/shared/mattermost-requests-plug-appbar.png)

### 3. Dropdown: channel More actions

Click the **channel name** at the top-left, then
**More actions → Request a Channel**.

### 4. Slash command

Type **`/mattermost-request-hub channel`** in any channel to open the request
dialog.

> Tip: **`/mattermost-request-hub docs`** opens this documentation in a simple
> browser page (all request types in one place).

### Filling out the request

![The Request a Channel form](images/channel-request/mattermost-creation-modal.png)

| Field | What it does |
|-------|--------------|
| **Domain prefix** | Appears only if your admin configured prefixes. Pick the category — the final URL is `<prefix><suffix>`. |
| **Channel name** | The display name people see (e.g. *SRE Team*). Required. |
| **URL suffix / URL name** | Optional. Leave blank to auto-generate from the channel name. A live **Preview** shows the final URL (e.g. `ops-sre-team`). |
| **Purpose** | Optional short description of the channel. |
| **Visibility** | **Public** (anyone can join) or **Private** (invite only). |
| **Members to add** | Search and add users who become regular members on approval. |
| **Channel Admins to add** | Search and add users who become Channel Admins on approval. |
| **Sidebar category** | Optional. Pick an existing category or type a new name. On approval the channel is placed there for you and listed members; a missing category is created automatically. |

**Members vs Channel Admins:** a user can be in only one of the two lists. If you
add someone to Channel Admins who's already a Member (or vice versa), they
**move** to the new list automatically — a badge in the dropdown shows where a
user is currently assigned.

Click **Submit request**. You'll see a confirmation, and you'll get a direct
message when an approver responds.

---

## Messaging while a request is pending

Every request opens a **linked pair of threads**: one on the approval card and
one in a DM from the bot. Replies are relayed both ways.

Use the **Thread** right-hand sidebar (open **Reply** on the card) — not a new
top-level channel or DM message.

1. An approver replies in the **approval Thread** — for example, *Why do you need
   this channel?* / *Who else should be a Channel Admin?*

![Approver asks a clarifying question in the Thread RHS](images/shared/mattermost-thread-approver-ask.png)

2. You see that question in your **DM Thread** with the bot as
   `Message from @admin:`.

![Requester DM Thread RHS showing the relayed question](images/shared/mattermost-thread-requester-dm.png)

3. You **reply in that Thread** (Reply to thread…). Your answer appears back on
   the approval Thread as `Reply from @you:` — reviewers never need you in the
   approval channel.

![Approval Thread RHS showing the requester's relayed reply](images/shared/mattermost-thread-relay-back.png)

Threads stay usable after approve/deny for follow-ups. Messaging the bot outside
a ticket thread gets a nudge to reply inside the specific request instead.

---

## Approving requests

Approvers (System Admins, plus Team Admins if your admin enabled it) receive each
request in the **approval channel** with **Approve** and **Deny** buttons.

![A channel creation request card with Approve/Deny buttons](images/channel-request/mattermost-creation-approval-card.png)

- **Approve** — creates the channel, adds the requested members, promotes the
  Channel Admins, places the channel in the requested sidebar category (if any),
  and posts a welcome message that @-mentions everyone added.
- **Deny** — the request is closed and the requester is notified by DM.

![The welcome message the bot posts in the new channel](images/channel-request/mattermost-creation-welcome.png)

Requests from **System Admins** who submit via the webapp Request Hub / modal
(not the slash-command dialog) skip this step — the channel is created
immediately. Slash-command dialogs always queue for approval (`ForceQueue`).

---

## Admin setup

Configure the plugin in **System Console → Plugins → Mattermost Request Hub**.

![Finding Channel Requests in the System Console](images/admin/mattermost-admin-settings.png)

| Setting | Purpose |
|---------|---------|
| **Approval team** | The team that owns plugin-managed approval channels. Required. |
| **Allow channel requests** | Enables channel-creation entry points. |
| **Channel domain prefixes** | **Required.** The prefix list requesters pick from (see below). An empty list rejects every channel request. |
| **Team Admins can approve requests** | When on, Team Admins of the approval team can approve/deny, not just System Admins. |
| **Per-team approval channels** | Default on. Creates `mattermost-channel-requests` in teams with a Team Admin or System Admin; those teams' channel requests go there (not the primary approval team). Ticket channels (including team-creation) live under sidebar category **Mattermost: Request Hub**. |

> To fully funnel non-admins through the request flow, remove the built-in
> **Create Public/Private Channel** permission from the System User role (System
> Console → User Management → Permissions). Mattermost's plugin API can't veto
> native channel creation.

### Domain prefixes

Each prefix is one row with a **prefix**, a **description**, and a **max suffix
length** (2–48 characters, capping the suffix only — not the prefix). A fresh
install starts empty:

![The domain-prefix editor, empty state](images/admin/mattermost-admin-prefixes.png)

Use **Load preset** to populate the table with a starter pack — **SRE / Ops**,
**Product org**, **Support / Success**, or **General org**. Loading a preset
updates prefixes you already have (description + max length) and appends any that
are missing. The **Live preview** at the bottom shows exactly what URL a request
will produce for a given prefix and typed suffix.

![Load preset open with the General org pack loaded, plus the live preview](images/admin/mattermost-admin-prefix-preview.png)

See also the [Admin Guide](admin-guide.md) and open **`/mattermost-request-hub docs`**
in Mattermost for the bundled HTML guide.
