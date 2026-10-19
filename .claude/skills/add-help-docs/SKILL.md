---
name: add-help-docs
description: Use when plugin code changes need to be reflected in the user-facing documentation, the built-in help pages under public/help and the guides under docs/. Trigger after adding or modifying admin settings, the slash command, the request or approval flow, or any user-visible message, or before cutting a release.
user-invocable: true
---

# Add Help Docs

Keep the user-facing documentation in sync with the code.

Channel Requests ships documentation in two places, both edited by hand:

- **`public/help/`**: static HTML bundled with the plugin. Mattermost serves the
  bundle's `public/` directory at `/plugins/<id>/public/**`, so there is no
  route, no renderer and no generator script. The request modal links to
  `help.html` (`webapp/src/RequestChannelModal.tsx`).
- **`docs/`**: Markdown guides read on GitHub, with screenshots in
  `docs/images/`.

## When to Use

- After adding, renaming, or removing an admin setting in `plugin.json`
- After changing the prefix rules, suffix rules or preset packs (`server/configuration.go`, `webapp/src/PrefixEditor.tsx`)
- After changing the `/channel-request` command or how the form opens (`server/command.go`, `webapp/src/channelCreationOverride.ts`, `webapp/src/HeaderIcon.tsx`, `webapp/src/membersPanelButton.ts`)
- After changing the request form (`webapp/src/RequestChannelModal.tsx`, the dialog in `server/http.go`)
- After changing who can approve, what approval does, or what gets posted (`server/request.go`, `server/http.go`)
- After changing the Channel Admin request flow (`server/admin_request.go`, `webapp/src/RequestChannelAdminModal.tsx`)
- After changing any user-visible message text
- Before cutting a release

## When NOT to Use

- Pure refactors with no user-visible change
- Test-only or build-only changes
- Changes already documented in a previous commit on the same branch

## Workflow

Run these steps in order. **Always produce a plan and get explicit user
confirmation before editing any documentation file.**

Before starting, create tasks using TaskCreate for each applicable step. Mark
each complete with TaskUpdate as you finish it.

### 1. Survey what changed

```bash
git diff main...HEAD -- server/ plugin.json webapp/src/ public/ docs/
git log main..HEAD --oneline
```

Focus on: new or renamed settings; changes to prefixes and naming; changes to
the request form, the approval post or the welcome message; changes to who can
request or approve; and any changed string a user can see.

### 2. Produce a plan

Write a short plan and present it. It must list which pages will be edited and
the specific user-visible change driving each edit, plus anything intentionally
left alone and why. Wait for explicit confirmation before continuing.

### 3. Update the pages

Edit only the pages whose scope actually changed. Match the existing tone and
heading structure.

#### Layout

| File | Covers | Kept in sync with |
|---|---|---|
| `public/help/help.html` | Overview, opening the request form, filling it out | `server/command.go`, `webapp/src/RequestChannelModal.tsx`, `webapp/src/channelCreationOverride.ts`, `webapp/src/HeaderIcon.tsx` |
| `public/help/approvals.html` | Who can approve, acting on a request, what happens on approve and deny | `server/decision.go` (`decideChannelRequest`, `decideAdminRequest`), `server/http.go` (`canApprove`), `server/request.go` |
| `public/help/admin.html` | One entry per setting, and the domain prefix rules | `plugin.json` `settings_schema.settings`, `server/configuration.go`, `webapp/src/PrefixEditor.tsx` |
| `public/help/commands.html` | Every `/channel-request` subcommand, the `create` options, their error replies, the Agents tools, and exercise mode with its scenario format | `server/command.go`, `server/command_exercise.go`, `server/exercise.go`, `server/exercise/silver_harbor.json`, `server/mcp_tools.go`, `server/request.go`, `server/admin_request.go`, `server/pending.go`, `server/decision.go`, `server/status.go` |
| `public/help/troubleshooting.html` | One section per symptom: the message, its cause and the fix | Every user-visible error and log warning it quotes |
| `public/help/styles.css` | Shared stylesheet. Rarely changes | |
| `docs/CHANNEL_CREATION_GUIDE.md` | Requesting, approving and setting up new channels, for all three roles | Everything above |
| `docs/CHANNEL_ADMIN_GUIDE.md` | Requesting and approving Channel Admin rights on an existing channel | `server/admin_request.go`, `webapp/src/RequestChannelAdminModal.tsx`, `webapp/src/membersPanelButton.ts` |

