# Team Membership & Guest Invites — User Guide

Mattermost Request Hub lets members request **adding existing users to a team**
and **guest invites** for a team. Both ticket types route to
`mattermost-team-requests` for System Admin approval.

> In Mattermost: **`/mattermost-request-hub docs`** opens the full HTML guide.
>
> Related: [Team Requests](TEAM_REQUEST_GUIDE.md) (create team / Team Admin).

---

## Request adding team members

Any authenticated user can nominate existing Mattermost users onto a team —
you do **not** need to already be a member of that team. System Admins review
and approve; that approval step is the access-control gate.

### How to open

- Slash: **`/mattermost-request-hub team-member`**
- Request Hub tile: **Request Team Members**
- Channel **More actions → Request Team Members**

### Form

| Field | Notes |
|-------|--------|
| Users to add | Existing Mattermost users (picker / multi-select) |
| Reason | Optional context for reviewers |

### On approve

Nominees are added to the target team via `CreateTeamMember`. You get a DM in
the ticket thread.

---

## Request a guest invite

You must be a **member of the target team** (System Admins may invite for any
team). Guest channel picks must belong to that same team.

### How to open

- Slash: **`/mattermost-request-hub team-guest`**
- Request Hub tile: **Request Guest Invite**
- Channel **More actions → Request Guest Invite** (public/private channels)

### Form

| Field | Notes |
|-------|--------|
| Guest email | **Optional.** If set, Mattermost emails a guest invitation on approve. |
| Channels | At least one channel the guest should access (hub modal uses the current channel) |
| Message / reason | Optional note for reviewers / invite email |

### On approve

1. You always receive a **shareable team invite link** in the ticket DM — forward
   it yourself if you did not provide an email.
2. If you provided an email, Mattermost also sends a **guest invitation email**
   (Guest Accounts must be enabled in System Console).

---

## Admin setup

Enable **Allow team member requests** and/or **Allow team guest requests** in
**System Console → Plugins → Mattermost Request Hub**. Ensure Guest Accounts are
enabled before relying on emailed guest invites.
