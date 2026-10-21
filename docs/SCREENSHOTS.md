# Screenshot layout

Screenshots live under `docs/images/<topic>/` so each guide can be exported
with its assets as a self-contained folder set. `make dist` copies the tree
into `public/help/images/` for the in-product `/mattermost-request-hub docs`
page (`public/help/docs.html`).

| Folder | Guide / use |
|--------|-------------|
| `channel-request/` | Channel creation |
| `channel-admin-request/` | Channel Admin promotion |
| `team-request/` | New team |
| `team-admin-request/` | Team Admin |
| `team-user-request/` | Team member add + guest invite (`TEAM_MEMBERSHIP_GUIDE.md`) |
| `bot-request/` | Bot accounts |
| `webhook-request/` | Incoming / outgoing webhooks |
| `admin/` | System Console / domain prefixes |
| `shared/` | App Bar plug icon, Request Hub dialog, Thread RHS Q&A (used by multiple guides) |

## Display sizes (markdown + in-app help)

Guides use HTML `<img width="…">` so GitHub / export viewers match
`public/help/styles.css`. Keep these widths when adding shots:

| Shot type | `width` | Examples |
|-----------|---------|----------|
| Menu / more-actions / native / + menu | `240` | `*-menu.png`, `*-more-actions.png`, `*-native*.png` |
| App Bar plug strip | `56` | `mattermost-requests-plug-appbar.png` |
| Request Hub dialog | `440` | `*-hub.png`, `mattermost-request-hub.png` |
| Modal / form | `480` | `*-modal.png` |
| Members-panel crop | `380` | `*-members-button.png` |
| Approval card / DM / welcome / integrations row | `720` | `*-approval-card.png`, `*-dm.png`, `*-welcome.png`, `*-integrations*.png` |
| Tall thread RHS | `300` | `mattermost-thread-approver-ask.png`, `mattermost-thread-relay-back.png` |
| Wide thread DM | `720` | `mattermost-thread-requester-dm.png` |
| Admin settings rail | `280` | `mattermost-admin-settings.png` |
| Admin prefixes | `520` | `mattermost-admin-prefixes.png`, `*-prefix-preview.png` |

Structure each guide like the HTML help pages: **How to open** as one-line
bullets, then a `<p>` row of entry screenshots, then **Form** / **On approve**.

## Capture style

Hub, form, menu, and card shots are **clipped to the popup / menu / attachment**
(not full Mattermost chrome). Thread shots crop the RHS panel. Integrations
shots crop the request tiles.

Re-capture everything (Docker Mattermost on `MM_URL`, plugin enabled):

```bash
# Optional: configure at least one ChannelNamePrefixes line first so the
# channel form is usable (ops-|Operational channels|48).
node scripts/capture-all-doc-screenshots.mjs

# Or one topic: TOPICS=webhook,bot node scripts/capture-all-doc-screenshots.mjs
# Team member/guest only: node scripts/capture-team-user-screenshots.mjs
```

Helpers live in `scripts/lib/screenshot.mjs`.

### `team-user-request/` filenames

| File | Shows |
|------|--------|
| `mattermost-team-member-more-actions.png` | More actions → Request Team Members |
| `mattermost-team-member-hub.png` | Request Hub (dialog only) |
| `mattermost-team-member-modal.png` | Request Team Members form |
| `mattermost-team-member-approval-card.png` | Approval card in `mattermost-team-requests` |
| `mattermost-team-guest-more-actions.png` | More actions → Request Guest Invite |
| `mattermost-team-guest-hub.png` | Request Hub (dialog only) |
| `mattermost-team-guest-modal.png` | Request Guest Invite form |
| `mattermost-team-guest-approval-card.png` | Guest invite approval card |

## Customer export

Copy the matching topic folder plus `shared/` (and `admin/` if the guide
covers System Console). Example for the channel creation guide:

```bash
mkdir -p export/CHANNEL_CREATION_GUIDE/images
cp docs/CHANNEL_CREATION_GUIDE.md export/CHANNEL_CREATION_GUIDE/
cp -R docs/images/channel-request docs/images/shared docs/images/admin \
  export/CHANNEL_CREATION_GUIDE/images/
# Adjust markdown paths if you flatten, or keep images/<topic>/ layout.
```

## Rename refresh (0.5.0)

Docs and `public/help/docs.html` text use **Mattermost Request Hub** /
`/mattermost-request-hub`. PNG captures may still show the previous product
label in UI chrome. After uploading the 0.5.0 bundle to a test server,
re-capture any image that still shows the old name, then re-run `make dist`.
