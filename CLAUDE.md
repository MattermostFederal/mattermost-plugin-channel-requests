# Channel Requests Plugin Guidelines

## Overview

Mattermost plugin that lets non-admins **request** things for approval instead of creating them directly: channels, teams, Team Admin rights, incoming webhooks, and bot tokens. Each request type has an admin on/off toggle (`RequestEnabled` in `configuration.go`). Webhooks and bot tokens use a two-step (security + system) approval engine keyed on User Attributes. The server is written in Go and the webapp in TypeScript/React.

## Architecture

- `server/` - Go plugin code. Entry point is `main.go` which calls `plugin.ClientMain(&Plugin{})`. The `Plugin` struct in `plugin.go` embeds `plugin.MattermostPlugin` and implements lifecycle hooks (`OnActivate`, `OnConfigurationChange`).
  - Each request type lives in its own file following a parallel pattern (own struct, KV prefix, submit/store/load, approval attachment + actions): `request.go` (channel), `team_request.go`, `admin_request.go` (channel admin), `team_admin_request.go`, `webhook_request.go`, `bot_token_request.go`.
  - Shared pieces: `http.go` (routing + handlers), `command.go` (`/request` command + subcommands, with `/channel-request` kept as an alias), `approvers.go` (User Attribute reads for the two approver pools), `approval_engine.go` (two-step `twoStepState` + `planApproval` decision logic), `rest.go` (Client4 for actions with no plugin API, e.g. incoming webhooks).
- `webapp/` - TypeScript/React webapp. Entry point is `src/index.tsx`. The `Plugin` class's `initialize()` method receives a `PluginRegistry` and Redux `Store` and is where components and hooks are registered. Channel/team requests have modals; team-admin, webhook, and bot-token requests are slash-command-only.
- `plugin.json` - plugin manifest. Generates `server/manifest.go` and `webapp/src/manifest.ts` at build time (both gitignored). Run `make apply` after editing settings.
- `build/` - build tooling from mattermost-plugin-starter-template (`setup.mk`, `custom.mk`, `manifest/`, `pluginctl/`).
- `assets/` - plugin icon and other static assets bundled at the top level.

## Coding conventions

- Match the style of surrounding code.
- Follow the established per-type parallel pattern when adding a request type; share cross-cutting helpers rather than generalizing prematurely.
- Server: follow Mattermost plugin API conventions. Use `p.API.LogError`/`LogWarn`/`LogInfo` for logging. Gate entry points on the server (source of truth); webapp hiding is best-effort/fail-open.
- Webapp: prefer functional React components with hooks.

## Build and test

- `make dist` - build the plugin bundle
- `make check-style` - lint both Go and webapp code
- `make test` - run tests
- `make deploy` - build and deploy to a running Mattermost server
