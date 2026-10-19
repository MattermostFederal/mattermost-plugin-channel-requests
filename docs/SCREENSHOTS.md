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
| `team-user-request/` | Team member / guest (screenshots TBD) |
| `bot-request/` | Bot accounts |
| `webhook-request/` | Incoming / outgoing webhooks |
| `admin/` | System Console / domain prefixes |
| `shared/` | App Bar plug icon, Thread RHS Q&A (used by multiple guides) |

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
