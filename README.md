# Mattermost Request Hub

Governance plugin for Mattermost: non-admins **request** channels, Channel Admin, teams, team membership, guest invites, bots, and webhooks. Approvers act from plugin-managed channels with two-way messaging threads.

**Plugin ID:** `com.mattermostfederal.mattermost-plugin-request-hub`  
**Slash command:** `/mattermost-request-hub`  
**Sidebar category:** Mattermost: Request Hub

> **Breaking (0.5.0):** plugin ID changed from `com.mattermostfederal.channel-requests`. Uninstall the old bundle before installing this one — settings and pending KV tickets do not migrate.

## Features

- Request Hub (App Bar) plus More actions, slash commands, and native menu overrides
- Per-type enable toggles in System Console
- Approval routing: channel requests (root or per-team), team-scope tickets in `mattermost-team-requests`, bots/webhooks in dedicated channels
- Soft-squash approval cards and DM ticket threads with message relay
- Bot/webhook two-step approval via User Attributes

## Documentation

- [Channel Requests](docs/CHANNEL_CREATION_GUIDE.md)
- [Channel Admin](docs/CHANNEL_ADMIN_GUIDE.md)
- [Team Requests](docs/TEAM_REQUEST_GUIDE.md) — create team / Team Admin
- [Team Membership & Guests](docs/TEAM_MEMBERSHIP_GUIDE.md) — add members / guest invite link
- [Bot Accounts](docs/BOT_REQUEST_GUIDE.md)
- [Webhooks](docs/WEBHOOK_REQUEST_GUIDE.md)
- [Admin Guide](docs/admin-guide.md)

In Mattermost, **`/mattermost-request-hub docs`** opens the bundled HTML guide.

## Build and deploy

```sh
make dist    # builds dist/com.mattermostfederal.mattermost-plugin-request-hub-*.tar.gz
make deploy  # optional: deploy to a running Mattermost server
```

## Configuration

**System Console → Plugins → Mattermost Request Hub**

- Set **Approval team**
- Enable the request types you need (team member / guest default off)
- Configure channel domain prefixes for channel creation
- For guests: enable Guest Accounts on the server before relying on emailed invites

## License

See [LICENSE](LICENSE).
