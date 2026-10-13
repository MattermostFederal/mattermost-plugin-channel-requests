# Mattermost Plugin: Channel Requests

A Mattermost plugin that lets non-admins **request** things for approval instead of creating them directly — channels, teams, Team Admin rights, incoming webhooks, and bot tokens. Each request type has an admin on/off toggle. On approval the plugin performs the action and notifies the requester; System Admins bypass approval. Webhooks and bot tokens require **two-step approval** (a security approver and a system approver).

Requires Mattermost server **v10.9+** (uses User Attributes for approver pools).

## Features

- **`/request` command**: Non-admins request things via `/request <type>` — `channel`, `team`, `team-admin`, `webhook`, or `bot-token`. `/channel-request` remains as an alias for `/request channel`. Channel and team requests also have webapp modals + channel-header buttons; the rest are slash-command-only.
- **Per-type toggles**: Admins enable/disable each request type independently (channel defaults on; the rest default off). Disabled types are hidden and their submissions rejected server-side.
- **Channels & teams**: Approving creates the channel/team, adds the requested members, promotes admins, and (for teams) makes the requester a Team Admin.
- **Team Admin & Channel Admin requests**: Request that people be promoted to Team Admin (on a team) or Channel Admin (on a channel). Routed to the relevant admins.
- **Webhooks & bot tokens (two-step)**: Require one **security** and one **system** approval, by two different people, identified by configurable **User Attributes**. The resulting webhook URL / bot token is DM'd privately to the requester — never posted in the approval channel.
- **Admin approval**: Requests are posted to a configured approval channel with Approve / Deny buttons; the requester is notified by DM either way. An optional audit channel records every decision.

## Documentation

In-app help ships under **`public/help/`** (linked from the request modals): overview & requesting, approving requests, and admin setup.

## Configuration

In **System Console → Plugins → Channel Requests**, set:

- **Allow … requests** — per-type toggles (channel / team / team-admin / webhook / bot-token).
- **Approval Team / Approval Channel** — where Approve/Deny cards are posted. **Required.**
- **Channel domain prefixes** — the prefix list requesters pick from for channel URLs.
- **Team Admins can approve requests** — let approval-team Team Admins act, not just System Admins.
- **Security / System approver attribute** — names of the User Attributes marking the two approver pools for webhook and bot-token requests (managed in User Management → Attributes).
- **Auto-approve users** — requests from these users skip approval (does not apply to two-step requests).
- **Audit channel ID** — optional; posts an audit line per decision.

> To fully funnel non-admins through the request flow, remove the built-in **Create Public/Private Channel** permission from the System User role (System Console → User Management → Permissions). Mattermost's plugin API cannot veto native channel creation.
>
> **Webhook requests** additionally require: a Site URL set, personal access tokens enabled, incoming webhooks enabled, and the plugin bot permitted to manage incoming webhooks.

## Build and deploy

```sh
make dist    # build the plugin bundle
make deploy  # build and deploy to a running Mattermost server
```

## Common commands

- `make dist` - build the plugin bundle
- `make check-style` - lint Go and webapp code
- `make test` - run tests
- `make deploy` - deploy to the Mattermost server specified by `MM_SERVICESETTINGS_SITEURL` and `MM_ADMIN_TOKEN`

## Vulnerability scanning

This plugin ships with a CycloneDX SBOM and grype-based vulnerability audit:

- `make sbom` - generate CycloneDX SBOMs for the Go server and npm webapp into `dist/sbom/`
- `make sbom-scan` - run grype against the SBOMs and fail on high or critical CVEs
- `make sbom-audit` - `sbom` then `sbom-scan` in one step

Suppress known-false-positive findings by adding entries to `.grype.yaml` with a short reason. Typical reasons include: dev-only transitive dependency (not in the shipped bundle) or runtime dependency that Mattermost externalizes (e.g. react, redux).

See the [Mattermost plugin developer docs](https://developers.mattermost.com/extend/plugins/) for more information.