The `settings_schema.header` and each setting's `help_text` in `plugin.json`
are documentation too: the System Console shows them. Keep them consistent with
`admin.html`.

#### Change-to-file matrix

| What changed | Pages to update |
|---|---|
| New or renamed setting in `plugin.json` | `admin.html` settings section, and the admin setup section of `docs/CHANNEL_CREATION_GUIDE.md` |
| Prefix, suffix rule or preset pack behavior | `admin.html` domain prefixes section, plus the naming part of `help.html` if requesters see it |
| New way to open the form, or a change to the form | `help.html`, and the requesting section of `docs/CHANNEL_CREATION_GUIDE.md` |
| Who can approve, or what approval or denial does | `approvals.html`, and the approving section of the guide |
| Channel Admin request flow | `docs/CHANNEL_ADMIN_GUIDE.md` |
| New or changed `/channel-request` subcommand or option | `commands.html` (a section with `id="<subcommand>"`), the summary table in `help.html`, and the short list in `docs/CHANNEL_CREATION_GUIDE.md` if the subcommand is new |
| New or changed Agents tool | `commands.html#agents` and the tool list in `troubleshooting.html#agents-missing` |
| New error or warning a user or admin could hit | `troubleshooting.html`, if the fix isn't obvious from the message |
| Changed user-facing message text | Every page that quotes it, verbatim |
| A UI change visible in a screenshot | Note the stale image in `docs/images/` in the report; do not fake a screenshot |

#### Rules

1. **No remote assets and no scripts in `public/help/`.** No CDN, no web fonts,
   no external images, no inline event handlers. These pages must render on an
   air-gapped host and work with scripting off.
2. **Preserve anchor ids and headings other pages link to.** A rename fails
   silently: the browser lands at the top. If you must rename one, update every
   `href` to it, including the links between the two `docs/` guides.
3. **Every help page's sidebar lists every page**, with `class="active"` on its
   own entry and nowhere else.
4. **Quote messages verbatim.** Copy user-facing strings from the Go or TSX
   source rather than paraphrasing, so search finds them.
5. **No em dashes** in `public/help/`, `docs/`, `README.md` or `plugin.json`.
   Use a comma, a colon, a period or parentheses.
6. **Hardcode no plugin id in the webapp.** It builds help links from `manifest.id`.
   The `plugin.json` header link and the Agents tool names on the help pages are
   the exceptions; `server/help_docs_test.go` checks both against the manifest
   and the running MCP server.

#### Adding a help page

1. Copy the shell from an existing page: `<head>`, the sidebar nav, and the
   breadcrumb. Move `class="active"` onto the new entry.
2. Add the new page to the nav block of **all** the other pages. The block is
   identical everywhere except for which entry carries `class="active"`.
3. Add it to `helpPages` in `server/help_docs_test.go`.
4. Link to it from `help.html`.

### 4. Verify

```sh
make check-style
make test
make dist
tar tzf dist/com.mattermostfederal.channel-requests-*.tar.gz | grep public/help
grep -o 'href="[^"#]*' public/help/*.html | sort -u
```

`server/help_docs_test.go` (part of `make test`) checks that the page list
matches `public/help/`, that every sidebar is complete with one active entry,
that every link and `#anchor` resolves, that every subcommand and Agents tool is
documented, that the System Console header links to the docs, that no page
loads a script or remote asset, and that the docs and `plugin.json` contain no
em dashes. It doesn't check the prose, so still open
`public/help/help.html` over `file://` and read what you changed.

### 5. Report

Summarize which pages changed and why, and anything intentionally left alone
(including stale screenshots).

## Common Mistakes

- Skipping the plan step. Always write the plan and get confirmation first.
- Updating `public/help/` but not the matching section of the `docs/` guide, or
  the reverse.
- Changing a setting's behavior without updating its `help_text` in
  `plugin.json`.
- Renaming an anchor or heading another page links to.
- Adding a page but forgetting the other navigation blocks.
- Paraphrasing a user-facing message instead of quoting it.
