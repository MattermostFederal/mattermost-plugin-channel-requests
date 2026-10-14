# Mattermost Plugin: Channel Requests

A Mattermost plugin that lets non-admins **request** new channels for System Admin approval instead of creating them directly. On approval, the plugin creates the channel and adds the requester plus any designated members. Admins bypass approval and create channels immediately.

## Features

- **Request workflow**: Non-admins submit a channel request via the `/channel-request` slash command (native dialog with a member picker), a "Request Channel" channel-header button, or the sidebar "+" menu (relabeled "Request new channel" for non-admins).
- **Admin approval**: Requests are posted to a configured approval channel with Approve / Deny buttons. Only System Admins can act. Approving creates the channel and adds the requester + designated members; denying notifies the requester. Either way the requester gets a DM.
- **Admin bypass**: System Admins' requests create the channel immediately.
- **Configurable naming**: Optionally force a standard channel URL via a template (`team-{{name}}`) and/or enforce a regex pattern on the final URL.
- **Channel Admin requests**: On an existing channel, a non-admin member can request that someone be made a Channel Admin — via a "Request Admin" button in the Members panel or the channel-name menu. The request is posted to the approval channel, @-mentioning the System Admins and the channel's Team Admins, who Approve / Deny.

## Documentation

- [Channel Requests — User Guide](docs/CHANNEL_CREATION_GUIDE.md) — requesting and approving **new channels**.
- [Channel Admin Requests — User Guide](docs/CHANNEL_ADMIN_GUIDE.md) — requesting **admin rights on an existing channel**.
- [Releasing](docs/RELEASING.md) — how to cut a release.

## Configuration

In **System Console → Plugins → Channel Requests**, set:

- **Approval Team** — URL name of the team containing the approval channel (e.g. `myteam`).
- **Approval Channel** — URL name of the channel where requests are posted (e.g. `channel-requests`).
- **Channel URL Template** — optional; `{{name}}` is replaced with the requester's slugified name.
- **Channel URL Pattern (regex)** — optional; the final channel URL must match this.

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
