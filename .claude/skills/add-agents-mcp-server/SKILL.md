---
name: add-agents-mcp-server
description: Add an MCP (Model Context Protocol) server to Channel Requests so the Mattermost Agents plugin can call its tools. Use when exposing the prefix list, channel requests or Channel Admin requests to Agents' LLM agents, or when wiring up the `pluginmcp` helper from mattermost-plugin-agents.
user-invocable: true
allowed-tools: Read, Write, Edit, Bash, Glob, Grep, WebFetch
---

# Add Agents MCP Server

Add a cross-plugin MCP (Model Context Protocol) server to this plugin so the
[Mattermost Agents plugin](https://github.com/mattermost/mattermost-plugin-agents)
can discover and call its tools. Uses the
`github.com/mattermost/mattermost-plugin-agents/v2/external/pluginmcp` helper, which
handles tool-name namespacing, inter-plugin auth, user-ID propagation, and async
registration retries.

MCP is a **third entry point onto operations this plugin already has**,
alongside the `/channel-request` dialog and the webapp modal. Both existing
entry points funnel into `submitRequest` (`server/request.go`) and
`submitAdminRequest` (`server/admin_request.go`), which own validation, team
and channel membership checks, and auto-approval. MCP must funnel into the same
functions. The transport decides who is asking, never what the rules are.

## Read first

- Root [`CLAUDE.md`](../../CLAUDE.md): the conventions.
- `server/http.go`: `ServeHTTP`'s path switch, `requireUserID` and why identity
  must never come from a request body, and `handleWebappCreate` /
  `handleRequestAdmin`, the handlers MCP tools mirror.
- `server/request.go`: `requestInput`, `validateRequestInput`, `submitRequest`,
  and the input bounds (`maxDisplayNameLen`, `maxPurposeLen`,
  `maxMembersPerList`).
- `server/configuration.go`: the configuration pattern, if this ships behind a
  setting.

## Prerequisites

Confirm these before starting; two of them need a decision:

- **`min_server_version` is `10.5.0`** in `plugin.json`. The helper's
  `Plugin.PluginHTTPStream` needs `11.3` or later. **Raising it drops support
  for every server below 11.3, so ask the maintainer first.** Do not hand-edit
  `plugin.json`'s `version`; release-please owns it.
- **Go** is `1.26.3` in the single root `go.mod`. There is no `server/go.mod`;
  the module is `github.com/MattermostFederal/mattermost-plugin-channel-requests`
  and everything under `server/` is `package main`.
- **The plugin id** is `com.mattermostfederal.channel-requests`, reverse-DNS and
  free of `__`. Double-underscore is the namespace separator on the Agents side.
- **`ServeHTTP`** lives in `server/http.go` and dispatches with an exact-match
  `switch r.URL.Path`, falling through to `http.NotFound`.
- **There is no `pluginapi` client.** Tool handlers call `p.API` directly, the
  way every other handler here does.
- **There is no `OnDeactivate`** in `server/plugin.go` today.

## Blocking gate: dependency licensing

The bundle ships to customers, so **verify the license of
`mattermost-plugin-agents` and `modelcontextprotocol/go-sdk` before adding
either**, including anything they pull in transitively. Mattermost's
first-party plugins are not uniformly licensed, and some carry a
source-available license rather than Apache 2.0. Do not assume. Check each
upstream `LICENSE` file, then run `make sbom-audit` to see what the new
dependencies bring in.

If any license is copyleft (GPL, AGPL, SSPL, LGPL, MPL, EPL), source-available,
unclear or unstated: **stop and ask.** Present alternatives and tradeoffs rather
than adding it.

## Blocking gate: which operations become tools

An LLM agent can be steered by prompt injection from any message it reads.
Decide with the maintainer which operations it may perform, and default to the
narrowest set:

| Candidate tool | Wraps | Risk |
|---|---|---|
| `list_prefixes` | `config.prefixes`, as `handlePrefixes` returns them | Read-only, safe |
| `request_channel` | `submitRequest` | Creates a pending request. **For a user in `AutoApproveUserIDs` it creates the channel immediately**, with no human in the loop |
| `request_channel_admin` | `submitAdminRequest` | Creates a pending promotion request |

**Never expose approve or deny.** Those are the human checkpoint this plugin
exists to provide; an agent acting as an approver removes it.

## Instructions

### Phase 0: Prepare a branch

Work on a feature branch (or a worktree, if that is how you work), never on
`main`.

### Phase 1: Add dependencies

Edit the root `go.mod`:

```
require (
    github.com/mattermost/mattermost-plugin-agents/v2 v2.7.0
    github.com/modelcontextprotocol/go-sdk v1.7.0
)
```

Then `go mod tidy` from the repo root.

**The module path carries `/v2`.** The unversioned path still resolves on the
module proxy, but its latest tag (`v1.14.2`) declares
`module github.com/mattermost/mattermost-plugin-ai` and ships no `external/`
directory at all, so it cannot satisfy this import.

Pin the go-sdk to whatever the agents release requires rather than its own
latest tag, so the two halves of the MCP stack agree; `v2.7.0` requires
`v1.7.0`. Check a newer agents release's `go.mod` before bumping either.

If the release you want does not export `external/pluginmcp/`, ask the
maintainer before pinning a pseudo-version or adding a `replace` against a local
checkout. A `replace` must not reach a release build.

### Phase 2: Add MCP server fields to the Plugin struct

In `server/plugin.go`, beside `configurationLock`:

```go
mcpServerLock sync.RWMutex
mcpServer     *pluginmcp.Server
```

The lock guards lazy initialization in `OnActivate` against concurrent reads
from `ServeHTTP`.

### Phase 3: Create `server/mcp.go`

This file owns the MCP server lifecycle. Indirect `pluginmcp.NewServer`,
`Register` and `Unregister` through package-level `var`s so tests can substitute
them without a live Agents plugin.

Follow this repo's error handling: `errors.Wrap` / `errors.New` from
`github.com/pkg/errors`, and `p.API.Log*` calls with an `"error", err.Error()`
pair.

```go
package main

import (
    "net/http"
    "strings"

    "github.com/mattermost/mattermost-plugin-agents/v2/external/pluginmcp"
    "github.com/pkg/errors"
)

const mcpPath = "/mcp"

var (
    mcpNewServer = pluginmcp.NewServer

    mcpRegister = func(server *pluginmcp.Server) error {
        return server.Register()
    }

    mcpUnregister = func(server *pluginmcp.Server) error {
        return server.Unregister()
    }
)

func (p *Plugin) ensureMCPServer() error {
    p.mcpServerLock.Lock()
    defer p.mcpServerLock.Unlock()

    if p.mcpServer != nil {
        return nil
    }

    if manifest.Id == "" || manifest.Version == "" || strings.TrimSpace(manifest.Name) == "" {
        return errors.New("the manifest is missing an id, a version or a name")
    }

    server := mcpNewServer(p.API, pluginmcp.Config{
        PluginID:       manifest.Id,
        Name:           manifest.Name + " MCP",
        Path:           mcpPath,
        ExposeExternal: true,
        Version:        manifest.Version,
    })

    p.registerMCPTools(server)
    p.mcpServer = server
    return nil
}

func (p *Plugin) registerMCPServerBestEffort() {
    server := p.currentMCPServer()
    if server == nil {
        return
    }

    if err := mcpRegister(server); err != nil {
        p.API.LogWarn("MCP registration unavailable; continuing activation", "error", err.Error())
    }
}

func (p *Plugin) unregisterMCPServerBestEffort() {
    server := p.currentMCPServer()
    if server == nil {
        return
    }

    if err := mcpUnregister(server); err != nil {
        p.API.LogWarn("MCP unregister failed; continuing shutdown", "error", err.Error())
    }
}

func (p *Plugin) serveMCPIfMatch(w http.ResponseWriter, r *http.Request) bool {
    if r.URL.Path != mcpPath && !strings.HasPrefix(r.URL.Path, mcpPath+"/") {
        return false
    }

    server := p.currentMCPServer()
    if server == nil {
        http.Error(w, "not ready", http.StatusServiceUnavailable)
        return true
    }

    server.ServeHTTP(w, r)
    return true
}

func (p *Plugin) currentMCPServer() *pluginmcp.Server {
    p.mcpServerLock.RLock()
    defer p.mcpServerLock.RUnlock()

    return p.mcpServer
}
```

`Register()` returns immediately and retries asynchronously (1s, 2s, 4s, 8s, up
to 15 attempts) until the Agents plugin acknowledges, so activation is never
blocked on a plugin that is not up yet.

### Phase 4: Create `server/mcp_tools.go`

**Reuse the operations, do not reimplement them.** A tool builds a
`requestInput` and calls `submitRequest`, exactly as `handleWebappCreate` does,
or calls `submitAdminRequest` as `handleRequestAdmin` does. Membership checks,
input bounds, prefix and suffix validation, the orphaned-channel guard and
auto-approval all live there. A tool that checks any of these itself is a
second copy that will drift.

**Identity comes only from `pluginmcp.GetUserID(ctx)`.** Refuse when it returns
`""`. Never take a requester id from the tool arguments, and never read
`X-Mattermost-UserID` from headers directly. This is the MCP form of the
`requireUserID` rule in `server/http.go`.

Accept usernames for members and admins, and resolve them with
`p.resolveUsernameList`, the way both HTTP handlers do.

Tool names are prefixed automatically. Here `com.mattermostfederal.channel-requests`
sanitizes to `com_mattermostfederal_channel-requests__`, so the LLM sees
`com_mattermostfederal_channel-requests__request_channel` while you write
`request_channel`.

```go
package main

import (
    "context"

    "github.com/mattermost/mattermost-plugin-agents/v2/external/pluginmcp"
    "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/pkg/errors"
)

type RequestChannelArgs struct {
    TeamID       string   `json:"team_id" jsonschema:"The team to create the channel in"`
    Prefix       string   `json:"prefix" jsonschema:"One of the configured domain prefixes, from list_prefixes"`
    DisplayName  string   `json:"display_name" jsonschema:"The channel's display name,minLength=1,maxLength=64"`
    Name         string   `json:"name,omitempty" jsonschema:"The URL suffix after the prefix; generated from display_name when blank"`
    Purpose      string   `json:"purpose,omitempty" jsonschema:"The channel purpose,maxLength=250"`
    ChannelType  string   `json:"channel_type,omitempty" jsonschema:"O for public, P for private"`
    Members      []string `json:"members,omitempty" jsonschema:"Usernames to add as members"`
    AdminMembers []string `json:"admin_members,omitempty" jsonschema:"Usernames to make Channel Admins"`
}

type RequestChannelOutput struct {
    Message string `json:"message" jsonschema:"What happened: the request is pending approval, or the channel was created"`
}

func (p *Plugin) registerMCPTools(server *pluginmcp.Server) {
    pluginmcp.AddTool(server, &mcp.Tool{
        Name:        "request_channel",
        Description: "Request a new channel. The request is posted for admin approval unless the user is configured for auto-approval.",
    }, p.requestChannelTool)
}

func (p *Plugin) requestChannelTool(ctx context.Context, _ *mcp.CallToolRequest, in RequestChannelArgs) (*mcp.CallToolResult, RequestChannelOutput, error) {
    userID := pluginmcp.GetUserID(ctx)
    if userID == "" {
        return nil, RequestChannelOutput{}, errors.New("no user is associated with this request")
    }

    memberIDs, err := p.resolveUsernameList(in.Members)
    if err != nil {
        return nil, RequestChannelOutput{}, err
    }
    adminMemberIDs, err := p.resolveUsernameList(in.AdminMembers)
    if err != nil {
        return nil, RequestChannelOutput{}, err
    }

    message, err := p.submitRequest(requestInput{
        RequesterID:    userID,
        TeamID:         in.TeamID,
        DisplayName:    in.DisplayName,
        Name:           in.Name,
        Prefix:         in.Prefix,
        Purpose:        in.Purpose,
        ChannelType:    in.ChannelType,
        MemberIDs:      memberIDs,
        AdminMemberIDs: adminMemberIDs,
    })
    if err != nil {
        return nil, RequestChannelOutput{}, err
    }

    return nil, RequestChannelOutput{Message: message}, nil
}
```

Handler signature is the go-sdk's `mcp.ToolHandlerFor[In, Out]`:

```
func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error)
```

Return `(nil, out, nil)` and the helper packs `out` into a `CallToolResult`.
Return a non-nil `*mcp.CallToolResult` to control the response fully
(multi-content replies, `IsError`).

Keep the tool count small: every tool costs schema tokens in every LLM request.
The three candidates above are the whole natural surface of this plugin.

### Phase 5: Wire into `OnActivate` and `OnDeactivate`

In `server/plugin.go`, after `EnsureBotUser` and before `RegisterCommand`
returns (change the final `return p.API.RegisterCommand(...)` into an error
check so the MCP setup can follow it):

```go
if err := p.ensureMCPServer(); err != nil {
    return errors.Wrap(err, "failed to initialize the MCP server")
}
p.registerMCPServerBestEffort()
```

Add an `OnDeactivate` that calls `unregisterMCPServerBestEffort` and nothing
else, unconditionally, so a failure elsewhere does not leave a stale
registration on the Agents side.

`Register()` errors are logged and swallowed. A down Agents plugin must never
fail this plugin's activation.

### Phase 6: Route MCP requests in `ServeHTTP`

In `server/http.go`, match MCP **before** the path `switch`. The switch matches
exact paths and its `default` answers 404, so a prefix like `/mcp/...` cannot
live inside it:

```go
func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
    if p.serveMCPIfMatch(w, r) {
        return
    }

    switch r.URL.Path {
```

Do **not** call `requireUserID` or add an auth gate of your own around
`serveMCPIfMatch`. An inter-plugin request carries no `Mattermost-User-Id`, so
`requireUserID` would answer 401. `pluginmcp.Server.ServeHTTP` already rejects
requests without `Mattermost-Plugin-ID: mattermost-ai`, a header Mattermost
strips from external requests, so only inter-plugin RPC can set it.

### Phase 7: Decide on an admin setting

Ask the maintainer whether MCP ships behind an `EnableMCP` setting. If it does,
follow the existing pattern in `server/configuration.go` (a field that is
**false at zero**, read fresh through `p.getConfiguration()` rather than
captured at activation) and add it to `plugin.json`'s
`settings_schema.settings` with `help_text`.

Note that a setting read at registration time will not retract a registration
the Agents plugin already holds until this plugin re-activates. Say so in the
setting's `help_text`.

### Phase 8: Tests

Add `server/mcp_test.go` and `server/mcp_tools_test.go`, using
`plugintest.API`, `newTestPlugin`, `stubLogs` and testify `require`, as the
existing tests do. Name tests in this repo's `TestSubject_Behavior` style.
Cover at least:

- `TestServeHTTP_MCPRouteIsMatchedBeforeTheSwitch`: a POST to `/mcp/...` does
  not get the switch's 404.
- `TestServeHTTP_MCPNotReadyReturns503`.
- `TestRequestChannelTool_RefusesWithoutUserID`, asserting `submitRequest`'s
  API calls (`GetUser`, `KVSet`) never happen.
- `TestRequestChannelTool_UsesContextUserNotArgs`: the requester is the
  `GetUserID` user.
- `TestRequestChannelTool_NonMemberIsRefused`: the team membership check in
  `submitRequest` applies to the MCP path.
- A round-trip through `httptest.Server` with
  `Mattermost-Plugin-ID: mattermost-ai` set, using the go-sdk client for
  `ListTools` and `CallTool`.

Override `mcpNewServer` / `mcpRegister` / `mcpUnregister` rather than reaching
for a live Agents plugin.

### Phase 9: Docs

Run `/add-help-docs`. An agent can now file requests on a user's behalf;
`approvals.html` and `docs/CHANNEL_CREATION_GUIDE.md` should say so, and
`admin.html` should cover any new setting.

### Phase 10: Verify

1. `make check-style`
2. `make test`
3. `make sbom-audit`
4. `make dist`
5. End to end: `make docker-setup` then `make deploy` (Mattermost on `:8065`).
   Install the Agents plugin from a build with cross-plugin MCP support, open
   its System Console **Tools** tab, and confirm the tools appear as
   `com_mattermostfederal_channel-requests__<name>` with per-tool policy
   controls. Tail `make docker-logs` for `Connected to plugin MCP server
   com.mattermostfederal.channel-requests`; its absence means `Register()` never
   succeeded. Ask an agent to request a channel and confirm the approval post
   names the right requester.

Commit with a conventional subject, `feat:` for the feature. No Claude
attribution.

## Configuration reference

```go
type Config struct {
    PluginID       string // required; must equal plugin.json "id"
    Name           string // human-readable; shown in admin UI
    Path           string // this plugin's MCP endpoint, "/mcp"
    ExposeExternal bool   // if true, tools may appear on Agents' external MCP aggregate
                          // (still subject to admin Enabled toggle and per-tool policy)
    Version        string // optional; defaults to "0.0.1"
}
```

## API reference

- `pluginmcp.NewServer(api, cfg) *Server`: `p.API` satisfies the `PluginAPI`
  interface.
- `pluginmcp.AddTool[In, Out](s, tool, handler)`: a free function, not a method,
  because Go disallows type parameters on methods.
- `(*Server).ServeHTTP(w, r)`: an `http.Handler`; route to it for requests under
  `cfg.Path`.
- `(*Server).Register() error`: starts async registration, returns immediately,
  retries in a goroutine.
- `(*Server).Unregister() error`: synchronously cancels pending retries and POSTs
  one unregister.
- `pluginmcp.GetUserID(ctx) string`: returns the user id stashed by `ServeHTTP`,
  or `""` if absent.

## Constraints and gotchas

- **Route above the switch, and without `requireUserID`.** See Phase 6.
- **`GetUserID` is the only user source.** It is trustworthy only inside a
  request that arrived through `pluginmcp.Server.ServeHTTP`; external callers
  cannot inject one. A tool that takes a user id from its arguments is an
  impersonation bug.
- **No approve or deny tools.** See the gate above.
- **Auto-approve users bypass the human.** `request_channel` for a user in
  `AutoApproveUserIDs` creates the channel immediately. Make sure the
  maintainer knows.
- **Tool-name sanitization.** `AddTool` prepends `{sanitizedPluginID}__`,
  replacing any character outside `[A-Za-z0-9_-]` with `_` to satisfy
  `^[a-zA-Z0-9_-]{1,128}$`. An already-prefixed name is not double-prefixed.
- **Do not double-gate auth.** A plugin-id check of your own around
  `serveMCPIfMatch` is redundant and will likely break the helper's.
- **Registration is one-shot per `OnActivate`.** If the Agents plugin restarts,
  admin-persisted entries come back but a never-saved registration returns only
  when this plugin re-activates. Permanent errors log `registration with Agents
  plugin failed permanently` and stop.
- **`ExposeExternal` vs admin `Enabled`.** Each register POST sends
  `expose_external` from `Config`. Admins still control the server's `Enabled`
  state and per-tool policy in the Agents console, and those survive
  re-registration.
- **No webapp half.** MCP is server-only.

## Troubleshooting

- **Tool missing from the admin Tools tab.** Look for `Connected to plugin MCP
  server <pluginID>` in the Agents log. Absence means `Register()` was never
  called or kept failing; the retry loop logs `gave up after N attempts` on
  terminal failure and `failed permanently` on a non-retriable 4xx.
- **`GetUserID` returns `""`.** Either the request did not go through
  `pluginmcp.Server.ServeHTTP` (typical in unit tests, where you inject a context
  yourself), or `ServeHTTP` is not routing to it: check `mcpPath` matches
  `Config.Path`.
- **Registration keeps retrying.** Agents disabled, in a crash loop, or
  `cfg.PluginID` does not match `plugin.json`'s `id` (Agents returns a
  non-retriable 403).
- **404 on a POST to `/mcp/...`.** The match is inside or below the path
  `switch` in `ServeHTTP`. See Phase 6.
- **401 on a POST to `/mcp`.** Something calls `requireUserID` before the MCP
  match.
- **403 during a local curl.** Expected. Mattermost strips
  `Mattermost-Plugin-ID` from external requests. Test through the Agents plugin
  or a unit test that sets the header.

## References

- Helper package: `mattermost-plugin-agents/v2/external/pluginmcp/` (`README.md`,
  `pluginmcp.go`, `server.go`, `tools.go`, `context.go`, `registration.go`).
- Reference implementation: `mattermost-plugin-demo` branch
  `IDEA-006-cross-plugin-mcp`, specifically `server/mcp.go`,
  `server/mcp_tools.go`, `server/activate_hooks.go`, `server/http_hooks.go` and
  `server/mcp_tools_test.go`. Its layout differs from this repo's: it has a
  separate `server/go.mod`, a `pluginapi` client, and split hook files. Take the
  shape, not the paths.
- MCP Go SDK: <https://github.com/modelcontextprotocol/go-sdk>.
