# Plugin Template Guidelines

## Overview

This is a minimal Mattermost plugin template. The server is written in Go and the webapp in TypeScript/React.

## Architecture

- `server/` - Go plugin code. Entry point is `main.go` which calls `plugin.ClientMain(&Plugin{})`. The `Plugin` struct in `plugin.go` embeds `plugin.MattermostPlugin` and implements lifecycle hooks (`OnActivate`, `OnConfigurationChange`).
- `webapp/` - TypeScript/React webapp. Entry point is `src/index.tsx`. The `Plugin` class's `initialize()` method receives a `PluginRegistry` and Redux `Store` and is where components and hooks are registered.
- `plugin.json` - plugin manifest. Generates `server/manifest.go` and `webapp/src/manifest.ts` at build time (both gitignored).
- `build/` - build tooling from mattermost-plugin-starter-template (`setup.mk`, `custom.mk`, `manifest/`, `pluginctl/`).
- `assets/` - plugin icon and other static assets bundled at the top level.

## Coding conventions

- Match the style of surrounding code.
- Keep the plugin minimal: avoid adding features or abstractions that are not part of the core template.
- Server: follow Mattermost plugin API conventions. Use `p.API.LogError`/`LogWarn`/`LogInfo` for logging.
- Webapp: prefer functional React components with hooks.

## Build and test

- `make dist` - build the plugin bundle
- `make check-style` - lint both Go and webapp code
- `make test` - run tests
- `make deploy` - build and deploy to a running Mattermost server
