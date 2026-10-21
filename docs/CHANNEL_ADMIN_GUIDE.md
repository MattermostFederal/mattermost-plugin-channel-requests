# Channel Admin Requests — User Guide

Mattermost Request Hub also lets a member **request that someone be made a
Channel Admin** on a channel that already exists — without giving every member
the ability to manage the channel directly. The request goes to an approver;
on approval the nominee is added to the channel (if not already a member) and
granted the Channel Admin role.

> Companion guides: [new channels](CHANNEL_CREATION_GUIDE.md) ·
> [teams](TEAM_REQUEST_GUIDE.md) ·
> [team membership & guests](TEAM_MEMBERSHIP_GUIDE.md) ·
> [bots](BOT_REQUEST_GUIDE.md) · [webhooks](WEBHOOK_REQUEST_GUIDE.md) ·
> [Admin Guide](admin-guide.md).
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

Same form from every entry point. The option is **only shown to members who
aren't already an admin** of the channel — if you're already a Channel Admin
(or a System Admin), you can manage the channel directly and won't see the menu
items (the slash command still works if the feature is enabled).

### How to open

- **Members:** channel Members panel → **Request Admin**
- **Dropdown:** channel name → More actions → **Request Channel Admin**
- **Hub:** right App Bar **plug** → Requests → **Request Channel Admin**
- **Slash:** `/mattermost-request-hub channel-admin`

<p>
<img src="images/channel-admin-request/mattermost-promotion-members-button.png" alt="Members panel" width="380">
<img src="images/channel-admin-request/mattermost-promotion-menu.png" alt="More actions" width="240">
<img src="images/shared/mattermost-requests-plug-appbar.png" alt="Right App Bar (plug)" width="56">
<img src="images/shared/mattermost-request-hub.png" alt="Request Hub" width="440">
</p>

> Tip: **`/mattermost-request-hub docs`** opens the full documentation page.

### Form

<img src="images/channel-admin-request/mattermost-promotion-modal.png" alt="Request a Channel Admin form" width="480">

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

- **Approver:** ask in the approval Thread (e.g. *Why does this person need Channel Admin?*)
- **Requester:** see `Message from @admin:` in your DM Thread with the bot
- **Reply:** answer in that DM Thread; the approval Thread shows `Reply from @you:`

<p>
<img src="images/shared/mattermost-thread-approver-ask.png" alt="Approval Thread" width="300">
<img src="images/shared/mattermost-thread-requester-dm.png" alt="Requester DM" width="720">
<img src="images/shared/mattermost-thread-relay-back.png" alt="Relay back" width="300">
</p>

You never need access to the approval channel. Use **Reply to thread…** in the
ticket DM Thread (not a new root DM to the bot).

---

## Approving requests

The request is posted to the **approval channel**, and everyone who can act on it
is @-mentioned there: every **System Admin** plus the **Team Admins** of the
channel's team. Any of them can **Approve** or **Deny**.

<img src="images/channel-admin-request/mattermost-promotion-approval-card.png" alt="Channel Admin approval card" width="720">

- **Approve** — each nominee is added to the channel (if needed) and promoted to
  Channel Admin. A bot message in the channel announces who can now manage it,
  and the requester is notified by DM.
- **Deny** — the request is closed and the requester is notified by DM.

Requests from **System Admins** skip this
step — the nominees are promoted immediately.

---

## Admin setup

Channel Admin requests reuse the same approval settings as channel-creation
requests, in **System Console → Plugins → Mattermost Request Hub**:

| Setting | Purpose |
|---------|---------|
| **Approval team** | The team that owns plugin-managed approval channels. Required. |
| **Allow channel admin requests** | Enables Channel Admin entry points. |
| **Team Admins can approve requests** | Also lets Team Admins of the approval team approve/deny. (Team Admins of the *requested channel's* team can always act on that channel's admin requests.) |
| **Per-team approval channels** | Default on. Same per-team routing as channel creation — requests go to that team's `mattermost-channel-requests` when it has a Team Admin or System Admin. |

See the [channel-creation guide](CHANNEL_CREATION_GUIDE.md#admin-setup) for the full settings
reference, or run **`/mattermost-request-hub docs`** in Mattermost.
