# Channel Admin Requests — User Guide

Mattermost Request Hub also lets a member **request that someone be made a
Channel Admin** on a channel that already exists — without giving every member
the ability to manage the channel directly. The request goes to an approver;
on approval the nominee is added to the channel (if not already a member) and
granted the Channel Admin role.

> Companion guides: [new channels](CHANNEL_CREATION_GUIDE.md) ·
> [teams](TEAM_REQUEST_GUIDE.md) · [bots](BOT_REQUEST_GUIDE.md) ·
> [webhooks](WEBHOOK_REQUEST_GUIDE.md) · [Admin Guide](admin-guide.md).
>
> In Mattermost, open the full in-app guide anytime with
> **`/mattermost-request-hub docs`**.

This guide covers:

- [Requesting a Channel Admin](#requesting-a-channel-admin) — any non-admin member
- [Messaging while a request is pending](#messaging-while-a-request-is-pending) — two-way threads
- [Approving requests](#approving-requests) — approvers
- [Admin setup](#admin-setup) — system admins

---

## Requesting a Channel Admin

There are several ways to open the request form. They all open the same window.
The option is **only shown to members who aren't already an admin** of the
channel — if you're already a Channel Admin (or a System Admin), you can manage
the channel directly and won't see the menu items (the slash command still works
if the feature is enabled).

### 1. Native: Members panel

Open the channel's **Members** list (click the member count in the channel
header). Next to **Add** you'll see a **Request Admin** button.

![The Request Admin button in the Members panel](images/channel-admin-request/mattermost-promotion-members-button.png)

### 2. Dropdown: channel More actions

Click the channel name at the top-left, then **More actions → Request Channel
Admin**.

![Request Channel Admin under the channel-name menu](images/channel-admin-request/mattermost-promotion-menu.png)

### 3. Right-side plug icon (Requests hub)

On the **far right** of the window (the App Bar / plugin strip), click the
**plug** icon → **Requests** → **Request Channel Admin**.

![Plug icon on the right App Bar opening Requests](images/shared/mattermost-requests-plug-appbar.png)

### 4. Slash command

Type **`/mattermost-request-hub channel-admin`** in any channel to open the
request dialog (you'll pick the channel and nominees in the form).

> Tip: **`/mattermost-request-hub docs`** opens the full documentation page.

### Filling out the request

![The Request a Channel Admin form](images/channel-admin-request/mattermost-promotion-modal.png)

| Field | What it does |
|-------|--------------|
| **Make Channel Admin** | Search and add one or more people to be promoted to Channel Admin. Start typing a name and pick from the list. |

Everyone you add is promoted to Channel Admin once an approver accepts the
request. Anyone who isn't already in the channel is added to it first.

Click **Submit request**. You'll see a confirmation, and you'll get a direct
message when an approver responds. If your admin hasn't configured an approval
channel yet, you'll be told so directly instead of the request disappearing
silently — pass that message along to a System Admin.

---

## Messaging while a request is pending

Channel Admin requests use the same **two-way Thread relay** as channel creation.
Open the **Thread** RHS from the card (**Reply**) — don't post a new top-level
message.

1. An approver asks in the **approval Thread** — for example, *Why does this
   person need Channel Admin?*

![Approver asks a clarifying question in the Thread RHS](images/shared/mattermost-thread-approver-ask.png)

2. You see `Message from @admin:` in your **DM Thread** with the bot.

![Requester DM Thread RHS showing the relayed question](images/shared/mattermost-thread-requester-dm.png)

3. You **reply in that Thread**. Your answer is posted back to the approval
   Thread as `Reply from @you:`.

![Approval Thread RHS showing the requester's relayed reply](images/shared/mattermost-thread-relay-back.png)

You never need access to the approval channel. Use **Reply to thread…** in the
ticket DM Thread (not a new root DM to the bot).

---

## Approving requests

The request is posted to the **approval channel**, and everyone who can act on it
is @-mentioned there: every **System Admin** plus the **Team Admins** of the
channel's team. Any of them can **Approve** or **Deny**.

![A Channel Admin request card with Approve/Deny buttons](images/channel-admin-request/mattermost-promotion-approval-card.png)

- **Approve** — each nominee is added to the channel (if needed) and promoted to
  Channel Admin. A bot message in the channel announces who can now manage it,
  and the requester is notified by DM.
- **Deny** — the request is closed and the requester is notified by DM.

Requests from **System Admins** skip this
step — the nominees are promoted immediately.

---

## Admin setup

Channel Admin requests reuse the same approval settings as channel-creation
requests, in **System Console → Plugins → Channel Requests** (or
**Mattermost Request Hub**):

| Setting | Purpose |
|---------|---------|
| **Approval team** | The team that owns plugin-managed approval channels. Required. |
| **Allow channel admin requests** | Enables Channel Admin entry points. |
| **Team Admins can approve requests** | Also lets Team Admins of the approval team approve/deny. (Team Admins of the *requested channel's* team can always act on that channel's admin requests.) |
| **Per-team approval channels** | Default on. Same per-team routing as channel creation — requests go to that team's `mattermost-channel-requests` when it has a Team Admin or System Admin. |

See the [channel-creation guide](CHANNEL_CREATION_GUIDE.md#admin-setup) for the full settings
reference, or run **`/mattermost-request-hub docs`** in Mattermost.
