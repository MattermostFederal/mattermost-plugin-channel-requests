# Mattermost Plugin: Channel Requests

A Mattermost plugin that lets non-admins **request** new channels for approval instead of creating them directly. On approval, the plugin creates the channel and adds the requester plus any designated members and Channel Admins. System Admins and users on an auto-approve list bypass approval and create channels immediately.

## Features

- **Request workflow**: Non-admins submit a channel request via the `/channel-request` slash command (native dialog with a member picker, or `create`, `admin` and `prefixes` subcommands to skip the form), a "Request Channel" channel-header button, or the sidebar "+" menu (relabeled "Request new channel" for non-admins).
- **Admin approval**: Requests are posted to a configured approval channel with Approve / Deny buttons. System Admins can act, and so can Team Admins of the approval team when **Team Admins can approve requests** is on. Only a System Admin can approve their own request. Approving creates the channel and adds the requester + designated members; denying notifies the requester. Either way the requester gets a DM.
- **Admin bypass**: Requests from System Admins and from users on the auto-approve list create the channel immediately.
- **Configurable naming**: Admins define a list of channel prefixes (`team-`, `ops-`, ...), each with a description and a maximum suffix length. Requesters pick a prefix, and the channel URL is the prefix plus a suffix. At least one prefix is required before requests can be made.
- **Agents tools**: When **Offer tools to Agents** is on, an agent from the Mattermost Agents plugin can list prefixes and file channel and Channel Admin requests for the user it's talking to, under the same rules. Agents can't approve or deny.
- **Channel Admin requests**: On an existing channel, a non-admin member can request that someone be made a Channel Admin, via a "Request Admin" button in the Members panel, the channel-name menu, or `/channel-request admin @user`. The request is posted to the approval channel, @-mentioning the System Admins and the channel's Team Admins, who Approve / Deny.

## Documentation

- [Channel Requests: User Guide](docs/CHANNEL_CREATION_GUIDE.md): requesting and approving **new channels**.
- [Channel Admin Requests: User Guide](docs/CHANNEL_ADMIN_GUIDE.md): requesting **admin rights on an existing channel**.
- [Releasing](docs/RELEASING.md): how to cut a release.
- Built-in help, served by the plugin at `/plugins/com.mattermostfederal.channel-requests/public/help/help.html`, with a slash command reference and a troubleshooting page. The System Console settings page links to it.

## Configuration

In **System Console → Plugins → Channel Requests**, set:

- **Approval team**: the team containing the approval channel, picked from a dropdown. Recommended.
- **Approval channel**: the channel where requests are posted, picked from a dropdown. Recommended: if it isn't set, or the channel is missing or archived, each request is sent by direct message to every System Admin instead.
- **Channel domain prefixes**: the prefixes requesters pick from, with descriptions and suffix rules. At least one is required.
- **Team Admins can approve requests**: optional; lets Team Admins of the approval team act on channel-creation requests.
- **Auto-approve requests from these users**: optional; these users' requests skip approval.
- **Pending requests per user**: how many requests of each kind (new channel, Channel Admin) one user can have waiting for review. Default 5, up to 1000; 0 means no limit.
- **Request expiry (days)**: pending requests older than this can no longer be approved. Default 30, at most 3650; 0 means requests never expire.
- **Audit channel ID**: optional; every approve/deny is logged to this channel, which must be a private channel.
- **Offer tools to Agents**: optional, off by default; lets the Mattermost Agents plugin file requests on a user's behalf.

Requires Mattermost 11.3 or later.

See the [user guide](docs/CHANNEL_CREATION_GUIDE.md#admin-setup) for details.

> To fully funnel non-admins through the request flow, remove the built-in **Create Public/Private Channel** permission from the System User role (System Console → User Management → Permissions). Mattermost's plugin API cannot veto native channel creation.

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

## PR preview environments

Add the `preview` label to a pull request to stand up a throwaway Mattermost server with that PR's bundle installed. The URL is posted on the PR. Removing the label or closing the PR tears it down.

`.github/workflows/preview.yml` is a thin copy of the shared workflow in the private `MattermostFederal/pr-preview` repo, checked out at its protected `v1` tag. It never checks out or runs PR code: it waits for `build.yml` to pass on the PR's head commit and deploys the `plugin-bundle` artifact that run uploaded. A fork PR deploys only the commit that was labeled; a later push tears the preview down until a maintainer re-applies the label.

What the preview does beyond installing the bundle lives in `.preview/`, which pr-preview reads from the PR's base branch, so a change to it reaches previews only after it merges. `deploy.sh` runs after the bundle is installed: it creates `~channel-requests` in team `test`, points the plugin's approval settings at it, and removes the built-in create-channel permissions from non-admins so they go through the request flow. Keep it in step with `make docker-setup` and `make deploy`. A `mattermost.env` file there would add `MM_*` settings to the server; none are needed today.

One-time setup:

- create the label: `gh label create preview`
- make sure the org variables `PREVIEW_CHECKOUT_APP_ID` and `PREVIEW_AWS_ROLE_ARN` and the org secrets `PREVIEW_CHECKOUT_APP_PRIVATE_KEY` and `PREVIEW_ADMIN_SECRET` are visible to this repo
- optionally set `PREVIEW_APP` (hostname prefix, default is the repo name without `mattermost-plugin-`) or `PREVIEW_MM_IMAGE_TAG` in the workflow's `env:` block

## Vulnerability scanning

This plugin ships with a CycloneDX SBOM and grype-based vulnerability audit:

- `make sbom` - generate CycloneDX SBOMs for the Go server and npm webapp into `dist/sbom/`
- `make sbom-scan` - run grype against the SBOMs and fail on high or critical CVEs
- `make sbom-audit` - `sbom` then `sbom-scan` in one step

Suppress known-false-positive findings by adding entries to `.grype.yaml` with a short reason. Typical reasons include: dev-only transitive dependency (not in the shipped bundle) or runtime dependency that Mattermost externalizes (e.g. react, redux).

See the [Mattermost plugin developer docs](https://developers.mattermost.com/extend/plugins/) for more information.
