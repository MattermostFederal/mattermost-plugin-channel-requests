# Requesting Channel Admin Privileges

Channel Admin is a per-channel role that allows managing members, pinning posts, and editing channel settings. If you don't have that role, you can request it — or request it for someone else — through the permissions plugin.

> This is the companion to [requesting a new channel](request-channel.md). That guide covers creating channels from scratch; this one covers admin rights on a channel that already exists.

This guide covers three roles:

- [Requesting a Channel Admin](#requesting-a-channel-admin) — any non-admin member
- [Approving requests](#approving-requests) — approvers
- [Admin setup](#admin-setup) — system admins

---

## Requesting a Channel Admin

The request option is **only shown to members who aren't already a Channel Admin or System Admin** on the channel. If you manage the channel directly, you won't see it.

There are two entry points, both opening the same form.

### 1. From the Members panel

Open the **Members** list by clicking the member count in the channel header. Next to **Add** you'll see a **Request Admin** button.

![The Request Admin button in the Members panel](images/mattermost-promotion-members-button.png)

### 2. From the channel-name menu

Click the channel name at the top-left, then **More actions → Request Channel Admin**.

![Request Channel Admin under the channel-name menu](images/mattermost-promotion-menu.png)

### Filling out the request

![The Request a Channel Admin form](images/mattermost-promotion-modal.png)

| Field | What it does |
|-------|--------------|
| **Make Channel Admin** | Search and add one or more people to be promoted. Start typing a name and pick from the list. |

The channel is pre-selected — you're always requesting admin rights on the channel you're currently in.

Click **Submit request**. You'll get a DM confirmation from the bot, and another DM when an approver responds. If no approval channel has been configured yet, you'll see an error message explaining what's missing rather than the request silently disappearing.

---

## Approving requests

The request is posted to the **approval channel**. Every System Admin and the Team Admins of the channel's team are @-mentioned. Any of them can act on the request.

![A Channel Admin request card with Approve/Deny buttons](images/mattermost-promotion-approval-card.png)

- **Approve** — each nominee is added to the channel (if not already a member) and promoted to Channel Admin. The requester is notified by DM.
- **Deny** — the request is closed and the requester is notified by DM.

Requests from System Admins and users on the **auto-approve list** skip this step — nominees are promoted immediately.

---

## Admin setup

Channel Admin requests use the same approval settings as channel-creation requests, in **System Console → Plugins → Channel Requests**:

| Setting | Purpose |
|---------|---------|
| **Approval team** | The team that owns the approval channel. Required. |
| **Approval channel** | Where Channel Admin request cards are posted. Required — if unset, requesters see an error instead of the request being dropped silently. |
| **Team Admins can approve requests** | Also lets Team Admins of the approval team approve/deny. |
| **Auto-approve requests from these users** | Requests from these users skip approval. |

See [Admin setup in the channel creation guide](request-channel.md#admin-setup) for the full settings reference.
